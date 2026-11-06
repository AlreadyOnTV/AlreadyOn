package playback

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/activity"
	"github.com/alreadyonapp/alreadyon/server/internal/governor"
	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/logging"
)

// CapacityRealTime is how many times real time a conversion must run to count
// as sustainable: the margin keeps a stream ahead of its viewer.
const CapacityRealTime = 1.3

// MaxParallel bounds the detected number of parallel conversions.
const MaxParallel = 4

var (
	ErrCapacityMeasurementUnavailable = errors.New("playback: no current server capacity measurement is available for this video pipeline")
	ErrCapacityUnsustainable          = errors.New("playback: server capacity measurements show this conversion cannot sustain the selected output")
)

// memoryHeadroom leaves resources for the OS and non-playback server work.
const memoryHeadroom = 0.8

// perConversionMemory is a conservative host-RAM reservation per process.
const perConversionMemory = 512 << 20

const unmeasuredPipelineSpeed = CapacityRealTime

// SourceClass identifies representative decode/filter workloads. PQ and HLG
// are separate because their tone-mapping paths differ.
type SourceClass string

const (
	SourceHEVCPQ   SourceClass = "hevc-main10-4k-pq"
	SourceHEVCHLG  SourceClass = "hevc-main10-4k-hlg"
	SourceHEVCSDR  SourceClass = "hevc-sdr-1080p"
	SourceH264HD   SourceClass = "h264-sdr-1080p"
	SourceSDRSmall SourceClass = "h264-sdr-480p"
)

type capacitySource struct {
	class       SourceClass
	width       int
	height      int
	fps         float64
	bitrate     int64
	codec       string
	profile     string
	pixelFormat string
	transfer    string
}

var capacitySources = []capacitySource{
	{SourceHEVCPQ, 3840, 2160, 60, 40000000, "hevc", "Main 10", "yuv420p10le", "smpte2084"},
	{SourceHEVCHLG, 3840, 2160, 60, 40000000, "hevc", "Main 10", "yuv420p10le", "arib-std-b67"},
	{SourceHEVCSDR, 1920, 1080, 30, 6000000, "hevc", "Main", "yuv420p", ""},
	{SourceH264HD, 1920, 1080, 60, 8000000, "h264", "High", "yuv420p", ""},
	{SourceSDRSmall, 854, 480, 30, 1000000, "h264", "Main", "yuv420p", ""},
}

var capacityRungs = func() []RungSample {
	rungs := make([]RungSample, 0, len(OutputRungs))
	for _, rung := range OutputRungs {
		rungs = append(rungs, RungSample{Height: rung.Height, FPS: rung.FPS})
	}
	return rungs
}()

// RungSample is how fast one output size converted: Speed is times real time.
type RungSample struct {
	SourceClass SourceClass `json:"sourceClass,omitempty"`
	Height      int         `json:"height"`
	FPS         float64     `json:"fps"`
	Speed       float64     `json:"speed,omitempty"`
}

// CapacityRecord is what survives a restart.
type CapacityRecord struct {
	MeasuredAt  time.Time
	Encoder     string
	Samples     []RungSample
	MaxHeight   int
	MaxFPS      float64
	Processes   int
	ProcessesAt time.Time
}

// CapacityStatus is the capacity API's answer.
type CapacityStatus struct {
	State       string        `json:"state"` // idle or running
	MeasuredAt  *time.Time    `json:"measuredAt"`
	Encoder     string        `json:"encoder"`
	Samples     []RungSample  `json:"samples"`
	MaxHeight   int           `json:"maxHeight"` // 0: no limit (hardware encoder, or not measured yet)
	MaxFPS      float64       `json:"maxFps"`
	Processes   int           `json:"processes"` // detected parallel conversions; 0 until known
	ProcessesAt *time.Time    `json:"processesAt"`
	CPUs        int           `json:"cpus"`
	Detail      string        `json:"detail"`
	HDR         []HDRCapacity `json:"hdr,omitempty"`
}

