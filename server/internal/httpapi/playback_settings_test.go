package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/alreadyonapp/alreadyon/server/internal/playback"
)

func TestPlaybackSettingsAPI(t *testing.T) {
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))
	rec := doJSON(t, router, http.MethodGet, "/api/settings/playback", "")
	var got playback.Settings
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 || got != playback.DefaultSettings() {
		t.Fatalf("%d %+v %v", rec.Code, got, err)
	}
	for _, body := range []string{`{}`, `{"maxProcesses":0,"maxRenditions":8,"maxViewers":256,"leaseSeconds":30}`, `{"maxProcesses":2,"maxRenditions":8,"maxViewers":256,"leaseSeconds":30,"unbounded":true}`, `{"maxProcesses":2,"maxRenditions":8,"maxViewers":256,"leaseSeconds":10}`} {
		if rec := doJSON(t, router, http.MethodPut, "/api/settings/playback", body); rec.Code != 400 {
			t.Fatalf("%s: %d", body, rec.Code)
		}
	}
	rec = doJSON(t, router, http.MethodPut, "/api/settings/playback", `{"maxProcesses":4,"maxRenditions":8,"maxViewers":256,"leaseSeconds":30}`)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	rec = doJSON(t, router, http.MethodGet, "/api/settings/playback", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.MaxProcesses != 4 {
		t.Fatal("setting did not persist")
	}
	if got.Ladder {
		t.Fatal("the ladder must stay off unless it is turned on")
	}
	rec = doJSON(t, router, http.MethodPut, "/api/settings/playback", `{"maxProcesses":4,"maxRenditions":8,"maxViewers":256,"leaseSeconds":30,"ladder":true}`)
	got = playback.Settings{}
	if err := json.Unmarshal(doJSON(t, router, http.MethodGet, "/api/settings/playback", "").Body.Bytes(), &got); rec.Code != 200 || err != nil || !got.Ladder {
		t.Fatalf("ladder did not persist: %d %+v %v", rec.Code, got, err)
	}
}

func TestPlaybackSettingsAutoProcessesDefaultsOnAndPersists(t *testing.T) {
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))
	var got playback.Settings
	if err := json.Unmarshal(doJSON(t, router, http.MethodGet, "/api/settings/playback", "").Body.Bytes(), &got); err != nil || !got.AutoProcesses {
		t.Fatalf("auto parallel conversions must default on: %+v %v", got, err)
	}
	rec := doJSON(t, router, http.MethodPut, "/api/settings/playback", `{"maxProcesses":3,"autoProcesses":false,"maxRenditions":8,"maxViewers":256,"leaseSeconds":30}`)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	got = playback.Settings{}
	if err := json.Unmarshal(doJSON(t, router, http.MethodGet, "/api/settings/playback", "").Body.Bytes(), &got); err != nil || got.AutoProcesses || got.MaxProcesses != 3 {
		t.Fatalf("manual choice did not persist: %+v %v", got, err)
	}
}
