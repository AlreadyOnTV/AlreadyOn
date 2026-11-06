package playback

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/library"
)

func TestChooseCapsAutoQualityAtTheServersMeasuredLimit(t *testing.T) {
	s := sourceInfo() // 1080p60
	caps := clientCaps()
	caps.ServerMaxHeight, caps.ServerMaxFPS = 720, 30
	p, err := Choose(s, caps, true, true)
	if err != nil || p.Mode != "transcode" || p.Height != 720 || p.FPS > 30 || p.VideoBitrate > 5000000 || !p.ServerLimited {
		t.Fatalf("plan = %+v %v, want a limited 720p30 conversion within 5 Mbps", p, err)
	}
	d := Describe("/m/a.mp4", s, p, caps)
	if !strings.Contains(d.VideoReason, "720p30") || !strings.Contains(d.VideoReason, "real time") {
		t.Errorf("details do not explain the cap: %q", d.VideoReason)
	}

	// Direct play needs no encoding, so the cap never applies to it.
	if p, err := Choose(s, caps, true, false); err != nil || p.Mode != "direct" {
		t.Errorf("direct play was affected: %+v %v", p, err)
	}
	// A hardware encoder, or a size the viewer picked, is not capped.
	hw := caps
	hw.Encoder = EncoderIntelVAAPI
	if p, _ := Choose(s, hw, true, true); p.Height != 1080 || p.ServerLimited {
		t.Errorf("hardware encoding was capped: %+v", p)
	}
	picked := caps
	picked.MaxHeight = 1080
	if p, _ := Choose(s, picked, true, true); p.Height != 720 || !p.ServerLimited {
		t.Errorf("an unsafe viewer target was not bounded: %+v", p)
	}
	// A source already within the limit is not "limited".
	small := sourceInfo()
	small.Streams[0].Width, small.Streams[0].Height, small.Streams[0].FrameRate = 1280, 720, 24
	if p, _ := Choose(small, caps, true, true); p.ServerLimited || p.Height != 720 {
		t.Errorf("a source within the limit was marked limited: %+v", p)
	}
}

func TestCapacityLimitAppliesOnlyToTheMeasuredSoftwareEncoder(t *testing.T) {
	var nilCapacity *Capacity
	if h, _ := nilCapacity.Limit(""); h != 0 {
		t.Fatalf("a missing capacity must not limit: %d", h)
	}
	c := &Capacity{}
	c.Load(CapacityRecord{MeasuredAt: time.Now(), MaxHeight: 720, MaxFPS: 30, Processes: 3})
	if h, f := c.Limit(""); h != 720 || f != 30 {
		t.Errorf("software limit = %dp%g, want 720p30", h, f)
	}
	if h, _ := c.Limit(EncoderIntelQSV); h != 0 {
		t.Errorf("a hardware encoder in use must not be limited by a software measurement: %d", h)
	}
	c.Load(CapacityRecord{MeasuredAt: time.Now(), Encoder: EncoderIntelQSV, MaxHeight: 0, Processes: 5})
	if h, _ := c.Limit(""); h != 0 {
		t.Errorf("a measurement for another encoder must not limit software: %d", h)
	}
}

func TestCapacityLimitForUsesTheMeasuredSourceClass(t *testing.T) {
	c := &Capacity{}
	c.Load(CapacityRecord{MeasuredAt: time.Now(), Samples: []RungSample{
		{SourceClass: SourceHEVCPQ, Height: 1080, FPS: 60, Speed: 0.8},
		{SourceClass: SourceHEVCPQ, Height: 720, FPS: 30, Speed: 1.5},
		{SourceClass: SourceHEVCPQ, Height: 480, FPS: 30, Speed: 2},
		{SourceClass: SourceH264HD, Height: 1080, FPS: 60, Speed: 2},
	}})
	hdr := library.StreamInfo{Index: 0, Kind: library.StreamVideo, Codec: "hevc", PixelFormat: "yuv420p10le", Width: 3840, Height: 2160, FrameRate: 60, ColorTransfer: "smpte2084"}
	if height, fps, measured, sustainable := c.LimitFor("", hdr); height != 720 || fps != 30 || !measured || !sustainable {
		t.Fatalf("HDR limit %dp%g measured=%t sustainable=%t", height, fps, measured, sustainable)
	}
	h264 := sourceInfo().Streams[0]
	if height, fps, measured, sustainable := c.LimitFor("", h264); height != 1080 || fps != 60 || !measured || !sustainable {
		t.Fatalf("H.264 limit %dp%g measured=%t sustainable=%t", height, fps, measured, sustainable)
	}
	if _, _, measured, _ := c.LimitFor("", library.StreamInfo{Codec: "vp9", Width: 1920, Height: 1080}); measured {
		t.Fatal("an unmeasured source class inherited another codec's result")
	}
}

