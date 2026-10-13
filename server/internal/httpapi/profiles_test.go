package httpapi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func doJSON(t *testing.T, router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rd *strings.Reader
	if body != "" {
		rd = strings.NewReader(body)
	} else {
		rd = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rd)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func decodeProfile(t *testing.T, rec *httptest.ResponseRecorder) profileResponse {
	t.Helper()
	var p profileResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode profile: %v (body %s)", err, rec.Body.String())
	}
	return p
}

func TestProfiles_ListCreateGetDelete(t *testing.T) {
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))

	rec := doJSON(t, router, http.MethodGet, "/api/profiles", "")
	var list []profileResponse
	json.Unmarshal(rec.Body.Bytes(), &list)
	if rec.Code != http.StatusOK || len(list) != 3 || list[0].Name != "Admin" || list[1].Name != "Teen" || list[2].Name != "Kids" {
		t.Fatalf("list = %d %s, want the seeded Admin, Teen and Kids", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, router, http.MethodPost, "/api/profiles", `{"name":"Guest","role":"viewer","audienceTier":"teen"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", rec.Code, rec.Body.String())
	}
	created := decodeProfile(t, rec)
	if created.Name != "Guest" || created.HasPin || created.BrightnessLevel != nil {
		t.Errorf("created = %+v", created)
	}

	if rec = doJSON(t, router, http.MethodGet, "/api/profiles/4", ""); rec.Code != http.StatusOK {
		t.Errorf("get status = %d", rec.Code)
	}
	if rec = doJSON(t, router, http.MethodDelete, "/api/profiles/4", ""); rec.Code != http.StatusNoContent {
		t.Errorf("delete status = %d", rec.Code)
	}
	if rec = doJSON(t, router, http.MethodGet, "/api/profiles/4", ""); rec.Code != http.StatusNotFound {
		t.Errorf("get after delete status = %d, want 404", rec.Code)
	}
	if rec = doJSON(t, router, http.MethodGet, "/api/profiles/abc", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id status = %d, want 400", rec.Code)
	}
}

func TestProfiles_CreateErrors(t *testing.T) {
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))
	cases := map[string]int{
		`{"name":"","role":"viewer","audienceTier":"adult"}`:               http.StatusBadRequest,
		`{"name":"X","role":"root","audienceTier":"adult"}`:                http.StatusBadRequest,
		`{"name":"kids","role":"viewer","audienceTier":"adult"}`:           http.StatusConflict,
		`{"name":"X","role":"viewer","audienceTier":"adult","pin":"1234"}`: http.StatusBadRequest, // unknown field
		`not json`: http.StatusBadRequest,
	}
	for body, want := range cases {
		if rec := doJSON(t, router, http.MethodPost, "/api/profiles", body); rec.Code != want {
			t.Errorf("%s: status = %d, want %d (%s)", body, rec.Code, want, rec.Body.String())
		}
	}
}

func TestProfiles_PatchIsPartialAndValidated(t *testing.T) {
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))

	doJSON(t, router, http.MethodPatch, "/api/profiles/1", `{"brightnessLevel":1}`)
	rec := doJSON(t, router, http.MethodPatch, "/api/profiles/1", `{"guideTimeFormat":"24h"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body=%s", rec.Code, rec.Body.String())
	}
	p := decodeProfile(t, rec)
	if p.BrightnessLevel == nil || *p.BrightnessLevel != 1 || p.GuideTimeFormat == nil || *p.GuideTimeFormat != "24h" {
		t.Errorf("after two patches = %+v, want both fields kept", p)
	}

	rec = doJSON(t, router, http.MethodPatch, "/api/profiles/1", `{"brightnessLevel":null}`)
	if p := decodeProfile(t, rec); p.BrightnessLevel != nil {
		t.Errorf("brightness = %v after null, want cleared", *p.BrightnessLevel)
	}

	for body, want := range map[string]int{
		`{"guideSpanHours":8}`:         http.StatusBadRequest, // removed: the guide always shows 12 hours
		`{"nope":1}`:                   http.StatusBadRequest,
		`{"role":"viewer"}`:            http.StatusConflict, // last admin
		`{"name":"Kids"}`:              http.StatusConflict,
		`{"pin":"1234"}`:               http.StatusBadRequest, // PINs have their own endpoints
		`{"lastWatchedChannelId":999}`: http.StatusBadRequest, // only successful tunes may set it
	} {
		if rec := doJSON(t, router, http.MethodPatch, "/api/profiles/1", body); rec.Code != want {
			t.Errorf("%s: status = %d, want %d (%s)", body, rec.Code, want, rec.Body.String())
		}
	}
	if rec := doJSON(t, router, http.MethodPatch, "/api/profiles/99", `{}`); rec.Code != http.StatusNotFound {
		t.Errorf("missing profile status = %d, want 404", rec.Code)
	}
}

func TestProfiles_LastAdminCannotBeDeleted(t *testing.T) {
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))
	if rec := doJSON(t, router, http.MethodDelete, "/api/profiles/1", ""); rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

