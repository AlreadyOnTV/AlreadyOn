package schedule

import (
	"testing"

	"github.com/alreadyonapp/alreadyon/server/internal/commercial"
)

func program(id int64, seconds float64) lineupItem {
	return lineupItem{MediaFileID: id, DurationSeconds: seconds}
}

func typedProgram(id int64, seconds float64, mediaType string) lineupItem {
	return lineupItem{MediaFileID: id, DurationSeconds: seconds, MediaType: mediaType}
}

func sumDuration(items []lineupItem) float64 {
	var total float64
	for _, it := range items {
		total += it.DurationSeconds
	}
	return total
}

func kinds(items []lineupItem) []Kind {
	ks := make([]Kind, len(items))
	for i, it := range items {
		ks[i] = it.Kind
	}
	return ks
}

func TestExpand_Disabled_PassesProgramsThroughUnchanged(t *testing.T) {
	programs := []lineupItem{program(1, 100), program(2, 200)}
	pool := []lineupItem{program(9, 30)}

	got := expand(programs, pool, commercial.Settings{Enabled: false, Mode: commercial.ModeBetweenOnly})

	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2 (no breaks when disabled)", len(got))
	}
	for i, it := range got {
		if it.Kind != KindProgram {
			t.Errorf("got[%d].Kind = %q, want %q", i, it.Kind, KindProgram)
		}
		if it.MediaFileID != programs[i].MediaFileID || it.DurationSeconds != programs[i].DurationSeconds {
			t.Errorf("got[%d] = %+v, want %+v", i, it, programs[i])
		}
	}
}

func TestExpand_EnabledButEmptyPool_PassesThroughUnchanged(t *testing.T) {
	programs := []lineupItem{program(1, 100)}

	got := expand(programs, nil, commercial.Settings{Enabled: true, Mode: commercial.ModeBetweenOnly})

	if len(got) != 1 || got[0].Kind != KindProgram {
		t.Errorf("got = %+v, want programs unchanged (no pool to draw breaks from)", got)
	}
}

func TestExpand_BetweenOnly_InsertsConfiguredCountAfterEveryProgram(t *testing.T) {
	programs := []lineupItem{typedProgram(1, 1000, "episode"), typedProgram(2, 500, "movie")}
	pool := []lineupItem{program(100, 30), program(101, 45), program(102, 60), program(103, 15), program(104, 20), program(105, 25), program(106, 10)}

	got := expand(programs, pool, commercial.Settings{
		Enabled: true, Mode: commercial.ModeBetweenOnly, TVCommercialsPerBreak: 2, MovieCommercialsPerBreak: 5,
	})

	want := []Kind{KindProgram, KindCommercial, KindCommercial, KindProgram, KindCommercial, KindCommercial, KindCommercial, KindCommercial, KindCommercial}
	if gotKinds := kinds(got); len(gotKinds) != len(want) {
		t.Fatalf("kinds = %v, want %v (2 commercials after the episode, 5 after the movie)", gotKinds, want)
	} else {
		for i := range want {
			if gotKinds[i] != want[i] {
				t.Errorf("kinds[%d] = %q, want %q (full: %v)", i, gotKinds[i], want[i], gotKinds)
			}
		}
	}
	// Round-robin: the movie's 5 commercials continue the pool index
	// where the episode's 2 left off (102..106), not restarting at 100.
	wantIDs := []int64{1, 100, 101, 2, 102, 103, 104, 105, 106}
	for i, it := range got {
		if it.MediaFileID != wantIDs[i] {
			t.Errorf("got[%d].MediaFileID = %d, want %d", i, it.MediaFileID, wantIDs[i])
		}
	}
}

func TestExpand_BetweenOnly_NeverSplitsAProgram(t *testing.T) {
	programs := []lineupItem{typedProgram(1, 7200, "movie")} // a 2-hour movie
	pool := []lineupItem{program(100, 30)}

	got := expand(programs, pool, commercial.Settings{Enabled: true, Mode: commercial.ModeBetweenOnly, TVCommercialsPerBreak: 1, MovieCommercialsPerBreak: 1})

	if len(got) != 2 || got[0].DurationSeconds != 7200 || got[0].SourceOffset != 0 {
		t.Fatalf("got = %+v, want the movie as one untouched 7200s segment starting at offset 0", got)
	}
}

