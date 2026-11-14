package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/refresh"
)

// A blocked refresh lock models a minutes-long scan. Saving 40 preferences must
// still acknowledge atomically, with no lineup/schedule writes in the HTTP path.
func TestBulkPathSaveReturnsWhilePipelineIsBusy(t *testing.T) {
	db := openTestDB(t)
	if _, err := library.SaveFolderSettings(t.Context(), db, library.FolderSettings{Movie: []string{"/movies"}}); err != nil {
		t.Fatal(err)
	}
	for i := 200; i < 240; i++ {
		c, err := channel.Create(t.Context(), db, channel.Draft{Number: i, Name: fmt.Sprintf("Synthetic %d", i)})
		if err != nil {
			t.Fatal(err)
		}
		paths := []string{}
		if _, err = channel.SetFilter(t.Context(), db, c.ID, channel.Filter{AudienceTier: "adult", MediaType: "movie", FilterExtra: channel.FilterExtra{IncludedPaths: &paths}}); err != nil {
			t.Fatal(err)
		}
	}
	router := NewRouter(t.TempDir(), t.TempDir(), db)
	var state refresh.UpdateStatus
	refresh.Exclusive(func() {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		request := httptest.NewRequest(http.MethodPut, "/api/settings/folders/customize", strings.NewReader(`{"kind":"movies","path":"/movies","movies":true,"tv":false,"custom":true}`)).WithContext(ctx)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusAccepted {
			t.Fatalf("bulk save blocked or failed: %d %s", response.Code, response.Body.String())
		}
		if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
	})
	if state.State != "queued" || state.Revision != 1 {
		t.Fatalf("unexpected acknowledgement %+v", state)
	}
	var pending, schedules int
	db.QueryRow(`SELECT count(*) FROM library_update_channels`).Scan(&pending)
	db.QueryRow(`SELECT count(*) FROM schedule_entries`).Scan(&schedules)
	if pending != 40 || schedules != 0 {
		t.Fatalf("request did generation or lost channels: dirty=%d schedule=%d", pending, schedules)
	}
	filters, err := channel.ListFilters(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	for _, filter := range filters {
		if filter.IncludedPaths == nil || len(*filter.IncludedPaths) != 1 || (*filter.IncludedPaths)[0] != "/movies" {
			t.Fatal("preference did not persist before response")
		}
	}
}

func TestBulkPathSaveValidationAndAuthorization(t *testing.T) {
	db := openTestDB(t)
	router := NewRouter(t.TempDir(), t.TempDir(), db)
	for _, body := range []string{
		`{"kind":"movies","path":"/unconfigured","movies":true,"tv":false,"custom":false}`,
		`{"kind":"unknown","path":"/movies","movies":true,"tv":false,"custom":false}`,
		`{"kind":"movies","path":"/movies","movies":true}`,
		`{"kind":"movies","path":"/movies","movies":true,"tv":false,"custom":false,"extra":true}`,
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/settings/folders/customize", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatalf("invalid input: %d %s", w.Code, w.Body.String())
		}
	}
	state, err := refresh.GetUpdateStatus(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	if state.Revision != 0 {
		t.Fatal("invalid input queued work")
	}
	for _, route := range []struct{ method, path string }{{"GET", "/api/settings/folders/customize"}, {"PUT", "/api/settings/folders/customize"}, {"GET", "/api/settings/folders/updates"}, {"POST", "/api/settings/folders/updates/retry"}} {
		request := httptest.NewRequest(route.method, route.path, nil)
		w := httptest.NewRecorder()
		auth.RequireAuth(db, router).ServeHTTP(w, request)
		if w.Code != 401 || auth.RoleAllows(auth.RoleViewer, request) {
			t.Fatalf("route accessible without an owner: %+v %d", route, w.Code)
		}
	}
}
