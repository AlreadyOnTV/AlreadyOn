package playback

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/activity"
	"github.com/alreadyonapp/alreadyon/server/internal/governor"
)

var ErrServerStarting = errors.New("Server starting, please wait.")

// Benchmarks owns startup readiness and one complete measurement group. Admission
// and start decisions share a mutex so a new viewer cannot race an idle check.
type Benchmarks struct {
	Context         context.Context
	Governor        *governor.Governor
	Busy            func() bool
	Steps           []func(context.Context)
	StartupSteps    []func(context.Context)
	StartupBudget   time.Duration
	mu              sync.Mutex
	cancel          context.CancelFunc
	done            chan struct{}
	admissions      int
	ready           atomic.Bool
	requested       atomic.Bool
	requestRevision atomic.Uint64
}

func (b *Benchmarks) Ready() bool   { return b.ready.Load() }
func (b *Benchmarks) Running() bool { b.mu.Lock(); defer b.mu.Unlock(); return b.done != nil }
func (b *Benchmarks) Start(startup bool) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done != nil || (!startup && (!b.ready.Load() || b.admissions > 0 || b.Busy != nil && b.Busy())) {
		return false
	}
	ctx, cancel := context.WithCancel(b.Context)
	if startup {
		budget := b.StartupBudget
		if budget <= 0 {
			budget = 120 * time.Second
		}
		cancel()
		ctx, cancel = context.WithTimeout(b.Context, budget)
	}
	done := make(chan struct{})
	b.done, b.cancel = done, cancel
	// Reserve before launching so no lower-priority work can enter the startup gap.
	release := b.Governor.Reserve(governor.PriorityBenchmark)
	ctx = governor.WithPriority(ctx, governor.PriorityBenchmark)
	go func() {
		defer cancel()
		_, finish := activity.Default.Start("Performance Benchmarks", "benchmarking", 0)
		steps := b.Steps
		if startup && b.StartupSteps != nil {
			steps = b.StartupSteps
		}
		for _, step := range steps {
			if ctx.Err() != nil {
				break
			}
			step(ctx)
		}
		finish()
		release()
		b.mu.Lock()
		if startup && b.Context.Err() == nil {
			b.ready.Store(true)
		}
		b.done, b.cancel = nil, nil
		close(done)
		b.mu.Unlock()
	}()
	return true
}
func (b *Benchmarks) Wait(ctx context.Context) error {
	b.mu.Lock()
	done := b.done
	b.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

// Admit cancels a recurring measurement and waits for subprocess teardown before
// letting playback enter. The caller releases its reservation after admission.
func (b *Benchmarks) Admit(ctx context.Context) (func(), error) {
	b.mu.Lock()
	if !b.ready.Load() {
		b.mu.Unlock()
		return func() {}, ErrServerStarting
	}
	b.admissions++
	done := b.done
	if b.cancel != nil {
		b.cancel()
	}
	b.mu.Unlock()
	var once sync.Once
	release := func() { once.Do(func() { b.mu.Lock(); b.admissions--; b.mu.Unlock() }) }
	if done != nil {
		select {
		case <-ctx.Done():
			release()
			return func() {}, ctx.Err()
		case <-done:
		}
	}
	return release, nil
}

// NextBenchmark is the next local four-hour boundary, including across DST.
// Walking real minutes avoids manufacturing nonexistent local wall-clock times.
func NextBenchmark(now time.Time) time.Time {
	next := now.Truncate(time.Minute).Add(time.Minute)
	for next.Hour()%4 != 0 || next.Minute() != 0 {
		next = next.Add(time.Minute)
	}
	return next
}
func (b *Benchmarks) RunPeriodic(ctx context.Context) {
	b.runPeriodic(ctx, NextBenchmark, 30*time.Second)
}
func (b *Benchmarks) runPeriodic(ctx context.Context, next func(time.Time) time.Time, idleRetry time.Duration) {
	for ctx.Err() == nil {
		timer := time.NewTimer(time.Until(next(time.Now())))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		// Keep only one pending request while viewers are active; no catch-up burst.
		for !b.Start(false) {
			if b.Running() {
				break
			} // An in-flight group also satisfies this boundary.
			timer.Reset(idleRetry)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		if b.Wait(ctx) != nil {
			return
		}
	}
}

// Request coalesces settings/manual triggers into one pending idle-only rerun.
func (b *Benchmarks) Request() {
	b.requestRevision.Add(1)
	if b.Start(false) {
		return
	}
	if !b.requested.CompareAndSwap(false, true) {
		return
	}
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-b.Context.Done():
				b.requested.Store(false)
				return
			case <-ticker.C:
				revision := b.requestRevision.Load()
				if b.Start(false) {
					b.requested.Store(false)
					if b.requestRevision.Load() != revision {
						b.Request()
					}
					return
				}
			}
		}
	}()
}
