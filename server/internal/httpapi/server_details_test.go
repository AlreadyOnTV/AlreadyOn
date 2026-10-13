package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// Exercise the optional FFmpeg probe and cache without depending on host codecs.
func TestServerDetailsCachesFFmpegVersion(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf 'ffmpeg version test-7 Copyright test\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("TZ", "America/New_York")
	handler := handleServerDetails(openTestDB(t))
	for i := 0; i < 2; i++ {
		response := httptest.NewRecorder()
		handler(response, httptest.NewRequest("GET", "/api/server/details", nil))
		var details struct {
			ServerName    string `json:"serverName"`
			UptimeSeconds int64  `json:"uptimeSeconds"`
			AppVersion    string `json:"appVersion"`
			TimeZone      string `json:"timeZone"`
			FFmpegVersion string `json:"ffmpegVersion"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &details); err != nil {
			t.Fatal(err)
		}
		if response.Code != 200 || details.UptimeSeconds < 0 || details.ServerName == "" || details.AppVersion == "" || details.TimeZone != "America/New_York" || details.FFmpegVersion != "test-7" {
			t.Fatalf("unexpected details: %+v", details)
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("uptime must not be cached")
		}
		// The second request must use the cached version after the executable is gone.
		if i == 0 {
			if err := os.Remove(executable); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestAppVersionNativeBuildStamp(t *testing.T) {
	previous := nativeBuildVersion
	t.Cleanup(func() { nativeBuildVersion = previous })
	nativeBuildVersion = "26.10.02_ab3a4cf"
	if got := appVersion(); got != nativeBuildVersion {
		t.Fatalf("version = %q, want %q", got, nativeBuildVersion)
	}
}

func TestServerDetailsWithoutFFmpeg(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	response := httptest.NewRecorder()
	handleServerDetails(openTestDB(t))(response, httptest.NewRequest("GET", "/api/server/details", nil))
	var details map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &details); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || details["ffmpegVersion"] != "Unavailable" {
		t.Fatalf("%s", response.Body.String())
	}
}
