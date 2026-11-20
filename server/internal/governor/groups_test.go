package governor

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Group priority must exclude lower work even with otherwise unused pool slots.
func TestGroupsPauseAndResumeLowerPriorityWork(t *testing.T) {
	g := Fixed(4)
	previous := defaultPool.Swap(g)
	t.Cleanup(func() { defaultPool.Store(previous) })
	library, releaseLibrary := Library(t.Context())
	defer releaseLibrary()
	lowCtx, cancelLow := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancelLow()
	if _, err := g.Acquire(lowCtx, PriorityHigh); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("analysis entered library group: %v", err)
	}
	ticket, err := g.Acquire(library, PriorityLibrary)
	if err != nil {
		t.Fatal(err)
	}
	defer ticket.Release()
	releaseBenchmark := g.Reserve(PriorityBenchmark)
	blocked := make(chan error, 1)
	go func() { blocked <- Checkpoint(library) }()
	select {
	case <-blocked:
		t.Fatal("library did not pause for benchmark")
	case <-time.After(40 * time.Millisecond):
	}
	benchmark, err := g.Acquire(WithPriority(t.Context(), PriorityBenchmark), PriorityBenchmark)
	if err != nil {
		t.Fatal(err)
	}
	benchmark.Release()
	releaseBenchmark()
	releaseBenchmark()
	select {
	case err := <-blocked:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("library did not resume")
	}
	releaseLibrary()
	low, err := g.Acquire(t.Context(), PriorityHigh)
	if err != nil {
		t.Fatal(err)
	}
	low.Release()
}

func TestLibraryRemainsEligibleDuringForegroundPlayback(t *testing.T) {
	g := Fixed(2)
	release := g.ReserveForeground()
	defer release()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	ticket, err := g.Acquire(ctx, PriorityLibrary)
	if err != nil {
		t.Fatal("playback blocked library:", err)
	}
	ticket.Release()
	low, stop := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer stop()
	if _, err := g.Acquire(low, PriorityLow); err == nil {
		t.Fatal("foreground reservation admitted analysis")
	}
}

func TestPausedTimeDoesNotExpireLibraryProbe(t *testing.T) {
	g := Fixed(1)
	previous := defaultPool.Swap(g)
	t.Cleanup(func() { defaultPool.Store(previous) })
	release := g.Reserve(PriorityBenchmark)
	ctx, cancel := WithTimeout(WithPriority(t.Context(), PriorityLibrary), 60*time.Millisecond)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatal("paused timeout expired")
	case <-time.After(120 * time.Millisecond):
	}
	release()
	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatal(ctx.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("active timeout did not expire")
	}
	parent, stop := context.WithCancel(t.Context())
	child, end := WithTimeout(parent, time.Hour)
	defer end()
	stop()
	select {
	case <-child.Done():
	case <-time.After(time.Second):
		t.Fatal("parent cancellation was delayed")
	}
}

// Spare resource capacity must not turn the low-priority backlog into parallel
// FFmpeg/ffprobe jobs; keyframes can preempt loudness within the single lane.
func TestAnalysisUsesOneLaneEvenWithSpareCapacity(t *testing.T) {
	g := Fixed(4)
	g.serialAnalysis = true
	first, err := g.Acquire(t.Context(), PriorityHigh)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	if _, err := g.Acquire(ctx, PriorityHigh); err == nil {
		t.Fatal("parallel analysis entered")
	}
	first.Release()
	second, err := g.Acquire(t.Context(), PriorityLow)
	if err != nil {
		t.Fatal(err)
	}
	second.Release()
}
