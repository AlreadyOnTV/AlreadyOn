package library

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/alreadyonapp/alreadyon/server/internal/governor"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// StreamKind is the ffprobe codec_type for one stream in a media file.
type StreamKind string

const (
	StreamVideo    StreamKind = "video"
	StreamAudio    StreamKind = "audio"
	StreamSubtitle StreamKind = "subtitle"
)

// StreamInfo describes one video, audio, or subtitle stream within a
// media file, as reported by ffprobe.
type StreamInfo struct {
	CodecTag                 string
	DolbyVisionProfile       int
	DolbyVisionLevel         int
	DolbyVisionCompatibility int
	HDR10Plus                bool
	Index                    int
	Kind                     StreamKind
	Codec                    string
	Width                    int    // video only
	Height                   int    // video only
	Channels                 int    // audio only
	Language                 string // from stream tags, when present
	Title                    string // from stream tags, when present
	Default                  bool   // flagged as the default track
	Forced                   bool   // flagged as forced (for example signs-only subtitles)
	Profile                  string
	Level                    int
	PixelFormat              string
	FrameRate                float64
	BitRate                  int64
	SampleRate               int
	ChannelLayout            string
	ColorTransfer            string
	SampleAspectRatio        float64
}

// ProbeResult holds technical metadata extracted from a media file via
// ffprobe.
type ProbeResult struct {
	MetadataVersion int
	DurationSeconds float64
	Container       string
	SizeBytes       int64
	Streams         []StreamInfo
	BitRate         int64
	// KeyframeGap is the longest gap between video keyframes (internal/keyframes),
	// filled in by playback planning, never stored with the probe. Zero means not
	// measured and a negative value means the measurement failed.
	KeyframeGap time.Duration `json:"-"`
}

// Probe runs ffprobe on path and returns its technical metadata. It
// invokes ffprobe as an argument-array subprocess (never through a
// shell), so path is never subject to shell interpretation regardless of
// its contents.
//
// A corrupt, unreadable, or missing file does not panic; ffprobe's own
// stderr is folded into the returned error so the caller gets an
// actionable message instead of a bare "exit status 1".
func Probe(ctx context.Context, path string) (ProbeResult, error) {
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		// Library inputs are local files, never network playlists or URLs.
		"-protocol_whitelist", "file,pipe",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		"--",
		path,
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := governor.RunCommand(ctx, cmd); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return ProbeResult{}, fmt.Errorf("library: ffprobe %s: %s", path, msg)
	}

	var raw rawProbe
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		return ProbeResult{}, fmt.Errorf("library: parse ffprobe output for %s: %w", path, err)
	}

	// Optional bounded frame sampling discovers HDR10+ metadata. A missing
	// decoder must not prevent cataloging or direct-playing the original file.
	for _, stream := range raw.Streams {
		if stream.CodecType != "video" || (stream.ColorTransfer != "smpte2084" && stream.ColorTransfer != "arib-std-b67") {
			continue
		}
		frameCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		frameCmd := exec.CommandContext(frameCtx, "ffprobe", "-v", "error", "-protocol_whitelist", "file,pipe", "-threads", "1", "-select_streams", "v:0", "-read_intervals", "%+#64", "-show_frames", "-show_entries", "frame=stream_index:frame_side_data", "-of", "json", "--", path)
		var frames bytes.Buffer
		frameCmd.Stdout = &frames
		frameErr := governor.RunCommand(frameCtx, frameCmd)
		cancel()
		if frameErr == nil {
			var sampled rawProbe
			if json.Unmarshal(frames.Bytes(), &sampled) == nil {
				raw.Frames = sampled.Frames
			}
		}
		break
	}
	return raw.toProbeResult(), nil
}

// rawProbe mirrors the subset of `ffprobe -show_format -show_streams
// -print_format json` output this package understands.
type rawProbe struct {
	Frames []struct {
		StreamIndex int           `json:"stream_index"`
		SideData    []rawSideData `json:"side_data_list"`
	} `json:"frames"`
	Streams []rawStream `json:"streams"`
	Format  rawFormat   `json:"format"`
}

