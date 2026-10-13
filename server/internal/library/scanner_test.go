package library

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/activity"
	"github.com/alreadyonapp/alreadyon/server/internal/config"
	"github.com/alreadyonapp/alreadyon/server/internal/governor"
	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

func TestScanWaitsForAnalysisBudgetWithoutMarkingFileCorrupt(t *testing.T) {
	g := governor.Fixed(1)
	claim, err := g.Acquire(t.Context(), governor.PriorityHigh)
	if err != nil {
		t.Fatal(err)
	}
	defer claim.Release()
	SetBackgroundGovernor(g)
	t.Cleanup(func() { SetBackgroundGovernor(nil) })
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "waiting.mkv"), []byte("synthetic placeholder"), 0600); err != nil {
		t.Fatal(err)
	}
	db := openTestDB(t)
	scanner := &Scanner{DB: db, Roots: []config.ValidatedRoot{mediaRoot(t, root)}}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, _ = scanner.Scan(ctx)
	var corrupt int
	if err := db.QueryRow("SELECT COUNT(*) FROM media_files WHERE status='corrupt'").Scan(&corrupt); err != nil {
		t.Fatal(err)
	}
	if corrupt != 0 {
		t.Fatal("waiting for the governor marked an unprobed file corrupt")
	}
}

// writeSyntheticVideo generates a short, silent, synthetically-patterned
// video+audio fixture at the given path via ffmpeg's lavfi sources — no
// real (or copyrighted) media ever touches this test suite.
func writeSyntheticVideo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for fixture: %v", err)
	}
	cmd := exec.Command("ffmpeg",
		"-y",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=64x64:rate=5",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-c:v", "mpeg4",
		"-c:a", "mp2",
		"-shortest",
		path,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate synthetic fixture %s: %v\n%s", path, err, out)
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

func mediaRoot(t *testing.T, dir string) config.ValidatedRoot {
	t.Helper()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", dir, err)
	}
	return config.ValidatedRoot{Kind: config.RootKindMedia, ConfiguredPath: dir, RealPath: real}
}

