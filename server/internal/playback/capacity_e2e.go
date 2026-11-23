//go:build e2e

package playback

// FixtureCapacity scopes a deterministic capacity result to the synthetic ladder
// scenario. Real startup calibration remains exercised separately; this helper
// is absent from production builds and never writes a cache or database record.
func (c *Capacity) FixtureCapacity(threads int) func() {
	c.mu.Lock()
	status, hdr, profiles, calibrated := c.status, c.hdr, c.profiles, c.calibrated
	c.status = CapacityStatus{}
	c.hdr = nil
	c.mu.Unlock()
	profile := ProfileCapacity{SourceClass: SourceH264HD, SourceWidth: 1920, SourceHeight: 1080, SourceFPS: 60, Height: 720, FPS: 30, Threads: threads, Streams: 2, SingleSpeed: 4, Speeds: []float64{4, 4}, Verified: true}
	baseline := profile
	baseline.TargetHeight, baseline.Height = AutoStartHeight, AutoStartHeight
	c.publishProfiles([]ProfileCapacity{baseline, profile})
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.status, c.hdr, c.profiles, c.calibrated = status, hdr, profiles, calibrated
	}
}
