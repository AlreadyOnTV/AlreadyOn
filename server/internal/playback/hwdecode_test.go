package playback

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/library"
)

func hdrJob(t *testing.T, encoder string, hwDecode, hwToneMap bool) Job {
	t.Helper()
	src, caps := detailsFixture()
	caps.Encoder, caps.HWDecode, caps.HWToneMap = encoder, hwDecode, hwToneMap
	plan, err := Choose(src, caps, false, true)
	if err != nil {
		t.Fatal(err)
	}
	return Job{Path: "/media/m.mkv", Source: src, Plan: plan, Duration: time.Second, OutputDir: t.TempDir(), Device: "/dev/dri/renderD128"}
}

func TestForegroundReservationMatchesOnly4KMain10CPUToneMapping(t *testing.T) {
	job := hdrJob(t, EncoderIntelVAAPI, true, false)
	if !needsForegroundReservation(job) {
		t.Fatal("4K Main 10 CPU tone map did not reserve foreground CPU")
	}
	job.Plan.ToneMapper = "gpu"
	if needsForegroundReservation(job) {
		t.Fatal("GPU tone mapping reserved foreground CPU")
	}
	job.Plan.ToneMapper = "cpu"
	job.Source.Streams[0].Height = 1080
	job.Source.Streams[0].Width = 1920
	if needsForegroundReservation(job) {
		t.Fatal("1080p HDR reserved the 4K exception")
	}
	job.Source.Streams[0].Height = 2160
	job.Source.Streams[0].Width = 3840
	job.Source.Streams[0].PixelFormat = "yuv420p"
	if needsForegroundReservation(job) {
		t.Fatal("8-bit HEVC reserved the Main 10 exception")
	}
	job.Source.Streams[0].PixelFormat = "yuv420p10le"
	job.Source.Streams[0].ColorTransfer = "bt709"
	if needsForegroundReservation(job) {
		t.Fatal("SDR Main 10 reserved the HDR exception")
	}
}

func TestChoosePicksTheGPUPipeline(t *testing.T) {
	src, caps := detailsFixture()
	caps.Encoder, caps.HWDecode, caps.HWToneMap = EncoderIntelVAAPI, true, true
	if p, _ := Choose(src, caps, false, true); p.Decoder != "hardware" || p.ToneMapper != "gpu" || !p.ToneMap {
		t.Errorf("HDR10 on Intel: %+v", p)
	}
	hlg := src
	hlg.Streams = append([]library.StreamInfo(nil), src.Streams...)
	hlg.Streams[0].ColorTransfer = "arib-std-b67"
	if p, _ := Choose(hlg, caps, false, true); p.Decoder != "hardware" || p.ToneMapper != "cpu" {
		t.Errorf("HLG has no GPU tone mapper: %+v", p)
	}
	caps.HWToneMap = false
	if p, _ := Choose(src, caps, false, true); p.Decoder != "hardware" || p.ToneMapper != "cpu" {
		t.Errorf("GPU without a tone mapper: %+v", p)
	}
	caps.HWDecode = false
	if p, _ := Choose(src, caps, false, true); p.Decoder != "" || p.ToneMapper != "cpu" {
		t.Errorf("decoding switched off: %+v", p)
	}
	caps.HWDecode, caps.Encoder = true, ""
	if p, _ := Choose(src, caps, false, true); p.Decoder != "" {
		t.Errorf("GPU decoding needs a hardware encoder: %+v", p)
	}
}

func TestGPUCanDecode(t *testing.T) {
	for _, tc := range []struct {
		codec, pix string
		w, h       int
		want       bool
	}{
		{"h264", "yuv420p", 1920, 1080, true}, {"h264", "yuv420p10le", 1920, 1080, false}, {"hevc", "yuv420p10le", 3840, 2160, true},
		{"hevc", "yuv444p", 1920, 1080, false}, {"av1", "yuv420p", 1920, 1080, true}, {"mpeg4", "yuv420p", 720, 480, false},
		{"h264", "yuv420p", 16384, 8192, false}, {"h264", "yuv420p", 0, 0, false},
	} {
		if got := GPUCanDecode(library.StreamInfo{Codec: tc.codec, PixelFormat: tc.pix, Width: tc.w, Height: tc.h}); got != tc.want {
			t.Errorf("%+v = %v", tc, got)
		}
	}
}

