package httpapi

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/alreadyonapp/alreadyon/server/internal/activity"
	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"github.com/alreadyonapp/alreadyon/server/internal/playback"
	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
	"github.com/alreadyonapp/alreadyon/server/internal/status"
)

const activityTTL = 35 * time.Second

type viewerActivity struct {
	ended                   bool
	ID                      string                 `json:"id"`
	Channel                 string                 `json:"channel"`
	Video                   string                 `json:"video"`
	Program                 string                 `json:"program"`
	User                    string                 `json:"user"`
	VideoReason             string                 `json:"videoReason"`
	AudioReason             string                 `json:"audioReason"`
	Profile                 string                 `json:"profile"`
	SourceIP                string                 `json:"sourceIp"`
	Device                  string                 `json:"device"`
	AppVersion              string                 `json:"appVersion"`
	Mode                    string                 `json:"mode"`
	Encoder                 string                 `json:"encoder"`
	SourceVideo             playback.VideoDetails  `json:"sourceVideo"`
	OutputVideo             playback.VideoDetails  `json:"outputVideo"`
	SourceAudio             *playback.AudioDetails `json:"sourceAudio,omitempty"`
	OutputAudio             *playback.AudioDetails `json:"outputAudio,omitempty"`
	BandwidthMbps           *float64               `json:"bandwidthMbps"`
	LatencyMS               *float64               `json:"latencyMs"`
	Commercial              bool                   `json:"commercial"`
	Paused                  bool                   `json:"paused"`
	StartedAt               time.Time              `json:"startedAt"`
	PlaybackPositionSeconds *float64               `json:"playbackPositionSeconds,omitempty"`
	PlaybackDurationSeconds *float64               `json:"playbackDurationSeconds,omitempty"`
	UpdatedAt               time.Time              `json:"updatedAt"`
	principal               int64
	channelID               int64
	sessionID, streamKey    string
	bytes                   int64
	sampleAt                time.Time
	lastSeen                time.Time
	programPositionSeconds  float64
	programPositionAt       time.Time
}