// Capacity measures what this server can sustain and shares it with every
// viewer: the largest picture software encoding keeps ahead of real time (the
// Auto quality never goes above it) and how many conversions can run at once.
// The speed measurement is slow, so it runs at start, when the encoder setting
// changes and once an hour; the parallel limit is recomputed from it, the CPU
// count and the free memory on its own, cheaper schedule.
type Capacity struct {
	// Context ends measurements when the server shuts down; nil never does.
	Context    context.Context
	Executable string
	Hardware   *Hardware
	// Encoder is the hardware encoder setting ("" for software).
	Encoder func(context.Context) string
	// HardwareDecoding selects the full pipeline for the HDR benchmark.
	HardwareDecoding func(context.Context) bool
	// Threads is the CPU thread count one conversion uses.
	Threads func() int
	// Busy reports running conversions, which would distort a measurement.
	Busy func() bool
	// TunerRunning reports a thread-count measurement, which this one waits for.
	TunerRunning func() bool
	// Save keeps the result across restarts; OnChange follows every update.
	Save     func(context.Context, CapacityRecord)
	OnChange func()
	// measure and memory are replaced in tests.
	measure func(ctx context.Context, source SourceClass, rung RungSample, threads int, encoder, sample string) (float64, error)
	memory  func() uint64
	cpus    int

	mu      sync.Mutex
	status  CapacityStatus
	started bool
	hdr     []HDRCapacity
}

// Load restores the last result before the first measurement.
func (c *Capacity) Load(r CapacityRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r.MeasuredAt.IsZero() && r.Processes == 0 && len(r.Samples) == 0 {
		return
	}
	s := &c.status
	s.Encoder, s.Samples, s.MaxHeight, s.MaxFPS, s.Processes = r.Encoder, append([]RungSample(nil), r.Samples...), r.MaxHeight, r.MaxFPS, min(r.Processes, MaxParallel)
	if !r.MeasuredAt.IsZero() {
		s.MeasuredAt = &r.MeasuredAt
	}
	if !r.ProcessesAt.IsZero() {
		s.ProcessesAt = &r.ProcessesAt
	}
}

func (c *Capacity) Status() CapacityStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.status
	if s.State == "" {
		s.State = "idle"
	}
	s.CPUs = c.cpuCount()
	s.Samples = append([]RungSample(nil), s.Samples...)
	s.HDR = append([]HDRCapacity(nil), c.hdr...)
	for i := range s.HDR {
		s.HDR[i].Samples = append([]RungSample(nil), s.HDR[i].Samples...)
	}
	return s
}

func (c *Capacity) cpuCount() int {
	if c.cpus > 0 {
		return c.cpus
	}
	return min(runtime.NumCPU(), runtime.GOMAXPROCS(0))
}

