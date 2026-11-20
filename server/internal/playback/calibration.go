package playback

import (
	"context"
	"math"
	"path/filepath"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/activity"
	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/logging"
)

// ProfileCapacity describes an actual homogeneous concurrent experiment, not a
// CPU-count extrapolation. Unverified paths retain conservative admission.
type ProfileCapacity struct {
	SourceClass    SourceClass `json:"sourceClass"`
	Encoder        string      `json:"encoder"`
	HardwareDecode bool        `json:"hardwareDecode"`
	ToneMapper     string      `json:"toneMapper,omitempty"`
	SourceWidth    int         `json:"sourceWidth"`
	SourceHeight   int         `json:"sourceHeight"`
	SourceFPS      float64     `json:"sourceFps"`
	Height         int         `json:"height"`
	FPS            float64     `json:"fps"`
	Threads        int         `json:"threads"`
	Streams        int         `json:"validatedStreams"`
	SingleSpeed    float64     `json:"singleSpeed"`
	Speeds         []float64   `json:"perStreamSpeeds"`
	Verified       bool        `json:"verified"`
	MeasuredAt     time.Time   `json:"measuredAt"`
}
type calibrationCase struct {
	class   SourceClass
	path    string
	source  library.ProbeResult
	plan    Plan
	threads int
}
type Calibration struct {
	Tuner     *Tuner
	Capacity  *Capacity
	DataDir   string
	InputsDir string
	// Replaced only by isolated tests; production always runs real frame windows.
	measure func(context.Context, calibrationCase, int) ([]float64, error)
}

