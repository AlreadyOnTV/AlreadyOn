package playback

import "time"

// ScratchFloorBytes is the transcode scratch space a minimum-spec server is
// assumed to have: an 8 GiB tmpfs on a 16 GB machine (docs/PLAYBACK.md,
// Minimum and recommended hardware).
const ScratchFloorBytes = 8 << 30

// MaxProcessBytes is the most one FFmpeg process may keep on disk. The most
// processes the settings allow (16) each use at most this, with half the floor
// left free, so scratch can never fill the tmpfs.
const MaxProcessBytes = ScratchFloorBytes / 16 / 2

// SegmentsOnDisk is how many HLS segments exist at once: six listed, two kept
// for deletion and one being written.
const SegmentsOnDisk = 9

// MaxCopyKeyframeGap is the longest keyframe spacing at which video may be
// copied into HLS. Keep it independent of the longer presentation delay so a
// copy segment cannot consume most of the client's buffer budget.
const MaxCopyKeyframeGap = 4 * time.Second

// copyFitsKeyframes reports whether video with this keyframe gap and bitrate may
// be copied, and why not otherwise. An unknown or failed measurement fails
// closed: converting costs CPU, copying a long-GOP file stalls the stream.
func copyFitsKeyframes(gap time.Duration, bitsPerSecond int64) (bool, string) {
	switch {
	case gap == 0:
		return false, "keyframe spacing has not been measured"
	case gap < 0:
		return false, "keyframe spacing could not be measured"
	case gap > MaxCopyKeyframeGap:
		return false, "its keyframes are too far apart to copy"
	}
	segment := max(gap, 2*time.Second)
	// A quarter more than the average rate covers VBR peaks and muxing overhead.
	need := float64(bitsPerSecond) / 8 * segment.Seconds() * SegmentsOnDisk * 1.25
	if need > float64(MaxProcessBytes) {
		return false, "its bitrate is too high to copy within the scratch budget"
	}
	return true, ""
}

// limitsFor lengthens the startup and stall limits for copied video, whose
// first and later segments take as long as the keyframe gap, up to a hard cap.
func limitsFor(base Limits, j Job) Limits {
	if gap := j.Source.KeyframeGap; j.Plan.VideoCopy && gap > 0 {
		base.Stall = min(max(base.Stall, 3*gap), 30*time.Second)
		base.Startup = min(max(base.Startup, 3*gap+5*time.Second), 30*time.Second)
	}
	return base
}
