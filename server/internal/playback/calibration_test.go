package playback

import (
	"context"
	"errors"
	"testing"
)

func TestCalibrationRejectsStarvingConcurrentStream(t *testing.T) {
	c := &Calibration{Tuner: &Tuner{}}
	c.measure = func(_ context.Context, _ calibrationCase, n int) ([]float64, error) {
		switch n {
		case 1:
			return []float64{3}, nil
		case 2:
			return []float64{2, 2}, nil
		default:
			return []float64{4, 4, 4, .9}, nil
		}
	}
	p := c.evaluate(context.Background(), calibrationCase{}, nil)
	if p.Streams != 2 {
		t.Fatalf("validated %d, want 2", p.Streams)
	}
}
func TestCalibrationConfirmsBorderlineCapacity(t *testing.T) {
	calls := 0
	c := &Calibration{Tuner: &Tuner{}}
	c.measure = func(_ context.Context, _ calibrationCase, n int) ([]float64, error) {
		if n == 1 {
			return []float64{3}, nil
		}
		calls++
		if calls == 1 {
			return []float64{1.4, 1.4}, nil
		}
		return []float64{1.2, 1.2}, nil
	}
	p := c.evaluate(context.Background(), calibrationCase{}, nil)
	if p.Streams != 1 || calls != 2 {
		t.Fatalf("streams=%d calls=%d", p.Streams, calls)
	}
}
func TestCalibrationFailureRemainsUnverified(t *testing.T) {
	c := &Calibration{measure: func(context.Context, calibrationCase, int) ([]float64, error) { return nil, errors.New("unavailable") }}
	if p := c.evaluate(context.Background(), calibrationCase{}, nil); p.Verified || p.Streams != 0 {
		t.Fatal(p)
	}
}

func TestCalibratedAdmissionUsesPipelineCountAndUnknownFallback(t *testing.T) {
	source := sourceInfo()
	plan := Plan{Mode: "transcode", Height: 1080, FPS: 30}
	job := Job{Source: source, Plan: plan, Threads: 2}
	video, _ := Tracks(source)
	c := &Capacity{calibrated: true, memory: func() uint64 { return 64 << 30 }, profiles: []ProfileCapacity{{SourceClass: classifySource(video), SourceWidth: 1920, SourceHeight: 1080, SourceFPS: 60, Height: 1080, FPS: 30, Threads: 2, Streams: 2, Verified: true}}}
	if err := c.AdmissionError([]Job{job}, job); err != nil {
		t.Fatal(err)
	}
	if err := c.AdmissionError([]Job{job, job}, job); !errors.Is(err, ErrBusy) {
		t.Fatalf("third stream: %v", err)
	}
	unknown := job
	unknown.Threads = 4
	if err := c.AdmissionError([]Job{job}, unknown); !errors.Is(err, ErrBusy) {
		t.Fatalf("unknown concurrent path: %v", err)
	}
	c.profiles[0].Streams = 0
	if err := c.AdmissionError(nil, job); !errors.Is(err, ErrCapacityUnsustainable) {
		t.Fatalf("failed pipeline: %v", err)
	}
}

func TestCachedLimitFailureRetainsMeasuredLowerCount(t *testing.T) {
	c := &Calibration{Tuner: &Tuner{}}
	c.measure = func(_ context.Context, _ calibrationCase, n int) ([]float64, error) {
		if n == 4 {
			return nil, errors.New("overloaded")
		}
		if n == 2 {
			return []float64{2, 2}, nil
		}
		return []float64{4}, nil
	}
	old := []ProfileCapacity{{Verified: true, Streams: 4}}
	if p := c.evaluate(context.Background(), calibrationCase{}, old); p.Streams != 2 {
		t.Fatalf("streams=%d", p.Streams)
	}
}

func TestRepresentativePublishPreservesFullHDRMatrix(t *testing.T) {
	c := &Capacity{hdr: []HDRCapacity{{Transfer: "smpte2084", SourceFPS: 60, Threads: 2, Samples: []RungSample{{Height: 720, FPS: 30, Speed: 3}, {Height: 1080, FPS: 30, Speed: 2}}}}}
	c.publishProfiles([]ProfileCapacity{{Verified: true, SourceClass: SourceHEVCPQ, SourceFPS: 60, HardwareDecode: true, ToneMapper: "gpu", Threads: 2, Height: 1080, FPS: 30, SingleSpeed: 4, Streams: 2}})
	if len(c.hdr) != 1 || len(c.hdr[0].Samples) != 2 || c.hdr[0].Samples[0].Height != 720 || c.hdr[0].Samples[1].Speed != 4 {
		t.Fatalf("matrix overwritten: %+v", c.hdr)
	}
}