// Limit is the tallest, fastest software output the server sustains, for the
// encoder now in use. Zero height means no limit: a hardware encoder, a
// measurement for a different encoder, or nothing measured yet.
func (c *Capacity) Limit(encoder string) (height int, fps float64) {
	if c == nil {
		return 0, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if encoder != "" || c.status.Encoder != "" || c.status.MaxHeight <= 0 {
		return 0, 0
	}
	return c.status.MaxHeight, c.status.MaxFPS
}

// LimitFor returns the measured software ceiling for a source workload. An
// unmeasured class does not inherit a more optimistic result from another codec.
func (c *Capacity) LimitFor(encoder string, source library.StreamInfo) (height int, fps float64, measured, sustainable bool) {
	if c == nil || encoder != "" {
		return 0, 0, false, true
	}
	class := classifySource(source)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.status.Encoder != encoder {
		return 0, 0, false, false
	}
	var samples []RungSample
	for _, sample := range c.status.Samples {
		if sample.SourceClass == class {
			samples = append(samples, sample)
		}
	}
	if len(samples) == 0 {
		return 0, 0, false, false
	}
	return pickSourceRung(samples)
}

// Allows admits a conversion only when the measured CPU and host-memory budget
// can cover the active mix at each stream's selected output rung.
func (c *Capacity) Allows(active []Job, candidate Job) bool {
	return c.AdmissionError(active, candidate) == nil
}

// AdmissionError distinguishes missing/unsafe measurements from an otherwise
// valid pipeline that is temporarily out of aggregate CPU or memory headroom.
func (c *Capacity) AdmissionError(active []Job, candidate Job) error {
	if c == nil || jobResourceCost(candidate) == 0 {
		return nil
	}
	c.mu.Lock()
	encoder := c.status.Encoder
	samples := append([]RungSample(nil), c.status.Samples...)
	hdr := append([]HDRCapacity(nil), c.hdr...)
	c.mu.Unlock()
	hasSourceMeasurements := false
	for _, sample := range samples {
		if sample.SourceClass != "" {
			hasSourceMeasurements = true
			break
		}
	}
	if !hasSourceMeasurements {
		return nil
	}
	free := memoryAvailable()
	if c.memory != nil {
		free = c.memory()
	}
	var cpuDemand float64
	totalSlots := 0
	unknownPipelines := 0
	unknownSlots := 0
	jobs := append(append([]Job(nil), active...), candidate)
	for _, job := range jobs {
		cost := jobResourceCost(job)
		if cost == 0 {
			continue
		}
		threads := job.Threads
		if threads < 1 {
			threads = DefaultThreads
			if c.Threads != nil {
				threads = max(1, c.Threads())
			}
		}
		speed, measured := 0.0, false
		if job.Plan.Encoder == encoder {
			speed, measured = hdrSpeedForJob(hdr, job, threads)
			if !measured {
				video, _ := Tracks(job.Source)
				if !(IsHDRTransfer(video.ColorTransfer) && job.Plan.ToneMap && (job.Plan.Decoder == "hardware" || job.Plan.ToneMapper == "gpu")) {
					speed, measured = sourceSpeedForJob(samples, job)
				}
			}
		}
		if !measured {
			unknownPipelines++
			unknownSlots += cost
			speed = unmeasuredPipelineSpeed
		}
		if speed < CapacityRealTime {
			return ErrCapacityUnsustainable
		}
		totalSlots += cost
		cpuDemand += float64(threads*cost) / speed
	}
	if unknownSlots > unknownPipelines {
		return ErrBusy
	}
	if totalSlots > MaxParallel || cpuDemand > 0.8*float64(c.cpuCount()) {
		return ErrBusy
	}
	if free == 0 {
		if totalSlots > 1 {
			return ErrBusy
		}
		return nil
	}
	if float64(totalSlots*perConversionMemory) > float64(free)*memoryHeadroom {
		return ErrBusy
	}
	return nil
}

// Processes is the detected number of parallel conversions, or 0 when unknown.
func (c *Capacity) Processes() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status.Processes
}

// Start measures in the background. It does nothing when a measurement is
// already running, and (unless force) when a stream is playing.
func (c *Capacity) Start(force bool) bool {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return false
	}
	if !force && c.Busy != nil && c.Busy() {
		c.status.Detail = "Skipped while a stream is playing; it runs again at the next hour."
		c.mu.Unlock()
		return false
	}
	c.started = true
	c.status.State = "running"
	c.status.Detail = ""
	c.mu.Unlock()
	base := c.Context
	if base == nil {
		base = context.Background()
	}
	go func() {
		ctx, cancel := measurementContext(base, force, c.Busy)
		defer cancel()
		c.run(ctx)
	}()
	return true
}

