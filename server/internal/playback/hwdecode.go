package playback

import (
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/alreadyonapp/alreadyon/server/internal/library"
)

// GPUCanDecode says whether a GPU can plausibly decode the video. It is a
// conservative codec table, not proof: older GPUs lack AV1 or HEVC 10-bit, and
// the runner falls back to the CPU when a decode fails (see Runner.Run).
func GPUCanDecode(v library.StreamInfo) bool {
	if v.Width <= 0 || v.Height <= 0 || v.Width > 8192 || v.Height > 4320 {
		return false
	}
	eightBit := slices.Contains([]string{"yuv420p", "yuvj420p"}, v.PixelFormat)
	tenBit := v.PixelFormat == "yuv420p10le"
	switch v.Codec {
	case "h264":
		// Hardware decoders handle 8-bit 4:2:0 H.264; High 10 and 4:2:2 mostly do not.
		return eightBit
	case "hevc", "vp9", "av1":
		return eightBit || tenBit
	case "mpeg2video", "vc1":
		return eightBit
	}
	return false
}

// gpuPipeline is a whole-GPU video path: decode, scale, optional tone map, and
// encode, keeping frames on the GPU between the steps.
type gpuPipeline struct {
	global []string // device setup, before the input
	input  []string // hardware decode options, before -i
	filter string
	encode []string
}

// cpuToneMap converts linear-light HDR frames to BT.709 SDR on the CPU (at the
// already reduced size); it is the same stage the software path uses. The
// setparams step tags the frames first: FFmpeg 8 fails with "no path between
// colorspaces" when a file leaves its primaries or transfer untagged, even
// though zscale is told them as well.
func cpuToneMap(transfer string) string {
	return fmt.Sprintf("setparams=color_primaries=bt2020:color_trc=%[1]s:colorspace=bt2020nc,"+
		"zscale=tin=%[1]s:min=bt2020nc:pin=bt2020:t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,"+
		"tonemap=tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv", transfer)
}

// gpuPipelineFor builds the decode, scale and tone map steps for the plan's
// encoder. The HDR transfer is needed only for the CPU tone map.
func gpuPipelineFor(p Plan, transfer, device string) (gpuPipeline, error) {
	hw, err := hardwareSetup(p.Encoder, device, p.Profile, p.VideoBitrate)
	if err != nil {
		return gpuPipeline{}, err
	}
	if p.ToneMap && (p.ToneMapper != "gpu" && p.ToneMapper != "cpu" || !IsHDRTransfer(transfer)) {
		return gpuPipeline{}, errors.New("playback: tone mapping needs a PQ or HLG source and a tone mapper")
	}
	size := fmt.Sprintf("w=%d:h=%d", p.Width, p.Height)
	fps := strconv.FormatFloat(p.FPS, 'f', 6, 64)
	tail := ",setsar=1,fps=" + fps
	cpu := func(upload string) string {
		// Drop excess frames before expensive CPU tone mapping, not after it.
		return "hwdownload,format=p010le,fps=" + fps + "," + cpuToneMap(transfer) + ",format=nv12,setsar=1" + upload
	}
	g := gpuPipeline{encode: hw.encode}
	switch p.Encoder {
	case EncoderIntelVAAPI, EncoderAMDVAAPI:
		if !renderNodePattern.MatchString(device) {
			return gpuPipeline{}, errors.New("playback: no /dev/dri render device for VA-API")
		}
		g.global = []string{"-init_hw_device", "vaapi=va:" + device, "-filter_hw_device", "va"}
		g.input = []string{"-hwaccel", "vaapi", "-hwaccel_device", "va", "-hwaccel_output_format", "vaapi"}
		switch {
		case !p.ToneMap:
			g.filter = "scale_vaapi=" + size + ":format=nv12" + tail
		case p.ToneMapper == "gpu":
			g.filter = "scale_vaapi=" + size + ":format=p010,tonemap_vaapi=format=nv12:p=bt709:t=bt709:m=bt709" + tail
		default:
			g.filter = "scale_vaapi=" + size + ":format=p010," + cpu(",hwupload")
		}
	case EncoderIntelQSV:
		if !renderNodePattern.MatchString(device) {
			return gpuPipeline{}, errors.New("playback: no /dev/dri render device for Quick Sync")
		}
		g.global = []string{"-init_hw_device", "vaapi=va:" + device, "-init_hw_device", "qsv=qs@va", "-filter_hw_device", "qs"}
		g.input = []string{"-hwaccel", "qsv", "-hwaccel_device", "qs", "-hwaccel_output_format", "qsv"}
		switch {
		case !p.ToneMap:
			g.filter = "vpp_qsv=" + size + ":format=nv12" + tail
		case p.ToneMapper == "gpu":
			g.filter = "vpp_qsv=" + size + ":format=nv12:tonemap=1" + tail
		default:
			g.filter = "vpp_qsv=" + size + ":format=p010," + cpu(",hwupload=extra_hw_frames=64")
		}
	case EncoderNvidiaNVENC:
		g.input = []string{"-hwaccel", "cuda", "-hwaccel_output_format", "cuda"}
		if !p.ToneMap {
			g.filter = "scale_cuda=" + size + ":format=nv12" + tail
		} else {
			// This FFmpeg has no CUDA tone mapper that works without OpenCL; NVENC takes system frames.
			g.filter = "scale_cuda=" + size + ":format=p010le," + cpu("")
		}
	default:
		return gpuPipeline{}, fmt.Errorf("playback: %q is not a hardware encoder", p.Encoder)
	}
	return g, nil
}

// Pipeline names where each stage of the video runs, for the playback details.
type Pipeline struct {
	Decoder    string `json:"decoder"`
	Scaler     string `json:"scaler"`
	ToneMapper string `json:"toneMapper,omitempty"`
	Encoder    string `json:"encoder,omitempty"`
}

// DescribePipeline reads a plan's stages. Copied video goes through none.
func DescribePipeline(p Plan) Pipeline {
	if p.Mode == "direct" || p.VideoCopy {
		return Pipeline{Decoder: "Not Used", Scaler: "Not Used"}
	}
	gpu := EncoderLabel(p.Encoder) + " (GPU)"
	pl := Pipeline{Decoder: "Software (CPU)", Scaler: "Software (CPU)", Encoder: "Software (CPU)"}
	if p.Encoder != "" {
		pl.Encoder = gpu
	}
	if p.Decoder == "hardware" {
		pl.Decoder, pl.Scaler = gpu, gpu
	}
	if p.ToneMap {
		pl.ToneMapper = "Software (CPU)"
		if p.ToneMapper == "gpu" {
			pl.ToneMapper = gpu
		}
	}
	return pl
}