func countMediaFiles(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM media_files`).Scan(&n); err != nil {
		t.Fatalf("count media_files: %v", err)
	}
	return n
}

func TestScan_AddsNewVideoFiles(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	writeSyntheticVideo(t, filepath.Join(root, "movie-one.mkv"))
	writeSyntheticVideo(t, filepath.Join(root, "sub", "movie-two.mp4"))

	db := openTestDB(t)
	scanner := &Scanner{DB: db, Roots: []config.ValidatedRoot{mediaRoot(t, root)}}

	result, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.FilesAdded != 2 {
		t.Errorf("FilesAdded = %d, want 2 (errors: %v)", result.FilesAdded, result.Errors)
	}
	if got := countMediaFiles(t, db); got != 2 {
		t.Errorf("media_files rows = %d, want 2", got)
	}

	var duration sql.NullFloat64
	if err := db.QueryRow(`SELECT duration_seconds FROM media_files WHERE path = ?`, filepath.Join(root, "movie-one.mkv")).Scan(&duration); err != nil {
		t.Fatalf("query probed duration: %v", err)
	}
	if !duration.Valid || duration.Float64 <= 0 {
		t.Errorf("expected movie-one.mkv to have been probed with a positive duration, got %v", duration)
	}
}

func TestScan_RescanUnchangedLibrary_MakesNoChanges(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	writeSyntheticVideo(t, filepath.Join(root, "movie.mkv"))

	db := openTestDB(t)
	scanner := &Scanner{DB: db, Roots: []config.ValidatedRoot{mediaRoot(t, root)}}

	first, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("first Scan: %v", err)
	}
	if first.FilesAdded != 1 {
		t.Fatalf("first scan FilesAdded = %d, want 1 (errors: %v)", first.FilesAdded, first.Errors)
	}

	second, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("second Scan: %v", err)
	}
	if second.FilesAdded != 0 || second.FilesUpdated != 0 {
		t.Errorf("rescan of an unchanged library should add/update nothing, got added=%d updated=%d unchanged=%d",
			second.FilesAdded, second.FilesUpdated, second.FilesUnchanged)
	}
	if second.FilesUnchanged != 1 {
		t.Errorf("FilesUnchanged = %d, want 1", second.FilesUnchanged)
	}
	if got := countMediaFiles(t, db); got != 1 {
		t.Errorf("rescan should not duplicate rows, got %d", got)
	}
}

func TestScan_DetectsModifiedFile_ReProbes(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	path := filepath.Join(root, "movie.mkv")
	writeSyntheticVideo(t, path)

	db := openTestDB(t)
	scanner := &Scanner{DB: db, Roots: []config.ValidatedRoot{mediaRoot(t, root)}}

	if _, err := scanner.Scan(context.Background()); err != nil {
		t.Fatalf("first Scan: %v", err)
	}

	// Regenerate with a longer duration and force the mtime forward, since
	// some filesystems have coarse mtime resolution.
	cmd := exec.Command("ffmpeg", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=64x64:rate=5",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:v", "mpeg4", "-c:a", "mp2", "-shortest", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("regenerate fixture: %v\n%s", err, out)
	}
	future := time.Now().Add(1 * time.Hour)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	result, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("second Scan: %v", err)
	}
	if result.FilesUpdated != 1 {
		t.Errorf("FilesUpdated = %d, want 1 (errors: %v)", result.FilesUpdated, result.Errors)
	}
	if got := countMediaFiles(t, db); got != 1 {
		t.Errorf("modifying a file should update its row, not add a new one; got %d rows", got)
	}

	var duration float64
	if err := db.QueryRow(`SELECT duration_seconds FROM media_files WHERE path = ?`, path).Scan(&duration); err != nil {
		t.Fatalf("query probed duration: %v", err)
	}
	if duration < 1.5 {
		t.Errorf("expected the re-probed duration to reflect the longer fixture (~2s), got %v", duration)
	}
}

func TestScan_SkipsNonVideoAndHiddenFiles(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"poster.jpg", "info.nfo", "subtitles.srt", ".hidden.mkv"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}

	db := openTestDB(t)
	scanner := &Scanner{DB: db, Roots: []config.ValidatedRoot{mediaRoot(t, root)}}

	result, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.FilesAdded != 0 {
		t.Errorf("FilesAdded = %d, want 0", result.FilesAdded)
	}
	if got := countMediaFiles(t, db); got != 0 {
		t.Errorf("media_files rows = %d, want 0", got)
	}
}

func TestScan_ExcludePatternPrunesDirectory(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	writeSyntheticVideo(t, filepath.Join(root, "keep.mkv"))
	writeSyntheticVideo(t, filepath.Join(root, "@eaDir", "junk.mkv"))

	db := openTestDB(t)
	scanner := &Scanner{
		DB:      db,
		Roots:   []config.ValidatedRoot{mediaRoot(t, root)},
		Exclude: []string{"@eaDir"},
	}

	result, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.FilesAdded != 1 {
		t.Errorf("FilesAdded = %d, want 1 (errors: %v)", result.FilesAdded, result.Errors)
	}
}

func TestScan_CorruptFile_RecordsErrorButContinuesScanning(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	writeSyntheticVideo(t, filepath.Join(root, "good.mkv"))
	if err := os.WriteFile(filepath.Join(root, "corrupt.mkv"), []byte("not a real video"), 0o644); err != nil {
		t.Fatalf("write corrupt fixture: %v", err)
	}

	db := openTestDB(t)
	scanner := &Scanner{DB: db, Roots: []config.ValidatedRoot{mediaRoot(t, root)}}

	result, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan should not fail outright for one bad file: %v", err)
	}
	if result.FilesAdded != 2 {
		t.Errorf("FilesAdded = %d, want 2 (both files get a media_files row even if probing one fails)", result.FilesAdded)
	}
	if len(result.Errors) != 1 {
		t.Errorf("expected exactly 1 collected error for the corrupt file, got %v", result.Errors)
	}
}

func TestScan_SymlinkEscapingRoot_IsRejected(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	outside := t.TempDir()
	writeSyntheticVideo(t, filepath.Join(outside, "secret.mkv"))

	if err := os.Symlink(filepath.Join(outside, "secret.mkv"), filepath.Join(root, "escape.mkv")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	db := openTestDB(t)
	scanner := &Scanner{DB: db, Roots: []config.ValidatedRoot{mediaRoot(t, root)}}

	result, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.FilesAdded != 0 {
		t.Errorf("FilesAdded = %d, want 0 — a symlink escaping the root must not be recorded", result.FilesAdded)
	}
	if got := countMediaFiles(t, db); got != 0 {
		t.Errorf("media_files rows = %d, want 0", got)
	}
	if len(result.Errors) != 1 {
		t.Errorf("expected exactly 1 collected error for the escaping symlink, got %v", result.Errors)
	}
}

func TestScan_IdentifiesEpisodesDuringScan(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	path := filepath.Join(root, "Bramble & Fox", "Season 01", "Bramble & Fox - S01E01 - Pilot.mkv")
	writeSyntheticVideo(t, path)

	db := openTestDB(t)
	scanner := &Scanner{DB: db, Roots: []config.ValidatedRoot{mediaRoot(t, root)}}

	if _, err := scanner.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	var mediaType, title, seriesTitle string
	var season, episode int
	err := db.QueryRow(`
		SELECT i.media_type, i.title, i.series_title, i.season_number, i.episode_number
		FROM identified_items i JOIN media_files m ON m.id = i.media_file_id
		WHERE m.path = ?`, path).
		Scan(&mediaType, &title, &seriesTitle, &season, &episode)
	if err != nil {
		t.Fatalf("query identified_items: %v", err)
	}
	if mediaType != "episode" || title != "Pilot" || seriesTitle != "Bramble & Fox" || season != 1 || episode != 1 {
		t.Errorf("got media_type=%q title=%q series_title=%q season=%d episode=%d",
			mediaType, title, seriesTitle, season, episode)
	}
}

func TestScan_UnidentifiableFile_StillRecordedInMediaFiles(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	path := filepath.Join(root, "video001.mkv")
	writeSyntheticVideo(t, path)

	db := openTestDB(t)
	scanner := &Scanner{DB: db, Roots: []config.ValidatedRoot{mediaRoot(t, root)}}

	if _, err := scanner.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if got := countMediaFiles(t, db); got != 1 {
		t.Fatalf("media_files rows = %d, want 1", got)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM identified_items`).Scan(&count); err != nil {
		t.Fatalf("count identified_items: %v", err)
	}
	if count != 0 {
		t.Errorf("expected no identified_items row for an unidentifiable filename, got %d", count)
	}
}

