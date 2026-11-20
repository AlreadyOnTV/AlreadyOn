// Cache tests cover identity changes and time bounds without live server state.
package playback

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCalibrationCacheIdentityAndExpiry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	record := calibrationCache{Fingerprint: "fixture-driver-policy", At: time.Now().Add(-time.Minute), Threads: 2}
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
	if _, ok := readCalibrationCache(path, record.Fingerprint); !ok {
		t.Fatal("matching fresh identity rejected")
	}
	if _, ok := readCalibrationCache(path, "changed-driver"); ok {
		t.Fatal("changed identity accepted")
	}
	record.At = time.Now().Add(-25 * time.Hour)
	write()
	if _, ok := readCalibrationCache(path, record.Fingerprint); ok {
		t.Fatal("expired cache accepted")
	}
	record.At = time.Now().Add(time.Hour)
	write()
	if _, ok := readCalibrationCache(path, record.Fingerprint); ok {
		t.Fatal("future cache accepted")
	}
}
