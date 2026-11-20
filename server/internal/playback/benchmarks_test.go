package playback

import (
	"context"
	"errors"
	"github.com/alreadyonapp/alreadyon/server/internal/governor"
	"sync/atomic"
	"testing"
	"time"
)

func TestStartupBenchmarksSerializeAndGatePlayback(t *testing.T) {
	first, proceed := make(chan struct{}), make(chan struct{})
	var stage atomic.Int32
	b := &Benchmarks{Context: t.Context(), Governor: governor.Fixed(4), Steps: []func(context.Context){
		func(ctx context.Context) { stage.Store(1); close(first); <-proceed },
		func(ctx context.Context) {
			if stage.Load() != 1 {
				t.Error("capacity before tuning")
			}
			stage.Store(2)
		},
		func(ctx context.Context) {
			if stage.Load() != 2 {
				t.Error("limits before capacity")
			}
			stage.Store(3)
		},
	}}
	if b.Start(false) {
		t.Fatal("recurring request bypassed initial startup")
	}
	if !b.Start(true) {
		t.Fatal("startup did not start")
	}
	<-first
	if b.Start(false) {
		t.Fatal("duplicate group started")
	}
	if _, err := b.Admit(t.Context()); !errors.Is(err, ErrServerStarting) {
		t.Fatalf("startup allowed playback: %v", err)
	}
	close(proceed)
	if err := b.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !b.Ready() || stage.Load() != 3 {
		t.Fatal("readiness before group completed")
	}
	release, err := b.Admit(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if b.Start(false) {
		t.Fatal("benchmark raced pending admission")
	}
	release()
	release()
}
func TestRecurringBenchmarksSkipBusyAndYieldBeforeAdmittingPlayback(t *testing.T) {
	entered, stopping, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var busy atomic.Bool
	b := &Benchmarks{Context: t.Context(), Governor: governor.Fixed(4), Busy: busy.Load, Steps: []func(context.Context){
		func(ctx context.Context) { close(entered); <-ctx.Done(); close(stopping); <-stopped },
	}}
	b.ready.Store(true)
	busy.Store(true)
	if b.Start(false) {
		t.Fatal("measured while streaming")
	}
	busy.Store(false)
	if !b.Start(false) {
		t.Fatal("idle benchmark did not start")
	}
	<-entered
	admitted := make(chan error, 1)
	go func() { release, err := b.Admit(t.Context()); defer release(); admitted <- err }()
	<-stopping
	select {
	case <-admitted:
		t.Fatal("playback admitted before benchmark teardown")
	case <-time.After(40 * time.Millisecond):
	}
	close(stopped)
	select {
	case err := <-admitted:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("playback did not resume")
	}
	if !b.Ready() {
		t.Fatal("recurring measurement reinstated startup gate")
	}
}
func TestNextBenchmarkUsesFourHourLocalBoundaries(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ now, want string }{
		{"2026-11-20T00:00:00-05:00", "2026-11-20T04:00:00-05:00"},
		{"2026-11-20T03:59:59-05:00", "2026-11-20T04:00:00-05:00"},
		{"2026-11-20T12:01:00-05:00", "2026-11-20T16:00:00-05:00"},
		{"2026-11-20T23:59:00-05:00", "2026-11-21T00:00:00-05:00"},
		{"2026-03-08T00:00:00-05:00", "2026-03-08T04:00:00-04:00"},
		{"2026-11-01T00:00:00-04:00", "2026-11-01T04:00:00-05:00"},
	} {
		now, _ := time.Parse(time.RFC3339, tc.now)
		if got := NextBenchmark(now.In(zone)).Format(time.RFC3339); got != tc.want {
			t.Errorf("%s: %s != %s", tc.now, got, tc.want)
		}
	}
}

// A due clock request waits for idle without blocking normal operation and
// never overlaps an in-flight group. Short controlled intervals avoid a 4h wait.
func TestPeriodicBenchmarkDefersUntilIdle(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var busy atomic.Bool
	busy.Store(true)
	entered := make(chan struct{})
	b := &Benchmarks{Context: ctx, Governor: governor.Fixed(1), Busy: busy.Load, Steps: []func(context.Context){func(c context.Context) { close(entered); <-c.Done() }}}
	b.ready.Store(true)
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.runPeriodic(ctx, func(now time.Time) time.Time { return now.Add(5 * time.Millisecond) }, 5*time.Millisecond)
	}()
	select {
	case <-entered:
		t.Fatal("periodic benchmark entered during playback")
	case <-time.After(40 * time.Millisecond):
	}
	busy.Store(false)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("deferred benchmark did not start when idle")
	}
	if b.Start(false) {
		t.Fatal("a second periodic group overlapped")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("periodic loop did not stop")
	}
}
