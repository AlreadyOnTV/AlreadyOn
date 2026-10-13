package httpapi

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/lineup"
)

// addItem inserts a probed library file and its identified item, returning
// the identified_items id (what the library API addresses items by).
func addItem(t *testing.T, db *sql.DB, mediaType, title, series string, year int, tags string) int64 {
	t.Helper()
	filterFixtureCounter++
	now := time.Now().Unix()
	var fileID int64
	if err := db.QueryRow(`INSERT INTO media_files (root_kind, path, size_bytes, modified_unix, first_seen_unix, last_seen_unix, duration_seconds, status)
		VALUES ('media', ?, 1, ?, ?, ?, 600, 'ok') RETURNING id`, fmt.Sprintf("/lib/%04d.mkv", filterFixtureCounter), now, now, now).Scan(&fileID); err != nil {
		t.Fatalf("insert media_files: %v", err)
	}
	var tagsArg, seriesArg, yearArg any
	if tags != "" {
		tagsArg = tags
	}
	if series != "" {
		seriesArg = series
	}
	if year != 0 {
		yearArg = year
	}
	var id int64
	if err := db.QueryRow(`INSERT INTO identified_items (media_file_id, media_type, title, series_title, season_number, episode_number, release_year, genre_tags)
		VALUES (?, ?, ?, ?, 1, 1, ?, ?) RETURNING id`, fileID, mediaType, title, seriesArg, yearArg, tagsArg).Scan(&id); err != nil {
		t.Fatalf("insert identified_items: %v", err)
	}
	return id
}

type unmatchedResponse struct {
	Total int                     `json:"total"`
	Items []unmatchedItemResponse `json:"items"`
}

func getUnmatched(t *testing.T, router http.Handler, query string) unmatchedResponse {
	t.Helper()
	rec := doJSON(t, router, http.MethodGet, "/api/library/unmatched"+query, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("unmatched status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var r unmatchedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatalf("decode unmatched: %v", err)
	}
	return r
}

func TestUnmatched_ListsItemsWithReasonsAndPages(t *testing.T) {
	db := openTestDB(t)
	router := NewRouter(t.TempDir(), t.TempDir(), db)
	addItem(t, db, "movie", "First", "", 2001, "")
	addItem(t, db, "movie", "Second", "", 2002, `[]`)
	addItem(t, db, "episode", "Ep", "A Show", 2003, `["documentary"]`)

	r := getUnmatched(t, router, "")
	if r.Total != 3 || len(r.Items) != 3 {
		t.Fatalf("unmatched = %+v, want 3 items", r)
	}
	first := r.Items[0]
	if first.Title != "First" || first.Genres != nil || !strings.Contains(first.Reason, "No configured automatic channel filters") || first.Year == nil || *first.Year != 2001 {
		t.Errorf("first = %+v", first)
	}
	if r.Items[2].SeriesTitle == nil || *r.Items[2].SeriesTitle != "A Show" {
		t.Errorf("episode = %+v, want its series title", r.Items[2])
	}

	page := getUnmatched(t, router, "?limit=1&offset=1")
	if page.Total != 3 || len(page.Items) != 1 || page.Items[0].Title != "Second" {
		t.Errorf("page = %+v, want the second item alone with total 3", page)
	}
	for _, q := range []string{"?limit=0", "?limit=500", "?limit=x", "?offset=-1"} {
		if rec := doJSON(t, router, http.MethodGet, "/api/library/unmatched"+q, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, rec.Code)
		}
	}
}

func TestUnmatched_EmptyListIsAnEmptyArray(t *testing.T) {
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))
	rec := doJSON(t, router, http.MethodGet, "/api/library/unmatched", "")
	if !strings.Contains(rec.Body.String(), `"items":[]`) || !strings.Contains(rec.Body.String(), `"total":0`) {
		t.Errorf("body = %s, want an empty items array", rec.Body.String())
	}
}

