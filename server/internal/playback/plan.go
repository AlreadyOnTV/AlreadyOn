package playback

import (
	"errors"
	"github.com/alreadyonapp/alreadyon/server/internal/loudness"
	"math"
	"slices"
	"strings"

	"github.com/alreadyonapp/alreadyon/server/internal/library"
)

// Capabilities is a snapshot collected at client startup, not at each tune.
// It describes tested configurations, not authentication or server policy.
type Capabilities struct {
	VolumeOffsetDB float64               `json:"-"`
	SelectAudio    bool                  `json:"-"`
	Normalization  *loudness.Measurement `json:"-"`
	Video          []VideoCapability     `json:"video"`
	Audio          []AudioCapability     `json:"audio"`
	Containers     []string              `json:"containers"`
	HLS            bool                  `json:"hls"`
	Mbps           float64               `json:"mbps"` // zero means unmeasured: assume 25 Mbps
	// MaxHeight is the viewer's chosen quality ceiling (0 = automatic). It is set
	// per tune from the request, never from the cached snapshot.
	MaxHeight int `json:"-"`
	// Ladder is the server's opt-in for a low rung on large conversions.
	Ladder bool `json:"-"`
	// ServerMaxHeight and ServerMaxFPS are the largest software conversion this
	// server keeps ahead of real time (from Capacity.Limit); zero means no limit.
	// They bound only Auto quality: a size the viewer picked is honored.
	ServerMaxHeight int     `json:"-"`
	ServerMaxFPS    float64 `json:"-"`
	// Encoder is a hardware encoder that has passed its test; empty means software.
	Encoder string `json:"-"`
	// HWDecode: Hardware Decoding is on, Encoder is set, and the GPU can decode this
	// source. HWToneMap: the GPU's own HDR tone mapper passed its test.
	HWDecode  bool `json:"-"`
	HWToneMap bool `json:"-"`
	// HDRMeasured allows a measured mixed GPU/CPU limit without capping SDR.
	HDRMeasured bool `json:"-"`
}

// QualityHeights are the quality ceilings a viewer can choose. 2160 (4K) is
// honored only while a hardware encoder is in use; otherwise streams stay at 1080.
var QualityHeights = []int{360, 480, 720, 1080, UHDHeight}

// UHDHeight is the 4K picture height, offered with hardware encoding.
const UHDHeight = 2160

// heightBitrate is the video bitrate ceiling for a chosen output height.
func heightBitrate(h int) int64 {
	switch {
	case h <= 360:
		return 1500000
	case h <= 480:
		return 2500000
	case h <= 720:
		return 5000000
	case h <= 1080:
		return 12000000
	}
	return 25000000
}

type VideoCapability struct {
	Container          string  `json:"container,omitempty"`
	ColorTransfer      string  `json:"colorTransfer,omitempty"`
	HDRMetadata        string  `json:"hdrMetadata,omitempty"`
	DolbyVisionProfile int     `json:"dolbyVisionProfile,omitempty"`
	DolbyVisionLevel   int     `json:"dolbyVisionLevel,omitempty"`
	CodecTag           string  `json:"codecTag,omitempty"`
	Codec              string  `json:"codec"`
	Profile            string  `json:"profile"`
	Level              int     `json:"level"`
	Width              int     `json:"width"`
	Height             int     `json:"height"`
	FPS                float64 `json:"fps"`
	Bitrate            int64   `json:"bitrate"`
	PixelFormat        string  `json:"pixelFormat"`
}

type AudioCapability struct {
	HLS        bool   `json:"hls,omitempty"`
	Container  string `json:"container,omitempty"`
	Profile    string `json:"profile,omitempty"`
	Spatial    bool   `json:"spatial,omitempty"`
	Codec      string `json:"codec"`
	Channels   int    `json:"channels"`
	SampleRate int    `json:"sampleRate"`
	Bitrate    int64  `json:"bitrate"`
}

// IsHDRTransfer reports whether an ffprobe color_transfer is PQ or HLG.
func IsHDRTransfer(transfer string) bool {
	return transfer == "smpte2084" || transfer == "arib-std-b67"
}

