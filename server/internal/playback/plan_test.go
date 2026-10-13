package playback

import (
	"github.com/alreadyonapp/alreadyon/server/internal/loudness"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
)

func clientCaps() Capabilities {
	return Capabilities{HLS: true, Containers: []string{"mp4"}, Video: []VideoCapability{{Codec: "h264", Profile: "High", Level: 42, Width: 1920, Height: 1080, FPS: 60, Bitrate: 20000000, PixelFormat: "yuv420p"}}, Audio: []AudioCapability{{Codec: "aac", Channels: 2, SampleRate: 48000, Bitrate: 320000}}}
}

func TestScheduledJobUsesAuthoritativeOffset(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	e := schedule.Entry{StartsAt: now.Add(-10 * time.Second), EndsAt: now.Add(20 * time.Second), SourceOffsetSeconds: 15}
	j, err := ScheduledJob(e, schedule.FixedClock{Time: now}, "/media/source.mp4", sourceInfo(), Plan{}, "/tmp/output")
	if err != nil || j.Offset != 25*time.Second || j.Duration != 20*time.Second {
		t.Fatalf("%+v %v", j, err)
	}
	if _, err := ScheduledJob(e, schedule.FixedClock{Time: e.EndsAt}, "", sourceInfo(), Plan{}, ""); err == nil {
		t.Fatal("expired entry accepted")
	}
}

func sourceInfo() library.ProbeResult {
	return library.ProbeResult{Container: "mov,mp4", BitRate: 8000000, DurationSeconds: 60, KeyframeGap: 2 * time.Second, Streams: []library.StreamInfo{
		{Index: 0, Kind: library.StreamVideo, Codec: "h264", Profile: "High", Level: 42, PixelFormat: "yuv420p", Width: 1920, Height: 1080, FrameRate: 60, BitRate: 7800000},
		{Index: 1, Kind: library.StreamAudio, Codec: "aac", Profile: "LC", Channels: 2, SampleRate: 48000, BitRate: 192000},
	}}
}

func TestChoosePreservesCompatibleTracks(t *testing.T) {
	for _, tc := range []struct {
		name, codec, container, mode string
		vc, ac                       bool
	}{
		{"direct", "aac", "mp4", "direct", true, true},
		{"remux", "aac", "matroska", "remux", true, true},
		{"audio only", "ac3", "matroska", "transcode", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := sourceInfo()
			s.Container = tc.container
			s.Streams[1].Codec = tc.codec
			p, err := Choose(s, clientCaps(), true, false)
			if err != nil || p.Mode != tc.mode || p.VideoCopy != tc.vc || p.AudioCopy != tc.ac {
				t.Fatalf("%+v %v", p, err)
			}
		})
	}
}

func TestChooseQualityAndBandwidth(t *testing.T) {
	s := sourceInfo()
	s.Streams[0].Codec = "mpeg2video"
	p, err := Choose(s, clientCaps(), true, false)
	if err != nil || p.VideoCopy || !p.AudioCopy || p.Width != 1920 || p.Height != 1080 || p.FPS != 60 {
		t.Fatalf("%+v %v", p, err)
	}
	s.Streams[0].Width, s.Streams[0].Height, s.Streams[0].FrameRate = 640, 480, 24000.0/1001
	p, err = Choose(s, clientCaps(), true, false)
	if err != nil || p.Width != 640 || p.Height != 480 || p.FPS != 24000.0/1001 {
		t.Fatalf("upscale/cadence changed: %+v %v", p, err)
	}
	c := clientCaps()
	c.Mbps = 3
	p, err = Choose(sourceInfo(), c, true, false)
	if err != nil || p.VideoCopy || p.VideoBitrate+p.AudioBitrate > Budget(3) || p.Height > 480 {
		t.Fatalf("bandwidth exceeded: %+v %v", p, err)
	}
	if Budget(0) != 20000000 || Budget(100) != 80000000 || Budget(10) != 8000000 || Budget(math.NaN()) != 0 {
		t.Fatal("invalid budget")
	}
}

