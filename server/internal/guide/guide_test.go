package guide

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/commercial"
	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
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

// insertMediaFile inserts a media_files row and returns its id. path is
// also used as a unique key, so tests can pass distinct fake paths freely.
func insertMediaFile(t *testing.T, db *sql.DB, rootKind, path string, durationSeconds float64) int64 {
	t.Helper()
	now := time.Now().Unix()
	var id int64
	err := db.QueryRowContext(context.Background(), `
		INSERT INTO media_files (root_kind, path, size_bytes, modified_unix, first_seen_unix, last_seen_unix, duration_seconds, status)
		VALUES (?, ?, 1024, ?, ?, ?, ?, 'ok') RETURNING id`,
		rootKind, path, now, now, now, durationSeconds).Scan(&id)
	if err != nil {
		t.Fatalf("insert media_files: %v", err)
	}
	return id
}

func insertIdentifiedEpisode(t *testing.T, db *sql.DB, mediaFileID int64, series string, season, episode int, title string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO identified_items (media_file_id, media_type, title, series_title, season_number, episode_number)
		VALUES (?, 'episode', ?, ?, ?, ?)`, mediaFileID, title, series, season, episode)
	if err != nil {
		t.Fatalf("insert identified_items: %v", err)
	}
}

func insertChannel(t *testing.T, db *sql.DB, number int, name string, mediaFileIDs ...int64) int64 {
	t.Helper()
	ctx := context.Background()
	var channelID int64
	if err := db.QueryRowContext(ctx, `INSERT INTO channels (number, name) VALUES (?, ?) RETURNING id`, number, name).
		Scan(&channelID); err != nil {
		t.Fatalf("insert channels: %v", err)
	}
	for i, id := range mediaFileIDs {
		if _, err := db.ExecContext(ctx, `INSERT INTO channel_items (channel_id, media_file_id, position) VALUES (?, ?, ?)`,
			channelID, id, i); err != nil {
			t.Fatalf("insert channel_items: %v", err)
		}
	}
	return channelID
}

// regenerate calls schedule.Regenerate so that its generated sequence
// starts exactly at time.Unix(0, 0).UTC() (schedule.TrailingWindow
// ahead of that is used as the "now" passed in) — every test in this
// file already writes its clocks relative to time.Unix(0, 0), from
// back when that was schedule's own internal `epoch`; this keeps all of
// that math valid now that Preview/Window read a persisted, generated
// schedule instead of computing one on the fly.
func regenerate(t *testing.T, db *sql.DB, channelID int64) {
	t.Helper()
	anchor := time.Unix(0, 0).UTC().Add(schedule.TrailingWindow)
	if err := schedule.Regenerate(context.Background(), db, channelID, anchor); err != nil {
		t.Fatalf("schedule.Regenerate: %v", err)
	}
}

func TestPreview_ResolvesIdentifiedProgram(t *testing.T) {
	db := openTestDB(t)
	mediaFileID := insertMediaFile(t, db, "media", "/tv/Show/S01E02.mkv", 600)
	insertIdentifiedEpisode(t, db, mediaFileID, "Some Show", 1, 2, "The Episode Title")
	channelID := insertChannel(t, db, 1000, "Test Channel", mediaFileID)
	regenerate(t, db, channelID)

	clock := schedule.FixedClock{Time: time.Unix(0, 0).UTC().Add(100 * time.Second)}
	p, offset, err := Preview(context.Background(), db, clock, channelID)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}

	if p.ChannelID != channelID || p.MediaFileID != mediaFileID {
		t.Errorf("p = %+v, want channel %d media file %d", p, channelID, mediaFileID)
	}
	if p.Kind != schedule.KindProgram {
		t.Errorf("p.Kind = %q, want %q", p.Kind, schedule.KindProgram)
	}
	if p.Title != "The Episode Title" || p.SeriesTitle != "Some Show" || p.SeasonNumber != 1 || p.EpisodeNumber != 2 || p.MediaType != "episode" {
		t.Errorf("p = %+v, want resolved episode metadata", p)
	}
	if offset != 100*time.Second {
		t.Errorf("offset = %v, want 100s", offset)
	}
}

func TestPreview_UnidentifiedFile_FallsBackToFilename(t *testing.T) {
	db := openTestDB(t)
	mediaFileID := insertMediaFile(t, db, "media", "/tv/Unsorted/some.weird.file (2026).mkv", 600)
	channelID := insertChannel(t, db, 1000, "Test Channel", mediaFileID)
	regenerate(t, db, channelID)

	clock := schedule.FixedClock{Time: time.Unix(0, 0).UTC()}
	p, _, err := Preview(context.Background(), db, clock, channelID)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if p.Title != "some.weird.file (2026)" {
		t.Errorf("p.Title = %q, want the filename with only its extension stripped", p.Title)
	}
}

func TestPreview_EmptyLineup_PropagatesErrEmptyLineup(t *testing.T) {
	db := openTestDB(t)
	channelID := insertChannel(t, db, 1000, "Empty Channel")
	regenerate(t, db, channelID)

	_, _, err := Preview(context.Background(), db, schedule.FixedClock{Time: time.Unix(0, 0).UTC()}, channelID)
	if !errors.Is(err, schedule.ErrEmptyLineup) {
		t.Errorf("err = %v, want schedule.ErrEmptyLineup", err)
	}
}

func TestWindow_FirstEntryMatchesPreview(t *testing.T) {
	db := openTestDB(t)
	a := insertMediaFile(t, db, "media", "/tv/a.mkv", 600)
	b := insertMediaFile(t, db, "media", "/tv/b.mkv", 600)
	channelID := insertChannel(t, db, 1000, "Test Channel", a, b)
	regenerate(t, db, channelID)

	clock := schedule.FixedClock{Time: time.Unix(0, 0).UTC().Add(300 * time.Second)}
	preview, _, err := Preview(context.Background(), db, clock, channelID)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	window, err := Window(context.Background(), db, clock, channelID, time.Hour)
	if err != nil {
		t.Fatalf("Window: %v", err)
	}
	if len(window) == 0 || window[0] != preview {
		t.Errorf("window[0] = %+v, want it to match Preview() = %+v", window[0], preview)
	}
}

func TestWindow_ClipsToRequestedDuration(t *testing.T) {
	db := openTestDB(t)
	// Three 600s (10-min) programs looping forever.
	a := insertMediaFile(t, db, "media", "/tv/a.mkv", 600)
	b := insertMediaFile(t, db, "media", "/tv/b.mkv", 600)
	c := insertMediaFile(t, db, "media", "/tv/c.mkv", 600)
	channelID := insertChannel(t, db, 1000, "Test Channel", a, b, c)
	regenerate(t, db, channelID)

	clock := schedule.FixedClock{Time: time.Unix(0, 0).UTC()}
	// A 15-minute window should include the first program (0-10min) and
	// the second (10-20min, which STARTS within the window), but not the
	// third (starts at 20min, past the 15-min cutoff).
	window, err := Window(context.Background(), db, clock, channelID, 15*time.Minute)
	if err != nil {
		t.Fatalf("Window: %v", err)
	}
	if len(window) != 2 {
		t.Fatalf("len(window) = %d, want 2, got %+v", len(window), window)
	}
	if window[0].MediaFileID != a || window[1].MediaFileID != b {
		t.Errorf("window = %+v, want [a, b]", window)
	}
}

func TestWindow_LabelsCommercialBreaks(t *testing.T) {
	db := openTestDB(t)
	program := insertMediaFile(t, db, "media", "/tv/show.mkv", 1440) // 24 min
	insertMediaFile(t, db, "commercial", "/ads/ad1.mkv", 1000)       // ~16.7 min
	channelID := insertChannel(t, db, 1000, "Test Channel", program)

	if err := commercial.SaveSettings(context.Background(), db, commercial.Settings{
		Enabled: true, Mode: commercial.ModeBetweenOnly, IntervalMinutes: 30, TVCommercialsPerBreak: 1, MovieCommercialsPerBreak: 1,
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	regenerate(t, db, channelID)

	// The 24-min program plus its one-commercial between-episodes break
	// form a ~40.7-min loop; a 25-min window captures exactly one of
	// each without wrapping into the next loop iteration.
	clock := schedule.FixedClock{Time: time.Unix(0, 0).UTC()}
	window, err := Window(context.Background(), db, clock, channelID, 25*time.Minute)
	if err != nil {
		t.Fatalf("Window: %v", err)
	}
	if len(window) != 2 {
		t.Fatalf("len(window) = %d, want 2 (program + break), got %+v", len(window), window)
	}
	if window[1].Kind != schedule.KindCommercial || window[1].Title != commercialBreakTitle {
		t.Errorf("window[1] = %+v, want a labeled commercial break", window[1])
	}
	if window[1].MediaType != "" {
		t.Errorf("window[1].MediaType = %q, want empty for a commercial break", window[1].MediaType)
	}
}

// The old ">=2 distinct shows" auto-visibility rule (and its tests)
// moved to internal/channel (SeriesCounts/HideEmpty) — see #90's
// follow-on, where it became a persisted, admin-overridable
// channels.hidden column instead of something recomputed live on every
// guide request.

// A prefetched entry must resolve its own title even when its airtime is in the
// future and another file is currently scheduled on the same channel.
func TestForEntryUsesSelectedMedia(t *testing.T) {
	db := openTestDB(t)
	first := insertMediaFile(t, db, "media", "/fixture/first.mkv", 120)
	next := insertMediaFile(t, db, "media", "/fixture/next.mkv", 120)
	insertIdentifiedEpisode(t, db, first, "First Show", 1, 1, "First Episode")
	insertIdentifiedEpisode(t, db, next, "Next Show", 2, 12, "Next Episode [remux]")
	start := time.Now().UTC().Add(time.Hour)
	entry := schedule.Entry{MediaFileID: next, Kind: schedule.KindProgram, StartsAt: start, EndsAt: start.Add(2 * time.Minute)}
	got, err := ForEntry(context.Background(), db, entry)
	if err != nil {
		t.Fatal(err)
	}
	if got.SeriesTitle != "Next Show" || got.Title != "Next Episode [remux]" || got.SeasonNumber != 2 || got.EpisodeNumber != 12 || !got.StartsAt.Equal(start) {
		t.Fatalf("selected entry metadata changed: %+v", got)
	}
	entry.Kind = schedule.KindCommercial
	got, err = ForEntry(context.Background(), db, entry)
	if err != nil || got.Title != "Commercial Break" || got.SeriesTitle != "" {
		t.Fatalf("commercial metadata: %+v, %v", got, err)
	}
}
