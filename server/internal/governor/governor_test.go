package governor

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/logging"
)

// Recovery must allow small hosts to progress without resuming on one idle blip.
func TestSingleCPURecoversOnlyAfterQuietWindows(t *testing.T) {
	g := New(1)
	g.cores = 1
	samples := make(chan Sample)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	read := func() (Sample, error) {
		select {
		case s := <-samples:
			return s, nil
		case <-ctx.Done():
			return Sample{}, errors.New("done")
		}
	}
	go g.Run(ctx, read, time.Millisecond)
	samples <- Sample{Total: 1000, Busy: 0}
	samples <- Sample{Total: 2000, Busy: 950}
	waitFor(t, "pressure pauses every worker", func() bool { return g.Target() == 0 })
	for i := 3; i <= 3; i++ {
		samples <- Sample{Total: uint64(i * 1000), Busy: 950}
		if g.Target() != 0 {
			t.Fatal("resumed before two quiet samples")
		}
	}
	samples <- Sample{Total: 4000, Busy: 950}
	waitFor(t, "single CPU recovers after quiet samples", func() bool { return g.Target() == 1 })
}

func TestPercentAndProcStatParsing(t *testing.T) {
	if p, ok := Percent(Sample{Busy: 100, Total: 400}, Sample{Busy: 160, Total: 500}); !ok || p != 60 {
		t.Fatalf("percent = %v %v, want 60", p, ok)
	}
	if _, ok := Percent(Sample{Busy: 5, Total: 10}, Sample{Busy: 5, Total: 10}); ok {
		t.Fatal("an empty window should not produce a percentage")
	}
}

