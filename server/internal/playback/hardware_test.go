package playback

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func hwPlan(encoder string) Plan {
	return Plan{Mode: "transcode", Encoder: encoder, Width: 1920, Height: 1080, FPS: 30, VideoBitrate: 8000000, AudioBitrate: 192000, Profile: "high"}
}

func TestArgumentsUseTheHardwareEncoder(t *testing.T) {
	for _, tc := range []struct {
		encoder string
		want    []string
	}{
		{EncoderIntelVAAPI, []string{"-vaapi_device /dev/dri/renderD128", "format=nv12,hwupload", "-c:v h264_vaapi", "-profile:v high", "-b:v 8000000"}},
		{EncoderAMDVAAPI, []string{"-vaapi_device /dev/dri/renderD128", "-c:v h264_vaapi"}},
		{EncoderIntelQSV, []string{"-init_hw_device vaapi=va:/dev/dri/renderD128", "-init_hw_device qsv=qs@va", "-filter_hw_device qs", "hwupload=extra_hw_frames=64", "-c:v h264_qsv", "-forced_idr 1"}},
		{EncoderNvidiaNVENC, []string{"format=nv12", "-c:v h264_nvenc", "-preset p4", "-forced-idr 1"}},
	} {
		job := Job{Path: "/media/a.mkv", Source: sourceInfo(), Plan: hwPlan(tc.encoder), Duration: 10 * time.Second, OutputDir: "/tmp/out", Device: "/dev/dri/renderD128"}
		args, err := Arguments(job)
		if err != nil {
			t.Fatalf("%s: %v", tc.encoder, err)
		}
		line := strings.Join(args, " ")
		for _, want := range tc.want {
			if !strings.Contains(line, want) {
				t.Errorf("%s arguments lack %q:\n%s", tc.encoder, want, line)
			}
		}
		if strings.Contains(line, "libopenh264") || strings.Contains(line, "-pix_fmt") || strings.Count(line, "-b:v") != 1 {
			t.Errorf("%s arguments still carry the software encoder or repeat the rate:\n%s", tc.encoder, line)
		}
		if !strings.Contains(line, "-force_key_frames") || !strings.Contains(line, "-f hls") {
			t.Errorf("%s arguments lost keyframes or HLS output:\n%s", tc.encoder, line)
		}
	}
	// A VA-API or Quick Sync job needs a real render node, nothing else.
	for _, device := range []string{"", "/etc/passwd", "/dev/dri/../sda", "/dev/dri/card0"} {
		job := Job{Path: "/media/a.mkv", Source: sourceInfo(), Plan: hwPlan(EncoderIntelVAAPI), Duration: time.Second, OutputDir: "/tmp/out", Device: device}
		if _, err := Arguments(job); err == nil {
			t.Errorf("device %q accepted", device)
		}
	}
	// The ladder is software only.
	ladder := hwPlan(EncoderIntelVAAPI)
	ladder.LowWidth, ladder.LowHeight, ladder.LowBitrate = 640, 360, 1500000
	if _, err := Arguments(Job{Path: "/media/a.mkv", Source: sourceInfo(), Plan: ladder, Duration: time.Second, OutputDir: "/tmp/out", Device: "/dev/dri/renderD128"}); err == nil {
		t.Error("a hardware plan with a ladder was accepted")
	}
}

func TestChooseCarriesTheHardwareEncoderOnlyIntoConversions(t *testing.T) {
	s := sourceInfo() // 1080p60 H.264
	caps := clientCaps()
	caps.Encoder, caps.Ladder = EncoderIntelVAAPI, true
	forced, err := Choose(s, caps, false, true)
	if err != nil || forced.Mode != "transcode" || forced.Encoder != EncoderIntelVAAPI || forced.LowHeight != 0 {
		t.Fatalf("a forced conversion should use the hardware encoder with no ladder: %+v %v", forced, err)
	}
	direct, err := Choose(s, caps, true, false)
	if err != nil || direct.Mode != "direct" || direct.Encoder != "" {
		t.Fatalf("direct play never encodes: %+v %v", direct, err)
	}
	soft, err := Choose(s, clientCaps(), false, true)
	if err != nil || soft.Encoder != "" {
		t.Fatalf("no encoder unless asked: %+v %v", soft, err)
	}
}

