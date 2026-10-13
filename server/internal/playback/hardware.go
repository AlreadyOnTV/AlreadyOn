package playback

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/logging"
)

// The hardware choices of the Transcoding setting. The administrator picks one
// having installed the host drivers and passed the device into the container.
const (
	EncoderIntelQSV    = "intel-qsv"
	EncoderIntelVAAPI  = "intel-vaapi"
	EncoderNvidiaNVENC = "nvidia-nvenc"
	EncoderAMDVAAPI    = "amd-vaapi"
)

// IsHardwareEncoder reports whether name is one of the hardware choices.
func IsHardwareEncoder(name string) bool {
	return slices.Contains([]string{EncoderIntelQSV, EncoderIntelVAAPI, EncoderNvidiaNVENC, EncoderAMDVAAPI}, name)
}

// SettingIntel is the Transcoding setting for any Intel GPU. It stands for Quick
// Sync when that works fully and for VA-API otherwise (see Resolve).
const SettingIntel = "intel"

// Resolve turns the Transcoding setting into the encoder to use. Intel picks
// Quick Sync when its encode test passes (and, with hardware decoding on, its
// whole-GPU decode test too), else VA-API, which runs on every Intel GPU the media
// driver supports. Quick Sync only starts on Tiger Lake and newer, and its
// failure is expected on older GPUs, so it is not logged as a fault. Other
// settings are returned unchanged.
func (h *Hardware) Resolve(ctx context.Context, setting string, decoding bool) string {
	if setting != SettingIntel {
		return setting
	}
	qsv := h.cached(EncoderIntelQSV, false, func() HardwareStatus { return h.test(ctx, EncoderIntelQSV) }, "")
	picked := EncoderIntelVAAPI
	switch {
	case qsv.OK && (!decoding || h.DecodeReady(ctx, EncoderIntelQSV)):
		picked = EncoderIntelQSV
	case h.Check(ctx, EncoderIntelVAAPI, false).OK:
	case qsv.OK:
		picked = EncoderIntelQSV // VA-API fails too: Quick Sync's encoder alone is still better than software
	}
	h.mu.Lock()
	changed := h.intelPick != picked
	h.intelPick = picked
	h.mu.Unlock()
	if changed {
		logging.Info("hardware transcoding: Intel uses %s", EncoderLabel(picked))
	}
	return picked
}

// TestSetting is the Server tab's Test button: it tries the setting now and says
// what it found. For Intel it tries Quick Sync, then VA-API, and names the one
// streams will use.
func (h *Hardware) TestSetting(ctx context.Context, setting string, decoding bool) HardwareStatus {
	try := func(encoder string) HardwareStatus {
		if decoding {
			return h.CheckDecode(ctx, encoder)
		}
		if setting == SettingIntel && encoder == EncoderIntelQSV {
			return h.cached(encoder, true, func() HardwareStatus { return h.test(ctx, encoder) }, "")
		}
		return h.Check(ctx, encoder, true)
	}
	if setting != SettingIntel {
		return try(setting)
	}
	qsv := try(EncoderIntelQSV)
	if qsv.OK {
		qsv.Detail = "Intel uses Quick Sync. " + qsv.Detail
		return qsv
	}
	vaapi := try(EncoderIntelVAAPI)
	if vaapi.OK {
		vaapi.Detail = "Intel uses VA-API; Quick Sync is not available here (" + qsv.Detail + ") " + vaapi.Detail
		return vaapi
	}
	vaapi.Detail = "Neither Quick Sync nor VA-API works. Quick Sync: " + qsv.Detail + " VA-API: " + vaapi.Detail
	return vaapi
}

var renderNodePattern = regexp.MustCompile(`^/dev/dri/renderD[0-9]{1,3}$`)

// RenderNode is the first DRI render node (what /dev/dri gives a container), or
// "" when there is none.
func RenderNode() string {
	nodes, _ := filepath.Glob("/dev/dri/renderD*")
	sort.Strings(nodes)
	for _, n := range nodes {
		if renderNodePattern.MatchString(n) {
			return n
		}
	}
	return ""
}

// hwSetup is what a hardware encoder adds to the FFmpeg command: global device
// options, a filter suffix that puts frames where the encoder wants them, and
// the encoder options. Decoding and scaling stay in software.
type hwSetup struct {
	global []string
	filter string
	encode []string
}