func TestCapacityMarksTheSmallestRungUnsustainable(t *testing.T) {
	c := &Capacity{}
	c.Load(CapacityRecord{MeasuredAt: time.Now(), Samples: []RungSample{
		{SourceClass: SourceH264HD, Height: 1080, FPS: 60, Speed: 0.4},
		{SourceClass: SourceH264HD, Height: 720, FPS: 30, Speed: 0.7},
		{SourceClass: SourceH264HD, Height: 480, FPS: 30, Speed: 0.9},
		{SourceClass: SourceH264HD, Height: 360, FPS: 30, Speed: 1.1},
	}})
	if height, fps, measured, sustainable := c.LimitFor("", sourceInfo().Streams[0]); height != 360 || fps != 30 || !measured || sustainable {
		t.Fatalf("fallback %dp%g measured=%t sustainable=%t", height, fps, measured, sustainable)
	}
	caps := clientCaps()
	caps.ServerMeasured, caps.ServerSustainable = true, false
	source := sourceInfo()
	source.Streams[0].Codec = "mpeg2video"
	if _, err := Choose(source, caps, false, false); !errors.Is(err, ErrCapacityUnsustainable) {
		t.Fatalf("unsustainable conversion error = %v", err)
	}
}

func TestUnsustainableHDRDoesNotReduceSupportedSDRConcurrency(t *testing.T) {
	samples := []RungSample{
		{SourceClass: SourceHEVCPQ, Height: 1080, FPS: 60, Speed: 0.6},
		{SourceClass: SourceHEVCPQ, Height: 720, FPS: 30, Speed: 0.9},
		{SourceClass: SourceHEVCPQ, Height: 480, FPS: 30, Speed: 1.1},
		{SourceClass: SourceHEVCPQ, Height: 360, FPS: 30, Speed: 1.15},
	}
	for _, source := range capacitySources[2:] {
		for _, rung := range OutputRungs {
			if rung.Height > source.height {
				continue
			}
			samples = append(samples, RungSample{SourceClass: source.class, Height: rung.Height, FPS: min(rung.FPS, source.fps), Speed: 2})
		}
	}
	c := &Capacity{Threads: func() int { return 2 }, memory: func() uint64 { return 16 << 30 }, cpus: 16}
	c.Load(CapacityRecord{MeasuredAt: time.Now(), Samples: samples, MaxHeight: 1080, MaxFPS: 60})
	c.RecomputeProcesses(context.Background())
	if got := c.Processes(); got != MaxParallel {
		t.Fatalf("SDR concurrency = %d, want %d while unsustainable HDR remains separately blocked", got, MaxParallel)
	}
}

func TestCapacityAdmissionUsesSelectedRungAndMixedResourceLoad(t *testing.T) {
	c := &Capacity{Threads: func() int { return 8 }, memory: func() uint64 { return 16 << 30 }, cpus: 16}
	samples := []RungSample{
		{SourceClass: SourceHEVCPQ, Height: 1080, FPS: 60, Speed: 0.79},
		{SourceClass: SourceHEVCPQ, Height: 720, FPS: 30, Speed: 1.5},
		{SourceClass: SourceHEVCPQ, Height: 480, FPS: 30, Speed: 1.73},
		{SourceClass: SourceHEVCPQ, Height: 360, FPS: 30, Speed: 1.86},
		{SourceClass: SourceH264HD, Height: 1080, FPS: 60, Speed: 1.92},
		{SourceClass: SourceH264HD, Height: 720, FPS: 30, Speed: 5.67},
	}
	c.Load(CapacityRecord{MeasuredAt: time.Now(), Samples: samples})
	h264Job := func(height int) Job {
		return Job{Source: sourceInfo(), Plan: Plan{Mode: "transcode", Height: height, FPS: 30, VideoBitrate: heightBitrate(height)}}
	}
	if c.Allows([]Job{h264Job(1080), h264Job(1080), h264Job(1080)}, h264Job(1080)) {
		t.Fatal("four 1080p H.264 jobs exceeded the measured CPU budget")
	}
	if !c.Allows([]Job{h264Job(720), h264Job(720), h264Job(720)}, h264Job(720)) {
		t.Fatal("four lower-rung SDR jobs should fit the measured budget")
	}
	hdr := library.StreamInfo{Codec: "hevc", PixelFormat: "yuv420p10le", Width: 3840, Height: 2160, FrameRate: 60, ColorTransfer: "smpte2084"}
	hdrJob := Job{Source: library.ProbeResult{Streams: []library.StreamInfo{hdr}}, Plan: Plan{Mode: "transcode", Height: 720, FPS: 30}}
	if c.Allows([]Job{hdrJob, hdrJob}, hdrJob) {
		t.Fatal("three 720p HDR jobs exceeded the measured CPU budget")
	}
	c.memory = func() uint64 { return 1 << 30 }
	if c.Allows([]Job{hdrJob}, hdrJob) {
		t.Fatal("host-memory headroom was not applied")
	}
	c.status.Encoder = EncoderIntelVAAPI
	if !c.Allows(nil, h264Job(720)) {
		t.Fatal("an encoder-mismatched plan was rejected instead of using bounded fallback")
	}
}

