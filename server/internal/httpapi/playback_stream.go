package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/loudness"

	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/commercial"
	"github.com/alreadyonapp/alreadyon/server/internal/guide"
	"github.com/alreadyonapp/alreadyon/server/internal/keyframes"
	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/logging"
	"github.com/alreadyonapp/alreadyon/server/internal/playback"
	"github.com/alreadyonapp/alreadyon/server/internal/profile"
	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
	"github.com/alreadyonapp/alreadyon/server/internal/serversettings"
)

type tuneResponse struct {
	ChannelNumber int              `json:"channelNumber,omitempty"`
	ChannelName   string           `json:"channelName,omitempty"`
	Program       *programResponse `json:"program,omitempty"`
	playback.Session
	Token               string        `json:"token,omitempty"`
	URL                 string        `json:"url"`
	MediaURL            string        `json:"mediaUrl"`
	Plan                playback.Plan `json:"plan"`
	Ready               bool          `json:"ready"`
	ServerTime          time.Time     `json:"serverTime"`
	EntryStartsAt       time.Time     `json:"entryStartsAt"`
	EntryEndsAt         time.Time     `json:"entryEndsAt"`
	SourceOffsetSeconds float64       `json:"sourceOffsetSeconds"`
	StreamOffsetSeconds float64       `json:"streamOffsetSeconds"`
	MediaFileID         int64         `json:"mediaFileId"`
	HLSOrigin           time.Time     `json:"hlsOrigin,omitempty"`
	Kind                string        `json:"kind"`
	// Clients present serverTime minus this delay; entries and stream
	// positions are chosen for that presented moment.
	PresentationDelaySeconds float64 `json:"presentationDelaySeconds"`
	// DurationSeconds is the length of the whole source file, which is what a
	// viewer means by "the episode's length" even when it airs in pieces.
	DurationSeconds float64 `json:"durationSeconds"`
	SourceHeight    int     `json:"sourceHeight"`
	// UHD is true while a hardware encoder is in use, so the Quality menu may offer 4K.
	UHD                  bool `json:"uhd"`
	AutoCapacityPressure bool `json:"autoCapacityPressure,omitempty"`
	AutoQualityCeiling   int  `json:"autoQualityCeiling,omitempty"`
	// Subtitles are the text tracks that can be fetched from the media listener.
	AudioTracks []playback.AudioTrack    `json:"audioTracks"`
	Subtitles   []playback.SubtitleTrack `json:"subtitles"`
	// Details explains how the file became this stream (Show Playback Details).
	Details playback.Details `json:"details"`
	// Runtime contains process-scoped live diagnostics for remux/transcode.
	Runtime playback.RuntimeStats `json:"runtime"`
}

