package lineup

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

var testPathCounter int64

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	// Existing full-catalog fixtures model a configured provider without making
	// network calls. Provider-mode tests explicitly override these settings.
	if _, err := db.Exec(`UPDATE metadata_settings SET tmdb_api_key='fixture'`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db.DB
}

func insertChannel(t *testing.T, db *sql.DB, number int, name string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRowContext(context.Background(), `INSERT INTO channels (number, name) VALUES (?, ?) RETURNING id`,
		number, name).Scan(&id); err != nil {
		t.Fatalf("insert channels: %v", err)
	}
	// Population tests use explicit narrow filters rather than the evolving defaults.
	if name == "Sci-Fi Network" || name == "Fantasy Network" {
		genre := "scifi"
		if name == "Fantasy Network" {
			genre = "fantasy"
		}
		if _, err := channel.SetFilter(context.Background(), db, id, channel.Filter{AudienceTier: "adult", MediaType: "tv", Genres: []string{genre}}); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

// insertClassified inserts a media_files + identified_items row with
// genre_tags already populated, as if a prior ClassifyGenres run had
// processed it.
func insertClassified(t *testing.T, db *sql.DB, mediaType, seriesTitle string, season, episode int, releaseYear int, durationSeconds float64, genreTagsJSON string) int64 {
	t.Helper()
	ctx := context.Background()
	now := time.Now().Unix()
	var mediaFileID int64
	path := fmt.Sprintf("/media/%s-s%de%d-%d.mkv", seriesTitle, season, episode, atomic.AddInt64(&testPathCounter, 1))
	if err := db.QueryRowContext(ctx, `
		INSERT INTO media_files (root_kind, path, size_bytes, modified_unix, first_seen_unix, last_seen_unix, duration_seconds, status)
		VALUES ('media', ?, 1024, ?, ?, ?, ?, 'ok') RETURNING id`,
		path, now, now, now, durationSeconds).Scan(&mediaFileID); err != nil {
		t.Fatalf("insert media_files: %v", err)
	}

	var seriesTitleArg sql.NullString
	if seriesTitle != "" {
		seriesTitleArg = sql.NullString{String: seriesTitle, Valid: true}
	}
	title := seriesTitle
	if mediaType == "movie" {
		title = "Movie"
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO identified_items (media_file_id, media_type, title, series_title, season_number, episode_number, release_year, genre_tags)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		mediaFileID, mediaType, title, seriesTitleArg, season, episode, releaseYear, genreTagsJSON); err != nil {
		t.Fatalf("insert identified_items: %v", err)
	}
	return mediaFileID
}

func channelItemMediaFileIDs(t *testing.T, db *sql.DB, channelID int64) []int64 {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `SELECT media_file_id FROM channel_items WHERE channel_id = ? ORDER BY position`, channelID)
	if err != nil {
		t.Fatalf("query channel_items: %v", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		ids = append(ids, id)
	}
	return ids
}

func TestPopulate_FillsMatchingEmptyDefaultChannel(t *testing.T) {
	db := openTestDB(t)
	scifiChannel := insertChannel(t, db, 129, "Sci-Fi Network") // Tags: scifi
	e1 := insertClassified(t, db, "episode", "Some Show", 1, 1, 0, 600, `["scifi"]`)
	e2 := insertClassified(t, db, "episode", "Some Show", 1, 2, 0, 600, `["scifi"]`)

	populated, err := Populate(context.Background(), db)
	if err != nil {
		t.Fatalf("Populate: %v", err)
	}
	if populated != 1 {
		t.Errorf("populated = %d, want 1", populated)
	}
	got := channelItemMediaFileIDs(t, db, scifiChannel)
	want := []int64{e1, e2}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("channel_items = %v, want %v (natural order)", got, want)
	}
}

func TestPopulate_SkipsChannelThatAlreadyHasContent(t *testing.T) {
	db := openTestDB(t)
	scifiChannel := insertChannel(t, db, 129, "Sci-Fi Network")
	existing := insertClassified(t, db, "episode", "Existing Show", 1, 1, 0, 600, `["scifi"]`)
	if _, err := db.Exec(`INSERT INTO channel_items (channel_id, media_file_id, position) VALUES (?, ?, 0)`, scifiChannel, existing); err != nil {
		t.Fatalf("seed existing channel_items: %v", err)
	}
	insertClassified(t, db, "episode", "New Show", 1, 1, 0, 600, `["scifi"]`) // should NOT get added

	populated, err := Populate(context.Background(), db)
	if err != nil {
		t.Fatalf("Populate: %v", err)
	}
	if populated != 0 {
		t.Errorf("populated = %d, want 0 (channel already has content)", populated)
	}
	got := channelItemMediaFileIDs(t, db, scifiChannel)
	if len(got) != 1 || got[0] != existing {
		t.Errorf("channel_items = %v, want untouched [%d]", got, existing)
	}
}

func TestPopulate_NonMatchingContent_LeavesChannelEmpty(t *testing.T) {
	db := openTestDB(t)
	scifiChannel := insertChannel(t, db, 129, "Sci-Fi Network")
	insertClassified(t, db, "episode", "A Comedy Show", 1, 1, 0, 600, `["comedy"]`)

	populated, err := Populate(context.Background(), db)
	if err != nil {
		t.Fatalf("Populate: %v", err)
	}
	if populated != 0 {
		t.Errorf("populated = %d, want 0 (no matching content)", populated)
	}
	if got := channelItemMediaFileIDs(t, db, scifiChannel); len(got) != 0 {
		t.Errorf("channel_items = %v, want empty", got)
	}
}

func TestPopulate_CustomChannelNumber_IsNeverTouched(t *testing.T) {
	db := openTestDB(t)
	customChannel := insertChannel(t, db, 200, "My Custom Channel")
	insertClassified(t, db, "episode", "Some Show", 1, 1, 0, 600, `["scifi"]`)

	populated, err := Populate(context.Background(), db)
	if err != nil {
		t.Fatalf("Populate: %v", err)
	}
	if populated != 0 {
		t.Errorf("populated = %d, want 0 (200 isn't a built-in default channel number)", populated)
	}
	if got := channelItemMediaFileIDs(t, db, customChannel); len(got) != 0 {
		t.Errorf("channel_items = %v, want empty", got)
	}
}

func TestPopulate_UnclassifiedContent_IsIgnored(t *testing.T) {
	db := openTestDB(t)
	scifiChannel := insertChannel(t, db, 129, "Sci-Fi Network")
	ctx := context.Background()
	now := time.Now().Unix()
	var mediaFileID int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO media_files (root_kind, path, size_bytes, modified_unix, first_seen_unix, last_seen_unix, duration_seconds, status)
		VALUES ('media', '/media/unclassified.mkv', 1024, ?, ?, ?, 600, 'ok') RETURNING id`,
		now, now, now).Scan(&mediaFileID); err != nil {
		t.Fatalf("insert media_files: %v", err)
	}
	// genre_tags left NULL — not yet classified.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO identified_items (media_file_id, media_type, title, season_number, episode_number)
		VALUES (?, 'episode', 'Some Show', 1, 1)`, mediaFileID); err != nil {
		t.Fatalf("insert identified_items: %v", err)
	}

	populated, err := Populate(context.Background(), db)
	if err != nil {
		t.Fatalf("Populate: %v", err)
	}
	if populated != 0 {
		t.Errorf("populated = %d, want 0 (content isn't classified yet)", populated)
	}
	if got := channelItemMediaFileIDs(t, db, scifiChannel); len(got) != 0 {
		t.Errorf("channel_items = %v, want empty", got)
	}
}

