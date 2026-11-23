package httpapi

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"github.com/alreadyonapp/alreadyon/server/internal/logging"
	"github.com/alreadyonapp/alreadyon/server/internal/playback"
	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
)

// PlaybackHTTP keeps session issuance and scoped media authorization separate
// on one HTTPS listener. Browser cookies never authorize media payloads.
type PlaybackHTTP struct {
	Benchmarks   *playback.Benchmarks
	Calibration  *playback.Calibration
	activityMu   sync.Mutex
	activityRows map[string]viewerActivity
	DB           *sql.DB
	Sessions     *playback.Sessions
	Manager      *playback.Manager
	probe        []byte
	Clock        schedule.Clock
	tuneSlots    chan struct{}
	Subtitles    *playback.Subtitles
	// Hardware is nil when hardware transcoding is not set up (tests); streams then use software.
	Hardware *playback.Hardware
	// Tuner picks the CPU thread count for transcodes; nil uses the default.
	Tuner *playback.Tuner
	// Capacity is what the server measured about its own encoding speed; nil
	// means no limits beyond the settings.
	Capacity *playback.Capacity
}

func NewPlaybackHTTP(db *sql.DB, sessions *playback.Sessions, manager *playback.Manager) *PlaybackHTTP {
	data := make([]byte, 1<<20)
	if _, err := rand.Read(data); err != nil {
		panic(err)
	}
	return &PlaybackHTTP{DB: db, Sessions: sessions, Manager: manager, probe: data, Clock: schedule.SystemClock{}, tuneSlots: make(chan struct{}, 2), Subtitles: playback.NewSubtitles()}
}

func (p *PlaybackHTTP) register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/playback/activity", p.reportActivity)
	mux.HandleFunc("DELETE /api/playback/activity", p.endActivity)
	mux.HandleFunc("GET /api/server/activity", p.serverActivity)
	mux.HandleFunc("POST /api/playback/sessions", p.issue)
	mux.HandleFunc("POST /api/channels/{id}/tune", p.tune)
	mux.HandleFunc("POST /api/playback/status", p.playbackStatus)
	mux.HandleFunc("POST /api/playback/probe", p.issueProbe)
	mux.HandleFunc("DELETE /api/playback/sessions", p.revoke)
	mux.HandleFunc("GET /api/settings/playback/delivery", p.getDelivery)
	mux.HandleFunc("PUT /api/settings/playback/delivery", p.putDelivery)
	mux.HandleFunc("GET /api/settings/transcoding/threads", p.getThreads)
	mux.HandleFunc("GET /api/settings/transcoding/benchmark", p.getBenchmark)
	mux.HandleFunc("GET /api/settings/transcoding/capacity", p.getCapacity)
	mux.HandleFunc("POST /api/settings/transcoding/benchmark", p.startBenchmark)
	mux.HandleFunc("DELETE /api/settings/transcoding/benchmark", p.stopBenchmark)
}

