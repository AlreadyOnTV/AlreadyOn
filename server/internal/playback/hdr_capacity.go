package playback

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/governor"
	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/logging"
)

// HDRCapacity describes a measured complete HDR pipeline, not encoder throughput.
// Results are intentionally process-local: device/driver changes across restarts
// must not inherit a stale capability decision from the database.
type HDRCapacity struct {
	SourceFPS      float64      `json:"sourceFps"`
	Encoder        string       `json:"encoder"`
	HardwareDecode bool         `json:"hardwareDecode"`
	Transfer       string       `json:"transfer"`
	ToneMapper     string       `json:"toneMapper"`
	Threads        int          `json:"threads"`
	Samples        []RungSample `json:"samples"`
	MeasuredAt     time.Time    `json:"measuredAt"`
}

// HDRLimit applies only to measured 4K-or-smaller, up-to-60fps sources and the
// exact configured pipeline. Explicit quality choices are handled by the planner.
func (c *Capacity) HDRLimit(encoder string, decode bool, v library.StreamInfo, threads int) (height int, fps float64, measured bool) {
	if c == nil || !IsHDRTransfer(v.ColorTransfer) || v.Codec != "hevc" || v.PixelFormat != "yuv420p10le" || v.Width > 3840 || v.Height > 2160 || v.FrameRate <= 0 || v.FrameRate > 60 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.hdr {
		if r.SourceFPS < v.FrameRate || r.Encoder != encoder || r.HardwareDecode != decode || r.Transfer != v.ColorTransfer || r.Threads != threads || time.Since(r.MeasuredAt) > 2*time.Hour {
			continue
		}
		if r.ToneMapper == "gpu" && c.Hardware != nil && !c.Hardware.ToneMapOnGPU(context.Background(), encoder) {
			continue
		}
		height, fps = pickRung("", r.Samples)
		return height, fps, len(r.Samples) > 0
	}
	return
}

// benchmarkHDR checks PQ and HLG independently. It runs in the existing idle-only
// capacity worker, never in the tuning request, and is cancelled when viewing starts.
func (c *Capacity) benchmarkHDR(ctx context.Context, encoder string) ([]HDRCapacity, error) {
	var results []HDRCapacity
	for _, fps := range []float64{24, 30, 60} {
		records, err := c.benchmarkHDRSource(ctx, encoder, fps)
		if err != nil {
			return results, err
		}
		results = append(results, records...)
	}
	return results, nil
}

// Separate source-rate classes avoid penalizing film content for 60fps decoding.
func (c *Capacity) benchmarkHDRSource(ctx context.Context, encoder string, sourceFPS float64) ([]HDRCapacity, error) {
	dir, err := os.MkdirTemp("", "alreadyon-hdr-capacity-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	exe := c.Executable
	if exe == "" {
		exe = "ffmpeg"
	}
	sample := filepath.Join(dir, "hdr.mkv")
	// A real Main 10 decode workload, at the maximum source rate covered by results.
	// Never substitute an 8-bit codec for a failed HEVC fixture.
	args := []string{"-hide_banner", "-v", "error", "-nostdin", "-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=3840x2160:rate=%g:duration=3,noise=alls=6:allf=t", sourceFPS), "-pix_fmt", "yuv420p10le", "-c:v", "libx265", "-preset", "ultrafast", "-x265-params", "pools=2:frame-threads=2:log-level=error", "-color_trc", "smpte2084", "-color_primaries", "bt2020", "-colorspace", "bt2020nc", sample}
	if out, err := governor.BackgroundOutput(ctx, lowPriority(ctx, exe, args...)); err != nil {
		return nil, fmt.Errorf("HDR fixture: %w: %s", err, out)
	}
	probe, err := library.Probe(ctx, sample)
	if err != nil {
		return nil, err
	}
	threads := DefaultThreads
	if c.Threads != nil {
		threads = c.Threads()
	}
	decode := encoder != "" && c.HardwareDecoding != nil && c.HardwareDecoding(ctx)
	if decode && c.Hardware != nil {
		v, _ := Tracks(probe)
		decode = c.Hardware.DecodeAllowed(encoder, v) && c.Hardware.DecodeReady(ctx, encoder)
	}
	var results []HDRCapacity
	for _, transfer := range []string{"smpte2084", "arib-std-b67"} {
		for i := range probe.Streams {
			if probe.Streams[i].Kind == library.StreamVideo {
				probe.Streams[i].ColorTransfer = transfer
			}
		}
		r := HDRCapacity{SourceFPS: sourceFPS, Encoder: encoder, HardwareDecode: decode, Transfer: transfer, ToneMapper: "cpu", Threads: threads}
		if decode && transfer == "smpte2084" && c.Hardware != nil && c.Hardware.ToneMapOnGPU(ctx, encoder) {
			r.ToneMapper = "gpu"
		}
		var gpuError error
		rungs := []RungSample{{Height: 1080, FPS: 30}, {Height: 720, FPS: 30}, {Height: 480, FPS: 30}, {Height: 360, FPS: 30}}
		if sourceFPS > 30 {
			rungs = capacityRungs
		} else {
			for i := range rungs {
				rungs[i].FPS = sourceFPS
			}
		}
		for _, rung := range rungs {
			plan := Plan{Mode: "transcode", ToneMap: true, ToneMapper: r.ToneMapper, Width: rung.Height * 16 / 9 / 2 * 2, Height: rung.Height, FPS: rung.FPS, VideoBitrate: heightBitrate(rung.Height), Profile: "high", Encoder: encoder}
			if decode {
				plan.Decoder = "hardware"
			}
			speed, err := timePlan(ctx, exe, dir, sample, probe, plan, threads)
			if err != nil && r.ToneMapper == "gpu" && ctx.Err() == nil {
				gpuError = err
				r.ToneMapper = "cpu"
				plan.ToneMapper = "cpu"
				speed, err = timePlan(ctx, exe, dir, sample, probe, plan, threads)
			}
			if err != nil {
				return nil, err
			}
			if gpuError != nil && c.Hardware != nil {
				c.Hardware.ToneMapFailed(encoder, gpuError)
				gpuError = nil
			}
			rung.Speed = speed
			r.Samples = append(r.Samples, rung)
			if speed >= CapacityRealTime {
				break
			}
		}
		r.MeasuredAt = time.Now()
		results = append(results, r)
		logging.Info("HDR capacity: %s decode GPU=%t %s tone map=%s: %s", encoder, decode, transfer, r.ToneMapper, describeRungs(r.Samples))
	}
	return results, nil
}