type Plan struct {
	AudioTrackIndex int                   `json:"audioTrackIndex"`
	VolumeOffsetDB  float64               `json:"volumeOffsetDb,omitempty"`
	AudioChannels   int                   `json:"audioChannels,omitempty"`
	Normalization   *loudness.Measurement `json:"normalization,omitempty"`
	ToneMap         bool                  `json:"toneMap,omitempty"` // convert HDR (PQ/HLG, BT.2020) video to SDR
	Mode            string                `json:"mode"`              // direct, remux, transcode
	VideoCopy       bool                  `json:"videoCopy"`
	AudioCopy       bool                  `json:"audioCopy"`
	Width           int                   `json:"width"`
	Height          int                   `json:"height"`
	FPS             float64               `json:"fps"`
	VideoBitrate    int64                 `json:"videoBitrate"`
	AudioBitrate    int64                 `json:"audioBitrate"`
	Profile         string                `json:"profile"`
	Reason          string                `json:"reason"`
	// The low rung of a two-rendition ladder: a small copy of the picture that
	// is ready sooner than the main one, so a stream can start on it and move up.
	LowWidth   int   `json:"lowWidth,omitempty"`
	LowHeight  int   `json:"lowHeight,omitempty"`
	LowBitrate int64 `json:"lowBitrate,omitempty"`
	// Encoder is the hardware encoder used for the video; empty is software.
	Encoder string `json:"encoder,omitempty"`
	// Decoder is "hardware" when the GPU decodes and scales (needs Encoder); empty is the CPU.
	Decoder string `json:"decoder,omitempty"`
	// ToneMapper is "gpu" or "cpu" for an HDR source that is being tone mapped.
	ToneMapper string `json:"toneMapper,omitempty"`
	// ServerLimited: the picture is smaller or slower than the client and network
	// allow because software encoding on this server cannot go faster in real time.
	ServerLimited bool `json:"serverLimited,omitempty"`
}

// lowRungHeight is the small rendition's height; a ladder is only worth its
// encode cost when the main picture is much larger.
const lowRungHeight = 360

// ladderMinHeight is the smallest main picture that gets a low rung.
const ladderMinHeight = 720

var ErrUnsupported = errors.New("playback: no compatible output within the connection budget")

// Budget reserves 20% for other traffic. Inputs are untrusted; avoid NaN,
// infinities and overflow even when this is called independently of HTTP.
func Budget(mbps float64) int64 {
	if mbps == 0 {
		mbps = 25
	}
	if math.IsNaN(mbps) || math.IsInf(mbps, 0) || mbps < 0 || mbps > 100000 {
		return 0
	}
	return min(int64(mbps*800000), 90000000)
}

func Tracks(source library.ProbeResult) (video, audio library.StreamInfo) {
	video.Index, audio.Index = -1, -1
	for _, s := range source.Streams {
		if s.Kind == library.StreamVideo && video.Index < 0 {
			video = s
		}
		if s.Kind == library.StreamAudio && audio.Index < 0 {
			audio = s
		}
	}
	return
}

func supportsVideo(v library.StreamInfo, caps []VideoCapability) bool {
	for _, c := range caps {
		if c.CodecTag != "" && c.CodecTag != v.CodecTag {
			continue
		}
		if c.ColorTransfer != "" && c.ColorTransfer != v.ColorTransfer {
			continue
		}
		if IsHDRTransfer(v.ColorTransfer) && c.ColorTransfer != v.ColorTransfer {
			continue
		}
		if v.DolbyVisionProfile > 0 {
			if c.DolbyVisionProfile != v.DolbyVisionProfile || c.DolbyVisionLevel < v.DolbyVisionLevel || c.CodecTag != v.CodecTag {
				continue
			}
		} else if c.DolbyVisionProfile > 0 {
			continue
		}
		if v.HDR10Plus && c.HDRMetadata != "smpteSt2094-40" {
			continue
		}
		if c.Codec == v.Codec && c.Profile == v.Profile && ((v.Level > 0 && v.Level <= c.Level) || ((v.Codec == "vp9" || v.Codec == "av1") && v.Level <= 0)) &&
			v.PixelFormat == c.PixelFormat && v.Width > 0 && v.Width <= c.Width &&
			v.Height > 0 && v.Height <= c.Height && v.FrameRate > 0 && v.FrameRate <= c.FPS &&
			v.BitRate > 0 && v.BitRate <= c.Bitrate {
			return true
		}
	}
	return false
}

