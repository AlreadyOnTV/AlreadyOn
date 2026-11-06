package playback

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestHDRLimitMatchesPipelineAndLeavesExplicitQualityAlone(t *testing.T) {
	src, caps := detailsFixture()
	v, _ := Tracks(src)
	c := &Capacity{hdr: []HDRCapacity{{SourceFPS: 30, Encoder: EncoderIntelVAAPI, HardwareDecode: true, Transfer: "smpte2084", ToneMapper: "cpu", Threads: 2, MeasuredAt: time.Now(), Samples: []RungSample{{Height: 1080, FPS: 30, Speed: .8}, {Height: 720, FPS: 30, Speed: 1.6}}}}}
	h, f, measured, sustainable := c.HDRLimit(EncoderIntelVAAPI, true, v, 2)
	if !measured || !sustainable || h != 720 || f != 30 {
		t.Fatalf("limit %d %g measured=%t sustainable=%t", h, f, measured, sustainable)
	}
	for _, tc := range []struct {
		encoder string
		decode  bool
		threads int
	}{{EncoderIntelQSV, true, 2}, {EncoderIntelVAAPI, false, 2}, {EncoderIntelVAAPI, true, 4}} {
		if _, _, measured, _ := c.HDRLimit(tc.encoder, tc.decode, v, tc.threads); measured {
			t.Fatal("mismatched pipeline limited")
		}
	}
	caps.Encoder, caps.HWDecode, caps.HDRMeasured = EncoderIntelVAAPI, true, true
	caps.ServerMeasured, caps.ServerSustainable = true, sustainable
	caps.ServerMaxHeight, caps.ServerMaxFPS = h, f
	p, err := Choose(src, caps, false, true)
	if err != nil || p.Height != 720 || !p.ServerLimited {
		t.Fatalf("auto %+v %v", p, err)
	}
	caps.MaxHeight = 1080
	p, err = Choose(src, caps, false, true)
	if err != nil || p.Height != 720 || !p.ServerLimited {
		t.Fatalf("explicit %+v %v", p, err)
	}
	caps.MaxHeight = 0
	src.Streams[0].ColorTransfer = "bt709"
	p, err = Choose(src, caps, false, true)
	if err != nil || p.Height != 1080 {
		t.Fatalf("SDR %+v %v", p, err)
	}
	c.hdr[0].MeasuredAt = time.Now().Add(-3 * time.Hour)
	if _, _, measured, _ := c.HDRLimit(EncoderIntelVAAPI, true, v, 2); measured {
		t.Fatal("stale capacity used")
	}
}

func TestHDRLimitReportsMeasuredButUnsustainablePipeline(t *testing.T) {
	source, caps := detailsFixture()
	video, _ := Tracks(source)
	c := &Capacity{hdr: []HDRCapacity{{
		SourceFPS: 24, Encoder: EncoderIntelVAAPI, HardwareDecode: true,
		Transfer: video.ColorTransfer, ToneMapper: "gpu", Threads: 2, MeasuredAt: time.Now(),
		Samples: []RungSample{{Height: 1080, FPS: 24, Speed: 0.9}, {Height: 720, FPS: 24, Speed: 1.1}, {Height: 360, FPS: 24, Speed: 1.2}},
	}}}
	height, fps, measured, sustainable := c.HDRLimit(EncoderIntelVAAPI, true, video, 2)
	if height != 360 || fps != 24 || !measured || sustainable {
		t.Fatalf("limit=%dp%g measured=%t sustainable=%t", height, fps, measured, sustainable)
	}
	caps.Encoder, caps.HWDecode, caps.HDRMeasured = EncoderIntelVAAPI, true, true
	caps.ServerMeasured, caps.ServerSustainable = true, false
	caps.ServerMaxHeight, caps.ServerMaxFPS = height, fps
	if _, err := Choose(source, caps, false, true); !errors.Is(err, ErrCapacityUnsustainable) {
		t.Fatalf("unsustainable HDR conversion error = %v", err)
	}
}