// Activity is tied to a player instance, not an FFmpeg rendition or a prefetch.
func (p *PlaybackHTTP) reportActivity(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ViewerID   string   `json:"viewerId"`
		Token      string   `json:"token"`
		ChannelID  int64    `json:"channelId"`
		ProfileID  int64    `json:"profileId"`
		LatencyMS  *float64 `json:"latencyMs"`
		Paused     bool     `json:"paused"`
		AppVersion string   `json:"appVersion"`
	}
	if !decodeStrict(w, r, &b) {
		return
	}
	if len(b.AppVersion) > 100 || strings.ContainsFunc(b.AppVersion, unicode.IsControl) || len(b.ViewerID) < 16 || len(b.ViewerID) > 80 || (b.LatencyMS != nil && (math.IsNaN(*b.LatencyMS) || math.IsInf(*b.LatencyMS, 0) || *b.LatencyMS < 0 || *b.LatencyMS > 60000)) {
		writeJSONError(w, 400, "invalid activity report")
		return
	}
	s, err := p.Sessions.Validate(b.Token, b.ChannelID)
	principal := auth.PrincipalID(r.Context())
	if err != nil || s.KeyID != principal || p.Manager == nil || s.StreamKey == "" {
		writeJSONError(w, 401, "invalid playback session")
		return
	}
	stream, err := p.Manager.Inspect(s.StreamKey, s.ID)
	if err != nil {
		writeJSONError(w, 410, "stream stopped")
		return
	}
	profileName := "Unknown"
	userName := "Unknown"
	if user, ok := auth.UserFrom(r.Context()); ok {
		userName = user.Username
	}
	if b.ProfileID > 0 {
		u, _ := auth.UserFrom(r.Context())
		if err := p.DB.QueryRowContext(r.Context(), `SELECT name FROM profiles WHERE id=? AND user_id=?`, b.ProfileID, u.ID).Scan(&profileName); err != nil {
			writeJSONError(w, 400, "invalid profile")
			return
		}
	}
	var channelName string
	var number int
	if err := p.DB.QueryRowContext(r.Context(), `SELECT number,name FROM channels WHERE id=?`, b.ChannelID).Scan(&number, &channelName); err != nil {
		writeJSONError(w, 404, "channel not found")
		return
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	now := p.Clock.Now()
	key := fmt.Sprintf("%d:%s", principal, b.ViewerID)
	p.activityMu.Lock()
	defer p.activityMu.Unlock()
	if p.activityRows == nil {
		p.activityRows = map[string]viewerActivity{}
	}
	for k, v := range p.activityRows {
		if now.Sub(v.lastSeen) >= activityTTL {
			delete(p.activityRows, k)
		}
	}
	v, exists := p.activityRows[key]
	if v.ended {
		writeJSONError(w, 410, "player ended")
		return
	}
	if !exists && len(p.activityRows) >= 1024 {
		writeJSONError(w, 503, "activity capacity reached")
		return
	}
	commercial := string(stream.Job.Entry.Kind) == "commercial"
	if !exists || v.channelID != b.ChannelID {
		v = viewerActivity{ID: s.ID, principal: principal, channelID: b.ChannelID, Channel: fmt.Sprintf("CH %d - %s", number, channelName), Profile: profileName, User: userName, SourceIP: ip, Device: deviceLabel(r.UserAgent()), StartedAt: now}
	}
	if !commercial {
		if stream.Job.Entry.Kind == schedule.KindProgram && !stream.Job.Entry.StartsAt.IsZero() {
			v.programPositionSeconds = max(0, now.Sub(stream.Job.Entry.StartsAt).Seconds()+stream.Job.Entry.SourceOffsetSeconds)
			v.programPositionAt = now
		}
		v.PlaybackDurationSeconds = nil
		if duration := stream.Job.Source.DurationSeconds; duration > 0 && !math.IsNaN(duration) && !math.IsInf(duration, 0) {
			v.PlaybackDurationSeconds = &duration
		}
		if v.sessionID == s.ID && !v.Commercial && now.After(v.sampleAt) {
			rate := max(0, float64(s.MediaBytes-v.bytes)*8/now.Sub(v.sampleAt).Seconds()/1e6)
			v.BandwidthMbps = &rate
		} else {
			v.BandwidthMbps = nil
		}
		v.LatencyMS = b.LatencyMS
		v.Channel = fmt.Sprintf("CH %d - %s", number, channelName)
		fileName := stream.Job.Details.FileName
		if v.sessionID != s.ID || v.Video != fileName {
			v.Program = activityProgramTitle(r.Context(), p.DB, stream.Job.Entry.MediaFileID, fileName)
		}
		v.Video = fileName
		v.VideoReason = stream.Job.Details.VideoReason
		v.AudioReason = stream.Job.Details.AudioReason
		v.SourceVideo = stream.Job.Details.SourceVideo
		v.OutputVideo = stream.Job.Details.OutputVideo
		v.SourceAudio = stream.Job.Details.SourceAudio
		v.OutputAudio = stream.Job.Details.OutputAudio
		if stream.Job.Details.SourceAudio == nil {
			v.AudioReason = "No audio track."
		}
		v.Mode = stream.Job.Plan.Mode
		v.Encoder = ""
		if v.Mode != "direct" && (!stream.Job.Plan.VideoCopy || !stream.Job.Plan.AudioCopy) {
			v.Encoder = "Software"
			if !stream.Job.Plan.VideoCopy && playback.IsHardwareEncoder(stream.Job.Plan.Encoder) {
				v.Encoder = playback.EncoderLabel(stream.Job.Plan.Encoder)
			}
		}
		v.UpdatedAt = now
	} else if !v.Commercial && !v.programPositionAt.IsZero() {
		v.programPositionSeconds += max(0, now.Sub(v.programPositionAt).Seconds())
		v.programPositionAt = now
	}
	// Reported by this player, never substituted with the server build.
	v.AppVersion = strings.TrimSpace(b.AppVersion)
	v.Profile = profileName
	v.User = userName
	v.SourceIP = ip
	v.Paused = b.Paused
	v.Commercial = commercial
	v.lastSeen = now
	v.sampleAt = now
	v.bytes = s.MediaBytes
	v.sessionID = s.ID
	v.streamKey = s.StreamKey
	p.activityRows[key] = v
	w.WriteHeader(http.StatusNoContent)
}

func (p *PlaybackHTTP) endActivity(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ViewerID string `json:"viewerId"`
	}
	if !decodeStrict(w, r, &b) {
		return
	}
	if len(b.ViewerID) < 16 || len(b.ViewerID) > 80 {
		writeJSONError(w, 400, "invalid player identifier")
		return
	}
	p.activityMu.Lock()
	if p.activityRows == nil {
		p.activityRows = map[string]viewerActivity{}
	}
	now := p.Clock.Now()
	for key, v := range p.activityRows {
		if now.Sub(v.lastSeen) >= activityTTL {
			delete(p.activityRows, key)
		}
	}
	key := fmt.Sprintf("%d:%s", auth.PrincipalID(r.Context()), b.ViewerID)
	if _, exists := p.activityRows[key]; exists || len(p.activityRows) < 1024 {
		// A late in-flight report must not resurrect an explicitly ended player.
		p.activityRows[key] = viewerActivity{ended: true, lastSeen: now, principal: auth.PrincipalID(r.Context())}
	}
	p.activityMu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}
