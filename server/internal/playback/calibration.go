package playback

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/activity"
	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/logging"
)

// ProfileCapacity describes an actual homogeneous concurrent experiment, not a
// CPU-count extrapolation. Unverified paths retain conservative admission.
type ProfileCapacity struct {
	TargetHeight      int         `json:"targetHeight,omitempty"`
	SourceClass       SourceClass `json:"sourceClass"`
	Encoder           string      `json:"encoder"`
	HardwareDecode    bool        `json:"hardwareDecode"`
	ToneMapper        string      `json:"toneMapper,omitempty"`
	SourceWidth       int         `json:"sourceWidth"`
	SourceHeight      int         `json:"sourceHeight"`
	SourceFPS         float64     `json:"sourceFps"`
	Height            int         `json:"height"`
	FPS               float64     `json:"fps"`
	Threads           int         `json:"threads"`
	Streams           int         `json:"validatedStreams"`
	SingleStreamSpeed float64     `json:"singleStreamSpeed"`
	SingleSpeed       float64     `json:"singleSpeed"`
	Speeds            []float64   `json:"perStreamSpeeds"`
	Verified          bool        `json:"verified"`
	MeasuredAt        time.Time   `json:"measuredAt"`
	CPUCores          float64     `json:"cpuCores"`
	GPUEngines        *float64    `json:"gpuEngines,omitempty"`
	ReferenceSSIM     *float64    `json:"referenceSsim,omitempty"`
}
type calibrationCase struct {
	class     SourceClass
	path      string
	source    library.ProbeResult
	plan      Plan
	threads   int
	resources *resourceMeasurement
	full      bool
	mini      bool
}
type Calibration struct {
	Tuner     *Tuner
	Capacity  *Capacity
	DataDir   string
	InputsDir string
	// Replaced only by isolated tests; production always runs real frame windows.
	measure func(context.Context, calibrationCase, int) ([]float64, error)
	mu      sync.Mutex
	quick   calibrationCache
	full    calibrationCache
	quickOK bool
	fullOK  bool
}

const (
	fullSampleWarmup = 500 * time.Millisecond
	fullSampleWindow = 2 * time.Second
	miniSampleWarmup = 200 * time.Millisecond
	miniSampleWindow = time.Second
)

// BenchmarkResult summarizes one recorded pipeline case for Server Settings.
type BenchmarkResult struct {
	Name       string  `json:"name"`
	Multiplier float64 `json:"multiplier"`
	Passed     bool    `json:"passed"`
}

type CalibrationStatus struct {
	QuickAt       time.Time         `json:"quickAt,omitempty"`
	FullAt        time.Time         `json:"fullAt,omitempty"`
	RecommendFull bool              `json:"recommendFull"`
	QuickProfiles int               `json:"quickProfiles"`
	FullProfiles  int               `json:"fullProfiles"`
	Results       []BenchmarkResult `json:"results,omitempty"`
}

func (c *Calibration) Status() CalibrationStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	status := CalibrationStatus{RecommendFull: c.recommendFullLocked()}
	if c.quickOK {
		status.QuickAt = c.quick.At
		status.QuickProfiles = len(c.quick.Profiles)
		status.Results = appendBenchmarkResults(status.Results, "Quick", c.quick.Profiles)
	}
	if c.fullOK {
		status.FullAt = c.full.At
		status.FullProfiles = len(c.full.Profiles)
		status.Results = appendBenchmarkResults(status.Results, "Full", c.full.Profiles)
	}
	return status
}

func (c *Calibration) sample(ctx context.Context, test calibrationCase, count int) ([]float64, error) {
	if c.measure != nil {
		return c.measure(ctx, test, count)
	}
	budget := 14 * time.Second
	warmup, window := fullSampleWarmup, fullSampleWindow
	if test.mini {
		budget = 6 * time.Second
		warmup, window = miniSampleWarmup, miniSampleWindow
	}
	trial, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	if test.resources != nil {
		trial = context.WithValue(trial, resourceKey{}, test.resources)
	}
	return measureStreams(trial, test.path, test.source, test.plan, test.threads, count, warmup, window)
}

