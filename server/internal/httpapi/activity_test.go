package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/activity"
	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"github.com/alreadyonapp/alreadyon/server/internal/playback"
	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
)

func TestServerActivityLifecycleCommercialsAndAuthorization(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`INSERT INTO channels(id,number,name) VALUES(999,999,'Synthetic')`); err != nil {
		t.Fatal(err)
	}
	raw, key, err := auth.GenerateAPIKey(context.Background(), db, testOwnerID(t, db), "activity test")
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := auth.GenerateAPIKey(context.Background(), db, testOwnerID(t, db), "other viewer")
	if err != nil {
		t.Fatal(err)
	}
	clock := &httpClock{time.Now()}
	worker := &activityFallbackWorker{updates: make(chan playback.Plan), applied: make(chan struct{})}
	manager, err := playback.NewManager(t.TempDir(), clock, worker, playback.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	p := NewPlaybackHTTP(db, playback.NewSessions(clock), manager)
	p.Clock = clock
	handler := auth.RequireAuth(db, NewRouter(t.TempDir(), t.TempDir(), db, p))
	call := func(method, path, body, credential string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.RemoteAddr = "192.0.2.10:4567"
		r.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) Chrome/130.0")
		if credential != "" {
			r.Header.Set("Authorization", "Bearer "+credential)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	snapshot := func() []viewerActivity {
		t.Helper()
		w := call("GET", "/api/server/activity", "", raw)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), raw) || strings.Contains(w.Body.String(), "streamKey") || strings.Contains(w.Body.String(), "token") {
			t.Fatal("activity leaked credentials")
		}
		var v struct{ Sessions []viewerActivity }
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v.Sessions
	}
	start := func(kind, name string, plans ...playback.Plan) string {
		t.Helper()
		token, s, err := p.Sessions.Issue(key.ID, 999)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = p.Sessions.Claim(token, key.ID, 999); err != nil {
			t.Fatal(err)
		}
		streamKey := s.ID
		plan := playback.Plan{Mode: "direct"}
		if len(plans) > 0 {
			plan = plans[0]
		}
		_, err = manager.Acquire(context.Background(), streamKey, s.ID, playback.Job{Plan: plan, Entry: schedule.Entry{Kind: schedule.Kind(kind)}, Details: playback.Details{FileName: name, VideoReason: "Video reason for " + name, AudioReason: "Audio reason for " + name, SourceAudio: &playback.AudioDetails{}}})
		if err != nil {
			t.Fatal(err)
		}
		if err = p.Sessions.Bind(token, streamKey); err != nil {
			t.Fatal(err)
		}
		return token
	}
	report := func(token, viewer, credential string, intend int) {
		t.Helper()
		w := call("POST", "/api/playback/activity", fmt.Sprintf(`{"viewerId":%q,"channelId":999,"token":%q,"profileId":1,"latencyMs":12}`, viewer, token), credential)
		if w.Code != intend {
			t.Fatalf("report %d: %s", w.Code, w.Body.String())
		}
	}
	viewer := "viewer-1234567890"
	token := start("program", "Synthetic Program.mp4")
	if len(snapshot()) != 0 {
		t.Fatal("prefetch appeared as viewer")
	}
	if w := call("GET", "/api/server/activity", "", ""); w.Code != 401 {
		t.Fatal("unprotected activity", w.Code)
	}
	report(token, viewer, other, 401)
	report(token, viewer, raw, 204)
	first := snapshot()
	if len(first) != 1 || first[0].Profile != "Admin" || first[0].SourceIP != "192.0.2.10" || first[0].BandwidthMbps != nil {
		t.Fatalf("first=%+v", first)
	}
	counter := &mediaCounter{ResponseWriter: httptest.NewRecorder(), sessions: p.Sessions, token: token}
	if _, err := counter.Write(make([]byte, 625000)); err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(5 * time.Second)
	report(token, viewer, raw, 204)
	program := snapshot()[0]
	if program.VideoReason == "" || program.AudioReason == "" {
		t.Fatal("missing per-track playback reasons")
	}
	if program.BandwidthMbps == nil || *program.BandwidthMbps != 1 {
		t.Fatalf("bandwidth=%v", program.BandwidthMbps)
	}
	ad := start("commercial", "Do Not Display.avi")
	report(ad, viewer, raw, 204)
	during := snapshot()[0]
	if !during.Commercial || during.Video != program.Video || during.VideoReason != program.VideoReason || during.AudioReason != program.AudioReason || *during.BandwidthMbps != *program.BandwidthMbps || during.UpdatedAt != program.UpdatedAt {
		t.Fatalf("commercial changed program: %+v", during)
	}
	// A second player joining during an ad remains hidden until its first program.
	report(ad, "viewer-0987654321", raw, 204)
	if len(snapshot()) != 1 {
		t.Fatal("ad created a row")
	}
	report(token, viewer, raw, 204)
	resumed := snapshot()[0]
	if resumed.Commercial || resumed.Video != program.Video || resumed.BandwidthMbps != nil {
		t.Fatalf("resumed=%+v", resumed)
	}
	// Another principal cannot end this viewer even if it knows the public ID.
	call("DELETE", "/api/playback/activity", `{"viewerId":"viewer-1234567890"}`, other)
	if len(snapshot()) != 1 {
		t.Fatal("cross-principal deletion")
	}
	if w := call("DELETE", "/api/playback/activity", `{"viewerId":"viewer-1234567890"}`, raw); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if len(snapshot()) != 0 {
		t.Fatal("ended row retained")
	}
	report(token, viewer, raw, 410)
	report(token, "viewer-1122334455", raw, 204)
	clock.now = clock.now.Add(activityTTL)
	if len(snapshot()) != 0 {
		t.Fatal("stale viewer retained")
	}
	session, _ := p.Sessions.Validate(token, 999)
	if _, err := manager.Inspect(session.StreamKey, session.ID); err == nil {
		t.Fatal("dashboard renewed lease")
	}

	manager.Sweep()
	hardware := start("program", "GPU Then CPU.mp4", playback.Plan{Mode: "transcode", Encoder: playback.EncoderIntelVAAPI})
	report(hardware, "viewer-fallback-123", raw, 204)
	if got := snapshot()[0].Encoder; !strings.Contains(got, "VAAPI") {
		t.Fatalf("hardware: %s", got)
	}
	worker.updates <- playback.Plan{Mode: "transcode"}
	<-worker.applied
	report(hardware, "viewer-fallback-123", raw, 204)
	if got := snapshot()[0].Encoder; got != "Software" {
		t.Fatalf("fallback misreported as %s", got)
	}
}