func (p *PlaybackHTTP) issue(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ChannelID int64 `json:"channelId"`
	}
	if !decodeStrict(w, r, &body) {
		return
	}
	var exists int
	if body.ChannelID <= 0 || p.DB.QueryRowContext(r.Context(), `SELECT 1 FROM channels WHERE id=?`, body.ChannelID).Scan(&exists) != nil {
		writeJSONError(w, 404, "channel not found")
		return
	}
	p.issueFor(w, r, body.ChannelID)
}
func (p *PlaybackHTTP) issueProbe(w http.ResponseWriter, r *http.Request) { p.issueFor(w, r, 0) }
func (p *PlaybackHTTP) issueFor(w http.ResponseWriter, r *http.Request, channelID int64) {
	// Capability/network probes run on settings pages too. They must respect
	// initial readiness without cancelling an idle benchmark merely to view status.
	if channelID != 0 || p.Benchmarks != nil && !p.Benchmarks.Ready() {
		release, ok := p.admitPlayback(w, r)
		if !ok {
			return
		}
		defer release()
	}
	token, s, err := p.Sessions.Issue(auth.PrincipalID(r.Context()), channelID)
	if errors.Is(err, playback.ErrBusy) {
		w.Header().Set("Retry-After", "1")
		writeJSONError(w, 503, "playback session capacity reached; retry shortly")
		return
	}
	if err != nil {
		writeJSONError(w, 401, "could not issue playback authorization")
		return
	}
	base, err := p.mediaBase(r)
	if err != nil {
		p.Sessions.Revoke(token)
		writeJSONError(w, 500, "could not resolve media address")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 201, struct {
		playback.Session
		Token    string `json:"token"`
		MediaURL string `json:"mediaUrl"`
	}{s, token, fmt.Sprintf("%s/media/%d", base, channelID)})
}
func (p *PlaybackHTTP) revoke(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token     string `json:"token"`
		ChannelID int64  `json:"channelId"`
	}
	if !decodeStrict(w, r, &body) {
		return
	}
	s, err := p.Sessions.ReleaseFor(body.Token, auth.PrincipalID(r.Context()), body.ChannelID)
	if err != nil {
		writeJSONCode(w, 401, "playback_session_invalid", "invalid playback session")
		return
	}
	if p.Manager != nil && s.StreamKey != "" {
		p.Manager.Release(s.StreamKey, s.ID)
		logging.Info("stream stopped: %s, viewer left", channelLabel(r.Context(), p.DB, body.ChannelID))
	}
	w.WriteHeader(204)
}

func (p *PlaybackHTTP) mediaBase(r *http.Request) (string, error) {
	// Use the browser origin, including its public port. Forwarding headers and
	// legacy advertised media ports must not redirect playback to another listener.
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host, nil
}

func (p *PlaybackHTTP) getDelivery(w http.ResponseWriter, r *http.Request) {
	d, err := playback.GetDelivery(r.Context(), p.DB)
	if err != nil {
		writeJSONError(w, 500, "could not load delivery settings")
		return
	}
	writeJSON(w, 200, d)
}
func (p *PlaybackHTTP) putDelivery(w http.ResponseWriter, r *http.Request) {
	var d playback.Delivery
	if !decodeStrict(w, r, &d) {
		return
	}
	if err := d.Validate(); err != nil {
		writeJSONError(w, 400, err.Error())
		return
	}
	if err := playback.SaveDelivery(r.Context(), p.DB, d); err != nil {
		writeJSONError(w, 500, "could not save delivery settings")
		return
	}
	writeJSON(w, 200, d)
}

func (p *PlaybackHTTP) MediaHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /media/{channel}/probe", p.mediaProbe)
	mux.HandleFunc("GET /media/{channel}/source", p.mediaSource)
	mux.HandleFunc("GET /media/{channel}/index.m3u8", p.mediaManifest)
	mux.HandleFunc("GET /media/{channel}/subtitles/{index}", p.mediaSubtitles)
	mux.HandleFunc("GET /media/{channel}/{segment}", p.mediaSegment)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if origin := r.Header.Get("Origin"); origin != "" {
			d, err := playback.GetDelivery(r.Context(), p.DB)
			if err != nil || !p.originAllowed(r, origin, d) {
				writeJSONError(w, 403, "media origin is not allowed")
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Timing-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Range, Authorization")
			w.Header().Set("Access-Control-Expose-Headers", "Content-Length, Content-Range, Accept-Ranges")
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		// Do not rely on router matching to establish the authorization scope.
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) < 3 || parts[0] != "media" {
			writeJSONError(w, 401, "invalid playback authorization")
			return
		}
		channel, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			writeJSONError(w, 401, "invalid playback authorization")
			return
		}
		token := r.URL.Query().Get("token")
		s, err := p.Sessions.Validate(token, channel)
		if err != nil || !auth.PrincipalActive(r.Context(), p.DB, s.KeyID, time.Now()) {
			writeJSONError(w, 401, "invalid playback authorization")
			return
		}
		// Probes, manifests and subtitles are not stream payload.
		if parts[2] == "source" || segmentName.MatchString(parts[2]) {
			w = &mediaCounter{ResponseWriter: w, sessions: p.Sessions, token: token}
		}
		if parts[2] != "probe" || p.Benchmarks != nil && !p.Benchmarks.Ready() {
			release, ok := p.admitPlayback(w, r)
			if !ok {
				return
			}
			defer release()
		}
		mux.ServeHTTP(w, r)
	})
}