func hardwareSetup(encoder, device, profile string, bitrate int64) (hwSetup, error) {
	rate := func() []string {
		b := strconv.FormatInt(bitrate, 10)
		return []string{"-b:v", b, "-maxrate", b, "-bufsize", strconv.FormatInt(bitrate*2, 10)}
	}
	// QSV and NVENC name the lowest H.264 profile "baseline".
	nvProfile := strings.Replace(profile, "constrained_baseline", "baseline", 1)
	switch encoder {
	case EncoderIntelVAAPI, EncoderAMDVAAPI:
		if !renderNodePattern.MatchString(device) {
			return hwSetup{}, errors.New("playback: no /dev/dri render device for VA-API")
		}
		return hwSetup{
			global: []string{"-vaapi_device", device},
			filter: "format=nv12,hwupload",
			encode: append([]string{"-c:v", "h264_vaapi", "-profile:v", profile}, rate()...),
		}, nil
	case EncoderIntelQSV:
		if !renderNodePattern.MatchString(device) {
			return hwSetup{}, errors.New("playback: no /dev/dri render device for Quick Sync")
		}
		return hwSetup{
			global: []string{"-init_hw_device", "vaapi=va:" + device, "-init_hw_device", "qsv=qs@va", "-filter_hw_device", "qs"},
			filter: "format=nv12,hwupload=extra_hw_frames=64",
			// forced_idr makes -force_key_frames produce real IDR frames; without it a seek to a
			// fractional offset leaves the HLS muxer no keyframe to cut a segment at.
			encode: append([]string{"-c:v", "h264_qsv", "-profile:v", nvProfile, "-forced_idr", "1"}, rate()...),
		}, nil
	case EncoderNvidiaNVENC:
		return hwSetup{
			filter: "format=nv12",
			encode: append(append([]string{"-c:v", "h264_nvenc", "-profile:v", nvProfile, "-preset", "p4"}, rate()...), "-forced-idr", "1"),
		}, nil
	}
	return hwSetup{}, fmt.Errorf("playback: %q is not a hardware encoder", encoder)
}

// HardwareTestArguments encodes a second of synthetic video with the encoder, the
// same way a stream would, and throws it away.
func HardwareTestArguments(encoder, device string) ([]string, error) {
	hw, err := hardwareSetup(encoder, device, "high", 2000000)
	if err != nil {
		return nil, err
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}
	args = append(args, hw.global...)
	args = append(args, "-f", "lavfi", "-i", "testsrc2=duration=1:size=640x360:rate=30", "-vf", hw.filter, "-frames:v", "20")
	args = append(args, hw.encode...)
	return append(args, "-g", "30", "-f", "null", "-"), nil
}

// HardwareDecodeSampleArguments makes the short H.264 clip (in software, to
// stdout) that the decode test feeds the GPU.
func HardwareDecodeSampleArguments() []string {
	return []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-f", "lavfi", "-i", "testsrc2=duration=1:size=640x360:rate=30",
		"-c:v", "libopenh264", "-pix_fmt", "yuv420p", "-f", "mpegts", "pipe:1"}
}

// HardwareDecodeTestArguments runs the clip through the same whole-GPU path a
// stream uses (decode, scale, encode) and throws the result away. The filter
// fails unless the frames really come from the GPU, so a silent software decode
// cannot pass.
func HardwareDecodeTestArguments(encoder, device string) ([]string, error) {
	plan := Plan{Encoder: encoder, Decoder: "hardware", Width: 320, Height: 180, FPS: 30, Profile: "high", VideoBitrate: 1000000}
	g, err := gpuPipelineFor(plan, "", device)
	if err != nil {
		return nil, err
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}
	args = append(args, g.global...)
	args = append(args, g.input...)
	args = append(args, "-f", "mpegts", "-i", "pipe:0", "-vf", g.filter, "-frames:v", "20")
	args = append(args, g.encode...)
	return append(args, "-g", "30", "-f", "null", "-"), nil
}

// HardwareStatus is the outcome of trying a hardware encoder.
type HardwareStatus struct {
	Encoder string `json:"encoder"`
	Device  string `json:"device,omitempty"`
	OK      bool   `json:"ok"`
	Detail  string `json:"detail"`
	// Raw is FFmpeg's own complaint, kept for the log rather than the screen.
	Raw string `json:"-"`
}

