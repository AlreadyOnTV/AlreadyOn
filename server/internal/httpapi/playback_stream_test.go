package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/playback"
	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
)

func TestTuneDirectOffsetScopeAndRange(t *testing.T) {
	db := openTestDB(t)
	root := t.TempDir()
	path := filepath.Join(root, "sample.mp4")
	if err := os.WriteFile(path, []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	clock := &httpClock{time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)}
	source := library.ProbeResult{Container: "mp4", DurationSeconds: 120, BitRate: 1000000, Streams: []library.StreamInfo{{Index: 0, Kind: library.StreamVideo, Codec: "h264", Profile: "High", Level: 40, Width: 320, Height: 240, PixelFormat: "yuv420p", FrameRate: 30, BitRate: 800000}, {Index: 1, Kind: library.StreamAudio, Codec: "aac", Profile: "LC", Channels: 2, SampleRate: 48000, BitRate: 192000}}}
	probe, _ := json.Marshal(source)
	roots, _ := json.Marshal([]string{root})
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE folder_settings SET tv_paths=?,movie_paths='[]',commercial_paths='[]' WHERE id=1`, []any{string(roots)}},
		{`INSERT INTO channels(id,number,name) VALUES(999,999,'test')`, nil},
		{`UPDATE commercial_settings SET volume_offset_db=-6 WHERE id=1`, nil},
		{`INSERT INTO media_files(id,root_kind,path,size_bytes,modified_unix,first_seen_unix,last_seen_unix,duration_seconds,status,probe_json) VALUES(999,'media',?,10,0,0,0,120,'ok',?)`, []any{path, string(probe)}},
		{`INSERT INTO schedule_entries(channel_id,media_file_id,kind,source_offset_seconds,duration_seconds,starts_at_unix,ends_at_unix) VALUES(999,999,'program',15,60,?,?)`, []any{clock.now.Add(-10 * time.Second).Unix(), clock.now.Add(50 * time.Second).Unix()}},
	} {
		if _, err := db.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	raw, _, err := auth.GenerateAPIKey(context.Background(), db, testOwnerID(t, db), "test")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := playback.NewManager(t.TempDir(), clock, playback.Runner{}, playback.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	p := NewPlaybackHTTP(db, playback.NewSessions(clock), manager)
	p.Clock = clock
	management := auth.RequireAuth(db, NewRouter(t.TempDir(), t.TempDir(), db, p))
	body := `{"profileId":1,"capabilities":{"hls":true,"containers":["mp4"],"video":[{"codec":"h264","profile":"High","level":42,"width":1920,"height":1080,"fps":60,"bitrate":20000000,"pixelFormat":"yuv420p"}],"audio":[{"codec":"aac","channels":2,"sampleRate":48000,"bitrate":320000}]}}`
	r := httptest.NewRequest("POST", "http://localhost:19900/api/channels/999/tune", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+raw)
	w := httptest.NewRecorder()
	management.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	// A new authenticated browser/session sees the committed profile immediately,
	// without waiting for an activity heartbeat or a separate client save.
	client := newAuthClientForHandler(t, db, management)
	if rec := client.do("POST", "/api/auth/login", `{"username":"owner","password":"synthetic-owner-pass"}`); rec.Code != 200 {
		t.Fatalf("login: %d %s", rec.Code, rec.Body.String())
	}
	rec := client.do("GET", "/api/profiles/1", "")
	viewer := decodeProfile(t, rec)
	if rec.Code != 200 || viewer.LastWatchedChannelID == nil || *viewer.LastWatchedChannelID != 999 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("remembered channel: %d %+v", rec.Code, viewer)
	}
	if kids := decodeProfile(t, client.do("GET", "/api/profiles/2", "")); kids.LastWatchedChannelID != nil {
		t.Fatalf("tune changed a different profile: %+v", kids)
	}
	for _, attempt := range []struct {
		path, body string
		status     int
	}{
		{"/api/channels/999/tune", strings.Replace(body, `"profileId":1`, `"profileId":99999`, 1), 404},
		{"/api/channels/998/tune", strings.Replace(body, `"profileId":1`, `"profileId":2`, 1), 404},
		{"/api/channels/999/tune", strings.Replace(body, `"profileId":1`, `"profileId":2,"maxHeight":500`, 1), 400},
		{"/api/channels/999/tune", strings.Replace(body, `"profileId":1,`, "", 1), 201},
	} {
		if rec := client.do("POST", attempt.path, attempt.body); rec.Code != attempt.status {
			t.Fatalf("attempt %s: %d %s; want %d", attempt.path, rec.Code, rec.Body.String(), attempt.status)
		}
	}
	if kids := decodeProfile(t, client.do("GET", "/api/profiles/2", "")); kids.LastWatchedChannelID != nil {
		t.Fatalf("failed or legacy tune changed a profile: %+v", kids)
	}
	var result tuneResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ChannelNumber != 999 || result.ChannelName != "test" || result.Program == nil || result.Program.MediaFileID != 999 || result.Program.Title != "sample" {
		t.Fatalf("missing stream display metadata: %+v", result)
	}
	// 10s into the entry on the server clock, presented 4s behind, plus the
	// entry's 15s source offset.
	if result.Plan.VolumeOffsetDB != 0 || result.Plan.Mode != "direct" || result.StreamOffsetSeconds != 21 || result.PresentationDelaySeconds != 4 || !result.Ready {
		t.Fatalf("%+v", result)
	}
	if result.DurationSeconds != 120 || result.SourceHeight != 240 || result.Subtitles == nil || len(result.Subtitles) != 0 {
		t.Fatalf("duration/height/subtitles = %v/%v/%v, want the whole file's length, its height and an empty (non-null) track list", result.DurationSeconds, result.SourceHeight, result.Subtitles)
	}
	bad := httptest.NewRequest("POST", "http://localhost:19900/api/channels/999/tune", strings.NewReader(strings.Replace(body, "{", `{"maxHeight":500,`, 1)))
	bad.Header.Set("Authorization", "Bearer "+raw)
	w = httptest.NewRecorder()
	management.ServeHTTP(w, bad)
	if w.Code != 400 {
		t.Fatalf("an unsupported quality ceiling: %d, want 400", w.Code)
	}
	for extra, expected := range map[string]int{`"audioTrackIndex":0,"audioMediaFileId":999,`: 400, `"audioTrackIndex":1,`: 400, `"audioTrackIndex":999,"audioMediaFileId":998,`: 201} {
		req := httptest.NewRequest("POST", "http://localhost:19900/api/channels/999/tune", strings.NewReader(strings.Replace(body, "{", "{"+extra, 1)))
		req.Header.Set("Authorization", "Bearer "+raw)
		recorder := httptest.NewRecorder()
		management.ServeHTTP(recorder, req)
		if recorder.Code != expected {
			t.Fatalf("audio selection status %d, want %d", recorder.Code, expected)
		}
	}
	media := p.MediaHandler()
	// Missing client capabilities and a small connection budget need different
	// recovery advice, even though both are rejected by the same planner.
	for _, tc := range []struct{ body, code string }{
		{`{"capabilities":{"hls":false,"video":[],"audio":[]}}`, "client_codec_unsupported"},
		{strings.Replace(body, `"codec":"h264"`, `"codec":"vp9"`, 1), "client_codec_unsupported"},
		{strings.Replace(body, `"codec":"aac"`, `"codec":"opus"`, 1), "client_codec_unsupported"},
		{strings.Replace(body, `"hls":true`, `"hls":true,"mbps":0.001`, 1), "playback_incompatible"},
	} {
		req := httptest.NewRequest("POST", "http://localhost:19900/api/channels/999/tune", strings.NewReader(tc.body))
		req.Header.Set("Authorization", "Bearer "+raw)
		recorder := httptest.NewRecorder()
		management.ServeHTTP(recorder, req)
		var failure struct{ Code, RetryAt, ServerTime string }
		if err := json.Unmarshal(recorder.Body.Bytes(), &failure); err != nil || recorder.Code != 422 || failure.Code != tc.code || failure.RetryAt == "" || failure.ServerTime == "" {
			t.Fatalf("expected %s with recovery time; got %d %s", tc.code, recorder.Code, recorder.Body.String())
		}
	}
	r = httptest.NewRequest("GET", result.URL, nil)
	r.Header.Set("Range", "bytes=2-5")
	w = httptest.NewRecorder()
	media.ServeHTTP(w, r)
	if w.Code != 206 || w.Body.String() != "2345" {
		t.Fatalf("range: %d %q", w.Code, w.Body.String())
	}
	// HTML-looking library bytes must remain inert on the media listener, even
	// when served through both the accounting and idle-deadline wrappers.
	if err := os.WriteFile(path, []byte("<script>alert(1)</script>"), 0600); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("GET", result.URL, nil)
	w = httptest.NewRecorder()
	media.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/octet-stream" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("active media response: %d %v", w.Code, w.Header())
	}
	r = httptest.NewRequest("GET", strings.Replace(result.URL, "/999/", "/998/", 1), nil)
	w = httptest.NewRecorder()
	media.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("wrong-channel file exposed")
	}
	if _, err := db.Exec(`UPDATE folder_settings SET tv_paths='[]' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("GET", result.URL, nil)
	w = httptest.NewRecorder()
	media.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal("removed root remains accessible")
	}
	if _, err := db.Exec(`UPDATE folder_settings SET tv_paths=? WHERE id=1`, string(roots)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ status, code string }{{"missing", "source_missing"}, {"corrupt", "source_unavailable"}, {"ok", "source_missing"}} {
		if _, err := db.Exec(`UPDATE media_files SET status=? WHERE id=999`, tc.status); err != nil {
			t.Fatal(err)
		}
		if tc.status == "ok" {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}
		req := httptest.NewRequest("POST", "http://localhost:19900/api/channels/999/tune", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+raw)
		recorder := httptest.NewRecorder()
		management.ServeHTTP(recorder, req)
		var failure struct{ Code, Error string }
		if err := json.Unmarshal(recorder.Body.Bytes(), &failure); err != nil || recorder.Code != 422 || failure.Code != tc.code {
			t.Fatalf("source status %s: %d %s", tc.status, recorder.Code, recorder.Body.String())
		}
	}
}

