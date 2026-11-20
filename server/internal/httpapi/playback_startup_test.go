package httpapi

import (
	"context"
	"github.com/alreadyonapp/alreadyon/server/internal/governor"
	"github.com/alreadyonapp/alreadyon/server/internal/playback"
	"net/http/httptest"
	"strings"
	"testing"
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
