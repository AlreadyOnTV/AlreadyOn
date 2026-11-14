package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/alreadyonapp/alreadyon/server/internal/refresh"
	"net/http"
	"strings"
	"testing"
	"time"
)

var filterFixtureCounter int

// insertMovie adds a classified, playable movie to the library.
func insertMovie(t *testing.T, db *sql.DB, title string, year int, rating, tags string) {
	t.Helper()
	filterFixtureCounter++
	now := time.Now().Unix()
	var fileID int64
	if err := db.QueryRow(`INSERT INTO media_files (root_kind, path, size_bytes, modified_unix, first_seen_unix, last_seen_unix, duration_seconds, status)
		VALUES ('media', ?, 1, ?, ?, ?, 600, 'ok') RETURNING id`, fmt.Sprintf("/m/%d.mkv", filterFixtureCounter), now, now, now).Scan(&fileID); err != nil {
		t.Fatalf("insert media_files: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO identified_items (media_file_id, media_type, title, release_year, genre_tags, content_rating)
		VALUES (?, 'movie', ?, ?, ?, ?)`, fileID, title, year, tags, rating); err != nil {
		t.Fatalf("insert identified_items: %v", err)
	}
}

func decodeChannel(t *testing.T, body []byte) channelResponse {
	t.Helper()
	var c channelResponse
	if err := json.Unmarshal(body, &c); err != nil {
		t.Fatalf("decode channel: %v (%s)", err, body)
	}
	return c
}

// Wait on durable applied state, never on incidental log order or elapsed sleeps.
func applyPendingSettings(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); refresh.RunUpdateWorker(ctx, db) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, err := refresh.GetUpdateStatus(t.Context(), db)
		if err != nil {
			t.Fatal(err)
		}
		if state.State == "failed" {
			t.Fatal(state.Error)
		}
		if state.State == "idle" && state.AppliedRevision == state.Revision {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("background settings did not apply")
}

const potterFilter = `{"audienceTier":"teen","genres":[],"mediaType":"movie","era":"both","titleContains":"harry potter"}`

func TestChannels_CreateWithFilterBuildsTheLineup(t *testing.T) {
	db := openTestDB(t)
	router := NewRouter(t.TempDir(), t.TempDir(), db)
	insertMovie(t, db, "Harry Potter and the Goblet of Fire", 2005, "PG-13", `["fantasy"]`)
	insertMovie(t, db, "HARRY POTTER and the Prisoner of Azkaban", 2004, "PG", `["fantasy"]`)
	insertMovie(t, db, "Star Wars", 1977, "PG", `["scifi"]`)

	rec := doJSON(t, router, http.MethodPost, "/api/channels", `{"number":200,"name":"Wizards","filter":`+potterFilter+`}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", rec.Code, rec.Body.String())
	}
	c := decodeChannel(t, rec.Body.Bytes())
	if c.ItemCount != 0 || c.Filter == nil || c.Filter.TitleContains != "harry potter" || c.Filter.MediaType != "movie" {
		t.Errorf("created = %+v (filter %+v), want pending lineup and the saved filter", c, c.Filter)
	}

	applyPendingSettings(t, db)
	if err := db.QueryRow(`SELECT count(*) FROM channel_items WHERE channel_id=?`, c.ID).Scan(&c.ItemCount); err != nil || c.ItemCount != 2 {
		t.Fatalf("background lineup=%d err=%v", c.ItemCount, err)
	}
	rec = doJSON(t, router, http.MethodGet, "/api/channels", "")
	var list []channelResponse
	json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 1 || list[0].Filter == nil || list[0].Filter.AudienceTier != "teen" {
		t.Errorf("list = %+v, want the filter included", list)
	}
}

func TestChannels_UpdateFilterRebuildsAndNullClears(t *testing.T) {
	db := openTestDB(t)
	router := NewRouter(t.TempDir(), t.TempDir(), db)
	insertMovie(t, db, "Harry Potter One", 2001, "PG", `["fantasy"]`)
	insertMovie(t, db, "Funny Movie", 2001, "PG", `["comedy"]`)
	rec := doJSON(t, router, http.MethodPost, "/api/channels", `{"number":200,"name":"Custom","filter":`+potterFilter+`}`)
	id := decodeChannel(t, rec.Body.Bytes()).ID

	applyPendingSettings(t, db)
	// Renaming without a filter field keeps the filter and the lineup.
	rec = doJSON(t, router, http.MethodPut, fmt.Sprintf("/api/channels/%d", id), `{"number":200,"name":"Renamed"}`)
	c := decodeChannel(t, rec.Body.Bytes())
	if rec.Code != http.StatusOK || c.Name != "Renamed" || c.Filter == nil || c.ItemCount != 1 {
		t.Fatalf("rename = %d %+v, want filter kept", rec.Code, c)
	}

	// A new filter rebuilds the lineup from the library.
	rec = doJSON(t, router, http.MethodPut, fmt.Sprintf("/api/channels/%d", id),
		`{"number":200,"name":"Renamed","filter":{"audienceTier":"adult","genres":["comedy"],"mediaType":"both","era":"both","titleContains":""}}`)
	c = decodeChannel(t, rec.Body.Bytes())
	if c.ItemCount != 1 || c.Filter == nil || len(c.Filter.Genres) != 1 || c.Filter.Genres[0] != "comedy" {
		t.Fatalf("after filter change = %+v", c)
	}
	applyPendingSettings(t, db)
	var title string
	db.QueryRow(`SELECT ii.title FROM channel_items ci JOIN identified_items ii ON ii.media_file_id = ci.media_file_id WHERE ci.channel_id = ?`, id).Scan(&title)
	if title != "Funny Movie" {
		t.Errorf("lineup title = %q, want the comedy", title)
	}

	// The schedule was regenerated for the new lineup.
	var scheduled int
	db.QueryRow(`SELECT COUNT(*) FROM schedule_entries WHERE channel_id = ?`, id).Scan(&scheduled)
	if scheduled == 0 {
		t.Error("no schedule entries after a filter change, want the schedule regenerated")
	}

	// null removes the filter but leaves the current lineup.
	rec = doJSON(t, router, http.MethodPut, fmt.Sprintf("/api/channels/%d", id), `{"number":200,"name":"Renamed","filter":null}`)
	c = decodeChannel(t, rec.Body.Bytes())
	if c.Filter != nil || c.ItemCount != 1 {
		t.Errorf("after null = %+v, want no filter and the lineup kept", c)
	}
}

func TestChannels_InvalidFilterIsRejectedBeforeAnythingIsWritten(t *testing.T) {
	db := openTestDB(t)
	router := NewRouter(t.TempDir(), t.TempDir(), db)
	for name, filter := range map[string]string{
		"missing tier":  `{"genres":[]}`,
		"unknown genre": `{"audienceTier":"adult","genres":["nope"]}`,
		"bad media":     `{"audienceTier":"adult","mediaType":"radio"}`,
		"bad era":       `{"audienceTier":"adult","era":"old"}`,
		"long title":    `{"audienceTier":"adult","titleContains":"` + strings.Repeat("a", 101) + `"}`,
	} {
		rec := doJSON(t, router, http.MethodPost, "/api/channels", `{"number":200,"name":"X","filter":`+filter+`}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (%s)", name, rec.Code, rec.Body.String())
		}
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM channels`).Scan(&n)
	if n != 0 {
		t.Errorf("%d channels created by rejected requests, want 0", n)
	}
}

func TestChannels_BuiltInChannelCanGetAFilter(t *testing.T) {
	db := openTestDB(t)
	router := NewRouter(t.TempDir(), t.TempDir(), db)
	var id int64
	db.QueryRow(`INSERT INTO channels (number, name) VALUES (150, 'Built-in') RETURNING id`).Scan(&id)
	rec := doJSON(t, router, http.MethodPut, fmt.Sprintf("/api/channels/%d", id), `{"number":150,"name":"Built-in","filter":`+potterFilter+`}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if c := decodeChannel(t, rec.Body.Bytes()); c.Filter == nil {
		t.Errorf("built-in channel has no filter after saving one: %s", rec.Body.String())
	}
}

func TestFilterPreviewAndGenres(t *testing.T) {
	db := openTestDB(t)
	router := NewRouter(t.TempDir(), t.TempDir(), db)
	insertMovie(t, db, "Harry Potter One", 2001, "PG", `["fantasy"]`)
	insertMovie(t, db, "Something Else", 2001, "R", `["drama"]`)

	rec := doJSON(t, router, http.MethodPost, "/api/channels/filter-preview", potterFilter)
	var got map[string]int
	json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != http.StatusOK || got["titles"] != 1 || got["items"] != 1 {
		t.Errorf("preview = %d %s, want 1 title / 1 item", rec.Code, rec.Body.String())
	}
	if rec = doJSON(t, router, http.MethodPost, "/api/channels/filter-preview", `{"audienceTier":"adult","genres":["nope"]}`); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid preview status = %d, want 400", rec.Code)
	}

	rec = doJSON(t, router, http.MethodGet, "/api/library/genres", "")
	var genres []map[string]string
	json.Unmarshal(rec.Body.Bytes(), &genres)
	if rec.Code != http.StatusOK || len(genres) < 10 || genres[0]["id"] == "" || genres[0]["label"] == "" {
		t.Errorf("genres = %d %s", rec.Code, rec.Body.String())
	}
}