func TestChooseUnknownAndUnsupported(t *testing.T) {
	s := sourceInfo()
	s.Streams[0].BitRate, s.BitRate = 0, 0
	p, err := Choose(s, clientCaps(), true, false)
	if err != nil || p.VideoCopy {
		t.Fatalf("unknown source must not be copied: %+v %v", p, err)
	}
	// Matroska typically reports only the container rate, which bounds video.
	mkv := sourceInfo()
	mkv.Container, mkv.Streams[0].BitRate, mkv.Streams[1].BitRate = "matroska,webm", 0, 0
	if p, err := Choose(mkv, clientCaps(), true, false); err != nil || !p.VideoCopy || p.AudioCopy || p.AudioBitrate != 192000 {
		t.Fatalf("container-rate video should be copied, unknown-rate audio converted: %+v %v", p, err)
	}
	mkv.BitRate = 30000000 // above the client's tested 20 Mbps decode rate
	if p, err := Choose(mkv, clientCaps(), true, false); err != nil || p.VideoCopy {
		t.Fatalf("container rate above capability copied: %+v %v", p, err)
	}
	if _, err := Choose(s, Capabilities{}, true, false); err == nil {
		t.Fatal("unsupported client accepted")
	}
	s.Streams[0].ColorTransfer = "smpte2084"
	hdr, err := Choose(s, clientCaps(), true, false)
	if err != nil || hdr.Mode != "transcode" || !hdr.ToneMap || hdr.VideoCopy {
		t.Fatalf("HDR must be converted and tone-mapped, never copied or played directly: %+v %v", hdr, err)
	}
	s.Streams[0].ColorTransfer = "arib-std-b67"
	if p, err := Choose(s, clientCaps(), true, false); err != nil || !p.ToneMap {
		t.Fatalf("HLG must be tone-mapped: %+v %v", p, err)
	}
}

func TestChoosePreservesAnamorphicAspect(t *testing.T) {
	s := sourceInfo()
	s.Streams[0].Codec = "mpeg2video"
	s.Streams[0].Width, s.Streams[0].Height, s.Streams[0].SampleAspectRatio = 720, 480, 8.0/9
	p, err := Choose(s, clientCaps(), false, false)
	if err != nil || p.Width != 640 || p.Height != 480 {
		t.Fatalf("anamorphic aspect: %+v %v", p, err)
	}
}

func TestChooseHonorsTheViewersQualityCeiling(t *testing.T) {
	s := sourceInfo() // 1080p60 H.264 in MP4: direct play by default
	auto, err := Choose(s, clientCaps(), true, false)
	if err != nil || auto.Mode != "direct" {
		t.Fatalf("automatic = %+v %v, want direct play", auto, err)
	}
	same := clientCaps()
	same.MaxHeight = 1080
	if p, err := Choose(s, same, true, false); err != nil || p.Mode != "direct" {
		t.Fatalf("a ceiling at the source height must not force a conversion: %+v %v", p, err)
	}
	for _, tc := range []struct {
		max, wantH int
		maxRate    int64
	}{{720, 720, 5000000}, {480, 480, 2500000}, {360, 360, 1500000}} {
		caps := clientCaps()
		caps.MaxHeight = tc.max
		p, err := Choose(s, caps, true, false)
		if err != nil || p.Mode != "transcode" || p.VideoCopy || p.Height != tc.wantH || p.VideoBitrate > tc.maxRate || p.Width > (tc.wantH*16+8)/9 {
			t.Errorf("ceiling %d: %+v %v, want a %dp conversion within %d bps", tc.max, p, err, tc.wantH, tc.maxRate)
		}
	}
}

func TestChoosePlansALowRungOnlyForLargeSdrConversions(t *testing.T) {
	s := sourceInfo() // 1080p60 H.264
	off, err := Choose(s, clientCaps(), false, true)
	if err != nil || off.Mode != "transcode" || off.LowHeight != 0 {
		t.Fatalf("the ladder is opt-in: %+v %v", off, err)
	}
	on := clientCaps()
	on.Ladder = true
	forced, err := Choose(s, on, false, true)
	if err != nil || forced.Mode != "transcode" || forced.Height != 1080 || forced.LowHeight != 360 || forced.LowWidth != 640 || forced.LowBitrate != 1500000 {
		t.Fatalf("1080p conversion should carry a 360p low rung: %+v %v", forced, err)
	}
	for _, ceiling := range []int{720, 480, 360} {
		caps := on
		caps.MaxHeight = ceiling
		p, err := Choose(s, caps, true, false)
		wantLadder := ceiling >= 720
		if err != nil || (p.LowHeight > 0) != wantLadder {
			t.Errorf("ceiling %d: low rung = %d, want a ladder: %v (%+v %v)", ceiling, p.LowHeight, wantLadder, p, err)
		}
	}
	hdr := sourceInfo()
	for i := range hdr.Streams {
		if hdr.Streams[i].Kind == library.StreamVideo {
			hdr.Streams[i].ColorTransfer = "smpte2084"
		}
	}
	p, err := Choose(hdr, on, true, false)
	if err != nil || !p.ToneMap || p.LowHeight != 0 {
		t.Fatalf("tone-mapped HDR must stay single-rung: %+v %v", p, err)
	}
}

