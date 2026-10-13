// Linux-only local synthetic workload measurements; never point at a real library.
//go:build linux

package performance_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/config"
	"github.com/alreadyonapp/alreadyon/server/internal/keyframes"
	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/loudness"
	"github.com/alreadyonapp/alreadyon/server/internal/metrics"
	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

func cpuSeconds() float64 {
	var total float64
	for _, who := range []int{syscall.RUSAGE_SELF, syscall.RUSAGE_CHILDREN} {
		var r syscall.Rusage
		if syscall.Getrusage(who, &r) == nil {
			total += float64(r.Utime.Sec+r.Stime.Sec) + float64(r.Utime.Usec+r.Stime.Usec)/1e6
		}
	}
	return total
}
func fixture(b *testing.B) string {
	b.Helper()
	dir := b.TempDir()
	file := filepath.Join(dir, "synthetic.mkv")
	cmd := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc2=duration=120:size=160x90:rate=24", "-f", "lavfi", "-i", "sine=duration=120:sample_rate=48000", "-c:v", "mpeg4", "-g", "48", "-c:a", "aac", "-shortest", file)
	if raw, err := cmd.CombinedOutput(); err != nil {
		b.Fatalf("fixture: %v: %s", err, raw)
	}
	return file
}
func BenchmarkBackgroundWorkloads(b *testing.B) {
	file := fixture(b)
	raw, err := os.ReadFile(file)
	if err != nil {
		b.Fatal(err)
	}
	for _, kind := range []string{"unchanged-scan-24-files", "new-scan-24-files", "schedule-24-channels", "loudness-120s", "keyframes-120s"} {
		b.Run(kind, func(b *testing.B) {
			db, err := storage.Open(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			defer db.Close()
			ctx := context.Background()
			dir := b.TempDir()
			for i := 0; i < 24; i++ {
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("Synthetic %d.mkv", i)), raw, 0600); err != nil {
					b.Fatal(err)
				}
			}
			scanner := library.Scanner{DB: db.DB, Roots: []config.ValidatedRoot{{Kind: config.RootKindMedia, ConfiguredPath: dir, RealPath: dir}}}
			if _, err := scanner.Scan(ctx); err != nil {
				b.Fatal(err)
			}
			for i := 0; i < 24; i++ {
				if _, err := db.Exec(`INSERT INTO channels(id,number,name) VALUES(?,?,?)`, 10000+i, 10000+i, fmt.Sprintf("synthetic %d", i)); err != nil {
					b.Fatal(err)
				}
				if _, err := db.Exec(`INSERT INTO channel_items(channel_id,media_file_id,position) SELECT ?,id,id FROM media_files`, 10000+i); err != nil {
					b.Fatal(err)
				}
			}
			began := cpuSeconds()
			for b.Loop() {
				switch kind {
				case "new-scan-24-files":
					if _, err := db.Exec(`DELETE FROM channel_items`); err != nil {
						b.Fatal(err)
					}
					if _, err := db.Exec(`DELETE FROM media_files`); err != nil {
						b.Fatal(err)
					}
					fallthrough
				case "unchanged-scan-24-files":
					if result, err := scanner.Scan(ctx); err != nil || len(result.Errors) > 0 {
						b.Fatalf("%+v %v", result, err)
					}
				case "schedule-24-channels":
					for i := 0; i < 24; i++ {
						if err := schedule.Regenerate(ctx, db.DB, int64(10000+i), time.Unix(1800000000, 0)); err != nil {
							b.Fatal(err)
						}
					}
				case "loudness-120s":
					if _, state, err := loudness.Analyze(ctx, file, 1); err != nil || state != "ready" {
						b.Fatalf("%s %v", state, err)
					}
				case "keyframes-120s":
					if _, err := keyframes.Measure(ctx, file, 120, true, false); err != nil {
						b.Fatal(err)
					}
				}
			}
			b.ReportMetric((cpuSeconds()-began)*1000/float64(b.N), "cpu-ms/op")
		})
	}
}

func TestIdleMetricsCollectionCost(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	began := cpuSeconds()
	start := time.Now()
	metrics.Collect(ctx, db.DB, t.TempDir(), nil)
	t.Logf("Idle collector: %.3f CPU seconds over %.3f wall seconds", cpuSeconds()-began, time.Since(start).Seconds())
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM server_metrics`).Scan(&n); err != nil || n < 7 {
		t.Fatalf("samples=%d: %v", n, err)
	}
}
