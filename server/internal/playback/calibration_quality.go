package playback

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/governor"
)

var ssimOverall = regexp.MustCompile(`All:([0-9.]+)`)

// Reference SSIM measures agreement with the software color pipeline, not a
// subjective HDR quality score. The reference is lossless; the candidate uses
// the actual playback encoder, bitrate and profile, including compression loss.
func (c *Calibration) referenceQuality(ctx context.Context, test calibrationCase) *float64 {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	dir, err := os.MkdirTemp("", "alreadyon-quality-")
	if err != nil {
		return nil
	}
	defer os.RemoveAll(dir)
	v, _ := Tracks(test.source)
	cpu := test.plan
	cpu.Encoder = ""
	cpu.Decoder = ""
	if cpu.ToneMap {
		cpu.ToneMapper = "cpu"
	}
	reference, _, err := videoFilter(cpu, v)
	if err != nil {
		return nil
	}
	run := func(name string, global, input []string, filter string, encode []string) bool {
		args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
		args = append(args, global...)
		args = append(args, input...)
		args = append(args, "-i", test.path, "-an", "-sn", "-frames:v", "8", "-vf", filter, "-filter_threads", "1")
		args = append(args, encode...)
		args = append(args, "-threads", "2", "-y", filepath.Join(dir, name))
		return governor.BenchmarkCommand(ctx, lowPriority(ctx, "ffmpeg", args...)) == nil
	}
	if !run("reference.mkv", nil, nil, reference+",format=yuv420p", []string{"-c:v", "ffv1"}) {
		return nil
	}
	filter := reference
	var global, input []string
	encode := softwareVideoArgs(softwareEncoder(), test.plan.Profile, "2", "1")
	encode = append(encode, "-b:v", strconv.FormatInt(test.plan.VideoBitrate, 10), "-maxrate", strconv.FormatInt(test.plan.VideoBitrate, 10), "-bufsize", strconv.FormatInt(test.plan.VideoBitrate*2, 10))
	if test.plan.Decoder == "hardware" {
		pipeline, e := gpuPipelineFor(test.plan, v.ColorTransfer, RenderNodeFor(test.plan.Encoder))
		if e != nil {
			return nil
		}
		global, input, filter, encode = pipeline.global, pipeline.input, pipeline.filter, pipeline.encode
	} else if test.plan.Encoder != "" {
		setup, e := hardwareSetup(test.plan.Encoder, RenderNodeFor(test.plan.Encoder), test.plan.Profile, test.plan.VideoBitrate)
		if e != nil {
			return nil
		}
		global = setup.global
		filter += "," + setup.filter
		encode = setup.encode
	}
	if !run("candidate.mkv", global, input, filter, encode) {
		return nil
	}
	return referenceSimilarity(ctx, filepath.Join(dir, "reference.mkv"), filepath.Join(dir, "candidate.mkv"))
}

// Encoder reordering and container timestamp offsets must not shift the frames
// being compared: SSIM should reflect pixels, not differing stream start times.
func referenceSimilarity(ctx context.Context, reference, candidate string) *float64 {
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-nostdin", "-i", reference, "-i", candidate, "-lavfi", "[0:v]settb=1/1,setpts=N[ref];[1:v]settb=1/1,setpts=N[candidate];[ref][candidate]ssim=shortest=1", "-f", "null", "-")
	raw, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}
	match := ssimOverall.FindSubmatch(raw)
	if len(match) != 2 {
		return nil
	}
	value, err := strconv.ParseFloat(string(match[1]), 64)
	if err != nil || value < 0 || value > 1 {
		return nil
	}
	return &value
}
