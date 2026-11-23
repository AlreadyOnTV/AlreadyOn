package playback

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestReferenceQualityMeasuresEncodedCandidate(t *testing.T) {
	job := fixture(t)
	job.Plan.Profile = "high"
	job.Plan.VideoBitrate = 2000000
	c := &Calibration{}
	score := c.referenceQuality(t.Context(), calibrationCase{path: job.Path, source: job.Source, plan: job.Plan, threads: 2})
	if score == nil || *score < .8 || *score > 1 {
		t.Fatalf("encoded reference comparison unavailable or invalid: %v", score)
	}
}

func TestReferenceSimilarityAlignsStreamStartTimes(t *testing.T) {
	job := fixture(t)
	shifted := filepath.Join(t.TempDir(), "shifted.mkv")
	cmd := exec.CommandContext(t.Context(), "ffmpeg", "-v", "error", "-i", job.Path, "-map", "0:v:0", "-c:v", "copy", "-an", "-output_ts_offset", "2", shifted)
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("shifted fixture: %s %v", raw, err)
	}
	score := referenceSimilarity(t.Context(), job.Path, shifted)
	if score == nil || *score < .999 {
		if score == nil {
			t.Fatal("timestamp comparison unavailable")
		}
		t.Fatalf("timestamp offset changed pixel fidelity: %f", *score)
	}
}