func (p *PlaybackHTTP) tune(w http.ResponseWriter, r *http.Request) {
	release, ok := p.admitPlayback(w, r)
	if !ok {
		return
	}
	defer release()
	if p.Manager == nil {
		writeJSONError(w, 503, "playback is unavailable")
		return
	}
	select {
	case p.tuneSlots <- struct{}{}:
		defer func() { <-p.tuneSlots }()
	default:
		logging.Warn("stream failed: tune request rejected, the server is already handling two tune requests")
		writeJSONError(w, 503, "playback is busy; retry shortly")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, 400, "invalid channel")
		return
	}
	var body struct {
		Capabilities     playback.Capabilities `json:"capabilities"`
		ForceEncode      bool                  `json:"forceEncode"`
		Token            string                `json:"token"`
		ReplacementToken string                `json:"replacementToken"`
		EntryStartsAt    time.Time             `json:"entryStartsAt"`
		// MaxHeight is the viewer's quality ceiling: 0 for automatic, else one of playback.QualityHeights.
		MaxHeight        int    `json:"maxHeight"`
		AudioLanguage    string `json:"audioLanguage"`
		AudioTrackIndex  *int   `json:"audioTrackIndex"`
		AudioMediaFileID int64  `json:"audioMediaFileId"`
		// Optional for older clients without viewer profiles.
		ProfileID *int64 `json:"profileId"`
	}
	if !decodeStrict(w, r, &body) {
		return
	}
	if body.ProfileID != nil {
		if _, err := callerProfile(r, p.DB, *body.ProfileID); err != nil {
			writeProfileError(w, err)
			return
		}
	}
	if body.MaxHeight != 0 && !slices.Contains(playback.QualityHeights, body.MaxHeight) {
		writeJSONError(w, 400, "maxHeight must be 0, 240, 360, 480, 720, 1080 or 2160")
		return
	}
	if body.Capabilities.AutoMaxHeight != 0 && !playback.ValidAutoHeight(body.Capabilities.AutoMaxHeight) {
		writeJSONError(w, 400, "invalid Auto quality ceiling")
		return
	}
	client := body.Capabilities.ClientKind
	if client != "" && client != "phone" && client != "tv" && client != "web" {
		writeJSONError(w, 400, "invalid playback client kind")
		return
	}
	floor, start, maximum := body.Capabilities.ClientQuality()
	if client != "" {
		if body.MaxHeight > 0 {
			body.MaxHeight = min(maximum, max(floor, body.MaxHeight))
		} else if body.Capabilities.AutoMaxHeight == 0 {
			body.Capabilities.AutoMaxHeight = start
		} else {
			body.Capabilities.AutoMaxHeight = min(maximum, max(floor, body.Capabilities.AutoMaxHeight))
		}
	}
	adaptive := body.MaxHeight == 0 && body.Capabilities.AutoMaxHeight > 0
	if len(body.AudioLanguage) > 32 {
		writeJSONError(w, 400, "audioLanguage is too long")
		return
	}
	body.Capabilities.MaxHeight = body.MaxHeight
	settings, err := playback.GetSettings(r.Context(), p.DB)
	if err != nil {
		writeJSONError(w, 500, "could not load playback limits")
		return
	}
	// Adaptive Auto requests one rendition at a time to leave room for other viewers.
	body.Capabilities.Ladder = settings.Ladder && (body.MaxHeight != 0 || body.Capabilities.AutoMaxHeight == 0)
	// A hardware encoder is used only while its self-test passes.
	encoder, device, hardwareDecoding := p.defaultHardwareEncoder(r.Context())
	body.Capabilities.Encoder = encoder
	label := channelLabel(r.Context(), p.DB, id)
	e, jobClock, err := playback.TuneEntry(r.Context(), p.DB, p.Clock, id, body.EntryStartsAt)
	if err != nil {
		logging.Warn("stream failed: %s: nothing playable is scheduled: %v", label, err)
		writeJSONError(w, 404, "nothing playable is scheduled")
		return
	}
	path, source, err := playback.LoadSource(r.Context(), p.DB, e.MediaFileID)
	if err != nil {
		logging.Warn("stream failed: %s: media file %d is missing, corrupt, or outside the library: %v", label, e.MediaFileID, err)
		code := "source_unavailable"
		message := "The stream could not play. The video file is unreadable, corrupt, or outside the configured library."
		if errors.Is(err, os.ErrNotExist) {
			code = "source_missing"
			message = "The stream could not play. The video file is missing."
		}
		writeJSON(w, 422, map[string]any{"error": message, "code": code, "retryAt": e.EndsAt, "serverTime": p.Clock.Now()})
		return
	}
	// Copied video is cut only at keyframes, so planning needs their spacing. Only
	// H.264 is ever copied, so other codecs skip the lookup.
	if video, _ := playback.Tracks(source); video.Codec == "h264" {
		source.KeyframeGap = keyframes.ForTune(r.Context(), p.DB, e.MediaFileID, path, source.DurationSeconds)
	}
	// Track indices are local to a file; an upcoming entry must never inherit
	// an explicit index from the preceding program or commercial.
	explicit := body.AudioTrackIndex
	if explicit != nil && body.AudioMediaFileID <= 0 {
		writeJSONError(w, 400, "audioMediaFileId is required for audio track selection")
		return
	}
	if body.AudioMediaFileID != e.MediaFileID {
		explicit = nil
	}
	source, err = playback.SelectAudio(source, body.AudioLanguage, explicit)
	if err != nil {
		writeJSONError(w, 400, err.Error())
		return
	}
	// Browser track APIs are not portable. Multi-audio files use a server-selected
	// HLS stream so native file playback cannot silently choose another track.
	body.Capabilities.SelectAudio = len(playback.AudioTracks(source)) > 1
	if string(e.Kind) == "commercial" {
		commercials, err := commercial.GetSettings(r.Context(), p.DB)
		if err != nil {
			writeJSONError(w, 500, "could not load commercial volume")
			return
		}
		body.Capabilities.VolumeOffsetDB = commercials.VolumeOffsetDB
	}
	// Use the measured source-specific pipeline; API preference is only the
	// initial candidate. Re-check failures before applying cached choices.
	desiredHeight := body.MaxHeight
	if desiredHeight == 0 {
		desiredHeight = body.Capabilities.AutoMaxHeight
	}
	preferred, hasPreferred := p.Capacity.PreferredPipeline(source, body.Capabilities.Encoder, desiredHeight)
	if hasPreferred && p.Hardware != nil && preferred.Encoder != "" {
		status := p.Hardware.Check(r.Context(), preferred.Encoder, false)
		if !status.OK {
			hasPreferred = false
		} else {
			body.Capabilities.Encoder, device = preferred.Encoder, status.Device
			hardwareDecoding = preferred.HardwareDecode
			body.Capabilities.HWToneMapMethod = preferred.ToneMapper
		}
	}

	// GPU decoding, scaling and tone mapping, when switched on, suit sources the GPU can decode.
	if video, _ := playback.Tracks(source); hardwareDecoding && p.Hardware.DecodeAllowed(body.Capabilities.Encoder, video) && p.Hardware.DecodeReady(r.Context(), body.Capabilities.Encoder) {
		body.Capabilities.HWDecode = true
		body.Capabilities.HWToneMap = p.Hardware.ToneMapOnGPU(r.Context(), body.Capabilities.Encoder)
		if hasPreferred {
			body.Capabilities.HWToneMap = body.Capabilities.HWToneMap && (preferred.ToneMapper == "gpu" || preferred.ToneMapper == "opencl")
		}
	}
	// QSV silently skips VPP tone mapping without static HDR metadata. Prefer
	// the compiled OpenCL path in that case; driver failures retain CPU fallback.
	if video, _ := playback.Tracks(source); body.Capabilities.Encoder == playback.EncoderIntelQSV && !video.HDRStaticMetadata && body.Capabilities.HWToneMap {
		body.Capabilities.HWToneMapMethod = "opencl"
	}
	video, _ := playback.Tracks(source)
	if height, fps, measured, sustainable := p.Capacity.LimitFor(body.Capabilities.Encoder, video); measured {
		body.Capabilities.ServerMaxHeight, body.Capabilities.ServerMaxFPS = height, fps
		body.Capabilities.ServerMeasured, body.Capabilities.ServerSustainable = true, sustainable
	}
	threads := playback.DefaultThreads
	if p.Tuner != nil {
		threads = p.Tuner.Threads()
	}
	threads = p.Capacity.ProfileThreads(source, body.Capabilities.Encoder, body.Capabilities.HWDecode, threads)
	if height, fps, measured, sustainable := p.Capacity.HDRLimit(body.Capabilities.Encoder, body.Capabilities.HWDecode, video, threads); measured {
		body.Capabilities.ServerMaxHeight, body.Capabilities.ServerMaxFPS = height, fps
		body.Capabilities.ServerMeasured, body.Capabilities.ServerSustainable = true, sustainable
		body.Capabilities.HDRMeasured = true
	}
	// Limits must describe the selected tone mapper/API, not the first HDR
	// record from a different candidate that happened to be benchmarked earlier.
	if hasPreferred {
		body.Capabilities.ServerMaxHeight, body.Capabilities.ServerMaxFPS = preferred.Height, preferred.FPS
		body.Capabilities.ServerMeasured, body.Capabilities.ServerSustainable = true, true
		body.Capabilities.HDRMeasured = playback.IsHDRTransfer(video.ColorTransfer)
		threads = preferred.Threads
	}
	_, audioTrack := playback.Tracks(source)
	normalization, err := loudness.Lookup(r.Context(), p.DB, e.MediaFileID, path, audioTrack.Index)
	if err != nil {
		writeJSONError(w, 500, "could not load audio measurements")
		return
	}
	body.Capabilities.Normalization = normalization
	offset := jobClock.Now().Sub(e.StartsAt).Seconds() + e.SourceOffsetSeconds
	plan, err := playback.ChooseScheduled(source, body.Capabilities, body.ForceEncode, offset)
	if err == nil && adaptive && !plan.VideoCopy {
		if ceiling := p.Manager.AutoQualityCeiling(); ceiling > 0 && ceiling < body.Capabilities.AutoMaxHeight {
			body.Capabilities.AutoMaxHeight = max(floor, ceiling)
			plan, err = playback.ChooseScheduled(source, body.Capabilities, body.ForceEncode, offset)
		}
	}
	if err != nil {
		logging.Warn("stream failed: %s: %s: %v", label, filepath.Base(path), err)
		if writeCapacityFailure(w, err) {
			return
		}
		code := "playback_incompatible"
		// Only absent baseline capabilities justify browser setup advice. Other
		// planning failures can reflect bandwidth, source metadata or HDR limits.
		_, audio := playback.Tracks(source)
		hasVideo := slices.ContainsFunc(body.Capabilities.Video, func(v playback.VideoCapability) bool { return v.Codec == "h264" })
		hasAudio := audio.Index < 0 || slices.ContainsFunc(body.Capabilities.Audio, func(a playback.AudioCapability) bool { return a.Codec == "aac" })
		if errors.Is(err, playback.ErrUnsupported) && (!body.Capabilities.HLS || !hasVideo || !hasAudio) {
			code = "client_codec_unsupported"
		}
		writeJSON(w, 422, map[string]any{"error": err.Error(), "code": code, "retryAt": e.EndsAt, "serverTime": p.Clock.Now()})
		return
	}
	if missing := playback.MissingDecoders(plan, source); len(missing) > 0 {
		logging.Warn("stream failed: %s: %s needs FFmpeg to decode %s, which this FFmpeg build cannot", label, filepath.Base(path), strings.Join(missing, ", "))
		code := "server_decoder_missing"
		message := "The stream could not play. The server is missing the required codecs to decode this file. Contact the server owner."
		// The official image supplies this immutable stamp and its full media stack.
		// Native development needs codecs on the host, not in the viewing browser.
		if _, err := os.Stat("/usr/local/share/alreadyon/build-version"); errors.Is(err, os.ErrNotExist) {
			code = "native_server_decoder_missing"
			message = "The stream could not play. The local development server is missing the required codecs to decode this file."
		}
		writeJSON(w, 422, map[string]any{"error": message, "code": code, "missingCodecs": missing, "retryAt": e.EndsAt, "serverTime": p.Clock.Now()})
		return
	}
	job, err := playback.ScheduledJob(e, jobClock, path, source, plan, "")
	if err != nil {
		logging.Warn("stream failed: %s: schedule changed while tuning: %v", label, err)
		writeJSONError(w, 409, "schedule changed; tune again")
		return
	}
	if adaptive {
		job.AutoQualityMax = maximum
	}
	job.Burst = playback.ReadBurst(p.Clock.Now(), jobClock.Now())
	job.Device = device
	if p.Tuner != nil {
		job.Threads = p.Tuner.Threads()
	}
	if hasPreferred && preferred.Encoder == plan.Encoder && preferred.HardwareDecode == (plan.Decoder == "hardware") {
		job.Threads = preferred.Threads
	} else {
		job.Threads = p.Capacity.ProfileThreads(source, plan.Encoder, plan.Decoder == "hardware", job.Threads)
	}
	job.ValidatedThreads = p.Capacity.ValidatedThreads(job)
	job.Details = playback.Describe(path, source, plan, body.Capabilities)
	if err := p.Manager.Configure(playback.EffectiveSettings(settings, p.Capacity)); err != nil {
		writeJSONError(w, 500, "invalid playback limits")
		return
	}
	key := streamKey(id, e, plan)
	token := body.Token
	if token == "" {
		token, _, err = p.Sessions.Issue(auth.PrincipalID(r.Context()), id)
		if err != nil {
			writeJSONError(w, 503, "playback session capacity reached")
			return
		}
	}
	var userID int64
	maxStreams := 0
	if u, ok := auth.UserFrom(r.Context()); ok {
		userID, maxStreams = u.ID, u.MaxStreams
	}
	s, err := p.Sessions.ClaimFor(token, auth.PrincipalID(r.Context()), id, userID, maxStreams)
	if errors.Is(err, playback.ErrStreamLimit) {
		p.Sessions.Revoke(token)
		logging.Warn("stream refused: %s: account stream limit (%d) reached", label, maxStreams)
		writeJSONCode(w, http.StatusTooManyRequests, "stream_limit",
			fmt.Sprintf("This account can watch on %d device(s) at once. Stop playback on another device and try again.", maxStreams))
		return
	}
	if err != nil {
		writeJSONError(w, 409, "playback authorization was cancelled or already used")
		return
	}
	replace, replaceErr := p.replacementSession(body.Capabilities, body.ReplacementToken, auth.PrincipalID(r.Context()), id)
	if replaceErr != nil {
		p.Sessions.Revoke(token)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "playback replacement is no longer available", "code": "playback_replacement_invalid"})
		return
	}
	var stream playback.ManagedStream
	if replace != nil {
		stream, err = p.Manager.AcquireReplacement(r.Context(), key, s.ID, replace.StreamKey, replace.ID, job)
	} else {
		stream, err = p.Manager.Acquire(r.Context(), key, s.ID, job)
	}
	// Keep the same grant while trying cheaper measured rungs on an initial tune.
	initialAuto := adaptive && !body.Capabilities.AutoReplacement && !plan.VideoCopy
	capacityDenied := adaptive && !plan.VideoCopy && errors.Is(err, playback.ErrBusy)
	if errors.Is(err, playback.ErrBusy) && initialAuto {
		caps := body.Capabilities
		lastHeight := playback.PlanQualityHeight(plan)
		for i := len(playback.QualityHeights) - 1; i >= 0; i-- {
			height := playback.QualityHeights[i]
			if height < floor || height >= lastHeight {
				continue
			}
			caps.AutoMaxHeight = height
			low, planErr := playback.ChooseScheduled(source, caps, body.ForceEncode, offset)
			if planErr != nil || low.VideoCopy || playback.PlanQualityHeight(low) >= lastHeight {
				continue
			}
			lastHeight = playback.PlanQualityHeight(low)
			candidate := job
			candidate.Plan = low
			candidate.ValidatedThreads = p.Capacity.ValidatedThreads(candidate)
			if !candidate.ValidatedThreads {
				continue
			}
			candidate.Details = playback.Describe(path, source, low, caps)
			lowKey := streamKey(id, e, low)
			if replacement, acquireErr := p.Manager.Acquire(r.Context(), lowKey, s.ID, candidate); acquireErr == nil {
				stream, key, err = replacement, lowKey, nil
				break
			}
		}
	}
	if capacityDenied && (body.EntryStartsAt.IsZero() || replace != nil) {
		p.Manager.MarkAutoCapacityPressure(body.Capabilities.AutoMaxHeight, floor)
	}
	if err != nil {
		p.Sessions.Revoke(token)
		// Capacity is expected under rapid channel changes and retried by the client.
		logging.Warn("stream failed: %s: %s: %v", label, filepath.Base(path), err)
		if writeCapacityFailure(w, err) {
			return
		}
		if errors.Is(err, playback.ErrBusy) {
			w.Header().Set("Retry-After", "1")
			writeJSON(w, 503, map[string]string{"error": "stream capacity reached; retry shortly", "code": "stream_capacity"})
		} else {
			writeJSONError(w, 503, "could not start playback; retry shortly")
		}
		return
	}
	if err := p.Sessions.Bind(token, key); err != nil {
		p.Manager.Release(key, s.ID)
		p.Sessions.Revoke(token)
		logging.Warn("stream failed: %s: could not attach playback session: %v", label, err)
		writeJSONError(w, 503, "could not attach playback session")
		return
	}
	s.StreamKey = key
	if adaptive && replace != nil {
		p.Manager.ObserveAutoDownshift(replace.StreamKey, replace.ID, key, s.ID)
	}
	response, err := p.tuneResult(r, s, token, stream)
	if err != nil || r.Context().Err() != nil {
		p.Manager.Release(key, s.ID)
		p.Sessions.Revoke(token)
		writeJSONError(w, 503, "tune cancelled")
		return
	}
	// Save before acknowledging the tune: leaving the page immediately must
	// not depend on a later browser request or activity heartbeat.
	if body.ProfileID != nil {
		if err := profile.RememberChannel(r.Context(), p.DB, *body.ProfileID, id); err != nil {
			p.Manager.Release(key, s.ID)
			p.Sessions.Revoke(token)
			writeProfileError(w, err)
			return
		}
	}
	logging.Info("stream started: %s, %s %q (%s: %s)", label, e.Kind, filepath.Base(path), plan.Mode, plan.Reason)
	writeJSON(w, 201, response)
}

