package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/playback"
)

func TestTranscodingTestEndpoint(t *testing.T) {
	db := openTestDB(t)
	clock := &httpClock{time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)}
	manager, err := playback.NewManager(t.TempDir(), clock, playback.Runner{}, playback.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	p := NewPlaybackHTTP(db, playback.NewSessions(clock), manager)
	router := NewRouter(t.TempDir(), t.TempDir(), db, p)
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/settings/transcoding/test", strings.NewReader(body)))
		return w
	}

	if w := post(`{"transcoding":"intel"}`); w.Code != 503 {
		t.Fatalf("without a checker the test is unavailable: %d %s", w.Code, w.Body.String())
	}
	dir := t.TempDir()
	good := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(good, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p.Hardware = &playback.Hardware{Executable: good, Device: func() string { return "/dev/dri/renderD128" }}
	w := post(`{"transcoding":"intel"}`)
	var status playback.HardwareStatus
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil || w.Code != 200 || !status.OK || status.Encoder != "intel-qsv" {
		t.Fatalf("%d %s %v", w.Code, w.Body.String(), err)
	}
	if w := post(`{"transcoding":"software"}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("software: %d %s", w.Code, w.Body.String())
	}
	if w := post(`{"transcoding":"intel","decoding":true}`); w.Code != 200 || !strings.Contains(w.Body.String(), "Test decode, scale and encode") {
		t.Fatalf("decode test: %d %s", w.Code, w.Body.String())
	}
	if w := post(`{"transcoding":"software","decoding":true}`); w.Code != 400 {
		t.Fatalf("decode with software: %d, want 400", w.Code)
	}
	for _, body := range []string{`{"transcoding":"rm -rf"}`, `{}`, `{"transcoding":"intel","extra":1}`} {
		if w := post(body); w.Code != 400 {
			t.Errorf("%s: %d, want 400", body, w.Code)
		}
	}
}

func TestThreadTuningEndpointsAndRetuneOnEncoderChange(t *testing.T) {
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
	p.Tuner.Busy = nil
	p.Tuner.Executable = filepath.Join(t.TempDir(), "missing-ffmpeg")
	if w := call(http.MethodPost, "/api/settings/transcoding/threads/tune", ""); w.Code != 202 {
		t.Errorf("tune now: %d %s", w.Code, w.Body.String())
	}
}

func TestServerCapacityEndpointsAndRetuneOnEncoderChange(t *testing.T) {
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
	p.Capacity.Busy = nil
	p.Capacity.Executable = filepath.Join(t.TempDir(), "missing-ffmpeg")
	if w := call(http.MethodPost, "/api/settings/transcoding/capacity/measure", ""); w.Code != 202 {
		t.Errorf("measure now: %d %s", w.Code, w.Body.String())
	}
}
