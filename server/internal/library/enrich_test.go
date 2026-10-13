package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/metadata"
	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

func openClassifyTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db.DB
}

// insertUnclassified inserts a media_files + identified_items row with
// genre_tags left NULL, and returns the identified_items row's id.
func insertUnclassified(t *testing.T, db *sql.DB, mediaType, title, seriesTitle string, releaseYear int) int64 {
	t.Helper()
	ctx := context.Background()
	now := time.Now().Unix()
	var mediaFileID int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO media_files (root_kind, path, size_bytes, modified_unix, first_seen_unix, last_seen_unix)
		VALUES ('media', ?, 1024, ?, ?, ?) RETURNING id`,
		"/media/"+title+".mkv", now, now, now).Scan(&mediaFileID); err != nil {
		t.Fatalf("insert media_files: %v", err)
	}

	var year sql.NullInt64
	if releaseYear > 0 {
		year = sql.NullInt64{Int64: int64(releaseYear), Valid: true}
	}
	var id int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO identified_items (media_file_id, media_type, title, series_title, release_year)
		VALUES (?, ?, ?, NULLIF(?, ''), ?) RETURNING id`,
		mediaFileID, mediaType, title, seriesTitle, year).Scan(&id); err != nil {
		t.Fatalf("insert identified_items: %v", err)
	}
	return id
}

func genreTagsOf(t *testing.T, db *sql.DB, id int64) sql.NullString {
	t.Helper()
	var tags sql.NullString
	if err := db.QueryRowContext(context.Background(), `SELECT genre_tags FROM identified_items WHERE id = ?`, id).Scan(&tags); err != nil {
		t.Fatalf("query genre_tags: %v", err)
	}
	return tags
}

func mockTMDbClient(t *testing.T, body string) *metadata.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	client := metadata.NewClient("test-key")
	client.HTTPClient = server.Client()
	client.BaseURL = server.URL
	return client
}

func TestClassifyGenres_PersistsMappedTags(t *testing.T) {
	db := openClassifyTestDB(t)
	id := insertUnclassified(t, db, "movie", "Some Movie", "", 2020)
	client := mockTMDbClient(t, `{"results":[{"id":1,"genre_ids":[28,35]}]}`)

	if err := ClassifyGenres(context.Background(), db, client); err != nil {
		t.Fatalf("ClassifyGenres: %v", err)
	}

	raw := genreTagsOf(t, db, id)
	if !raw.Valid {
		t.Fatal("genre_tags is NULL, want a persisted result")
	}
	var tags []string
	if err := json.Unmarshal([]byte(raw.String), &tags); err != nil {
		t.Fatalf("unmarshal genre_tags: %v", err)
	}
	if len(tags) != 2 {
		t.Errorf("tags = %v, want 2 entries (action, comedy)", tags)
	}
}

func TestClassifyGenres_NoMatch_PersistsEmptyArrayNotNull(t *testing.T) {
	db := openClassifyTestDB(t)
	id := insertUnclassified(t, db, "movie", "Nonexistent Movie", "", 2020)
	client := mockTMDbClient(t, `{"results":[]}`)

	if err := ClassifyGenres(context.Background(), db, client); err != nil {
		t.Fatalf("ClassifyGenres: %v", err)
	}

	raw := genreTagsOf(t, db, id)
	if !raw.Valid {
		t.Fatal("genre_tags is NULL, want a persisted empty array (so this row isn't retried forever)")
	}
	if raw.String != "[]" {
		t.Errorf("genre_tags = %q, want %q", raw.String, "[]")
	}
}

func TestClassifyGenres_DisabledClient_LeavesRowsNullForLaterRetry(t *testing.T) {
	db := openClassifyTestDB(t)
	id := insertUnclassified(t, db, "movie", "Some Movie", "", 2020)

	if err := ClassifyGenres(context.Background(), db, metadata.NewClient("")); err != nil {
		t.Fatalf("ClassifyGenres: %v", err)
	}

	if raw := genreTagsOf(t, db, id); raw.Valid {
		t.Errorf("genre_tags = %q, want NULL (untouched) with no API key configured", raw.String)
	}
}

func TestClassifyGenres_AlreadyClassifiedRow_IsNotReQueried(t *testing.T) {
	db := openClassifyTestDB(t)
	id := insertUnclassified(t, db, "movie", "Some Movie", "", 2020)
	if _, err := db.Exec(`UPDATE identified_items SET genre_tags = '["action"]' WHERE id = ?`, id); err != nil {
		t.Fatalf("pre-set genre_tags: %v", err)
	}

	requested := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = true
	}))
	defer server.Close()
	client := metadata.NewClient("test-key")
	client.HTTPClient = server.Client()
	client.BaseURL = server.URL

	if err := ClassifyGenres(context.Background(), db, client); err != nil {
		t.Fatalf("ClassifyGenres: %v", err)
	}
	if requested {
		t.Error("ClassifyGenres re-queried an already-classified row")
	}
	if raw := genreTagsOf(t, db, id); raw.String != `["action"]` {
		t.Errorf("genre_tags = %q, want the original value untouched", raw.String)
	}
}