type hardwareResult struct {
	status HardwareStatus
	at     time.Time
}

// Hardware remembers whether each hardware encoder works, so a stream chooses
// it only when it does and falls back to software otherwise.
type Hardware struct {
	Executable string // ffmpeg; defaults to "ffmpeg"
	// Device finds the render node; defaults to RenderNode.
	Device func() string
	// Timeout bounds one test; defaults to 20s.
	Timeout time.Duration

	mu            sync.Mutex
	results       map[string]hardwareResult
	decodeFailed  map[string]time.Time
	toneMapFailed map[string]time.Time
	intelPick     string
	now           func() time.Time

	filtersOnce sync.Once
	filters     string
}

const (
	hardwareOKFor         = 10 * time.Minute
	hardwareFailFor       = time.Minute
	hardwareDecodeFailFor = 10 * time.Minute
)

func (h *Hardware) clock() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

// Check returns the encoder's status, testing it unless a recent result exists
// (force always tests). Only one test runs at a time.
func (h *Hardware) Check(ctx context.Context, encoder string, force bool) HardwareStatus {
	if !IsHardwareEncoder(encoder) {
		return HardwareStatus{Encoder: encoder, OK: true, Detail: "Software encoding needs no hardware."}
	}
	return h.cached(encoder, force, func() HardwareStatus { return h.test(ctx, encoder) }, encoder)
}

// cached runs test unless a recent result for key exists, and logs once when a
// feature changes between working and unavailable.
func (h *Hardware) cached(key string, force bool, test func() HardwareStatus, name string) HardwareStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.results == nil {
		h.results = map[string]hardwareResult{}
	}
	if r, ok := h.results[key]; ok && !force {
		ttl := hardwareFailFor
		if r.status.OK {
			ttl = hardwareOKFor
		}
		if h.clock().Sub(r.at) < ttl {
			return r.status
		}
	}
	status := test()
	previous, had := h.results[key]
	h.results[key] = hardwareResult{status: status, at: h.clock()}
	if name == "" {
		return status
	}
	if status.OK && (!had || !previous.status.OK) {
		logging.Info("hardware transcoding: %s works%s", name, deviceNote(status.Device))
	} else if !status.OK && (!had || previous.status.OK) {
		logging.Warn("hardware transcoding: %s is unavailable, streams use software: %s [%s]", name, status.Detail, status.Raw)
	}
	return status
}

func deviceNote(device string) string {
	if device == "" {
		return ""
	}
	return " (" + device + ")"
}

// Failed records that a stream using the encoder failed, so the next stream uses
// software for a while.
func (h *Hardware) Failed(encoder string, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.results == nil {
		h.results = map[string]hardwareResult{}
	}
	detail, raw := hardwareHint(encoder, err.Error())
	detail = "A stream failed: " + detail
	h.results[encoder] = hardwareResult{status: HardwareStatus{Encoder: encoder, Detail: detail, Raw: raw}, at: h.clock()}
	logging.Warn("hardware transcoding: %s failed on a stream, using software for a minute: %s [%s]", encoder, detail, raw)
}

func (h *Hardware) test(ctx context.Context, encoder string) HardwareStatus {
	device := ""
	if encoder != EncoderNvidiaNVENC {
		find := h.Device
		if find == nil {
			find = RenderNode
		}
		if device = find(); device == "" {
			return HardwareStatus{Encoder: encoder, Detail: "No /dev/dri render device. Pass the GPU into the container (devices: /dev/dri:/dev/dri) or check the host driver."}
		}
	}
	args, err := HardwareTestArguments(encoder, device)
	if err != nil {
		return HardwareStatus{Encoder: encoder, Device: device, Detail: err.Error()}
	}
	timeout := h.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	exe := h.Executable
	if exe == "" {
		exe = "ffmpeg"
	}
	out, err := exec.CommandContext(ctx, exe, args...).CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if ctx.Err() != nil {
			text = "the test timed out"
		}
		detail, raw := hardwareHint(encoder, text)
		return HardwareStatus{Encoder: encoder, Device: device, Detail: detail, Raw: raw}
	}
	return HardwareStatus{Encoder: encoder, Device: device, OK: true, Detail: "Test encode succeeded."}
}