func TestExpand_Interval_SingleBreakAtIntervalMark(t *testing.T) {
	// 1200s (20-min) episode, 900s (15-min) interval, TVBufferSeconds
	// (300s/5min): the interval mark (900) sits exactly at the safe-end
	// boundary (1200-300), so it's allowed — one break, splitting the
	// episode into a 900s segment and a 300s segment.
	programs := []lineupItem{typedProgram(1, 1200, "episode")}
	pool := []lineupItem{program(100, 30)}

	got := expand(programs, pool, commercial.Settings{
		Enabled: true, Mode: commercial.ModeInterval, IntervalMinutes: 15, TVCommercialsPerBreak: 1, MovieCommercialsPerBreak: 1,
	})

	want := []Kind{KindProgram, KindCommercial, KindProgram}
	if gotKinds := kinds(got); len(gotKinds) != len(want) {
		t.Fatalf("kinds = %v, want %v", gotKinds, want)
	}
	if got[0].DurationSeconds != 900 || got[0].SourceOffset != 0 {
		t.Errorf("got[0] = %+v, want the first 900s of the episode (offset 0)", got[0])
	}
	if got[1].MediaFileID != 100 {
		t.Errorf("got[1] = %+v, want the pool's commercial", got[1])
	}
	if got[2].DurationSeconds != 300 || got[2].SourceOffset != 900 {
		t.Errorf("got[2] = %+v, want the remaining 300s starting at offset 900", got[2])
	}
	if got[2].MediaFileID != 1 {
		t.Errorf("got[2].MediaFileID = %d, want 1 (still the same episode, split)", got[2].MediaFileID)
	}
}

func TestExpand_Interval_MultipleBreaksInOneLongProgram(t *testing.T) {
	// A 30-min episode with a 10-min interval fits two breaks (at 10 and
	// 20 min) well clear of both the 5-min start and end buffers.
	programs := []lineupItem{typedProgram(1, 1800, "episode")}
	pool := []lineupItem{program(100, 30), program(101, 30)}

	got := expand(programs, pool, commercial.Settings{
		Enabled: true, Mode: commercial.ModeInterval, IntervalMinutes: 10, TVCommercialsPerBreak: 1, MovieCommercialsPerBreak: 1,
	})

	want := []Kind{KindProgram, KindCommercial, KindProgram, KindCommercial, KindProgram}
	if gotKinds := kinds(got); len(gotKinds) != len(want) {
		t.Fatalf("kinds = %v, want %v", gotKinds, want)
	}
	wantDurations := []float64{600, 30, 600, 30, 600}
	for i, d := range wantDurations {
		if got[i].DurationSeconds != d {
			t.Errorf("got[%d].DurationSeconds = %v, want %v (full: %+v)", i, got[i].DurationSeconds, d, got)
		}
	}
	if total := sumDuration(got); total != 1800+60 {
		t.Errorf("total = %v, want %v (episode duration + 2 commercials)", total, 1800+60.0)
	}
}

func TestExpand_Interval_TooShortForAnySafeBreak_PlaysWhole(t *testing.T) {
	// 500s episode: even a break exactly at the 5-min (300s) buffer would
	// leave only 200s before the end, inside the trailing 300s buffer —
	// no safe point exists anywhere in it, no matter how small the
	// interval is or how much time has accumulated toward it (a
	// structural fact about this one program, not a timing question) —
	// both programs here play whole, untouched.
	programs := []lineupItem{typedProgram(1, 500, "episode"), typedProgram(2, 500, "episode")}
	pool := []lineupItem{program(100, 30)}

	got := expand(programs, pool, commercial.Settings{
		Enabled: true, Mode: commercial.ModeInterval, IntervalMinutes: 1, TVCommercialsPerBreak: 1, MovieCommercialsPerBreak: 1,
	})

	if len(got) != 2 || kinds(got)[0] != KindProgram || kinds(got)[1] != KindProgram {
		t.Fatalf("got = %+v, want both 500s programs untouched (neither has any safe point)", got)
	}
}

