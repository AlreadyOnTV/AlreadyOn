package httpapi

import (
	"fmt"
	"math"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/activity"
	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"github.com/alreadyonapp/alreadyon/server/internal/playback"
	"github.com/alreadyonapp/alreadyon/server/internal/status"
)

const activityTTL = 35 * time.Second

type viewerActivity struct {
	ended                bool
	ID                   string    `json:"id"`
	Channel              string    `json:"channel"`
	Video                string    `json:"video"`
	VideoReason          string    `json:"videoReason"`
	AudioReason          string    `json:"audioReason"`
	Profile              string    `json:"profile"`
	SourceIP             string    `json:"sourceIp"`
	Device               string    `json:"device"`
	Mode                 string    `json:"mode"`
	Encoder              string    `json:"encoder"`
	BandwidthMbps        *float64  `json:"bandwidthMbps"`
	LatencyMS            *float64  `json:"latencyMs"`
	Commercial           bool      `json:"commercial"`
	Paused               bool      `json:"paused"`
	UpdatedAt            time.Time `json:"updatedAt"`
	principal            int64
	channelID            int64
	sessionID, streamKey string
	bytes                int64
	sampleAt             time.Time
	lastSeen             time.Time
}

// Activity is tied to a player instance, not an FFmpeg rendition or a prefetch.
func (p *PlaybackHTTP) reportActivity(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ViewerID  string   `json:"viewerId"`
		Token     string   `json:"token"`
		ChannelID int64    `json:"channelId"`
		ProfileID int64    `json:"profileId"`
		LatencyMS *float64 `json:"latencyMs"`
		Paused    bool     `json:"paused"`
	}
	if !decodeStrict(w, r, &b) {
		return
	}
	if len(b.ViewerID) < 16 || len(b.ViewerID) > 80 || (b.LatencyMS != nil && (math.IsNaN(*b.LatencyMS) || math.IsInf(*b.LatencyMS, 0) || *b.LatencyMS < 0 || *b.LatencyMS > 60000)) {
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
		v = viewerActivity{ID: s.ID, principal: principal, channelID: b.ChannelID, Channel: fmt.Sprintf("%d · %s", number, channelName), Profile: profileName, SourceIP: ip, Device: deviceLabel(r.UserAgent())}
	}
	if !commercial {
		if v.sessionID == s.ID && !v.Commercial && now.After(v.sampleAt) {
			rate := max(0, float64(s.MediaBytes-v.bytes)*8/now.Sub(v.sampleAt).Seconds()/1e6)
			v.BandwidthMbps = &rate
		} else {
			v.BandwidthMbps = nil
		}
		v.LatencyMS = b.LatencyMS
		v.Channel = fmt.Sprintf("%d · %s", number, channelName)
		v.Video = stream.Job.Details.FileName
		v.VideoReason = stream.Job.Details.VideoReason
		v.AudioReason = stream.Job.Details.AudioReason
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
	}
	v.Profile = profileName
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
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"sessions": sessions, "processes": tasks})
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
