//go:build e2e

package playback

import "testing"

// Fixture teardown restores actual calibration rather than leaking test limits.
func TestFixtureCapacityRestoresMeasurements(t *testing.T) {
	c := &Capacity{}
	c.publishProfiles([]ProfileCapacity{{SourceClass: SourceH264HD, Verified: true, Streams: 1, Threads: 2, Height: 360, FPS: 30, SingleSpeed: 1.4}})
	restore := c.FixtureCapacity(2)
	if c.Processes() != 2 || len(c.profiles) != 1 || c.profiles[0].Height != 720 {
		t.Fatal("fixture not installed")
	}
	restore()
	if c.Processes() != 1 || len(c.profiles) != 1 || c.profiles[0].Height != 360 || c.status.Samples[0].Height != 360 {
		t.Fatal("measured calibration not restored")
	}
}
