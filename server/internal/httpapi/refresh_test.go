package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alreadyonapp/alreadyon/server/internal/logging"
	"github.com/alreadyonapp/alreadyon/server/internal/metadata"
	"github.com/alreadyonapp/alreadyon/server/internal/profile"
	"github.com/alreadyonapp/alreadyon/server/internal/refresh"
)

// Automatic free sources run even without a TMDb key. This empty synthetic
// library verifies the refresh contract without making provider network calls.
func TestRefreshScheduleAPI_AutomaticSourcesWithoutTMDb_StillRefreshesChannels(t *testing.T) {
	db := openTestDB(t)
	router := NewRouter(t.TempDir(), t.TempDir(), db)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/refresh-schedule", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got refreshScheduleResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Classified {
		t.Errorf("Classified = false, want automatic free metadata classification")
	}
}

func TestRefreshScheduleAPI_ReturnsConflictWhileStartupPipelineRuns(t *testing.T) {
	db := openTestDB(t)
	router := NewRouter(t.TempDir(), t.TempDir(), db)

	held, release := make(chan struct{}), make(chan struct{})
	go refresh.Exclusive(func() { close(held); <-release })
	<-held
	defer close(release)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/refresh-schedule", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusConflict, rec.Body.String())
	}
}

// TestRefreshScheduleAPI_KeyFromServerSettings_CountsAsConfigured: the
// refresh handler reads the TMDb key saved in Server Settings.
func TestRefreshScheduleAPI_KeyFromServerSettings_CountsAsConfigured(t *testing.T) {
	db := openTestDB(t)
	if err := metadata.SaveSettings(context.Background(), db, metadata.Settings{TMDbAPIKey: "saved-tmdb-key"}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	router := NewRouter(t.TempDir(), t.TempDir(), db)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/refresh-schedule", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got refreshScheduleResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Classified {
		t.Errorf("Classified = false, want true (a key saved in Server Settings counts as configured)")
	}
}

func postRefreshMedia(t *testing.T, router http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/refresh-media", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func loggedMessage(t *testing.T, substr string) bool {
	t.Helper()
	for _, e := range logging.Entries(nil, 0) {
		if strings.Contains(e.Message, substr) {
			return true
		}
	}
	return false
}

func TestRefreshMediaAPI_LogsWhichProfileRequestedTheRescan(t *testing.T) {
	db := openTestDB(t)
	router := NewRouter(t.TempDir(), t.TempDir(), db)

	list, err := profile.List(context.Background(), db, 1)
	if err != nil || len(list) == 0 {
		t.Fatalf("profile.List: %v (%d profiles)", err, len(list))
	}
	body := fmt.Sprintf(`{"profileId":%d}`, list[0].ID)
	rec := postRefreshMedia(t, router, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	want := fmt.Sprintf("manual media refresh requested by profile %q (id %d)", list[0].Name, list[0].ID)
	if !loggedMessage(t, want) {
		t.Errorf("no log entry containing %q", want)
	}
}

func TestRefreshMediaAPI_BodyIsOptionalAndUnknownProfileIsRejected(t *testing.T) {
	db := openTestDB(t)
	router := NewRouter(t.TempDir(), t.TempDir(), db)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/refresh-media", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("no body: status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if rec := postRefreshMedia(t, router, `{"profileId":999999}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown profile: status = %d, want 400", rec.Code)
	}
}

func TestRefreshMediaAPI_ReturnsConflictWhileARunIsInProgress(t *testing.T) {
	db := openTestDB(t)
	router := NewRouter(t.TempDir(), t.TempDir(), db)

	held, release := make(chan struct{}), make(chan struct{})
	go refresh.Exclusive(func() { close(held); <-release })
	<-held
	defer close(release)

	if rec := postRefreshMedia(t, router, `{}`); rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body=%s", rec.Code, rec.Body.String())
	}
}
