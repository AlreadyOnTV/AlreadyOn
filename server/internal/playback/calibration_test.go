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

func TestFullCalibrationBenchmarksFourKHardwareOutput(t *testing.T) {
	if got := benchmarkOutputHeight(1080, 2160, EncoderIntelQSV, true); got != UHDHeight {
		t.Fatalf("full hardware output = %d", got)
	}
	for _, got := range []int{
		benchmarkOutputHeight(1080, 2160, "", true),
		benchmarkOutputHeight(1080, 2160, EncoderIntelQSV, false),
		benchmarkOutputHeight(1080, 1080, EncoderIntelQSV, true),
	} {
		if got != 1080 {
			t.Fatalf("non-4K case changed output to %d", got)
		}
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
		if n >= 3 {
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

func TestCalibrationMeasuresThreeWhenFourCannotKeepUp(t *testing.T) {
	c := &Calibration{Tuner: &Tuner{}}
	c.measure = func(_ context.Context, _ calibrationCase, n int) ([]float64, error) {
		if n == 4 {
			return []float64{2, 2, 2, 0.9}, nil
		}
		speeds := make([]float64, n)
		for i := range speeds {
			speeds[i] = 2
		}
		return speeds, nil
	}
	if got := c.evaluate(context.Background(), calibrationCase{}, nil).Streams; got != 3 {
		t.Fatalf("expected three confirmed streams, got %d", got)
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

func TestUnmeasuredCalibrationAllowsTwoConversionSlots(t *testing.T) {
	c := &Capacity{memory: func() uint64 { return 16 << 30 }}
	c.resetCalibration()
	job := Job{Source: sourceInfo(), Plan: Plan{Mode: "transcode", Height: 1080, FPS: 30}, Threads: 2}
	settings := DefaultSettings()
	settings.AutoProcesses = true
	if got := EffectiveSettings(settings, c).MaxProcesses; got != 2 {
		t.Fatalf("effective process limit = %d, want 2", got)
	}
	if err := c.AdmissionError([]Job{job}, job); err != nil {
		t.Fatalf("second conversion: %v", err)
	}
	if err := c.AdmissionError([]Job{job, job}, job); !errors.Is(err, ErrBusy) {
		t.Fatalf("third conversion: %v", err)
	}
}

func TestCalibrationStatusIncludesQuickAndFullCases(t *testing.T) {
	c := &Calibration{}
	c.remember("quick", calibrationCache{Profiles: []ProfileCapacity{{
		SourceClass: SourceH264HD, SingleSpeed: 0.8, Verified: true,
	}}})
	c.remember("full", calibrationCache{Profiles: []ProfileCapacity{{
		SourceClass: SourceHEVCPQ, Encoder: EncoderIntelQSV, Streams: 2,
		SingleSpeed: 3, Speeds: []float64{2.1, 1.67}, Verified: true,
	}}})
	rows := c.Status().Results
	if len(rows) != 2 || rows[0].Passed || rows[0].Multiplier != 0.8 ||
		!rows[1].Passed || rows[1].Multiplier != 1.67 {
		t.Fatalf("unexpected test results: %+v", rows)
	}
}

func TestMixedConversionsUseMeasuredDemand(t *testing.T) {
	source := sourceInfo()
	video, _ := Tracks(source)
	light := ProfileCapacity{TargetHeight: AutoCapacityHeight, SourceClass: classifySource(video),
		SourceWidth: video.Width, SourceHeight: video.Height, SourceFPS: 60, Height: 360, FPS: 30,
		Threads: 2, Streams: 4, Verified: true}
	heavy := light
	heavy.TargetHeight, heavy.Height, heavy.Streams = 720, 720, 2
	c := &Capacity{memory: func() uint64 { return 16 << 30 }}
	c.publishProfiles([]ProfileCapacity{light, heavy})
	small := Job{Source: source, Plan: Plan{Mode: "transcode", Height: 360, FPS: 30}, Threads: 2}
	large := small
	large.Plan.Height = 720
	if c.Processes() != 4 {
		t.Fatalf("expected four verified baseline slots, got %d", c.Processes())
	}
	if err := c.AdmissionError([]Job{large, small}, small); err != nil {
		t.Fatalf("mixed load should fit: %v", err)
	}
	if err := c.AdmissionError([]Job{large, small, small}, small); !errors.Is(err, ErrBusy) {
		t.Fatalf("mixed load should reject overcommit: %v", err)
	}
}

func TestFullCalibrationLimitsAlternateBackendCoverage(t *testing.T) {
	primary := Plan{Height: UHDHeight, Decoder: "hardware", ToneMap: true, ToneMapper: "gpu"}
	if got := fullCalibrationHeights(true, 0, primary); len(got) != 6 ||
		got[0] != AutoCapacityHeight || got[1] != 480 || got[2] != 720 || got[3] != 1080 || got[4] != UHDHeight || got[5] != AutoMinHeight {
		t.Fatalf("primary HDR rungs: %v", got)
	}
	if got := fullCalibrationHeights(false, 0, primary); len(got) != 1 || got[0] != AutoCapacityHeight {
		t.Fatalf("alternate backend rungs: %v", got)
	}
	primary.ToneMapper = "cpu"
	if got := fullCalibrationHeights(true, 1, primary); len(got) != 2 || got[1] != UHDHeight {
		t.Fatalf("CPU tone-map fallback: %v", got)
	}
	if representativeComparison(SourceHEVCPQ, 60) || representativeComparison(SourceHEVCHLG, 24) ||
		!representativeComparison(SourceHEVCPQ, 30) || !representativeComparison(SourceHEVCSDR, 30) {
		t.Fatal("alternate backends should compare SDR and 30 fps HDR")
	}
}

func TestFullCalibrationResumesOnlyMissingAndExpiredCases(t *testing.T) {
	now := time.Date(2026, 11, 23, 14, 0, 0, 0, time.UTC)
	source := sourceInfo()
	video, _ := Tracks(source)
	cases := func() []calibrationCase {
		var result []calibrationCase
		for _, height := range []int{360, 480, 720, 1080} {
			result = append(result, calibrationCase{class: SourceH264HD, source: source, plan: Plan{Height: height}})
		}
		return result
	}
	profile := func(height int, age time.Duration) ProfileCapacity {
		return ProfileCapacity{SourceClass: SourceH264HD, TargetHeight: height, SourceFPS: video.FrameRate,
			LastCompleted: now.Add(-age)}
	}
	history := []ProfileCapacity{profile(360, time.Hour), profile(480, 24*time.Hour), profile(720, 48*time.Hour)}
	due := dueFullCalibrationCases(cases(), history, now)
	if len(due) != 3 || due[0].plan.Height != 1080 || due[1].plan.Height != 720 || due[2].plan.Height != 480 {
		t.Fatalf("expected missing then oldest cases, got %+v", due)
	}
	// A completed failure also counts as completed. Interrupted 720p/480p
	// cases keep their old timestamps and are retried.
	history = append(history, profile(1080, 0))
	due = dueFullCalibrationCases(cases(), history, now)
	if len(due) != 2 || due[0].plan.Height != 720 || due[1].plan.Height != 480 {
		t.Fatalf("resume repeated a finished case: %+v", due)
	}
}

func TestPreferredPipelineDoesNotTradeCheaperHDRForExactMeasuredHeight(t *testing.T) {
	source, _ := detailsFixture()
	video := &source.Streams[0]
	video.Width, video.Height, video.HDRStaticMetadata = 3840, 2160, true
	cpu := ProfileCapacity{SourceClass: SourceHEVCPQ, Encoder: EncoderIntelQSV, HardwareDecode: true,
		ToneMapper: "cpu", SourceFPS: 30, SourceWidth: 3840, SourceHeight: 2160,
		Height: 1080, FPS: 30, Verified: true, Threads: 2, Streams: 1, SingleSpeed: 2, CPUCores: 1.2}
	gpu := cpu
	gpu.Height, gpu.Streams, gpu.ToneMapper, gpu.CPUCores = 2160, 2, "gpu", 0.2
	c := &Capacity{profiles: []ProfileCapacity{cpu, gpu}}
	for _, height := range []int{720, 1080} {
		got, found := c.PreferredPipeline(source, EncoderIntelQSV, height)
		if !found || got.ToneMapper != "gpu" {
			t.Fatalf("%dp selected %+v, want GPU tone mapping", height, got)
		}
	}
}

func TestPartialFullCalibrationKeepsValidatedQuickRungs(t *testing.T) {
	baseline := ProfileCapacity{SourceClass: SourceHEVCSDR, TargetHeight: 360, Verified: true, Streams: 2}
	start := baseline
	start.TargetHeight = 720
	fullBaseline := baseline
	fullBaseline.Streams = 4
	merged := fillCalibrationGaps([]ProfileCapacity{fullBaseline}, []ProfileCapacity{baseline, start})
	if len(merged) != 2 || merged[0].Streams != 4 || merged[1].TargetHeight != 720 || merged[1].Streams != 2 {
		t.Fatalf("expected full 360p and quick 720p evidence, got %+v", merged)
	}
}
