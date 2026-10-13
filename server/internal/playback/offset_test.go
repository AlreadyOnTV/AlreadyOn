package playback

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
)

// Each source frame's luminance encodes its ordinal (four luma units per
// frame at 10 fps). Decoding the result verifies the actual content seek,
// independently of FFmpeg's rebased output timestamps.
func TestScheduledOffsetRealFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "clock.mkv")
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "nullsrc=s=64x64:r=10:d=6,geq=lum='N*4':cb=128:cr=128", "-c:v", "ffv1", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	source, err := library.Probe(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Choose(source, clientCaps(), false, false)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	e := schedule.Entry{StartsAt: now.Add(-time.Second), EndsAt: now.Add(time.Second), SourceOffsetSeconds: 1.5}
	j, err := ScheduledJob(e, schedule.FixedClock{Time: now}, path, source, plan, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := (Runner{}).Run(ctx, j); err != nil {
		t.Fatal(err)
	}
	cmd = exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", filepath.Join(j.OutputDir, "segment-000000000.ts"), "-frames:v", "1", "-pix_fmt", "yuv420p", "-f", "rawvideo", "pipe:1")
	raw, err := cmd.Output()
	if err != nil || len(raw) < 4096 {
		t.Fatalf("decode: %v", err)
	}
	sum := 0
	for _, v := range raw[:4096] {
		sum += int(v)
	}
	luma := float64(sum) / 4096
	// Frame 25 (2.5s) has luma=100. Six units permit ≤0.15s encoding error.
	if luma < 94 || luma > 106 {
		t.Fatalf("seek content luma=%f, want 100 ±6 (0.15s)", luma)
	}
}
