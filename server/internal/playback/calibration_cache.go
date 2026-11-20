package playback

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type calibrationCache struct {
	Fingerprint string
	At          time.Time
	Threads     int
	Status      CapacityStatus
	HDR         []HDRCapacity
	Profiles    []ProfileCapacity
}

// Raw hardware/cgroup data stays out of the cache; only a digest is persisted.
// Changes to the fixture recipe, runtime libraries, driver/kernel, CPU features,
// quotas, memory limits or encoder policy invalidate previous measurements.
func (c *Calibration) fingerprint(ctx context.Context, inputs *benchmarkInputs) (string, error) {
	hash := sha256.New()
	fmt.Fprintf(hash, "calibration-policy-v2\n%s\n%d:%d\n", inputs.fingerprint, runtime.NumCPU(), runtime.GOMAXPROCS(0))
	for _, exe := range []string{"ffmpeg", "ffprobe"} {
		raw, err := exec.CommandContext(ctx, exe, "-version").Output()
		if err != nil {
			return "", err
		}
		hash.Write(raw)
	}
	for _, path := range []string{"/proc/sys/kernel/osrelease", "/sys/fs/cgroup/cpu.max", "/sys/fs/cgroup/cpuset.cpus.effective", "/sys/fs/cgroup/memory.max", "/sys/class/drm/renderD128/device/vendor", "/sys/class/drm/renderD128/device/device", "/sys/class/drm/renderD128/device/driver/module/version", "/sys/module/i915/srcversion", "/sys/module/xe/srcversion", "/proc/driver/nvidia/version"} {
		raw, err := os.ReadFile(path)
		fmt.Fprintf(hash, "%s:%t\n", path, err == nil)
		hash.Write(raw)
	}
	if raw, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, "model name") || strings.HasPrefix(line, "flags") || strings.HasPrefix(line, "vendor_id") {
				hash.Write([]byte(line))
			}
		}
	}
	fmt.Fprintf(hash, "%s:%t:%s", c.Capacity.currentEncoder(ctx), c.Capacity.HardwareDecoding != nil && c.Capacity.HardwareDecoding(ctx), RenderNode())
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func readCalibrationCache(path, fingerprint string) (calibrationCache, bool) {
	var cached calibrationCache
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 2<<20 {
		return cached, false
	}
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &cached) != nil {
		return cached, false
	}
	if cached.Fingerprint != fingerprint || cached.At.After(time.Now()) || time.Since(cached.At) > 24*time.Hour || cached.Threads < 1 || cached.Threads > MaxThreads || len(cached.Profiles) > 32 || len(cached.Status.Samples) > 128 || len(cached.HDR) > 32 {
		return calibrationCache{}, false
	}
	for _, p := range cached.Profiles {
		if p.Threads < 1 || p.Threads > MaxThreads || p.Streams < 0 || p.Streams > MaxParallel || p.Height < 1 || p.Height > 2160 || p.FPS <= 0 || p.FPS > 60 || p.SourceFPS <= 0 || p.SourceFPS > 60 || p.SourceWidth < 1 || p.SourceWidth > 3840 || p.SourceHeight < 1 || p.SourceHeight > 2160 || len(p.Speeds) > MaxParallel || math.IsNaN(p.SingleSpeed) || math.IsInf(p.SingleSpeed, 0) || p.SingleSpeed < 0 || p.SingleSpeed > 100000 {
			return calibrationCache{}, false
		}
		for _, speed := range p.Speeds {
			if math.IsNaN(speed) || math.IsInf(speed, 0) || speed < 0 || speed > 100000 {
				return calibrationCache{}, false
			}
		}
	}
	return cached, true
}
func (c *Calibration) save(ctx context.Context, fingerprint string) {
	if c.DataDir == "" || ctx.Err() != nil {
		return
	}
	threads := c.Tuner.Threads()
	c.Capacity.mu.Lock()
	record := calibrationCache{Fingerprint: fingerprint, At: time.Now(), Threads: threads, Status: c.Capacity.status, HDR: append([]HDRCapacity(nil), c.Capacity.hdr...), Profiles: append([]ProfileCapacity(nil), c.Capacity.profiles...)}
	c.Capacity.mu.Unlock()
	raw, err := json.Marshal(record)
	if err != nil {
		return
	}
	file, err := os.CreateTemp(c.DataDir, ".benchmark-cache-*")
	if err != nil {
		return
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(raw); err != nil {
		file.Close()
		return
	}
	if err = file.Close(); err != nil {
		return
	}
	_ = os.Rename(name, filepath.Join(c.DataDir, "benchmark-cache.json"))
}
