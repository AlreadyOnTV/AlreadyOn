package playback

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/alreadyonapp/alreadyon/server/internal/library"
)

// VideoDetails and AudioDetails describe one side (source or output) of a
// stream for the player's Show Playback Details panel.
type VideoDetails struct {
	Codec        string  `json:"codec"`
	Profile      string  `json:"profile,omitempty"`
	Width        int     `json:"width"`
	Height       int     `json:"height"`
	FPS          float64 `json:"fps"`
	Bitrate      int64   `json:"bitrate"` // bits per second; 0 unknown
	PixelFormat  string  `json:"pixelFormat,omitempty"`
	DynamicRange string  `json:"dynamicRange"` // SDR, HDR10/PQ or HLG
	// Method is how the picture reaches the player: Direct, Copied or Converted.
	Method string `json:"method"`
}

type AudioDetails struct {
	Codec      string `json:"codec"`
	Profile    string `json:"profile,omitempty"`
	Channels   int    `json:"channels"`
	Layout     string `json:"layout,omitempty"`
	SampleRate int    `json:"sampleRate"`
	Bitrate    int64  `json:"bitrate"`
	Language   string `json:"language,omitempty"`
	Method     string `json:"method"`
}

// Details is everything the player shows about how a file became a stream.
type Details struct {
	FileName    string        `json:"fileName"`
	Container   string        `json:"container"`
	SourceVideo VideoDetails  `json:"sourceVideo"`
	SourceAudio *AudioDetails `json:"sourceAudio,omitempty"`
	OutputVideo VideoDetails  `json:"outputVideo"`
	OutputAudio *AudioDetails `json:"outputAudio,omitempty"`
	VideoReason string        `json:"videoReason"`
	AudioReason string        `json:"audioReason,omitempty"`
	// Pipeline names where decoding, scaling, tone mapping and encoding run.
	Pipeline Pipeline `json:"pipeline"`
}

// RuntimeStats are live measurements from the FFmpeg process serving a shared
// rendition. Nil means the host or process has not supplied a reliable value.
type RuntimeStats struct {
	CPUUtil *float64 `json:"cpuUtil,omitempty"`
}

// EncoderLabel is the display name of an encoder choice; "" is software.
func EncoderLabel(encoder string) string {
	switch encoder {
	case EncoderIntelQSV:
		return "Intel Quick Sync (QSV)"
	case EncoderIntelVAAPI:
		return "Intel VAAPI"
	case EncoderNvidiaNVENC:
		return "NVIDIA NVENC"
	case EncoderAMDVAAPI:
		return "AMD VAAPI"
	}
	return "Software (CPU)"
}

func dynamicRange(transfer string) string {
	switch transfer {
	case "smpte2084":
		return "HDR10 (PQ)"
	case "arib-std-b67":
		return "HLG"
	}
	return "SDR"
}

