package httpapi

import (
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
	"github.com/alreadyonapp/alreadyon/server/internal/serversettings"
)

// PlaybackHTTP keeps session issuance and scoped media authorization separate
// on one HTTPS listener. Browser cookies never authorize media payloads.
type PlaybackHTTP struct {
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
	mux.HandleFunc("POST /api/settings/transcoding/test", p.testTranscoding)
	mux.HandleFunc("GET /api/settings/transcoding/threads", p.getThreads)
	mux.HandleFunc("POST /api/settings/transcoding/threads/tune", p.tuneThreads)
	mux.HandleFunc("GET /api/settings/transcoding/capacity", p.getCapacity)
	mux.HandleFunc("POST /api/settings/transcoding/capacity/measure", p.measureCapacity)
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
	s, err := p.Sessions.Validate(body.Token, body.ChannelID)
	if err != nil || s.KeyID != auth.PrincipalID(r.Context()) {
		writeJSONError(w, 401, "invalid playback session")
		return
	}
	s = p.Sessions.Revoke(body.Token)
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

// testTranscoding tries the named Transcoding choice on this host (a one-second
// synthetic encode, or with decoding set a synthetic GPU decode) so the administrator learns whether it works before relying
// on it. Streams fall back to software on their own when it does not.
func (p *PlaybackHTTP) testTranscoding(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Transcoding string `json:"transcoding"`
		Decoding    bool   `json:"decoding"`
	}
	if !decodeStrict(w, r, &body) {
		return
	}
	if !slices.Contains(serversettings.Transcoding, body.Transcoding) {
		writeJSONError(w, 400, serversettings.ErrInvalidTranscoding.Error())
		return
	}
	if body.Decoding && body.Transcoding == "software" {
		writeJSONError(w, 400, serversettings.ErrDecodeNeedsEncode.Error())
		return
	}
	if p.Hardware == nil {
		writeJSONError(w, 503, "hardware testing is not available")
		return
	}
	writeJSON(w, 200, p.Hardware.TestSetting(r.Context(), body.Transcoding, body.Decoding))
}

// retune re-measures the thread count and the server's capacity after the
// encoder settings changed.
func (p *PlaybackHTTP) retune() {
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
	writeJSON(w, 200, p.Capacity.Status())
}

// measureCapacity starts a measurement now, even while streams play.
func (p *PlaybackHTTP) measureCapacity(w http.ResponseWriter, r *http.Request) {
	if p.Capacity == nil {
		writeJSONError(w, 503, "server capacity measurement is not available")
		return
	}
	p.Capacity.Start(true)
	writeJSON(w, 202, p.Capacity.Status())
}

func (p *PlaybackHTTP) getThreads(w http.ResponseWriter, r *http.Request) {
	if p.Tuner == nil {
		writeJSONError(w, 503, "thread tuning is not available")
		return
	}
	writeJSON(w, 200, p.Tuner.Status())
}

// tuneThreads starts a measurement now, even while streams play.
func (p *PlaybackHTTP) tuneThreads(w http.ResponseWriter, r *http.Request) {
	if p.Tuner == nil {
		writeJSONError(w, 503, "thread tuning is not available")
		return
	}
	p.Tuner.Start(true)
	writeJSON(w, 202, p.Tuner.Status())
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