func (c *Calibration) sample(ctx context.Context, test calibrationCase, count int) ([]float64, error) {
	if c.measure != nil {
		return c.measure(ctx, test, count)
	}
	trial, cancel := context.WithTimeout(ctx, 14*time.Second)
	defer cancel()
	return measureStreams(trial, test.path, test.source, test.plan, test.threads, count, time.Second, 4*time.Second)
}
func (c *Calibration) cases(ctx context.Context, inputs *benchmarkInputs, threads int, full bool) ([]calibrationCase, error) {
	encoder := c.Capacity.currentEncoder(ctx)
	decode := c.Capacity.HardwareDecoding != nil && c.Capacity.HardwareDecoding(ctx)
	classes := []SourceClass{SourceH264HD, SourceHEVCPQ, SourceHEVCHLG}
	if full {
		classes = append(classes, SourceHEVCSDR, SourceSDRSmall)
	}
	var cases []calibrationCase
	for _, class := range classes {
		fps := float64(60)
		if class == SourceHEVCSDR || class == SourceSDRSmall {
			fps = 30
		}
		path := inputs.files[inputName(class, fps)]
		probe, err := library.Probe(ctx, path)
		if err != nil {
			return nil, err
		}
		height := 1080
		if class == SourceSDRSmall {
			height = 480
		}
		plan := Plan{Mode: "transcode", Width: height * 16 / 9 / 2 * 2, Height: height, FPS: 30, Profile: outputProfile(height), VideoBitrate: heightBitrate(height), AudioBitrate: 192000, AudioChannels: 2, Encoder: encoder}
		if class == SourceHEVCPQ || class == SourceHEVCHLG {
			plan.ToneMap = true
			plan.ToneMapper = "cpu"
		}
		video, _ := Tracks(probe)
		if decode && encoder != "" && c.Capacity.Hardware != nil && c.Capacity.Hardware.DecodeAllowed(encoder, video) && c.Capacity.Hardware.DecodeReady(ctx, encoder) {
			plan.Decoder = "hardware"
			if class == SourceHEVCPQ && c.Capacity.Hardware.ToneMapOnGPU(ctx, encoder) {
				plan.ToneMapper = "gpu"
			}
		}
		// GPU pipelines start with a small CPU thread pool; CPU fallbacks use the
		// separately measured CPU-bound choice instead of assuming they share an optimum.
		selected := threads
		if plan.Decoder == "hardware" {
			selected = DefaultThreads
		}
		cases = append(cases, calibrationCase{class, path, probe, plan, selected})
	}
	return cases, nil
}
func (c *Calibration) tune(ctx context.Context, inputs *benchmarkInputs) {
	stage, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	path := inputs.files[inputName(SourceHEVCPQ, 60)]
	probe, err := library.Probe(stage, path)
	if err != nil {
		return
	}
	encoder := c.Capacity.currentEncoder(stage)
	plan := Plan{Mode: "transcode", Width: 1920, Height: 1080, FPS: 30, ToneMap: true, ToneMapper: "cpu", Encoder: encoder, Profile: "high", VideoBitrate: 12000000, AudioBitrate: 192000, AudioChannels: 2}
	var samples []TuneSample
	for _, threads := range candidatesUpTo(c.Tuner.cpuCount()) {
		speeds, err := c.sample(stage, calibrationCase{SourceHEVCPQ, path, probe, plan, threads}, 1)
		if err != nil {
			break
		}
		samples = append(samples, TuneSample{threads, speeds[0]})
		if len(samples) >= 3 && samples[len(samples)-1].Speed <= samples[len(samples)-2].Speed*1.1 && samples[len(samples)-2].Speed <= samples[len(samples)-3].Speed*1.1 {
			break
		}
	}
	if len(samples) == 0 {
		return
	}
	chosen := PickThreads(samples)
	now := time.Now()
	c.Tuner.mu.Lock()
	c.Tuner.status.Threads = chosen
	c.Tuner.status.MeasuredAt = &now
	c.Tuner.status.Samples = samples
	c.Tuner.status.Workload = "Steady CPU decode/tone-map fallback"
	c.Tuner.mu.Unlock()
	if c.Tuner.Save != nil {
		c.Tuner.Save(ctx, chosen, now)
	}
	logging.Info("startup calibration: CPU thread choice %d (%s)", chosen, describeSamples(samples))
}
func minimumSpeed(speeds []float64) float64 {
	min := math.Inf(1)
	for _, speed := range speeds {
		if math.IsNaN(speed) || math.IsInf(speed, 0) || speed < 0 {
			return 0
		}
		min = math.Min(min, speed)
	}
	if len(speeds) == 0 {
		return 0
	}
	return min
}
func (c *Calibration) evaluate(ctx context.Context, test calibrationCase, old []ProfileCapacity) ProfileCapacity {
	video, _ := Tracks(test.source)
	p := ProfileCapacity{SourceClass: test.class, Encoder: test.plan.Encoder, HardwareDecode: test.plan.Decoder == "hardware", ToneMapper: test.plan.ToneMapper, SourceWidth: video.Width, SourceHeight: video.Height, SourceFPS: video.FrameRate, Height: test.plan.Height, FPS: test.plan.FPS, Threads: test.threads}
	speeds, err := c.sample(ctx, test, 1)
	if err != nil {
		logging.Warn("calibration %s: %v", test.class, err)
		return p
	}
	if test.plan.Decoder == "hardware" && len(old) == 0 && c.Tuner.cpuCount() >= 4 {
		alternate := test
		alternate.threads = 4
		if trial, err := c.sample(ctx, alternate, 1); err == nil && minimumSpeed(trial) > minimumSpeed(speeds)*1.1 {
			test = alternate
			speeds = trial
			p.Threads = 4
		}
	}
	p.SingleSpeed = minimumSpeed(speeds)
	p.Speeds = speeds
	p.Verified = true
	p.MeasuredAt = time.Now()
	if p.SingleSpeed < CapacityRealTime {
		// A failing expensive rung does not imply every lower output also fails.
		test.plan.Height = 360
		test.plan.Width = 640
		test.plan.VideoBitrate = heightBitrate(360)
		test.plan.Profile = outputProfile(360)
		speeds, err = c.sample(ctx, test, 1)
		if err != nil {
			return p
		}
		p.Height = 360
		p.SingleSpeed = minimumSpeed(speeds)
		p.Speeds = speeds
	}
	if p.SingleSpeed < CapacityRealTime {
		return p
	}
	p.Streams = 1
	// A cache hit is confirmed at its actual previous stream count, not with a
	// single fast stream that might conceal contention at the cached limit.
	counts := []int{2, 4}
	for _, cached := range old {
		if sameProfile(p, cached) && cached.Verified && cached.Streams > 1 {
			counts = []int{2}
			if cached.Streams > 2 {
				counts = append(counts, cached.Streams)
			}
			break
		}
	}
	for _, count := range counts {
		if ctx.Err() != nil {
			break
		}
		speeds, err = c.sample(ctx, test, count)
		if err != nil || minimumSpeed(speeds) < CapacityRealTime {
			break
		}
		if minimumSpeed(speeds) < 1.5 {
			confirmation, confirmErr := c.sample(ctx, test, count)
			if confirmErr != nil || minimumSpeed(confirmation) < CapacityRealTime {
				break
			}
			if minimumSpeed(confirmation) < minimumSpeed(speeds) {
				speeds = confirmation
			}
		}
		p.Streams = count
		p.Speeds = speeds
		p.MeasuredAt = time.Now()
	}
	return p
}
func sameProfile(a, b ProfileCapacity) bool {
	return a.SourceClass == b.SourceClass && a.Encoder == b.Encoder && a.HardwareDecode == b.HardwareDecode && a.ToneMapper == b.ToneMapper && a.Height == b.Height && a.FPS == b.FPS && a.Threads == b.Threads
}

