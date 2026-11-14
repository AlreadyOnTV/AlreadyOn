package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/commercial"
	"github.com/alreadyonapp/alreadyon/server/internal/refresh"
)

func TestCommercialSettingsAPI_GetDefaultThenPutRoundTrips(t *testing.T) {
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))

	req := httptest.NewRequest(http.MethodGet, "/api/settings/commercials", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got commercialSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if !got.Enabled || got.Mode != "between-only" || got.TVCommercialsPerBreak != 2 || got.MovieCommercialsPerBreak != 5 {
		t.Errorf("default settings = %+v, want enabled/between-only/2/5", got)
	}

	putBody, _ := json.Marshal(map[string]any{
		"enabled": true, "mode": "interval", "intervalMinutes": 15, "tvCommercialsPerBreak": 3, "movieCommercialsPerBreak": 4,
	})
	req = httptest.NewRequest(http.MethodPut, "/api/settings/commercials", bytes.NewReader(putBody))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("put status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/settings/commercials", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get-after-put response: %v", err)
	}
	if !got.Enabled || got.Mode != "interval" || got.IntervalMinutes != 15 || got.TVCommercialsPerBreak != 3 || got.MovieCommercialsPerBreak != 4 {
		t.Errorf("settings after PUT = %+v, want enabled/interval/15/3/4", got)
	}
}

func TestCommercialSettingsAPI_PutInvalidMode_ReturnsBadRequest(t *testing.T) {
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))

	putBody, _ := json.Marshal(map[string]any{"enabled": true, "mode": "hourly", "intervalMinutes": 30, "tvCommercialsPerBreak": 2, "movieCommercialsPerBreak": 5})
	req := httptest.NewRequest(http.MethodPut, "/api/settings/commercials", bytes.NewReader(putBody))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestCommercialSettingsAPI_PutOutOfRangeCommercialsPerBreak_ReturnsBadRequest(t *testing.T) {
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))

	putBody, _ := json.Marshal(map[string]any{"mode": "between-only", "intervalMinutes": 30, "tvCommercialsPerBreak": 9, "movieCommercialsPerBreak": 5})
	req := httptest.NewRequest(http.MethodPut, "/api/settings/commercials", bytes.NewReader(putBody))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

// Saving must not wait for an in-progress scan or perform catalog/schedule work.
func TestCommercialSettingsAPI_QueuesBackgroundUpdate(t *testing.T) {
	db := openTestDB(t)
	channelID := insertTestChannelWithContent(t, db, 200, "Synthetic")
	before, err := commercial.GetSettings(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	next := before
	next.Enabled = !before.Enabled
	body, _ := json.Marshal(toCommercialSettingsResponse(next))
	router := NewRouter(t.TempDir(), t.TempDir(), db)
	refresh.Exclusive(func() {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		req := httptest.NewRequest(http.MethodPut, "/api/settings/commercials", bytes.NewReader(body)).WithContext(ctx)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("save blocked/failed: %d %s", rec.Code, rec.Body.String())
		}
	})
	got, err := commercial.GetSettings(t.Context(), db)
	if err != nil || got != next {
		t.Fatalf("preferences not saved: %+v %v", got, err)
	}
	var pending, rebuild int
	if err = db.QueryRow(`SELECT count(*),coalesce(sum(rebuild),0) FROM library_update_channels WHERE channel_id=?`, channelID).Scan(&pending, &rebuild); err != nil || pending != 1 || rebuild != 0 {
		t.Fatalf("schedule work not queued correctly: %d %d %v", pending, rebuild, err)
	}
	state, err := refresh.GetUpdateStatus(t.Context(), db)
	if err != nil || state.State != "queued" || state.NeedsScan {
		t.Fatalf("unexpected state %+v %v", state, err)
	}
}

func insertClassifiedForRefreshTest(t *testing.T, db *sql.DB, title string, releaseYear int, genreTagsJSON string) int64 {
	t.Helper()
	ctx := context.Background()
	now := time.Now().Unix()
	var mediaFileID int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO media_files (root_kind, path, size_bytes, modified_unix, first_seen_unix, last_seen_unix, duration_seconds, status)
		VALUES ('media', ?, 1024, ?, ?, ?, 3600, 'ok') RETURNING id`,
		"/media/"+title+".mkv", now, now, now).Scan(&mediaFileID); err != nil {
		t.Fatalf("insert media_files: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO identified_items (media_file_id, media_type, title, release_year, genre_tags)
		VALUES (?, 'movie', ?, ?, ?)`, mediaFileID, title, releaseYear, genreTagsJSON); err != nil {
		t.Fatalf("insert identified_items: %v", err)
	}
	return mediaFileID
}

func channelItemMediaFileIDsForRefreshTest(t *testing.T, db *sql.DB, channelID int64) []int64 {
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
