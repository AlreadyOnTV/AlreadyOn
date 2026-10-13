package playback

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/governor"
	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
)

type Job struct {
	Entry     schedule.Entry
	Path      string
	Source    library.ProbeResult
	Plan      Plan
	Offset    time.Duration
	Duration  time.Duration
	Burst     time.Duration // content read before real-time pacing; see ReadBurst
	OutputDir string        // newly-created private directory owned by the supervisor
	Device    string        // DRI render node for a hardware encoder
	Threads   int           // CPU threads for decoding and filtering; 0 is DefaultThreads
	Details   Details       // what the player shows about this stream; not used to run FFmpeg
	// Report is told when the runner falls back to a simpler pipeline, so the
	// stream's plan and details stay true. It may be nil.
	Report func(Plan, Pipeline)
	// ReportStats publishes process-scoped diagnostics while FFmpeg is alive.
	// It is installed by Manager and may be nil for standalone runner callers.
	ReportStats func(RuntimeStats)
	// Ready runs once after the first segment, while a long stream is still alive.
	Ready func()
}

type Limits struct {
	Startup  time.Duration
	Stall    time.Duration
	Grace    time.Duration
	MaxBytes int64
}

func DefaultLimits() Limits {
	return Limits{15 * time.Second, 10 * time.Second, 5 * time.Second, MaxProcessBytes}
}

var (
	ErrStartup   = errors.New("playback: FFmpeg startup timed out")
	ErrStalled   = errors.New("playback: FFmpeg stopped producing segments")
	ErrDiskLimit = errors.New("playback: stream output budget exceeded")
)

