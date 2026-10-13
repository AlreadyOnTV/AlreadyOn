package httpapi

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/alreadyonapp/alreadyon/server/internal/auth"
)

// TestViewerCannotReachOtherAccountsOrOutliveItsAccess covers the boundaries a
// shared demo account relies on: a Viewer cannot see or sign out another
// account's browsers, owner changes to the account take effect on its live
// session immediately, and oversized bodies are refused without harm.
func TestViewerCannotReachOtherAccountsOrOutliveItsAccess(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	testOwnerID(t, db)
	viewer, err := auth.CreateUser(ctx, db, auth.NewUser{Username: "reviewer", Password: "synthetic-viewer-pass", Role: auth.RoleViewer, MaxStreams: 1})
	if err != nil {
		t.Fatal(err)
	}
	handler := auth.RequireAuth(db, NewRouter(t.TempDir(), t.TempDir(), db))
	o := newAuthClientForHandler(t, db, handler)
	if rec := o.do("POST", "/api/auth/login", `{"username":"owner","password":"synthetic-owner-pass"}`); rec.Code != 200 {
		t.Fatalf("owner login: %d %s", rec.Code, rec.Body.String())
	}
	v := newAuthClientForHandler(t, db, handler)
	if rec := v.do("POST", "/api/auth/login", `{"username":"reviewer","password":"synthetic-viewer-pass"}`); rec.Code != 200 {
		t.Fatalf("viewer login: %d %s", rec.Code, rec.Body.String())
	}

	// The viewer's session list holds only its own browser, and revoking
	// every other session id leaves the owner signed in.
	var ownerSession int64
	if err := db.QueryRow(`SELECT s.id FROM browser_sessions s JOIN users u ON u.id = s.user_id WHERE u.username = 'owner'`).Scan(&ownerSession); err != nil {
		t.Fatal(err)
	}
	if rec := v.do("GET", "/api/auth/sessions", ""); rec.Code != 200 || strings.Count(rec.Body.String(), `"id"`) != 1 {
		t.Fatalf("viewer sessions: %d %s", rec.Code, rec.Body.String())
	}
	if rec := v.do("DELETE", fmt.Sprintf("/api/auth/sessions/%d", ownerSession), ""); rec.Code != 204 {
		t.Fatalf("revoke foreign session: %d %s", rec.Code, rec.Body.String())
	}
	if rec := v.do("POST", "/api/auth/sessions/revoke", `{"scope":"others"}`); rec.Code != 204 && rec.Code != 200 {
		t.Fatalf("revoke others: %d %s", rec.Code, rec.Body.String())
	}
	if rec := o.do("GET", "/api/users", ""); rec.Code != 200 {
		t.Fatalf("owner was signed out by the viewer: %d %s", rec.Code, rec.Body.String())
	}

	// A viewer cannot mint API keys, change server settings or escalate itself.
	for _, attempt := range []struct{ method, path, body string }{
		{"POST", "/api/keys", `{"name":"x"}`},
		{"PATCH", fmt.Sprintf("/api/users/%d", viewer.ID), `{"role":"owner"}`},
		{"PUT", fmt.Sprintf("/api/users/%d/password", viewer.ID), `{"password":"new-pass-123"}`},
		{"PUT", "/api/settings/playback", `{}`},
	} {
		if rec := v.do(attempt.method, attempt.path, attempt.body); rec.Code != 403 {
			t.Fatalf("viewer %s %s = %d %s, want 403", attempt.method, attempt.path, rec.Code, rec.Body.String())
		}
	}

	// A body far larger than any API accepts is refused, not buffered.
	huge := `{"name":"` + strings.Repeat("a", auth.MaxAPIBodyBytes+1) + `"}`
	if rec := v.do("POST", "/api/profiles", huge); rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("oversized body = %d, want a 4xx", rec.Code)
	}

	// Locking the password stops the viewer changing it; disabling the
	// account ends its live session at once.
	if rec := o.do("PATCH", fmt.Sprintf("/api/users/%d", viewer.ID), `{"passwordLocked":true}`); rec.Code != 200 {
		t.Fatalf("lock: %d %s", rec.Code, rec.Body.String())
	}
	if rec := v.do("POST", "/api/auth/password", `{"currentPassword":"synthetic-viewer-pass","newPassword":"changed-pass-1"}`); rec.Code != 403 || !strings.Contains(rec.Body.String(), "password_locked") {
		t.Fatalf("locked change: %d %s", rec.Code, rec.Body.String())
	}
	if rec := o.do("PATCH", fmt.Sprintf("/api/users/%d", viewer.ID), `{"disabled":true}`); rec.Code != 200 {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body.String())
	}
	if rec := v.do("GET", "/api/profiles", ""); rec.Code != 401 {
		t.Fatalf("disabled account still served: %d %s", rec.Code, rec.Body.String())
	}
	if rec := v.do("POST", "/api/auth/login", `{"username":"reviewer","password":"synthetic-viewer-pass"}`); rec.Code != 401 {
		t.Fatalf("disabled account signed in: %d %s", rec.Code, rec.Body.String())
	}
}
