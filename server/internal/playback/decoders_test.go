package playback

import (
	"slices"
	"testing"

	"github.com/alreadyonapp/alreadyon/server/internal/library"
)

const sampleCodecList = `Codecs:
 D..... = Decoding supported
 .E.... = Encoding supported
 -------
 DEV.LS h264                 H.264 / AVC (decoders: libopenh264) (encoders: libopenh264)
 .EV.LS hevc                 H.265 / HEVC (encoders: hevc_vaapi)
 DEA.L. aac                  AAC (Advanced Audio Coding)
 ..AIL. eac3                 ATSC A/52B (AC-3, E-AC-3)
`

func TestParseCodecList(t *testing.T) {
	got := parseCodecList(sampleCodecList)
	want := map[string]bool{"h264": true, "hevc": false, "aac": true, "eac3": false}
	for name, decodes := range want {
		if got[name] != decodes {
			t.Errorf("%s decodable = %v, want %v", name, got[name], decodes)
		}
	}
	if parseCodecList("no codec table here") != nil {
		t.Error("unparseable output must report unknown (nil), not an empty capability set")
	}
}

func withDecoders(t *testing.T, known map[string]bool) {
	t.Helper()
	old := decoders
	decoders = func() map[string]bool { return known }
	t.Cleanup(func() { decoders = old })
}

func sourceWith(video, audio string) library.ProbeResult {
	return library.ProbeResult{Streams: []library.StreamInfo{
		{Index: 0, Kind: library.StreamVideo, Codec: video},
		{Index: 1, Kind: library.StreamAudio, Codec: audio},
	}}
}

func TestMissingDecoders(t *testing.T) {
	withDecoders(t, map[string]bool{"h264": true, "hevc": false, "aac": true, "eac3": false})
	convert := Plan{Mode: "transcode"}

	if got := MissingDecoders(convert, sourceWith("hevc", "eac3")); !slices.Equal(got, []string{"hevc", "eac3"}) {
		t.Errorf("hevc+eac3 conversion: missing = %v", got)
	}
	if got := MissingDecoders(convert, sourceWith("h264", "aac")); len(got) != 0 {
		t.Errorf("h264+aac conversion: missing = %v, want none", got)
	}
	copyVideo := Plan{Mode: "transcode", VideoCopy: true}
	if got := MissingDecoders(copyVideo, sourceWith("hevc", "aac")); len(got) != 0 {
		t.Errorf("copied video needs no decoder: missing = %v", got)
	}
	if got := MissingDecoders(Plan{Mode: "remux", VideoCopy: true, AudioCopy: true}, sourceWith("hevc", "eac3")); len(got) != 0 {
		t.Errorf("remux decodes nothing: missing = %v", got)
	}
	if got := MissingDecoders(convert, sourceWith("vp9", "opus")); !slices.Equal(got, []string{"vp9", "opus"}) {
		t.Errorf("codecs absent from the list count as missing: %v", got)
	}
}

func TestMissingDecodersUnknownCapabilityNeverBlocks(t *testing.T) {
	withDecoders(t, nil)
	if got := MissingDecoders(Plan{Mode: "transcode"}, sourceWith("hevc", "eac3")); got != nil {
		t.Errorf("missing = %v, want nil when FFmpeg's codecs are unknown", got)
	}
}
