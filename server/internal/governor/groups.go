package governor

import (
	"context"
	"sync"
)

// Reservations span complete jobs, including Go/SQLite work between subprocesses.
// Lower-priority process groups are stopped, and cooperative loops yield at their
// next safe checkpoint. The existing pressure/CPU budget remains in force.
const (
	PriorityBenchmark = -2
	PriorityLibrary   = -1
)

type priorityKey struct{}

func WithPriority(ctx context.Context, priority int) context.Context {
	return context.WithValue(ctx, priorityKey{}, priority)
}
func Priority(ctx context.Context) int {
	if p, ok := ctx.Value(priorityKey{}).(int); ok {
		return p
	}
	return PriorityLow
}
func Library(ctx context.Context) (context.Context, func()) {
	if Priority(ctx) == PriorityLibrary {
		return ctx, func() {}
	}
	if g := defaultPool.Load(); g != nil {
		return WithPriority(ctx, PriorityLibrary), g.Reserve(PriorityLibrary)
	}
	return WithPriority(ctx, PriorityLibrary), func() {}
}
func (g *Governor) Reserve(priority int) func() {
	g.mu.Lock()
	if g.groups == nil {
		g.groups = make(map[int]int)
	}
	g.groups[priority]++
	g.applyLocked()
	g.signalLocked()
	g.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			g.groups[priority]--
			g.applyLocked()
			g.signalLocked()
			g.mu.Unlock()
		})
	}
}
func (g *Governor) eligibleLocked(priority int) bool {
	for p, n := range g.groups {
		if n > 0 && p < priority {
			return false
		}
	}
	return g.foreground == 0 || priority < PriorityHigh
}