// fakeFFmpeg records each run in a file and exits with the given status.
func fakeFFmpeg(t *testing.T, status int, message string) (exe, runs string) {
	t.Helper()
	dir := t.TempDir()
	runs = filepath.Join(dir, "runs")
	exe = filepath.Join(dir, "ffmpeg")
	script := "#!/bin/sh\necho run >> " + runs + "\necho '" + message + "' >&2\nexit " + string(rune('0'+status)) + "\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe, runs
}

func runCount(t *testing.T, runs string) int {
	t.Helper()
	raw, err := os.ReadFile(runs)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(raw), "run")
}

func TestHardwareChecksAreCachedAndForcedOnDemand(t *testing.T) {
	exe, runs := fakeFFmpeg(t, 0, "")
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	h := &Hardware{Executable: exe, Device: func() string { return "/dev/dri/renderD128" }, now: func() time.Time { return now }}
	ctx := context.Background()
	if st := h.Check(ctx, EncoderIntelVAAPI, false); !st.OK || st.Device != "/dev/dri/renderD128" {
		t.Fatalf("first check: %+v", st)
	}
	h.Check(ctx, EncoderIntelVAAPI, false)
	if runCount(t, runs) != 1 {
		t.Fatalf("a good result should be reused, ran %d times", runCount(t, runs))
	}
	h.Check(ctx, EncoderIntelVAAPI, true)
	if runCount(t, runs) != 2 {
		t.Fatalf("force should test again, ran %d times", runCount(t, runs))
	}
	now = now.Add(11 * time.Minute)
	h.Check(ctx, EncoderIntelVAAPI, false)
	if runCount(t, runs) != 3 {
		t.Fatalf("an old result should be retested, ran %d times", runCount(t, runs))
	}
	if st := h.Check(ctx, "software", true); !st.OK || runCount(t, runs) != 3 {
		t.Fatalf("software needs no test: %+v", st)
	}
}

func TestHardwareFailureFallsBackAndRecovers(t *testing.T) {
	exe, runs := fakeFFmpeg(t, 1, "[vost#0:0/h264_vaapi] Compatible profile VAProfileH264High (7) is not supported by driver.")
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	h := &Hardware{Executable: exe, Device: func() string { return "/dev/dri/renderD128" }, now: func() time.Time { return now }}
	st := h.Check(context.Background(), EncoderIntelVAAPI, false)
	if st.OK || !strings.Contains(st.Detail, "cannot encode H.264") || !strings.Contains(st.Raw, "not supported by driver") || strings.Contains(st.Detail, "vost#0") {
		t.Fatalf("failure should explain itself: %+v", st)
	}
	h.Check(context.Background(), EncoderIntelVAAPI, false)
	if runCount(t, runs) != 1 {
		t.Fatalf("a failure is remembered for a minute, ran %d times", runCount(t, runs))
	}
	now = now.Add(2 * time.Minute)
	h.Check(context.Background(), EncoderIntelVAAPI, false)
	if runCount(t, runs) != 2 {
		t.Fatalf("a failure is retried after a minute, ran %d times", runCount(t, runs))
	}
	// A stream failing marks the encoder bad without running a test.
	good, _ := fakeFFmpeg(t, 0, "")
	h2 := &Hardware{Executable: good, Device: func() string { return "/dev/dri/renderD128" }}
	h2.Check(context.Background(), EncoderNvidiaNVENC, false)
	h2.Failed(EncoderNvidiaNVENC, errors.New("playback: FFmpeg failed"))
	if st := h2.Check(context.Background(), EncoderNvidiaNVENC, false); st.OK {
		t.Fatalf("a failed stream should switch to software: %+v", st)
	}
}