func channelHidden(t *testing.T, db *sql.DB, channelID int64) bool {
	t.Helper()
	var hidden bool
	if err := db.QueryRowContext(context.Background(), `SELECT hidden FROM channels WHERE id = ?`, channelID).Scan(&hidden); err != nil {
		t.Fatalf("query hidden: %v", err)
	}
	return hidden
}

func TestPopulate_LeavesVisibilityAloneWhenAutoHideIsOff(t *testing.T) {
	db := openTestDB(t)
	scifiChannel := insertChannel(t, db, 129, "Sci-Fi Network")
	emptyChannel := insertChannel(t, db, 130, "Fantasy Network")
	insertClassified(t, db, "episode", "Some Show", 1, 1, 0, 600, `["scifi"]`)

	if _, err := Populate(context.Background(), db); err != nil {
		t.Fatalf("Populate: %v", err)
	}
	if channelHidden(t, db, scifiChannel) {
		t.Error("a channel with content must stay visible")
	}
	if !channelHidden(t, db, emptyChannel) {
		t.Error("an empty Auto channel is hidden")
	}
	if err := channel.SetVisibility(context.Background(), db, []int64{emptyChannel}, channel.VisibilityShow); err != nil {
		t.Fatal(err)
	}
	if _, err := Populate(context.Background(), db); err != nil {
		t.Fatalf("Populate: %v", err)
	}
	if channelHidden(t, db, emptyChannel) {
		t.Error("Populate hid an empty channel set to Show")
	}
}

