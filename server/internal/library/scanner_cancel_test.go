package library

import (
	"context"
	"errors"
	"testing"

	"github.com/alreadyonapp/alreadyon/server/internal/storage"
	"github.com/alreadyonapp/alreadyon/server/internal/workrevision"
)

// A partial walk must never turn the unvisited remainder of a live library into missing files.
func TestSupersededScanCannotMarkUnvisitedFilesMissing(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO media_files(root_kind,path,size_bytes,modified_unix,first_seen_unix,last_seen_unix,status) VALUES('media','/synthetic/movie.mkv',1,1,1,1,'ok')`); err != nil {
		t.Fatal(err)
	}
	scanner := Scanner{DB: db.DB}
	if _, err := db.Exec(`UPDATE library_update_state SET scan_revision=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := scanner.markUnvisitedAsMissing(workrevision.WithScan(t.Context(), 0), map[int64]struct{}{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("obsolete scan result: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := scanner.Scan(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled walk result: %v", err)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM media_files`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "ok" {
		t.Fatal("partial scan marked media missing")
	}
}
