package config

import "testing"

func TestLoad_UnsetListeners_UseDefaults(t *testing.T) {
	t.Setenv("ALREADYON_WEB_PORT", "")
	t.Setenv("ALREADYON_STREAM_PORT", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ManagementAddr != ":19900" {
		t.Errorf("addr = %q; want :19900", cfg.ManagementAddr)
	}
}

func TestLoad_PortOverrides(t *testing.T) {
	t.Setenv("ALREADYON_WEB_PORT", "8123")
	t.Setenv("ALREADYON_STREAM_PORT", "8124")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ManagementAddr != ":8123" {
		t.Errorf("addr = %q; want :8123", cfg.ManagementAddr)
	}
}

func TestLoad_InvalidPort_FailsFast(t *testing.T) {
	for _, bad := range []string{"abc", "0", "70000", ":8080", "-1"} {
		t.Setenv("ALREADYON_WEB_PORT", bad)
		t.Setenv("ALREADYON_STREAM_PORT", "")
		if _, err := Load(); err == nil {
			t.Errorf("Load accepted ALREADYON_WEB_PORT=%q, want an error", bad)
		}
	}
}

func TestLoad_DirectoriesAreBuildTimeValues(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DataDir != dataDir || cfg.WebDir != webDir {
		t.Errorf("dirs = %q, %q; want the build-time values %q, %q", cfg.DataDir, cfg.WebDir, dataDir, webDir)
	}
}

// Existing deployments may retain the old stream-port override after upgrading.
// It must not reject startup or open a second application listener.
func TestLoad_ObsoleteStreamPortIsIgnored(t *testing.T) {
	t.Setenv("ALREADYON_WEB_PORT", "19900")
	t.Setenv("ALREADYON_STREAM_PORT", "19900")

	_, err := Load()
	if err != nil {
		t.Fatalf("obsolete stream port must not prevent startup: %v", err)
	}
}

func TestLoad_DistinctAddrs_Succeeds(t *testing.T) {
	t.Setenv("ALREADYON_WEB_PORT", "19900")
	t.Setenv("ALREADYON_STREAM_PORT", "19901")

	if _, err := Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
}