func (c *Capacity) run(ctx context.Context) {
	_, finish := activity.Default.Start("Server Capacity Measurement", "benchmarking", 0)
	defer finish()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	for c.TunerRunning != nil && c.TunerRunning() && ctx.Err() == nil {
		time.Sleep(time.Second)
	}
	encoder := c.currentEncoder(ctx)
	samples, err := c.benchmark(ctx, encoder)
	if err != nil || len(samples) == 0 {
		if err == nil {
			err = fmt.Errorf("capacity benchmark returned no samples")
		}
		c.mu.Lock()
		c.started = false
		c.status.State = "idle"
		detail := fmt.Sprintf("The measurement failed, so the previous limits stay in use: %v", err)
		c.status.Detail = detail
		c.mu.Unlock()
		logging.Warn("server capacity: %s", detail)
		return
	}

	// Keep the familiar status ceiling tied to the typical 1080p H.264 source.
	now := time.Now()
	c.mu.Lock()
	s := &c.status
	s.MeasuredAt, s.Encoder, s.Samples = &now, encoder, samples
	c.hdr = nil
	s.MaxHeight, s.MaxFPS = pickRung(encoder, samples, SourceH264HD)
	var details []string
	if encoder == "" && s.MaxHeight > 0 && s.MaxHeight < capacityRungs[0].Height {
		_, _, _, sustainable := pickSourceRung(sourceSamples(samples, SourceH264HD))
		if sustainable {
			details = append(details, fmt.Sprintf("Software encoding keeps up with %dp%g at best for 1080p H.264 SDR.", s.MaxHeight, s.MaxFPS))
		} else {
			details = append(details, "Software encoding could not sustain the 360p30 fallback for 1080p H.264 SDR.")
		}
	}
	for _, class := range []SourceClass{SourceHEVCPQ, SourceHEVCHLG} {
		_, _, measured, sustainable := pickSourceRung(sourceSamples(samples, class))
		if measured && !sustainable {
			details = append(details, fmt.Sprintf("%s cannot sustain 360p30; conversion for that source class is disabled.", class))
		}
	}
	s.Detail = strings.Join(details, " ")
	c.mu.Unlock()
	logging.Info("server capacity: %s (%s)", c.describe(), describeRungs(samples))
	c.RecomputeProcesses(ctx)

	// The source matrix includes the software HDR pipelines. The optional HDR
	// pass is still needed for a configured GPU decoder/tone mapper.
	if c.measure == nil && encoder != "" && c.HardwareDecoding != nil && c.HardwareDecoding(ctx) {
		hdr, hdrErr := c.benchmarkHDR(ctx, encoder)
		if hdrErr != nil {
			logging.Warn("HDR capacity measurement unavailable: %v", hdrErr)
		} else {
			c.mu.Lock()
			c.hdr = hdr
			c.mu.Unlock()
		}
	}

	c.mu.Lock()
	c.started = false
	c.status.State = "idle"
	c.mu.Unlock()
}

func (c *Capacity) describe() string {
	s := c.Status()
	if s.Encoder != "" {
		return fmt.Sprintf("%s, no picture-size limit", EncoderLabel(s.Encoder))
	}
	return fmt.Sprintf("software sustains up to %dp%g", s.MaxHeight, s.MaxFPS)
}

func describeRungs(samples []RungSample) string {
	var parts []string
	for _, s := range samples {
		parts = append(parts, fmt.Sprintf("%dp%g %.2fx", s.Height, s.FPS, s.Speed))
	}
	return strings.Join(parts, ", ")
}

// currentEncoder is the hardware encoder when it is set and works, else "".
func (c *Capacity) currentEncoder(ctx context.Context) string {
	encoder := ""
	if c.Encoder != nil {
		encoder = c.Encoder(ctx)
	}
	if !IsHardwareEncoder(encoder) || (c.Hardware != nil && !c.Hardware.Check(ctx, encoder, false).OK) {
		return ""
	}
	return encoder
}

// pickRung chooses the best sustained software rung, or none for hardware.
func pickRung(encoder string, samples []RungSample, sourceClass ...SourceClass) (height int, fps float64) {
	if encoder != "" {
		return 0, 0
	}
	class := SourceH264HD
	if len(sourceClass) > 0 {
		class = sourceClass[0]
	}
	var selected []RungSample
	for _, sample := range samples {
		if sample.SourceClass == class || (sample.SourceClass == "" && len(sourceClass) == 0) {
			selected = append(selected, sample)
		}
	}
	height, fps, _, _ = pickSourceRung(selected)
	return height, fps
}

