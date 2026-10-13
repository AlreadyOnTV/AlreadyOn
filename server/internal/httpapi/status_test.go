package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/status"
)

func getStartupStatus(t *testing.T, handler http.Handler) startupStatusResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/status/startup", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	var got startupStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

func TestStartupStatus_ReportsTrackerStateAndLiveCounts(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().Unix()
	insert := func(kind, path, fileStatus string, duration any, identified bool, genres any) {
		var id int64
		if err := db.QueryRow(`
			INSERT INTO media_files (root_kind, path, size_bytes, modified_unix, first_seen_unix, last_seen_unix, duration_seconds, status)
			VALUES (?, ?, 1, ?, ?, ?, ?, ?) RETURNING id`, kind, path, now, now, now, duration, fileStatus).Scan(&id); err != nil {
			t.Fatalf("insert media_files: %v", err)
		}
		if identified {
			if _, err := db.Exec(`INSERT INTO identified_items (media_file_id, media_type, title, genre_tags) VALUES (?, 'movie', 'T', ?)`, id, genres); err != nil {
				t.Fatalf("insert identified_items: %v", err)
			}
		}
	}
	insert("media", "/m/a.mkv", "ok", 600.0, true, `["comedy"]`)
	insert("media", "/m/b.mkv", "ok", 600.0, true, nil)
	insert("media", "/m/c.mkv", "ok", nil, false, nil)
	insert("media", "/m/gone.mkv", "missing", 600.0, true, `["comedy"]`)
	insert("commercial", "/c/ad.mkv", "ok", 30.0, false, nil)
	var channelID int64
	if err := db.QueryRow(`INSERT INTO channels (number, name) VALUES (200, 'Custom') RETURNING id`).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO channel_items (channel_id, media_file_id, position) VALUES (?, 1, 0), (?, 2, 1)`, channelID, channelID); err != nil {
		t.Fatalf("insert channel_items: %v", err)
	}

	tracker := status.New(time.Now)
	tracker.SetMediaRoots(2)
	tracker.Begin(status.TriggerStartup, status.PhaseClassifying)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status/startup", handleStartupStatus(db, tracker))

	got := getStartupStatus(t, mux)
	if got.Phase != status.PhaseClassifying || !got.Active || got.Trigger != status.TriggerStartup || got.StartupComplete || got.MediaRoots != 2 || got.StartedAt == nil {
		t.Errorf("tracker fields = %+v", got)
	}
	want := libraryCounts{FilesFound: 3, FilesProbed: 2, ItemsIdentified: 2, ItemsClassified: 1, ChannelsWithContent: 1}
	if got.Library != want {
		t.Errorf("library = %+v, want %+v (media only, missing excluded)", got.Library, want)
	}

	tracker.End()
	got = getStartupStatus(t, mux)
	if got.Phase != status.PhaseReady || got.Active || !got.StartupComplete || got.FinishedAt == nil {
		t.Errorf("after End = %+v, want ready/complete", got)
	}
}

func TestNewRouter_ServesStartupStatus(t *testing.T) {
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))
	got := getStartupStatus(t, router)
	if got.Phase == "" {
		t.Error("phase is empty")
	}
}