func TestCapacityAllowsOneOptimisticStreamForAnUnmatchedOutputSize(t *testing.T) {
	c := &Capacity{Threads: func() int { return 2 }, memory: func() uint64 { return 16 << 30 }, cpus: 16}
	c.Load(CapacityRecord{MeasuredAt: time.Now(), Encoder: EncoderIntelQSV, Samples: []RungSample{
		{SourceClass: SourceSDRSmall, Height: 480, FPS: 30, Speed: 11.98},
	}})
	source := sourceInfo()
	source.Streams[0].Width, source.Streams[0].Height, source.Streams[0].FrameRate = 768, 576, 30000.0/1001
	job := Job{Source: source, Threads: 2, Plan: Plan{Mode: "transcode", Encoder: EncoderIntelQSV, Width: 768, Height: 576, FPS: 30000.0 / 1001, VideoBitrate: 5000000}}
	if err := c.AdmissionError(nil, job); err != nil {
		t.Fatalf("single unmatched SD conversion was rejected: %v", err)
	}
	knownSource := sourceInfo()
	known := Job{Source: knownSource, Threads: 2, Plan: Plan{Mode: "transcode", Encoder: EncoderIntelQSV, Width: 1280, Height: 720, FPS: 30, VideoBitrate: 5000000}}
	c.mu.Lock()
	c.status.Samples = append(c.status.Samples, RungSample{SourceClass: SourceH264HD, Height: 720, FPS: 30, Speed: 5.67})
	c.mu.Unlock()
	if err := c.AdmissionError([]Job{known}, job); err != nil {
		t.Fatalf("one optimistic stream could not share remaining measured budget: %v", err)
	}
	if err := c.AdmissionError([]Job{job, job, job}, job); err != nil {
		t.Fatalf("four optimistic streams did not fit the aggregate budget: %v", err)
	}
	if !errors.Is(c.AdmissionError([]Job{job, job, job, job}, job), ErrBusy) {
		t.Fatal("an optimistic stream passed the four-slot limit")
	}
	job.Plan.LowHeight = 360
	if !errors.Is(c.AdmissionError(nil, job), ErrBusy) {
		t.Fatal("an unmeasured two-rendition ladder was admitted")
	}
	job.Plan.LowHeight = 0
	c.mu.Lock()
	c.status.Samples[0].Speed = 0.8
	c.mu.Unlock()
	job.Plan.Height, job.Plan.Width, job.Plan.FPS, job.Plan.VideoBitrate = 480, 640, 30, 2500000
	if !errors.Is(c.AdmissionError(nil, job), ErrCapacityUnsustainable) {
		t.Fatal("known-below-threshold source was overridden by the optimistic fallback")
	}
	c.mu.Lock()
	c.status.Encoder = ""
	c.mu.Unlock()
	if err := c.AdmissionError(nil, job); err != nil {
		t.Fatalf("hardware pipeline was rejected by a software measurement instead of bounded fallback: %v", err)
	}
	job.Plan.Encoder = ""
	c.mu.Lock()
	c.status.Encoder = EncoderIntelQSV
	c.mu.Unlock()
	if err := c.AdmissionError(nil, job); err != nil {
		t.Fatalf("software pipeline was rejected by a hardware measurement instead of bounded fallback: %v", err)
	}
}

