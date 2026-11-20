package governor

import (
	"context"
	"time"
)

type activeTimeout struct{ context.Context }

func (c activeTimeout) Err() error {
	if c.Context.Err() != nil && context.Cause(c.Context) == context.DeadlineExceeded {
		return context.DeadlineExceeded
	}
	return c.Context.Err()
}

// WithTimeout counts eligible execution time, excluding scheduling/pressure pauses.
// Wall-clock subprocess deadlines would kill paused probes instead of resuming
// them after a long benchmark. Parent cancellation always remains immediate.
func WithTimeout(parent context.Context, budget time.Duration) (context.Context, context.CancelFunc) {
	g := defaultPool.Load()
	priority := Priority(parent)
	if ticket := TicketFrom(parent); ticket != nil {
		g, priority = ticket.g, ticket.prio
	}
	if g == nil {
		return context.WithTimeout(parent, budget)
	}
	ctx, cancel := context.WithCancelCause(parent)
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		previous := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				g.mu.Lock()
				eligible := g.target > 0 && !g.throttled && g.eligibleLocked(priority)
				g.mu.Unlock()
				if eligible {
					budget -= now.Sub(previous)
				}
				previous = now
				if budget <= 0 {
					cancel(context.DeadlineExceeded)
					return
				}
			}
		}
	}()
	return activeTimeout{ctx}, func() { cancel(context.Canceled) }
}
