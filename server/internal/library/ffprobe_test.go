package library

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestSampleRateRejectsOverflowAndNegativeMetadata(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	for _, tc := range []struct {
		raw  string
		want int
	}{
		{"48000", 48000}, {"", 0}, {"-48000", 0}, {"not a rate", 0},
		{strconv.FormatUint(uint64(maxInt)+1, 10), 0},
		{"18446744073709551615", 0},
	} {
		probe := rawProbe{Streams: []rawStream{{CodecType: "audio", SampleRate: tc.raw}}}.toProbeResult()
		if got := probe.Streams[0].SampleRate; got != tc.want {
			t.Errorf("sample rate %q = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

// requireFFmpeg skips the test if ffmpeg/ffprobe aren't on PATH, so this
// suite degrades gracefully on a machine or CI image that hasn't
// installed the media toolchain yet, instead of failing misleadingly.
func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not on PATH")
	}
}

// syntheticMediaFile generates a short, silent, synthetically-patterned
// video+audio fixture with ffmpeg's lavfi sources — no real (or
// copyrighted) media ever touches this test suite.
func syntheticMediaFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sample.avi")

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
		t.Fatalf("generate synthetic fixture: %v\n%s", err, out)
	}
	return path
}

func TestProbe_ExtractsDurationContainerAndStreams(t *testing.T) {
	requireFFmpeg(t)
	path := syntheticMediaFile(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := Probe(ctx, path)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}

	if result.Container == "" {
		t.Error("expected a non-empty container")
	}
	if result.DurationSeconds < 0.9 || result.DurationSeconds > 1.2 {
		t.Errorf("expected duration near 1s, got %v", result.DurationSeconds)
	}
	if result.SizeBytes <= 0 {
		t.Errorf("expected a positive size, got %d", result.SizeBytes)
	}

	var sawVideo, sawAudio bool
	for _, s := range result.Streams {
		switch s.Kind {
		case StreamVideo:
			sawVideo = true
			if s.Width != 64 || s.Height != 64 {
				t.Errorf("expected a 64x64 video stream, got %dx%d", s.Width, s.Height)
			}
		case StreamAudio:
			sawAudio = true
			if s.Channels <= 0 {
				t.Errorf("expected a positive channel count, got %d", s.Channels)
			}
		}
	}
	if !sawVideo {
		t.Error("expected a video stream in the result")
	}
	if !sawAudio {
		t.Error("expected an audio stream in the result")
	}
}

func TestProbe_CorruptFile_ReturnsErrorWithoutCrashing(t *testing.T) {
	requireFFmpeg(t)

	path := filepath.Join(t.TempDir(), "corrupt.mp4")
	if err := os.WriteFile(path, []byte("this is not a real media file"), 0o644); err != nil {
		t.Fatalf("write corrupt fixture: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := Probe(ctx, path)
	if err == nil {
		t.Fatal("expected an error probing a corrupt file, got nil")
	}
}

func TestProbe_MissingFile_ReturnsErrorWithoutCrashing(t *testing.T) {
	requireFFmpeg(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := Probe(ctx, filepath.Join(t.TempDir(), "does-not-exist.mp4"))
	if err == nil {
		t.Fatal("expected an error probing a missing file, got nil")
	}
}

func TestProbeHDRMetadata(t *testing.T) {
	raw := rawProbe{Streams: []rawStream{{Index: 0, CodecType: "video", CodecName: "hevc", CodecTag: "dvh1", SideData: []rawSideData{{Type: "DOVI configuration record", DVProfile: 5, DVLevel: 9}}}}}
	result := raw.toProbeResult()
	if result.MetadataVersion != 3 || result.Streams[0].DolbyVisionProfile != 5 || result.Streams[0].DolbyVisionLevel != 9 || result.Streams[0].CodecTag != "dvh1" {
		t.Fatalf("%+v", result)
	}
	raw.Streams[0].SideData = []rawSideData{{Type: "HDR Dynamic Metadata SMPTE2094-40 (HDR10+)"}}
	if !raw.toProbeResult().Streams[0].HDR10Plus {
		t.Fatal("HDR10+ metadata lost")
	}
}

func TestProbeRecognizesStaticHDRFrameMetadata(t *testing.T) {
	var raw rawProbe
	if err := json.Unmarshal([]byte(`{"streams":[{"index":0,"codec_type":"video","codec_name":"hevc"}],"frames":[{"stream_index":0,"side_data_list":[{"side_data_type":"Mastering display metadata"}]}]}`), &raw); err != nil {
		t.Fatal(err)
	}
	if !raw.toProbeResult().Streams[0].HDRStaticMetadata {
		t.Fatal("static HDR frame metadata lost")
	}
}