func (c *Capacity) benchmark(ctx context.Context, encoder string) ([]RungSample, error) {
	dir, err := os.MkdirTemp("", "alreadyon-capacity-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	exe := c.Executable
	if exe == "" {
		exe = "ffmpeg"
	}
	measure := c.measure
	if measure == nil {
		measure = func(ctx context.Context, source SourceClass, rung RungSample, threads int, encoder, sample string) (float64, error) {
			return measureRung(ctx, exe, dir, source, rung, threads, encoder, sample)
		}
	}
	threads := DefaultThreads
	if c.Threads != nil {
		threads = c.Threads()
	}
	var samples []RungSample
	for _, source := range capacitySources {
		sample := "synthetic:" + string(source.class)
		if c.measure == nil {
			var err error
			sample, err = makeCapacitySample(ctx, exe, dir, source)
			if err != nil {
				return samples, err
			}
		}
		for _, target := range OutputRungs {
			if target.Height > source.height {
				continue
			}
			rung := RungSample{SourceClass: source.class, Height: target.Height, FPS: min(target.FPS, source.fps)}
			speed, err := measure(ctx, source.class, rung, threads, encoder, sample)
			if err != nil {
				return samples, err
			}
			rung.Speed = speed
			samples = append(samples, rung)
		}
	}
	return samples, nil
}

// RecomputeProcesses detects how many conversions can run at once from the
// measured speed, the CPU count and the free memory, and applies it.
func (c *Capacity) RecomputeProcesses(ctx context.Context) {
	encoder := c.currentEncoder(ctx)
	c.mu.Lock()
	s := &c.status
	speed := sustainedSpeed(s.Samples, s.MaxHeight, s.MaxFPS)
	if len(s.Samples) == 0 || s.Encoder != encoder || speed <= 0 {
		c.mu.Unlock() // nothing measured for this encoder yet: keep what is in use
		return
	}
	threads := DefaultThreads
	if c.Threads != nil {
		threads = max(1, c.Threads())
	}
	free := uint64(0)
	if c.memory != nil {
		free = c.memory()
	} else {
		free = memoryAvailable()
	}
	n, measuredClasses := 0, 0
	for _, source := range capacitySources {
		for _, sample := range s.Samples {
			if sample.SourceClass != source.class {
				continue
			}
			measuredClasses++
			if sample.Speed >= CapacityRealTime {
				n = max(n, DetectProcesses(sample.Speed, c.cpuCount(), threads, free, encoder))
			}
		}
	}
	if measuredClasses == 0 {
		n = DetectProcesses(speed, c.cpuCount(), threads, free, encoder)
	} else if n == 0 {
		n = 1
	}
	now := time.Now()
	s.Processes, s.ProcessesAt = n, &now
	record := c.recordLocked()
	c.mu.Unlock()
	logging.Info("parallel conversions: %d (%.2fx real time on %d CPUs, %d threads each)", n, speed, c.cpuCount(), threads)
	if c.Save != nil {
		c.Save(ctx, record)
	}
	if c.OnChange != nil {
		c.OnChange()
	}
}

func (c *Capacity) recordLocked() CapacityRecord {
	s := c.status
	r := CapacityRecord{Encoder: s.Encoder, Samples: append([]RungSample(nil), s.Samples...), MaxHeight: s.MaxHeight, MaxFPS: s.MaxFPS, Processes: s.Processes}
	if s.MeasuredAt != nil {
		r.MeasuredAt = *s.MeasuredAt
	}
	if s.ProcessesAt != nil {
		r.ProcessesAt = *s.ProcessesAt
	}
	return r
}

// sustainedSpeed is the measured speed at the rung that sets the limit (the
// only rung for a hardware encoder).
func sustainedSpeed(samples []RungSample, height int, fps float64) float64 {
	for _, s := range samples {
		if height == 0 || (s.Height == height && s.FPS == fps) {
			return s.Speed
		}
	}
	return 0
}

// DetectProcesses estimates sustainable simultaneous conversions from measured
// full-pipeline speed, host CPU headroom, and a conservative memory reservation.
func DetectProcesses(speed float64, cpus, threads int, freeMemory uint64, encoder string) int {
	const cpuHeadroom = 0.8
	n := int(cpuHeadroom * float64(max(cpus, 1)) * speed / float64(max(threads, 1)))
	if freeMemory == 0 {
		n = min(n, 1)
	} else {
		n = min(n, int(float64(freeMemory)*memoryHeadroom/perConversionMemory))
	}
	return max(1, min(n, MaxParallel))
}

// EffectiveSettings applies the detected parallel-conversion limit unless the
// administrator chose a number.
func EffectiveSettings(s Settings, c *Capacity) Settings {
	if s.AutoProcesses {
		if n := c.Processes(); n > 0 {
			s.MaxProcesses = n
		}
	}
	s.MaxProcesses = min(max(s.MaxProcesses, 1), MaxParallel)
	return s
}

// memoryAvailable reads MemAvailable from /proc/meminfo; 0 when unknown.
func memoryAvailable() uint64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if rest, ok := strings.CutPrefix(sc.Text(), "MemAvailable:"); ok {
			fields := strings.Fields(rest)
			if len(fields) > 0 {
				kb, _ := strconv.ParseUint(fields[0], 10, 64)
				return kb << 10
			}
		}
	}
	return 0
}