func supportsAudio(a library.StreamInfo, caps []AudioCapability) bool {
	if a.Index < 0 {
		return true
	}
	if a.Codec == "aac" && a.Profile != "LC" {
		return false
	}
	for _, c := range caps {
		if c.Profile != "" && c.Profile != a.Profile {
			continue
		}
		if strings.Contains(strings.ToLower(a.Profile), "atmos") && !c.Spatial {
			continue
		}
		if c.Codec == a.Codec && a.Channels > 0 && a.Channels <= c.Channels &&
			a.SampleRate > 0 && a.SampleRate <= c.SampleRate && a.BitRate > 0 && a.BitRate <= c.Bitrate {
			return true
		}
	}
	return false
}

// Legacy snapshots advertised AAC stereo for HLS. Larger layouts require an
// explicit media-source probe; file decoding alone does not qualify.
func supportsHLSAudio(a library.StreamInfo, caps []AudioCapability) bool {
	for _, c := range caps {
		if (c.HLS || (a.Channels <= 2 && c.Codec == "aac" && c.Container == "")) && supportsAudio(a, []AudioCapability{c}) {
			return true
		}
	}
	return false
}

// Choose is pure: selection uses cached client results and stored source
// metadata. allowDirect is false when the caller cannot enforce scheduled
// file handoffs; forceEncode permits bounded recovery after a decode failure.
func Choose(source library.ProbeResult, caps Capabilities, allowDirect, forceEncode bool) (Plan, error) {
	return choose(source, caps, allowDirect, forceEncode, false)
}

// ChooseScheduled disables video streamcopy at arbitrary HLS cuts until
// keyframe-aware seeking is verified. Direct file seeking remains available.
func ChooseScheduled(source library.ProbeResult, caps Capabilities, forceEncode bool, offset float64) (Plan, error) {
	return choose(source, caps, true, forceEncode, offset > 0.1)
}

