package loudness

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/governor"
	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

func clip(t *testing.T) string {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe", "nice", "ionice"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " unavailable")
		}
	}
	path := filepath.Join(t.TempDir(), "quiet.wav")
	cmd := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=3", "-af", "volume=0.1", "-c:a", "pcm_s16le", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	return path
}
func TestFullTrackAnalysisAndNormalizedOutput(t *testing.T) {
	path := clip(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	m, state, err := Analyze(ctx, path, 0)
	if err != nil || state != "ready" || m == nil {
		t.Fatalf("%+v %s %v", m, state, err)
	}
	out := filepath.Join(t.TempDir(), "normalized-test-only.wav")
	if raw, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", path, "-af", m.Filter(), "-ar", "48000", "-c:a", "pcm_s16le", out).CombinedOutput(); err != nil {
		t.Fatalf("%s %v", raw, err)
	}
	normalized, state, err := Analyze(ctx, out, 0)
	if err != nil || state != "ready" || math.Abs(normalized.Integrated-Target) > 0.5 || normalized.TruePeak > Peak+0.2 {
		t.Fatalf("normalized %+v %s %v", normalized, state, err)
	}
}
func TestMeasurementRejectsSilenceAndInvalidValues(t *testing.T) {
	if m, s, err := parse(`{"input_i":"-inf","input_tp":"-inf"}`); err != nil || s != "silent" || m != nil {
		t.Fatal(m, s, err)
	}
	for _, raw := range []string{"no JSON", `{"input_i":"NaN"}`, `{"input_i":"-inf","input_tp":"-20"}`} {
		if _, _, err := parse(raw); err == nil {
			t.Fatal("accepted invalid measurements")
		}
	}
}
func TestWorkerPersistenceInvalidationAndDisabledDefault(t *testing.T) {
	path := clip(t)
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root, _ := json.Marshal([]string{filepath.Dir(path)})
	if _, err = db.Exec(`UPDATE folder_settings SET tv_paths=?,movie_paths='[]',commercial_paths='[]' WHERE id=1`, string(root)); err != nil {
		t.Fatal(err)
	}
	stat, _ := os.Stat(path)
	if _, err = db.Exec(`INSERT INTO media_files(id,root_kind,path,size_bytes,modified_unix,first_seen_unix,last_seen_unix,status) VALUES(1,'commercial',?,?,?,0,0,'ok')`, path, stat.Size(), stat.ModTime().Unix()); err != nil {
		t.Fatal(err)
	}
	s, err := Get(t.Context(), db.DB)
	if err != nil || s.Enabled || s.Pending != 1 {
		t.Fatal(s, err)
	}
	if err = Request(t.Context(), db.DB); err == nil {
		t.Fatal("Run Now while disabled")
	}
	if err = SetEnabled(t.Context(), db.DB, true); err != nil {
		t.Fatal(err)
	}
	c, err := next(t.Context(), db.DB, 1)
	if err != nil {
		t.Fatal(err)
	}
	process(t.Context(), db.DB, c, 1)
	s, err = Get(t.Context(), db.DB)
	if err != nil || s.Ready != 1 || s.Pending != 0 {
		t.Fatal(s, err)
	}
	m, err := Lookup(t.Context(), db.DB, 1, path, 0)
	if err != nil || m == nil {
		t.Fatal(m, err)
	}
	if _, err = db.Exec(`UPDATE loudness_measurements SET version=? WHERE media_file_id=1`, fullFileVersion); err != nil {
		t.Fatal(err)
	}
	s, err = Get(t.Context(), db.DB)
	if err != nil || s.Ready != 1 || s.Pending != 0 {
		t.Fatal("full-file cache was invalidated", s, err)
	}
	if cached, err := Lookup(t.Context(), db.DB, 1, path, 0); err != nil || cached == nil {
		t.Fatal("full-file cache not reused", cached, err)
	}
	if _, err = next(t.Context(), db.DB, 2); err == nil {
		t.Fatal("valid measurements should not be reanalyzed")
	}
	if other, _ := Lookup(t.Context(), db.DB, 1, path, 1); other != nil {
		t.Fatal("wrong track accepted")
	}
	if err = SetEnabled(t.Context(), db.DB, false); err != nil {
		t.Fatal(err)
	}
	if other, _ := Lookup(t.Context(), db.DB, 1, path, 0); other != nil {
		t.Fatal("disabled normalization")
	}
	_ = SetEnabled(t.Context(), db.DB, true)
	if err = os.Chtimes(path, time.Now(), stat.ModTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if other, _ := Lookup(t.Context(), db.DB, 1, path, 0); other != nil {
		t.Fatal("stale measurement accepted")
	}
	if _, err = db.Exec(`UPDATE media_files SET modified_unix=modified_unix+1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = next(t.Context(), db.DB, 2); err != nil {
		t.Fatal("changed file was not queued", err)
	}
}

func TestFirstTenthAnalysisAndFullFileFallback(t *testing.T) {
	for _, tc := range []struct {
		name       string
		duration   float64
		sampleJSON string
		calls      int
		bounded    bool
	}{
		{"sample", 123.5, `{"input_i":"-24","input_tp":"-4","input_lra":"3","input_thresh":"-34","target_offset":"0.5"}`, 1, true},
		{"silent-intro", 120, `{"input_i":"-inf","input_tp":"-inf"}`, 2, true},
		{"invalid-sample", 120, `{}`, 2, true},
		{"short", 20, `{}`, 1, false},
		{"unknown-duration", 0, `{}`, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tools := t.TempDir()
			calls := filepath.Join(tools, "calls")
			script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + calls + "'\ncase \" $* \" in\n*' -t '*) printf '%s\\n' '" + tc.sampleJSON + "' >&2 ;;\n*) printf '%s\\n' '{\"input_i\":\"-24\",\"input_tp\":\"-4\",\"input_lra\":\"3\",\"input_thresh\":\"-34\",\"target_offset\":\"0.5\"}' >&2 ;;\nesac\n"
			if err := os.WriteFile(filepath.Join(tools, "ffmpeg"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
			m, state, err := AnalyzeSample(t.Context(), "source.mkv", 2, tc.duration)
			if err != nil || state != "ready" || m == nil {
				t.Fatal(m, state, err)
			}
			raw, err := os.ReadFile(calls)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
			if len(lines) != tc.calls || strings.Contains(lines[0], " -t ") != tc.bounded {
				t.Fatalf("unexpected analysis commands: %s", raw)
			}
			if tc.bounded {
				limit := " -t " + strconv.FormatFloat(tc.duration/10, 'f', -1, 64) + " -i "
				if !strings.Contains(lines[0], limit) || !strings.Contains(lines[0], " -map 0:2 -vn -sn -dn ") {
					t.Fatalf("sample was not audio-only with an input cutoff: %s", raw)
				}
			}
			if tc.calls == 2 && strings.Contains(lines[1], " -t ") {
				t.Fatalf("fallback remained bounded: %s", raw)
			}
		})
	}
}

func TestDisablingCancelsActiveAnalysis(t *testing.T) {
	path := clip(t)
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root, _ := json.Marshal([]string{filepath.Dir(path)})
	_, _ = db.Exec(`UPDATE folder_settings SET tv_paths=?,movie_paths='[]',commercial_paths='[]' WHERE id=1`, string(root))
	info, _ := os.Stat(path)
	_, err = db.Exec(`INSERT INTO media_files(id,root_kind,path,size_bytes,modified_unix,first_seen_unix,last_seen_unix,status) VALUES(1,'media',?,?,?,0,0,'ok')`, path, info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatal(err)
	}
	tools := t.TempDir()
	started := filepath.Join(tools, "started")
	if err = os.WriteFile(filepath.Join(tools, "ffmpeg"), []byte("#!/bin/sh\ntouch '"+started+"'\nsleep 60\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	_ = SetEnabled(t.Context(), db.DB, true)
	c, _ := next(t.Context(), db.DB, 1)
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { process(ctx, db.DB, c, 1); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err = os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("analysis did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err = SetEnabled(t.Context(), db.DB, false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("disabled analysis did not cancel")
	}
	s, err := Get(t.Context(), db.DB)
	if err != nil || s.Running || s.Failed != 0 || s.Pending != 1 {
		t.Fatal(s, err)
	}
}

// measureQueue measures eight files through p and returns the most analyses seen at once.
func measureQueue(t *testing.T, p pool) int {
	path := clip(t)
	dir := filepath.Dir(path)
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root, _ := json.Marshal([]string{dir})
	_, _ = db.Exec(`UPDATE folder_settings SET tv_paths=?,movie_paths='[]',commercial_paths='[]' WHERE id=1`, string(root))
	const files = 8
	data, _ := os.ReadFile(path)
	for i := 1; i <= files; i++ {
		copyPath := filepath.Join(dir, "copy"+strconv.Itoa(i)+".wav")
		if err = os.WriteFile(copyPath, data, 0600); err != nil {
			t.Fatal(err)
		}
		info, _ := os.Stat(copyPath)
		if _, err = db.Exec(`INSERT INTO media_files(id,root_kind,path,size_bytes,modified_unix,first_seen_unix,last_seen_unix,status) VALUES(?,'media',?,?,?,0,0,'ok')`, i, copyPath, info.Size(), info.ModTime().Unix()); err != nil {
			t.Fatal(err)
		}
	}
	tools, log := t.TempDir(), t.TempDir()
	script := "#!/bin/sh\n" +
		"d='" + log + "'\n" +
		"echo \"$*\" >> \"$d/started\"\n" +
		"touch \"$d/run.$$\"\n" +
		"ls \"$d\" | grep -c '^run\\.' >> \"$d/counts\"\n" +
		"sleep 0.3\n" +
		"rm \"$d/run.$$\"\n" +
		"echo '{\"input_i\":\"-20.0\",\"input_tp\":\"-3.0\",\"input_lra\":\"5.0\",\"input_thresh\":\"-30.0\",\"target_offset\":\"0.0\"}' >&2\n"
	if err = os.WriteFile(filepath.Join(tools, "ffmpeg"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	_ = SetEnabled(t.Context(), db.DB, true)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { run(ctx, db.DB, p); close(done) }()
	deadline := time.Now().Add(30 * time.Second)
	for {
		s, err := Get(t.Context(), db.DB)
		if err == nil && s.Ready == files {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not all files were measured", s, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	for deadline = time.Now().Add(3 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if s, _ := Get(t.Context(), db.DB); !s.Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("status still running after the queue emptied")
		}
	}
	cancel()
	<-done
	started, _ := os.ReadFile(filepath.Join(log, "started"))
	lines := strings.Split(strings.TrimSpace(string(started)), "\n")
	seen := map[string]bool{}
	for _, l := range lines {
		if seen[l] {
			t.Fatalf("file analyzed twice: %s", l)
		}
		seen[l] = true
	}
	if len(lines) != files {
		t.Fatalf("analyses = %d, want %d", len(lines), files)
	}
	counts, _ := os.ReadFile(filepath.Join(log, "counts"))
	peak := 0
	for _, f := range strings.Fields(string(counts)) {
		if n, _ := strconv.Atoi(f); n > peak {
			peak = n
		}
	}
	return peak
}

func TestWorkersShareTheQueueWithoutRepeats(t *testing.T) {
	if peak := measureQueue(t, governor.Fixed(4)); peak < 2 || peak > 4 {
		t.Fatalf("peak concurrency = %d, want 2..4", peak)
	}
}

// A governor that has not been run stays at one job, as a loaded host would.
func TestJobsBeyondTheTargetWait(t *testing.T) {
	if peak := measureQueue(t, governor.New(4)); peak != 1 {
		t.Fatalf("peak concurrency = %d, want 1 with a target of one", peak)
	}
}