// makeCapacitySample creates a deterministic synthetic source in an isolated
// temporary directory. HDR10+ dynamic metadata is intentionally not fabricated:
// the fixture represents Main 10 PQ/HLG transfer and BT.2020 color only.
func makeCapacitySample(ctx context.Context, exe, dir string, source capacitySource) (string, error) {
	path := filepath.Join(dir, string(source.class)+".mkv")
	videoFilter := fmt.Sprintf("noise=alls=3:allf=t:all_seed=1,format=%s", source.pixelFormat)
	if source.transfer != "" {
		videoFilter = fmt.Sprintf("setparams=color_primaries=bt2020:color_trc=%s:colorspace=bt2020nc:range=tv,%s", source.transfer, videoFilter)
	}
	base := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-f", "lavfi", "-i",
		fmt.Sprintf("testsrc2=size=%dx%d:rate=%g:duration=%d,%s", source.width, source.height, source.fps, tuneSeconds, videoFilter),
		"-f", "lavfi", "-i", fmt.Sprintf("anullsrc=channel_layout=stereo:sample_rate=48000:duration=%d", tuneSeconds),
		"-map", "0:v:0", "-map", "1:a:0", "-shortest"}
	if source.transfer != "" {
		base = append(base, "-color_primaries", "bt2020", "-color_trc", source.transfer, "-colorspace", "bt2020nc")
	}
	if source.codec == "hevc" {
		profile := strings.ToLower(strings.ReplaceAll(source.profile, " ", ""))
		base = append(base, "-c:v", "libx265", "-preset", "ultrafast", "-b:v", strconv.FormatInt(source.bitrate, 10), "-maxrate", strconv.FormatInt(source.bitrate, 10), "-bufsize", strconv.FormatInt(source.bitrate*2, 10), "-profile:v", profile,
			"-x265-params", "pools=2:frame-threads=2:log-level=error")
	} else {
		base = append(base, "-c:v", "libx264", "-preset", "ultrafast", "-b:v", strconv.FormatInt(source.bitrate, 10), "-maxrate", strconv.FormatInt(source.bitrate, 10), "-bufsize", strconv.FormatInt(source.bitrate*2, 10), "-profile:v", strings.ToLower(source.profile))
	}
	base = append(base, "-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-ac", "2", path)
	if out, err := governor.BackgroundOutput(ctx, lowPriority(ctx, exe, base...)); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("%s fixture: %v %s", source.class, err, strings.TrimSpace(string(out)))
	}
	return path, nil
}

// measureRung measures source decode, server filters, video encode and AAC audio
// encode as one complete playback conversion, without real-time pacing.
func measureRung(ctx context.Context, exe, dir string, sourceClass SourceClass, rung RungSample, threads int, encoder, sample string) (float64, error) {
	probe, err := library.Probe(ctx, sample)
	if err != nil {
		return 0, err
	}
	v, _ := Tracks(probe)
	scale := min(1, float64(rung.Height)/float64(v.Height))
	w := max(2, int(float64(v.Width)*scale)/2*2)
	h := max(2, int(float64(v.Height)*scale)/2*2)
	plan := Plan{Mode: "transcode", Width: w, Height: h, FPS: rung.FPS, VideoBitrate: heightBitrate(h), AudioBitrate: 192000, AudioChannels: 2, Profile: outputProfile(h), Encoder: encoder}
	if sourceClass == SourceHEVCPQ || sourceClass == SourceHEVCHLG {
		plan.ToneMap, plan.ToneMapper = true, "cpu"
	}
	return timePlan(ctx, exe, dir, sample, probe, plan, threads)
}