func TestPopulate_AutoHideOn_HidesEmptyAndKeepsPopulatedVisible(t *testing.T) {
	db := openTestDB(t)
	scifiChannel := insertChannel(t, db, 129, "Sci-Fi Network")
	emptyChannel := insertChannel(t, db, 130, "Fantasy Network")
	insertClassified(t, db, "episode", "Some Show", 1, 1, 0, 600, `["scifi"]`)

	if _, err := Populate(context.Background(), db); err != nil {
		t.Fatalf("Populate: %v", err)
	}
	if channelHidden(t, db, scifiChannel) {
		t.Error("channel with content should stay visible")
	}
	if !channelHidden(t, db, emptyChannel) {
		t.Error("channel with no content should be hidden")
	}
}

func TestPopulate_TwoDistinctShows_DefaultsToVisible(t *testing.T) {
	db := openTestDB(t)
	scifiChannel := insertChannel(t, db, 129, "Sci-Fi Network")
	insertClassified(t, db, "episode", "Show A", 1, 1, 0, 600, `["scifi"]`)
	insertClassified(t, db, "episode", "Show B", 1, 1, 0, 600, `["scifi"]`)

	if _, err := Populate(context.Background(), db); err != nil {
		t.Fatalf("Populate: %v", err)
	}
	if channelHidden(t, db, scifiChannel) {
		t.Error("channel with 2 distinct shows should default to visible")
	}
}

func TestRefresh_AutoHideOn_ShowsNewChannelsAndHidesEmptiedOnes(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	scifiChannel := insertChannel(t, db, 129, "Sci-Fi Network")
	fantasyChannel := insertChannel(t, db, 130, "Fantasy Network")
	insertClassified(t, db, "episode", "Space Show", 1, 1, 0, 600, `["scifi"]`)
	if _, err := Refresh(ctx, db); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if channelHidden(t, db, scifiChannel) || !channelHidden(t, db, fantasyChannel) {
		t.Fatalf("after first refresh: scifi hidden=%v (want false), fantasy hidden=%v (want true)",
			channelHidden(t, db, scifiChannel), channelHidden(t, db, fantasyChannel))
	}

	// New media arrives for the hidden channel; the sci-fi media goes away.
	insertClassified(t, db, "episode", "Dragon Show", 1, 1, 0, 600, `["fantasy"]`)
	if _, err := db.Exec(`DELETE FROM identified_items WHERE series_title = 'Space Show'`); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := Refresh(ctx, db); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if channelHidden(t, db, fantasyChannel) {
		t.Error("channel that gained content should be shown automatically")
	}
	if !channelHidden(t, db, scifiChannel) {
		t.Error("channel that lost all content should be hidden automatically")
	}
}

