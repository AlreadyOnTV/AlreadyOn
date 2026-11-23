// Calibration policy tests use controlled measurements to verify conservative admission.
package playback

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCalibrationRecommendsFullForLargeQuickDifference(t *testing.T) {
	now := time.Now()
	profile := ProfileCapacity{SourceClass: SourceH264HD, Encoder: EncoderIntelQSV, Height: 1080, FPS: 30, SourceFPS: 30, Threads: 4, Verified: true, Streams: 4, SingleSpeed: 1.4, SingleStreamSpeed: 2, Speeds: []float64{1.4, 1.4, 1.4, 1.4}}
	c := &Calibration{}
	c.remember("full", calibrationCache{At: now.Add(-time.Minute), Profiles: []ProfileCapacity{profile}})
	quick := profile
	quick.Streams = 1
	quick.SingleSpeed = 1
	quick.SingleStreamSpeed = 1
	quick.Speeds = []float64{1}
	c.remember("quick", calibrationCache{At: now, Profiles: []ProfileCapacity{quick}})
	if !c.Status().RecommendFull {
		t.Fatal("large quick/full difference did not request a full test")
	}
	quick.SingleSpeed = 1.6
	quick.SingleStreamSpeed = 1.6
	quick.Speeds = []float64{1.6}
	c.remember("quick", calibrationCache{At: now, Profiles: []ProfileCapacity{quick}})
	if c.Status().RecommendFull {
		t.Fatal("similar quick/full result incorrectly requested a full test")
	}
	// A different validated concurrency count is expected: Quick checks one.
	quick.SingleSpeed = 2
	quick.SingleStreamSpeed = 2
	quick.Speeds = []float64{2}
	c.remember("quick", calibrationCache{At: now, Profiles: []ProfileCapacity{quick}})
	if c.Status().RecommendFull {
		t.Fatal("quick/full stream-count difference was treated as degradation")
	}
	// A new Full Test supersedes the older Quick Test comparison.
	c.remember("full", calibrationCache{At: now.Add(time.Second), Profiles: []ProfileCapacity{profile}})
	if c.Status().RecommendFull {
		t.Fatal("new full result retained an old quick recommendation")
	}
}

func TestFullCalibrationUsesShortStatisticallyUsefulWindows(t *testing.T) {
	if fullSampleWarmup != 500*time.Millisecond || fullSampleWindow != 2*time.Second {
		t.Fatalf("full sample timing = %s + %s", fullSampleWarmup, fullSampleWindow)
	}
	// Even the slowest supported fixture observes 48 frames in the window.
	// Borderline concurrent capacity still receives the existing second sample.
	if int(24*fullSampleWindow.Seconds()) != 48 {
		t.Fatal("full sample window does not observe 48 frames at 24 fps")
	}
}

func TestCalibrationRejectsStarvingConcurrentStream(t *testing.T) {
	c := &Calibration{Tuner: &Tuner{}}
	c.measure = func(_ context.Context, _ calibrationCase, n int) ([]float64, error) {
		switch n {
		case 1:
			return []float64{3}, nil
		case 2:
			return []float64{2, 2}, nil
		default:
			return []float64{4, 4, 4, .9}, nil
		}
	}
	p := c.evaluate(context.Background(), calibrationCase{}, nil)
	if p.Streams != 2 {
		t.Fatalf("validated %d, want 2", p.Streams)
	}
}
func TestCalibrationConfirmsBorderlineCapacity(t *testing.T) {
	calls := 0
	c := &Calibration{Tuner: &Tuner{}}
	c.measure = func(_ context.Context, _ calibrationCase, n int) ([]float64, error) {
		if n == 1 {
			return []float64{3}, nil
		}
		calls++
		if calls == 1 {
			return []float64{1.4, 1.4}, nil
		}
		return []float64{1.2, 1.2}, nil
	}
	p := c.evaluate(context.Background(), calibrationCase{}, nil)
	if p.Streams != 1 || calls != 2 {
		t.Fatalf("streams=%d calls=%d", p.Streams, calls)
	}
}
func TestCalibrationFailureRemainsUnverified(t *testing.T) {
	c := &Calibration{measure: func(context.Context, calibrationCase, int) ([]float64, error) { return nil, errors.New("unavailable") }}
	if p := c.evaluate(context.Background(), calibrationCase{}, nil); p.Verified || p.Streams != 0 {
		t.Fatal(p)
	}
}

