package httpapi

import (
	"context"
	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"github.com/alreadyonapp/alreadyon/server/internal/playback"
	"testing"
)

// Server-wide monitoring is intentionally shared; its GET access must never grant settings writes.
func TestDashboardViewerReadOnlyAccess(t *testing.T) {
	db := openTestDB(t)
	testOwnerID(t, db)
	if _, err := auth.CreateUser(context.Background(), db, auth.NewUser{Username: "dashboard-viewer", Password: "synthetic-viewer-pass", Role: auth.RoleViewer}); err != nil {
		t.Fatal(err)
	}
	p := NewPlaybackHTTP(db, playback.NewSessions(nil), nil)
	handler := auth.RequireAuth(db, NewRouter(t.TempDir(), t.TempDir(), db, p))
	anonymous := newAuthClientForHandler(t, db, handler)
	viewer := newAuthClientForHandler(t, db, handler)
	if rec := viewer.do("POST", "/api/auth/login", `{"username":"dashboard-viewer","password":"synthetic-viewer-pass"}`); rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	for _, path := range []string{"/api/server/details", "/api/server/activity", "/api/server/metrics?minutes=15"} {
		if rec := anonymous.do("GET", path, ""); rec.Code != 401 {
			t.Fatalf("anonymous %s: %d", path, rec.Code)
		}
		if rec := viewer.do("GET", path, ""); rec.Code != 200 {
			t.Fatalf("viewer %s: %d %s", path, rec.Code, rec.Body.String())
		}
	}
	for _, request := range [][2]string{{"GET", "/api/settings/server"}, {"PUT", "/api/settings/server"}, {"POST", "/api/server/restart"}, {"POST", "/api/server/metrics"}, {"DELETE", "/api/server/activity"}} {
		if rec := viewer.do(request[0], request[1], `{}`); rec.Code != 403 {
			t.Fatalf("viewer %v: %d", request, rec.Code)
		}
	}
}
