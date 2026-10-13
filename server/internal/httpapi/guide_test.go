package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
)

// insertTestMediaFile inserts a media_files row plus a classified (movie)
// identified_items row for it — a distinct "show", keyed by path so
// multiple calls each count as their own show.
func insertTestMediaFile(t *testing.T, db *sql.DB, path string, durationSeconds float64) int64 {
	return insertRatedMediaFile(t, db, path, durationSeconds, "")
}

// insertRatedMediaFile is insertTestMediaFile with an explicit movie
// content_rating — certification == "" leaves it NULL, same as an
// unenriched row.
func insertRatedMediaFile(t *testing.T, db *sql.DB, path string, durationSeconds float64, certification string) int64 {
	t.Helper()
	ctx := context.Background()
	now := time.Now().Unix()
	var id int64
	err := db.QueryRowContext(ctx, `
		INSERT INTO media_files (root_kind, path, size_bytes, modified_unix, first_seen_unix, last_seen_unix, duration_seconds, status)
		VALUES ('media', ?, 1024, ?, ?, ?, ?, 'ok') RETURNING id`,
		path, now, now, now, durationSeconds).Scan(&id)
	if err != nil {
		t.Fatalf("insert media_files: %v", err)
	}
	var cert sql.NullString
	if certification != "" {
		cert = sql.NullString{String: certification, Valid: true}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO identified_items (media_file_id, media_type, title, content_rating) VALUES (?, 'movie', ?, ?)`, id, path, cert); err != nil {
		t.Fatalf("insert identified_items: %v", err)
	}
	return id
}

func insertTestChannelWithContent(t *testing.T, db *sql.DB, number int, name string, mediaFileIDs ...int64) int64 {
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

func setChannelHidden(t *testing.T, db *sql.DB, channelID int64, hidden bool) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), `UPDATE channels SET hidden = ? WHERE id = ?`, hidden, channelID); err != nil {
		t.Fatalf("set channel hidden: %v", err)
	}
}

// regenerateSchedule brings channelID's persisted schedule up to date as
// of right now — the real HTTP handlers (handleGuide, handleChannelPreview)
// use schedule.SystemClock{} internally, so tests need real, current
// schedule_entries rows to find, not just channel_items.
func regenerateSchedule(t *testing.T, db *sql.DB, channelID int64) {
	t.Helper()
	if err := schedule.Regenerate(context.Background(), db, channelID, time.Now()); err != nil {
		t.Fatalf("schedule.Regenerate: %v", err)
	}
}

func TestGuideAPI_ReturnsProgramsForEveryChannel(t *testing.T) {
	db := openTestDB(t)
	a := insertTestMediaFile(t, db, "/tv/show-a.mkv", 600)
	b := insertTestMediaFile(t, db, "/tv/show-b.mkv", 600)
	channelID := insertTestChannelWithContent(t, db, 1000, "Test Channel", a, b)
	regenerateSchedule(t, db, channelID)
	router := NewRouter(t.TempDir(), t.TempDir(), db)

	req := httptest.NewRequest(http.MethodGet, "/api/guide", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got []channelGuideResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].ChannelID != channelID {
		t.Fatalf("got = %+v, want one entry for channel %d", got, channelID)
	}
	if len(got[0].Programs) == 0 {
		t.Errorf("Programs is empty, want at least the one scheduled program")
	}
}

func TestGuideAPI_ReportsEachChannelsAudienceTier(t *testing.T) {
	db := openTestDB(t)
	media := insertTestMediaFile(t, db, "/tv/show.mkv", 600)
	kids := insertTestChannelWithContent(t, db, 1000, "Kids Channel", media)
	insertTestChannelWithContent(t, db, 1001, "Unfiltered Channel", media)
	if _, err := db.Exec(`INSERT INTO channel_filters (channel_id, audience_tier, genres, media_type, era, title_contains) VALUES (?, 'young_child', '[]', 'both', 'both', '')`, kids); err != nil {
		t.Fatalf("insert channel_filters: %v", err)
	}
	router := NewRouter(t.TempDir(), t.TempDir(), db)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/guide", nil))
	var got []channelGuideResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	tiers := map[string]string{}
	for _, c := range got {
		tiers[c.Name] = c.AudienceTier
	}
	if tiers["Kids Channel"] != "young_child" || tiers["Unfiltered Channel"] != "" {
		t.Errorf("audience tiers = %v, want young_child for the filtered channel and none for the other", tiers)
	}
}

func TestGuideAPI_EmptyOrSparseChannel_IsVisibleByDefault(t *testing.T) {
	// channels.hidden defaults to false — a freshly-created channel
	// (e.g. from the Admin Channels page) shows up right away even with
	// no/little content yet; only internal/lineup's population-time
	// default (or the admin's own toggle / "Hide Empty") actually hides
	// one. See #90's follow-on.
	db := openTestDB(t)
	insertTestChannelWithContent(t, db, 1000, "Empty Channel")
	media := insertTestMediaFile(t, db, "/tv/show.mkv", 600)
	oneShowChannelID := insertTestChannelWithContent(t, db, 1001, "One-Show Channel", media)
	regenerateSchedule(t, db, oneShowChannelID)
	router := NewRouter(t.TempDir(), t.TempDir(), db)

	req := httptest.NewRequest(http.MethodGet, "/api/guide", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got []channelGuideResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got = %+v, want both channels visible", got)
	}
}

func TestGuideAPI_ExplicitlyHiddenChannel_IsExcludedRegardlessOfContent(t *testing.T) {
	db := openTestDB(t)
	a := insertTestMediaFile(t, db, "/tv/show-a.mkv", 600)
	b := insertTestMediaFile(t, db, "/tv/show-b.mkv", 600)
	channelID := insertTestChannelWithContent(t, db, 1000, "Well-Stocked But Hidden", a, b)
	regenerateSchedule(t, db, channelID)
	setChannelHidden(t, db, channelID, true)
	router := NewRouter(t.TempDir(), t.TempDir(), db)

	req := httptest.NewRequest(http.MethodGet, "/api/guide", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var got []channelGuideResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got = %+v, want hidden despite having 2 distinct shows (admin explicitly hid it)", got)
	}
}

func TestGuideAPI_AudienceTierFilter_ExcludesIneligibleChannel(t *testing.T) {
	db := openTestDB(t)
	kid := insertRatedMediaFile(t, db, "/movies/g.mkv", 600, "G")
	kidChannelID := insertTestChannelWithContent(t, db, 1000, "Kids Channel", kid)
	regenerateSchedule(t, db, kidChannelID)

	adultMovie := insertRatedMediaFile(t, db, "/movies/r.mkv", 600, "R")
	adultChannelID := insertTestChannelWithContent(t, db, 1001, "Adult Channel", adultMovie)
	regenerateSchedule(t, db, adultChannelID)

	router := NewRouter(t.TempDir(), t.TempDir(), db)
	req := httptest.NewRequest(http.MethodGet, "/api/guide?audienceTier=young_child", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got []channelGuideResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].ChannelID != kidChannelID {
		t.Fatalf("got = %+v, want only the young_child-eligible channel", got)
	}
}

func TestGuideAPI_AudienceTierFilter_Unspecified_IncludesEverything(t *testing.T) {
	db := openTestDB(t)
	adultMovie := insertRatedMediaFile(t, db, "/movies/r.mkv", 600, "R")
	channelID := insertTestChannelWithContent(t, db, 1000, "Adult Channel", adultMovie)
	regenerateSchedule(t, db, channelID)
	router := NewRouter(t.TempDir(), t.TempDir(), db)

	req := httptest.NewRequest(http.MethodGet, "/api/guide", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var got []channelGuideResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got = %+v, want the channel visible when no audienceTier is requested", got)
	}
}

func TestChannelPreviewAPI_ReturnsCurrentProgram(t *testing.T) {
	db := openTestDB(t)
	media := insertTestMediaFile(t, db, "/tv/show.mkv", 600)
	channelID := insertTestChannelWithContent(t, db, 1000, "Test Channel", media)
	regenerateSchedule(t, db, channelID)
	router := NewRouter(t.TempDir(), t.TempDir(), db)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/channels/%d/preview", channelID), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got previewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Program.MediaFileID != media {
		t.Errorf("got.Program.MediaFileID = %d, want %d", got.Program.MediaFileID, media)
	}
}

func TestChannelPreviewAPI_UnknownChannel_ReturnsNotFound(t *testing.T) {
	db := openTestDB(t)
	router := NewRouter(t.TempDir(), t.TempDir(), db)

	req := httptest.NewRequest(http.MethodGet, "/api/channels/999/preview", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestChannelPreviewAPI_EmptyLineup_ReturnsNotFound(t *testing.T) {
	db := openTestDB(t)
	channelID := insertTestChannelWithContent(t, db, 1000, "Empty Channel")
	router := NewRouter(t.TempDir(), t.TempDir(), db)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/channels/%d/preview", channelID), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d, body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestGuideAPI_AudienceTierFilter_HidesChannelsSetUpForHigherTiers(t *testing.T) {
	db := openTestDB(t)
	g := insertRatedMediaFile(t, db, "/movies/g.mkv", 600, "G")
	add := func(number int, name, tier string) int64 {
		id := insertTestChannelWithContent(t, db, number, name, g)
		regenerateSchedule(t, db, id)
		if _, err := db.Exec(`INSERT INTO channel_filters (channel_id, audience_tier, genres, media_type, era, title_contains) VALUES (?, ?, '[]', 'both', 'both', '')`, id, tier); err != nil {
			t.Fatal(err)
		}
		return id
	}
	kids := add(1000, "Kids", "young_child")
	add(1001, "Teens", "teen")
	add(1002, "Adults", "adult")
	router := NewRouter(t.TempDir(), t.TempDir(), db)

	ask := func(tier string) []int64 {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/guide?audienceTier="+tier, nil))
		var got []channelGuideResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		var ids []int64
		for _, c := range got {
			ids = append(ids, c.ChannelID)
		}
		return ids
	}
	if got := ask("young_child"); len(got) != 1 || got[0] != kids {
		t.Errorf("a kids profile saw %v, want only the kids channel %d", got, kids)
	}
	if got := ask("teen"); len(got) != 2 {
		t.Errorf("a teen profile saw %v, want the kids and teen channels", got)
	}
	if got := ask("adult"); len(got) != 3 {
		t.Errorf("an adult profile saw %v, want all three", got)
	}
}

func TestGuideAPI_CarriesYearRatingAndAirDate(t *testing.T) {
	db := openTestDB(t)
	movie := insertRatedMediaFile(t, db, "/movies/mulan.mkv", 600, "PG")
	if _, err := db.Exec(`UPDATE identified_items SET release_year = 1998, release_date = '1998-06-19' WHERE media_file_id = ?`, movie); err != nil {
		t.Fatal(err)
	}
	channelID := insertTestChannelWithContent(t, db, 1000, "Movies", movie)
	regenerateSchedule(t, db, channelID)
	rec := httptest.NewRecorder()
	NewRouter(t.TempDir(), t.TempDir(), db).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/guide", nil))
	var got []channelGuideResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got) != 1 || len(got[0].Programs) == 0 {
		t.Fatalf("%v %s", err, rec.Body.String())
	}
	p := got[0].Programs[0]
	if p.Year != 1998 || p.ContentRating != "PG" || p.AirDate != "1998-06-19" {
		t.Errorf("program = %+v, want year 1998, rating PG, date 1998-06-19", p)
	}
}
