// Cache tests cover identity changes and time bounds without live server state.
package playback

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCalibrationCacheDeviceAndAge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	record := calibrationCache{Device: SettingIntel, At: time.Now().Add(-time.Minute), Threads: 2, Profiles: []ProfileCapacity{{Threads: 2, Streams: 1, Height: 1080, FPS: 30, SourceFPS: 30, SourceWidth: 1920, SourceHeight: 1080}}}
	write := func() {
		t.Helper()
		raw, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	if _, ok := readCalibrationCache(path, record.Device); !ok {
		t.Fatal("matching fresh identity rejected")
	}
	if _, ok := readCalibrationCache(path, "software"); ok {
		t.Fatal("different device accepted")
	}
	record.At = time.Now().Add(-25 * time.Hour)
	write()
	if _, ok := readCalibrationCache(path, record.Device); !ok {
		t.Fatal("old results for the same device were rejected")
	}
	record.Device = ""
	record.Profiles[0].Encoder = EncoderIntelQSV
	write()
	if _, ok := readCalibrationCache(path, SettingIntel); !ok {
		t.Fatal("legacy Intel results should migrate without a new benchmark")
	}
	if _, ok := readCalibrationCache(path, "software"); ok {
		t.Fatal("legacy Intel results should not be used for CPU Only")
	}
	record.Device = SettingIntel
	record.At = time.Now().Add(time.Hour)
	write()
	if _, ok := readCalibrationCache(path, record.Device); ok {
		t.Fatal("future cache accepted")
	}
}

func TestCalibrationStartupKeepsOldFullResultsWithoutFixtures(t *testing.T) {
	dir := t.TempDir()
	device := SettingIntel
	capacity := &Capacity{}
	c := &Calibration{
		Capacity: capacity, Tuner: &Tuner{}, DataDir: dir, InputsDir: filepath.Join(dir, "missing"),
		Device: func(context.Context) (string, error) { return device, nil },
	}
	at := time.Now().Add(-72 * time.Hour)
	c.save("full", calibrationCache{At: at, Threads: 2, Profiles: []ProfileCapacity{{
		Encoder: EncoderIntelQSV, TargetHeight: 1080, Height: 1080, FPS: 24,
		SourceFPS: 24, SourceWidth: 3840, SourceHeight: 2160, Threads: 2, Streams: 2, Verified: true,
	}}})
	c.Startup(context.Background())
	if len(capacity.profiles) != 1 || capacity.profiles[0].Height != 1080 {
		t.Fatalf("startup did not load the old full result: %+v", capacity.profiles)
	}
	persisted, ok := readCalibrationCache(benchmarkCachePath(dir, "full"), device)
	if !ok || persisted.Device != device || !persisted.At.Equal(at) {
		t.Fatalf("startup did not migrate the device or preserve measurement age: %+v", persisted)
	}
	device = "software"
	c.Startup(context.Background())
	if len(capacity.profiles) != 0 {
		t.Fatalf("startup reused another device's profiles: %+v", capacity.profiles)
	}
}
