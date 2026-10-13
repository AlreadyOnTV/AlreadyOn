package playback

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
)

// PresentationDelay is how far behind the server clock every client presents
// a channel, giving converted streams time to produce segments. Entry lookup
// and stream positions follow the presented moment rather than the server
// clock, so an entry shorter than the delay is neither skipped nor started
// late, and a program resumed after commercials does not replay or skip.
const PresentationDelay = 4 * time.Second

const PrepareAhead = 8 * time.Second

// TuneEntry allows only the presented item or its immediate successor.
// Preparation never shifts the channel clock: future workers start at the
// entry's source offset, while ordinary/late tunes seek to the presented
// moment. Workers still cannot start more than PrepareAhead before their
// entry airs on the server clock.
func TuneEntry(ctx context.Context, db *sql.DB, clock schedule.Clock, channelID int64, requested time.Time) (schedule.Entry, schedule.Clock, error) {
	now := clock.Now()
	presented := schedule.FixedClock{Time: now.Add(-PresentationDelay)}
	if requested.IsZero() {
		entry, _, err := schedule.Current(ctx, db, presented, channelID)
		return entry, presented, err
	}
	if requested.After(now.Add(PrepareAhead)) {
		return schedule.Entry{}, nil, errors.New("playback: preparation is too early")
	}
	entries, err := schedule.Upcoming(ctx, db, presented, channelID, 2)
	if err != nil {
		return schedule.Entry{}, nil, err
	}
	for _, entry := range entries {
		if entry.StartsAt.Equal(requested) {
			return entry, schedule.FixedClock{Time: later(presented.Time, entry.StartsAt)}, nil
		}
	}
	return schedule.Entry{}, nil, errors.New("playback: scheduled boundary changed")
}

// ReadBurst is how much of a job's content FFmpeg reads without real-time
// pacing: whatever lies between the job's position and the server clock. A
// stream started at the presented moment then keeps the same headroom as one
// prepared ahead of its boundary, instead of trailing the delay by a segment.
func ReadBurst(now, position time.Time) time.Duration {
	return min(max(now.Sub(position), 0), PresentationDelay)
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