func writeCapacityFailure(w http.ResponseWriter, err error) bool {
	code, message := "", ""
	switch {
	case errors.Is(err, playback.ErrCapacityMeasurementUnavailable):
		code = "server_capacity_measurement_unavailable"
		message = "The server has no current capacity measurement for this video pipeline, so it could not safely start the conversion."
	case errors.Is(err, playback.ErrCapacityUnsustainable):
		code = "server_capacity_unsustainable"
		message = "The server's measured transcoding pipeline cannot sustain this output quality. Choose a lower quality or try again after capacity is remeasured."
	default:
		return false
	}
	writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": message, "code": code})
	return true
}

func (p *PlaybackHTTP) replacementSession(capabilities playback.Capabilities, token string, principalID, channelID int64) (*playback.Session, error) {
	if !capabilities.AutoReplacement {
		return nil, nil
	}
	session, err := p.Sessions.Validate(token, channelID)
	if err != nil || session.KeyID != principalID || session.StreamKey == "" {
		return nil, playback.ErrUnauthorized
	}
	return &session, nil
}

func channelLabel(ctx context.Context, db *sql.DB, id int64) string {
	var number int
	var name string
	if db.QueryRowContext(ctx, `SELECT number, name FROM channels WHERE id=?`, id).Scan(&number, &name) != nil {
		return fmt.Sprintf("channel id %d", id)
	}
	return fmt.Sprintf("channel %d %q", number, name)
}

