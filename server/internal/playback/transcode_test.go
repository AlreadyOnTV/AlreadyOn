package playback

import (
	"context"
	"errors"
	"github.com/alreadyonapp/alreadyon/server/internal/loudness"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/library"
)

func fixture(t *testing.T) Job {
	t.Helper()
	for _, exe := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(exe); err != nil {
			t.Skip(exe + " not available")
		}
	}
	// Shell metacharacters are intentionally part of the real path.
	path := filepath.Join(t.TempDir(), "sample $(touch injected); 'clip'.avi")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc2=duration=4:size=320x240:rate=30", "-f", "lavfi", "-i", "sine=duration=4:sample_rate=48000", "-c:v", "mpeg4", "-c:a", "mp2", "-shortest", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	s, err := library.Probe(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Choose(s, clientCaps(), false, false)
	if err != nil {
		t.Fatal(err)
	}
	return Job{Path: path, Source: s, Plan: p, Duration: 3 * time.Second, OutputDir: t.TempDir()}
}

func TestRunnerRealFFmpeg(t *testing.T) {
	j := fixture(t)
	if err := (Runner{}).Run(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(j.OutputDir, "index.m3u8"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), "#EXT-X-ENDLIST") {
		t.Fatalf("unfinished output: %s", manifest)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, err := library.Probe(ctx, filepath.Join(j.OutputDir, "segment-000000000.ts"))
	if err != nil {
		t.Fatal(err)
	}
	v, a := Tracks(p)
	if v.Codec != "h264" || v.Width != 320 || v.Height != 240 || a.Codec != "aac" || a.Channels != 2 || a.SampleRate != 48000 {
		t.Fatalf("unexpected output: %+v", p)
	}
}

func TestRunnerFailureCancellationAndLimits(t *testing.T) {
	base := fixture(t)
	for _, name := range []string{"missing", "corrupt", "cancel", "startup", "disk"} {
		t.Run(name, func(t *testing.T) {
			j := base
			j.OutputDir = t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := Runner{Limits: DefaultLimits()}
			var want error
			switch name {
			case "missing":
				j.Path = filepath.Join(t.TempDir(), "missing.avi")
			case "corrupt":
				j.Path = filepath.Join(t.TempDir(), "corrupt.avi")
				if err := os.WriteFile(j.Path, []byte("garbage"), 0600); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				time.AfterFunc(250*time.Millisecond, cancel)
				want = context.Canceled
			case "startup":
				r.Limits.Startup = time.Millisecond
				want = ErrStartup
			case "disk":
				r.Limits.MaxBytes = 1
				want = ErrDiskLimit
			}
			start := time.Now()
			err := r.Run(ctx, j)
			if err == nil || (want != nil && !errors.Is(err, want)) {
				t.Fatalf("got %v, want %v", err, want)
			}
			if time.Since(start) > 5*time.Second {
				t.Fatal("shutdown was not bounded")
			}
			if _, err := os.Stat(j.OutputDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed stream output retained")
			}
		})
	}
}

func TestArgumentsRejectInvalidJobs(t *testing.T) {
	if _, err := Arguments(Job{}); err == nil {
		t.Fatal("empty job accepted")
	}
	var b tailBuffer
	_, _ = b.Write([]byte(strings.Repeat("x", 100000)))
	if b.Len() != 8192 {
		t.Fatal("unbounded diagnostics")
	}
}

func TestHDQualityDefaultsRealFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "hd.mkv")
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc2=duration=2:size=1920x1080:rate=60", "-f", "lavfi", "-i", "sine=duration=2:sample_rate=48000", "-c:v", "mpeg4", "-q:v", "5", "-c:a", "pcm_s16le", "-shortest", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	source, err := library.Probe(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	caps := clientCaps()
	caps.Ladder = true
	plan, err := Choose(source, caps, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Width != 1920 || plan.Height != 1080 || plan.FPS != 60 || plan.AudioBitrate != 192000 {
		t.Fatalf("quality defaults: %+v", plan)
	}
	job := Job{Path: path, Source: source, Plan: plan, Duration: 2 * time.Second, OutputDir: t.TempDir()}
	if err := (Runner{}).Run(ctx, job); err != nil {
		t.Fatal(err)
	}
	// A 1080p conversion is a two-rung ladder: rung 0 is the small one that
	// starts sooner, rung 1 the main picture. Both carry the same audio.
	if plan.LowHeight != 360 || plan.LowWidth != 640 {
		t.Fatalf("no low rung planned: %+v", plan)
	}
	for _, name := range []string{"index.m3u8", "stream-0.m3u8", "stream-1.m3u8"} {
		if _, err := os.Stat(filepath.Join(job.OutputDir, name)); err != nil {
			t.Fatalf("ladder output lacks %s: %v", name, err)
		}
	}
	for rung, want := range map[string][2]int{"segment-1-000000000.ts": {1920, 1080}, "segment-0-000000000.ts": {640, 360}} {
		result, err := library.Probe(ctx, filepath.Join(job.OutputDir, rung))
		if err != nil {
			t.Fatal(err)
		}
		v, a := Tracks(result)
		if v.Width != want[0] || v.Height != want[1] || v.FrameRate != 60 || v.Codec != "h264" || a.Codec != "aac" || a.Channels != 2 || a.BitRate < 128000 {
			t.Fatalf("%s output: %+v", rung, result)
		}
	}
	if _, err := HLSOrigin(job.OutputDir); err != nil {
		t.Fatalf("stream origin was not captured from the low rung: %v", err)
	}
}

