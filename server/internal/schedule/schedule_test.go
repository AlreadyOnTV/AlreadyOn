package schedule

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db.DB
}

// testAnchor is an arbitrary, fixed "now" used to regenerate a test
// channel's schedule — the value itself doesn't matter to any test,
// only its relationship to the loopBase regenerate returns.
var testAnchor = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// regenerate calls Regenerate for channelID as of testAnchor and returns
// the point its generated sequence actually starts from
// (testAnchor-TrailingWindow, for a channel with no prior schedule_entries
// — true for every test here, each using its own fresh DB) — the same
// role `epoch` played before schedule became a persisted, query-based
// package: every test computes its expected offsets relative to this.
func regenerate(t *testing.T, db *sql.DB, channelID int64) time.Time {
	t.Helper()
	if err := Regenerate(context.Background(), db, channelID, testAnchor); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	return testAnchor.Add(-TrailingWindow)
}

// setupChannel creates a channel (at the given channel number — tests
// that only ever need one channel all use 1000) and attaches media
// files with the given durations (in seconds), in lineup order. A
// duration of 0 means "not yet probed" (NULL duration_seconds) rather
// than a zero-length file.
func setupChannel(t *testing.T, db *sql.DB, number int, durations ...float64) int64 {
	t.Helper()
	ctx := context.Background()

	var channelID int64
	if err := db.QueryRowContext(ctx, `INSERT INTO channels (number, name) VALUES (?, 'Test Channel') RETURNING id`, number).
		Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}

	now := time.Now().Unix()
	for i, d := range durations {
		var mediaFileID int64
		err := db.QueryRowContext(ctx, `
			INSERT INTO media_files (root_kind, path, size_bytes, modified_unix, first_seen_unix, last_seen_unix)
			VALUES ('media', ?, 1024, ?, ?, ?) RETURNING id`,
			fmt.Sprintf("/media/channel-%d-file-%d.mkv", number, i), now, now, now).
			Scan(&mediaFileID)
		if err != nil {
			t.Fatalf("insert media_files: %v", err)
		}

		if d > 0 {
			if _, err := db.ExecContext(ctx, `UPDATE media_files SET duration_seconds = ? WHERE id = ?`, d, mediaFileID); err != nil {
				t.Fatalf("set duration: %v", err)
			}
		}

		if _, err := db.ExecContext(ctx, `INSERT INTO channel_items (channel_id, media_file_id, position) VALUES (?, ?, ?)`,
			channelID, mediaFileID, i); err != nil {
			t.Fatalf("insert channel_items: %v", err)
		}
	}
	return channelID
}

func TestCurrent_PicksCorrectItemAtOffset(t *testing.T) {
	db := openTestDB(t)
	channelID := setupChannel(t, db, 1000, 100, 200, 300) // loop = 600s: [0,100) [100,300) [300,600)
	loopBase := regenerate(t, db, channelID)

	cases := []struct {
		name            string
		secondsIntoLoop int64
		wantItemIndex   int
		wantOffset      time.Duration
	}{
		{"start of first item", 0, 0, 0},
		{"middle of first item", 50, 0, 50 * time.Second},
		{"exact start of second item", 100, 1, 0},
		{"middle of second item", 200, 1, 100 * time.Second},
		{"middle of third item", 450, 2, 150 * time.Second},
		{"last second of loop", 599, 2, 299 * time.Second},
	}

	// Fetch each item's media_file_id in lineup order, to check against.
	rows, err := db.Query(`SELECT media_file_id FROM channel_items WHERE channel_id = ? ORDER BY position`, channelID)
	if err != nil {
		t.Fatalf("query channel_items: %v", err)
	}
	var mediaFileIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		mediaFileIDs = append(mediaFileIDs, id)
	}
	rows.Close()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := FixedClock{Time: loopBase.Add(time.Duration(tc.secondsIntoLoop) * time.Second)}
			entry, offset, err := Current(context.Background(), db, clock, channelID)
			if err != nil {
				t.Fatalf("Current: %v", err)
			}
			if entry.MediaFileID != mediaFileIDs[tc.wantItemIndex] {
				t.Errorf("MediaFileID = %d, want item index %d (id %d)", entry.MediaFileID, tc.wantItemIndex, mediaFileIDs[tc.wantItemIndex])
			}
			if offset != tc.wantOffset {
				t.Errorf("offset = %v, want %v", offset, tc.wantOffset)
			}
		})
	}
}