func TestExpand_Interval_MovieUsesFifteenMinuteBuffer(t *testing.T) {
	// 1200s (20-min): safe for a TV episode (5-min buffer leaves 10 safe
	// minutes) but too short for a movie (15-min buffer leaves none).
	tvGot := expand(
		[]lineupItem{typedProgram(1, 1200, "episode")},
		[]lineupItem{program(100, 30)},
		commercial.Settings{Enabled: true, Mode: commercial.ModeInterval, IntervalMinutes: 5, TVCommercialsPerBreak: 1, MovieCommercialsPerBreak: 1},
	)
	if len(tvGot) == 1 {
		t.Errorf("tvGot = %+v, want at least one break (10 safe minutes at a 5-min buffer)", tvGot)
	}

	movieGot := expand(
		[]lineupItem{typedProgram(1, 1200, "movie")},
		[]lineupItem{program(100, 30)},
		commercial.Settings{Enabled: true, Mode: commercial.ModeInterval, IntervalMinutes: 5, TVCommercialsPerBreak: 1, MovieCommercialsPerBreak: 1},
	)
	if len(movieGot) != 1 || movieGot[0].DurationSeconds != 1200 {
		t.Errorf("movieGot = %+v, want the movie played whole (no safe point at a 15-min buffer)", movieGot)
	}
}

func TestExpand_Interval_EarlyCandidateClampsToBufferStart(t *testing.T) {
	// A 1-minute (60s) interval fires well before the 300s (5-min) start
	// buffer ends — the break is held to land exactly at the buffer,
	// not before it.
	programs := []lineupItem{typedProgram(1, 1200, "episode")}
	pool := []lineupItem{program(100, 30)}

	got := expand(programs, pool, commercial.Settings{
		Enabled: true, Mode: commercial.ModeInterval, IntervalMinutes: 1, TVCommercialsPerBreak: 1, MovieCommercialsPerBreak: 1,
	})

	if got[0].DurationSeconds != TVBufferSeconds {
		t.Errorf("got[0].DurationSeconds = %v, want %v (clamped to the 5-min buffer, not the raw 60s interval)", got[0].DurationSeconds, float64(TVBufferSeconds))
	}
}

func TestExpand_Interval_SinceBreakCarriesAcrossPrograms(t *testing.T) {
	// program1 (200s) is far too short for any break on its own (well
	// under the 300s buffer) and simply plays whole, but its 200s still
	// counts toward the 900s interval. program2 then only needs another
	// 700s (not the full 900s) before its first break fires — proving
	// the timer carried over rather than resetting at the program
	// boundary.
	programs := []lineupItem{typedProgram(1, 200, "episode"), typedProgram(2, 1200, "episode")}
	pool := []lineupItem{program(100, 30)}

	got := expand(programs, pool, commercial.Settings{
		Enabled: true, Mode: commercial.ModeInterval, IntervalMinutes: 15, TVCommercialsPerBreak: 1, MovieCommercialsPerBreak: 1,
	})

	want := []Kind{KindProgram, KindProgram, KindCommercial, KindProgram}
	if gotKinds := kinds(got); len(gotKinds) != len(want) {
		t.Fatalf("kinds = %v, want %v (full: %+v)", gotKinds, want, got)
	}
	if got[0].DurationSeconds != 200 {
		t.Errorf("got[0] = %+v, want program1 played whole (200s)", got[0])
	}
	if got[1].MediaFileID != 2 || got[1].DurationSeconds != 700 || got[1].SourceOffset != 0 {
		t.Errorf("got[1] = %+v, want program2's first 700s (900s interval minus the 200s already carried over)", got[1])
	}
	if got[3].DurationSeconds != 500 || got[3].SourceOffset != 700 {
		t.Errorf("got[3] = %+v, want the remaining 500s of program2 starting at offset 700", got[3])
	}
}

func TestBreakNeverRepeatsMediaAndContinuesRoundRobin(t *testing.T) {
	for _, pool := range [][]lineupItem{nil, {program(100, 30)}, {program(100, 30), program(101, 45)}, {program(100, 30), program(100, 30), program(101, 45)}} {
		e := expander{pool: pool}
		e.insertBreak(5)
		seen := map[int64]bool{}
		for _, item := range e.items {
			if seen[item.MediaFileID] {
				t.Fatalf("repeated commercial: %+v", e.items)
			}
			seen[item.MediaFileID] = true
		}
		want := 0
		if len(pool) > 0 {
			want = 1
		}
		if len(pool) > 1 {
			want = 2
		}
		if len(e.items) != want {
			t.Fatalf("break size %d, want %d", len(e.items), want)
		}
	}
	e := expander{pool: []lineupItem{program(100, 30), program(101, 45), program(102, 60)}, poolIdx: 2}
	for range 3 {
		e.insertBreak(2)
	}
	for i, want := range []int64{102, 100, 101, 102, 100, 101} {
		if e.items[i].MediaFileID != want {
			t.Fatalf("rotation restarted: %+v", e.items)
		}
	}
}
