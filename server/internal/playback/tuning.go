package playback

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/activity"
	"github.com/alreadyonapp/alreadyon/server/internal/governor"
	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/logging"
)

// DefaultThreads is the CPU thread count for decoding when nothing has been measured.
const DefaultThreads = 2

// MaxThreads bounds what a job may ask for.
const MaxThreads = 64

// threadCandidates are the counts tried, doubling from two.
var threadCandidates = []int{2, 4, 8, 16, 32}

// TuneSample is one measured thread count: how many times real time the test
// pipeline ran (2 means a minute of video took thirty seconds).
type TuneSample struct {
	Threads int     `json:"threads"`
	Speed   float64 `json:"speed"`
}

// TuneStatus is the thread tuning API's answer.
type TuneStatus struct {
	Threads    int          `json:"threads"`
	State      string       `json:"state"` // idle or running
	MeasuredAt *time.Time   `json:"measuredAt"`
	CPUs       int          `json:"cpus"`
	Workload   string       `json:"workload"`
	Samples    []TuneSample `json:"samples"`
	Detail     string       `json:"detail"`
}

// Tuner finds how many CPU threads one transcode should use on this server by
// timing a demanding pipeline (2160p HEVC HDR10 decoded, tone mapped and encoded,
// the way a stream would be without the GPU) at increasing counts. The result
// becomes the default for every transcode. It runs at startup and whenever the
// encoder or hardware decoding setting changes, never while a stream plays
// unless an administrator asks.
type Tuner struct {
	// Context ends measurements when the server shuts down; nil never does.
	Context    context.Context
	Executable string
	Hardware   *Hardware
	// Encoder is the hardware encoder in use ("" for software).
	Encoder func(context.Context) string
	// Processes is how many transcodes may run at once; the count is capped so
	// that many streams together do not exceed the CPU.
	Processes func(context.Context) int
	// Busy reports running transcodes, which would distort a measurement.
	Busy func() bool
	// Save keeps the result across restarts.
	Save func(context.Context, int, time.Time)
	// measure is replaced in tests.
	measure func(ctx context.Context, threads int, encoder, sample string) (float64, error)
	cpus    int

	mu      sync.Mutex
	status  TuneStatus
	started bool
}

// Load sets the thread count from a previous run, before the first measurement.
func (t *Tuner) Load(threads int, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if threads >= 1 && threads <= MaxThreads {
		t.status.Threads = threads
		if !at.IsZero() {
			t.status.MeasuredAt = &at
		}
	}
}

// Threads is the count to give a transcode.
func (t *Tuner) Threads() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.status.Threads < 1 {
		return DefaultThreads
	}
	return t.status.Threads
}

func (t *Tuner) Status() TuneStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.status
	if s.Threads < 1 {
		s.Threads = DefaultThreads
	}
	if s.State == "" {
		s.State = "idle"
	}
	s.CPUs = t.cpuCount()
	s.Samples = append([]TuneSample(nil), s.Samples...)
	return s
}

func (t *Tuner) cpuCount() int {
	if t.cpus > 0 {
		return t.cpus
	}
	return runtime.NumCPU()
}

// Start measures in the background. It does nothing when a measurement is
// already running, and (unless force) when a stream is playing.
func (t *Tuner) Start(force bool) bool {
	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		return false
	}
	if !force && t.Busy != nil && t.Busy() {
		t.status.Detail = "Skipped while a stream is playing; measure again with POST /api/settings/transcoding/threads/tune."
		t.mu.Unlock()
		return false
	}
	t.started = true
	t.status.State = "running"
	t.status.Detail = ""
	t.mu.Unlock()
	base := t.Context
	if base == nil {
		base = context.Background()
	}
	go func() {
		ctx, cancel := measurementContext(base, force, t.Busy)
		defer cancel()
		t.run(ctx)
	}()
	return true
}