func TestHardwareNeedsARenderNodeExceptNVENC(t *testing.T) {
	exe, _ := fakeFFmpeg(t, 0, "")
	h := &Hardware{Executable: exe, Device: func() string { return "" }}
	if st := h.Check(context.Background(), EncoderIntelQSV, true); st.OK || !strings.Contains(st.Detail, "/dev/dri") {
		t.Fatalf("no device should be reported: %+v", st)
	}
	if st := h.Check(context.Background(), EncoderNvidiaNVENC, true); !st.OK {
		t.Fatalf("NVENC does not use a render node: %+v", st)
	}
}

func TestHardwareDecodeTestArguments(t *testing.T) {
	for _, tc := range []struct{ encoder, device, want string }{
		{EncoderIntelVAAPI, "/dev/dri/renderD128", "-hwaccel vaapi -hwaccel_device va -hwaccel_output_format vaapi"},
		{EncoderAMDVAAPI, "/dev/dri/renderD129", "-hwaccel vaapi -hwaccel_device va -hwaccel_output_format vaapi"},
		{EncoderIntelQSV, "/dev/dri/renderD128", "-hwaccel qsv -hwaccel_device qs -hwaccel_output_format qsv"},
		{EncoderNvidiaNVENC, "", "-hwaccel cuda -hwaccel_output_format cuda"},
	} {
		args, err := HardwareDecodeTestArguments(tc.encoder, tc.device)
		joined := strings.Join(args, " ")
		if err != nil || !strings.Contains(joined, tc.want) || !strings.Contains(joined, "-i pipe:0") || !strings.Contains(joined, "-c:v h264_") {
			t.Errorf("%s: %v %v", tc.encoder, args, err)
		}
	}
	if _, err := HardwareDecodeTestArguments(EncoderIntelVAAPI, "/dev/sda"); err == nil {
		t.Error("a device outside /dev/dri must be refused")
	}
	if _, err := HardwareDecodeTestArguments("software", ""); err == nil {
		t.Error("software is not a hardware encoder")
	}
}

func TestCheckDecodeReportsPassAndFailure(t *testing.T) {
	device := func() string { return "/dev/dri/renderD128" }
	good, runs := fakeFFmpeg(t, 0, "")
	if st := (&Hardware{Executable: good, Device: device}).CheckDecode(context.Background(), EncoderIntelVAAPI); !st.OK || st.Device != "/dev/dri/renderD128" {
		t.Fatalf("%+v", st)
	}
	if b, _ := os.ReadFile(runs); strings.Count(string(b), "run") < 2 {
		t.Errorf("a decode test makes a clip and then decodes it: %q", b)
	}
	bad, _ := fakeFFmpeg(t, 1, "No VA display found for device /dev/dri/renderD128.")
	if st := (&Hardware{Executable: bad, Device: device}).CheckDecode(context.Background(), EncoderIntelVAAPI); st.OK || st.Detail == "" {
		t.Errorf("a failing decode must say why: %+v", st)
	}
	if st := (&Hardware{Executable: good}).CheckDecode(context.Background(), "software"); st.OK {
		t.Errorf("software has no hardware decode to test: %+v", st)
	}
	if st := (&Hardware{Executable: good, Device: func() string { return "" }}).CheckDecode(context.Background(), EncoderIntelQSV); st.OK {
		t.Errorf("no render node: %+v", st)
	}
}