func TestRunFollowsHostLoad(t *testing.T) {
	g := newGovernor(8, 8)
	var util atomic.Int64 // percent of busy time per window
	util.Store(5)
	var busy, total uint64
	read := func() (Sample, error) {
		total += 1000
		busy += uint64(util.Load()) * 10
		return Sample{Busy: busy, Total: total}, nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go g.Run(ctx, read, 5*time.Millisecond)
	waitFor(t, "ramp up on an idle host", func() bool { return g.Target() == 8 })
	util.Store(99)
	waitFor(t, "fully yield under load", func() bool { return g.Target() == 0 })
	if g.CPU() < 90 {
		t.Fatalf("cpu = %.0f, want the loaded reading", g.CPU())
	}
}

func TestFixedPoolNeverChanges(t *testing.T) {
	g := Fixed(4)
	g.Run(t.Context(), func() (Sample, error) { return Sample{}, nil }, time.Millisecond)
	if g.Target() != 4 || g.Max() != 4 {
		t.Fatalf("fixed pool = %d of %d", g.Target(), g.Max())
	}
}

func TestForegroundReservationsNestAndRecover(t *testing.T) {
	g := newGovernor(4, 3)
	g.target = 3
	releaseFirst := g.ReserveForeground()
	releaseSecond := g.ReserveForeground()
	if g.Target() != 0 || g.Limit() != LimitPlayback {
		t.Fatalf("reserved pool = %d %q", g.Target(), g.Limit())
	}
	releaseFirst()
	if g.Target() != 0 || g.Limit() != LimitPlayback {
		t.Fatal("one stream released another stream's reservation")
	}
	releaseFirst() // Releases are idempotent.
	releaseSecond()
	if g.Target() != 0 || g.Limit() != LimitRecovery {
		t.Fatalf("released pool = %d %q", g.Target(), g.Limit())
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out: %s", what)
}

func TestAcquireWaitsForTheTargetAndWakesOnRelease(t *testing.T) {
	g := newGovernor(4, 4) // target starts at one
	first, err := g.Acquire(t.Context(), PriorityLow)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan *Ticket, 1)
	go func() {
		tk, _ := g.Acquire(t.Context(), PriorityLow)
		got <- tk
	}()
	select {
	case <-got:
		t.Fatal("a second job started beyond the target")
	case <-time.After(50 * time.Millisecond):
	}
	first.Release()
	select {
	case tk := <-got:
		tk.Release()
	case <-time.After(2 * time.Second):
		t.Fatal("a waiting job did not start after a release")
	}
	ctx, cancel := context.WithCancel(t.Context())
	held, _ := g.Acquire(t.Context(), PriorityLow)
	cancel()
	if _, err := g.Acquire(ctx, PriorityLow); err == nil {
		t.Fatal("a cancelled wait should fail")
	}
	held.Release()
}

func TestHighPriorityIsAdmittedPastLowPriorityJobs(t *testing.T) {
	g := newGovernor(4, 4) // target starts at one
	low, err := g.Acquire(t.Context(), PriorityLow)
	if err != nil {
		t.Fatal(err)
	}
	defer low.Release()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	high, err := g.Acquire(ctx, PriorityHigh)
	if err != nil {
		t.Fatalf("a high-priority job waited behind a low-priority one: %v", err)
	}
	high.Release()
}

func TestRunReportsTheLimitAndShedsWorkersUnderMemoryPressure(t *testing.T) {
	g := newGovernor(4, 4)
	var total, busy, wait, avail atomic.Uint64
	avail.Store(50)
	mode := atomic.Int64{} // 0 idle, 1 disk-bound, 2 low memory
	read := func() (Sample, error) {
		total.Add(1000)
		disk := 0.0
		switch mode.Load() {
		case 1:
			disk = 90
			busy.Add(950)
			wait.Add(700)
		case 2:
			busy.Add(100)
			avail.Store(5)
		default:
			busy.Add(100)
		}
		return Sample{Busy: busy.Load(), Total: total.Load(), IOWait: wait.Load(), DiskUtil: disk, MemAvailable: avail.Load(), MemTotal: 100}, nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go g.Run(ctx, read, 5*time.Millisecond)
	waitFor(t, "ramp to the maximum with nothing limiting", func() bool { return g.Target() == 4 && g.Limit() == "" })
	mode.Store(1)
	waitFor(t, "disk busy time is named", func() bool { return g.Limit() == LimitDisk && g.Target() == 0 })
	mode.Store(2)
	waitFor(t, "low memory is named", func() bool { return g.Limit() == LimitRAM })
	waitFor(t, "pool log names work and limit", func() bool {
		for _, entry := range logging.Entries(nil, 200) {
			if strings.Contains(entry.Message, "media analysis pool (loudness detection, keyframe analysis)") && strings.Contains(entry.Message, "claimed work: idle") && strings.Contains(entry.Message, "limit: available RAM") && strings.Contains(entry.Message, "disk") {
				return true
			}
		}
		return false
	})
}

func TestWorkSummaryNamesClaimsAndPausedJobs(t *testing.T) {
	g := Fixed(4)
	first, _ := g.Acquire(t.Context(), PriorityLow)
	second, _ := g.Acquire(t.Context(), PriorityLow)
	high, _ := g.Acquire(t.Context(), PriorityHigh)
	pending, _ := g.Acquire(t.Context(), PriorityLow)
	first.SetTask("loudness detection")
	second.SetTask("loudness detection")
	high.SetTask("keyframe analysis")
	g.mu.Lock()
	second.stopped = true
	got := g.workSummaryLocked()
	g.mu.Unlock()
	want := "keyframe analysis=1, loudness detection=1, loudness detection (paused)=1, pending job claims=1"
	if got != want {
		t.Fatalf("%q != %q", got, want)
	}
	first.Release()
	second.Release()
	high.Release()
	pending.Release()
	g.mu.Lock()
	got = g.workSummaryLocked()
	g.mu.Unlock()
	if got != "idle" {
		t.Fatal(got)
	}
	if reasonLabel("") != "none" || reasonLabel(LimitDisk) != "disk utilization" {
		t.Fatal("ambiguous limit reason")
	}
}