func TestBackgroundActivityIsCountedAndRemoved(t *testing.T) {
	tracker := &activity.Tracker{}
	id, finish := tracker.Start("Channel Schedule Maintenance", "channels", 4)
	tracker.Progress(id, 1, 4)
	rows := tracker.Snapshot(time.Now().Add(2 * time.Second))
	if len(rows) != 1 || rows[0].Percent == nil || *rows[0].Percent != 25 || rows[0].RuntimeSeconds < 2 {
		t.Fatalf("%+v", rows)
	}
	finish()
	if len(tracker.Snapshot(time.Now())) != 0 {
		t.Fatal("finished task retained")
	}
	_, finish = tracker.Start("Discovery", "discovering files", 0)
	defer finish()
	if tracker.Snapshot(time.Now())[0].Percent != nil {
		t.Fatal("unknown total fabricated percentage")
	}
}

var _ http.ResponseWriter = (*mediaCounter)(nil)

func TestActivityCountsOnlySuccessfulMediaPayload(t *testing.T) {
	clock := &httpClock{time.Now()}
	sessions := playback.NewSessions(clock)
	token, _, err := sessions.Issue(1, 999)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []int{200, 206, 404, 503} {
		w := &mediaCounter{ResponseWriter: httptest.NewRecorder(), sessions: sessions, token: token}
		w.WriteHeader(code)
		if _, err := w.Write([]byte("12345")); err != nil {
			t.Fatal(err)
		}
	}
	s, err := sessions.Validate(token, 999)
	if err != nil || s.MediaBytes != 10 {
		t.Fatalf("bytes %d, err %v", s.MediaBytes, err)
	}
	sessions.Revoke(token)
	sessions.AddMediaBytes(token, 100)
	if _, err := sessions.Validate(token, 999); err == nil {
		t.Fatal("byte accounting resurrected revoked session")
	}
}

type activityFallbackWorker struct {
	updates chan playback.Plan
	applied chan struct{}
}

func (w *activityFallbackWorker) Run(ctx context.Context, j playback.Job) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case plan := <-w.updates:
			j.Report(plan, playback.Pipeline{})
			w.applied <- struct{}{}
		}
	}
}