// Startup is deliberately bounded by the owning Benchmarks group's deadline.
// Expensive full-matrix probing is reserved for idle periodic runs.
func (c *Calibration) Startup(ctx context.Context) { c.run(ctx, false) }
func (c *Calibration) Full(ctx context.Context)    { c.run(ctx, true) }
func (c *Calibration) run(ctx context.Context, full bool) {
	_, finish := activity.Default.Start("Representative Capacity Validation", "pipelines", 0)
	defer finish()
	directory := c.InputsDir
	if directory == "" {
		directory = BenchmarkInputsDir
	}
	inputs, err := loadBenchmarkInputs(directory)
	if err != nil {
		logging.Warn("startup calibration: prepared inputs unavailable: %v", err)
		c.Capacity.resetCalibration()
		return
	}
	fingerprint, err := c.fingerprint(ctx, inputs)
	if err != nil {
		logging.Warn("calibration identity unavailable: %v", err)
		c.Capacity.resetCalibration()
		return
	}
	cached, hit := readCalibrationCache(filepath.Join(c.DataDir, "benchmark-cache.json"), fingerprint)
	var old []ProfileCapacity
	if hit {
		old = cached.Profiles
	}
	if hit && !full {
		c.Capacity.mu.Lock()
		c.Capacity.status = cached.Status
		c.Capacity.hdr = cached.HDR
		c.Capacity.profiles = append([]ProfileCapacity(nil), old...)
		c.Capacity.calibrated = true
		c.Capacity.mu.Unlock()
		c.Tuner.Load(cached.Threads, cached.At)
	} else if !full {
		c.Capacity.resetCalibration()
	}
	if !full && !hit {
		c.tune(ctx, inputs)
	}
	cases, err := c.cases(ctx, inputs, c.Tuner.Threads(), full)
	if err != nil {
		logging.Warn("calibration pipeline setup: %v", err)
		return
	}
	profiles := append([]ProfileCapacity(nil), old...)
	for _, test := range cases {
		if hit && !full {
			for _, prior := range old {
				if prior.SourceClass == test.class && prior.HardwareDecode == (test.plan.Decoder == "hardware") && prior.Encoder == test.plan.Encoder && prior.ToneMapper == test.plan.ToneMapper {
					test.threads = prior.Threads
					break
				}
			}
		}
		if ctx.Err() != nil {
			break
		}
		// At most one weak cached class is retained per identity; a completed new
		// failure supersedes it, while interruption preserves prior validated data.
		result := c.evaluate(ctx, test, old)
		if !result.Verified && ctx.Err() != nil {
			break
		}
		filtered := profiles[:0]
		for _, prior := range profiles {
			if prior.SourceClass != result.SourceClass || prior.HardwareDecode != result.HardwareDecode || prior.Encoder != result.Encoder {
				filtered = append(filtered, prior)
			}
		}
		profiles = append(filtered, result)
		c.Capacity.publishProfiles(profiles)
		logging.Info("capacity validation: %s decode=%t %dp%g: %d validated streams (slowest %.2fx)", result.SourceClass, result.HardwareDecode, result.Height, result.FPS, result.Streams, minimumSpeed(result.Speeds))
		c.save(ctx, fingerprint)
	}
	c.Capacity.publishProfiles(profiles)
	if c.Capacity.OnChange != nil {
		c.Capacity.OnChange()
	}
}
func (c *Capacity) resetCalibration() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calibrated = true
	c.profiles = nil
	c.hdr = nil
	c.status.Samples = nil
	c.status.Processes = 1
	c.status.Calibration = "unverified"
	c.status.MaxHeight = 0
	c.status.MaxFPS = 0
}
func (c *Capacity) publishProfiles(profiles []ProfileCapacity) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calibrated = true
	c.profiles = append([]ProfileCapacity(nil), profiles...)
	c.status.Processes = 1
	c.status.Calibration = "representative"
	for _, p := range profiles {
		if !p.Verified {
			continue
		}
		c.status.Processes = max(c.status.Processes, p.Streams)
		sample := RungSample{SourceClass: p.SourceClass, Height: p.Height, FPS: p.FPS, Speed: p.SingleSpeed}
		if p.HardwareDecode && p.ToneMapper != "" {
			record := HDRCapacity{SourceFPS: p.SourceFPS, Encoder: p.Encoder, HardwareDecode: true, Transfer: "smpte2084", ToneMapper: p.ToneMapper, Threads: p.Threads, Samples: []RungSample{sample}, MeasuredAt: p.MeasuredAt}
			if p.SourceClass == SourceHEVCHLG {
				record.Transfer = "arib-std-b67"
			}
			found := false
			for i, old := range c.hdr {
				if old.Transfer == record.Transfer && old.SourceFPS == record.SourceFPS && old.Threads == record.Threads {
					// Keep the broader idle matrix while refreshing this measured rung.
					record.Samples = append([]RungSample(nil), old.Samples...)
					replaced := false
					for j, rung := range record.Samples {
						if rung.Height == sample.Height && rung.FPS == sample.FPS {
							record.Samples[j] = sample
							replaced = true
							break
						}
					}
					if !replaced {
						record.Samples = append(record.Samples, sample)
					}
					c.hdr[i] = record
					found = true
					break
				}
			}
			if !found {
				c.hdr = append(c.hdr, record)
			}
		} else {
			replaced := false
			for i, old := range c.status.Samples {
				if old.SourceClass == p.SourceClass && old.Height == p.Height && old.FPS == p.FPS {
					c.status.Samples[i] = sample
					replaced = true
					break
				}
			}
			if !replaced {
				c.status.Samples = append(c.status.Samples, sample)
			}
		}
		c.status.Encoder = p.Encoder
	}
	now := time.Now()
	c.status.MeasuredAt = &now
	c.status.ProcessesAt = &now
}
func profileMatches(p ProfileCapacity, job Job) bool {
	video, _ := Tracks(job.Source)
	return p.Verified && p.SourceClass == classifySource(video) && p.Encoder == job.Plan.Encoder && p.HardwareDecode == (job.Plan.Decoder == "hardware") && p.ToneMapper == job.Plan.ToneMapper && video.Width <= p.SourceWidth && video.Height <= p.SourceHeight && video.FrameRate <= p.SourceFPS+.01 && job.Plan.Height <= p.Height && job.Plan.FPS <= p.FPS+.01 && job.Threads == p.Threads
}
func (c *Capacity) calibratedAdmission(active []Job, job Job) (bool, error) {
	if c == nil {
		return false, nil
	}
	c.mu.Lock()
	enabled := c.calibrated
	profiles := append([]ProfileCapacity(nil), c.profiles...)
	c.mu.Unlock()
	if !enabled {
		return false, nil
	}
	limit, total := MaxParallel, 0
	for _, current := range append(append([]Job(nil), active...), job) {
		cost := jobResourceCost(current)
		if cost == 0 {
			continue
		}
		total += cost
		found := false
		for _, p := range profiles {
			if profileMatches(p, current) {
				found = true
				if p.Streams == 0 {
					return true, ErrCapacityUnsustainable
				}
				limit = min(limit, p.Streams)
				break
			}
		}
		if !found {
			limit = 1
		}
	}
	if total > limit {
		return true, ErrBusy
	}
	free := memoryAvailable()
	if c.memory != nil {
		free = c.memory()
	}
	if free == 0 && total > 1 || free > 0 && float64(total*perConversionMemory) > float64(free)*memoryHeadroom {
		return true, ErrBusy
	}
	return true, nil
}
func (c *Capacity) ProfileThreads(source library.ProbeResult, encoder string, decode bool, fallback int) int {
	if c == nil {
		return fallback
	}
	video, _ := Tracks(source)
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range c.profiles {
		if p.Verified && p.Streams > 0 && p.SourceClass == classifySource(video) && p.Encoder == encoder && p.HardwareDecode == decode && video.Width <= p.SourceWidth && video.Height <= p.SourceHeight && video.FrameRate <= p.SourceFPS+.01 {
			return p.Threads
		}
	}
	return fallback
}
func (c *Capacity) ValidatedThreads(job Job) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range c.profiles {
		if p.Streams > 0 && profileMatches(p, job) {
			return true
		}
	}
	return false
}
