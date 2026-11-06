package playback

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Prefer x264's quality/rate control in the complete container image. Retain
// OpenH264 for distribution builds (including codec-limited distributions).
var softwareEncoder = sync.OnceValue(func() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-encoders").Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) > 1 && fields[1] == "libx264" {
				return "libx264"
			}
		}
	}
	return "libopenh264"
})

func softwareVideoArgs(encoder, profile, threads, filterThreads string) []string {
	if encoder == "libx264" && profile == "constrained_baseline" {
		profile = "baseline"
	}
	args := []string{"-c:v", encoder, "-profile:v", profile, "-pix_fmt", "yuv420p", "-threads", threads, "-filter_threads", filterThreads}
	if encoder == "libx264" {
		args = append(args, "-preset", "veryfast", "-tune", "zerolatency")
	}
	return args
}