func TestScan_PreservesManualCorrectionAcrossRescan(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	path := filepath.Join(root, "Bramble & Fox", "Season 01", "Bramble & Fox - S01E01 - Pilot.mkv")
	writeSyntheticVideo(t, path)

	db := openTestDB(t)
	scanner := &Scanner{DB: db, Roots: []config.ValidatedRoot{mediaRoot(t, root)}}

	if _, err := scanner.Scan(context.Background()); err != nil {
		t.Fatalf("first Scan: %v", err)
	}

	var mediaFileID int64
	if err := db.QueryRow(`SELECT id FROM media_files WHERE path = ?`, path).Scan(&mediaFileID); err != nil {
		t.Fatalf("query media_files id: %v", err)
	}

	correction := Identification{IsEpisode: true, Show: "Corrected Show Name", Season: 1, Episode: 1, Title: "Corrected Title"}
	if err := SetCorrection(context.Background(), db, mediaFileID, correction); err != nil {
		t.Fatalf("SetCorrection: %v", err)
	}

	// Force the scanner to actually attempt re-identification (an
	// unchanged rescan wouldn't even try, per #15's change detection) by
	// modifying the file, then confirm the correction still isn't
	// overwritten by the fresh automatic guess.
	cmd := exec.Command("ffmpeg", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=64x64:rate=5",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:v", "mpeg4", "-c:a", "mp2", "-shortest", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("regenerate fixture: %v\n%s", err, out)
	}
	future := time.Now().Add(1 * time.Hour)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	result, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("second Scan: %v", err)
	}
	if result.FilesUpdated != 1 {
		t.Fatalf("expected the modified file to be detected as updated, got %+v", result)
	}

	var title, seriesTitle string
	err = db.QueryRow(`SELECT title, series_title FROM identified_items WHERE media_file_id = ?`, mediaFileID).
		Scan(&title, &seriesTitle)
	if err != nil {
		t.Fatalf("query identified_items: %v", err)
	}
	if title != "Corrected Title" || seriesTitle != "Corrected Show Name" {
		t.Errorf("expected the manual correction to survive a rescan of the modified file, got title=%q series_title=%q",
			title, seriesTitle)
	}
}

func mediaFileStatus(t *testing.T, db *sql.DB, path string) string {
	t.Helper()
	var status string
	if err := db.QueryRow(`SELECT status FROM media_files WHERE path = ?`, path).Scan(&status); err != nil {
		t.Fatalf("query status for %s: %v", path, err)
	}
	return status
}