// Arguments builds a software-only pipeline. Segment-copy paths retain source
// keyframes rather than advertising independently forced two-second segments.
func Arguments(j Job) ([]string, error) {
	v, a := Tracks(j.Source)
	p := j.Plan
	if !filepath.IsAbs(j.Path) || !filepath.IsAbs(j.OutputDir) || j.Offset < 0 || j.Duration <= 0 || j.Duration > 48*time.Hour || j.Burst < 0 || j.Burst > PresentationDelay || v.Index < 0 || (p.Mode != "remux" && p.Mode != "transcode") {
		return nil, errors.New("playback: invalid transcode job")
	}
	threads := j.Threads
	if threads == 0 {
		threads = DefaultThreads
	}
	if threads < 1 || threads > MaxThreads {
		return nil, errors.New("playback: invalid thread count")
	}
	var hw hwSetup
	var gpu gpuPipeline
	onGPU := p.Decoder == "hardware" && p.Encoder != "" && !p.VideoCopy
	if p.Encoder != "" && !p.VideoCopy {
		var err error
		if p.LowHeight > 0 {
			return nil, errors.New("playback: a hardware encoder does not use the ladder")
		}
		if onGPU {
			gpu, err = gpuPipelineFor(p, v.ColorTransfer, j.Device)
		} else {
			hw, err = hardwareSetup(p.Encoder, j.Device, p.Profile, p.VideoBitrate)
		}
		if err != nil {
			return nil, err
		}
	}
	// Half a second is FFmpeg's own default burst, kept for prepared-ahead jobs.
	// A GPU step that hangs says why only in warnings, which the failure report keeps.
	logLevel := "error"
	if p.Encoder != "" {
		logLevel = "warning"
	}
	args := []string{"-hide_banner", "-loglevel", logLevel, "-nostdin", "-n", "-max_alloc", "67108864", "-filter_threads", "1"}
	args = append(args, hw.global...)
	args = append(args, gpu.global...)
	args = append(args, "-protocol_whitelist", "file,pipe", "-probesize", "5000000", "-analyzeduration", "5000000",
		"-threads", strconv.Itoa(threads), "-readrate", "1", "-readrate_initial_burst", seconds(max(j.Burst, 500*time.Millisecond)), "-ss", seconds(j.Offset))
	args = append(args, gpu.input...)
	args = append(args, "-i", j.Path, "-t", seconds(j.Duration), "-sn", "-dn", "-map_metadata", "-1")
	ladder := !p.VideoCopy && p.LowHeight > 0
	if ladder {
		if p.ToneMap || p.LowWidth < 2 || p.LowWidth%2 != 0 || p.LowHeight < 2 || p.LowHeight%2 != 0 || p.LowHeight >= p.Height || p.LowBitrate < 300000 || p.LowBitrate > p.VideoBitrate {
			return nil, errors.New("playback: invalid low rung")
		}
	}
	if !ladder {
		args = append(args, "-map", fmt.Sprintf("0:%d", v.Index))
	}
	if a.Index >= 0 && !ladder {
		args = append(args, "-map", fmt.Sprintf("0:%d", a.Index))
	}
	if p.VideoCopy {
		args = append(args, "-c:v", "copy")
	} else {
		// 4K output is only ever planned for a hardware encoder.
		maxW, maxH, maxRate := 1920, 1080, int64(12000000)
		if p.Encoder != "" {
			maxW, maxH, maxRate = 3840, UHDHeight, 25000000
		}
		if p.Width < 2 || p.Width > maxW || p.Height < 2 || p.Height > maxH || p.Width%2 != 0 || p.Height%2 != 0 || math.IsNaN(p.FPS) || p.FPS <= 0 || p.FPS > 60 || p.VideoBitrate < 300000 || p.VideoBitrate > maxRate {
			return nil, errors.New("playback: invalid video output")
		}
		if p.Profile != "high" && p.Profile != "main" && p.Profile != "constrained_baseline" {
			return nil, errors.New("playback: invalid H.264 profile")
		}
		filter, filterThreads := gpu.filter, "1"
		if onGPU && p.ToneMap && p.ToneMapper == "cpu" {
			// Hardware decode does not make the intervening CPU tone map single-threaded.
			filterThreads = strconv.Itoa(min(max(4, threads), 16))
		}
		if !onGPU {
			var err error
			if filter, filterThreads, err = videoFilter(p, v); err != nil {
				return nil, err
			}
			if p.ToneMap {
				filterThreads = strconv.Itoa(min(max(4, threads), 16))
			}
		}
		encode := softwareVideoArgs(softwareEncoder(), p.Profile, filterThreads)
		if onGPU {
			// Decode, scale and any tone map already ran on the GPU, in the filter.
			encode = append([]string{"-filter_threads", filterThreads}, gpu.encode...)
		} else if p.Encoder != "" {
			// Decoding and scaling stay in software; only the encode moves to the hardware.
			filter += "," + hw.filter
			encode = append([]string{"-filter_threads", filterThreads}, hw.encode...)
		}
		// stream is an FFmpeg stream specifier: "v" for all video, "v:0" for one output.
		rate := func(stream string, bitrate int64) []string {
			b := strconv.FormatInt(bitrate, 10)
			return []string{"-b:" + stream, b, "-maxrate:" + stream, b, "-bufsize:" + stream, strconv.FormatInt(bitrate*2, 10)}
		}
		if ladder {
			// One decode and one scale to the main size feed both encodes; the low
			// rung is scaled down from that. Rung 0 is the low one.
			graph := fmt.Sprintf("[0:%d]%s,split=2[main][small];[small]scale=%d:%d,setsar=1[low]", v.Index, filter, p.LowWidth, p.LowHeight)
			args = append(args, "-filter_complex", graph, "-map", "[low]", "-map", "[main]")
			encode = append(encode, rate("v:0", p.LowBitrate)...)
			encode = append(encode, rate("v:1", p.VideoBitrate)...)
		} else {
			encode = append(encode, "-vf", filter)
			if p.Encoder == "" {
				encode = append(encode, rate("v", p.VideoBitrate)...)
			}
		}
		args = append(args, encode...)
		args = append(args, "-g", strconv.Itoa(int(math.Ceil(p.FPS*2))), "-force_key_frames", "expr:gte(t,n_forced*2)")
	}
	if p.Normalization != nil && (p.AudioCopy || !p.Normalization.Valid()) {
		return nil, errors.New("playback: invalid audio normalization plan")
	}
	if math.IsNaN(p.VolumeOffsetDB) || math.IsInf(p.VolumeOffsetDB, 0) || p.VolumeOffsetDB < -30 || p.VolumeOffsetDB > 12 || (p.VolumeOffsetDB != 0 && p.AudioCopy && a.Index >= 0) {
		return nil, errors.New("playback: invalid commercial volume offset")
	}
	if a.Index >= 0 {
		filters := []string{}
		if p.Normalization != nil {
			filters = append(filters, p.Normalization.Filter())
		}
		if p.VolumeOffsetDB != 0 {
			filters = append(filters, fmt.Sprintf("volume=%.2fdB", p.VolumeOffsetDB))
			// Positive gain needs peak protection, with no automatic makeup gain.
			if p.VolumeOffsetDB > 0 {
				filters = append(filters, "alimiter=limit=0.891251:level=false:latency=true")
			}
		}
		if len(filters) > 0 {
			args = append(args, "-af", strings.Join(filters, ","))
		}
		if ladder {
			// Each variant playlist carries its own copy of the audio.
			for range 2 {
				args = append(args, "-map", fmt.Sprintf("0:%d", a.Index))
			}
		}
		if p.AudioCopy {
			args = append(args, "-c:a", "copy")
		} else {
			if p.AudioBitrate < 128000 || p.AudioBitrate > 1024000 {
				return nil, errors.New("playback: invalid audio output")
			}
			channels := p.AudioChannels
			if channels == 0 {
				channels = 2
			}
			if channels != 2 && channels != 6 && channels != 8 {
				return nil, errors.New("playback: unsupported AAC channel layout")
			}
			if p.Normalization != nil && channels != 2 {
				return nil, errors.New("playback: loudness measurements require stereo output")
			}
			args = append(args, "-c:a", "aac", "-b:a", strconv.FormatInt(p.AudioBitrate, 10), "-ar", "48000", "-ac", strconv.Itoa(channels))
		}
	}
	flags := "temp_file+delete_segments+program_date_time"
	if !p.VideoCopy {
		flags += "+independent_segments"
	}
	args = append(args, "-max_muxing_queue_size", "1024", "-f", "hls", "-hls_time", "2", "-hls_list_size", "6", "-hls_delete_threshold", "2", "-hls_flags", flags)
	if ladder {
		variants := "v:0 v:1"
		if a.Index >= 0 {
			variants = "v:0,a:0 v:1,a:1"
		}
		return append(args, "-master_pl_name", "index.m3u8", "-var_stream_map", variants,
			"-hls_segment_filename", filepath.Join(j.OutputDir, "segment-%v-%09d.ts"), filepath.Join(j.OutputDir, "stream-%v.m3u8")), nil
	}
	return append(args, "-hls_segment_filename", filepath.Join(j.OutputDir, "segment-%09d.ts"), filepath.Join(j.OutputDir, "index.m3u8")), nil
}

