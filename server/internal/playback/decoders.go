package playback

import (
	"context"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/library"
)

// Distribution FFmpeg builds differ: Fedora's ffmpeg-free, for one, has no
// HEVC, E-AC-3 or DTS decoder, so converting such a title fails only once
// FFmpeg is already running. Knowing up front lets the server say why.

// decoders returns the codec names this FFmpeg can decode, or nil when that
// could not be determined (no ffmpeg, unexpected output).
var decoders = sync.OnceValue(probeDecoders)

func probeDecoders() map[string]bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-codecs").Output()
	if err != nil {
		return nil
	}
	return parseCodecList(string(out))
}

// parseCodecList reads `ffmpeg -codecs`: a header, a dashed line, then rows of
// a six-character flag column (first flag D = decoding supported) and a name.
func parseCodecList(out string) map[string]bool {
	found := map[string]bool{}
	seenRows := false
	for _, line := range strings.Split(out, "\n") {
		if !seenRows {
			seenRows = strings.HasPrefix(strings.TrimSpace(line), "-------")
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || len(fields[0]) != 6 {
			continue
		}
		found[fields[1]] = fields[0][0] == 'D'
	}
	if len(found) == 0 {
		return nil
	}
	return found
}

// MissingDecoders names the source codecs that plan needs FFmpeg to decode
// but this FFmpeg build cannot. Copying and direct play decode nothing.
func MissingDecoders(plan Plan, source library.ProbeResult) []string {
	known := decoders()
	if known == nil || plan.Mode != "transcode" {
		return nil
	}
	v, a := Tracks(source)
	var missing []string
	if !plan.VideoCopy && v.Codec != "" && !known[v.Codec] {
		missing = append(missing, v.Codec)
	}
	if !plan.AudioCopy && a.Index >= 0 && a.Codec != "" && !known[a.Codec] && !slices.Contains(missing, a.Codec) {
		missing = append(missing, a.Codec)
	}
	return missing
}

// commonCodecs are the ones typical libraries need converted for browsers.
var commonCodecs = []string{"h264", "hevc", "aac", "ac3", "eac3", "dts"}

// MissingCommonDecoders lists common codecs this FFmpeg cannot decode, for a
// startup warning. It is nil when everything is available or unknown.
func MissingCommonDecoders() []string {
	known := decoders()
	if known == nil {
		return nil
	}
	var missing []string
	for _, c := range commonCodecs {
		if !known[c] {
			missing = append(missing, c)
		}
	}
	return missing
}
