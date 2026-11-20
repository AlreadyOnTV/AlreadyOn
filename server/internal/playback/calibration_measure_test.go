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