func (p *PlaybackHTTP) serverActivity(w http.ResponseWriter, r *http.Request) {
	now := p.Clock.Now()
	sessions := []viewerActivity{}
	p.activityMu.Lock()
	for key, v := range p.activityRows {
		if now.Sub(v.lastSeen) >= activityTTL || !auth.PrincipalActive(r.Context(), p.DB, v.principal, now) {
			delete(p.activityRows, key)
			continue
		}
		if v.ended {
			continue
		}
		// Allow a short entry-transition gap after release, without extending playback leases.
		if p.Manager == nil {
			continue
		}
		if _, err := p.Manager.Inspect(v.streamKey, v.sessionID); err != nil && now.Sub(v.lastSeen) > 10*time.Second {
			delete(p.activityRows, key)
			continue
		}
		if v.Video != "" {
			if !v.programPositionAt.IsZero() {
				position := v.programPositionSeconds
				if !v.Commercial {
					position += max(0, now.Sub(v.programPositionAt).Seconds())
				}
				v.PlaybackPositionSeconds = &position
			}
			sessions = append(sessions, v)
		}
	}
	p.activityMu.Unlock()
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].ID < sessions[j].ID })
	tasks := activity.Default.Snapshot(now)
	pipeline := status.Default.Snapshot()
	// Counted sub-steps below have their own rows; metadata discovery has no known total yet.
	pipelineVisible := false
	for _, task := range tasks {
		if strings.HasPrefix(task.Name, "Library") || strings.HasPrefix(task.Name, "Metadata") || strings.HasPrefix(task.Name, "Channel") {
			pipelineVisible = true
		}
	}
	if pipeline.Active && pipeline.StartedAt != nil && !pipelineVisible {
		tasks = append(tasks, activity.Task{ID: "library-pipeline", Name: "Library Maintenance", Unit: string(pipeline.Phase), StartedAt: *pipeline.StartedAt, RuntimeSeconds: max(0, now.Sub(*pipeline.StartedAt).Seconds())})
	}
	transcodeSlots := struct {
		Active int `json:"active"`
		Limit  int `json:"limit"`
	}{}
	if p.Manager != nil {
		transcodeSlots.Active, transcodeSlots.Limit = p.Manager.TranscodeSlots()
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"sessions": sessions, "processes": tasks, "transcodeSlots": transcodeSlots})
}

func activityProgramTitle(ctx context.Context, db *sql.DB, mediaFileID int64, fallback string) string {
	var mediaType, title, series string
	var releaseYear int
	err := db.QueryRowContext(ctx, `SELECT media_type, title, COALESCE(series_title,''), COALESCE(release_year,0) FROM identified_items WHERE media_file_id=?`, mediaFileID).Scan(&mediaType, &title, &series, &releaseYear)
	if err != nil || strings.TrimSpace(title) == "" {
		return fallback
	}
	if mediaType == "episode" && strings.TrimSpace(series) != "" {
		if series == title {
			return series
		}
		return series + " - " + title
	}
	if releaseYear > 0 {
		return fmt.Sprintf("%s (%d)", title, releaseYear)
	}
	return title
}

func deviceLabel(ua string) string {
	device := "Desktop"
	switch {
	case strings.Contains(ua, "TV") || strings.Contains(ua, "CrKey"):
		device = "TV"
	case strings.Contains(ua, "iPad") || strings.Contains(ua, "Tablet"):
		device = "Tablet"
	case strings.Contains(ua, "Mobile") || strings.Contains(ua, "Android"):
		device = "Mobile"
	case ua == "":
		return "Unknown"
	}
	browser := "Browser"
	switch {
	case strings.Contains(ua, "Edg/"):
		browser = "Edge"
	case strings.Contains(ua, "Firefox/"):
		browser = "Firefox"
	case strings.Contains(ua, "Chrome/"):
		browser = "Chrome"
	case strings.Contains(ua, "Safari/"):
		browser = "Safari"
	}
	os := ""
	switch {
	case strings.Contains(ua, "Android"):
		os = "Android"
	case strings.Contains(ua, "iPhone") || strings.Contains(ua, "iPad"):
		os = "iOS"
	case strings.Contains(ua, "Windows"):
		os = "Windows"
	case strings.Contains(ua, "Macintosh"):
		os = "macOS"
	case strings.Contains(ua, "Linux"):
		os = "Linux"
	}
	return strings.TrimSpace(device + " · " + browser + " " + os)
}