func (t *Tuner) run(ctx context.Context) {
	_, finish := activity.Default.Start("Transcode Thread Measurement", "benchmarking", 0)
	defer finish()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	encoder := ""
	if t.Encoder != nil {
		encoder = t.Encoder(ctx)
	}
	if !IsHardwareEncoder(encoder) || (t.Hardware != nil && !t.Hardware.Check(ctx, encoder, false).OK) {
		encoder = "" // software, or streams use software while the encoder does not work
	}
	processes := 1
	if t.Processes != nil {
		processes = max(1, t.Processes(ctx))
	}
	limit := max(2, t.cpuCount()/processes)
	samples, workload, err := t.benchmark(ctx, encoder, limit)

	t.mu.Lock()
	defer t.mu.Unlock()
	t.started = false
	t.status.State = "idle"
	if err != nil || ctx.Err() != nil || len(samples) == 0 {
		if err == nil {
			err = ctx.Err()
		}
		t.status.Detail = fmt.Sprintf("The measurement failed, so %d threads stay in use: %v", t.threadsLocked(), err)
		logging.Warn("transcode threads: %s", t.status.Detail)
		return
	}
	now := time.Now()
	pick := PickThreads(samples)
	t.status.Threads, t.status.MeasuredAt, t.status.Samples, t.status.Workload = pick, &now, samples, workload
	t.status.Detail = ""
	if best := bestSpeed(samples); best < 1 {
		t.status.Detail = fmt.Sprintf("This server cannot convert the test video in real time even with %d threads.", pick)
	}
	logging.Info("transcode threads: %d (%s; %s)", pick, workload, describeSamples(samples))
	if t.Save != nil {
		t.Save(ctx, pick, now)
	}
}

func (t *Tuner) threadsLocked() int {
	if t.status.Threads < 1 {
		return DefaultThreads
	}
	return t.status.Threads
}

// PickThreads chooses the smallest count within 10% of the fastest one: more
// threads past that point cost CPU that other streams could use.
func PickThreads(samples []TuneSample) int {
	best := bestSpeed(samples)
	for _, s := range samples {
		if s.Speed >= best*0.9 {
			return s.Threads
		}
	}
	return DefaultThreads
}

func bestSpeed(samples []TuneSample) float64 {
	best := 0.0
	for _, s := range samples {
		best = max(best, s.Speed)
	}
	return best
}

func describeSamples(samples []TuneSample) string {
	var parts []string
	for _, s := range samples {
		parts = append(parts, fmt.Sprintf("%d threads %.2fx", s.Threads, s.Speed))
	}
	return strings.Join(parts, ", ")
}

// candidatesUpTo lists the counts to try: from two up to the cap, always at least two.
func candidatesUpTo(limit int) []int {
	var out []int
	for _, n := range threadCandidates {
		if n <= limit {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		out = []int{2}
	}
	return out
}

// benchmark times the test pipeline at each candidate count, stopping once more
// threads no longer help.
func (t *Tuner) benchmark(ctx context.Context, encoder string, limit int) ([]TuneSample, string, error) {
	dir, err := os.MkdirTemp("", "alreadyon-tune-")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(dir)
	exe := t.Executable
	if exe == "" {
		exe = "ffmpeg"
	}
	sample, codec, err := makeTuneSample(ctx, exe, dir)
	if err != nil {
		return nil, "", err
	}
	workload := fmt.Sprintf("2160p %s HDR10 to 1080p, CPU decode and tone map, %s encode", strings.ToUpper(codec), EncoderLabel(encoder))
	measure := t.measure
	if measure == nil {
		measure = func(ctx context.Context, threads int, encoder, sample string) (float64, error) {
			return measureThreads(ctx, exe, dir, threads, encoder, sample)
		}
	}
	var samples []TuneSample
	for _, n := range candidatesUpTo(limit) {
		speed, err := measure(ctx, n, encoder, sample)
		if err != nil {
			return samples, workload, err
		}
		samples = append(samples, TuneSample{Threads: n, Speed: speed})
		// Two steps in a row that gain under 10% mean the curve has flattened.
		if k := len(samples); k >= 3 && samples[k-1].Speed < samples[k-2].Speed*1.1 && samples[k-2].Speed < samples[k-3].Speed*1.1 {
			break
		}
	}
	return samples, workload, nil
}

const tuneSeconds = 3

// makeTuneSample encodes a few seconds of noisy 2160p HDR10 video to disk, as
// HEVC 10-bit when this FFmpeg can, else as 8-bit H.264 tagged the same way.
func makeTuneSample(ctx context.Context, exe, dir string) (path, codec string, err error) {
	path = filepath.Join(dir, "sample.mkv")
	base := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-f", "lavfi", "-i",
		fmt.Sprintf("testsrc2=size=3840x2160:rate=24:duration=%d,noise=alls=6:allf=t", tuneSeconds)}
	attempts := []struct {
		codec string
		args  []string
	}{
		{"hevc", []string{"-vf", "setparams=color_primaries=bt2020:color_trc=smpte2084:colorspace=bt2020nc:range=tv,format=yuv420p10le", "-c:v", "libx265", "-preset", "ultrafast", "-crf", "18", "-x265-params", "log-level=error"}},
		{"h264", []string{"-vf", "setparams=color_primaries=bt2020:color_trc=smpte2084:colorspace=bt2020nc:range=tv,format=yuv420p", "-c:v", "libx264", "-preset", "ultrafast", "-crf", "18"}},
		{"h264", []string{"-vf", "setparams=color_primaries=bt2020:color_trc=smpte2084:colorspace=bt2020nc:range=tv,format=yuv420p", "-c:v", "libopenh264", "-b:v", "60M"}},
	}
	var last error
	for _, a := range attempts {
		args := append(append([]string{}, base...), a.args...)
		args = append(args, path)
		if out, e := governor.BackgroundOutput(ctx, lowPriority(ctx, exe, args...)); e != nil {
			last = fmt.Errorf("%s: %v %s", a.codec, e, strings.TrimSpace(string(out)))
			_ = os.Remove(path)
			continue
		}
		return path, a.codec, nil
	}
	return "", "", fmt.Errorf("no encoder could make the test video: %v", last)
}