func TestQuickSyncSessionFailureNamesTheSupportedGPUs(t *testing.T) {
	stderr := "[QSV @ 0x1] Error creating a MFX session: -9.\nFailed to set value 'qsv=hw' for option 'init_hw_device': Unknown error occurred"
	if detail, _ := hardwareHint(EncoderIntelQSV, stderr); !strings.Contains(detail, "runtime") || !strings.Contains(detail, "VA-API") {
		t.Fatalf("detail = %q, want the supported generations and the VA-API alternative", detail)
	}
	if detail, _ := hardwareHint(EncoderIntelVAAPI, stderr); strings.Contains(detail, "Tiger Lake") {
		t.Fatalf("VA-API must not get the Quick Sync explanation: %q", detail)
	}
}

// fakeQuickSync is an FFmpeg that fails the Quick Sync commands that match failWhen:
// "all" fails every one, "decode" only the whole-GPU decode test.
func fakeQuickSync(t *testing.T, failWhen string) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "ffmpeg")
	script := `#!/bin/sh
qsv=0; decode=0
for a in "$@"; do
  case "$a" in *qsv*) qsv=1;; esac
  case "$a" in -hwaccel) decode=1;; esac
done
if [ $qsv = 1 ] && { [ "` + failWhen + `" = all ] || { [ "` + failWhen + `" = decode ] && [ $decode = 1 ]; }; }; then
  echo "[QSV @ 0x1] Error creating a MFX session: -9." >&2
  exit 1
fi
exit 0
`
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

func TestIntelUsesQuickSyncWhenItFullyWorksAndVAAPIOtherwise(t *testing.T) {
	device := func() string { return "/dev/dri/renderD128" }
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		failWhen string
		decoding bool
		want     string
	}{
		{"quick sync works", "none", false, EncoderIntelQSV},
		{"quick sync works with hardware decoding", "none", true, EncoderIntelQSV},
		{"quick sync cannot start (older GPU)", "all", false, EncoderIntelVAAPI},
		{"quick sync encodes but cannot decode on the GPU", "decode", true, EncoderIntelVAAPI},
		{"quick sync decode failing does not matter without hardware decoding", "decode", false, EncoderIntelQSV},
	} {
		h := &Hardware{Executable: fakeQuickSync(t, tc.failWhen), Device: device}
		if got := h.Resolve(ctx, SettingIntel, tc.decoding); got != tc.want {
			t.Errorf("%s: Intel resolved to %q, want %q", tc.name, got, tc.want)
		}
	}

	h := &Hardware{Executable: fakeQuickSync(t, "none"), Device: device}
	for _, setting := range []string{"software", EncoderAMDVAAPI, EncoderNvidiaNVENC} {
		if got := h.Resolve(ctx, setting, true); got != setting {
			t.Errorf("%s must pass through unchanged, got %q", setting, got)
		}
	}
}

func TestIntelTestButtonNamesTheEncoderStreamsWillUse(t *testing.T) {
	device := func() string { return "/dev/dri/renderD128" }
	ctx := context.Background()

	h := &Hardware{Executable: fakeQuickSync(t, "none"), Device: device}
	if st := h.TestSetting(ctx, SettingIntel, false); !st.OK || st.Encoder != EncoderIntelQSV || !strings.Contains(st.Detail, "Quick Sync") {
		t.Errorf("quick sync: %+v", st)
	}
	h = &Hardware{Executable: fakeQuickSync(t, "all"), Device: device}
	for _, decoding := range []bool{false, true} {
		st := h.TestSetting(ctx, SettingIntel, decoding)
		if !st.OK || st.Encoder != EncoderIntelVAAPI || !strings.Contains(st.Detail, "Intel uses VA-API") || !strings.Contains(st.Detail, "runtime") {
			t.Errorf("fallback (decoding=%v): %+v", decoding, st)
		}
	}
	h = &Hardware{Executable: filepath.Join(t.TempDir(), "missing-ffmpeg"), Device: device}
	if st := h.TestSetting(ctx, SettingIntel, false); st.OK || !strings.Contains(st.Detail, "Neither Quick Sync nor VA-API") {
		t.Errorf("both failing: %+v", st)
	}
}