func TestCurrent_LoopsAround(t *testing.T) {
	db := openTestDB(t)
	channelID := setupChannel(t, db, 1000, 100, 200, 300) // loop = 600s
	loopBase := regenerate(t, db, channelID)

	// Three full loops plus 50 seconds should land in the same place as
	// just 50 seconds — the same item, same offset.
	clockA := FixedClock{Time: loopBase.Add(50 * time.Second)}
	clockB := FixedClock{Time: loopBase.Add((600*3 + 50) * time.Second)}

	entryA, offsetA, err := Current(context.Background(), db, clockA, channelID)
	if err != nil {
		t.Fatalf("Current (A): %v", err)
	}
	entryB, offsetB, err := Current(context.Background(), db, clockB, channelID)
	if err != nil {
		t.Fatalf("Current (B): %v", err)
	}

	if entryA.MediaFileID != entryB.MediaFileID {
		t.Errorf("MediaFileID differs across loop iterations: %d vs %d", entryA.MediaFileID, entryB.MediaFileID)
	}
	if offsetA != offsetB {
		t.Errorf("offset differs across loop iterations: %v vs %v", offsetA, offsetB)
	}
}

func TestCurrent_DeterministicForSameClock(t *testing.T) {
	db := openTestDB(t)
	channelID := setupChannel(t, db, 1000, 100, 200, 300)
	loopBase := regenerate(t, db, channelID)
	clock := FixedClock{Time: loopBase.Add(250 * time.Second)}

	first, firstOffset, err := Current(context.Background(), db, clock, channelID)
	if err != nil {
		t.Fatalf("first Current: %v", err)
	}
	second, secondOffset, err := Current(context.Background(), db, clock, channelID)
	if err != nil {
		t.Fatalf("second Current: %v", err)
	}

	if first != second || firstOffset != secondOffset {
		t.Errorf("Current was not deterministic for the same clock: (%+v, %v) vs (%+v, %v)",
			first, firstOffset, second, secondOffset)
	}
}

func TestCurrent_TimestampsAreUTC(t *testing.T) {
	db := openTestDB(t)
	channelID := setupChannel(t, db, 1000, 100, 200)
	loopBase := regenerate(t, db, channelID)
	clock := FixedClock{Time: loopBase.Add(50 * time.Second)}

	entry, _, err := Current(context.Background(), db, clock, channelID)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if entry.StartsAt.Location() != time.UTC {
		t.Errorf("StartsAt location = %v, want UTC", entry.StartsAt.Location())
	}
	if entry.EndsAt.Location() != time.UTC {
		t.Errorf("EndsAt location = %v, want UTC", entry.EndsAt.Location())
	}
}

func TestCurrent_EmptyLineup_ReturnsErrEmptyLineup(t *testing.T) {
	db := openTestDB(t)
	channelID := setupChannel(t, db, 1000) // no items at all
	regenerate(t, db, channelID)

	_, _, err := Current(context.Background(), db, FixedClock{Time: testAnchor}, channelID)
	if !errors.Is(err, ErrEmptyLineup) {
		t.Errorf("err = %v, want ErrEmptyLineup", err)
	}
}

func TestCurrent_NoItemsHaveDuration_ReturnsErrEmptyLineup(t *testing.T) {
	db := openTestDB(t)
	channelID := setupChannel(t, db, 1000, 0, 0) // "0" means not-yet-probed (NULL duration)
	regenerate(t, db, channelID)

	_, _, err := Current(context.Background(), db, FixedClock{Time: testAnchor}, channelID)
	if !errors.Is(err, ErrEmptyLineup) {
		t.Errorf("err = %v, want ErrEmptyLineup", err)
	}
}

func TestCurrent_SkipsItemsWithoutDuration(t *testing.T) {
	db := openTestDB(t)
	// Middle item (index 1) has no duration yet and must be skipped —
	// the effective loop is just the two 100s items, totaling 200s.
	channelID := setupChannel(t, db, 1000, 100, 0, 100)
	loopBase := regenerate(t, db, channelID)

	clock := FixedClock{Time: loopBase.Add(150 * time.Second)}
	entry, offset, err := Current(context.Background(), db, clock, channelID)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}

	var thirdMediaFileID int64
	if err := db.QueryRow(`SELECT media_file_id FROM channel_items WHERE channel_id = ? AND position = 2`, channelID).
		Scan(&thirdMediaFileID); err != nil {
		t.Fatalf("query: %v", err)
	}
	if entry.MediaFileID != thirdMediaFileID {
		t.Errorf("expected the durationless middle item to be skipped, got MediaFileID=%d, want %d", entry.MediaFileID, thirdMediaFileID)
	}
	if offset != 50*time.Second {
		t.Errorf("offset = %v, want 50s", offset)
	}
}