func TestCapacityAdmissionUsesTheMatchingHDRPipeline(t *testing.T) {
	source, _ := detailsFixture()
	c := &Capacity{
		Threads: func() int { return 2 },
		memory:  func() uint64 { return 16 << 30 },
		cpus:    16,
		hdr: []HDRCapacity{
			{SourceFPS: 24, Encoder: EncoderIntelVAAPI, HardwareDecode: true, Transfer: "smpte2084", ToneMapper: "gpu", Threads: 2, MeasuredAt: time.Now(), Samples: []RungSample{{Height: 1080, FPS: 24, Speed: 5.9}}},
			{SourceFPS: 60, Encoder: EncoderIntelVAAPI, HardwareDecode: true, Transfer: "arib-std-b67", ToneMapper: "cpu", Threads: 2, MeasuredAt: time.Now(), Samples: []RungSample{{Height: 1080, FPS: 60, Speed: 0.58}, {Height: 720, FPS: 30, Speed: 1.58}}},
		},
	}
	c.Load(CapacityRecord{MeasuredAt: time.Now(), Encoder: EncoderIntelVAAPI, Samples: []RungSample{
		{SourceClass: SourceHEVCPQ, Height: 1080, FPS: 60, Speed: 0.72},
		{SourceClass: SourceHEVCPQ, Height: 720, FPS: 30, Speed: 1.10},
		{SourceClass: SourceHEVCHLG, Height: 1080, FPS: 60, Speed: 0.78},
	}})
	pq := Job{Source: source, Threads: 2, Plan: Plan{Mode: "transcode", Encoder: EncoderIntelVAAPI, Decoder: "hardware", ToneMap: true, ToneMapper: "gpu", Height: 1080, FPS: 23.976}}
	if !c.Allows(nil, pq) {
		t.Fatal("measured sustainable PQ GPU pipeline was rejected by the slower generic matrix")
	}
	pq.Plan.ToneMapper = "cpu"
	if !c.Allows(nil, pq) {
		t.Fatal("a single unmatched PQ pipeline did not receive the optimistic default")
	}
	if !c.Allows([]Job{pq}, pq) {
		t.Fatal("two unmatched PQ pipelines should fit this host's aggregate default budget")
	}
	hlgSource := source
	hlgSource.Streams[0].ColorTransfer = "arib-std-b67"
	hlgSource.Streams[0].FrameRate = 60
	hlg := Job{Source: hlgSource, Threads: 2, Plan: Plan{Mode: "transcode", Encoder: EncoderIntelVAAPI, Decoder: "hardware", ToneMap: true, ToneMapper: "cpu", Height: 720, FPS: 30}}
	if !c.Allows(nil, hlg) {
		t.Fatal("measured sustainable HLG CPU tone-map pipeline was rejected")
	}
	hlg.Plan.ToneMapper = "gpu"
	if !c.Allows(nil, hlg) {
		t.Fatal("a single unmatched HLG pipeline did not receive the optimistic default")
	}
	if !c.Allows([]Job{hlg}, hlg) {
		t.Fatal("two unmatched HLG pipelines should fit this host's aggregate default budget")
	}
	if height, fps, measured, sustainable := c.HDRLimit(EncoderIntelVAAPI, true, hlgSource.Streams[0], 2); !measured || !sustainable || height != 720 || fps != 30 {
		t.Fatalf("HLG limit=%dp%g measured=%t sustainable=%t", height, fps, measured, sustainable)
	}
}

func TestHDRMixedPipelineUsesCPUFilterThreads(t *testing.T) {
	j := hdrJob(t, EncoderIntelVAAPI, true, false)
	args, err := Arguments(j)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(args, " "), "-filter_threads 2") {
		t.Fatal("CPU tone mapping ignored the job thread ceiling")
	}
	j.Threads = 4
	args, err = Arguments(j)
	if err != nil || !strings.Contains(strings.Join(args, " "), "-filter_threads 4") {
		t.Fatalf("CPU tone mapping did not use the expanded thread ceiling: %v", err)
	}
	j.Plan.ToneMapper = "gpu"
	args, err = Arguments(j)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(args, " "), "-filter_threads 4") {
		t.Fatal("GPU path changed")
	}
}