func TestPatchLibraryItem_CorrectedItemLeavesTheUnmatchedListAfterARefresh(t *testing.T) {
	db := openTestDB(t)
	router := NewRouter(t.TempDir(), t.TempDir(), db)
	// This fixture exercises the full TMDb channel catalog, using stored
	// metadata only (lineup.Refresh never calls providers).
	db.Exec(`UPDATE metadata_settings SET tmdb_api_key='fixture'`)
	// Channel 129 takes unrestricted fantasy/sci-fi TV from any era.
	db.Exec(`INSERT INTO channels (number, name) VALUES (129, 'Sci-Fi')`)
	if _, err := lineup.SeedDefaultFilters(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	id := addItem(t, db, "episode", "Pilot", "Space Show", 0, "")

	if got := getUnmatched(t, router, ""); got.Total != 1 {
		t.Fatalf("before the correction: %+v, want the item unmatched", got)
	}

	rec := doJSON(t, router, http.MethodPatch, fmt.Sprintf("/api/library/items/%d", id), `{"year":2015,"genres":["scifi"],"certification":"TV-MA"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"updated":1`) {
		t.Fatalf("patch = %d %s", rec.Code, rec.Body.String())
	}
	// Saved, but not placed yet: no refresh runs inline.
	got := getUnmatched(t, router, "")
	if got.Total != 1 || got.Items[0].Genres[0] != "scifi" || got.Items[0].Certification == nil || *got.Items[0].Certification != "TV-MA" || !strings.Contains(got.Items[0].Reason, "Eligible but not assigned") {
		t.Fatalf("after the correction: %+v, want it saved and waiting for a refresh", got)
	}

	if _, err := lineup.Refresh(t.Context(), db); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := getUnmatched(t, router, ""); got.Total != 0 {
		t.Errorf("after the refresh: %+v, want the item placed on a channel", got)
	}
}

func TestPatchLibraryItem_Errors(t *testing.T) {
	db := openTestDB(t)
	router := NewRouter(t.TempDir(), t.TempDir(), db)
	id := addItem(t, db, "movie", "A Movie", "", 2001, "")
	path := fmt.Sprintf("/api/library/items/%d", id)

	for body, want := range map[string]int{
		`{"genres":["nope"]}`:       http.StatusBadRequest,
		`{"certification":"TV-14"}`: http.StatusBadRequest,
		`{"year":1}`:                http.StatusBadRequest,
		`{"title":""}`:              http.StatusBadRequest,
		`{}`:                        http.StatusBadRequest,
		`{"surprise":true}`:         http.StatusBadRequest,
		`not json`:                  http.StatusBadRequest,
	} {
		if rec := doJSON(t, router, http.MethodPatch, path, body); rec.Code != want {
			t.Errorf("%s: status = %d, want %d (%s)", body, rec.Code, want, rec.Body.String())
		}
	}
	if rec := doJSON(t, router, http.MethodPatch, "/api/library/items/99999", `{"title":"x"}`); rec.Code != http.StatusNotFound {
		t.Errorf("missing item status = %d, want 404", rec.Code)
	}
	if rec := doJSON(t, router, http.MethodPatch, "/api/library/items/abc", `{"title":"x"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id status = %d, want 400", rec.Code)
	}
}

func TestBulkPatchLibraryItems(t *testing.T) {
	db := openTestDB(t)
	router := NewRouter(t.TempDir(), t.TempDir(), db)
	a := addItem(t, db, "movie", "File A", "", 2001, "")
	b := addItem(t, db, "movie", "File B", "", 2002, "")

	rec := doJSON(t, router, http.MethodPatch, "/api/library/items", fmt.Sprintf(`{"ids":[%d,%d],"mediaType":"episode","seriesTitle":"One Show","genres":["drama"]}`, a, b))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"updated":2`) {
		t.Fatalf("bulk = %d %s", rec.Code, rec.Body.String())
	}
	r := getUnmatched(t, router, "")
	for _, it := range r.Items {
		if it.MediaType != "episode" || it.SeriesTitle == nil || *it.SeriesTitle != "One Show" || len(it.Genres) != 1 {
			t.Errorf("item %+v not updated by the bulk edit", it)
		}
	}

	// An unknown id fails the whole request and leaves the rest alone.
	rec = doJSON(t, router, http.MethodPatch, "/api/library/items", fmt.Sprintf(`{"ids":[%d,99999],"seriesTitle":"Other"}`, a))
	if rec.Code != http.StatusNotFound {
		t.Errorf("bulk with unknown id = %d, want 404", rec.Code)
	}
	for _, it := range getUnmatched(t, router, "").Items {
		if it.SeriesTitle == nil || *it.SeriesTitle != "One Show" {
			t.Errorf("item %+v was changed by a rejected bulk request", it)
		}
	}
	if rec := doJSON(t, router, http.MethodPatch, "/api/library/items", `{"seriesTitle":"x"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bulk with no ids = %d, want 400", rec.Code)
	}
	ids := make([]string, maxBulkItems+1)
	for i := range ids {
		ids[i] = "1"
	}
	if rec := doJSON(t, router, http.MethodPatch, "/api/library/items", `{"ids":[`+strings.Join(ids, ",")+`],"seriesTitle":"x"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bulk over the cap = %d, want 400", rec.Code)
	}
}

func TestListCertifications(t *testing.T) {
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))
	rec := doJSON(t, router, http.MethodGet, "/api/library/certifications", "")
	var got map[string][]string
	json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != http.StatusOK || len(got["movie"]) == 0 || len(got["tv"]) == 0 || got["movie"][0] != "G" || got["tv"][0] != "TV-Y" {
		t.Errorf("certifications = %d %s", rec.Code, rec.Body.String())
	}
}