func TestClassifyGenres_Episode_UsesSeriesTitleNotEpisodeTitle(t *testing.T) {
	db := openClassifyTestDB(t)
	id := insertUnclassified(t, db, "episode", "Episode One Title", "The Series Name", 0)

	var capturedQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"id":1,"genre_ids":[18]}]}`))
	}))
	defer server.Close()
	client := metadata.NewClient("test-key")
	client.HTTPClient = server.Client()
	client.BaseURL = server.URL

	if err := ClassifyGenres(context.Background(), db, client); err != nil {
		t.Fatalf("ClassifyGenres: %v", err)
	}
	if capturedQuery != "The Series Name" {
		t.Errorf("query = %q, want the series title, not the episode title", capturedQuery)
	}
	if raw := genreTagsOf(t, db, id); !raw.Valid {
		t.Error("genre_tags is NULL, want a persisted result")
	}
}

// mockTMDbPathRoutedClient serves a different body per exact request
// path — EnrichMetadata's movie/episode flows each make multiple
// requests (search, then details, then release_dates/content_ratings),
// unlike ClassifyGenres' single search call.
func mockTMDbPathRoutedClient(t *testing.T, byPath map[string]string) (*metadata.Client, *int) {
	t.Helper()
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		key := r.URL.Path
		if r.URL.RawQuery != "" {
			key += "?" + r.URL.RawQuery
		}
		body, ok := byPath[key]
		if !ok {
			body, ok = byPath[r.URL.Path]
		}
		if !ok {
			t.Errorf("unexpected request %s", key)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	client := metadata.NewClient("test-key")
	client.HTTPClient = server.Client()
	client.BaseURL = server.URL
	return client, &requestCount
}

func metadataFetchedAtOf(t *testing.T, db *sql.DB, id int64) sql.NullInt64 {
	t.Helper()
	var v sql.NullInt64
	if err := db.QueryRowContext(context.Background(), `SELECT metadata_fetched_at_unix FROM identified_items WHERE id = ?`, id).Scan(&v); err != nil {
		t.Fatalf("query metadata_fetched_at_unix: %v", err)
	}
	return v
}

func TestEnrichMetadata_Movie_PersistsOverviewDateAndCertification(t *testing.T) {
	db := openClassifyTestDB(t)
	id := insertUnclassified(t, db, "movie", "Fight Club", "", 1999)
	client, _ := mockTMDbPathRoutedClient(t, map[string]string{
		"/search/movie":            `{"results":[{"id":550,"genre_ids":[18]}]}`,
		"/movie/550":               `{"overview":"An insomniac office worker...","release_date":"1999-10-15"}`,
		"/movie/550/release_dates": `{"results":[{"iso_3166_1":"US","release_dates":[{"certification":"R","type":3}]}]}`,
	})

	if err := EnrichMetadata(context.Background(), db, client); err != nil {
		t.Fatalf("EnrichMetadata: %v", err)
	}

	var overview, releaseDate, cert sql.NullString
	var tmdbID sql.NullInt64
	if err := db.QueryRow(`SELECT overview, release_date, content_rating, tmdb_id FROM identified_items WHERE id = ?`, id).
		Scan(&overview, &releaseDate, &cert, &tmdbID); err != nil {
		t.Fatalf("query: %v", err)
	}
	if overview.String != "An insomniac office worker..." || releaseDate.String != "1999-10-15" || cert.String != "R" || tmdbID.Int64 != 550 {
		t.Errorf("overview=%q releaseDate=%q cert=%q tmdbID=%v", overview.String, releaseDate.String, cert.String, tmdbID)
	}
	if !metadataFetchedAtOf(t, db, id).Valid {
		t.Error("metadata_fetched_at_unix is NULL, want it stamped")
	}
}

func TestEnrichMetadata_Movie_NoMatch_MarksAttemptedWithoutRetrying(t *testing.T) {
	db := openClassifyTestDB(t)
	id := insertUnclassified(t, db, "movie", "Nonexistent Movie", "", 2020)
	client, requests := mockTMDbPathRoutedClient(t, map[string]string{
		"/search/movie": `{"results":[]}`,
	})

	if err := EnrichMetadata(context.Background(), db, client); err != nil {
		t.Fatalf("EnrichMetadata: %v", err)
	}
	if !metadataFetchedAtOf(t, db, id).Valid {
		t.Error("metadata_fetched_at_unix is NULL, want it stamped (so this row isn't retried forever)")
	}
	firstCount := *requests

	if err := EnrichMetadata(context.Background(), db, client); err != nil {
		t.Fatalf("EnrichMetadata (2nd call): %v", err)
	}
	if *requests != firstCount {
		t.Errorf("2nd EnrichMetadata call made %d more request(s), want the already-attempted row skipped", *requests-firstCount)
	}
}

func TestEnrichMetadata_Episode_CreatesShowRowAndLinksEpisode(t *testing.T) {
	db := openClassifyTestDB(t)
	id := insertUnclassified(t, db, "episode", "Winter Is Coming", "Game of Thrones", 0)
	client, _ := mockTMDbPathRoutedClient(t, map[string]string{
		"/search/tv":                  `{"results":[{"id":1399,"genre_ids":[18]}]}`,
		"/tv/1399":                    `{"overview":"Nine noble families...","first_air_date":"2011-04-17","last_air_date":"2019-05-19"}`,
		"/tv/1399/content_ratings":    `{"results":[{"iso_3166_1":"US","rating":"TV-MA"}]}`,
		"/tv/1399/season/0/episode/0": `{"overview":"Lord Eddard Stark...","air_date":"2011-04-17"}`,
	})

	if err := EnrichMetadata(context.Background(), db, client); err != nil {
		t.Fatalf("EnrichMetadata: %v", err)
	}

	var showID sql.NullInt64
	var episodeOverview, episodeAirDate sql.NullString
	if err := db.QueryRow(`SELECT show_id, overview, episode_air_date FROM identified_items WHERE id = ?`, id).
		Scan(&showID, &episodeOverview, &episodeAirDate); err != nil {
		t.Fatalf("query episode row: %v", err)
	}
	if !showID.Valid {
		t.Fatal("show_id is NULL, want it linked to the created tv_shows row")
	}
	if episodeOverview.String != "Lord Eddard Stark..." || episodeAirDate.String != "2011-04-17" {
		t.Errorf("overview=%q airDate=%q", episodeOverview.String, episodeAirDate.String)
	}

	var showTitle, showOverview, showRating sql.NullString
	var showTMDbID sql.NullInt64
	if err := db.QueryRow(`SELECT title, overview, content_rating, tmdb_id FROM tv_shows WHERE id = ?`, showID.Int64).
		Scan(&showTitle, &showOverview, &showRating, &showTMDbID); err != nil {
		t.Fatalf("query tv_shows row: %v", err)
	}
	if showTitle.String != "Game of Thrones" || showOverview.String != "Nine noble families..." || showRating.String != "TV-MA" || showTMDbID.Int64 != 1399 {
		t.Errorf("title=%q overview=%q rating=%q tmdbID=%v", showTitle.String, showOverview.String, showRating.String, showTMDbID)
	}
}

func TestEnrichMetadata_Episode_SharesOneShowSearchAcrossEpisodes(t *testing.T) {
	db := openClassifyTestDB(t)
	id1 := insertUnclassified(t, db, "episode", "Episode One", "Game of Thrones", 0)
	id2 := insertUnclassified(t, db, "episode", "Episode Two", "Game of Thrones", 0)
	client, _ := mockTMDbPathRoutedClient(t, map[string]string{
		"/search/tv":                  `{"results":[{"id":1399,"genre_ids":[18]}]}`,
		"/tv/1399":                    `{"overview":"Nine noble families...","first_air_date":"2011-04-17"}`,
		"/tv/1399/content_ratings":    `{"results":[]}`,
		"/tv/1399/season/0/episode/0": `{"overview":"An episode.","air_date":"2011-04-17"}`,
	})

	if err := EnrichMetadata(context.Background(), db, client); err != nil {
		t.Fatalf("EnrichMetadata: %v", err)
	}

	var showID1, showID2 sql.NullInt64
	if err := db.QueryRow(`SELECT show_id FROM identified_items WHERE id = ?`, id1).Scan(&showID1); err != nil {
		t.Fatalf("query row 1: %v", err)
	}
	if err := db.QueryRow(`SELECT show_id FROM identified_items WHERE id = ?`, id2).Scan(&showID2); err != nil {
		t.Fatalf("query row 2: %v", err)
	}
	if !showID1.Valid || !showID2.Valid || showID1.Int64 != showID2.Int64 {
		t.Errorf("showID1=%v showID2=%v, want both episodes linked to the same single tv_shows row", showID1, showID2)
	}
	var showCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tv_shows`).Scan(&showCount); err != nil {
		t.Fatalf("count tv_shows: %v", err)
	}
	if showCount != 1 {
		t.Errorf("tv_shows row count = %d, want exactly 1 (one show, not re-created per episode)", showCount)
	}
}

func TestEnrichMetadata_DisabledClient_LeavesRowsUntouched(t *testing.T) {
	db := openClassifyTestDB(t)
	id := insertUnclassified(t, db, "movie", "Some Movie", "", 2020)

	if err := EnrichMetadata(context.Background(), db, metadata.NewClient("")); err != nil {
		t.Fatalf("EnrichMetadata: %v", err)
	}
	if metadataFetchedAtOf(t, db, id).Valid {
		t.Error("metadata_fetched_at_unix is set, want NULL with no API key configured")
	}
}