// measureThreads runs the stream pipeline on the sample at one thread count,
// without real-time pacing, and returns how many times faster than real time it ran.
func measureThreads(ctx context.Context, exe, dir string, threads int, encoder, sample string) (float64, error) {
	probe, err := library.Probe(ctx, sample)
	if err != nil {
		return 0, err
	}
	// An encoder may not write the HDR tags; the filter chain names them itself.
	for i := range probe.Streams {
		if probe.Streams[i].Kind == "video" {
			probe.Streams[i].ColorTransfer = "smpte2084"
		}
	}
	plan := Plan{Mode: "transcode", ToneMap: true, ToneMapper: "cpu", Width: 1920, Height: 1080, FPS: 24, VideoBitrate: 12000000, Profile: "high", Encoder: encoder}
	return timePlan(ctx, exe, dir, sample, probe, plan, threads)
}

// timePlan runs the stream pipeline for the plan on the sample, without
// real-time pacing, and returns how many times faster than real time it ran.
func timePlan(ctx context.Context, exe, dir, sample string, probe library.ProbeResult, plan Plan, threads int) (float64, error) {
	out, err := os.MkdirTemp(dir, "run-")
	if err != nil {
		return 0, err
	}
	job := Job{Path: sample, Source: probe, Plan: plan, Duration: tuneSeconds * time.Second, OutputDir: out, Threads: threads}
	if plan.Encoder != "" && plan.Encoder != EncoderNvidiaNVENC {
		job.Device = RenderNode()
	}
	args, err := Arguments(job)
	if err != nil {
		return 0, err
	}
	// Drop the real-time pacing; the point is how fast the pipeline can go.
	var fast []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-readrate" || args[i] == "-readrate_initial_burst" {
			i++
			continue
		}
		fast = append(fast, args[i])
	}
	start := time.Now()
	if b, err := governor.BackgroundOutput(ctx, lowPriority(ctx, exe, fast...)); err != nil {
		return 0, fmt.Errorf("%v: %s", err, strings.TrimSpace(string(b)))
	}
	return tuneSeconds / max(time.Since(start).Seconds(), 0.001), nil
}

// lowPriority lowers benchmark scheduling priority; this alone is not a CPU
// limit. Automatic measurements also cancel when playback becomes busy.
func lowPriority(ctx context.Context, exe string, args ...string) *exec.Cmd {
	if nice, err := exec.LookPath("nice"); err == nil {
		if io, err := exec.LookPath("ionice"); err == nil {
			return exec.CommandContext(ctx, nice, append([]string{"-n", "19", io, "-c", "3", exe}, args...)...)
		}
		return exec.CommandContext(ctx, nice, append([]string{"-n", "19", exe}, args...)...)
	}
	return exec.CommandContext(ctx, exe, args...)
}

// Benchmarks must measure unthrottled speed, so they cannot use the analysis
// CPU-time budget. Automatic runs instead yield by cancellation when a viewer
// arrives. Explicit administrator-forced measurements retain their override.
func measurementContext(parent context.Context, force bool, busy func() bool) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	if !force && busy != nil {
		go func() {
			ticker := time.NewTicker(200 * time.Millisecond)
			defer ticker.Stop()
			for {
				if busy() {
					cancel()
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	}
	return ctx, cancel
}