func TestCapacityBenchmarksEveryValidSourceOutputPairing(t *testing.T) {
	type pairing struct {
		class  SourceClass
		height int
		fps    float64
	}
	var got []pairing
	c := &Capacity{measure: func(_ context.Context, class SourceClass, rung RungSample, _ int, _, _ string) (float64, error) {
		got = append(got, pairing{class, rung.Height, rung.FPS})
		return 2, nil
	}}
	samples, err := c.benchmark(context.Background(), "")
	if err != nil || len(samples) != 18 || len(got) != 18 {
		t.Fatalf("benchmark pairings=%d samples=%d err=%v", len(got), len(samples), err)
	}
	if got[0] != (pairing{SourceHEVCPQ, 1080, 60}) || got[4] != (pairing{SourceHEVCHLG, 1080, 60}) {
		t.Fatalf("highest-cost PQ/HLG pairings were not prioritized: %+v", got[:5])
	}
	for _, sample := range samples {
		var source capacitySource
		for _, candidate := range capacitySources {
			if candidate.class == sample.SourceClass {
				source = candidate
				break
			}
		}
		if sample.SourceClass == "" || sample.Height > source.height || sample.FPS > source.fps {
			t.Errorf("invalid/upscaled pairing: %+v from %+v", sample, source)
		}
	}
}

