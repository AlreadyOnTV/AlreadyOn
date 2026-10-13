package playback

import (
	"bufio"
	"context"
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
const MaxParallel = 16

// nvencSessions is the most NVENC sessions assumed usable: consumer drivers
// limit them and the limit is not queryable.
const nvencSessions = 8

// perConversionMemory is the RAM one software conversion is assumed to need.
const perConversionMemory = 400 << 20

// capacityRungs are the software output sizes tried, best first.
var capacityRungs = []RungSample{{Height: 1080, FPS: 60}, {Height: 1080, FPS: 30}, {Height: 720, FPS: 60}, {Height: 720, FPS: 30}, {Height: 480, FPS: 30}, {Height: 360, FPS: 30}}

// RungSample is how fast one output size converted: Speed is times real time.
type RungSample struct {
	Height int     `json:"height"`
	FPS    float64 `json:"fps"`
	Speed  float64 `json:"speed,omitempty"`
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
	measure func(ctx context.Context, rung RungSample, threads int, encoder, sample string) (float64, error)
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
	if r.MeasuredAt.IsZero() && r.Processes == 0 {
		return
	}
	s := &c.status
	s.Encoder, s.Samples, s.MaxHeight, s.MaxFPS, s.Processes = r.Encoder, append([]RungSample(nil), r.Samples...), r.MaxHeight, r.MaxFPS, r.Processes
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
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
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

	// Save SDR capacity before the optional HDR matrix, which can take several minutes.
	now := time.Now()
	c.mu.Lock()
	s := &c.status
	s.MeasuredAt, s.Encoder, s.Samples = &now, encoder, samples
	c.hdr = nil
	s.MaxHeight, s.MaxFPS = pickRung(encoder, samples)
	if encoder == "" && s.MaxHeight > 0 && s.MaxHeight < capacityRungs[0].Height {
		s.Detail = fmt.Sprintf("Software encoding keeps up with %dp%g at best, so Auto quality stays at or below that.", s.MaxHeight, s.MaxFPS)
	} else {
		s.Detail = ""
	}
	c.mu.Unlock()
	logging.Info("server capacity: %s (%s)", c.describe(), describeRungs(samples))
	c.RecomputeProcesses(ctx)

	// The injected benchmark used by isolated tests does not launch extra FFmpeg jobs.
	if c.measure == nil {
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
func pickRung(encoder string, samples []RungSample) (height int, fps float64) {
	if encoder != "" {
		return 0, 0
	}
	for _, s := range samples {
		if s.Speed >= CapacityRealTime {
			return s.Height, s.FPS
		}
	}
	last := capacityRungs[len(capacityRungs)-1]
	return last.Height, last.FPS // nothing sustained: the smallest, and say so
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
	sample, err := makeCapacitySample(ctx, exe, dir)
	if err != nil {
		return nil, err
	}
	measure := c.measure
	if measure == nil {
		measure = func(ctx context.Context, rung RungSample, threads int, encoder, sample string) (float64, error) {
			return measureRung(ctx, exe, dir, rung, threads, encoder, sample)
		}
	}
	threads := DefaultThreads
	if c.Threads != nil {
		threads = c.Threads()
	}
	rungs := capacityRungs
	if encoder != "" {
		rungs = []RungSample{{Height: 1080, FPS: 30}} // a GPU has no picture-size limit to find
		threads = 1
	}
	var samples []RungSample
	for _, r := range rungs {
		speed, err := measure(ctx, r, threads, encoder, sample)
		if err != nil {
			return samples, err
		}
		r.Speed = speed
		samples = append(samples, r)
		if speed >= CapacityRealTime {
			break
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
	threads := 1
	if encoder == "" && c.Threads != nil {
		threads = max(1, c.Threads())
	}
	free := uint64(0)
	if c.memory != nil {
		free = c.memory()
	} else {
		free = memoryAvailable()
	}
	n := DetectProcesses(speed, c.cpuCount(), threads, free, encoder)
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

// DetectProcesses is how many conversions run side by side without the CPU
// passing the governor's 80% ceiling. One conversion that runs speed times real
// time alone keeps about threads/speed cores busy when paced to real time.
func DetectProcesses(speed float64, cpus, threads int, freeMemory uint64, encoder string) int {
	n := int(0.8 * float64(max(cpus, 1)) * speed / float64(max(threads, 1)))
	if encoder == "" && freeMemory > 0 {
		n = min(n, int(freeMemory/perConversionMemory))
	}
	if encoder == EncoderNvidiaNVENC {
		n = min(n, nvencSessions)
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

// makeCapacitySample encodes a few seconds of ordinary-looking 1080p60 SDR video.
func makeCapacitySample(ctx context.Context, exe, dir string) (string, error) {
	path := filepath.Join(dir, "capacity.mkv")
	base := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-f", "lavfi", "-i",
		fmt.Sprintf("testsrc2=size=1920x1080:rate=60:duration=%d,noise=alls=3:allf=t,format=yuv420p", tuneSeconds)}
	var last error
	for _, codec := range [][]string{{"-c:v", "libx264", "-preset", "ultrafast", "-crf", "20"}, {"-c:v", "libopenh264", "-b:v", "20M"}} {
		args := append(append(append([]string{}, base...), codec...), path)
		if out, err := governor.BackgroundOutput(ctx, lowPriority(ctx, exe, args...)); err != nil {
			last = fmt.Errorf("%v %s", err, strings.TrimSpace(string(out)))
			_ = os.Remove(path)
			continue
		}
		return path, nil
	}
	return "", fmt.Errorf("no encoder could make the test video: %v", last)
}

// measureRung converts the sample to one output size the way a stream would.
func measureRung(ctx context.Context, exe, dir string, rung RungSample, threads int, encoder, sample string) (float64, error) {
	probe, err := library.Probe(ctx, sample)
	if err != nil {
		return 0, err
	}
	w := (rung.Height*16/9 + 1) / 2 * 2
	plan := Plan{Mode: "transcode", Width: w, Height: rung.Height, FPS: rung.FPS, VideoBitrate: heightBitrate(rung.Height), Profile: "high", Encoder: encoder}
	return timePlan(ctx, exe, dir, sample, probe, plan, threads)
}
