package playback

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"time"
)

type calibrationCache struct {
	Device   string
	At       time.Time
	Threads  int
	Status   CapacityStatus
	HDR      []HDRCapacity
	Profiles []ProfileCapacity
}

func benchmarkCachePath(dataDir, mode string) string {
	return filepath.Join(dataDir, "benchmark-cache-"+mode+".json")
}

// Age controls when cases are refreshed, not whether their results are usable.
const calibrationRefreshInterval = 24 * time.Hour

func readCalibrationCache(path, device string) (calibrationCache, bool) {
	var cached calibrationCache
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 2<<20 {
		return cached, false
	}
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &cached) != nil {
		return cached, false
	}
	if cached.At.After(time.Now()) || cached.Threads < 1 || cached.Threads > MaxThreads || len(cached.Profiles) == 0 || len(cached.Profiles) > 256 || len(cached.Status.Samples) > 128 || len(cached.HDR) > 128 {
		return calibrationCache{}, false
	}
	for _, p := range cached.Profiles {
		if p.Threads < 1 || p.Threads > MaxThreads || p.Streams < 0 || p.Streams > MaxParallel || p.Height < 1 || p.Height > 2160 || p.FPS <= 0 || p.FPS > 60 || p.SourceFPS <= 0 || p.SourceFPS > 60 || p.SourceWidth < 1 || p.SourceWidth > 3840 || p.SourceHeight < 1 || p.SourceHeight > 2160 || len(p.Speeds) > MaxParallel || math.IsNaN(p.SingleSpeed) || math.IsInf(p.SingleSpeed, 0) || p.SingleSpeed < 0 || p.SingleSpeed > 100000 {
			return calibrationCache{}, false
		}
		if p.SingleStreamSpeed < 0 || p.SingleStreamSpeed > 100000 || math.IsNaN(p.SingleStreamSpeed) || math.IsInf(p.SingleStreamSpeed, 0) || p.CPUCores < 0 || p.CPUCores > 1024 || math.IsNaN(p.CPUCores) || math.IsInf(p.CPUCores, 0) {
			return calibrationCache{}, false
		}
		if p.GPUEngines != nil && (*p.GPUEngines < 0 || *p.GPUEngines > 128) {
			return calibrationCache{}, false
		}
		if p.ReferenceSSIM != nil && (*p.ReferenceSSIM < 0 || *p.ReferenceSSIM > 1) {
			return calibrationCache{}, false
		}
		for _, speed := range p.Speeds {
			if math.IsNaN(speed) || math.IsInf(speed, 0) || speed < 0 || speed > 100000 {
				return calibrationCache{}, false
			}
		}
	}
	recordedDevice := cached.Device
	if recordedDevice == "" {
		recordedDevice = legacyCalibrationDevice(cached.Profiles)
	}
	if recordedDevice == "" || recordedDevice != device {
		return calibrationCache{}, false
	}
	return cached, true
}

// Legacy records contain encoder names, including optional software fallback
// measurements in GPU runs. Adopt them only when they identify one vendor.
func legacyCalibrationDevice(profiles []ProfileCapacity) string {
	device := "software"
	for _, profile := range profiles {
		encoder := profile.Encoder
		switch encoder {
		case "":
			continue
		case EncoderIntelQSV, EncoderIntelVAAPI:
			encoder = SettingIntel
		case EncoderAMDVAAPI, EncoderNvidiaNVENC:
		default:
			return ""
		}
		if device != "software" && device != encoder {
			return ""
		}
		device = encoder
	}
	return device
}

func (c *Calibration) save(mode string, record calibrationCache) {
	if c.DataDir == "" {
		return
	}
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
	_ = os.Rename(name, benchmarkCachePath(c.DataDir, mode))
}