func TestUpcoming_LoopsLineupToFillLimit(t *testing.T) {
	db := openTestDB(t)
	channelID := setupChannel(t, db, 1000, 100, 200) // loop = 300s
	loopBase := regenerate(t, db, channelID)

	entries, err := Upcoming(context.Background(), db, FixedClock{Time: loopBase}, channelID, 5)
	if err != nil {
		t.Fatalf("Upcoming: %v", err)
	}
	if len(entries) != 5 {
		t.Fatalf("len(entries) = %d, want 5", len(entries))
	}

	// Should cycle item0, item1, item0, item1, item0.
	if entries[0].MediaFileID != entries[2].MediaFileID || entries[2].MediaFileID != entries[4].MediaFileID {
		t.Errorf("expected entries 0, 2, 4 to be the same (looped) item, got %+v", entries)
	}
	if entries[1].MediaFileID != entries[3].MediaFileID {
		t.Errorf("expected entries 1, 3 to be the same (looped) item, got %+v", entries)
	}

	// Contiguous: each entry's start must equal the previous entry's end.
	for i := 1; i < len(entries); i++ {
		if !entries[i].StartsAt.Equal(entries[i-1].EndsAt) {
			t.Errorf("entries[%d].StartsAt = %v, want %v (previous entry's EndsAt)", i, entries[i].StartsAt, entries[i-1].EndsAt)
		}
	}
}

func TestUpcoming_FirstEntryMatchesCurrent(t *testing.T) {
	db := openTestDB(t)
	channelID := setupChannel(t, db, 1000, 100, 200, 300)
	loopBase := regenerate(t, db, channelID)
	clock := FixedClock{Time: loopBase.Add(250 * time.Second)}

	current, _, err := Current(context.Background(), db, clock, channelID)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	upcoming, err := Upcoming(context.Background(), db, clock, channelID, 3)
	if err != nil {
		t.Fatalf("Upcoming: %v", err)
	}

	if upcoming[0] != current {
		t.Errorf("Upcoming[0] = %+v, want it to match Current() = %+v", upcoming[0], current)
	}
}

// TestCurrent_SurvivesSimulatedRestart is #23's acceptance test: close
// the DB connection and open a brand new one against the same on-disk
// database — exactly what main.go does on every process start
// (storage.Open(cfg.DataDir)) — then confirm Current() still sees
// exactly what was scheduled before. Under the persisted-schedule
// design (see Regenerate's doc comment), this is close to automatic:
// schedule_entries is a durable SQLite table, not in-memory state, so
// there's nothing to lose across a restart in the first place — the
// point of this test is confirming that directly, not deriving it from
// a recomputation happening to agree with itself.
func TestCurrent_SurvivesSimulatedRestart(t *testing.T) {
	dir := t.TempDir()

	db1, err := storage.Open(dir)
	if err != nil {
		t.Fatalf("storage.Open (before restart): %v", err)
	}
	channelID := setupChannel(t, db1.DB, 1000, 100, 200, 300) // loop = 600s: [0,100) [100,300) [300,600)
	loopBase := regenerate(t, db1.DB, channelID)

	clockBefore := FixedClock{Time: loopBase.Add(200 * time.Second)} // 100s into item 1 [100,300)
	before, offsetBefore, err := Current(context.Background(), db1.DB, clockBefore, channelID)
	if err != nil {
		t.Fatalf("Current (before restart): %v", err)
	}

	if err := db1.Close(); err != nil {
		t.Fatalf("close db (simulated shutdown): %v", err)
	}
	db2, err := storage.Open(dir)
	if err != nil {
		t.Fatalf("storage.Open (simulated restart): %v", err)
	}
	t.Cleanup(func() { db2.Close() })

	// 30 real seconds pass while "the server was down", still inside the
	// same item (ends at 300s into the loop, so this stays well clear of
	// the boundary) — and, crucially, no Regenerate call on db2: this
	// queries only what db1 already committed, to prove it survived.
	clockAfter := FixedClock{Time: clockBefore.Time.Add(30 * time.Second)}
	after, offsetAfter, err := Current(context.Background(), db2.DB, clockAfter, channelID)
	if err != nil {
		t.Fatalf("Current (after restart): %v", err)
	}

	if after.MediaFileID != before.MediaFileID {
		t.Errorf("MediaFileID changed across restart: before=%d after=%d", before.MediaFileID, after.MediaFileID)
	}
	if !after.StartsAt.Equal(before.StartsAt) {
		t.Errorf("StartsAt changed across restart: before=%v after=%v (an item's start time must stay fixed regardless of restarts)", before.StartsAt, after.StartsAt)
	}
	if !after.EndsAt.Equal(before.EndsAt) {
		t.Errorf("EndsAt changed across restart: before=%v after=%v", before.EndsAt, after.EndsAt)
	}
	if offsetAfter-offsetBefore != 30*time.Second {
		t.Errorf("offset did not advance by the elapsed wall-clock time: before=%v after=%v (want exactly +30s)", offsetBefore, offsetAfter)
	}
}

func TestUpcoming_ZeroLimit_ReturnsEmpty(t *testing.T) {
	db := openTestDB(t)
	channelID := setupChannel(t, db, 1000, 100)
	regenerate(t, db, channelID)

	entries, err := Upcoming(context.Background(), db, FixedClock{Time: testAnchor}, channelID, 0)
	if err != nil {
		t.Fatalf("Upcoming: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("len(entries) = %d, want 0", len(entries))
	}
}
