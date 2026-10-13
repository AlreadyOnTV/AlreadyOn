// Package governor meters background media subprocess CPU time and yields all
// analysis under host pressure. Playback does not enter this pool.
package governor

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/logging"
)

const (
	// HighPercent is the sampled host pressure trigger, not a hard host limit.
	HighPercent = 80.0
	// DefaultInterval is the CPU sampling window.
	DefaultInterval = time.Second
)

// Sample is cumulative CPU time in clock ticks since boot (Busy includes
// IOWait) plus the memory figures at the moment it was read.
type Sample struct {
	Busy, Total, IOWait    uint64
	MemAvailable, MemTotal uint64
	DiskUtil, NetworkMbps  float64
	SwapIn, SwapOut        uint64
}

// MinMemoryAvailablePercent is the available-memory floor: below it all
// analysis yields and reports RAM as the limit.
const MinMemoryAvailablePercent = 20

// SwapActivityThresholdBytesPerSecond avoids treating isolated page movement as pressure.
const SwapActivityThresholdBytesPerSecond = 1 << 20

const (
	swapPressureSamples        = 3
	swapRecoveryBytesPerSecond = SwapActivityThresholdBytesPerSecond / 2
)

// What is holding the pool back, as reported by Limit.
const (
	LimitRecovery = "recovery"
	LimitMetrics  = "metrics"
	LimitCPU      = "cpu"
	LimitDisk     = "disk"
	LimitRAM      = "ram"
	LimitNetwork  = "network"
	LimitSwap     = "swap"
	LimitPlayback = "playback"
)

// Reader returns the current host CPU sample.
type Reader func() (Sample, error)

// Percent is the share of CPU time busy between two samples.
func Percent(prev, cur Sample) (float64, bool) {
	if cur.Total <= prev.Total || cur.Busy < prev.Busy {
		return 0, false
	}
	return 100 * float64(cur.Busy-prev.Busy) / float64(cur.Total-prev.Total), true
}

// Priorities order tickets when the target shrinks: lower numbers keep running
// and higher ones are paused first.
const (
	PriorityHigh = 0
	PriorityLow  = 1
)

// Governor holds ranked background claims, CPU credit and host-pressure state.
// All attached analysis processes share one CPU-time budget.
type Governor struct {
	mu         sync.Mutex
	cores      int
	max        int
	target     int
	fixed      bool
	cpu        float64
	limit      string
	next       uint64
	tickets    map[*Ticket]struct{}
	changed    chan struct{} // closed and replaced whenever a waiter might proceed
	budget     bool
	throttled  bool
	credit     time.Duration
	quiet      int
	allowance  float64
	charged    time.Duration
	foreground int
}

// Ticket is one running job's claim on the pool. The job may pause with it
// (SIGSTOP) when it ranks below the target.
type Ticket struct {
	task    string
	g       *Governor
	prio    int
	seq     uint64
	pgid    int
	stopped bool
	cpu     time.Duration
}

type swapPressureState struct {
	highSamples  int
	clearSamples int
	limited      bool
}

func (s *swapPressureState) update(prev, cur Sample, interval time.Duration) bool {
	rate := swapActivityBytesPerSecond(prev, cur, interval)
	if rate >= SwapActivityThresholdBytesPerSecond {
		s.highSamples++
		s.clearSamples = 0
		if s.highSamples >= swapPressureSamples {
			s.limited = true
		}
		return s.limited
	}
	s.highSamples = 0
	if !s.limited {
		return false
	}
	if rate < swapRecoveryBytesPerSecond {
		s.clearSamples++
		if s.clearSamples >= swapPressureSamples {
			s.limited = false
			s.clearSamples = 0
		}
	} else {
		s.clearSamples = 0
	}
	return s.limited
}

// New creates the shared multicore background pool. One logical CPU's worth
// of capacity remains outside the pool; this is a capacity reservation, not pinning.
func New(most int) *Governor {
	cores := runtime.NumCPU()
	g := newGovernor(cores, min(most, max(1, cores-1)))
	g.budget = true
	g.allowance = cpuCapacity(cores)
	return g
}

func cpuCapacity(cores int) float64 {
	return min(float64(max(1, cores))*HighPercent/100, float64(max(1, cores-1)))
}

func newGovernor(cores, most int) *Governor {
	most = max(1, min(most, cores))
	return &Governor{cores: cores, max: most, target: 1, tickets: map[*Ticket]struct{}{}, changed: make(chan struct{})}
}

// Fixed is a pool of exactly n workers that never changes.
func Fixed(n int) *Governor {
	g := newGovernor(n, n)
	g.target, g.fixed = g.max, true
	return g
}

// Target is how many jobs may run now.
func (g *Governor) Target() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.target
}