// CheckDecode runs a synthetic H.264 clip through the whole-GPU path (decode,
// scale, encode) for the encoder. It does not use the cache; it is the test button.
func (h *Hardware) CheckDecode(ctx context.Context, encoder string) HardwareStatus {
	status := h.testDecode(ctx, encoder)
	if status.OK {
		if h.ToneMapOnGPU(ctx, encoder) {
			status.Detail += " HDR10 GPU tone mapping is a candidate; HDR capacity measurements verify it separately."
		} else {
			status.Detail += " HDR tone mapping stays on the CPU with this GPU."
		}
	}
	return status
}

// DecodeReady reports whether the whole-GPU path works for the encoder, using
// the same cache and retry rules as the encoder's own self-test.
func (h *Hardware) DecodeReady(ctx context.Context, encoder string) bool {
	if !IsHardwareEncoder(encoder) {
		return false
	}
	return h.cached(encoder+"/decode", false, func() HardwareStatus { return h.testDecode(ctx, encoder) }, "hardware decoding with "+encoder).OK
}

func (h *Hardware) testDecode(ctx context.Context, encoder string) HardwareStatus {
	if !IsHardwareEncoder(encoder) {
		return HardwareStatus{Encoder: encoder, Detail: "Hardware decoding needs a hardware encoder."}
	}
	device := ""
	if encoder != EncoderNvidiaNVENC {
		find := h.Device
		if find == nil {
			find = RenderNode
		}
		if device = find(); device == "" {
			return HardwareStatus{Encoder: encoder, Detail: "No /dev/dri render device. Pass the GPU into the container (devices: /dev/dri:/dev/dri) or check the host driver."}
		}
	}
	args, err := HardwareDecodeTestArguments(encoder, device)
	if err != nil {
		return HardwareStatus{Encoder: encoder, Device: device, Detail: err.Error()}
	}
	timeout := h.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	exe := h.Executable
	if exe == "" {
		exe = "ffmpeg"
	}
	fail := func(text string) HardwareStatus {
		if ctx.Err() != nil {
			text = "the test timed out"
		}
		detail, raw := hardwareDecodeHint(encoder, text)
		return HardwareStatus{Encoder: encoder, Device: device, Detail: detail, Raw: raw}
	}
	sample, err := exec.CommandContext(ctx, exe, HardwareDecodeSampleArguments()...).Output()
	if err != nil {
		return fail("could not make the test clip: " + fmt.Sprint(err))
	}
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Stdin = bytes.NewReader(sample)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fail(strings.TrimSpace(string(out)))
	}
	return HardwareStatus{Encoder: encoder, Device: device, OK: true, Detail: "Test decode, scale and encode on the GPU succeeded (H.264)."}
}

// hardwareDecodeHint is hardwareHint for a decode failure.
func hardwareDecodeHint(encoder, stderr string) (detail, raw string) {
	detail, raw = hardwareHint(encoder, stderr)
	lower := strings.ToLower(stderr)
	if strings.Contains(lower, "no usable encoding profile") || strings.Contains(lower, "could not open encoder") || strings.Contains(lower, "encoder not found") {
		detail = "The host driver cannot decode or encode H.264 on this device (Intel needs the full media driver, AMD Mesa's VA-API drivers)."
	}
	return detail, raw
}

// ToneMapOnGPU reports whether the GPU's own HDR tone mapper is worth trying:
// Intel's VA-API and Quick Sync have one (HDR10 only) when this FFmpeg has the filter.
// AMD and NVIDIA tone map on the CPU; a stream that fails on the GPU falls back.
func (h *Hardware) ToneMapOnGPU(ctx context.Context, encoder string) bool {
	h.mu.Lock()
	at, failed := h.toneMapFailed[encoder]
	h.mu.Unlock()
	if failed && h.clock().Sub(at) < hardwareDecodeFailFor {
		return false
	}
	switch encoder {
	case EncoderIntelVAAPI:
		return h.hasFilter(ctx, "tonemap_vaapi")
	case EncoderIntelQSV:
		return h.hasFilter(ctx, "vpp_qsv")
	}
	return false
}

func (h *Hardware) hasFilter(ctx context.Context, name string) bool {
	h.filtersOnce.Do(func() {
		exe := h.Executable
		if exe == "" {
			exe = "ffmpeg"
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		out, _ := exec.CommandContext(ctx, exe, "-hide_banner", "-filters").Output()
		h.filters = string(out)
	})
	for _, line := range strings.Split(h.filters, "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[1] == name {
			return true
		}
	}
	return false
}

