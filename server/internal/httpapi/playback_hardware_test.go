package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/playback"
)

func TestThreadStatusAndRetuneOnEncoderChange(t *testing.T) {
	db := openTestDB(t)
	clock := &httpClock{time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)}
	manager, err := playback.NewManager(t.TempDir(), clock, playback.Runner{}, playback.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	p := NewPlaybackHTTP(db, playback.NewSessions(clock), manager)
	router := NewRouter(t.TempDir(), t.TempDir(), db, p)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}

	if w := call(http.MethodGet, "/api/settings/transcoding/threads", ""); w.Code != 503 {
		t.Fatalf("without a tuner: %d", w.Code)
	}
	// A busy server skips automatic measurements, which makes the trigger visible without running FFmpeg.
	p.Tuner = &playback.Tuner{Busy: func() bool { return true }}
	var status playback.TuneStatus
	w := call(http.MethodGet, "/api/settings/transcoding/threads", "")
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil || w.Code != 200 || status.Threads != playback.DefaultThreads || status.State != "idle" || status.CPUs < 1 {
		t.Fatalf("%d %s %v", w.Code, w.Body.String(), err)
	}

	call(http.MethodPut, "/api/settings/server", `{"tlsMode":"acme"}`)
	if s := p.Tuner.Status(); s.Detail != "" {
		t.Errorf("an unrelated setting must not re-measure: %q", s.Detail)
	}
	call(http.MethodPut, "/api/settings/server", `{"transcoding":"intel"}`)
	if s := p.Tuner.Status(); !strings.Contains(s.Detail, "stream is playing") {
		t.Errorf("changing the encoder should ask for a measurement: %q", s.Detail)
	}
}

func TestServerCapacityStatusAndRetuneOnEncoderChange(t *testing.T) {
	db := openTestDB(t)
	clock := &httpClock{time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)}
	manager, err := playback.NewManager(t.TempDir(), clock, playback.Runner{}, playback.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	p := NewPlaybackHTTP(db, playback.NewSessions(clock), manager)
	router := NewRouter(t.TempDir(), t.TempDir(), db, p)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	if w := call(http.MethodGet, "/api/settings/transcoding/capacity", ""); w.Code != 503 {
		t.Fatalf("without a capacity: %d", w.Code)
	}

	p.Capacity = &playback.Capacity{Busy: func() bool { return true }}
	p.Capacity.Load(playback.CapacityRecord{MeasuredAt: time.Now(), MaxHeight: 720, MaxFPS: 30, Processes: 3})
	var status playback.CapacityStatus
	w := call(http.MethodGet, "/api/settings/transcoding/capacity", "")
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil || w.Code != 200 || status.MaxHeight != 720 || status.Processes != 3 || status.State != "idle" {
		t.Fatalf("%d %s %v", w.Code, w.Body.String(), err)
	}

	// A busy server skips automatic measurements, which makes the trigger visible without running FFmpeg.
	call(http.MethodPut, "/api/settings/server", `{"tlsMode":"acme"}`)
	if s := p.Capacity.Status(); s.Detail != "" {
		t.Errorf("an unrelated setting must not re-measure: %q", s.Detail)
	}
	call(http.MethodPut, "/api/settings/server", `{"transcoding":"intel"}`)
	if s := p.Capacity.Status(); !strings.Contains(s.Detail, "stream is playing") {
		t.Errorf("changing the encoder should ask for a measurement: %q", s.Detail)
	}
}
