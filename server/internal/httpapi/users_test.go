package httpapi

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/alreadyonapp/alreadyon/server/internal/auth"
)

func TestUserManagementLifecycle(t *testing.T) {
	db := openTestDB(t)
	testOwnerID(t, db)
	icons := t.TempDir()
	handler := auth.RequireAuth(db, NewRouter(t.TempDir(), icons, db))
	o := newAuthClientForHandler(t, db, handler)
	if rec := o.do("POST", "/api/auth/login", `{"username":"owner","password":"synthetic-owner-pass"}`); rec.Code != 200 {
		t.Fatalf("owner login: %d %s", rec.Code, rec.Body.String())
	}

	rec := o.do("POST", "/api/users", `{"username":"reviewer","password":"first-pass-123","role":"viewer","mustChangePassword":true}`)
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created userResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Role != "viewer" || !created.MustChangePassword || created.MaxStreams != auth.DefaultViewerStreams {
		t.Fatalf("created = %+v", created)
	}
	if bytes := rec.Body.String(); containsAny(bytes, "first-pass-123", "hash") {
		t.Fatalf("response leaks the password: %s", bytes)
	}
	for _, bad := range []struct {
		body string
		code int
	}{
		{`{"username":"reviewer","password":"another-pass-1","role":"viewer"}`, 409},
		{`{"username":"x","password":"123","role":"viewer"}`, 400},
		{`{"username":"y","password":"long-enough-1","role":"admin"}`, 400},
		{`{"username":"z","password":"long-enough-1","role":"viewer","maxStreams":99}`, 400},
		{`{"username":"z","password":"long-enough-1","role":"viewer","isAdmin":true}`, 400},
	} {
		if rec := o.do("POST", "/api/users", bad.body); rec.Code != bad.code {
			t.Fatalf("create %s = %d %s, want %d", bad.body, rec.Code, rec.Body.String(), bad.code)
		}
	}

	// The viewer must change its password before anything else works, and
	// has no access to account management afterwards.
	v := newAuthClientForHandler(t, db, handler)
	if rec := v.do("POST", "/api/auth/login", `{"username":"reviewer","password":"first-pass-123"}`); rec.Code != 200 {
		t.Fatalf("viewer login: %d %s", rec.Code, rec.Body.String())
	}
	if rec := v.do("GET", "/api/profiles", ""); rec.Code != 403 || !containsAny(rec.Body.String(), "password_change_required") {
		t.Fatalf("before change: %d %s", rec.Code, rec.Body.String())
	}
	if rec := v.do("POST", "/api/auth/password", `{"currentPassword":"first-pass-123","newPassword":"second-pass-456"}`); rec.Code != 204 && rec.Code != 200 {
		t.Fatalf("change: %d %s", rec.Code, rec.Body.String())
	}
	if rec := v.do("GET", "/api/profiles", ""); rec.Code != 200 {
		t.Fatalf("after change: %d %s", rec.Code, rec.Body.String())
	}
	for _, path := range []string{"/api/users", fmt.Sprintf("/api/users/%d", created.ID)} {
		if rec := v.do("GET", path, ""); rec.Code != 403 {
			t.Fatalf("viewer GET %s = %d, want 403", path, rec.Code)
		}
	}
	if rec := v.do("PATCH", fmt.Sprintf("/api/users/%d", created.ID), `{"role":"owner"}`); rec.Code != 403 {
		t.Fatalf("viewer self-promotion = %d, want 403", rec.Code)
	}

	// Owner list, update and password reset.
	var list []userResponse
	_ = json.Unmarshal(o.do("GET", "/api/users", "").Body.Bytes(), &list)
	if len(list) != 2 {
		t.Fatalf("list = %+v", list)
	}
	if rec := o.do("PATCH", fmt.Sprintf("/api/users/%d", created.ID), `{"maxStreams":1,"passwordLocked":true}`); rec.Code != 200 {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	if rec := v.do("POST", "/api/auth/password", `{"currentPassword":"second-pass-456","newPassword":"third-pass-789"}`); rec.Code != 403 {
		t.Fatalf("locked password change = %d %s, want 403", rec.Code, rec.Body.String())
	}
	// A locked account forced to change its password could never sign in.
	if rec := o.do("PATCH", fmt.Sprintf("/api/users/%d", created.ID), `{"mustChangePassword":true}`); rec.Code != 400 {
		t.Fatalf("locked + must change = %d, want 400", rec.Code)
	}
	if rec := o.do("PUT", fmt.Sprintf("/api/users/%d/password", created.ID), `{"password":"owner-set-pass-1","mustChangePassword":true}`); rec.Code != 400 {
		t.Fatalf("locked reset with must change = %d, want 400", rec.Code)
	}
	if rec := o.do("POST", "/api/users", `{"username":"stuck","password":"stuck-pass","role":"viewer","mustChangePassword":true,"passwordLocked":true}`); rec.Code != 400 {
		t.Fatalf("create locked + must change = %d, want 400", rec.Code)
	}
	if rec := o.do("PUT", fmt.Sprintf("/api/users/%d/password", created.ID), `{"password":"owner-set-pass-1","mustChangePassword":false}`); rec.Code != 204 {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body.String())
	}
	if rec := v.do("GET", "/api/profiles", ""); rec.Code != 401 {
		t.Fatalf("viewer session survived a password reset: %d", rec.Code)
	}

	// The last owner is protected; the signed-in owner cannot delete itself.
	ownerID := testOwnerID(t, db)
	if rec := o.do("PATCH", fmt.Sprintf("/api/users/%d", ownerID), `{"role":"viewer"}`); rec.Code != 409 {
		t.Fatalf("demote last owner = %d, want 409", rec.Code)
	}
	if rec := o.do("DELETE", fmt.Sprintf("/api/users/%d", ownerID), ""); rec.Code != 409 {
		t.Fatalf("self delete = %d, want 409", rec.Code)
	}

	// Deleting removes the account, its profiles and uploaded icons.
	icon := filepath.Join(icons, "77-1.png")
	if err := os.WriteFile(icon, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE profiles SET icon='/profile-icons/77-1.png' WHERE user_id=? AND name='Kids'`, created.ID); err != nil {
		t.Fatal(err)
	}
	if rec := o.do("DELETE", fmt.Sprintf("/api/users/%d", created.ID), ""); rec.Code != 204 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM profiles WHERE user_id=?`, created.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("%d profiles survived account deletion", n)
	}
	if _, err := os.Stat(icon); !os.IsNotExist(err) {
		t.Fatalf("uploaded icon survived account deletion: %v", err)
	}
	if rec := o.do("GET", fmt.Sprintf("/api/users/%d", created.ID), ""); rec.Code != 404 {
		t.Fatalf("deleted user GET = %d", rec.Code)
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && len(s) >= len(sub) {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}

func TestChangeOwnUsername(t *testing.T) {
	db := openTestDB(t)
	testOwnerID(t, db)
	handler := auth.RequireAuth(db, NewRouter(t.TempDir(), t.TempDir(), db))
	o := newAuthClientForHandler(t, db, handler)
	if rec := o.do("POST", "/api/auth/login", `{"username":"owner","password":"synthetic-owner-pass"}`); rec.Code != 200 {
		t.Fatalf("owner login: %d %s", rec.Code, rec.Body.String())
	}
	rec := o.do("POST", "/api/users", `{"username":"viewer1","password":"viewer-pass-1","role":"viewer"}`)
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var viewer userResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &viewer)
	v := newAuthClientForHandler(t, db, handler)
	if rec := v.do("POST", "/api/auth/login", `{"username":"viewer1","password":"viewer-pass-1"}`); rec.Code != 200 {
		t.Fatalf("viewer login: %d %s", rec.Code, rec.Body.String())
	}

	for _, bad := range []struct {
		body string
		code int
	}{
		{`{"currentPassword":"wrong","newUsername":"renamed"}`, 403},
		{`{"currentPassword":"viewer-pass-1","newUsername":"owner"}`, 409},
		{`{"currentPassword":"viewer-pass-1","newUsername":""}`, 400},
		{`{"currentPassword":"viewer-pass-1","newUsername":"renamed","role":"owner"}`, 400},
	} {
		if rec := v.do("POST", "/api/auth/username", bad.body); rec.Code != bad.code {
			t.Fatalf("rename %s = %d %s, want %d", bad.body, rec.Code, rec.Body.String(), bad.code)
		}
	}
	rec = v.do("POST", "/api/auth/username", `{"currentPassword":"viewer-pass-1","newUsername":"renamed"}`)
	if rec.Code != 200 || !containsAny(rec.Body.String(), `"renamed"`) {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body.String())
	}
	// The session belongs to the account, so it survives the rename.
	if rec := v.do("GET", "/api/auth/status", ""); !containsAny(rec.Body.String(), `"renamed"`) {
		t.Fatalf("status after rename: %s", rec.Body.String())
	}
	if rec := v.do("GET", "/api/profiles", ""); rec.Code != 200 {
		t.Fatalf("session after rename: %d", rec.Code)
	}

	// A password-locked (demo) account cannot rename itself.
	if rec := o.do("PATCH", fmt.Sprintf("/api/users/%d", viewer.ID), `{"passwordLocked":true}`); rec.Code != 200 {
		t.Fatalf("lock: %d %s", rec.Code, rec.Body.String())
	}
	rec = v.do("POST", "/api/auth/username", `{"currentPassword":"viewer-pass-1","newUsername":"other"}`)
	if rec.Code != 403 || !containsAny(rec.Body.String(), "password_locked") {
		t.Fatalf("locked rename = %d %s, want 403 password_locked", rec.Code, rec.Body.String())
	}
}