func (p *PlaybackHTTP) originAllowed(r *http.Request, origin string, d playback.Delivery) bool {
	if r.TLS != nil {
		u, err := url.Parse(origin)
		if err != nil || u.Scheme != "https" {
			return false
		}
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if origin == scheme+"://"+r.Host {
		return true
	}
	// Cross-origin clients require an explicit allowlist; forwarded headers are
	// never trusted to broaden the same-origin security boundary.
	return slices.Contains(d.AllowedOrigins, origin)
}

func (p *PlaybackHTTP) mediaProbe(w http.ResponseWriter, r *http.Request) {
	n := int64(1)
	if raw := r.URL.Query().Get("bytes"); raw != "" {
		var err error
		n, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 1 || n > 8<<20 {
			writeJSONError(w, 400, "probe size must be 1 through 8388608 bytes")
			return
		}
	}
	if !p.Sessions.SpendProbe(r.URL.Query().Get("token"), n) {
		writeJSONError(w, 429, "probe budget exhausted")
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Second))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(n, 10))
	for n > 0 {
		size := min(n, int64(len(p.probe)))
		if _, err := w.Write(p.probe[:size]); err != nil {
			return
		}
		n -= size
	}
}

// retune re-measures the thread count and the server's capacity after the
// encoder settings changed.
func (p *PlaybackHTTP) retune() {
	if p.Benchmarks != nil {
		p.Benchmarks.AutomaticMini()
		if err := p.pauseStreams(); err != nil {
			p.Benchmarks.Stop()
			logging.Warn("video transcoding: could not pause streams for quick test: %v", err)
		}
		return
	}
	if p.Tuner != nil {
		p.Tuner.Start(false)
	}
	if p.Capacity != nil {
		p.Capacity.Start(false)
	}
}

func (p *PlaybackHTTP) getCapacity(w http.ResponseWriter, r *http.Request) {
	if p.Capacity == nil {
		writeJSONError(w, 503, "server capacity measurement is not available")
		return
	}
	writeJSON(w, 200, p.capacityStatus())
}

func (p *PlaybackHTTP) startBenchmark(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode               string `json:"mode"`
		AllowActiveStreams bool   `json:"allowActiveStreams"`
	}
	if !decodeStrict(w, r, &body) {
		return
	}
	if body.Mode != "quick" && body.Mode != "full" {
		writeJSONError(w, 400, "mode must be quick or full")
		return
	}
	if p.Benchmarks == nil {
		writeJSONError(w, 503, "benchmarking is not available")
		return
	}
	_, _, viewers := p.streamCounts()
	if body.Mode == "full" && viewers > 0 && !body.AllowActiveStreams {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Active streams will be paused while the full test runs. Confirm before starting it.", "activeStreams": viewers})
		return
	}
	if body.Mode == "quick" {
		p.Benchmarks.Mini()
	} else {
		p.Benchmarks.Full()
	}
	// Locking the benchmark happens before stream teardown, so no new viewer can
	// race into the resource reservation.
	if err := p.pauseStreams(); err != nil {
		p.Benchmarks.Stop()
		writeJSONError(w, 503, "could not pause active streams for the test")
		return
	}
	writeJSON(w, 202, p.benchmarkStatus())
}

