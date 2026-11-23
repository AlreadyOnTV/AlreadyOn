package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"github.com/alreadyonapp/alreadyon/server/internal/governor"
	"github.com/alreadyonapp/alreadyon/server/internal/playback"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Gate token issuance and direct tune admission, while keeping UI/control APIs available.
func TestPlaybackStartupGateAndRecovery(t *testing.T) {
	db := openTestDB(t)
	started, finish := make(chan struct{}), make(chan struct{})
	b := &playback.Benchmarks{Context: t.Context(), Governor: governor.Fixed(1), Steps: []func(context.Context){func(ctx context.Context) { close(started); <-finish }}}
	p := NewPlaybackHTTP(db, playback.NewSessions(&httpClock{}), nil)
	p.Benchmarks = b
	b.Start(true)
	<-started
	for _, handler := range []func(*httptest.ResponseRecorder){
		func(w *httptest.ResponseRecorder) {
			p.issueProbe(w, httptest.NewRequest("POST", "/api/playback/probe", nil))
		},
		func(w *httptest.ResponseRecorder) {
			p.tune(w, httptest.NewRequest("POST", "/api/channels/1/tune", strings.NewReader("{}")))
		},
	} {
		w := httptest.NewRecorder()
		handler(w)
		if w.Code != 503 || !strings.Contains(w.Body.String(), "server_starting") || w.Header().Get("Retry-After") != "2" {
			t.Fatalf("gate: %d %s", w.Code, w.Body.String())
		}
	}
	close(finish)
	if err := b.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	release, ok := p.admitPlayback(w, httptest.NewRequest("POST", "/api/channels/1/tune", nil))
	defer release()
	if !ok {
		t.Fatal("startup completion did not admit playback")
	}
}

func TestSettingsNetworkProbeDoesNotCancelFullBenchmark(t *testing.T) {
	entered, stop, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
	b := &playback.Benchmarks{Context: t.Context(), Governor: governor.Fixed(1), StartupSteps: []func(context.Context){}, Steps: []func(context.Context){func(ctx context.Context) {
		close(entered)
		select {
		case <-ctx.Done():
		case <-stop:
		}
		close(stopped)
	}}}
	b.Start(true)
	if err := b.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	b.Start(false)
	<-entered
	db := openTestDB(t)
	raw, _, err := auth.GenerateAPIKey(t.Context(), db, testOwnerID(t, db), "synthetic network benchmark probe")
	if err != nil {
		t.Fatal(err)
	}
	p := NewPlaybackHTTP(db, playback.NewSessions(&httpClock{time.Now()}), nil)
	p.Benchmarks = b
	router := auth.RequireAuth(db, NewRouter(t.TempDir(), t.TempDir(), db, p))
	request := httptest.NewRequest("POST", "http://localhost:19900/api/playback/probe", nil)
	request.Header.Set("Authorization", "Bearer "+raw)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, request)
	if w.Code != 201 {
		t.Fatalf("probe issuance: %d %s", w.Code, w.Body.String())
	}
	var issued struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost:19900/media/0/probe?bytes=16&token="+issued.Token, nil))
	if w.Code != 200 || w.Body.Len() != 16 {
		t.Fatalf("probe payload: %d %s", w.Code, w.Body.String())
	}
	select {
	case <-stopped:
		t.Fatal("settings capability probe cancelled full benchmark")
	default:
	}
	close(stop)
	if err := b.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestManualTranscodingTestCanBeStopped(t *testing.T) {
	entered := make(chan struct{})
	b := &playback.Benchmarks{Context: t.Context(), Governor: governor.Fixed(1), StartupSteps: []func(context.Context){func(context.Context) {}}, MiniSteps: []func(context.Context){func(ctx context.Context) {
		close(entered)
		<-ctx.Done()
	}}}
	// Startup must complete before manual tests can acquire the measurement lock.
	b.Start(true)
	if err := b.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	p := NewPlaybackHTTP(openTestDB(t), playback.NewSessions(&httpClock{time.Now()}), nil)
	p.Benchmarks = b
	w := httptest.NewRecorder()
	p.startBenchmark(w, httptest.NewRequest("POST", "/api/settings/transcoding/benchmark", strings.NewReader(`{"mode":"quick"}`)))
	if w.Code != 202 {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	<-entered
	if _, err := b.Admit(t.Context()); !errors.Is(err, playback.ErrBenchmarkRunning) {
		t.Fatalf("test admitted playback: %v", err)
	}
	w = httptest.NewRecorder()
	p.stopBenchmark(w, httptest.NewRequest("DELETE", "/api/settings/transcoding/benchmark", nil))
	if w.Code != 202 {
		t.Fatalf("stop: %d %s", w.Code, w.Body.String())
	}
	if err := b.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if status := b.Status(); status.Outcome != "stopped" {
		t.Fatal(status)
	}
}