// streamKey identifies a shareable rendition. Regeneration can keep an entry's
// media and start while changing its split; such entries must not share a
// worker with a stale offset or duration.
func streamKey(channelID int64, e schedule.Entry, plan playback.Plan) string {
	keyData, _ := json.Marshal(struct {
		Channel, Media, Start, End int64
		SourceOffset               float64
		Plan                       playback.Plan
	}{channelID, e.MediaFileID, e.StartsAt.Unix(), e.EndsAt.Unix(), e.SourceOffsetSeconds, plan})
	sum := sha256.Sum256(keyData)
	return hex.EncodeToString(sum[:])
}

func (p *PlaybackHTTP) tuneResult(r *http.Request, s playback.Session, token string, stream playback.ManagedStream) (tuneResponse, error) {
	base, err := p.mediaBase(r)
	if err != nil {
		return tuneResponse{}, err
	}
	j := stream.Job
	ready := j.Plan.Mode == "direct"
	name := "source"
	var origin time.Time
	if !ready {
		name = "index.m3u8"
		origin, err = playback.HLSOrigin(j.OutputDir)
		ready = err == nil
	}
	// Display metadata is best effort: a lookup failure must not interrupt playback.
	c, _ := channel.Get(r.Context(), p.DB, s.ChannelID)
	var program *programResponse
	if resolved, err := guide.ForEntry(r.Context(), p.DB, j.Entry); err == nil {
		value := toProgramResponse(resolved)
		program = &value
	}
	ceiling := 0
	if !j.Plan.VideoCopy {
		ceiling = p.Manager.AutoQualityCeiling()
	}
	return tuneResponse{ChannelNumber: c.Number, ChannelName: c.Name, Program: program, Session: s, Token: token, MediaURL: fmt.Sprintf("%s/media/%d", base, s.ChannelID), URL: fmt.Sprintf("%s/media/%d/%s?token=%s", base, s.ChannelID, name, url.QueryEscape(token)), HLSOrigin: origin, Plan: j.Plan, Ready: ready, ServerTime: p.Clock.Now(), EntryStartsAt: j.Entry.StartsAt, EntryEndsAt: j.Entry.EndsAt, SourceOffsetSeconds: j.Entry.SourceOffsetSeconds, StreamOffsetSeconds: j.Offset.Seconds(), MediaFileID: j.Entry.MediaFileID, Kind: string(j.Entry.Kind), PresentationDelaySeconds: playback.PresentationDelay.Seconds(), DurationSeconds: j.Source.DurationSeconds, SourceHeight: sourceHeight(j.Source), UHD: p.hardwareEncoderWorks(r.Context()), AudioTracks: playback.AudioTracks(j.Source), Subtitles: playback.SourceSubtitles(r.Context(), p.DB, j.Path, j.Source), Details: j.Details, Runtime: stream.Runtime, AutoCapacityPressure: ceiling > 0 && playback.PlanQualityHeight(j.Plan) > ceiling, AutoQualityCeiling: ceiling}, nil
}

