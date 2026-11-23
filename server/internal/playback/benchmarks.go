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
var ErrBenchmarkRunning = errors.New("Video transcoding test is running. Try playback again when it finishes.")

// Benchmarks owns startup readiness and one complete measurement group. Admission
// and start decisions share a mutex so a new viewer cannot race an idle check.
type Benchmarks struct {
	Context       context.Context
	Governor      *governor.Governor
	Busy          func() bool
	Steps         []func(context.Context)
	StartupSteps  []func(context.Context)
	MiniSteps     []func(context.Context)
	StartupBudget time.Duration
	mode          string
	startedAt     time.Time
	pendingMode   string
	pendingManual bool
	requestWorker bool
	generation    uint64
	outcome       string
	manual        bool
	lastManual    bool
	stopRequested bool
	mu            sync.Mutex
	cancel        context.CancelFunc
	done          chan struct{}
	admissions    int
	ready         atomic.Bool
}

func (b *Benchmarks) Ready() bool   { return b.ready.Load() }
func (b *Benchmarks) Running() bool { b.mu.Lock(); defer b.mu.Unlock(); return b.done != nil }
func (b *Benchmarks) Start(startup bool) bool {
	mode := "full"
	if startup {
		mode = "mini"
	}
	return b.start(startup, mode, false, false)
}
func (b *Benchmarks) start(startup bool, mode string, requested, manual bool) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !startup && !requested && b.pendingMode != "" {
		return false
	}
	if b.done != nil || (!startup && (!b.ready.Load() || b.admissions > 0 || b.Busy != nil && b.Busy())) {
		return false
	}
	ctx, cancel := context.WithCancel(b.Context)
	if mode == "mini" {
		budget := b.StartupBudget
		if budget <= 0 {
			budget = 120 * time.Second
		}
		cancel()
		ctx, cancel = context.WithTimeout(b.Context, budget)
	}
	done := make(chan struct{})
	b.done, b.cancel = done, cancel
	b.mode, b.startedAt = mode, time.Now()
	b.manual = manual
	b.lastManual = manual
	b.stopRequested = false
	b.outcome = ""
	// Reserve before launching so no lower-priority work can enter the startup gap.
	release := b.Governor.Reserve(governor.PriorityBenchmark)
	ctx = governor.WithPriority(ctx, governor.PriorityBenchmark)
	go func() {
		defer cancel()
		_, finish := activity.Default.Start("Performance Benchmarks", "benchmarking", 0)
		steps := b.Steps
		if mode == "mini" {
			if startup && b.StartupSteps != nil {
				steps = b.StartupSteps
			} else if b.MiniSteps != nil {
				steps = b.MiniSteps
			} else if b.StartupSteps != nil {
				steps = b.StartupSteps
			}
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
		b.outcome = "completed"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			b.outcome = "time-limit"
		} else if ctx.Err() != nil {
			if b.stopRequested {
				b.outcome = "stopped"
			} else {
				b.outcome = "interrupted"
			}
		}
		b.done, b.cancel = nil, nil
		b.manual, b.stopRequested = false, false
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
	if b.manual || b.pendingManual {
		b.mu.Unlock()
		return func() {}, ErrBenchmarkRunning
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

// Mini and Full replace an in-flight group. Teardown completes before the new
// group starts; generation checks prevent a superseded request from starting.
func (b *Benchmarks) Mini()          { b.request("mini", true, true) }
func (b *Benchmarks) AutomaticMini() { b.request("mini", true, false) }
func (b *Benchmarks) Full()          { b.request("full", true, true) }
func (b *Benchmarks) request(mode string, replace, manual bool) {
	b.mu.Lock()
	b.generation++
	b.pendingMode = mode
	b.pendingManual = manual
	if replace && b.cancel != nil {
		b.cancel()
	}
	if b.requestWorker {
		b.mu.Unlock()
		return
	}
	b.requestWorker = true
	b.mu.Unlock()
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			b.mu.Lock()
			if b.Context.Err() != nil {
				b.pendingMode = ""
				b.requestWorker = false
				b.mu.Unlock()
				return
			}
			mode, revision := b.pendingMode, b.generation
			manual := b.pendingManual
			if mode == "" {
				b.requestWorker = false
				b.mu.Unlock()
				return
			}
			b.mu.Unlock()
			if b.start(false, mode, true, manual) {
				b.mu.Lock()
				if b.generation == revision {
					b.pendingMode = ""
					b.pendingManual = false
					b.requestWorker = false
					b.mu.Unlock()
					return
				}
				if b.cancel != nil {
					b.cancel()
				}
				b.mu.Unlock()
			}
			select {
			case <-b.Context.Done():
			case <-ticker.C:
			}
		}
	}()
}

// Stop cancels an active group and any queued manual request. Completed cases
// remain persisted by the calibration step; cancellation cleans each subprocess
// and its temporary output directory before the group becomes idle.
func (b *Benchmarks) Stop() {
	b.mu.Lock()
	b.generation++
	b.pendingMode = ""
	b.pendingManual = false
	b.stopRequested = b.done != nil
	b.outcome = "stopped"
	if b.cancel != nil {
		b.cancel()
	}
	b.mu.Unlock()
}

type BenchmarkStatus struct {
	State         string            `json:"state"`
	Mode          string            `json:"mode"`
	StartedAt     time.Time         `json:"startedAt,omitempty"`
	Outcome       string            `json:"outcome"`
	Profiles      []ProfileCapacity `json:"profiles,omitempty"`
	Tasks         []activity.Task   `json:"tasks"`
	ActiveStreams int               `json:"activeStreams"`
	QuickAt       time.Time         `json:"quickAt,omitempty"`
	FullAt        time.Time         `json:"fullAt,omitempty"`
	QuickProfiles int               `json:"quickProfiles"`
	FullProfiles  int               `json:"fullProfiles"`
	RecommendFull bool              `json:"recommendFull"`
	Manual        bool              `json:"manual"`
}

func (b *Benchmarks) Status() BenchmarkStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	state, mode := "idle", b.mode
	if b.done != nil {
		state = "running"
	}
	if b.pendingMode != "" {
		state = "queued"
		mode = b.pendingMode
	}
	tasks := []activity.Task{}
	for _, task := range activity.Default.Snapshot(time.Now()) {
		switch task.Name {
		case "Performance Benchmarks", "Representative Capacity Validation", "CPU fallback tuning", "Pipeline analysis":
			tasks = append(tasks, task)
		}
	}
	return BenchmarkStatus{State: state, Mode: mode, StartedAt: b.startedAt, Outcome: b.outcome, Tasks: tasks, Manual: b.lastManual}
}