// Cases cover every immutable fixture, including PQ/HLG at 24/30/60fps.
// The mini pass starts with the preferred working encoder; full analysis adds
// alternate APIs and CPU stages without assuming an API name proves performance.
func (c *Calibration) cases(ctx context.Context, inputs *benchmarkInputs, threads int, full bool) ([]calibrationCase, error) {
	encoder := c.Capacity.currentEncoder(ctx)
	encoders := []string{encoder}
	if c.Capacity.Hardware != nil && (encoder == EncoderIntelQSV || encoder == EncoderIntelVAAPI) {
		for _, alternative := range []string{EncoderIntelQSV, EncoderIntelVAAPI} {
			if alternative != encoder && c.Capacity.Hardware.Check(ctx, alternative, false).OK {
				encoders = append(encoders, alternative)
			}
		}
	}
	if full && encoder != "" {
		encoders = append(encoders, "")
	}
	var names []string
	for name := range inputs.files {
		names = append(names, name)
	}
	sort.Strings(names)
	var cases []calibrationCase
	for _, name := range names {
		if ctx.Err() != nil {
			break
		}
		probe, err := library.Probe(ctx, inputs.files[name])
		if err != nil {
			return nil, err
		}
		v, _ := Tracks(probe)
		class := classifySource(v)
		height := 1080
		if class == SourceSDRSmall {
			height = 480
		}
		for _, encoder := range encoders {
			outputHeight := benchmarkOutputHeight(height, v.Height, encoder, full)
			plan := Plan{Mode: "transcode", Width: outputHeight * 16 / 9 / 2 * 2, Height: outputHeight, FPS: math.Min(30, v.FrameRate), Profile: outputProfile(outputHeight), VideoBitrate: heightBitrate(outputHeight), AudioBitrate: 192000, AudioChannels: 2, Encoder: encoder}
			if full {
				plan.FPS = math.Min(60, v.FrameRate)
			}
			plan.ToneMap = IsHDRTransfer(v.ColorTransfer)
			if plan.ToneMap {
				plan.ToneMapper = "cpu"
			}
			hw := c.Capacity.Hardware
			decode := c.Capacity.HardwareDecoding != nil && c.Capacity.HardwareDecoding(ctx) && encoder != "" && hw != nil && hw.DecodeAllowed(encoder, v) && hw.DecodeReady(ctx, encoder)
			var variants []Plan
			if decode {
				plan.Decoder = "hardware"
				if plan.ToneMap {
					if v.ColorTransfer == "smpte2084" && hw.ToneMapOnGPU(ctx, encoder) && ((encoder == EncoderIntelQSV && v.HDRStaticMetadata && hw.hasFilter(ctx, "vpp_qsv")) || (encoder == EncoderIntelVAAPI && hw.hasFilter(ctx, "tonemap_vaapi"))) {
						p := plan
						p.ToneMapper = "gpu"
						variants = append(variants, p)
					}
					if hw.hasFilter(ctx, "tonemap_opencl") && encoder != EncoderNvidiaNVENC {
						p := plan
						p.ToneMapper = "opencl"
						variants = append(variants, p)
					}
				}
			}
			variants = append(variants, plan)
			if full && decode {
				p := plan
				p.Decoder = ""
				p.ToneMapper = ""
				if p.ToneMap {
					p.ToneMapper = "cpu"
				}
				variants = append(variants, p)
			}
			for _, p := range variants {
				n := threads
				if p.Decoder == "hardware" {
					n = DefaultThreads
				}
				heights := []int{AutoStartHeight}
				if full {
					heights = append(heights, AutoMinHeight, p.Height)
				}
				for _, height := range heights {
					rung := p
					rung.Height = height
					rung.Width = height * 16 / 9 / 2 * 2
					rung.FPS = math.Min(rung.FPS, 30)
					if height == p.Height {
						rung.FPS = p.FPS
					}
					rung.VideoBitrate = heightBitrate(height)
					rung.Profile = outputProfile(height)
					cases = append(cases, calibrationCase{class: class, path: inputs.files[name], source: probe, plan: rung, threads: n, full: full, mini: !full})
				}
			}
		}
	}
	return cases, nil
}

func benchmarkOutputHeight(baseHeight, sourceHeight int, encoder string, full bool) int {
	if full && encoder != "" && sourceHeight >= UHDHeight {
		return UHDHeight
	}
	return baseHeight
}