func TestScan_DeletedFile_MarkedMissing(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	path := filepath.Join(root, "movie.mkv")
	writeSyntheticVideo(t, path)

	db := openTestDB(t)
	scanner := &Scanner{DB: db, Roots: []config.ValidatedRoot{mediaRoot(t, root)}}

	if _, err := scanner.Scan(context.Background()); err != nil {
		t.Fatalf("first Scan: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove fixture: %v", err)
	}

	result, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("second Scan: %v", err)
	}
	if result.FilesMissing != 1 {
		t.Errorf("FilesMissing = %d, want 1", result.FilesMissing)
	}
	if got := mediaFileStatus(t, db, path); got != "missing" {
		t.Errorf("status = %q, want %q", got, "missing")
	}
	if got := countMediaFiles(t, db); got != 1 {
		t.Errorf("expected the row to still exist (not deleted), got %d rows", got)
	}
}

func TestScan_RestoredFile_RecoversFromMissing(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	path := filepath.Join(root, "movie.mkv")
	writeSyntheticVideo(t, path)

	db := openTestDB(t)
	scanner := &Scanner{DB: db, Roots: []config.ValidatedRoot{mediaRoot(t, root)}}

	if _, err := scanner.Scan(context.Background()); err != nil {
		t.Fatalf("first Scan: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove fixture: %v", err)
	}
	if _, err := scanner.Scan(context.Background()); err != nil {
		t.Fatalf("second Scan: %v", err)
	}
	if got := mediaFileStatus(t, db, path); got != "missing" {
		t.Fatalf("status after deletion = %q, want %q", got, "missing")
	}

	writeSyntheticVideo(t, path) // restore it
	if _, err := scanner.Scan(context.Background()); err != nil {
		t.Fatalf("third Scan: %v", err)
	}
	if got := mediaFileStatus(t, db, path); got != "ok" {
		t.Errorf("status after restoring = %q, want %q", got, "ok")
	}
}

func TestScan_UnreadableFile_MarkedUnreadableThenRecovers(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root, which bypasses file permission checks")
	}
	requireFFmpeg(t)
	root := t.TempDir()
	path := filepath.Join(root, "movie.mkv")
	writeSyntheticVideo(t, path)

	db := openTestDB(t)
	scanner := &Scanner{DB: db, Roots: []config.ValidatedRoot{mediaRoot(t, root)}}

	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o644) })

	result, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("first Scan: %v", err)
	}
	// chmod 000 doesn't stop os.Stat (POSIX stat only needs directory
	// traversal permission on the parents, not read permission on the
	// file itself) — the file still gets recorded normally, but ffprobe
	// can't open it, and that "Permission denied" is what actually marks
	// it unreadable.
	if result.FilesAdded != 1 {
		t.Errorf("FilesAdded = %d, want 1", result.FilesAdded)
	}
	if len(result.Errors) == 0 {
		t.Error("expected the permission error to be recorded in result.Errors")
	}
	if got := mediaFileStatus(t, db, path); got != "unreadable" {
		t.Fatalf("status = %q, want %q", got, "unreadable")
	}

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod back: %v", err)
	}
	if _, err := scanner.Scan(context.Background()); err != nil {
		t.Fatalf("second Scan: %v", err)
	}
	if got := mediaFileStatus(t, db, path); got != "ok" {
		t.Errorf("status after restoring permissions = %q, want %q", got, "ok")
	}
}

func TestScan_CorruptFile_MarkedCorruptThenRecovers(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	path := filepath.Join(root, "movie.mkv")
	if err := os.WriteFile(path, []byte("not a real video"), 0o644); err != nil {
		t.Fatalf("write corrupt fixture: %v", err)
	}

	db := openTestDB(t)
	scanner := &Scanner{DB: db, Roots: []config.ValidatedRoot{mediaRoot(t, root)}}

	if _, err := scanner.Scan(context.Background()); err != nil {
		t.Fatalf("first Scan: %v", err)
	}
	if got := mediaFileStatus(t, db, path); got != "corrupt" {
		t.Fatalf("status = %q, want %q", got, "corrupt")
	}

	// "Fix" it by replacing with a valid file — must change size/mtime
	// to trigger re-probing at all, per the change-detection in #15.
	writeSyntheticVideo(t, path)
	future := time.Now().Add(1 * time.Hour)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	if _, err := scanner.Scan(context.Background()); err != nil {
		t.Fatalf("second Scan: %v", err)
	}
	if got := mediaFileStatus(t, db, path); got != "ok" {
		t.Errorf("status after replacing with a valid file = %q, want %q", got, "ok")
	}
}