func TestCapacityMeasuresDownTheLadderAndStopsAtTheFirstSustainedSize(t *testing.T) {
	speeds := map[int]float64{1080: 0.7, 720: 1.0, 480: 2.0, 360: 4.0} // by height; 60 fps is half as fast
	c := &Capacity{
		Threads: func() int { return 4 },
		measure: func(_ context.Context, _ SourceClass, r RungSample, _ int, _, _ string) (float64, error) {
			s := speeds[r.Height]
			if r.FPS > 30 {
				s /= 2
			}
			return s, nil
		},
		memory: func() uint64 { return 16 << 30 },
		cpus:   8,
	}
	var saved CapacityRecord
	c.Save = func(_ context.Context, r CapacityRecord) { saved = r }
	samples, err := c.benchmark(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	// 1080p60 .35x, 1080p30 .7x, 720p60 .5x, 720p30 1.0x, 480p30 2.0x: first at or above 1.3x is 480p30.
	if h, f := pickRung("", samples, SourceH264HD); h != 480 || f != 30 {
		t.Fatalf("picked %dp%g from the H.264 class, want 480p30", h, f)
	}

	c.Load(CapacityRecord{})
	c.mu.Lock()
	c.status.MeasuredAt, c.status.Samples = &time.Time{}, samples
	c.status.MaxHeight, c.status.MaxFPS = pickRung("", samples)
	c.mu.Unlock()
	c.RecomputeProcesses(context.Background())
	// The 360p30 measurement supports four conversions under the CPU budget.
	if got := c.Processes(); got != MaxParallel {
		t.Errorf("processes = %d, want %d", got, MaxParallel)
	}
	if saved.Processes != MaxParallel || saved.MaxHeight != 480 {
		t.Errorf("saved %+v, want the processes and limit stored", saved)
	}
}

func TestCapacityWhenNothingKeepsUpUsesTheSmallestSize(t *testing.T) {
	c := &Capacity{measure: func(context.Context, SourceClass, RungSample, int, string, string) (float64, error) { return 0.5, nil }}
	samples, err := c.benchmark(context.Background(), "")
	if err != nil || len(samples) != 18 {
		t.Fatalf("samples = %+v, %v; want every size tried", samples, err)
	}
	if h, f := pickRung("", samples); h != 360 || f != 30 {
		t.Errorf("picked %dp%g, want the smallest, 360p30", h, f)
	}
}

func TestCapacityHardwareMeasuresOneSizeAndSetsNoLimit(t *testing.T) {
	calls := 0
	c := &Capacity{measure: func(_ context.Context, _ SourceClass, r RungSample, threads int, encoder, _ string) (float64, error) {
		calls++
		if r.Height > 1080 || threads != DefaultThreads || encoder != EncoderIntelVAAPI {
			t.Errorf("unexpected measurement %+v threads=%d encoder=%q", r, threads, encoder)
		}
		return 6, nil
	}}
	samples, err := c.benchmark(context.Background(), EncoderIntelVAAPI)
	if err != nil || calls != 18 {
		t.Fatalf("samples %+v err %v calls %d", samples, err, calls)
	}
	if h, _ := pickRung(EncoderIntelVAAPI, samples); h != 0 {
		t.Errorf("a hardware encoder must have no size limit, got %d", h)
	}
}

func TestCapacityMeasurementFailureKeepsThePreviousResult(t *testing.T) {
	c := &Capacity{measure: func(context.Context, SourceClass, RungSample, int, string, string) (float64, error) {
		return 0, errors.New("boom")
	}}
	if _, err := c.benchmark(context.Background(), ""); err == nil {
		t.Fatal("a failed measurement must be reported")
	}
}

func TestDetectProcesses(t *testing.T) {
	for _, tc := range []struct {
		name          string
		speed         float64
		cpus, threads int
		free          uint64
		encoder       string
		want          int
	}{
		{"small NAS", 1.5, 4, 2, 16 << 30, "", 2},
		{"big desktop caps at four", 6, 18, 8, 32 << 30, "", 4},
		{"slower than real time still gets one", 0.3, 4, 4, 16 << 30, "", 1},
		{"memory bound", 6, 18, 2, 1 << 30, "", 1},
		{"never above the supported maximum", 50, 64, 2, 64 << 30, "", 4},
		{"unknown memory is conservative", 20, 16, 1, 0, EncoderNvidiaNVENC, 1},
		{"hardware respects host memory", 20, 4, 1, 1 << 20, EncoderIntelVAAPI, 1},
	} {
		if got := DetectProcesses(tc.speed, tc.cpus, tc.threads, tc.free, tc.encoder); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestEffectiveSettingsFollowTheDetectedLimitUnlessTheAdminChoseOne(t *testing.T) {
	c := &Capacity{}
	c.Load(CapacityRecord{MeasuredAt: time.Now(), Processes: 6, MaxHeight: 1080, MaxFPS: 60})
	s := DefaultSettings()
	if got := EffectiveSettings(s, c).MaxProcesses; got != MaxParallel {
		t.Errorf("auto limit = %d, want the four-process ceiling", got)
	}
	s.AutoProcesses, s.MaxProcesses = false, 3
	if got := EffectiveSettings(s, c).MaxProcesses; got != 3 {
		t.Errorf("manual limit = %d, want the administrator's 3", got)
	}
	s = DefaultSettings()
	if got := EffectiveSettings(s, &Capacity{}).MaxProcesses; got != 2 {
		t.Errorf("unmeasured auto limit = %d, want the default 2", got)
	}
	if err := EffectiveSettings(s, c).Validate(); err != nil {
		t.Errorf("effective settings must stay valid: %v", err)
	}
}

func TestCapacityMeasuresRealFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not available")
	}
	dir := t.TempDir()
	source := capacitySources[len(capacitySources)-1]
	sample, err := makeCapacitySample(t.Context(), "ffmpeg", dir, source)
	if err != nil {
		t.Fatal(err)
	}
	speed, err := measureRung(t.Context(), "ffmpeg", dir, source.class, RungSample{Height: 360, FPS: 30}, DefaultThreads, "", sample)
	if err != nil || speed <= 0 {
		t.Fatalf("complete synthetic conversion speed=%g err=%v", speed, err)
	}
	t.Logf("measured %s 854x480 H.264 SDR -> 640x360 H.264/AAC at %.2fx real time", source.class, speed)
}

func BenchmarkCapacityMatrix(b *testing.B) {
	ctx := context.Background()
	c := &Capacity{}
	tuner := &Tuner{cpus: c.cpuCount()}
	tuneSamples, workload, err := tuner.benchmark(ctx, "", c.cpuCount())
	if err != nil {
		b.Fatal(err)
	}
	threads := PickThreads(tuneSamples)
	c.Threads = func() int { return threads }
	b.Logf("thread measurement: %s; selected %d", workload, threads)
	for i := 0; i < b.N; i++ {
		samples, err := c.benchmark(ctx, "")
		if err != nil {
			b.Fatal(err)
		}
		for _, source := range capacitySources {
			classSamples := sourceSamples(samples, source.class)
			height, fps, measured, sustainable := pickSourceRung(classSamples)
			if !measured {
				b.Fatalf("missing capacity for %s", source.class)
			}
			processes := 0
			for _, sample := range classSamples {
				if sample.Speed >= CapacityRealTime {
					processes = max(processes, DetectProcesses(sample.Speed, c.cpuCount(), threads, memoryAvailable(), ""))
				}
			}
			b.Logf("%s: ceiling=%dp%g; %s; %d worker threads; max streams across rungs=%d; ceiling sustainable=%t", source.class, height, fps, describeRungs(classSamples), threads, processes, sustainable)
		}
	}
}