func (c *Calibration) tune(ctx context.Context, inputs *benchmarkInputs) {
	stage, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	taskID, finish := activity.Default.Start("CPU fallback tuning", "threads", len(candidatesUpTo(c.Tuner.cpuCount())))
	defer finish()
	path := inputs.files[inputName(SourceHEVCPQ, 60)]
	probe, err := library.Probe(stage, path)
	if err != nil {
		return
	}
	encoder := c.Capacity.currentEncoder(stage)
	plan := Plan{Mode: "transcode", Width: 1920, Height: 1080, FPS: 30, ToneMap: true, ToneMapper: "cpu", Encoder: encoder, Profile: "high", VideoBitrate: 12000000, AudioBitrate: 192000, AudioChannels: 2}
	var samples []TuneSample
	for index, threads := range candidatesUpTo(c.Tuner.cpuCount()) {
		activity.Default.Progress(taskID, index, len(candidatesUpTo(c.Tuner.cpuCount())))
		activity.Default.SetCurrentFile(taskID, fmt.Sprintf("%d CPU threads", threads))
		speeds, err := c.sample(stage, calibrationCase{class: SourceHEVCPQ, path: path, source: probe, plan: plan, threads: threads}, 1)
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
	p := ProfileCapacity{TargetHeight: test.plan.Height, SourceClass: test.class, Encoder: test.plan.Encoder, HardwareDecode: test.plan.Decoder == "hardware", ToneMapper: test.plan.ToneMapper, SourceWidth: video.Width, SourceHeight: video.Height, SourceFPS: video.FrameRate, Height: test.plan.Height, FPS: test.plan.FPS, Threads: test.threads}
	resources := &resourceMeasurement{}
	test.resources = resources
	speeds, err := c.sample(ctx, test, 1)
	if err != nil {
		logging.Warn("calibration %s: %v", test.class, err)
		return p
	}
	if !test.mini && test.plan.Decoder == "hardware" && len(old) == 0 && c.Tuner.cpuCount() >= 4 {
		alternate := test
		alternate.threads = 4
		alternate.resources = &resourceMeasurement{}
		if trial, err := c.sample(ctx, alternate, 1); err == nil && minimumSpeed(trial) > minimumSpeed(speeds)*1.1 {
			test = alternate
			speeds = trial
			p.Threads = 4
			resources = alternate.resources
		}
	}
	p.SingleSpeed = minimumSpeed(speeds)
	p.Speeds = speeds
	p.Verified = true
	p.MeasuredAt = time.Now()
	// Walk supported lower rungs rather than jumping from HD straight to 360p.
	for _, rung := range OutputRungs {
		if p.SingleSpeed >= CapacityRealTime {
			break
		}
		fps := math.Min(rung.FPS, test.plan.FPS)
		if rung.Height > test.plan.Height || rung.Height == test.plan.Height && fps >= test.plan.FPS {
			continue
		}
		test.plan.Height, test.plan.Width, test.plan.FPS = rung.Height, rung.Height*16/9/2*2, fps
		test.plan.VideoBitrate = heightBitrate(rung.Height)
		test.plan.Profile = outputProfile(rung.Height)
		speeds, err = c.sample(ctx, test, 1)
		if err != nil {
			return p
		}
		p.Height, p.FPS, p.SingleSpeed, p.Speeds = rung.Height, fps, minimumSpeed(speeds), speeds
	}

	p.CPUCores = resources.CPU / math.Max(minimumSpeed(speeds), .01)
	if test.plan.Encoder == "" {
		zero := float64(0)
		p.GPUEngines = &zero
	}
	if resources.GPU != nil {
		v := *resources.GPU / math.Max(minimumSpeed(speeds), .01)
		p.GPUEngines = &v
	}
	if test.full {
		confirmation, e := c.sample(ctx, test, 1)
		if e != nil || minimumSpeed(confirmation) <= 0 {
			return p
		}
		if minimumSpeed(confirmation) < minimumSpeed(speeds) {
			speeds = confirmation
		}
		p.ReferenceSSIM = c.referenceQuality(ctx, test)
	}
	p.SingleSpeed = minimumSpeed(speeds)
	p.Speeds = speeds
	p.SingleStreamSpeed = p.SingleSpeed
	if p.SingleSpeed < CapacityRealTime {
		return p
	}
	p.Streams = 1

	counts := []int{2, 4}

	for _, cached := range old {
		if test.full && sameProfile(p, cached) && cached.Verified && cached.Streams > 1 {
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
	return a.SourceClass == b.SourceClass && a.SourceFPS == b.SourceFPS && a.Encoder == b.Encoder && a.HardwareDecode == b.HardwareDecode && a.ToneMapper == b.ToneMapper && a.Height == b.Height && a.FPS == b.FPS && a.Threads == b.Threads
}

// Startup is deliberately bounded by the owning Benchmarks group's deadline.
// A valid quick or full cache satisfies startup without repeating a test.
func (c *Calibration) Startup(ctx context.Context) { c.run(ctx, false, true) }
func (c *Calibration) Quick(ctx context.Context)   { c.run(ctx, false, false) }
func (c *Calibration) Full(ctx context.Context)    { c.run(ctx, true, false) }
func (c *Calibration) run(ctx context.Context, full, startup bool) {
	if c.Capacity.Hardware != nil {
		c.Capacity.Hardware.Recheck()
	}
	stageID, finish := activity.Default.Start("Representative Capacity Validation", "pipelines", 0)
	activity.Default.SetCurrentFile(stageID, "Checking prepared media and hardware capabilities")
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
	quick, quickOK := readCalibrationCache(benchmarkCachePath(c.DataDir, "quick"), fingerprint)
	previousFull, fullOK := readCalibrationCache(benchmarkCachePath(c.DataDir, "full"), fingerprint)
	c.mu.Lock()
	c.quick, c.quickOK = quick, quickOK
	c.full, c.fullOK = previousFull, fullOK
	c.mu.Unlock()
	effective, effectiveOK := previousFull, fullOK
	mode := "quick"
	if full {
		mode = "full"
	}
	if !effectiveOK && quickOK {
		effective = quick
		effectiveOK = true
	}
	if effectiveOK {
		c.apply(effective)
	}
	if startup && effectiveOK {
		if c.Capacity.OnChange != nil {
			c.Capacity.OnChange()
		}
		return
	}
	if !full && !fullOK {
		c.Capacity.resetCalibration()
	}
	// A manual quick check observes its own result, but must not retune settings
	// already selected by a full analysis.
	threads := c.Tuner.Threads()
	if fullOK && !full {
		threads = previousFull.Threads
		c.Tuner.Load(threads, previousFull.At)
	}
	if full || !fullOK {
		c.tune(ctx, inputs)
	}
	threads = c.Tuner.Threads()
	cases, err := c.cases(ctx, inputs, threads, full)
	if err != nil {
		logging.Warn("calibration pipeline setup: %v", err)
		return
	}
	old := previousFull.Profiles
	profiles := append([]ProfileCapacity(nil), old...)
	if !full {
		old = previousFull.Profiles
		profiles = nil
		// A manual quick run replaces only the prior quick record, including
		// when it is stopped before completing every case.
		c.save(fingerprint, "quick", profiles)
		c.remember("quick", calibrationCache{Fingerprint: fingerprint, At: time.Now(), Threads: threads, Profiles: profiles})
	}
	progressID, endProgress := activity.Default.Start("Pipeline analysis", "pipelines", len(cases))
	defer endProgress()
	completed := 0
	tested := map[string]bool{}
	fixturePath := ""
	fixtureCtx := ctx
	fixtureCancel := func() {}
	for _, test := range cases {
		activity.Default.SetCurrentFile(progressID, benchmarkTestLabel(test))
		activity.Default.Progress(progressID, completed, len(cases))
		// The bounded pass tests each fixture once on its first stable path.
		key := test.path
		if !full && fixturePath != key {
			fixtureCancel()
			fixturePath = key
			fixtureCtx, fixtureCancel = context.WithTimeout(ctx, 7*time.Second)
		}
		if !full && fixtureCtx.Err() != nil {
			completed++
			continue
		}
		if !full && tested[key] {
			completed++
			continue
		}

		if effectiveOK && !full {
			video, _ := Tracks(test.source)
			for _, prior := range effective.Profiles {
				if prior.TargetHeight == test.plan.Height && prior.SourceClass == test.class && prior.SourceFPS == video.FrameRate && prior.HardwareDecode == (test.plan.Decoder == "hardware") && prior.Encoder == test.plan.Encoder && prior.ToneMapper == test.plan.ToneMapper {
					test.threads = prior.Threads
					if prior.Verified && prior.Streams > 0 {
						test.plan.Height = prior.Height
						test.plan.Width = prior.Height * 16 / 9 / 2 * 2
						test.plan.FPS = prior.FPS
						test.plan.VideoBitrate = heightBitrate(prior.Height)
						test.plan.Profile = outputProfile(prior.Height)
					}
					break
				}
			}
		}
		if ctx.Err() != nil {
			break
		}
		// At most one weak cached class is retained per identity; a completed new
		// failure supersedes it, while interruption preserves prior validated data.
		measureCtx := ctx
		if !full {
			measureCtx = fixtureCtx
		}
		result := c.evaluate(measureCtx, test, old)
		// Keep a proven smaller count if the bounded concurrent trial timed out.
		if measureCtx.Err() != nil && (!result.Verified || result.Streams == 0) {
			if ctx.Err() != nil {
				break
			}
			completed++
			continue
		}
		if result.Verified && result.Streams > 0 {
			tested[key] = true
		}
		if !result.Verified && ctx.Err() != nil {
			break
		}
		filtered := profiles[:0]
		for _, prior := range profiles {
			if prior.TargetHeight != result.TargetHeight || prior.SourceClass != result.SourceClass || prior.SourceFPS != result.SourceFPS || prior.HardwareDecode != result.HardwareDecode || prior.Encoder != result.Encoder || prior.ToneMapper != result.ToneMapper {
				filtered = append(filtered, prior)
			}
		}
		profiles = append(filtered, result)
		if len(profiles) > 0 || fullOK && full {
			c.save(fingerprint, mode, profiles)
			c.remember(mode, calibrationCache{Fingerprint: fingerprint, At: time.Now(), Threads: c.Tuner.Threads(), Profiles: profiles})
		}
		if full && (len(profiles) > 0 || fullOK) || !full && !fullOK {
			c.Capacity.publishProfiles(profiles)
		}
		completed++
		logging.Info("capacity validation: %s %s decode=%t tone=%s %dp%g: %d validated streams (slowest %.2fx, CPU %.2f cores/stream)", result.SourceClass, result.Encoder, result.HardwareDecode, result.ToneMapper, result.Height, result.FPS, result.Streams, minimumSpeed(result.Speeds), result.CPUCores)
	}
	fixtureCancel()
	activity.Default.Progress(progressID, completed, len(cases))
	if len(profiles) > 0 {
		c.save(fingerprint, mode, profiles)
		c.remember(mode, calibrationCache{Fingerprint: fingerprint, At: time.Now(), Threads: c.Tuner.Threads(), Profiles: profiles})
	}
	if len(profiles) > 0 && (full || !fullOK) {
		c.Capacity.publishProfiles(profiles)
	}
	if c.Capacity.OnChange != nil {
		c.Capacity.OnChange()
	}
}

func (c *Calibration) apply(cache calibrationCache) {
	c.Tuner.Load(cache.Threads, cache.At)
	c.Capacity.publishProfiles(cache.Profiles)
}

func (c *Calibration) remember(mode string, cache calibrationCache) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if mode == "full" {
		c.full, c.fullOK = cache, len(cache.Profiles) > 0
		return
	}
	c.quick, c.quickOK = cache, len(cache.Profiles) > 0
}

func appendBenchmarkResults(results []BenchmarkResult, mode string, profiles []ProfileCapacity) []BenchmarkResult {
	for _, p := range profiles {
		name := mode + " Test · " + benchmarkPipelineLabel(p.SourceClass, p.Encoder, p.HardwareDecode, p.ToneMapper, p.Height, p.FPS)
		multiplier := p.SingleSpeed
		if len(p.Speeds) > 0 {
			multiplier = minimumSpeed(p.Speeds)
		}
		if p.Streams > 1 {
			name += fmt.Sprintf(", %d streams", p.Streams)
		}
		results = append(results, BenchmarkResult{Name: name, Multiplier: multiplier, Passed: p.Verified && p.Streams > 0})
	}
	return results
}

func benchmarkTestLabel(test calibrationCase) string {
	return benchmarkPipelineLabel(test.class, test.plan.Encoder, test.plan.Decoder == "hardware", test.plan.ToneMapper, test.plan.Height, test.plan.FPS)
}

func benchmarkPipelineLabel(class SourceClass, encoder string, hardwareDecode bool, toneMapper string, height int, fps float64) string {
	device, driver := "CPU", "FFmpeg software"
	switch encoder {
	case EncoderIntelQSV:
		device, driver = "Intel GPU", "Quick Sync"
	case EncoderIntelVAAPI:
		device, driver = "Intel GPU", "VAAPI"
	case EncoderAMDVAAPI:
		device, driver = "AMD GPU", "VAAPI"
	case EncoderNvidiaNVENC:
		device, driver = "NVIDIA GPU", "NVENC"
	}
	decode := "CPU decode"
	if hardwareDecode {
		decode = "hardware decode"
	}
	tone := "no tone map"
	if toneMapper != "" {
		tone = toneMapper + " tone map"
	}
	return fmt.Sprintf("%s/%s - %s, %s, %s, %dp at %.0ffps", device, driver, class, decode, tone, height, fps)
}

func (c *Calibration) recommendFullLocked() bool {
	if !c.quickOK || !c.fullOK || len(c.quick.Profiles) == 0 || len(c.full.Profiles) == 0 {
		return false
	}
	if !c.quick.At.After(c.full.At) {
		return false
	}
	for _, quick := range c.quick.Profiles {
		matched := false
		for _, full := range c.full.Profiles {
			if quick.TargetHeight != full.TargetHeight || !samePipeline(quick, full) {
				continue
			}
			matched = true
			if quick.Verified != full.Verified || quick.Height != full.Height || quick.FPS != full.FPS {
				return true
			}
			quickSpeed := quick.SingleStreamSpeed
			if quickSpeed == 0 && quick.Streams == 1 {
				quickSpeed = quick.SingleSpeed
			}
			fullSpeed := full.SingleStreamSpeed
			// Cache entries written before this field existed cannot be used for
			// a valid one-stream speed comparison.
			if quickSpeed > 0 && fullSpeed > 0 && math.Abs(quickSpeed-fullSpeed)/fullSpeed >= .25 {
				return true
			}
			break
		}
		// A quick result without a corresponding full pipeline is not comparable.
		if !matched {
			continue
		}
	}
	return false
}

func samePipeline(a, b ProfileCapacity) bool {
	return a.SourceClass == b.SourceClass && a.SourceFPS == b.SourceFPS &&
		a.Encoder == b.Encoder && a.HardwareDecode == b.HardwareDecode &&
		a.ToneMapper == b.ToneMapper && a.Threads == b.Threads
}

const unmeasuredProcessLimit = 2

func (c *Capacity) resetCalibration() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calibrated = true
	c.profiles = nil
	c.hdr = nil
	c.status.Samples = nil
	c.status.Processes = unmeasuredProcessLimit
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
		if p.TargetHeight == AutoStartHeight && p.Height == AutoStartHeight {
			c.status.Processes = max(c.status.Processes, p.Streams)
		}
		sample := RungSample{SourceClass: p.SourceClass, Height: p.Height, FPS: p.FPS, Speed: p.SingleSpeed}
		if p.HardwareDecode && p.ToneMapper != "" {
			record := HDRCapacity{SourceFPS: p.SourceFPS, Encoder: p.Encoder, HardwareDecode: true, Transfer: "smpte2084", ToneMapper: p.ToneMapper, Threads: p.Threads, Samples: []RungSample{sample}, MeasuredAt: p.MeasuredAt}
			if p.SourceClass == SourceHEVCHLG {
				record.Transfer = "arib-std-b67"
			}
			found := false
			for i, old := range c.hdr {
				if old.Transfer == record.Transfer && old.SourceFPS == record.SourceFPS && old.Threads == record.Threads && old.Encoder == record.Encoder && old.ToneMapper == record.ToneMapper {
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
	total := 0
	demand := 0.0
	unmatchedLimit := 1
	if len(profiles) == 0 {
		unmatchedLimit = unmeasuredProcessLimit
	}
	for _, current := range append(append([]Job(nil), active...), job) {
		cost := jobResourceCost(current)
		if cost == 0 {
			continue
		}
		total += cost
		var selected *ProfileCapacity
		for _, p := range profiles {
			if profileMatches(p, current) && (selected == nil || (p.Height < selected.Height || p.Height == selected.Height && p.SourceFPS < selected.SourceFPS)) {
				copy := p
				selected = &copy
			}
		}
		if selected == nil {
			demand += float64(cost) / float64(unmatchedLimit)
		} else {
			if selected.Streams == 0 {
				return true, ErrCapacityUnsustainable
			}
			demand += float64(cost) / float64(selected.Streams)
		}

	}
	if total > MaxParallel || demand > 1.000001 {
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

// PreferredPipeline ranks only stable, real-time profiles for a matching source.
// CPU and GPU figures are normalized to one real-time stream. Missing GPU/SSIM
// telemetry is never treated as zero load or perfect fidelity.
func (c *Capacity) PreferredPipeline(source library.ProbeResult, encoder string, height int) (ProfileCapacity, bool) {
	if c == nil {
		return ProfileCapacity{}, false
	}
	v, _ := Tracks(source)
	c.mu.Lock()
	profiles := append([]ProfileCapacity(nil), c.profiles...)
	c.mu.Unlock()
	if height <= 0 {
		height = 1080
	}
	height = min(height, v.Height)
	var best ProfileCapacity
	found := false
	for _, p := range profiles {
		if c.Hardware != nil && p.HardwareDecode && !c.Hardware.DecodeAllowed(p.Encoder, v) {
			continue
		}
		if c.Hardware != nil && (p.ToneMapper == "gpu" || p.ToneMapper == "opencl") && !c.Hardware.ToneMapOnGPU(context.Background(), p.Encoder) {
			continue
		}
		if p.Encoder == EncoderIntelQSV && p.ToneMapper == "gpu" && !v.HDRStaticMetadata {
			continue
		}
		sameVendor := p.Encoder == encoder || (encoder == EncoderIntelQSV || encoder == EncoderIntelVAAPI) && (p.Encoder == EncoderIntelQSV || p.Encoder == EncoderIntelVAAPI)
		if !sameVendor || !p.Verified || p.Streams < 1 || p.SingleSpeed < CapacityRealTime || p.SourceClass != classifySource(v) || p.SourceFPS+.01 < v.FrameRate || v.Width > p.SourceWidth || v.Height > p.SourceHeight {
			continue
		}
		if !found || betterPipelineAtHeight(p, best, height) {
			best = p
			found = true
		}
	}
	return best, found
}
func betterPipelineAtHeight(a, b ProfileCapacity, height int) bool {
	aFits, bFits := a.Height >= height, b.Height >= height
	if aFits != bFits {
		return aFits
	}
	if a.Height != b.Height {
		if aFits {
			return a.Height < b.Height
		}
		return a.Height > b.Height
	}
	return betterPipeline(a, b)
}

func betterPipeline(a, b ProfileCapacity) bool {
	// Small measurement variations should not cause pipeline flapping.
	if math.Abs(a.CPUCores-b.CPUCores) > .05 {
		return a.CPUCores < b.CPUCores
	}
	if a.GPUEngines != nil && b.GPUEngines != nil && math.Abs(*a.GPUEngines-*b.GPUEngines) > .05 {
		return *a.GPUEngines < *b.GPUEngines
	}
	if a.Height != b.Height {
		return a.Height > b.Height
	}
	if a.FPS != b.FPS {
		return a.FPS > b.FPS
	}
	if a.ReferenceSSIM != nil && b.ReferenceSSIM != nil && math.Abs(*a.ReferenceSSIM-*b.ReferenceSSIM) > .005 {
		return *a.ReferenceSSIM > *b.ReferenceSSIM
	}
	if a.Streams != b.Streams {
		return a.Streams > b.Streams
	}
	return false
}