// Max is the most jobs the pool will ever allow, which sizes the worker count.
func (g *Governor) Max() int { return g.max }

// Limit reports host pressure, recovery hysteresis or unavailable metrics.
// An empty reason means the pool is available, still subject to its CPU budget.
func (g *Governor) Limit() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.limit
}

// ReserveForeground pauses every governed background task while a playback
// pipeline needs the container's full CPU quota. Nested streams share the pause;
// the returned release function is safe to call more than once.
func (g *Governor) ReserveForeground() func() {
	g.mu.Lock()
	g.foreground++
	g.target, g.quiet, g.limit = 0, 0, LimitPlayback
	g.applyLocked()
	g.signalLocked()
	g.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			g.foreground--
			if g.foreground == 0 {
				g.limit = LimitRecovery
			}
			g.signalLocked()
			g.mu.Unlock()
		})
	}
}

// CPU is the last sampled host CPU percentage.
func (g *Governor) CPU() float64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.cpu
}

// Acquire waits until fewer jobs are running than the target and returns a
// ticket for the caller's job. Release it when the job ends.
func (g *Governor) Acquire(ctx context.Context, prio int) (*Ticket, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		g.mu.Lock()
		if g.countAtOrAboveLocked(prio) < g.target {
			g.next++
			t := &Ticket{g: g, prio: prio, seq: g.next}
			g.tickets[t] = struct{}{}
			g.applyLocked()
			g.mu.Unlock()
			return t, nil
		}
		wait := g.changed
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-wait:
		}
	}
}

// countAtOrAboveLocked counts the tickets that outrank or equal prio. A job is
// admitted while fewer than the target of those are running, so higher
// priority work gets in even when lower priority jobs fill the pool (they are
// then paused by applyLocked).
func (g *Governor) countAtOrAboveLocked(prio int) int {
	n := 0
	for t := range g.tickets {
		if t.prio <= prio {
			n++
		}
	}
	return n
}

// SetTask names the actual claimed work, after the queue has supplied a job.
// It does not log per item; the next pool adjustment includes these counts.
func (t *Ticket) SetTask(name string) {
	t.g.mu.Lock()
	t.task = name
	t.g.mu.Unlock()
}

func (g *Governor) workSummaryLocked() string {
	counts := map[string]int{}
	pending := 0
	for t := range g.tickets {
		if t.task == "" {
			pending++
			continue
		}
		name := t.task
		if t.stopped {
			name += " (paused)"
		}
		counts[name]++
	}
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names)+1)
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s=%d", name, counts[name]))
	}
	if pending > 0 {
		parts = append(parts, fmt.Sprintf("pending job claims=%d", pending))
	}
	if len(parts) == 0 {
		return "idle"
	}
	return strings.Join(parts, ", ")
}

const poolName = "media analysis pool (loudness detection, keyframe analysis)"

func reasonLabel(reason string) string {
	switch reason {
	case LimitCPU:
		return "CPU budget"
	case LimitDisk:
		return "disk utilization"
	case LimitRAM:
		return "available RAM"
	case LimitNetwork:
		return "network utilization"
	case LimitSwap:
		return "paging/swap activity"
	case LimitRecovery:
		return "waiting for quiet host"
	case LimitMetrics:
		return "waiting for host metrics"
	case LimitPlayback:
		return "foreground HDR playback"
	default:
		return "none"
	}
}

// Attach records the job's process group so it can be paused and resumed, and
// returns a function that forgets it.
func (t *Ticket) Attach(pgid int) func() {
	t.g.mu.Lock()
	t.pgid = pgid
	t.stopped = false
	// Charge from process birth, including CPU used before Attach runs.
	t.cpu = 0
	t.g.applyLocked()
	t.g.mu.Unlock()
	return func() {
		t.g.mu.Lock()
		t.g.chargeLocked(t)
		t.pgid = 0
		t.g.mu.Unlock()
	}
}

// Release ends the job's claim; a waiting job may start and a paused one may
// resume.
func (t *Ticket) Release() {
	t.g.mu.Lock()
	delete(t.g.tickets, t)
	t.g.applyLocked()
	t.g.signalLocked()
	t.g.mu.Unlock()
}

func (g *Governor) signalLocked() {
	close(g.changed)
	g.changed = make(chan struct{})
}

// applyLocked runs the best-ranked tickets (by priority, then start order, up
// to the target) and pauses the rest.
func (g *Governor) applyLocked() {
	ranked := make([]*Ticket, 0, len(g.tickets))
	for t := range g.tickets {
		ranked = append(ranked, t)
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].prio != ranked[j].prio {
			return ranked[i].prio < ranked[j].prio
		}
		return ranked[i].seq < ranked[j].seq
	})
	for i, t := range ranked {
		want := i >= g.target || g.throttled
		if t.pgid == 0 || t.stopped == want {
			continue
		}
		if want {
			pauseGroup(t.pgid)
		} else {
			resumeGroup(t.pgid)
		}
		t.stopped = want
	}
}