func sourceHeight(src library.ProbeResult) int {
	v, _ := playback.Tracks(src)
	return v.Height
}

func (p *PlaybackHTTP) playbackStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token     string `json:"token"`
		ChannelID int64  `json:"channelId"`
	}
	if !decodeStrict(w, r, &body) {
		return
	}
	s, err := p.Sessions.Validate(body.Token, body.ChannelID)
	if err != nil || s.KeyID != auth.PrincipalID(r.Context()) || s.StreamKey == "" {
		writeJSONError(w, 401, "invalid playback session")
		return
	}
	stream, err := p.Manager.Touch(s.StreamKey, s.ID)
	if err != nil {
		logging.Warn("stream failed: %s: the stream is no longer running (it ended, failed, or the viewer's lease expired)", channelLabel(r.Context(), p.DB, body.ChannelID))
		writeJSONError(w, 410, "stream stopped; tune again")
		return
	}
	s, err = p.Sessions.Renew(body.Token, auth.PrincipalID(r.Context()), body.ChannelID)
	if err != nil {
		writeJSONError(w, 401, "playback session ended")
		return
	}
	response, err := p.tuneResult(r, s, body.Token, stream)
	if err != nil {
		writeJSONError(w, 500, "could not resolve media address")
		return
	}
	writeJSON(w, 200, response)
}

