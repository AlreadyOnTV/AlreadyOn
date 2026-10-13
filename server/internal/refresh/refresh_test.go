package refresh

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/status"
	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

func TestRun_ReturnsErrBusyWhileExclusiveHeld(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	var runErr error
	Exclusive(func() {
		_, runErr = Run(context.Background(), db.DB)
	})
	if !errors.Is(runErr, ErrBusy) {
		t.Fatalf("Run during Exclusive = %v, want ErrBusy", runErr)
	}

	if _, err := Run(context.Background(), db.DB); err != nil {
		t.Fatalf("Run after Exclusive released = %v, want success", err)
	}
}

func TestRunAs_ReportsRunToStatusTracker(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := RunAs(context.Background(), db.DB, status.TriggerPoll); err != nil {
		t.Fatalf("RunAs: %v", err)
	}
	s := status.Default.Snapshot()
	if s.Active || s.Phase != status.PhaseReady || s.Trigger != status.TriggerPoll || s.StartedAt == nil || s.FinishedAt == nil {
		t.Errorf("snapshot = %+v, want finished ready poll run", s)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := RunAs(cancelled, db.DB, status.TriggerRefresh); err == nil {
		t.Fatal("RunAs with cancelled context succeeded, want error")
	}
	s = status.Default.Snapshot()
	if s.Phase != status.PhaseFailed || s.LastError == "" || s.Trigger != status.TriggerRefresh {
		t.Errorf("snapshot after failure = %+v, want failed refresh run with error", s)
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db.DB
}

// writeFiles creates empty video files: the scanner records a file before
// it probes it, so junk content is enough to exercise scanning.
func writeFiles(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o644); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
	}
}

func saveFolders(t *testing.T, db *sql.DB, s library.FolderSettings) {
	t.Helper()
	if _, err := library.SaveFolderSettings(context.Background(), db, s); err != nil {
		t.Fatalf("SaveFolderSettings: %v", err)
	}
}

func presentFiles(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM media_files WHERE status != 'missing'`).Scan(&n); err != nil {
		t.Fatalf("count media_files: %v", err)
	}
	return n
}

func TestRescan_FollowsSavedFoldersAndDropsRemovedOnes(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	tv := t.TempDir()
	writeFiles(t, tv, "a.mkv", "b.mkv")
	saveFolders(t, db, library.FolderSettings{TV: []string{tv}, Movie: []string{}, Commercial: []string{}})

	res, err := Rescan(ctx, db)
	if err != nil {
		t.Fatalf("Rescan: %v", err)
	}
	if !res.Scanned || res.FilesAdded != 2 || presentFiles(t, db) != 2 {
		t.Fatalf("after first rescan: %+v present=%d, want 2 files added", res, presentFiles(t, db))
	}

	// Removing every folder must still scan, so the files go missing.
	saveFolders(t, db, library.FolderSettings{TV: []string{}, Movie: []string{}, Commercial: []string{}})
	if _, err := Rescan(ctx, db); err != nil {
		t.Fatalf("Rescan after removing folders: %v", err)
	}
	if got := presentFiles(t, db); got != 0 {
		t.Errorf("present files after removing the folder = %d, want 0", got)
	}
}

func TestRescan_AppliesExclusionPatterns(t *testing.T) {
	db := openTestDB(t)
	tv := t.TempDir()
	writeFiles(t, tv, "keep.mkv", "trailer.sample.mkv")
	skip := filepath.Join(tv, "extras")
	if err := os.Mkdir(skip, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, skip, "bonus.mkv")
	saveFolders(t, db, library.FolderSettings{TV: []string{tv}, Movie: []string{}, Commercial: []string{}}) // default exclusions

	if _, err := Rescan(context.Background(), db); err != nil {
		t.Fatalf("Rescan: %v", err)
	}
	if got := presentFiles(t, db); got != 1 {
		t.Errorf("files recorded with default exclusions = %d, want 1 (keep.mkv only)", got)
	}

	// A custom pattern list replaces the defaults entirely.
	saveFolders(t, db, library.FolderSettings{TV: []string{tv}, Movie: []string{}, Commercial: []string{}, Exclusions: []string{}})
	if _, err := Rescan(context.Background(), db); err != nil {
		t.Fatalf("Rescan: %v", err)
	}
	if got := presentFiles(t, db); got != 3 {
		t.Errorf("files recorded with no exclusions = %d, want 3", got)
	}
}

func TestPollOnce_DoesNothingWithoutFoldersAndSkipsPipelineWhenNothingNew(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := channel.SeedDefaults(ctx, db); err != nil { // so a pipeline run regenerates schedules
		t.Fatalf("SeedDefaults: %v", err)
	}
	saveFolders(t, db, library.FolderSettings{TV: []string{}, Movie: []string{}, Commercial: []string{}})
	if res, err := PollOnce(ctx, db); err != nil || res.Scanned {
		t.Fatalf("PollOnce with no folders = %+v, %v; want a silent no-op", res, err)
	}

	tv := t.TempDir()
	writeFiles(t, tv, "a.mkv")
	saveFolders(t, db, library.FolderSettings{TV: []string{tv}, Movie: []string{}, Commercial: []string{}})
	first, err := PollOnce(ctx, db)
	if err != nil || !first.Scanned || first.FilesAdded != 1 || first.SchedulesRegenerated == 0 {
		t.Fatalf("first PollOnce = %+v, %v; want the new file scanned and the pipeline run", first, err)
	}
	second, err := PollOnce(ctx, db)
	if err != nil || !second.Scanned || second.FilesAdded != 0 || second.SchedulesRegenerated != 0 {
		t.Errorf("second PollOnce = %+v, %v; want a scan with nothing new and no pipeline run", second, err)
	}
}

func TestPollOnce_ReturnsErrBusyWhileLocked(t *testing.T) {
	db := openTestDB(t)
	var err error
	Exclusive(func() { _, err = PollOnce(context.Background(), db) })
	if !errors.Is(err, ErrBusy) {
		t.Errorf("PollOnce during Exclusive = %v, want ErrBusy", err)
	}
}

func TestRescan_WaitsForARunInProgress(t *testing.T) {
	db := openTestDB(t)
	saveFolders(t, db, library.FolderSettings{TV: []string{}, Movie: []string{}, Commercial: []string{}})

	held, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go Exclusive(func() { close(held); <-release })
	<-held
	go func() {
		_, err := Rescan(context.Background(), db)
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("Rescan returned (%v) while another run held the lock", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Rescan after release = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Rescan never ran after the lock was released")
	}
}

func TestNotifyFoldersChanged_CoalescesAndNeverBlocks(t *testing.T) {
	select { // drain anything left by other tests
	case <-FoldersChanged():
	default:
	}
	NotifyFoldersChanged()
	NotifyFoldersChanged()
	NotifyFoldersChanged()
	select {
	case <-FoldersChanged():
	default:
		t.Fatal("no signal delivered")
	}
	select {
	case <-FoldersChanged():
		t.Error("notifications were not coalesced into one")
	default:
	}
}