// Run starts CPU metering and checks host pressure until ctx ends. Unreadable
// CPU metrics hold analysis, with retries on subsequent sampling intervals.
func (g *Governor) Run(ctx context.Context, read Reader, every time.Duration) {
	if g.fixed {
		return
	}
	if g.budget {
		go g.runBudget(ctx)
	}
	prev, err := read()
	sampledAt := time.Now()
	var swapPressure swapPressureState
	if err != nil {
		g.mu.Lock()
		g.target, g.limit = 0, LimitMetrics
		g.applyLocked()
		g.signalLocked()
		g.mu.Unlock()
		logging.Warn("%s: host CPU is not readable (%v); analysis waits for metrics", poolName, err)
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		cur, err := read()
		now := time.Now()
		elapsed := now.Sub(sampledAt)
		sampledAt = now
		if err != nil {
			g.mu.Lock()
			g.target, g.quiet, g.limit = 0, 0, LimitMetrics
			g.applyLocked()
			g.mu.Unlock()
			continue
		}
		util, ok := Percent(prev, cur)
		prevSample := prev
		prev = cur
		if !ok {
			g.mu.Lock()
			g.target, g.quiet, g.limit = 0, 0, LimitMetrics
			g.applyLocked()
			g.mu.Unlock()
			continue
		}
		// CPU execution excludes I/O wait; device busy time has its own limit.
		if cur.IOWait >= prevSample.IOWait {
			util = max(0, util-100*float64(cur.IOWait-prevSample.IOWait)/float64(cur.Total-prevSample.Total))
		}
		g.mu.Lock()
		g.cpu = util
		previousLimit := g.limit
		// Subtract our measured subprocess CPU from total load before finding
		// remaining headroom. Go/SQLite work is conservatively counted as host load.
		own := g.charged.Seconds() / max(elapsed.Seconds(), .001)
		g.charged = 0
		g.allowance = min(cpuCapacity(g.cores), max(0, .8*float64(g.cores)-(util*float64(g.cores)/100-own)))
		swapLimited := swapPressure.update(prevSample, cur, elapsed)
		g.limit = pressureReason(prevSample, cur, util, swapLimited)
		next := g.target
		if g.foreground > 0 {
			next, g.quiet, g.limit = 0, 0, LimitPlayback
		} else if g.limit != "" {
			next, g.quiet = 0, 0
		} else {
			g.quiet++
			if g.quiet >= 2 {
				// Metering bounds multithreaded children even when a single job
				// uses several CPUs. Parallel jobs can fill all reserved capacity.
				next = min(g.max, max(1, g.target*2))
				g.quiet = 0
			}
			if next == 0 {
				g.limit = LimitRecovery
			}
		}
		changed := next != g.target
		g.target = next
		g.applyLocked()
		if changed {
			g.signalLocked()
		}
		reason := g.limit
		report := changed || previousLimit != reason
		work := ""
		if report {
			work = g.workSummaryLocked()
		}
		g.mu.Unlock()
		if report {
			logging.Info("%s: allowed concurrency %d of %d; claimed work: %s; host CPU %.0f%%, disk %.0f%%, network %.1f Mbps; limit: %s", poolName, next, g.max, work, util, cur.DiskUtil, cur.NetworkMbps, reasonLabel(reason))
		}
	}
}

// pressureReason applies host-wide backoff. Existing swapped-out pages alone
// are not activity: only newly paged-in/out pages prevent background recovery.
func pressureReason(prev, cur Sample, cpu float64, swapLimited bool) string {
	switch {
	case cur.MemTotal > 0 && cur.MemAvailable*100 < cur.MemTotal*MinMemoryAvailablePercent:
		return LimitRAM
	case swapLimited:
		return LimitSwap
	case cur.DiskUtil >= 80:
		return LimitDisk
	case cur.NetworkMbps >= 750:
		return LimitNetwork
	case cpu >= HighPercent:
		return LimitCPU
	}
	return ""
}

func swapActivityBytesPerSecond(prev, cur Sample, interval time.Duration) float64 {
	if interval <= 0 {
		return 0
	}
	pages := float64(0)
	if cur.SwapIn > prev.SwapIn {
		pages += float64(cur.SwapIn - prev.SwapIn)
	}
	if cur.SwapOut > prev.SwapOut {
		pages += float64(cur.SwapOut - prev.SwapOut)
	}
	return pages * float64(os.Getpagesize()) / interval.Seconds()
}
