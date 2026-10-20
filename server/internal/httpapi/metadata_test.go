package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Fake but validly-shaped test fixtures — handlePutMetadataSettings now
// validates format server-side (metadata.IsValidTMDbKey), so a short
// placeholder like "a-real-key" would be rejected with 400 before any
// of the round-trip/precedence behavior these tests actually exercise
// even runs.
const (
	fakeTMDbKey    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.cccccccccccccccccccccccccccccccccccccccc"
	fakeTMDbKeyAlt = "dddddddddddddddddddddddddddddddddddddddd.eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee.ffffffffffffffffffffffffffffffffffffffff"
)

func putMetadataSettings(t *testing.T, router http.Handler, body string) metadataSettingsResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/settings/metadata", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("put status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got metadataSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode put response: %v", err)
	}
	return got
}

func getMetadataSettings(t *testing.T, router http.Handler) metadataSettingsResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/settings/metadata", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got metadataSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	return got
}

func TestMetadataSettingsAPI_GetNeverReturnsTheActualKeyValue(t *testing.T) {
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))
	putMetadataSettings(t, router, `{"tmdbApiKey":"`+fakeTMDbKey+`"}`)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/metadata", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if bytes.Contains(rec.Body.Bytes(), []byte(fakeTMDbKey)) {
		t.Errorf("GET response contained the raw key: %s", rec.Body.String())
	}
}

func TestMetadataSettingsAPI_GetDefaultsToNeitherSet(t *testing.T) {
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))
	got := getMetadataSettings(t, router)
	if got.TMDbAPIKeySet {
		t.Errorf("got %+v, want not set", got)
	}
}

func TestMetadataSettingsAPI_SettingAKey_MarksItSet(t *testing.T) {
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))
	got := putMetadataSettings(t, router, `{"tmdbApiKey":"`+fakeTMDbKey+`"}`)
	if !got.TMDbAPIKeySet {
		t.Errorf("got %+v, want TMDb set", got)
	}
}

func TestMetadataSettingsAPI_ExplicitEmptyString_ClearsTheKey(t *testing.T) {
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))
	putMetadataSettings(t, router, `{"tmdbApiKey":"`+fakeTMDbKey+`"}`)

	got := putMetadataSettings(t, router, `{"tmdbApiKey":""}`)
	if got.TMDbAPIKeySet {
		t.Errorf("got %+v, want TMDb cleared by an explicit empty string", got)
	}
}

func TestMetadataSettingsAPI_MalformedTMDbKey_Returns400(t *testing.T) {
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))

	req := httptest.NewRequest(http.MethodPut, "/api/settings/metadata", bytes.NewReader([]byte(`{"tmdbApiKey":"short-v3-style-key"}`)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	// Rejected input must not have been saved.
	got := getMetadataSettings(t, router)
	if got.TMDbAPIKeySet {
		t.Errorf("got %+v, want TMDb still unset after a rejected value", got)
	}
}

func TestMetadataSettingsAPI_KeylessAlwaysEnabled(t *testing.T) {
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))
	if got := getMetadataSettings(t, router); !got.KeylessMetadata {
		t.Fatalf("got %+v, want free providers enabled by default", got)
	}

	if got := putMetadataSettings(t, router, `{"keylessMetadata":true}`); !got.KeylessMetadata {
		t.Errorf("got %+v, want keyless on after enabling", got)
	}
	if got := getMetadataSettings(t, router); !got.KeylessMetadata {
		t.Errorf("got %+v, want keyless still on when read back", got)
	}
	if got := putMetadataSettings(t, router, `{"keylessMetadata":false}`); !got.KeylessMetadata {
		t.Errorf("got %+v, want free providers enabled after disabling", got)
	}
}

func TestMetadataSettingsAPI_TMDbKeyCombinesWithFreeSources(t *testing.T) {
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))
	putMetadataSettings(t, router, `{"keylessMetadata":true}`)

	got := putMetadataSettings(t, router, `{"tmdbApiKey":"`+fakeTMDbKey+`"}`)
	if !got.TMDbAPIKeySet || !got.KeylessMetadata {
		t.Errorf("got %+v, want key set and free sources switched off", got)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/settings/metadata", strings.NewReader(`{"keylessMetadata":true}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 while a key is set, body=%s", rec.Code, rec.Body.String())
	}

	// Clearing the key leaves them off until the admin turns them on again.
	got = putMetadataSettings(t, router, `{"tmdbApiKey":""}`)
	if got.TMDbAPIKeySet || !got.KeylessMetadata {
		t.Errorf("got %+v, want no key and free sources still off", got)
	}
	if got = putMetadataSettings(t, router, `{"keylessMetadata":true}`); !got.KeylessMetadata {
		t.Errorf("got %+v, want free sources on again without a key", got)
	}
}

func TestMetadataSettingsAPI_ResponseHasNoEnvironmentFields(t *testing.T) {
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))
	req := httptest.NewRequest(http.MethodGet, "/api/settings/metadata", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if bytes.Contains(bytes.ToLower(rec.Body.Bytes()), []byte("env")) {
		t.Errorf("response mentions the environment: %s", rec.Body.String())
	}
}