func TestCalibratedAdmissionUsesPipelineCountAndUnknownFallback(t *testing.T) {
	source := sourceInfo()
	plan := Plan{Mode: "transcode", Height: 1080, FPS: 30}
	job := Job{Source: source, Plan: plan, Threads: 2}
	video, _ := Tracks(source)
	c := &Capacity{calibrated: true, memory: func() uint64 { return 64 << 30 }, profiles: []ProfileCapacity{{SourceClass: classifySource(video), SourceWidth: 1920, SourceHeight: 1080, SourceFPS: 60, Height: 1080, FPS: 30, Threads: 2, Streams: 2, Verified: true}}}
	if err := c.AdmissionError([]Job{job}, job); err != nil {
		t.Fatal(err)
	}
	if err := c.AdmissionError([]Job{job, job}, job); !errors.Is(err, ErrBusy) {
		t.Fatalf("third stream: %v", err)
	}
	unknown := job
	unknown.Threads = 4
	if err := c.AdmissionError([]Job{job}, unknown); !errors.Is(err, ErrBusy) {
		t.Fatalf("unknown concurrent path: %v", err)
	}
	c.profiles[0].Streams = 0
	if err := c.AdmissionError(nil, job); !errors.Is(err, ErrCapacityUnsustainable) {
		t.Fatalf("failed pipeline: %v", err)
	}
}

func TestCachedLimitFailureRetainsMeasuredLowerCount(t *testing.T) {
	c := &Calibration{Tuner: &Tuner{}}
	c.measure = func(_ context.Context, _ calibrationCase, n int) ([]float64, error) {
		if n == 4 {
			return nil, errors.New("overloaded")
		}
		if n == 2 {
			return []float64{2, 2}, nil
		}
		return []float64{4}, nil
	}
	old := []ProfileCapacity{{Verified: true, Streams: 4}}
	if p := c.evaluate(context.Background(), calibrationCase{}, old); p.Streams != 2 {
		t.Fatalf("streams=%d", p.Streams)
	}
}

func TestRepresentativePublishPreservesFullHDRMatrix(t *testing.T) {
	c := &Capacity{hdr: []HDRCapacity{{Encoder: EncoderIntelQSV, ToneMapper: "gpu", HardwareDecode: true, Transfer: "smpte2084", SourceFPS: 60, Threads: 2, Samples: []RungSample{{Height: 720, FPS: 30, Speed: 3}, {Height: 1080, FPS: 30, Speed: 2}}}}}
	c.publishProfiles([]ProfileCapacity{{Verified: true, Encoder: EncoderIntelQSV, SourceClass: SourceHEVCPQ, SourceFPS: 60, HardwareDecode: true, ToneMapper: "gpu", Threads: 2, Height: 1080, FPS: 30, SingleSpeed: 4, Streams: 2}})
	if len(c.hdr) != 1 || len(c.hdr[0].Samples) != 2 || c.hdr[0].Samples[0].Height != 720 || c.hdr[0].Samples[1].Speed != 4 {
		t.Fatalf("matrix overwritten: %+v", c.hdr)
	}
}

func TestPipelineRankingPrefersCPUThenGPUThenFidelityThenStreams(t *testing.T) {
	gpu := func(v float64) *float64 { return &v }
	base := ProfileCapacity{CPUCores: .5, GPUEngines: gpu(.5), Height: 1080, FPS: 30, ReferenceSSIM: gpu(.99), Streams: 2}
	for _, tc := range []struct {
		name   string
		change func(*ProfileCapacity)
		want   bool
	}{
		{"CPU before GPU", func(p *ProfileCapacity) { p.CPUCores = .3; p.GPUEngines = gpu(.9) }, true},
		{"GPU before streams", func(p *ProfileCapacity) { p.GPUEngines = gpu(.3); p.Streams = 1 }, true},
		{"fidelity before streams", func(p *ProfileCapacity) { p.ReferenceSSIM = gpu(.9); p.Streams = 4 }, false},
		{"streams last", func(p *ProfileCapacity) { p.Streams = 4 }, true},
		{"unknown GPU is not free", func(p *ProfileCapacity) { p.GPUEngines = nil }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := base
			tc.change(&p)
			if got := betterPipeline(p, base); got != tc.want {
				t.Fatalf("got %t", got)
			}
		})
	}
}

func TestAdmissionUsesClosestMeasuredSourceRate(t *testing.T) {
	source := sourceInfo()
	source.Streams[0].FrameRate = 24
	video, _ := Tracks(source)
	p := ProfileCapacity{Verified: true, SourceClass: classifySource(video), SourceWidth: video.Width, SourceHeight: video.Height, SourceFPS: 60, Height: 1080, FPS: 30, Threads: 2, Streams: 0}
	film := p
	film.SourceFPS = 24
	film.Streams = 2
	c := &Capacity{calibrated: true, profiles: []ProfileCapacity{p, film}, memory: func() uint64 { return 64 << 30 }}
	job := Job{Source: source, Plan: Plan{Mode: "transcode", Height: 1080, FPS: 24}, Threads: 2}
	if _, err := c.calibratedAdmission(nil, job); err != nil {
		t.Fatalf("60fps failure overrode working film-rate profile: %v", err)
	}
}