func (p *PlaybackHTTP) stopBenchmark(w http.ResponseWriter, _ *http.Request) {
	if p.Benchmarks == nil {
		writeJSONError(w, 503, "benchmarking is not available")
		return
	}
	p.Benchmarks.Stop()
	writeJSON(w, 202, p.benchmarkStatus())
}

func (p *PlaybackHTTP) pauseStreams() error {
	if p.Manager == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := p.Manager.PauseAll(ctx); err != nil {
		return err
	}
	if p.Sessions != nil {
		p.Sessions.PauseStreaming()
	}
	return nil
}

func (p *PlaybackHTTP) streamCounts() (processes, renditions, viewers int) {
	if p.Manager == nil {
		return 0, 0, 0
	}
	return p.Manager.Counts()
}

func (p *PlaybackHTTP) getThreads(w http.ResponseWriter, r *http.Request) {
	if p.Tuner == nil {
		writeJSONError(w, 503, "thread tuning is not available")
		return
	}
	writeJSON(w, 200, p.threadStatus())
}

type mediaCounter struct {
	status int
	http.ResponseWriter
	sessions *playback.Sessions
	token    string
}

func (w *mediaCounter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *mediaCounter) WriteHeader(code int) {
	if code >= 200 && w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}
func (w *mediaCounter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	if w.status == http.StatusOK || w.status == http.StatusPartialContent {
		w.sessions.AddMediaBytes(w.token, n)
	}
	return n, err
}

// Startup rejections are retryable and do not revoke authentication or issue tokens.
func (p *PlaybackHTTP) admitPlayback(w http.ResponseWriter, r *http.Request) (func(), bool) {
	if p.Benchmarks == nil {
		return func() {}, true
	}
	release, err := p.Benchmarks.Admit(r.Context())
	if err != nil {
		w.Header().Set("Retry-After", "2")
		w.Header().Set("Cache-Control", "no-store")
		if errors.Is(err, playback.ErrServerStarting) {
			writeJSON(w, 503, map[string]any{"code": "server_starting", "error": playback.ErrServerStarting.Error()})
		} else if errors.Is(err, playback.ErrBenchmarkRunning) {
			writeJSON(w, 503, map[string]any{"code": "benchmark_running", "error": playback.ErrBenchmarkRunning.Error()})
		} else {
			writeJSONError(w, 503, "Playback is waiting for background work to stop; retry shortly.")
		}
		return func() {}, false
	}
	return release, true
}

func (p *PlaybackHTTP) capacityStatus() playback.CapacityStatus {
	s := p.Capacity.Status()
	if p.Benchmarks != nil && p.Benchmarks.Running() {
		s.State = "running"
	}
	return s
}
func (p *PlaybackHTTP) threadStatus() playback.TuneStatus {
	s := p.Tuner.Status()
	if p.Benchmarks != nil && p.Benchmarks.Running() {
		s.State = "running"
	}
	return s
}

func (p *PlaybackHTTP) getBenchmark(w http.ResponseWriter, r *http.Request) {
	if p.Benchmarks == nil {
		writeJSONError(w, 503, "benchmarking is not available")
		return
	}
	writeJSON(w, 200, p.benchmarkStatus())
}

func (p *PlaybackHTTP) benchmarkStatus() playback.BenchmarkStatus {
	s := p.Benchmarks.Status()
	if p.Capacity != nil {
		s.Profiles = p.Capacity.Status().Profiles
	}
	_, _, s.ActiveStreams = p.streamCounts()
	if p.Calibration != nil {
		calibration := p.Calibration.Status()
		s.QuickAt, s.FullAt = calibration.QuickAt, calibration.FullAt
		s.QuickProfiles, s.FullProfiles = calibration.QuickProfiles, calibration.FullProfiles
		s.RecommendFull = calibration.RecommendFull
	}
	return s
}