// DecodeAllowed says whether to try GPU decoding for the source: the codec table
// must allow it and it must not have failed on this GPU recently.
func (h *Hardware) DecodeAllowed(encoder string, v library.StreamInfo) bool {
	if !GPUCanDecode(v) {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	at, failed := h.decodeFailed[decodeKey(encoder, v)]
	return !failed || h.clock().Sub(at) >= hardwareDecodeFailFor
}

// DecodeFailed remembers that GPU decoding failed for this kind of video, so the
// next streams decode it on the CPU for a while.
func (h *Hardware) DecodeFailed(encoder string, v library.StreamInfo, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.decodeFailed == nil {
		h.decodeFailed = map[string]time.Time{}
	}
	h.decodeFailed[decodeKey(encoder, v)] = h.clock()
	_, raw := hardwareHint(encoder, err.Error())
	logging.Warn("hardware decoding: %s could not decode %s %s, using the CPU for it for %s: %s", encoder, v.Codec, v.PixelFormat, hardwareDecodeFailFor, raw)
}

func decodeKey(encoder string, v library.StreamInfo) string {
	return encoder + "|" + v.Codec + "|" + v.PixelFormat
}

// hardwareHint explains a failure: the likely cause when one is recognized
// (otherwise FFmpeg's own words), and FFmpeg's own last complaint separately.
func hardwareHint(encoder, stderr string) (detail, raw string) {
	// FFmpeg ends with boilerplate ("Nothing was written..."); its own complaint comes earlier.
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "Nothing was written") || strings.Contains(line, "Terminating thread") || strings.Contains(line, "Task finished") {
			continue
		}
		raw = line
	}
	if len(raw) > 240 {
		raw = raw[:240]
	}
	lower := strings.ToLower(stderr)
	switch {
	case encoder == EncoderIntelQSV && strings.Contains(lower, "error creating a mfx session"):
		detail = "Quick Sync could not start on this GPU. This image supports it on Intel 11th-generation (Tiger Lake) and newer GPUs, including Arc; older Intel GPUs (up to Jasper Lake and Ice Lake) use VA-API, which the Intel setting falls back to by itself."
	case strings.Contains(lower, "unknown encoder"), strings.Contains(lower, "encoder not found"):
		detail = "This FFmpeg build has no such encoder."
	case strings.Contains(lower, "not supported by driver"), strings.Contains(lower, "no usable encoding profile"),
		strings.Contains(lower, "could not open encoder") && encoder != EncoderNvidiaNVENC:
		detail = "The host driver cannot encode H.264 on this device. Intel needs the full media driver (intel-media-va-driver-non-free on Ubuntu, intel-media-driver from RPM Fusion on Fedora), and Quick Sync also the oneVPL runtime; AMD needs Mesa's VA-API drivers with H.264 encode."
	case strings.Contains(lower, "libcuda"), strings.Contains(lower, "cuda") && strings.Contains(lower, "cannot"), strings.Contains(lower, "nvenc") && strings.Contains(lower, "driver"):
		detail = "The NVIDIA driver or container runtime is not available to FFmpeg (needs the NVIDIA Container Toolkit)."
	case strings.Contains(lower, "no such file"), strings.Contains(lower, "permission denied"), strings.Contains(lower, "failed to initialise vaapi"), strings.Contains(lower, "failed to set value"):
		detail = "The device is missing or not readable (pass /dev/dri into the container and check its group permissions)."
	case raw != "":
		detail = raw
	default:
		detail = "The test encode failed."
	}
	return detail, raw
}

// ToneMapFailed leaves hardware decode/encode enabled when only HDR VPP fails.
// A bounded retry allows driver/device recovery without a process restart.
func (h *Hardware) ToneMapFailed(encoder string, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.toneMapFailed == nil {
		h.toneMapFailed = map[string]time.Time{}
	}
	if at, ok := h.toneMapFailed[encoder]; ok && h.clock().Sub(at) < hardwareDecodeFailFor {
		return
	}
	h.toneMapFailed[encoder] = h.clock()
	logging.Warn("hardware tone mapping: %s unavailable; using CPU for %s: %v", encoder, hardwareDecodeFailFor, err)
}
