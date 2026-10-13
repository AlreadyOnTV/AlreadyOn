package playback

// Detail tests keep diagnostic descriptions aligned with actual playback plans.

import (
	"strings"
	"testing"

	"github.com/alreadyonapp/alreadyon/server/internal/library"
)

func detailsFixture() (library.ProbeResult, Capabilities) {
	src := library.ProbeResult{Container: "matroska,webm", BitRate: 60000000, Streams: []library.StreamInfo{
		{Index: 0, Kind: library.StreamVideo, Codec: "hevc", Profile: "Main 10", Level: 153, Width: 3840, Height: 2160, PixelFormat: "yuv420p10le", FrameRate: 23.976, ColorTransfer: "smpte2084"},
		{Index: 1, Kind: library.StreamAudio, Codec: "truehd", Channels: 8, ChannelLayout: "7.1", SampleRate: 48000, Language: "eng"},
	}}
	caps := Capabilities{HLS: true, Mbps: 100,
		Video: []VideoCapability{{Codec: "h264", Profile: "High", Level: 42, Width: 1920, Height: 1080, FPS: 60, Bitrate: 20000000, PixelFormat: "yuv420p"}},
		Audio: []AudioCapability{{Codec: "aac", Channels: 2, SampleRate: 48000, Bitrate: 320000}},
	}
	return src, caps
}

func TestDescribeExplainsAnHDRConversion(t *testing.T) {
	src, caps := detailsFixture()
	plan, err := Choose(src, caps, true, false)
	if err != nil {
		t.Fatal(err)
	}
	d := Describe("/media/Movies/Mulan/Mulan.mkv", src, plan, caps)
	if d.FileName != "Mulan.mkv" || d.SourceVideo.Codec != "hevc" || d.SourceVideo.DynamicRange != "HDR10 (PQ)" || d.SourceVideo.Bitrate != 60000000 {
		t.Errorf("source video: %+v", d)
	}
	if d.OutputVideo.Codec != "h264" || d.OutputVideo.Method != "Converted" || d.OutputVideo.DynamicRange != "SDR" || d.OutputVideo.Height != plan.Height {
		t.Errorf("output video: %+v", d.OutputVideo)
	}
	if !strings.Contains(d.VideoReason, "HDR") || !strings.Contains(d.AudioReason, "TRUEHD") {
		t.Errorf("reasons: %q / %q", d.VideoReason, d.AudioReason)
	}
	if d.OutputAudio == nil || d.OutputAudio.Codec != "aac" || d.OutputAudio.Channels != 2 || d.SourceAudio.Channels != 8 || d.Pipeline.Encoder != "Software (CPU)" || d.Pipeline.ToneMapper != "Software (CPU)" {
		t.Errorf("audio/pipeline: %+v %+v %+v", d.OutputAudio, d.SourceAudio, d.Pipeline)
	}
}

func TestDescribeNamesTheHardwareEncoderAndDirectPlay(t *testing.T) {
	src, caps := detailsFixture()
	caps.Encoder = EncoderIntelVAAPI
	plan, _ := Choose(src, caps, true, false)
	if d := Describe("/m/x.mkv", src, plan, caps); d.Pipeline.Encoder != "Intel VAAPI (GPU)" || d.Pipeline.Decoder != "Software (CPU)" {
		t.Errorf("pipeline = %+v", d.Pipeline)
	}
	direct := library.ProbeResult{Container: "mov,mp4,m4a,3gp,3g2,mj2", BitRate: 4000000, Streams: []library.StreamInfo{
		{Index: 0, Kind: library.StreamVideo, Codec: "h264", Profile: "High", Level: 40, Width: 1280, Height: 720, PixelFormat: "yuv420p", FrameRate: 24, BitRate: 3500000},
		{Index: 1, Kind: library.StreamAudio, Codec: "aac", Profile: "LC", Channels: 2, SampleRate: 48000, BitRate: 128000},
	}}
	caps.Containers = []string{"mov", "mp4"}
	plan, err := Choose(direct, caps, true, false)
	if err != nil || plan.Mode != "direct" {
		t.Fatalf("plan %+v %v", plan, err)
	}
	d := Describe("/m/y.mp4", direct, plan, caps)
	if d.OutputVideo.Method != "Direct" || d.OutputAudio.Method != "Direct" || d.VideoReason != "Direct Play" || d.AudioReason != "Direct Play" {
		t.Errorf("direct: %+v", d)
	}
}

func TestDescribeDistinguishesRemuxAndAudioOnlyConversion(t *testing.T) {
	src, caps := detailsFixture()
	src.Streams[0].Codec, src.Streams[0].Profile, src.Streams[0].PixelFormat, src.Streams[0].ColorTransfer = "h264", "High", "yuv420p", ""
	src.Streams[0].Width, src.Streams[0].Height, src.Streams[0].BitRate = 1280, 720, 3000000
	src.BitRate = 5000000
	caps.Video = []VideoCapability{{Codec: "h264", Profile: "High", Level: 42, Width: 1920, Height: 1080, FPS: 60, Bitrate: 20000000, PixelFormat: "yuv420p"}}
	plan := Plan{Mode: "remux", VideoCopy: true, AudioCopy: true}
	d := Describe("/media/remux.mkv", src, plan, caps)
	if d.OutputVideo.Method != "Copied" || d.OutputAudio == nil || d.OutputAudio.Method != "Copied" || !strings.Contains(d.VideoReason, "remux") || !strings.Contains(d.AudioReason, "remux") {
		t.Fatalf("remux details = %+v", d)
	}

	plan = Plan{Mode: "transcode", VideoCopy: true, AudioCopy: false, AudioChannels: 2, AudioBitrate: 192000}
	d = Describe("/media/audio-convert.mkv", src, plan, caps)
	if d.OutputVideo.Method != "Copied" || d.OutputAudio == nil || d.OutputAudio.Method != "Converted" || d.Pipeline.Encoder != "" || !strings.Contains(d.AudioReason, "TRUEHD") {
		t.Fatalf("audio-only conversion details = %+v", d)
	}
}