func TestArgumentsBuildATwoRungLadder(t *testing.T) {
	plan := Plan{Mode: "transcode", Width: 1920, Height: 1080, FPS: 30, VideoBitrate: 8000000, AudioBitrate: 192000, Profile: "high", LowWidth: 640, LowHeight: 360, LowBitrate: 1500000}
	job := Job{Path: "/media/a.mkv", Source: sourceInfo(), Plan: plan, Duration: 10 * time.Second, OutputDir: "/tmp/out"}
	args, err := Arguments(job)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Join(args, " ")
	for _, want := range []string{"-filter_complex", "split=2[main][small]", "-map [low] -map [main]", "-b:v:0 1500000", "-b:v:1 8000000", "-var_stream_map v:0,a:0 v:1,a:1", "-master_pl_name index.m3u8", "/tmp/out/segment-%v-%09d.ts", "/tmp/out/stream-%v.m3u8"} {
		if !strings.Contains(line, want) {
			t.Errorf("ladder arguments lack %q:\n%s", want, line)
		}
	}
	for _, bad := range []func(*Plan){
		func(p *Plan) { p.LowHeight = 1080 },
		func(p *Plan) { p.LowWidth = 641 },
		func(p *Plan) { p.LowBitrate = 100 },
		func(p *Plan) { p.LowBitrate = 9000000 },
		func(p *Plan) { p.ToneMap = true },
	} {
		j := job
		bp := plan
		bad(&bp)
		j.Plan = bp
		if _, err := Arguments(j); err == nil {
			t.Errorf("invalid low rung accepted: %+v", bp)
		}
	}
	single := plan
	single.LowWidth, single.LowHeight, single.LowBitrate = 0, 0, 0
	j := job
	j.Plan = single
	args, err = Arguments(j)
	if err != nil || strings.Contains(strings.Join(args, " "), "var_stream_map") {
		t.Fatalf("a plan without a low rung must stay single-rendition: %v", err)
	}
}

func TestChoosePlansFourKOnlyOnRequestWithAHardwareEncoder(t *testing.T) {
	src, caps := detailsFixture() // 2160p HEVC HDR10
	caps.Video = []VideoCapability{
		{Codec: "h264", Profile: "High", Level: 42, Width: 1920, Height: 1080, FPS: 60, Bitrate: 20000000, PixelFormat: "yuv420p"},
		{Codec: "h264", Profile: "High", Level: 51, Width: 3840, Height: 2160, FPS: 30, Bitrate: 25000000, PixelFormat: "yuv420p"},
	}
	caps.Mbps = 200
	plan := func(encoder string, ceiling int) Plan {
		c := caps
		c.Encoder, c.MaxHeight = encoder, ceiling
		p, err := Choose(src, c, false, true)
		if err != nil {
			t.Fatalf("%q %d: %v", encoder, ceiling, err)
		}
		return p
	}
	if p := plan(EncoderIntelVAAPI, UHDHeight); p.Height != 2160 || p.Width != 3840 || p.VideoBitrate != 25000000 {
		t.Errorf("4K with a hardware encoder: %dx%d at %d", p.Width, p.Height, p.VideoBitrate)
	}
	if p := plan(EncoderIntelVAAPI, 0); p.Height > 1080 {
		t.Errorf("automatic must stay at 1080p: %dx%d", p.Width, p.Height)
	}
	if p := plan(EncoderIntelVAAPI, 1080); p.Height != 1080 {
		t.Errorf("a 1080p ceiling: %dx%d", p.Width, p.Height)
	}
	if p := plan("", UHDHeight); p.Height > 1080 {
		t.Errorf("software encoding never plans 4K: %dx%d", p.Width, p.Height)
	}
	// A client that reports no 4K decode gets the most it can play.
	caps.Video = caps.Video[:1]
	if p := plan(EncoderIntelVAAPI, UHDHeight); p.Height > 1080 {
		t.Errorf("no 4K capability: %dx%d", p.Width, p.Height)
	}
}

