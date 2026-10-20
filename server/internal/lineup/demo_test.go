package lineup

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// insertUnclassified inserts an identified media file with no genre tags,
// as a scan without a metadata provider key leaves it.
func insertUnclassified(t *testing.T, db *sql.DB, mediaType, title string, season, episode int) int64 {
	t.Helper()
	ctx := context.Background()
	now := time.Now().Unix()
	path := fmt.Sprintf("/media/%s-%d.mkv", title, atomic.AddInt64(&testPathCounter, 1))
	var id int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO media_files (root_kind, path, size_bytes, modified_unix, first_seen_unix, last_seen_unix, duration_seconds, status)
		VALUES ('media', ?, 1024, ?, ?, ?, 1800, 'ok') RETURNING id`, path, now, now, now).Scan(&id); err != nil {
		t.Fatalf("insert media_files: %v", err)
	}
	var seriesTitle sql.NullString
	if mediaType == "episode" {
		seriesTitle = sql.NullString{String: title, Valid: true}
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO identified_items (media_file_id, media_type, title, series_title, season_number, episode_number)
		VALUES (?, ?, ?, ?, ?, ?)`, id, mediaType, title, seriesTitle, season, episode); err != nil {
		t.Fatalf("insert identified_items: %v", err)
	}
	return id
}

func demoChannelIDs(t *testing.T, db *sql.DB) map[int]int64 {
	t.Helper()
	rows, err := db.Query(`SELECT number, id FROM channels WHERE number BETWEEN 196 AND 199`)
	if err != nil {
		t.Fatalf("query demo channels: %v", err)
	}
	defer rows.Close()
	out := map[int]int64{}
	for rows.Next() {
		var n int
		var id int64
		if err := rows.Scan(&n, &id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[n] = id
	}
	return out
}

// Retiring fallback channels must not discard an already-published airing.
func TestSyncDemoChannelsHidesFallbacksWithoutDeletingSchedules(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	id := insertChannel(t, db, 196, "Demo: TV Mix 1")
	media := insertUnclassified(t, db, "episode", "Example", 1, 1)
	if _, err := db.Exec(`INSERT INTO channel_items(channel_id,media_file_id,position) VALUES(?,?,0)`, id, media); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schedule_entries(channel_id,media_file_id,kind,source_offset_seconds,duration_seconds,starts_at_unix,ends_at_unix) VALUES(?,?,'program',0,600,100,700)`, id, media); err != nil {
		t.Fatal(err)
	}
	if n, err := SyncDemoChannels(ctx, db); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	var hidden, count int
	if err := db.QueryRow(`SELECT hidden FROM channels WHERE id=?`, id).Scan(&hidden); err != nil || hidden != 1 {
		t.Fatal(hidden, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM schedule_entries WHERE channel_id=?`, id).Scan(&count); err != nil || count != 1 {
		t.Fatal("published airing deleted", count, err)
	}
	if n, err := SyncDemoChannels(ctx, db); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}