func choose(source library.ProbeResult, caps Capabilities, allowDirect, forceEncode, accurateSeek bool) (Plan, error) {
	v, a := Tracks(source)
	budget := Budget(caps.Mbps)
	if v.Index < 0 || v.Width <= 0 || v.Height <= 0 || budget <= 0 || len(caps.Video) > 64 || len(caps.Audio) > 32 {
		return Plan{}, ErrUnsupported
	}
	// Preserve HDR only for a matching, explicitly reported client configuration.
	hdr := IsHDRTransfer(v.ColorTransfer)
	// Matroska usually reports only a container bitrate. It is an upper bound
	// for the video stream, so compatible video need not be converted.
	if v.BitRate <= 0 && source.BitRate > 0 {
		v.BitRate = source.BitRate
	}
	// Matroska often omits audio bitrate. Use the codec ceiling (bounded by
	// the container rate) for admission, never invent a low guessed bitrate.
	if a.BitRate <= 0 && source.BitRate > 0 {
		ceilings := map[string]int64{"ac3": 640000, "eac3": 6144000, "truehd": 18000000, "dts": 24500000, "flac": 36864000, "opus": 510000}
		if ceiling := ceilings[a.Codec]; ceiling > 0 {
			a.BitRate = min(ceiling, source.BitRate)
		}
	}
	videoOK, audioOK := supportsVideo(v, caps.Video), supportsAudio(a, caps.Audio)
	if (caps.Normalization != nil || caps.VolumeOffsetDB != 0) && a.Index >= 0 {
		audioOK = false
	}
	// Anything taller than the chosen ceiling must be converted, never copied.
	if caps.MaxHeight > 0 && v.Height > caps.MaxHeight {
		videoOK = false
	}
	// Average bitrate is not a peak guarantee: reserve a further 50% for VBR
	// bursts and mux overhead on copied sources. Unknown rates fail closed.
	rate := source.BitRate
	if rate <= 0 && v.BitRate > 0 && (a.Index < 0 || a.BitRate > 0) {
		rate = v.BitRate + a.BitRate
	}
	fits := rate > 0 && float64(rate)*1.5 <= float64(budget)
	containerOK := false
	for _, c := range strings.Split(source.Container, ",") {
		if slices.Contains(caps.Containers, c) {
			containerOK = true
		}
	}
	directVideo, directAudio := false, a.Index < 0
	for _, c := range caps.Video {
		if (c.Container == "" || slices.Contains(strings.Split(source.Container, ","), c.Container)) && supportsVideo(v, []VideoCapability{c}) {
			directVideo = true
		}
	}
	for _, c := range caps.Audio {
		if (c.Container == "" || slices.Contains(strings.Split(source.Container, ","), c.Container)) && supportsAudio(a, []AudioCapability{c}) {
			directAudio = true
		}
	}
	if allowDirect && !caps.SelectAudio && !forceEncode && containerOK && videoOK && audioOK && directVideo && directAudio && fits {
		return Plan{AudioTrackIndex: a.Index, Mode: "direct", VideoCopy: true, AudioCopy: true, Reason: "source supported within bandwidth budget"}, nil
	}
	// Profile 5 has no HDR10-compatible base layer. Never feed it through the
	// ordinary PQ tone mapper: that produces incorrect colors. A base layer that
	// is HDR10 (compatibility 1, or 6 for UHD Blu-ray profile 7), SDR (2) or HLG (4)
	// converts like any such video.
	if v.DolbyVisionProfile > 0 && !slices.Contains([]int{1, 2, 4, 6}, v.DolbyVisionCompatibility) {
		return Plan{}, errors.New("playback: this Dolby Vision source requires a compatible direct-play client; SDR conversion of its base layer is not supported")
	}
	if !caps.HLS {
		return Plan{}, ErrUnsupported
	}
	copyable := videoOK && !hdr && v.DolbyVisionProfile == 0 && v.Codec == "h264" && fits && !forceEncode && !accurateSeek
	keyframesOK, keyframeWhy := true, ""
	if copyable {
		keyframesOK, keyframeWhy = copyFitsKeyframes(source.KeyframeGap, rate)
	}
	p := Plan{AudioTrackIndex: a.Index, VolumeOffsetDB: caps.VolumeOffsetDB, Normalization: caps.Normalization, Mode: "remux", VideoCopy: copyable && keyframesOK,
		AudioCopy: a.Index < 0 || (audioOK && a.Codec == "aac" && supportsHLSAudio(a, caps.Audio) && a.BitRate < budget/4 && !forceEncode), Reason: "package as HLS"}
	if p.VideoCopy && p.AudioCopy {
		return p, nil
	}
	p.Mode, p.Reason = "transcode", "convert unsupported tracks or reduce payload"
	if p.Normalization != nil {
		p.Reason = "normalize audio loudness on the server"
	}
	if hdr {
		p.ToneMap, p.Reason = true, "tone-map HDR to SDR"
	}
	if accurateSeek && videoOK && fits && !forceEncode {
		p.Reason = "accurate scheduled seek requires video conversion"
	} else if copyable && !keyframesOK {
		p.Reason = "convert video: " + keyframeWhy
	}
	if !p.AudioCopy {
		for _, channels := range []int{8, 6, 2} {
			if channels > 2 && (a.Channels < channels || p.Normalization != nil) {
				continue
			}
			bitrate := map[int]int64{2: 192000, 6: 512000, 8: 768000}[channels]
			// Keep audio inside the connection budget, including VBR overhead.
			if bitrate*2 > budget/2 {
				continue
			}
			candidate := library.StreamInfo{Index: 0, Codec: "aac", Profile: "LC", Channels: channels, SampleRate: 48000, BitRate: bitrate}
			if supportsHLSAudio(candidate, caps.Audio) {
				p.AudioBitrate, p.AudioChannels = bitrate, channels
				break
			}
		}
		if p.AudioBitrate == 0 {
			return Plan{}, ErrUnsupported
		}
	}
	// Normalization's AAC output must fit even when the source audio was tiny.
	if (p.Normalization != nil || p.VolumeOffsetDB != 0) && p.VideoCopy && 1.5*float64(v.BitRate)+2*float64(p.AudioBitrate) > float64(budget) {
		p.VideoCopy = false
	}
	if p.VideoCopy {
		return p, nil
	}
	audioRate := p.AudioBitrate
	if p.AudioCopy {
		audioRate = a.BitRate
	}
	// AAC is variable-rate too. Reserve mux/audio overhead inside the budget.
	available := int64(float64(budget)/1.1) - audioRate*2
	if available < 300000 {
		return Plan{}, ErrUnsupported
	}
	for _, c := range caps.Video {
		if c.Codec != "h264" || c.PixelFormat != "yuv420p" || c.Width < 2 || c.Height < 2 || c.FPS <= 0 || math.IsNaN(c.FPS) || math.IsInf(c.FPS, 0) || c.Level < 31 {
			continue
		}
		profile := ""
		switch c.Profile {
		case "High":
			profile = "high"
		case "Main":
			profile = "main"
		case "Constrained Baseline":
			profile = "constrained_baseline"
		}
		if profile == "" {
			continue
		}
		// 4K only on request and only with a hardware encoder.
		maxW, maxH, rateCap := 1920, 1080, int64(12000000)
		if caps.Encoder != "" && caps.MaxHeight >= UHDHeight && c.Level >= 50 {
			maxW, maxH, rateCap = 3840, UHDHeight, 25000000
		}
		w, h := min(c.Width, maxW), min(c.Height, maxH)
		if caps.MaxHeight > 0 {
			h = min(h, caps.MaxHeight)
			w = min(w, (h*16+8)/9)
		}
		fps := math.Min(c.FPS, 60)
		if v.FrameRate > 0 {
			fps = math.Min(fps, v.FrameRate)
		}
		// Restrict output dimensions/rate to a level actually tested by client.
		if c.Level < 42 {
			fps = math.Min(fps, 30)
		}
		if c.Level < 40 {
			w, h = min(w, 1280), min(h, 720)
		}
		serverCap := (caps.Encoder == "" || (p.ToneMap && caps.HDRMeasured)) && caps.MaxHeight == 0 && caps.ServerMaxHeight > 0
		if serverCap {
			h = min(h, caps.ServerMaxHeight)
			w = min(w, (h*16+8)/9)
			fps = math.Min(fps, caps.ServerMaxFPS)
		}
		bitrate := min(available, c.Bitrate, rateCap)
		if caps.MaxHeight > 0 || serverCap {
			bitrate = min(bitrate, heightBitrate(h))
		}
		if bitrate < 300000 {
			continue
		}
		if bitrate < 3000000 {
			w, h, fps = min(w, 854), min(h, 480), math.Min(fps, 30)
		} else if bitrate < 6000000 {
			w, h, fps = min(w, 1280), min(h, 720), math.Min(fps, 30)
		}
		displayWidth := float64(v.Width)
		if v.SampleAspectRatio > 0 && v.SampleAspectRatio < 10 {
			displayWidth *= v.SampleAspectRatio
		}
		scale := math.Min(1, math.Min(float64(w)/displayWidth, float64(h)/float64(v.Height)))
		w, h = max(2, int(displayWidth*scale)/2*2), max(2, int(float64(v.Height)*scale)/2*2)
		if w*h > p.Width*p.Height || (w*h == p.Width*p.Height && fps > p.FPS) {
			p.Width, p.Height, p.FPS, p.Profile, p.VideoBitrate = w, h, fps, profile, bitrate
			p.ServerLimited = serverCap && (v.Height > caps.ServerMaxHeight || v.FrameRate > caps.ServerMaxFPS+0.01)
		}
	}
	if p.Width == 0 {
		return Plan{}, ErrUnsupported
	}
	// Tone mapping is too costly to run twice, so HDR streams stay single-rung.
	// The ladder is a software feature; hardware encoders do the main picture only.
	p.Encoder = caps.Encoder
	if p.Encoder != "" && caps.HWDecode {
		p.Decoder = "hardware"
	}
	if p.ToneMap {
		p.ToneMapper = "cpu"
		if p.Decoder == "hardware" && caps.HWToneMap && v.ColorTransfer == "smpte2084" {
			p.ToneMapper = "gpu"
		}
	}
	if caps.Ladder && caps.Encoder == "" && !p.ToneMap && p.Height >= ladderMinHeight {
		p.LowWidth = max(2, p.Width*lowRungHeight/p.Height/2*2)
		p.LowHeight, p.LowBitrate = lowRungHeight, heightBitrate(lowRungHeight)
	}
	return p, nil
}