func TestScan_RootKindIsRecordedPerRoot(t *testing.T) {
	requireFFmpeg(t)
	mediaDir := t.TempDir()
	commercialDir := t.TempDir()
	writeSyntheticVideo(t, filepath.Join(mediaDir, "movie.mkv"))
	writeSyntheticVideo(t, filepath.Join(commercialDir, "ad.mkv"))

	db := openTestDB(t)
	commercialRoot := mediaRoot(t, commercialDir)
	commercialRoot.Kind = config.RootKindCommercial
	scanner := &Scanner{DB: db, Roots: []config.ValidatedRoot{mediaRoot(t, mediaDir), commercialRoot}}

	if _, err := scanner.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	var mediaKind, commercialKind string
	if err := db.QueryRow(`SELECT root_kind FROM media_files WHERE path = ?`, filepath.Join(mediaDir, "movie.mkv")).Scan(&mediaKind); err != nil {
		t.Fatalf("query media root_kind: %v", err)
	}
	if err := db.QueryRow(`SELECT root_kind FROM media_files WHERE path = ?`, filepath.Join(commercialDir, "ad.mkv")).Scan(&commercialKind); err != nil {
		t.Fatalf("query commercial root_kind: %v", err)
	}
	if mediaKind != "media" {
		t.Errorf("media file root_kind = %q, want %q", mediaKind, "media")
	}
	if commercialKind != "commercial" {
		t.Errorf("commercial file root_kind = %q, want %q", commercialKind, "commercial")
	}
}

func TestScan_IgnoresShortMediaButNotShortCommercials(t *testing.T) {
	requireFFmpeg(t)
	mediaDir, commercialDir := t.TempDir(), t.TempDir()
	writeSyntheticVideo(t, filepath.Join(mediaDir, "clip.mkv")) // one second: treated as a stray commercial
	writeSyntheticVideo(t, filepath.Join(commercialDir, "ad.mkv"))
	long := filepath.Join(mediaDir, "episode.mkv")
	if out, err := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i", "testsrc=duration=301:size=16x16:rate=1", "-c:v", "mpeg4", long).CombinedOutput(); err != nil {
		t.Fatalf("generate long fixture: %v\n%s", err, out)
	}

	db := openTestDB(t)
	commercialRoot := mediaRoot(t, commercialDir)
	commercialRoot.Kind = config.RootKindCommercial
	scanner := &Scanner{DB: db, Roots: []config.ValidatedRoot{mediaRoot(t, mediaDir), commercialRoot}, MinMediaSeconds: MinMediaRuntimeSeconds}
	result, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.FilesAdded != 2 || result.FilesSkipped != 1 {
		t.Errorf("added=%d skipped=%d, want the episode and the ad added and the clip skipped (errors: %v)", result.FilesAdded, result.FilesSkipped, result.Errors)
	}
	var paths []string
	rows, err := db.Query(`SELECT path FROM media_files ORDER BY path`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		_ = rows.Scan(&p)
		paths = append(paths, filepath.Base(p))
	}
	sort.Strings(paths)
	if len(paths) != 2 || paths[0] != "ad.mkv" || paths[1] != "episode.mkv" {
		t.Errorf("recorded %v, want ad.mkv and episode.mkv only", paths)
	}
	// A second scan keeps ignoring it, without recording it.
	if again, err := scanner.Scan(context.Background()); err != nil || countMediaFiles(t, db) != 2 || again.FilesSkipped != 1 {
		t.Errorf("rescan: %+v %v", again, err)
	}
}

func TestScanProgressCountsCandidatesAndCleansUp(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"one.mp4", "two.mkv", ".hidden.mp4", "poster.jpg", "excluded.avi"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("invalid synthetic media"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	scanner := &Scanner{DB: openTestDB(t), Roots: []config.ValidatedRoot{mediaRoot(t, root)}, Exclude: []string{"excluded.*"}}
	result, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.candidates != 2 || result.inspected != 2 {
		t.Fatalf("candidates=%d inspected=%d", result.candidates, result.inspected)
	}
	for _, row := range activity.Default.Snapshot(time.Now()) {
		if row.ID == result.activityTask {
			t.Fatal("completed scan retained")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err = scanner.Scan(ctx)
	if err != context.Canceled {
		t.Fatalf("cancelled scan: %v", err)
	}
	for _, row := range activity.Default.Snapshot(time.Now()) {
		if row.ID == result.activityTask {
			t.Fatal("cancelled scan retained")
		}
	}
}