func TestProfiles_PINNeverAppearsInResponses(t *testing.T) {
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))

	if rec := doJSON(t, router, http.MethodPut, "/api/profiles/1/pin", `{"pin":"4827"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("set pin status = %d, body=%s", rec.Code, rec.Body.String())
	}
	for _, path := range []string{"/api/profiles", "/api/profiles/1"} {
		rec := doJSON(t, router, http.MethodGet, path, "")
		body := rec.Body.String()
		if strings.Contains(body, "4827") || strings.Contains(strings.ToLower(body), "hash") || strings.Contains(body, "$2") {
			t.Errorf("GET %s leaks PIN material: %s", path, body)
		}
		if !strings.Contains(body, `"hasPin":true`) {
			t.Errorf("GET %s should report hasPin true: %s", path, body)
		}
	}
	if rec := doJSON(t, router, http.MethodPut, "/api/profiles/1/pin", `{"pin":"12"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("short PIN status = %d, want 400", rec.Code)
	}
	if rec := doJSON(t, router, http.MethodDelete, "/api/profiles/1/pin", ""); rec.Code != http.StatusNoContent {
		t.Errorf("clear pin status = %d", rec.Code)
	}
	if rec := doJSON(t, router, http.MethodGet, "/api/profiles/1", ""); !strings.Contains(rec.Body.String(), `"hasPin":false`) {
		t.Errorf("hasPin should be false after clearing: %s", rec.Body.String())
	}
}

func TestProfiles_VerifyPIN(t *testing.T) {
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))
	doJSON(t, router, http.MethodPut, "/api/profiles/2/pin", `{"pin":"1234"}`)

	rec := doJSON(t, router, http.MethodPost, "/api/profiles/2/verify-pin", `{"pin":"1234"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"valid":true`) {
		t.Errorf("right PIN: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, router, http.MethodPost, "/api/profiles/2/verify-pin", `{"pin":"0000"}`)
	if rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "1234") {
		t.Errorf("wrong PIN: %d %s", rec.Code, rec.Body.String())
	}
	if rec = doJSON(t, router, http.MethodPost, "/api/profiles/99/verify-pin", `{"pin":"1234"}`); rec.Code != http.StatusNotFound {
		t.Errorf("missing profile status = %d, want 404", rec.Code)
	}
}

func TestProfiles_VerifyPIN_LocksAfterRepeatedFailures(t *testing.T) {
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))
	doJSON(t, router, http.MethodPut, "/api/profiles/2/pin", `{"pin":"1234"}`)

	var rec *httptest.ResponseRecorder
	for i := 0; i < 3; i++ {
		rec = doJSON(t, router, http.MethodPost, "/api/profiles/2/verify-pin", `{"pin":"0000"}`)
	}
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("third wrong PIN: %d retry-after=%q, want 429 with Retry-After", rec.Code, rec.Header().Get("Retry-After"))
	}
	// Even the correct PIN is refused during the lockout.
	if rec = doJSON(t, router, http.MethodPost, "/api/profiles/2/verify-pin", `{"pin":"1234"}`); rec.Code != http.StatusTooManyRequests {
		t.Errorf("correct PIN while locked: status = %d, want 429", rec.Code)
	}
}