func TestHDRFailureCacheDoesNotDisableDecoder(t *testing.T) {
	now := time.Now()
	h := &Hardware{now: func() time.Time { return now }, filters: " .. tonemap_vaapi V->V"}
	h.filtersOnce.Do(func() {})
	if !h.ToneMapOnGPU(context.Background(), EncoderIntelVAAPI) {
		t.Fatal("initial candidate unavailable")
	}
	h.ToneMapFailed(EncoderIntelVAAPI, errors.New("no HDR VPP"))
	if h.ToneMapOnGPU(context.Background(), EncoderIntelVAAPI) {
		t.Fatal("failed tone mapper retried")
	}
	src, _ := detailsFixture()
	v, _ := Tracks(src)
	if !h.DecodeAllowed(EncoderIntelVAAPI, v) {
		t.Fatal("unrelated decoder disabled")
	}
	now = now.Add(hardwareDecodeFailFor)
	if !h.ToneMapOnGPU(context.Background(), EncoderIntelVAAPI) {
		t.Fatal("retry never allowed")
	}
}

// Long-lived streams must remember unsupported VPP before viewers leave.
func TestHDRFallbackCachesFailureAtFirstSegment(t *testing.T) {
	exe := scriptedFFmpeg(t, "tonemap_vaapi")
	f, err := os.OpenFile(exe, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("sleep 2\n")
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	notified := make(chan struct{}, 1)
	r := Runner{Executable: exe, OnToneMapFailure: func(string, error) {
		select {
		case notified <- struct{}{}:
		default:
		}
	}}
	job := hdrJob(t, EncoderIntelVAAPI, true, true)
	job.Duration = 5 * time.Second
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx, job) }()
	select {
	case <-notified:
		select {
		case err := <-done:
			t.Fatalf("callback arrived only after exit: %v", err)
		default:
		}
	case <-time.After(time.Second):
		t.Fatal("working CPU fallback did not cache GPU failure promptly")
	}
	cancel()
	<-done
}

func TestHDRDropsExcessFramesBeforeCPUToneMapping(t *testing.T) {
	for _, hw := range []bool{false, true} {
		j := hdrJob(t, EncoderIntelVAAPI, hw, false)
		j.Plan.FPS = 30
		args, err := Arguments(j)
		if err != nil {
			t.Fatal(err)
		}
		var filter string
		for i := range args {
			if args[i] == "-vf" {
				filter = args[i+1]
			}
		}
		fps, tone := strings.Index(filter, "fps=30"), strings.Index(filter, "tonemap=")
		if fps < 0 || tone < 0 || fps > tone {
			t.Fatalf("excess frames reach CPU tone map: %s", filter)
		}
	}
}

func TestHDRUsesFilmRateMeasurementsWithoutCappingFasterSourcesByThem(t *testing.T) {
	src, _ := detailsFixture()
	v, _ := Tracks(src)
	c := &Capacity{hdr: []HDRCapacity{
		{SourceFPS: 24, Transfer: "smpte2084", ToneMapper: "cpu", Threads: 2, MeasuredAt: time.Now(), Samples: []RungSample{{Height: 1080, FPS: 24, Speed: 1.5}}},
		{SourceFPS: 30, Transfer: "smpte2084", ToneMapper: "cpu", Threads: 2, MeasuredAt: time.Now(), Samples: []RungSample{{Height: 1080, FPS: 30, Speed: .8}, {Height: 720, FPS: 30, Speed: 1.5}}},
	}}
	if h, _, measured, sustainable := c.HDRLimit("", false, v, 2); !measured || !sustainable || h != 1080 {
		t.Fatal("film inherited faster source limit")
	}
	v.FrameRate = 30
	if h, _, measured, sustainable := c.HDRLimit("", false, v, 2); !measured || !sustainable || h != 720 {
		t.Fatal("faster source used film measurement")
	}
	v.Codec = "av1"
	if _, _, measured, _ := c.HDRLimit("", false, v, 2); measured {
		t.Fatal("unmeasured codec used HEVC result")
	}
}
