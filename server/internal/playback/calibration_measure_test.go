// Cancellation must release progress readers even before process admission.
package playback

import (
	"context"
	"github.com/alreadyonapp/alreadyon/server/internal/governor"
	"testing"
	"time"
)

func TestMeasurementCancellationTearsDownProgressReaders(t *testing.T) {
	governor.SetDefault(governor.Fixed(0))
	defer governor.SetDefault(nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = measureStreams(ctx, "synthetic-missing.mkv", sourceInfo(), Plan{Mode: "transcode", Width: 1920, Height: 1080, FPS: 30, Profile: "high", VideoBitrate: 12000000}, 2, 4, time.Second, time.Second)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled measurement leaked a process/progress reader")
	}
}

func TestBenchmarkTimelinePreservesQuotedPathsAcrossClipBoundaries(t *testing.T) {
	job := fixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	speeds, err := measureStreams(ctx, job.Path, job.Source, job.Plan, 2, 1, 100*time.Millisecond, time.Second)
	if err != nil || len(speeds) != 1 || speeds[0] <= 0 {
		t.Fatalf("concat benchmark failed on quoted synthetic path: %v %v", speeds, err)
	}
}