func TestProfiles_Icons(t *testing.T) {
	iconDir := t.TempDir()
	router := NewRouter(t.TempDir(), iconDir, openTestDB(t))

	if rec := doJSON(t, router, http.MethodPatch, "/api/profiles/1", `{"icon":"fox"}`); rec.Code != http.StatusOK {
		t.Fatalf("preset icon: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, router, http.MethodPatch, "/api/profiles/1", `{"icon":"data:image/png;base64,AAAA"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("data URL icon status = %d, want 400", rec.Code)
	}

	req := newProfileIconUploadRequest(t, "/api/profiles/1/icon", "me.png", pngSignature)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload status = %d, body=%s", rec.Code, rec.Body.String())
	}
	p := decodeProfile(t, rec)
	if p.Icon == nil || !strings.HasPrefix(*p.Icon, "/profile-icons/") {
		t.Fatalf("icon = %v, want an uploaded-icon path", p.Icon)
	}

	get := httptest.NewRecorder()
	router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, *p.Icon, nil))
	if get.Code != http.StatusOK || !bytes.Equal(get.Body.Bytes(), pngSignature) {
		t.Errorf("GET %s = %d, want the uploaded bytes", *p.Icon, get.Code)
	}

	// Choosing a preset (or clearing) discards the uploaded file.
	doJSON(t, router, http.MethodPatch, "/api/profiles/1", `{"icon":null}`)
	entries, _ := os.ReadDir(iconDir)
	if len(entries) != 0 {
		t.Errorf("%d icon files left after clearing the icon, want 0", len(entries))
	}
}

func TestProfiles_IconUploadRejectsNonImages(t *testing.T) {
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))
	req := newProfileIconUploadRequest(t, "/api/profiles/1/icon", "evil.png", []byte("<svg onload=alert(1)>"))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestProfiles_DeleteRemovesUploadedIcon(t *testing.T) {
	iconDir := t.TempDir()
	router := NewRouter(t.TempDir(), iconDir, openTestDB(t))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, newProfileIconUploadRequest(t, "/api/profiles/2/icon", "k.png", pngSignature))
	if rec.Code != http.StatusOK {
		t.Fatalf("upload status = %d", rec.Code)
	}
	doJSON(t, router, http.MethodDelete, "/api/profiles/2", "")
	entries, _ := os.ReadDir(iconDir)
	if len(entries) != 0 {
		t.Errorf("%d icon files left after deleting the profile, want 0", len(entries))
	}
}

// pngSignature is enough for http.DetectContentType to sniff
// image/png — the upload handler never renders or decodes the image,
// so a full valid PNG isn't needed to exercise its validation path.
var pngSignature = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0}

func newProfileIconUploadRequest(t *testing.T, url string, filename string, content []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, url, &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

func TestProfiles_SubtitleModeAPI(t *testing.T) {
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))
	for _, mode := range []string{"always", "other_audio", "off"} {
		rec := doJSON(t, router, http.MethodPatch, "/api/profiles/1", `{"subtitleMode":"`+mode+`"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("patch %s: %d %s", mode, rec.Code, rec.Body.String())
		}
		saved := decodeProfile(t, doJSON(t, router, http.MethodGet, "/api/profiles/1", ""))
		if saved.SubtitleMode == nil || *saved.SubtitleMode != mode {
			t.Fatalf("mode round trip: %+v", saved)
		}
	}
	for _, body := range []string{`{"subtitleMode":"bad"}`, `{"subtitleMode":true}`} {
		rec := doJSON(t, router, http.MethodPatch, "/api/profiles/1", body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid mode: %d %s", rec.Code, rec.Body.String())
		}
	}
}