type rawSideData struct {
	Type            string `json:"side_data_type"`
	DVProfile       int    `json:"dv_profile"`
	DVLevel         int    `json:"dv_level"`
	DVCompatibility int    `json:"dv_bl_signal_compatibility_id"`
}
type rawStream struct {
	CodecTag          string            `json:"codec_tag_string"`
	SideData          []rawSideData     `json:"side_data_list"`
	Index             int               `json:"index"`
	CodecName         string            `json:"codec_name"`
	CodecType         string            `json:"codec_type"`
	Width             int               `json:"width"`
	Height            int               `json:"height"`
	Channels          int               `json:"channels"`
	Profile           string            `json:"profile"`
	Level             int               `json:"level"`
	PixelFormat       string            `json:"pix_fmt"`
	FrameRate         string            `json:"avg_frame_rate"`
	BitRate           string            `json:"bit_rate"`
	SampleRate        string            `json:"sample_rate"`
	ChannelLayout     string            `json:"channel_layout"`
	ColorTransfer     string            `json:"color_transfer"`
	SampleAspectRatio string            `json:"sample_aspect_ratio"`
	Tags              map[string]string `json:"tags"`
	Disposition       map[string]int    `json:"disposition"`
}

type rawFormat struct {
	FormatName string `json:"format_name"`
	Duration   string `json:"duration"`
	Size       string `json:"size"`
	BitRate    string `json:"bit_rate"`
}

func (r rawProbe) toProbeResult() ProbeResult {
	result := ProbeResult{
		MetadataVersion: 2,
		Container:       r.Format.FormatName,
		DurationSeconds: parseFloat(r.Format.Duration),
		SizeBytes:       parseInt(r.Format.Size),
		BitRate:         parseInt(r.Format.BitRate),
	}

	for _, s := range r.Streams {
		kind := StreamKind(s.CodecType)
		switch kind {
		case StreamVideo, StreamAudio, StreamSubtitle:
		default:
			// Ignore stream types this package doesn't model (e.g. "data"
			// attachment streams) rather than failing the whole probe.
			continue
		}
		dvProfile, dvLevel, dvCompatibility, hdr10plus := 0, 0, 0, false
		sideData := append([]rawSideData(nil), s.SideData...)
		for _, frame := range r.Frames {
			if frame.StreamIndex == s.Index {
				sideData = append(sideData, frame.SideData...)
			}
		}
		for _, side := range sideData {
			if strings.Contains(side.Type, "DOVI configuration") {
				dvProfile, dvLevel, dvCompatibility = side.DVProfile, side.DVLevel, side.DVCompatibility
			}
			if strings.Contains(side.Type, "2094-40") {
				hdr10plus = true
			}
		}
		result.Streams = append(result.Streams, StreamInfo{
			CodecTag: s.CodecTag, DolbyVisionProfile: dvProfile, DolbyVisionLevel: dvLevel, DolbyVisionCompatibility: dvCompatibility, HDR10Plus: hdr10plus,
			Index:    s.Index,
			Kind:     kind,
			Codec:    s.CodecName,
			Width:    s.Width,
			Height:   s.Height,
			Channels: s.Channels,
			Language: s.Tags["language"], Title: s.Tags["title"],
			Default: s.Disposition["default"] == 1, Forced: s.Disposition["forced"] == 1,
			Profile: s.Profile, Level: s.Level, PixelFormat: s.PixelFormat,
			FrameRate: parseRate(s.FrameRate), BitRate: parseInt(s.BitRate),
			SampleRate: parseSampleRate(s.SampleRate), ChannelLayout: s.ChannelLayout,
			ColorTransfer:     s.ColorTransfer,
			SampleAspectRatio: parseRate(strings.ReplaceAll(s.SampleAspectRatio, ":", "/")),
		})
	}

	return result
}

// Parse directly at the native int width so malformed metadata cannot wrap
// on 32-bit builds. Missing, negative and overflowing rates mean unknown.
func parseSampleRate(raw string) int {
	rate, err := strconv.Atoi(raw)
	if err != nil || rate < 0 {
		return 0
	}
	return rate
}

func parseRate(s string) float64 {
	parts := strings.Split(s, "/")
	if len(parts) != 2 || parseFloat(parts[1]) <= 0 {
		return 0
	}
	return parseFloat(parts[0]) / parseFloat(parts[1])
}

func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func parseInt(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}
