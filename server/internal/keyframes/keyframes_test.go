package keyframes

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/governor"
	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

func TestMaxGapReadsKeyframeRows(t *testing.T) {
	// pts_time,dts_time,flags: B-frame order means pts and dts differ.
	out := "0.000000,-0.083333,K_\n0.083333,-0.041667,__\n2.000000,1.916667,K_\n4.000000,3.916667,K_\n10.000000,9.916667,K_\n10.500000,10.416667,__\n"
	gap, err := maxGap(out)
	if err != nil || gap != 6*time.Second {
		t.Fatalf("gap = %v, %v; want 6s", gap, err)
	}
	if gap, err = maxGap("N/A,12.0,K_\n14.0,13.9,K_\n"); err != nil || gap != 2*time.Second {
		t.Fatalf("a missing pts should fall back to dts: %v %v", gap, err)
	}
	if gap, err = maxGap("0.0,0.0,K_\n0.04,0.04,__\n"); err != nil || gap != Window {
		t.Fatalf("one keyframe in the window means at least a full window, got %v %v", gap, err)
	}
	for _, bad := range []string{"", "\n", "garbage"} {
		if _, err = maxGap(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func encode(t *testing.T, gop int, seconds int) string {
	t.Helper()
	return encodeIn(t, t.TempDir(), gop, seconds)
}

func encodeIn(t *testing.T, dir string, gop int, seconds int) string {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe", "nice", "ionice"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " unavailable")
		}
	}
	path := filepath.Join(dir, "gop"+strconv.Itoa(gop)+".mkv")
	// A static picture never triggers scene-change keyframes, so only -g decides.
	out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=64x48:r=24:d="+strconv.Itoa(seconds), "-c:v", "mpeg4", "-g", strconv.Itoa(gop), "-bf", "0", path).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	return path
}

func TestMeasureFindsTheRealKeyframeSpacing(t *testing.T) {
	short, long := encode(t, 48, 40), encode(t, 240, 40) // 2 s and 10 s at 24 fps
	for _, background := range []bool{false, true} {
		if gap, err := Measure(t.Context(), short, 40, background, false); err != nil || gap < 1900*time.Millisecond || gap > 2100*time.Millisecond {
			t.Fatalf("short GOP (background=%v) = %v, %v; want about 2s", background, gap, err)
		}
	}
	if gap, err := Measure(t.Context(), long, 40, false, false); err != nil || gap < 9900*time.Millisecond || gap > 10100*time.Millisecond {
		t.Fatalf("long GOP = %v, %v; want about 10s", gap, err)
	}
}

func openDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func addFile(t *testing.T, db *storage.DB, id int64, path string) (size, modified int64) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	probe, _ := json.Marshal(library.ProbeResult{DurationSeconds: 40, Streams: []library.StreamInfo{{Index: 0, Kind: library.StreamVideo, Codec: "mpeg4"}}})
	root, _ := json.Marshal([]string{filepath.Dir(path)})
	_, _ = db.Exec(`UPDATE folder_settings SET tv_paths=?,movie_paths='[]',commercial_paths='[]' WHERE id=1`, string(root))
	if _, err = db.Exec(`INSERT INTO media_files(id,root_kind,path,size_bytes,modified_unix,first_seen_unix,last_seen_unix,status,probe_json) VALUES(?,'media',?,?,?,0,0,'ok',?)`, id, path, info.Size(), info.ModTime().Unix(), string(probe)); err != nil {
		t.Fatal(err)
	}
	return info.Size(), info.ModTime().Unix()
}

func TestLookupFollowsTheFileFingerprint(t *testing.T) {
	path := encode(t, 48, 10)
	db := openDB(t)
	size, modified := addFile(t, db, 1, path)
	if gap, err := Lookup(t.Context(), db.DB, 1); err != nil || gap != 0 {
		t.Fatalf("unmeasured file = %v, %v; want 0", gap, err)
	}
	if err := save(t.Context(), db.DB, 1, size, modified, "ready", 2*time.Second, ""); err != nil {
		t.Fatal(err)
	}
	if gap, _ := Lookup(t.Context(), db.DB, 1); gap != 2*time.Second {
		t.Fatalf("stored gap = %v", gap)
	}
	if err := save(t.Context(), db.DB, 1, size, modified, "failed", 0, "boom"); err != nil {
		t.Fatal(err)
	}
	if gap, _ := Lookup(t.Context(), db.DB, 1); gap != Failed {
		t.Fatalf("failed measurement = %v, want Failed", gap)
	}
	if err := save(t.Context(), db.DB, 1, size, modified, "ready", 2*time.Second, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE media_files SET modified_unix=modified_unix+1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if gap, _ := Lookup(t.Context(), db.DB, 1); gap != 0 {
		t.Fatalf("a changed file must read as unmeasured, got %v", gap)
	}
}

func TestForTuneMeasuresOnceThenGivesUpQuickly(t *testing.T) {
	path := encode(t, 48, 10)
	db := openDB(t)
	addFile(t, db, 1, path)
	if gap := ForTune(t.Context(), db.DB, 1, path, 10); gap < 1900*time.Millisecond || gap > 2100*time.Millisecond {
		t.Fatalf("inline measurement = %v, want about 2s", gap)
	}
	if gap, _ := Lookup(t.Context(), db.DB, 1); gap == 0 {
		t.Fatal("the inline measurement was not stored")
	}

	// A probe that hangs must not hold up the tune: it fails closed and stores nothing.
	other := encode(t, 48, 10)
	addFile(t, db, 2, other)
	tools := t.TempDir()
	if err := os.WriteFile(filepath.Join(tools, "ffprobe"), []byte("#!/bin/sh\nsleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	start := time.Now()
	if gap := ForTune(t.Context(), db.DB, 2, other, 10); gap != Failed {
		t.Fatalf("a slow probe = %v, want Failed", gap)
	}
	if time.Since(start) > 4*time.Second {
		t.Fatalf("the tune waited %v for the probe", time.Since(start))
	}
	if gap, _ := Lookup(t.Context(), db.DB, 2); gap != 0 {
		t.Fatalf("a timed-out measurement was stored: %v", gap)
	}
}

func TestBackgroundScanMeasuresEveryFileOnce(t *testing.T) {
	dir := t.TempDir()
	long, short := encodeIn(t, dir, 240, 40), encodeIn(t, dir, 48, 40)
	db := openDB(t)
	addFile(t, db, 1, long)
	addFile(t, db, 2, short)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { run(ctx, db.DB, governor.Fixed(2)); close(done) }()
	deadline := time.Now().Add(30 * time.Second)
	for {
		a, _ := Lookup(t.Context(), db.DB, 1)
		b, _ := Lookup(t.Context(), db.DB, 2)
		if a > 0 && b > 0 {
			if a < 9900*time.Millisecond || a > 10100*time.Millisecond || b < 1900*time.Millisecond || b > 2100*time.Millisecond {
				t.Fatalf("gaps = %v and %v, want about 10s and 2s", a, b)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("scan did not finish: %v %v", a, b)
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	<-done
	var rows int
	_ = db.QueryRow(`SELECT COUNT(*) FROM keyframe_measurements`).Scan(&rows)
	if rows != 2 {
		t.Fatalf("rows = %d, want 2", rows)
	}
}