func TestHDRToneMappedConversionRealFFmpeg(t *testing.T) {
	hdrToneMapRealFFmpeg(t, "colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc")
}

// Some HDR files tag only the transfer; FFmpeg 8 rejects those in zscale unless
// the frames are tagged first.
func TestHDRToneMapWithOnlyTheTransferTaggedRealFFmpeg(t *testing.T) {
	hdrToneMapRealFFmpeg(t, "transfer=smpte2084")
}

func hdrToneMapRealFFmpeg(t *testing.T, colorParams string) {
	for _, exe := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(exe); err != nil {
			t.Skip(exe + " not available")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "hdr.mkv")
	gen := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc2=duration=3:size=640x360:rate=24", "-f", "lavfi", "-i", "sine=duration=3:sample_rate=48000",
		"-vf", "format=yuv420p10le", "-c:v", "libx265", "-preset", "ultrafast", "-x265-params", "log-level=error:"+colorParams, "-c:a", "pcm_s16le", "-shortest", path)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("cannot build an HDR fixture (needs libx265): %v %s", err, out)
	}
	src, err := library.Probe(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := Tracks(src); !IsHDRTransfer(v.ColorTransfer) {
		t.Skipf("fixture is not tagged HDR (%q)", v.ColorTransfer)
	}
	plan, err := Choose(src, clientCaps(), false, false)
	if err != nil || !plan.ToneMap || plan.Mode != "transcode" {
		t.Fatalf("plan = %+v, %v; want a tone-mapped conversion", plan, err)
	}
	j := Job{Path: path, Source: src, Plan: plan, Duration: 2 * time.Second, OutputDir: t.TempDir()}
	args, err := Arguments(j)
	if err != nil || !strings.Contains(strings.Join(args, " "), "tonemap=tonemap=hable") {
		t.Fatalf("arguments lack the tone-map stage: %v %v", args, err)
	}
	if err := (Runner{}).Run(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	out, err := library.Probe(ctx, filepath.Join(j.OutputDir, "segment-000000000.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := Tracks(out); v.Codec != "h264" || v.PixelFormat != "yuv420p" || IsHDRTransfer(v.ColorTransfer) {
		t.Fatalf("output must be SDR H.264: %+v", v)
	}
}

func TestToneMapRejectsNonHDRSource(t *testing.T) {
	src := sourceInfo()
	j := Job{Path: "/x.mkv", Source: src, Plan: Plan{Mode: "transcode", ToneMap: true, Width: 1280, Height: 720, FPS: 24, VideoBitrate: 4000000, Profile: "high", AudioCopy: true}, Duration: time.Second, OutputDir: "/tmp/x"}
	if _, err := Arguments(j); err == nil {
		t.Fatal("tone mapping accepted for an SDR source")
	}
}

func TestRunnerAppliesMeasuredLoudness(t *testing.T) {
	j := fixture(t)
	_, audio := Tracks(j.Source)
	measurement, state, err := loudness.Analyze(t.Context(), j.Path, audio.Index)
	if err != nil || state != "ready" {
		t.Fatal(state, err)
	}
	j.Plan.Normalization = measurement
	if err = (Runner{}).Run(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(j.OutputDir, "index.m3u8")
	probe, err := library.Probe(t.Context(), out)
	if err != nil {
		t.Fatal(err)
	}
	_, audio = Tracks(probe)
	measured, state, err := loudness.Analyze(t.Context(), out, audio.Index)
	if err != nil || state != "ready" || math.Abs(measured.Integrated-loudness.Target) > 1 {
		t.Fatalf("normalized output: %+v %s %v", measured, state, err)
	}
}

func TestRunnerRealSurroundAAC(t *testing.T) {
	j := fixture(t)
	path := filepath.Join(t.TempDir(), "surround.mkv")
	cmd := exec.CommandContext(t.Context(), "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=128x72:rate=24", "-f", "lavfi", "-i", "anullsrc=channel_layout=7.1:sample_rate=48000", "-t", "3", "-c:v", "mpeg4", "-c:a", "pcm_s16le", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	src, err := library.Probe(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	caps := clientCaps()
	caps.Mbps = 100
	caps.Audio = append(caps.Audio, AudioCapability{Codec: "aac", Channels: 8, SampleRate: 48000, Bitrate: 768000, HLS: true, Container: "mp4"})
	plan, err := Choose(src, caps, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.AudioChannels != 8 {
		t.Fatalf("%+v", plan)
	}
	j.Path, j.Source, j.Plan = path, src, plan
	if err = (Runner{}).Run(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	out, err := library.Probe(t.Context(), filepath.Join(j.OutputDir, "segment-000000000.ts"))
	if err != nil {
		t.Fatal(err)
	}
	_, audio := Tracks(out)
	if audio.Channels != 8 || audio.Codec != "aac" {
		t.Fatalf("surround lost: %+v", audio)
	}
}