func TestArgumentsBuildTheGPUPipelines(t *testing.T) {
	for _, tc := range []struct {
		name       string
		encoder    string
		toneMapper string
		want       []string
		notWant    []string
	}{
		{"vaapi gpu tone map", EncoderIntelVAAPI, "gpu", []string{"-hwaccel vaapi", "-hwaccel_output_format vaapi", "scale_vaapi=w=1920:h=1080:format=p010,tonemap_vaapi=format=nv12", "-c:v h264_vaapi"}, []string{"zscale", "hwdownload"}},
		{"vaapi cpu tone map", EncoderIntelVAAPI, "cpu", []string{"scale_vaapi=w=1920:h=1080:format=p010,hwdownload,format=p010le,fps=23.976000,setparams=color_primaries=bt2020:color_trc=smpte2084:colorspace=bt2020nc,zscale=tin=smpte2084", "format=nv12,setsar=1", ",hwupload", "-filter_hw_device va"}, []string{"tonemap_vaapi"}},
		{"qsv gpu tone map", EncoderIntelQSV, "gpu", []string{"-hwaccel qsv", "vpp_qsv=w=1920:h=1080:format=nv12:tonemap=1", "-c:v h264_qsv", "-forced_idr 1"}, []string{"zscale"}},
		{"qsv cpu tone map", EncoderIntelQSV, "cpu", []string{"vpp_qsv=w=1920:h=1080:format=p010,hwdownload", "hwupload=extra_hw_frames=64"}, []string{"tonemap=1"}},
		{"nvenc", EncoderNvidiaNVENC, "cpu", []string{"-hwaccel cuda", "scale_cuda=w=1920:h=1080:format=p010le,hwdownload,format=p010le", "-c:v h264_nvenc"}, []string{"hwupload", "-init_hw_device"}},
	} {
		j := hdrJob(t, tc.encoder, true, tc.toneMapper == "gpu")
		j.Plan.ToneMapper = tc.toneMapper
		args, err := Arguments(j)
		joined := strings.Join(args, " ")
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		for _, w := range tc.want {
			if !strings.Contains(joined, w) {
				t.Errorf("%s: missing %q in %s", tc.name, w, joined)
			}
		}
		for _, w := range tc.notWant {
			if strings.Contains(joined, w) {
				t.Errorf("%s: unexpected %q in %s", tc.name, w, joined)
			}
		}
		// Hardware decode options belong before the input.
		if strings.Index(joined, "-hwaccel ") > strings.Index(joined, " -i /media/m.mkv") {
			t.Errorf("%s: -hwaccel must precede -i: %s", tc.name, joined)
		}
	}
	j := hdrJob(t, EncoderIntelVAAPI, true, true)
	j.Device = "/dev/sda"
	if _, err := Arguments(j); err == nil {
		t.Error("a device outside /dev/dri must be refused for GPU decoding")
	}
}

func TestFallbackPlansStepDownOneGPUStageAtATime(t *testing.T) {
	j := hdrJob(t, EncoderIntelVAAPI, true, true)
	plans := fallbackPlans(j.Plan)
	var got []string
	for _, p := range plans {
		got = append(got, p.Encoder+"/"+p.Decoder+"/"+p.ToneMapper)
	}
	want := []string{"intel-vaapi/hardware/gpu", "intel-vaapi/hardware/cpu", "intel-vaapi//cpu", "//cpu"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("plans = %v, want %v", got, want)
	}
	soft := hdrJob(t, "", false, false)
	if n := len(fallbackPlans(soft.Plan)); n != 1 {
		t.Errorf("software needs no fallback, got %d plans", n)
	}
}

// scriptedFFmpeg writes a fake ffmpeg that fails when its arguments contain
// failOn, and otherwise writes a playlist and a segment where FFmpeg would.
func scriptedFFmpeg(t *testing.T, failOn string) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "ffmpeg")
	script := "#!/bin/sh\n"
	if failOn != "" {
		script += "case \"$*\" in *" + failOn + "*) echo 'Simulated failure' >&2; exit 1;; esac\n"
	}
	script += "for last; do :; done\ndir=$(dirname \"$last\")\nprintf x > \"$dir/segment-000000000.ts\"\n" +
		"printf '#EXTM3U\\n#EXT-X-PROGRAM-DATE-TIME:2026-01-01T00:00:00.000Z\\n#EXTINF:2.0,\\nsegment-000000000.ts\\n' > \"$last\"\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

func TestRunnerFallsBackAndBlamesOnlyWhatASimplerPlanProves(t *testing.T) {
	for _, tc := range []struct {
		name, failOn                string
		wantDecoder, wantHW, wantTM string
		decodeBlamed, encodeBlamed  bool
		fails                       bool
	}{
		{name: "nothing fails", failOn: "", wantDecoder: "hardware", wantTM: "gpu"},
		{name: "GPU tone map fails", failOn: "tonemap_vaapi", wantDecoder: "hardware", wantHW: "intel-vaapi", wantTM: "cpu"},
		{name: "GPU decode fails", failOn: "-hwaccel", wantDecoder: "", wantHW: "intel-vaapi", wantTM: "cpu", decodeBlamed: true},
		{name: "GPU encoder fails", failOn: "h264_vaapi", wantDecoder: "", wantHW: "", wantTM: "cpu", encodeBlamed: true},
		{name: "everything fails", failOn: "ffmpeg", fails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := hdrJob(t, EncoderIntelVAAPI, true, true)
			var reported []Plan
			job.Report = func(p Plan, _ Pipeline) { reported = append(reported, p) }
			var decodeBlamed, encodeBlamed bool
			failOn := tc.failOn
			if failOn == "ffmpeg" {
				failOn = "-i" // every attempt has an input
			}
			r := Runner{Executable: scriptedFFmpeg(t, failOn),
				OnDecodeFailure:   func(string, library.StreamInfo, error) { decodeBlamed = true },
				OnHardwareFailure: func(string, error) { encodeBlamed = true }}
			err := r.Run(context.Background(), job)
			if tc.fails {
				if err == nil || decodeBlamed || encodeBlamed {
					t.Fatalf("err=%v decode=%v encode=%v: a source nothing can convert is not the GPU's fault", err, decodeBlamed, encodeBlamed)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if decodeBlamed != tc.decodeBlamed || encodeBlamed != tc.encodeBlamed {
				t.Errorf("decode blamed=%v encode blamed=%v, want %v %v", decodeBlamed, encodeBlamed, tc.decodeBlamed, tc.encodeBlamed)
			}
			if tc.failOn != "" {
				last := reported[len(reported)-1]
				if last.Decoder != tc.wantDecoder || last.Encoder != tc.wantHW || last.ToneMapper != tc.wantTM {
					t.Errorf("settled on %+v, want decoder %q encoder %q tone mapper %q", last, tc.wantDecoder, tc.wantHW, tc.wantTM)
				}
			}
		})
	}
}
