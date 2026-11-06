package playback

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAutomaticMeasurementYieldsToNewViewer(t *testing.T) {
	var busy atomic.Bool
	ctx, cancel := measurementContext(t.Context(), false, busy.Load)
	defer cancel()
	busy.Store(true)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("automatic benchmark did not yield")
	}
	forced, stop := measurementContext(t.Context(), true, busy.Load)
	defer stop()
	select {
	case <-forced.Done():
		t.Fatal("explicit override unexpectedly cancelled")
	default:
	}
}

func fakeTuneFFmpeg(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

func waitIdle(t *testing.T, tn *Tuner) TuneStatus {
	t.Helper()
	for i := 0; i < 200; i++ {
		if s := tn.Status(); s.State == "idle" {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the measurement did not finish")
	return TuneStatus{}
}

func TestPickThreadsTakesTheSmallestCountNearTheBest(t *testing.T) {
	got := PickThreads([]TuneSample{{2, 0.8}, {4, 1.17}, {8, 1.48}, {16, 1.5}})
	if got != 8 {
		t.Errorf("picked %d, want 8 (16 gains under 10%%)", got)
	}
	if got := PickThreads([]TuneSample{{2, 3}, {4, 3.1}}); got != 2 {
		t.Errorf("a flat curve should keep 2, got %d", got)
	}
	if got := PickThreads(nil); got != DefaultThreads {
		t.Errorf("no samples: %d", got)
	}
}

func TestFairThreadsDivideTheHostWorkerCeilingAcrossActiveJobs(t *testing.T) {
	for active, want := range map[int]int{1: 16, 2: 8, 4: 4} {
		if got := FairThreads(32, 16, active); got != want {
			t.Errorf("%d active jobs: %d threads, want %d", active, got, want)
		}
	}
	if got := FairThreads(8, 4, 4); got != 1 {
		t.Errorf("small host with four active jobs: %d threads, want 1", got)
	}
}

func TestCandidatesStayWithinTheCPUShare(t *testing.T) {
	for limit, want := range map[int][]int{1: {2}, 6: {2, 4}, 18: {2, 4, 8, 16}, 64: {2, 4, 8, 16, 32}} {
		if got := candidatesUpTo(limit); !slices.Equal(got, want) {
			t.Errorf("limit %d: %v, want %v", limit, got, want)
		}
	}
}

func TestTunerMeasuresSavesAndStopsEarlyWhenTheCurveFlattens(t *testing.T) {
	var saved atomic.Int64
	var tried []int
	tn := &Tuner{
		Executable: fakeTuneFFmpeg(t),
		cpus:       32,
		Save:       func(_ context.Context, n int, _ time.Time) { saved.Store(int64(n)) },
		measure: func(_ context.Context, threads int, _, _ string) (float64, error) {
			tried = append(tried, threads)
			return map[int]float64{2: 0.8, 4: 1.2, 8: 1.5, 16: 1.55, 32: 1.56}[threads], nil
		},
	}
	if !tn.Start(false) {
		t.Fatal("first start refused")
	}
	s := waitIdle(t, tn)
	if s.Threads != 8 || saved.Load() != 8 || s.MeasuredAt == nil {
		t.Errorf("status %+v saved %d", s, saved.Load())
	}
	if !slices.Equal(tried, []int{2, 4, 8, 16, 32}) {
		t.Errorf("tried %v", tried)
	}
	if tn.Threads() != 8 {
		t.Errorf("Threads() = %d", tn.Threads())
	}

	// Two flat steps in a row end the search early.
	tried = nil
	flat := &Tuner{
		Executable: fakeTuneFFmpeg(t),
		cpus:       32,
		measure: func(_ context.Context, threads int, _, _ string) (float64, error) {
			tried = append(tried, threads)
			return map[int]float64{2: 1.0, 4: 1.02, 8: 1.03}[threads], nil
		},
	}
	flat.Start(false)
	if s := waitIdle(t, flat); s.Threads != 2 || !slices.Equal(tried, []int{2, 4, 8}) {
		t.Errorf("flat curve: %+v tried %v", s, tried)
	}
}

func TestTunerSkipsWhileStreamsPlayUnlessForced(t *testing.T) {
	tn := &Tuner{
		Executable: fakeTuneFFmpeg(t),
		cpus:       4,
		Busy:       func() bool { return true },
		measure:    func(context.Context, int, string, string) (float64, error) { return 1, nil },
	}
	if tn.Start(false) {
		t.Fatal("must not measure under load")
	}
	if s := tn.Status(); !strings.Contains(s.Detail, "stream is playing") || s.Threads != DefaultThreads {
		t.Errorf("%+v", s)
	}
	if !tn.Start(true) {
		t.Fatal("force should measure")
	}
	waitIdle(t, tn)
}

func TestTunerKeepsTheOldCountWhenMeasuringFails(t *testing.T) {
	tn := &Tuner{
		Executable: fakeTuneFFmpeg(t),
		cpus:       8,
		measure:    func(context.Context, int, string, string) (float64, error) { return 0, context.DeadlineExceeded },
	}
	tn.Load(4, time.Now())
	tn.Start(false)
	s := waitIdle(t, tn)
	if s.Threads != 4 || !strings.Contains(s.Detail, "failed") {
		t.Errorf("%+v", s)
	}
}

func TestTunerSearchesTheSingleProcessBudgetRegardlessOfConcurrency(t *testing.T) {
	var tried []int
	tn := &Tuner{
		Executable: fakeTuneFFmpeg(t),
		cpus:       16,
		measure: func(_ context.Context, threads int, _, _ string) (float64, error) {
			tried = append(tried, threads)
			return float64(threads), nil
		},
	}
	tn.Start(false)
	waitIdle(t, tn)
	if !slices.Equal(tried, []int{2, 4, 8, 16}) {
		t.Errorf("tried %v", tried)
	}
}

func TestJobThreadsReachFFmpeg(t *testing.T) {
	job := fixture(t)
	job.Threads = 6
	args, err := Arguments(job)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.Index(args, "-readrate")
	if i < 2 || args[i-2] != "-threads" || args[i-1] != "6" {
		t.Errorf("decoder threads not applied: %v", args)
	}
	job.Threads = MaxThreads + 1
	if _, err := Arguments(job); err == nil {
		t.Error("a thread count over the maximum must be refused")
	}
}

func TestTunerTreatsTheSoftwareChoiceAsNoEncoder(t *testing.T) {
	var used string
	tn := &Tuner{
		Executable: fakeTuneFFmpeg(t),
		cpus:       2,
		Encoder:    func(context.Context) string { return "software" },
		measure: func(_ context.Context, _ int, encoder, _ string) (float64, error) {
			used = encoder
			return 1, nil
		},
	}
	tn.Start(false)
	waitIdle(t, tn)
	if used != "" {
		t.Errorf("benchmark encoder %q, want none", used)
	}
}
