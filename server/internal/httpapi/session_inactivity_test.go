package httpapi

// These API tests cover inactivity-policy validation, ownership, and cleanup.

import (
	"strings"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/auth"
)

func TestSessionInactivityPolicyAPI(t *testing.T) {
	c := newAuthClient(t)
	c.do("POST", "/api/auth/setup", `{"username":"owner","password":"password123"}`)
	if w := c.do("GET", "/api/auth/session-inactivity", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":true`) || !strings.Contains(w.Body.String(), `"days":14`) {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, body := range []string{`{}`, `{"days":14}`, `{"enabled":true,"days":0}`, `{"enabled":true,"days":3651}`} {
		if w := c.do("PUT", "/api/auth/session-inactivity", body); w.Code != 400 {
			t.Fatal(body, w.Code, w.Body.String())
		}
	}
	if w := c.do("PUT", "/api/auth/session-inactivity", `{"enabled":false,"days":21}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := c.do("GET", "/api/auth/session-inactivity", ""); !strings.Contains(w.Body.String(), `"days":21`) || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatal(w.Body.String())
	}
}

func TestInactivityEnablePrunesExistingSessionsAndIsOwnerOnly(t *testing.T) {
	db := openTestDB(t)
	handler := auth.RequireAuth(db, NewRouter(t.TempDir(), t.TempDir(), db))
	c := newAuthClientForHandler(t, db, handler)
	c.do("POST", "/api/auth/setup", `{"username":"owner","password":"password123"}`)
	c.do("PUT", "/api/auth/session-inactivity", `{"enabled":false,"days":14}`)
	var uid int64
	if err := db.QueryRow(`SELECT id FROM users WHERE username='owner'`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	_, id, _, err := auth.CreateNativeSession(t.Context(), db, time.Now().Add(-15*24*time.Hour), uid, "idle native fixture")
	if err != nil {
		t.Fatal(err)
	}
	if w := c.do("PUT", "/api/auth/session-inactivity", `{"enabled":true,"days":14}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM browser_sessions WHERE id=?`, id).Scan(&count); err != nil || count != 0 {
		t.Fatal("enabling policy did not remove idle sessions", count, err)
	}
	user, err := auth.CreateUser(t.Context(), db, auth.NewUser{Username: "viewer", Password: "viewer-password", Role: auth.RoleViewer})
	if err != nil {
		t.Fatal(err)
	}
	token, _, _, err := auth.CreateSession(t.Context(), db, time.Now(), user.ID, "viewer fixture")
	if err != nil {
		t.Fatal(err)
	}
	c.cookie.Value = token
	if w := c.do("GET", "/api/auth/session-inactivity", ""); w.Code != 403 {
		t.Fatal("viewer read server-wide policy", w.Code)
	}
	if w := c.do("PUT", "/api/auth/session-inactivity", `{"enabled":false,"days":14}`); w.Code != 403 {
		t.Fatal("viewer changed server-wide policy", w.Code)
	}
}