func outputProfile(height int) string {
	for i := len(OutputRungs) - 1; i >= 0; i-- {
		if height <= OutputRungs[i].Height {
			return OutputRungs[i].Profile
		}
	}
	return OutputRungs[0].Profile
}

func classifySource(source library.StreamInfo) SourceClass {
	switch source.ColorTransfer {
	case "smpte2084":
		return SourceHEVCPQ
	case "arib-std-b67":
		return SourceHEVCHLG
	}
	if source.Codec == "hevc" && !IsHDRTransfer(source.ColorTransfer) {
		return SourceHEVCSDR
	}
	if source.Codec == "h264" && source.Height >= 720 && !IsHDRTransfer(source.ColorTransfer) {
		return SourceH264HD
	}
	return SourceSDRSmall
}

func pickSourceRung(samples []RungSample) (height int, fps float64, measured, sustainable bool) {
	if len(samples) == 0 {
		return 0, 0, false, false
	}
	for _, sample := range samples {
		if sample.Speed >= CapacityRealTime {
			return sample.Height, sample.FPS, true, true
		}
	}
	last := samples[len(samples)-1]
	return last.Height, last.FPS, true, false
}

func sustainedSourceSpeed(samples []RungSample, class SourceClass, height int, fps float64) float64 {
	for _, sample := range samples {
		if sample.SourceClass == class && sample.Height == height && sample.FPS == fps {
			return sample.Speed
		}
	}
	return 0
}

func sourceSamples(samples []RungSample, class SourceClass) []RungSample {
	var selected []RungSample
	for _, sample := range samples {
		if sample.SourceClass == class {
			selected = append(selected, sample)
		}
	}
	return selected
}

func sourceSpeedForJob(samples []RungSample, job Job) (float64, bool) {
	video, _ := Tracks(job.Source)
	class := classifySource(video)
	height, fps := 0, 0.0
	var speed float64
	for _, sample := range samples {
		if sample.SourceClass != class || sample.Height < job.Plan.Height || sample.FPS+0.01 < job.Plan.FPS {
			continue
		}
		if height == 0 || sample.Height < height || (sample.Height == height && sample.FPS < fps) {
			height, fps, speed = sample.Height, sample.FPS, sample.Speed
		}
	}
	return speed, height > 0
}

func hdrSpeedForJob(records []HDRCapacity, job Job, threads int) (float64, bool) {
	video, _ := Tracks(job.Source)
	if !job.Plan.ToneMap || video.Codec != "hevc" || video.PixelFormat != "yuv420p10le" || !IsHDRTransfer(video.ColorTransfer) || video.FrameRate <= 0 || video.FrameRate > 60 || video.Width > 3840 || video.Height > 2160 {
		return 0, false
	}
	decode := job.Plan.Decoder == "hardware"
	var selected *HDRCapacity
	for i := range records {
		record := &records[i]
		if record.Encoder != job.Plan.Encoder || record.HardwareDecode != decode || record.Transfer != video.ColorTransfer || record.ToneMapper != job.Plan.ToneMapper || record.Threads != threads || record.SourceFPS < video.FrameRate || time.Since(record.MeasuredAt) > 2*time.Hour {
			continue
		}
		if selected == nil || record.SourceFPS < selected.SourceFPS {
			selected = record
		}
	}
	if selected == nil {
		return 0, false
	}
	height, fps := 0, 0.0
	var speed float64
	for _, sample := range selected.Samples {
		if sample.Height < job.Plan.Height || sample.FPS+0.01 < job.Plan.FPS {
			continue
		}
		if height == 0 || sample.Height < height || (sample.Height == height && sample.FPS < fps) {
			height, fps, speed = sample.Height, sample.FPS, sample.Speed
		}
	}
	return speed, height > 0
}

func (c *Capacity) limitForClassLocked(class SourceClass, encoder string) (height int, fps float64, measured, sustainable bool) {
	var samples []RungSample
	for _, sample := range c.status.Samples {
		if sample.SourceClass == class {
			samples = append(samples, sample)
		}
	}
	if encoder != "" && len(samples) > 0 {
		return samples[0].Height, samples[0].FPS, true, true
	}
	return pickSourceRung(samples)
}