// videoFilter scales to the planned size and rate. HDR is tone-mapped to BT.709
// SDR: scaled first (in 10-bit) so the floating-point stage sees fewer pixels.
// Measured on 4K HEVC HDR, one filter thread cannot keep up with real time at
// 1080p, so this path is allowed more.
func videoFilter(p Plan, v library.StreamInfo) (filter, threads string, err error) {
	fps := strconv.FormatFloat(p.FPS, 'f', 6, 64)
	if !p.ToneMap {
		return fmt.Sprintf("scale=%d:%d,setsar=1,fps=%s", p.Width, p.Height, fps), "1", nil
	}
	if !IsHDRTransfer(v.ColorTransfer) {
		return "", "", errors.New("playback: tone mapping needs a PQ or HLG source")
	}
	return fmt.Sprintf("fps=%s,zscale=w=%d:h=%d,%s,format=yuv420p,setsar=1",
		fps, p.Width, p.Height, cpuToneMap(v.ColorTransfer)), "4", nil
}

func seconds(d time.Duration) string { return strconv.FormatFloat(d.Seconds(), 'f', 6, 64) }

// Runner supervises one FFmpeg process until it has exited and been reaped.
// Successful output remains until its last viewer is done; failed output is
// removed. Global admission and leases belong to Manager, not this worker.
type Runner struct {
	Executable string
	Limits     Limits
	// OnHardwareFailure is told when a hardware encoder failed but the same
	// stream then worked on software, so the next stream can use software too.
	OnHardwareFailure func(encoder string, err error)
	// OnDecodeFailure is told when GPU decoding failed but CPU decoding then worked.
	OnDecodeFailure func(encoder string, source library.StreamInfo, err error)
	// OnToneMapFailure remembers failure of only the GPU HDR stage.
	OnToneMapFailure func(encoder string, err error)
}

// hardwareAttemptStartup bounds a try that depends on GPU drivers, so a hung
// GPU leaves time for the fallbacks that follow.
const hardwareAttemptStartup = 8 * time.Second