func TestRefresh_AutoHideOff_KeepsManualVisibility(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	scifiChannel := insertChannel(t, db, 129, "Sci-Fi Network")
	fantasyChannel := insertChannel(t, db, 130, "Fantasy Network")
	insertClassified(t, db, "episode", "Space Show", 1, 1, 0, 600, `["scifi"]`)
	insertClassified(t, db, "episode", "Dragon Show", 1, 1, 0, 600, `["fantasy"]`)
	if err := channel.SetVisibility(ctx, db, []int64{scifiChannel}, channel.VisibilityHide); err != nil {
		t.Fatalf("SetVisibility: %v", err)
	}

	if _, err := Refresh(ctx, db); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !channelHidden(t, db, scifiChannel) {
		t.Error("manually hidden channel was revealed by a refresh")
	}
	if channelHidden(t, db, fantasyChannel) {
		t.Error("visible channel was hidden by a refresh")
	}
}

func TestRefresh_RebuildsAlreadyPopulatedChannel(t *testing.T) {
	db := openTestDB(t)
	scifiChannel := insertChannel(t, db, 129, "Sci-Fi Network")
	insertClassified(t, db, "episode", "Show A", 1, 1, 0, 600, `["scifi"]`)
	insertClassified(t, db, "episode", "Show B", 1, 1, 0, 600, `["scifi"]`)
	if _, err := Populate(context.Background(), db); err != nil {
		t.Fatalf("Populate: %v", err)
	}
	before := channelItemMediaFileIDs(t, db, scifiChannel)
	if len(before) == 0 {
		t.Fatal("setup: expected Populate to have filled the channel")
	}

	// New content shows up after the initial populate (e.g. a later
	// scan+classify pass) — Refresh must pick it up, unlike Populate,
	// which would skip this now-non-empty channel entirely.
	insertClassified(t, db, "episode", "Show C", 1, 1, 0, 600, `["scifi"]`)

	refreshed, err := Refresh(context.Background(), db)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if refreshed != 1 {
		t.Errorf("refreshed = %d, want 1", refreshed)
	}
	after := channelItemMediaFileIDs(t, db, scifiChannel)
	if len(after) != 3 {
		t.Errorf("channel_items after refresh = %v, want 3 items (Show A/B/C)", after)
	}
}

func TestRefresh_NeverTouchesCustomChannels(t *testing.T) {
	db := openTestDB(t)
	customChannel := insertChannel(t, db, 200, "My Custom Channel")
	insertClassified(t, db, "episode", "Show A", 1, 1, 0, 600, `["scifi"]`)
	insertClassified(t, db, "episode", "Show B", 1, 1, 0, 600, `["scifi"]`)

	refreshed, err := Refresh(context.Background(), db)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if refreshed != 0 {
		t.Errorf("refreshed = %d, want 0 (200 isn't a default channel number)", refreshed)
	}
	if got := channelItemMediaFileIDs(t, db, customChannel); len(got) != 0 {
		t.Errorf("channel_items = %v, want untouched (empty)", got)
	}
}

func TestRefresh_RepeatedCalls_DoNotDuplicateItems(t *testing.T) {
	db := openTestDB(t)
	scifiChannel := insertChannel(t, db, 129, "Sci-Fi Network")
	insertClassified(t, db, "episode", "Show A", 1, 1, 0, 600, `["scifi"]`)
	insertClassified(t, db, "episode", "Show B", 1, 1, 0, 600, `["scifi"]`)

	if _, err := Refresh(context.Background(), db); err != nil {
		t.Fatalf("Refresh (1st): %v", err)
	}
	if _, err := Refresh(context.Background(), db); err != nil {
		t.Fatalf("Refresh (2nd): %v", err)
	}
	if got := channelItemMediaFileIDs(t, db, scifiChannel); len(got) != 2 {
		t.Errorf("channel_items after 2 refreshes = %v, want still just 2 (cleared before each rebuild)", got)
	}
}
