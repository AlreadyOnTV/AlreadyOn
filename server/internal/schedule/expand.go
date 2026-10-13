package schedule

import (
	"github.com/alreadyonapp/alreadyon/server/internal/commercial"
)

// Kind distinguishes a program from an inserted commercial break within
// an Entry.
type Kind string

const (
	KindProgram    Kind = "program"
	KindCommercial Kind = "commercial"
)

// TVBufferSeconds/MovieBufferSeconds: in commercial.ModeInterval, a
// break is never placed within this many seconds of a program's own
// start or end — no cold-open or final-scene interruptions. A movie
// gets a longer buffer than a TV episode; real intermissions land well
// clear of the opening/closing minutes either way.
const (
	TVBufferSeconds    = 5 * 60
	MovieBufferSeconds = 15 * 60
)

// expand interleaves commercial breaks into programs according to
// settings.Mode, producing the sequence Current/Upcoming actually loop
// over. With commercials disabled, no pool, or no programs, it returns
// programs unchanged (tagged KindProgram) — the pre-#24 behavior every
// existing test depends on.
//
// Pure function of its inputs: no I/O, no clock, no hidden state. The
// commercial pool is walked round-robin from index 0 every call, so
// re-running expand with the same inputs always inserts the same
// breaks in the same places — required for the whole schedule package
// to stay a pure function of (lineup content, time), restart-safe by
// construction (see epoch's doc comment).
func expand(programs []lineupItem, pool []lineupItem, settings commercial.Settings) []lineupItem {
	if !settings.Enabled || len(pool) == 0 || len(programs) == 0 {
		out := make([]lineupItem, len(programs))
		for i, p := range programs {
			out[i] = p
			out[i].Kind = KindProgram
		}
		return out
	}

	e := &expander{pool: pool}
	for _, p := range programs {
		if settings.Mode == commercial.ModeInterval {
			e.emitWithIntervalBreaks(p, settings)
		} else {
			e.emitWhole(p)
			e.insertBreak(settings.CommercialsPerBreak(p.MediaType))
		}
	}
	return e.items
}

// expander accumulates the expanded item sequence and the round-robin/
// timing state needed to build it, so expand's own loop body stays
// readable.
type expander struct {
	pool  []lineupItem
	items []lineupItem
	// sinceBreak is seconds of program content played since the last
	// break, carried across program boundaries (ModeInterval only) — an
	// interval timer, not a per-program counter, so a program too short
	// to ever reach a safe break point (see emitWithIntervalBreaks) just
	// gets played through whole, and the very next program picks up
	// wherever the timer left off rather than resetting.
	sinceBreak float64
	poolIdx    int
}

// next returns the pool's next commercial round-robin, advancing the
// pointer.
func (e *expander) next() lineupItem {
	c := e.pool[e.poolIdx%len(e.pool)]
	e.poolIdx++
	return c
}

// emitWhole appends p in full, as one uninterrupted segment.
func (e *expander) emitWhole(p lineupItem) {
	e.emitSegment(p, 0, p.DurationSeconds)
}

// emitSegment appends the portion of p's own file from fromSeconds to
// toSeconds as one KindProgram entry — a whole program when the two
// span its entire duration, or one piece of a program ModeInterval has
// split around a mid-program break otherwise. A zero-length span (two
// break points computed back to back) is silently dropped rather than
// emitting an empty entry.
func (e *expander) emitSegment(p lineupItem, fromSeconds, toSeconds float64) {
	dur := toSeconds - fromSeconds
	if dur <= 0 {
		return
	}
	e.items = append(e.items, lineupItem{
		Kind:            KindProgram,
		MediaFileID:     p.MediaFileID,
		MediaType:       p.MediaType,
		DurationSeconds: dur,
		SourceOffset:    fromSeconds,
	})
}

// insertBreak walks the pool round-robin, using each media file at most
// once per break. A small library produces a shorter break, never repeats.
func (e *expander) insertBreak(count int) {
	seen := make(map[int64]bool)
	for examined := 0; examined < len(e.pool) && len(seen) < count; examined++ {
		c := e.next()
		if seen[c.MediaFileID] {
			continue
		}
		seen[c.MediaFileID] = true
		e.items = append(e.items, lineupItem{Kind: KindCommercial, MediaFileID: c.MediaFileID, DurationSeconds: c.DurationSeconds})
	}
}

// emitWithIntervalBreaks plays p through, inserting a break roughly
// every settings.IntervalMinutes of its own content — never within the
// first/last buffer (TVBufferSeconds or MovieBufferSeconds, by media
// type) of p itself. The interval timer (e.sinceBreak) is continuous
// across programs: if p is too short to ever leave its own start
// buffer before hitting its end buffer, no break lands inside it at
// all, and the timer carries its accumulated value into whatever
// program comes next rather than resetting — so a run of short
// programs still eventually gets a break once one of them is long
// enough to offer a safe point, instead of the interval silently never
// firing.
func (e *expander) emitWithIntervalBreaks(p lineupItem, settings commercial.Settings) {
	bufferSeconds := float64(TVBufferSeconds)
	if p.MediaType == "movie" {
		bufferSeconds = MovieBufferSeconds
	}
	intervalSeconds := float64(settings.IntervalMinutes * 60)
	safeEnd := p.DurationSeconds - bufferSeconds

	if safeEnd <= bufferSeconds {
		// No safe point exists anywhere in this program (it's shorter
		// than twice its own buffer) — play it through untouched.
		e.emitWhole(p)
		e.sinceBreak += p.DurationSeconds
		return
	}

	segmentStart := 0.0
	for {
		candidate := segmentStart + (intervalSeconds - e.sinceBreak)
		if candidate >= p.DurationSeconds {
			break // the interval doesn't fire again before p ends
		}
		if candidate < bufferSeconds {
			candidate = bufferSeconds // too close to the start: hold for the first safe point
		}
		if candidate > safeEnd {
			break // no safe point remains between here and the end
		}

		e.emitSegment(p, segmentStart, candidate)
		e.insertBreak(settings.CommercialsPerBreak(p.MediaType))
		segmentStart = candidate
		e.sinceBreak = 0
	}
	e.emitSegment(p, segmentStart, p.DurationSeconds)
	e.sinceBreak += p.DurationSeconds - segmentStart
}