// Run starts the stream and, when a GPU step fails, tries again with less of the
// GPU: GPU tone mapping, then GPU decoding, then the GPU encoder. A failure is
// blamed on the hardware only if a simpler pipeline then works; when every
// attempt fails the source is the problem.
func (r Runner) Run(ctx context.Context, j Job) error {
	attempts := fallbackPlans(j.Plan)
	var errs []error
	for i, plan := range attempts {
		attempt := j
		attempt.Plan = plan
		if plan.Encoder == "" {
			attempt.Device = ""
		}
		if i > 0 {
			// A failed attempt removes its output directory.
			if err := os.Mkdir(j.OutputDir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return errors.Join(append(errs, err)...)
			}
			if j.Report != nil {
				j.Report(plan, DescribePipeline(plan))
			}
		}
		limited := r
		if plan.Encoder != "" && i < len(attempts)-1 {
			limited.Limits = r.limits()
			limited.Limits.Startup = min(limited.Limits.Startup, hardwareAttemptStartup)
			// A hung GPU ignores the polite stop; do not wait long before the next try.
			limited.Limits.Grace = min(limited.Limits.Grace, time.Second)
		}
		attempt.Ready = func() {
			r.blame(j, attempts[:i], errs, plan)
			if j.Ready != nil {
				j.Ready()
			}
		}
		err := limited.run(ctx, attempt)
		if err == nil {
			r.blame(j, attempts[:i], errs, plan)
			return nil
		}
		if ctx.Err() != nil {
			return err
		}
		errs = append(errs, err)
	}
	return errs[len(errs)-1]
}

// fallbackPlans lists the plans to try, most hardware first. The last one is
// always a plan that uses no GPU step the earlier ones did not also use.
func fallbackPlans(p Plan) []Plan {
	plans := []Plan{p}
	if p.Encoder == "" || p.VideoCopy {
		return plans
	}
	if p.ToneMapper == "gpu" {
		p.ToneMapper = "cpu"
		plans = append(plans, p)
	}
	if p.Decoder == "hardware" {
		p.Decoder = ""
		if p.ToneMap {
			p.ToneMapper = "cpu"
		}
		plans = append(plans, p)
	}
	p.Encoder, p.Decoder = "", ""
	if p.ToneMap {
		p.ToneMapper = "cpu"
	}
	return append(plans, p)
}

// blame reports what a working attempt proves about the failed ones before it:
// CPU decoding with the same GPU encoder working means GPU decoding was the
// trouble; software everything working means the GPU encoder was.
func (r Runner) blame(j Job, tried []Plan, errs []error, worked Plan) {
	if worked.ToneMapper == "cpu" && worked.Decoder == "hardware" && r.OnToneMapFailure != nil {
		for i, p := range tried {
			if p.ToneMapper == "gpu" && p.Encoder == worked.Encoder {
				r.OnToneMapFailure(p.Encoder, errs[i])
				break
			}
		}
	}
	lastDecode, lastEncode := -1, -1
	for i, p := range tried {
		if p.Decoder == "hardware" {
			lastDecode = i
		}
		if p.Encoder != "" {
			lastEncode = i
		}
	}
	if worked.Encoder != "" && worked.Decoder == "" && lastDecode >= 0 && r.OnDecodeFailure != nil {
		v, _ := Tracks(j.Source)
		r.OnDecodeFailure(tried[lastDecode].Encoder, v, errs[lastDecode])
	}
	if worked.Encoder == "" && lastEncode >= 0 && r.OnHardwareFailure != nil {
		r.OnHardwareFailure(tried[lastEncode].Encoder, errs[lastEncode])
	}
}

func (r Runner) limits() Limits {
	if r.Limits == (Limits{}) {
		return DefaultLimits()
	}
	return r.Limits
}