// Describe explains a plan: what the source is, what the player gets, and why
// anything was converted. It only reads what Choose already decided.
func Describe(path string, source library.ProbeResult, plan Plan, caps Capabilities) Details {
	v, a := Tracks(source)
	// Matroska usually reports only a container bitrate, as Choose assumes.
	if v.BitRate <= 0 && source.BitRate > 0 {
		v.BitRate = source.BitRate
	}
	d := Details{FileName: filepath.Base(path), Container: source.Container}
	d.SourceVideo = VideoDetails{Codec: v.Codec, Profile: v.Profile, Width: v.Width, Height: v.Height, FPS: v.FrameRate,
		Bitrate: v.BitRate, PixelFormat: v.PixelFormat, DynamicRange: dynamicRange(v.ColorTransfer)}
	if v.DolbyVisionProfile > 0 {
		d.SourceVideo.DynamicRange = fmt.Sprintf("Dolby Vision (profile %d)", v.DolbyVisionProfile)
	} else if v.HDR10Plus {
		d.SourceVideo.DynamicRange = "HDR10+"
	}
	if a.Index >= 0 {
		d.SourceAudio = &AudioDetails{Codec: a.Codec, Profile: a.Profile, Channels: a.Channels, Layout: a.ChannelLayout,
			SampleRate: a.SampleRate, Bitrate: a.BitRate, Language: a.Language}
	}

	d.OutputVideo, d.OutputAudio = d.SourceVideo, d.SourceAudio
	switch {
	case plan.Mode == "direct":
		d.OutputVideo.Method = "Direct"
	case plan.VideoCopy:
		d.OutputVideo.Method = "Copied"
	default:
		d.OutputVideo = VideoDetails{Codec: "h264", Profile: plan.Profile, Width: plan.Width, Height: plan.Height, FPS: plan.FPS,
			Bitrate: plan.VideoBitrate, PixelFormat: "yuv420p", DynamicRange: "SDR", Method: "Converted"}
	}
	d.SourceVideo.Method = ""
	if d.SourceAudio != nil {
		out := *d.SourceAudio
		switch {
		case plan.Mode == "direct":
			out.Method = "Direct"
		case plan.AudioCopy:
			out.Method = "Copied"
		default:
			channels := plan.AudioChannels
			if channels == 0 {
				channels = 2
			}
			out = AudioDetails{Codec: "aac", Profile: "LC", Channels: channels, Layout: map[int]string{2: "stereo", 6: "5.1", 8: "7.1"}[channels], SampleRate: 48000, Bitrate: plan.AudioBitrate, Language: a.Language, Method: "Converted"}
		}
		d.OutputAudio = &out
		d.SourceAudio.Method = ""
	}
	d.Pipeline = DescribePipeline(plan)
	d.VideoReason = videoReason(v, plan, caps)
	if a.Index >= 0 {
		d.AudioReason = audioReason(a, plan, caps)
	}
	return d
}

func videoReason(v library.StreamInfo, p Plan, caps Capabilities) string {
	switch {
	case p.Mode == "direct":
		return "Direct Play"
	case p.VideoCopy:
		return "Container remux required; video copied without conversion."
	}
	var why []string
	if IsHDRTransfer(v.ColorTransfer) {
		why = append(why, "this HDR source requires the SDR conversion path for this client, container, or playback mode")
	}
	if caps.MaxHeight > 0 && v.Height > caps.MaxHeight {
		why = append(why, fmt.Sprintf("the Quality menu limits it to %dp", caps.MaxHeight))
	}
	if !IsHDRTransfer(v.ColorTransfer) && !supportsVideo(v, caps.Video) {
		why = append(why, fmt.Sprintf("this browser can't play %s at this profile, size, bit depth or bitrate", strings.ToUpper(v.Codec)))
	} else if v.Codec != "h264" && !IsHDRTransfer(v.ColorTransfer) {
		why = append(why, "streams carry H.264 video")
	}
	if strings.Contains(p.Reason, "accurate scheduled seek") {
		why = append(why, "joining mid-program needs an exact cut, which a copy can't give")
	}
	if len(why) == 0 {
		why = append(why, "it is too large for the connection or was retried with conversion")
	}
	text := "Converted because " + strings.Join(why, ", and ") + "."
	if p.ServerLimited {
		text += fmt.Sprintf(" The picture is also held to %dp%g because this server's measured conversion pipeline can't sustain a higher Auto quality in real time (measured every hour).", caps.ServerMaxHeight, caps.ServerMaxFPS)
	}
	return text
}

func audioReason(a library.StreamInfo, p Plan, caps Capabilities) string {
	if p.VolumeOffsetDB != 0 {
		text := fmt.Sprintf("Commercial volume offset: %+.1f dB, applied on the server", p.VolumeOffsetDB)
		if p.Normalization != nil {
			text += " after loudness normalization"
		}
		return text + "; AAC output."
	}
	if p.Normalization != nil {
		return "Server-side loudness normalization to -16 LUFS using stored full-track measurements; AAC stereo output."
	}
	switch {
	case p.Mode == "direct":
		return "Direct Play"
	case p.AudioCopy:
		return "Container remux required; audio copied without conversion."
	}
	codec := strings.ToUpper(a.Codec)
	switch {
	case !supportsAudio(a, caps.Audio):
		return fmt.Sprintf("Converted because this browser can't play %s (%d channels) as it is.", codec, a.Channels)
	case a.Codec != "aac":
		return fmt.Sprintf("Converted because streams carry AAC audio, not %s.", codec)
	}
	return "Converted because the sound could not be copied (the connection budget or a retry with conversion)."
}