func (p *PlaybackHTTP) mediaStream(w http.ResponseWriter, r *http.Request) (playback.ManagedStream, bool) {
	id, _ := strconv.ParseInt(r.PathValue("channel"), 10, 64)
	s, err := p.Sessions.Validate(r.URL.Query().Get("token"), id)
	if err != nil || s.StreamKey == "" || p.Manager == nil {
		writeJSONError(w, 401, "invalid playback session")
		return playback.ManagedStream{}, false
	}
	stream, err := p.Manager.Touch(s.StreamKey, s.ID)
	if err != nil {
		writeJSONError(w, 410, "stream is no longer available")
		return stream, false
	}
	return stream, true
}
func (p *PlaybackHTTP) mediaSource(w http.ResponseWriter, r *http.Request) {
	s, ok := p.mediaStream(w, r)
	if !ok {
		return
	}
	if s.Job.Plan.Mode != "direct" {
		http.NotFound(w, r)
		return
	}
	f, err := playback.OpenSource(r.Context(), p.DB, s.Job.Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// A single range is enough for browser seeking; avoid multipart amplification.
	if strings.Contains(r.Header.Get("Range"), ",") {
		writeJSONError(w, 416, "multiple ranges are not supported")
		return
	}
	// Library bytes are media, never active HTML, even when a malformed file
	// has an HTML filename or payload. Preserve byte ranges for direct playback.
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(&idleWriter{w, http.NewResponseController(w)}, r, info.Name(), info.ModTime(), f)
}

// mediaWriteIdle bounds a stalled client without cutting off a long direct
// file: browsers stop reading once their buffer is full, sometimes for minutes
// of a feature-length program, so only idle time is limited, per write.
var mediaWriteIdle = time.Minute

type idleWriter struct {
	http.ResponseWriter
	rc *http.ResponseController
}

func (w *idleWriter) Write(p []byte) (int, error) {
	_ = w.rc.SetWriteDeadline(time.Now().Add(mediaWriteIdle))
	return w.ResponseWriter.Write(p)
}

// A ladder stream names segments per rung (segment-1-000000012.ts) and adds one
// playlist per rung (stream-1.m3u8) below the master index.m3u8.
var (
	segmentName  = regexp.MustCompile(`^segment-(?:[0-9]-)?[0-9]{9}\.ts$`)
	variantName  = regexp.MustCompile(`^stream-[0-9]\.m3u8$`)
	playlistName = regexp.MustCompile(`^(?:segment-(?:[0-9]-)?[0-9]{9}\.ts|stream-[0-9]\.m3u8)$`)
)

func (p *PlaybackHTTP) mediaManifest(w http.ResponseWriter, r *http.Request) {
	s, ok := p.mediaStream(w, r)
	if !ok {
		return
	}
	if s.Job.Plan.Mode == "direct" {
		http.NotFound(w, r)
		return
	}
	servePlaylist(w, r, s.Job.OutputDir, "index.m3u8")
}

// servePlaylist returns a playlist whose entries (segments or, for a ladder's
// master, variant playlists) carry the caller's short-lived token.
func servePlaylist(w http.ResponseWriter, r *http.Request, dir, file string) {
	raw, err := os.ReadFile(filepath.Join(dir, file))
	if errors.Is(err, os.ErrNotExist) {
		w.Header().Set("Retry-After", "1")
		writeJSONError(w, 503, "stream is starting")
		return
	}
	if err != nil || len(raw) > 65536 {
		writeJSONError(w, 500, "playlist unavailable")
		return
	}
	lines := strings.Split(string(raw), "\n")
	for i, line := range lines {
		if line != "" && !strings.HasPrefix(line, "#") {
			if !playlistName.MatchString(line) {
				writeJSONError(w, 500, "invalid playlist")
				return
			}
			lines[i] = line + "?token=" + url.QueryEscape(r.URL.Query().Get("token"))
		}
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	_, _ = w.Write([]byte(strings.Join(lines, "\n")))
}
func (p *PlaybackHTTP) mediaSegment(w http.ResponseWriter, r *http.Request) {
	s, ok := p.mediaStream(w, r)
	if !ok {
		return
	}
	name := r.PathValue("segment")
	if s.Job.Plan.Mode == "direct" {
		http.NotFound(w, r)
		return
	}
	if variantName.MatchString(name) {
		servePlaylist(w, r, s.Job.OutputDir, name)
		return
	}
	if !segmentName.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "video/mp2t")
	http.ServeFile(w, r, filepath.Join(s.Job.OutputDir, name))
}

// mediaSubtitles serves one text subtitle track of the stream's source as cues
// in source-file time. A track that is still being extracted answers 202.
func (p *PlaybackHTTP) mediaSubtitles(w http.ResponseWriter, r *http.Request) {
	s, ok := p.mediaStream(w, r)
	if !ok {
		return
	}
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	tracks := playback.SubtitleTracks(s.Job.Source)
	if index < 0 {
		tracks = playback.SourceSubtitles(r.Context(), p.DB, s.Job.Path, s.Job.Source)
	}
	match := slices.IndexFunc(tracks, func(t playback.SubtitleTrack) bool { return t.Index == index })
	if match < 0 {
		http.NotFound(w, r)
		return
	}
	var cues []playback.Cue
	var state playback.SubtitleState
	if index < 0 {
		cues, state = p.Subtitles.GetSidecar(tracks[match])
	} else {
		path, err := playback.AllowedPath(r.Context(), p.DB, s.Job.Path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		info, err := os.Stat(path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		cues, state = p.Subtitles.Get(path, info.ModTime().UnixNano(), index)
	}
	switch state {
	case playback.SubtitlePending:
		w.Header().Set("Retry-After", "2")
		writeJSONError(w, http.StatusAccepted, "subtitles are being prepared")
	case playback.SubtitleFailed:
		writeJSONError(w, http.StatusInternalServerError, "could not read that subtitle track")
	default:
		if cues == nil {
			cues = []playback.Cue{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"cues": cues})
	}
}

// defaultHardwareEncoder is the healthy starting backend before playback
// selects a measured pipeline for the particular source.
func (p *PlaybackHTTP) defaultHardwareEncoder(ctx context.Context) (encoder, device string, decode bool) {
	if p.Hardware != nil && p.DB != nil {
		if settings, err := serversettings.Get(ctx, p.DB); err == nil {
			encoder := p.Hardware.Resolve(ctx, settings.Transcoding, settings.HardwareDecoding)
			if playback.IsHardwareEncoder(encoder) {
				if status := p.Hardware.Check(ctx, encoder, false); status.OK {
					return encoder, status.Device, settings.HardwareDecoding
				}
			}
		}
	}
	return "", "", false
}

// hardwareEncoderWorks reports whether hardware output can offer 4K.
func (p *PlaybackHTTP) hardwareEncoderWorks(ctx context.Context) bool {
	encoder, _, _ := p.defaultHardwareEncoder(ctx)
	return encoder != ""
}