func TestArgumentsAcceptFourKOnlyWithAHardwareEncoder(t *testing.T) {
	job := hdrJob(t, EncoderIntelVAAPI, false, false)
	job.Plan.Width, job.Plan.Height, job.Plan.VideoBitrate = 3840, 2160, 25000000
	if _, err := Arguments(job); err != nil {
		t.Errorf("4K on a hardware encoder: %v", err)
	}
	job.Plan.Encoder = ""
	if _, err := Arguments(job); err == nil {
		t.Error("4K in software must be refused")
	}
}

func TestNormalizationConvertsOnlyAudioWhenVideoCopyIsAllowed(t *testing.T) {
	caps := clientCaps()
	caps.Normalization = &loudness.Measurement{Integrated: -25, TruePeak: -10, Range: 5, Threshold: -35, Offset: 0, GainDB: 9}
	source := sourceInfo()
	source.Container = "mp4"
	p, err := Choose(source, caps, true, false)
	if err != nil || p.Mode != "transcode" || !p.VideoCopy || p.AudioCopy || p.Normalization == nil {
		t.Fatalf("%+v %v", p, err)
	}
	args, err := Arguments(Job{Path: "/test/source.mp4", OutputDir: "/test/output", Source: source, Plan: p, Duration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-c:v copy") || !strings.Contains(joined, "measured_I=-25.000000") || !strings.Contains(joined, "-c:a aac") {
		t.Fatal(joined)
	}
	caps.Normalization = nil
	p, err = Choose(source, caps, true, false)
	if err != nil || p.Mode != "direct" {
		t.Fatal(p, err)
	}
}

func TestNormalizationHonorsBandwidthForCopiedVideo(t *testing.T) {
	source := sourceInfo()
	source.BitRate = 310000
	source.Streams[0].BitRate = 300000
	source.Streams[1].BitRate = 10000
	caps := clientCaps()
	caps.Mbps = .6
	caps.Normalization = &loudness.Measurement{Integrated: -25, TruePeak: -10, Threshold: -35}
	if _, err := Choose(source, caps, true, false); err != ErrUnsupported {
		t.Fatal("normalized audio exceeded the connection budget", err)
	}
}

func TestHomeTheaterDirectAndFallback(t *testing.T) {
	src := sourceInfo()
	src.BitRate = 40000000
	v := &src.Streams[0]
	v.Codec, v.Profile, v.PixelFormat, v.Level = "hevc", "Main 10", "yuv420p10le", 153
	v.Width, v.Height, v.BitRate, v.CodecTag, v.ColorTransfer = 3840, 2160, 38000000, "hvc1", "smpte2084"
	src.Streams[1].Codec, src.Streams[1].Profile, src.Streams[1].Channels, src.Streams[1].BitRate = "eac3", "Dolby Digital Plus + Dolby Atmos", 8, 768000
	caps := clientCaps()
	caps.Mbps = 1000
	caps.Video = append(caps.Video, VideoCapability{Codec: "hevc", Profile: "Main 10", Level: 153, Width: 3840, Height: 2160, FPS: 60, Bitrate: 60000000, PixelFormat: "yuv420p10le", Container: "mp4", CodecTag: "hvc1", ColorTransfer: "smpte2084", HDRMetadata: "smpteSt2086"})
	caps.Audio = append(caps.Audio, AudioCapability{Codec: "eac3", Channels: 8, SampleRate: 48000, Bitrate: 6144000, Container: "mp4", Spatial: true})
	assertDirect := func(want bool) {
		t.Helper()
		p, err := ChooseScheduled(src, caps, false, 300)
		if err != nil || (p.Mode == "direct") != want {
			t.Fatalf("plan=%+v error=%v, want direct=%v", p, err, want)
		}
		if !want && (!p.ToneMap || p.AudioCopy) {
			t.Fatalf("fallback must safely convert HDR and Atmos: %+v", p)
		}
	}
	assertDirect(true)
	caps.Audio[1].Spatial = false
	assertDirect(false)
	caps.Audio[1].Spatial = true
	v.HDR10Plus = true
	assertDirect(false)
	caps.Video[1].HDRMetadata = "smpteSt2094-40"
	assertDirect(true)
	caps.Normalization = &loudness.Measurement{Integrated: -22, TruePeak: -8, Range: 2, Threshold: -32}
	assertDirect(false)
	caps.Normalization = nil
	v.HDR10Plus = false
	v.DolbyVisionProfile, v.DolbyVisionLevel, v.CodecTag = 5, 9, "dvh1"
	if _, err := Choose(src, caps, true, false); err == nil {
		t.Fatal("unsupported Dolby Vision base must not be tone mapped")
	}
	caps.Video[1].DolbyVisionProfile, caps.Video[1].DolbyVisionLevel, caps.Video[1].CodecTag = 5, 9, "dvh1"
	assertDirect(true)
	if _, err := Choose(src, caps, true, true); err == nil {
		t.Fatal("recovery must not use unsafe Dolby Vision conversion")
	}
	v.DolbyVisionProfile, v.DolbyVisionCompatibility = 8, 1
	caps.Video[1].DolbyVisionProfile = 0
	assertDirect(false)
	// UHD Blu-ray remuxes (profile 7, compatibility 6) have an HDR10 base layer.
	v.DolbyVisionProfile, v.DolbyVisionCompatibility = 7, 6
	assertDirect(false)
	if Budget(1000) != 90000000 {
		t.Fatal("high throughput escaped per-stream budget")
	}
}

func TestLosslessSurroundNeedsMatchingClientAndContainer(t *testing.T) {
	for _, codec := range []string{"truehd", "dts", "flac"} {
		t.Run(codec, func(t *testing.T) {
			src := sourceInfo()
			src.Streams[1].Codec, src.Streams[1].Channels, src.Streams[1].BitRate = codec, 10, 6000000
			caps := clientCaps()
			caps.Mbps = 100
			caps.Audio = append(caps.Audio, AudioCapability{Codec: codec, Channels: 10, SampleRate: 192000, Bitrate: 24500000, Container: "mp4"})
			if p, e := Choose(src, caps, true, false); e != nil || p.Mode != "direct" {
				t.Fatalf("%+v %v", p, e)
			}
			caps.Audio[1].Channels = 8
			if p, e := Choose(src, caps, true, false); e != nil || p.AudioCopy {
				t.Fatalf("unsupported layout copied: %+v %v", p, e)
			}
			caps.Audio[1].Channels = 10
			caps.Audio[1].Container = "webm"
			if p, e := Choose(src, caps, true, false); e != nil || p.Mode == "direct" {
				t.Fatalf("container mismatch: %+v %v", p, e)
			}
		})
	}
}

func TestSurroundConversionRequiresHLSProbe(t *testing.T) {
	src := sourceInfo()
	src.Streams[1].Codec = "truehd"
	src.Streams[1].Channels = 8
	caps := clientCaps()
	caps.Mbps = 100
	caps.Audio = append(caps.Audio, AudioCapability{Codec: "aac", Channels: 8, SampleRate: 48000, Bitrate: 768000, Container: "mp4", HLS: true})
	p, e := Choose(src, caps, true, false)
	if e != nil || p.AudioChannels != 8 || p.AudioBitrate != 768000 {
		t.Fatalf("%+v %v", p, e)
	}
	caps.Audio[1].HLS = false
	p, e = Choose(src, caps, true, false)
	if e != nil || p.AudioChannels != 2 {
		t.Fatalf("file-only capability used for HLS: %+v %v", p, e)
	}
	caps.Audio[1].HLS = true
	caps.Normalization = &loudness.Measurement{Integrated: -22, TruePeak: -8, Range: 2, Threshold: -32}
	p, e = Choose(src, caps, true, false)
	if e != nil || p.AudioChannels != 2 {
		t.Fatalf("stereo measurement applied to surround: %+v %v", p, e)
	}
}

func TestSoftwareEncoderProfiles(t *testing.T) {
	args := strings.Join(softwareVideoArgs("libx264", "constrained_baseline", "1"), " ")
	if !strings.Contains(args, "-profile:v baseline") || !strings.Contains(args, "-preset veryfast -tune zerolatency") {
		t.Fatal(args)
	}
	args = strings.Join(softwareVideoArgs("libopenh264", "constrained_baseline", "1"), " ")
	if !strings.Contains(args, "-profile:v constrained_baseline") || strings.Contains(args, "-preset") {
		t.Fatal(args)
	}
}