func TestStreamKeyDistinguishesRegeneratedSplit(t *testing.T) {
	start := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	e := schedule.Entry{MediaFileID: 7, StartsAt: start, EndsAt: start.Add(time.Minute), SourceOffsetSeconds: 30}
	plan := playback.Plan{Mode: "transcode"}
	base := streamKey(1, e, plan)
	if streamKey(1, e, plan) != base {
		t.Fatal("identical entries must share a rendition")
	}
	for name, changed := range map[string]schedule.Entry{
		"end":    {MediaFileID: 7, StartsAt: start, EndsAt: start.Add(2 * time.Minute), SourceOffsetSeconds: 30},
		"offset": {MediaFileID: 7, StartsAt: start, EndsAt: start.Add(time.Minute), SourceOffsetSeconds: 90},
	} {
		if streamKey(1, changed, plan) == base {
			t.Fatalf("regenerated %s shares a stale worker", name)
		}
	}
	if streamKey(2, e, plan) == base {
		t.Fatal("channels share a rendition key")
	}
}

func TestSessionCapacityIsRetryable(t *testing.T) {
	db := openTestDB(t)
	raw, key, err := auth.GenerateAPIKey(context.Background(), db, testOwnerID(t, db), "test")
	if err != nil {
		t.Fatal(err)
	}
	sessions := playback.NewSessions(&httpClock{time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)})
	for {
		if _, _, err := sessions.Issue(key.ID, 0); err != nil {
			break
		}
	}
	management := auth.RequireAuth(db, NewRouter(t.TempDir(), t.TempDir(), db, NewPlaybackHTTP(db, sessions, nil)))
	r := httptest.NewRequest("POST", "http://localhost:19900/api/playback/probe", nil)
	r.Header.Set("Authorization", "Bearer "+raw)
	w := httptest.NewRecorder()
	management.ServeHTTP(w, r)
	if w.Code != 503 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("capacity: %d %s", w.Code, w.Body.String())
	}
}

func TestStreamKeySeparatesAudioChoices(t *testing.T) {
	e := schedule.Entry{MediaFileID: 7}
	p := playback.Plan{AudioTrackIndex: 1}
	original := streamKey(1, e, p)
	p.AudioTrackIndex = 2
	if streamKey(1, e, p) == original {
		t.Fatal("audio choices share a worker")
	}
	p.AudioTrackIndex = 1
	p.VolumeOffsetDB = -6
	if streamKey(1, e, p) == original {
		t.Fatal("volume choices share a worker")
	}
}