func (r Runner) run(ctx context.Context, j Job) error {
	releaseForeground := func() {}
	if needsForegroundReservation(j) {
		releaseForeground = governor.ReserveForeground()
	}
	defer releaseForeground()
	args, err := Arguments(j)
	if err != nil {
		return err
	}
	limits := r.Limits
	if limits == (Limits{}) {
		limits = DefaultLimits()
	}
	if limits.Startup <= 0 || limits.Stall <= 0 || limits.Grace <= 0 || limits.MaxBytes <= 0 {
		return errors.New("playback: invalid process limits")
	}
	limits = limitsFor(limits, j)
	entries, err := os.ReadDir(j.OutputDir)
	if err != nil || len(entries) != 0 {
		return errors.New("playback: output directory must exist and be empty")
	}
	if err := os.Chmod(j.OutputDir, 0700); err != nil {
		return err
	}
	processCtx, cancel := context.WithTimeout(ctx, j.Duration+limits.Startup)
	defer cancel()
	executable := r.Executable
	if executable == "" {
		executable = "ffmpeg"
	}
	cmd := exec.CommandContext(processCtx, executable, args...)
	configureProcess(cmd)
	cmd.WaitDelay = limits.Grace
	var stderr tailBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(j.OutputDir)
		return fmt.Errorf("playback: start FFmpeg: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	started, lastChange := time.Now(), time.Now()
	lastMetricsAt := started
	lastCPU, cpuErr := governor.ProcessCPUTime(cmd.Process.Pid)
	lastCPUValid := cpuErr == nil
	var lastStamp time.Time
	ready := false
	originCaptured := false
	stop := func(reason error) error {
		cancel()
		<-done
		_ = os.RemoveAll(j.OutputDir)
		// A hang leaves no exit status; FFmpeg's last words are the only clue.
		if tail := strings.TrimSpace(stderr.String()); tail != "" && (errors.Is(reason, ErrStartup) || errors.Is(reason, ErrStalled)) {
			if len(tail) > 600 {
				tail = tail[len(tail)-600:]
			}
			return fmt.Errorf("%w (%s)", reason, tail)
		}
		return reason
	}
	for {
		select {
		case err := <-done:
			if processCtx.Err() != nil {
				err = processCtx.Err()
			}
			if err != nil {
				_ = os.RemoveAll(j.OutputDir)
				return fmt.Errorf("playback: FFmpeg failed: %w (%s)", err, strings.TrimSpace(stderr.String()))
			}
			if _, err := os.Stat(filepath.Join(j.OutputDir, "index.m3u8")); err != nil {
				_ = os.RemoveAll(j.OutputDir)
				return errors.New("playback: FFmpeg produced no playlist")
			}
			if !originCaptured {
				if _, err := captureHLSOrigin(j.OutputDir); err != nil {
					return err
				}
			}
			return nil
		case <-processCtx.Done():
			return stop(processCtx.Err())
		case now := <-ticker.C:
			if j.ReportStats != nil && now.Sub(lastMetricsAt) >= time.Second {
				stats := RuntimeStats{}
				if cpu, err := governor.ProcessCPUTime(cmd.Process.Pid); err == nil {
					if lastCPUValid {
						percent := 100 * (cpu - lastCPU).Seconds() / now.Sub(lastMetricsAt).Seconds()
						if percent >= 0 {
							stats.CPUUtil = &percent
						}
					}
					lastCPU = cpu
					lastCPUValid = true
				} else {
					lastCPUValid = false
				}
				lastMetricsAt = now
				j.ReportStats(stats)
			}
			if !originCaptured {
				var err error
				originCaptured, err = captureHLSOrigin(j.OutputDir)
				if err != nil {
					return stop(err)
				}
			}
			var size int64
			files, err := os.ReadDir(j.OutputDir)
			if err != nil {
				return stop(err)
			}
			for _, f := range files {
				info, err := f.Info()
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err != nil {
					return stop(err)
				}
				size += info.Size()
				if strings.HasSuffix(f.Name(), ".ts") && info.Size() > 0 {
					if !ready && j.Ready != nil {
						j.Ready()
					}
					ready = true
					if info.ModTime().After(lastStamp) {
						lastStamp = info.ModTime()
						lastChange = now
					}
				}
			}
			if size > limits.MaxBytes {
				return stop(ErrDiskLimit)
			}
			if !ready && now.Sub(started) >= limits.Startup {
				return stop(ErrStartup)
			}
			if ready && now.Sub(lastChange) >= limits.Stall {
				return stop(ErrStalled)
			}
		}
	}
}

// needsForegroundReservation identifies the measured the deployment host bottleneck: a 4K
// HEVC Main 10 HDR source whose SDR conversion uses CPU tone mapping. Duration
// and file size do not affect the per-frame pipeline cost.
func needsForegroundReservation(j Job) bool {
	v, _ := Tracks(j.Source)
	return j.Plan.ToneMap && j.Plan.ToneMapper == "cpu" && v.Codec == "hevc" &&
		v.PixelFormat == "yuv420p10le" && v.Width >= 3840 && v.Height >= 2160 &&
		IsHDRTransfer(v.ColorTransfer)
}

// Keep a bounded diagnostic tail even when corrupt media produces many errors.
type tailBuffer struct{ bytes.Buffer }

func (b *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) >= 8192 {
		b.Reset()
		p = p[len(p)-8192:]
	}
	if b.Len()+len(p) > 8192 {
		b.Next(b.Len() + len(p) - 8192)
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}
