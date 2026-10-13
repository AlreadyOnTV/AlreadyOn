package library

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

func insertTestMediaFile(t *testing.T, db *sql.DB, path string) int64 {
	t.Helper()
	now := time.Now().Unix()
	res, err := db.Exec(`INSERT INTO media_files (root_kind, path, size_bytes, modified_unix, first_seen_unix, last_seen_unix)
		VALUES (?, ?, ?, ?, ?, ?)`, "media", path, 1024, now, now, now)
	if err != nil {
		t.Fatalf("insert media_files: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	return id
}

func TestSaveProbeResult_PersistsOntoMediaFilesRow(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	mediaFileID := insertTestMediaFile(t, db.DB, "/media/movies/example.mkv")

	probe := ProbeResult{
		DurationSeconds: 123.45,
		Container:       "mov,mp4,m4a,3gp,3g2,mj2",
		SizeBytes:       999,
		Streams: []StreamInfo{
			{Index: 0, Kind: StreamVideo, Codec: "h264", Width: 1920, Height: 1080},
			{Index: 1, Kind: StreamAudio, Codec: "aac", Channels: 2},
		},
	}

	if err := SaveProbeResult(context.Background(), db.DB, mediaFileID, probe); err != nil {
		t.Fatalf("SaveProbeResult: %v", err)
	}

	var duration float64
	var container, probeJSON string
	var probedAt int64
	err = db.QueryRow(`SELECT duration_seconds, container, probe_json, probed_at_unix FROM media_files WHERE id = ?`, mediaFileID).
		Scan(&duration, &container, &probeJSON, &probedAt)
	if err != nil {
		t.Fatalf("query persisted probe columns: %v", err)
	}

	if duration != probe.DurationSeconds {
		t.Errorf("duration_seconds = %v, want %v", duration, probe.DurationSeconds)
	}
	if container != probe.Container {
		t.Errorf("container = %q, want %q", container, probe.Container)
	}
	if probeJSON == "" {
		t.Error("expected probe_json to be populated")
	}
	if probedAt == 0 {
		t.Error("expected probed_at_unix to be set")
	}
}

func TestSaveProbeResult_UnknownMediaFileID_ReturnsError(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	err = SaveProbeResult(context.Background(), db.DB, 99999, ProbeResult{})
	if err == nil {
		t.Fatal("expected an error saving a probe result for a nonexistent media file id, got nil")
	}
}
