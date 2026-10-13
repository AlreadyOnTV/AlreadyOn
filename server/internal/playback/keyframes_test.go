package playback

import (
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/library"
)

func TestCopyNeedsKnownKeyframesThatFitThePresentationDelay(t *testing.T) {
	for _, tc := range []struct {
		name  string
		gap   time.Duration
		copy  bool
		why   string
		mbps  int64
		model string
	}{
		{"two second GOP copies", 2 * time.Second, true, "", 8, "mp4"},
		{"four second GOP is the limit", MaxCopyKeyframeGap, true, "", 8, "mp4"},
		{"twelve second GOP converts", 12 * time.Second, false, "convert video: its keyframes are too far apart to copy", 8, "mp4"},
		{"unmeasured converts", 0, false, "convert video: keyframe spacing has not been measured", 8, "mp4"},
		{"failed measurement converts", -time.Nanosecond, false, "convert video: keyframe spacing could not be measured", 8, "mp4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := sourceInfo()
			s.Container = "matroska"
			s.KeyframeGap = tc.gap
			s.BitRate = tc.mbps * 1_000_000
			s.Streams[0].BitRate = s.BitRate
			c := clientCaps()
			c.Mbps = 200
			p, err := Choose(s, c, true, false)
			if err != nil {
				t.Fatal(err)
			}
			if p.VideoCopy != tc.copy || (!tc.copy && p.Reason != tc.why) {
				t.Fatalf("copy=%v reason=%q, want copy=%v reason=%q", p.VideoCopy, p.Reason, tc.copy, tc.why)
			}
		})
	}
}

func TestScratchBudgetFitsTheMinimumSpecTmpfs(t *testing.T) {
	if MaxProcessBytes*16 > ScratchFloorBytes/2 {
		t.Fatalf("16 processes at %d bytes would use more than half of the %d byte floor", MaxProcessBytes, int64(ScratchFloorBytes))
	}
	if DefaultLimits().MaxBytes != MaxProcessBytes {
		t.Fatal("the runner should use the shared per-process budget")
	}
}

func TestCopiedVideoLimitsFollowTheKeyframeGap(t *testing.T) {
	base := DefaultLimits()
	copied := func(gap time.Duration) Limits {
		return limitsFor(base, Job{Plan: Plan{VideoCopy: true}, Source: sourceWithGap(gap)})
	}
	if got := copied(2 * time.Second); got.Stall != base.Stall || got.Startup != base.Startup {
		t.Fatalf("a short gap should keep the defaults: %+v", got)
	}
	if got := copied(8 * time.Second); got.Stall != 24*time.Second || got.Startup != 29*time.Second {
		t.Fatalf("limits did not grow with the gap: %+v", got)
	}
	if got := copied(60 * time.Second); got.Stall != 30*time.Second || got.Startup != 30*time.Second {
		t.Fatalf("limits are not capped: %+v", got)
	}
	if got := limitsFor(base, Job{Plan: Plan{VideoCopy: false}, Source: sourceWithGap(8 * time.Second)}); got != base {
		t.Fatal("converted video keeps its encoder-paced segments")
	}
}

func sourceWithGap(gap time.Duration) (s library.ProbeResult) {
	s = sourceInfo()
	s.KeyframeGap = gap
	return s
}

func TestCopyScratchBudgetScalesWithGapAndBitrate(t *testing.T) {
	for _, tc := range []struct {
		gap  time.Duration
		bps  int64
		fits bool
	}{
		{2 * time.Second, 40_000_000, true},
		{MaxCopyKeyframeGap, 40_000_000, true},
		{MaxCopyKeyframeGap, 50_000_000, false},
		{2 * time.Second, 100_000_000, false},
	} {
		if ok, why := copyFitsKeyframes(tc.gap, tc.bps); ok != tc.fits {
			t.Errorf("gap %v at %d bps: fits=%v (%s), want %v", tc.gap, tc.bps, ok, why, tc.fits)
		}
	}
}
