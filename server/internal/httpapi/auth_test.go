package httpapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"github.com/alreadyonapp/alreadyon/server/internal/playback"
	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
)

type authClient struct {
	t          *testing.T
	handler    http.Handler
	cookieName string
	cookie     *http.Cookie
}

func (c *authClient) do(method, path, body string) *httptest.ResponseRecorder {
	c.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "192.0.2.10:5555"
	if c.cookie != nil {
		r.AddCookie(c.cookie)
	}
	w := httptest.NewRecorder()
	c.handler.ServeHTTP(w, r)
	for _, ck := range w.Result().Cookies() {
		if ck.Name == c.cookieName {
			if ck.MaxAge < 0 {
				c.cookie = nil
			} else {
				c.cookie = ck
			}
		}
	}
	return w
}

func newAuthClient(t *testing.T) *authClient {
	db := openTestDB(t)
	return newAuthClientForHandler(t, db, auth.RequireAuth(db, NewRouter(t.TempDir(), t.TempDir(), db)))
}

func newAuthClientForHandler(t *testing.T, db *sql.DB, handler http.Handler) *authClient {
	t.Helper()
	cookieName, err := auth.SessionCookieName(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	return &authClient{t: t, handler: handler, cookieName: cookieName}
}

func authStatus(t *testing.T, c *authClient) authStatusResponse {
	t.Helper()
	w := c.do("GET", "/api/auth/status", "")
	var s authStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return s
}

func TestFirstRunSetupThenSignInFlow(t *testing.T) {
	c := newAuthClient(t)
	if s := authStatus(t, c); !s.SetupRequired || s.Authenticated {
		t.Fatalf("fresh server status = %+v, want setup required and signed out", s)
	}
	if w := c.do("GET", "/api/profiles", ""); w.Code != 401 {
		t.Errorf("API before setup: %d, want 401", w.Code)
	}
	if w := c.do("POST", "/api/auth/login", `{"username":"mike","password":"longenough1"}`); w.Code != http.StatusConflict {
		t.Errorf("login before setup: %d, want 409", w.Code)
	}
	if w := c.do("POST", "/api/auth/setup", `{"username":"mike","password":"123"}`); w.Code != 400 || !strings.Contains(w.Body.String(), "password") {
		t.Errorf("weak password: %d %s", w.Code, w.Body.String())
	}

	w := c.do("POST", "/api/auth/setup", `{"username":"mike","password":"longenough1"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	ck := c.cookie
	if ck == nil || !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode || ck.MaxAge != 30*24*60*60 || ck.Path != "/" {
		t.Fatalf("session cookie = %+v, want HttpOnly, SameSite=Lax, 30 days", ck)
	}
	if s := authStatus(t, c); s.SetupRequired || !s.Authenticated || s.Username != "mike" {
		t.Errorf("status after setup = %+v", s)
	}
	if w := c.do("GET", "/api/profiles", ""); w.Code != 200 {
		t.Errorf("API with the new session: %d", w.Code)
	}
	if w := c.do("POST", "/api/auth/setup", `{"username":"again","password":"longenough1"}`); w.Code != http.StatusConflict {
		t.Errorf("second setup: %d, want 409", w.Code)
	}

	c.do("POST", "/api/auth/logout", "")
	if c.cookie != nil {
		t.Error("logout must clear the cookie")
	}
	if w := c.do("GET", "/api/profiles", ""); w.Code != 401 {
		t.Errorf("after logout: %d", w.Code)
	}
	// The old cookie is dead server-side too, not just removed from the browser.
	c.cookie = ck
	if w := c.do("GET", "/api/profiles", ""); w.Code != 401 {
		t.Errorf("replayed cookie after logout: %d, want 401", w.Code)
	}
	c.cookie = nil

	if w := c.do("POST", "/api/auth/login", `{"username":"mike","password":"wrong-password"}`); w.Code != 401 || c.cookie != nil {
		t.Errorf("wrong password: %d", w.Code)
	}
	if w := c.do("POST", "/api/auth/login", `{"username":"mike","password":"longenough1"}`); w.Code != 200 || c.cookie == nil {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	if s := authStatus(t, c); !s.Authenticated || s.Username != "mike" {
		t.Errorf("status after login = %+v", s)
	}
}

func TestSeparateDatabasesUseSeparateSessionCookies(t *testing.T) {
	first := newAuthClient(t)
	second := newAuthClient(t)
	for _, c := range []*authClient{first, second} {
		if w := c.do("POST", "/api/auth/setup", `{"username":"mike","password":"longenough1"}`); w.Code != http.StatusCreated {
			t.Fatalf("setup: %d %s", w.Code, w.Body.String())
		}
	}
	if first.cookieName == second.cookieName {
		t.Fatalf("separate databases share cookie name %q", first.cookieName)
	}
	firstCookie := first.cookie
	second.cookie = firstCookie
	if w := second.do("GET", "/api/profiles", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("cookie from another database returned %d, want 401", w.Code)
	}
}

func TestRepeatedFailedLoginsAreThrottled(t *testing.T) {
	c := newAuthClient(t)
	c.do("POST", "/api/auth/setup", `{"username":"mike","password":"longenough1"}`)
	c.do("POST", "/api/auth/logout", "")
	for i := 0; i < 5; i++ {
		if w := c.do("POST", "/api/auth/login", `{"username":"mike","password":"nope-nope-nope"}`); w.Code != 401 {
			t.Fatalf("attempt %d: %d, want 401", i+1, w.Code)
		}
	}
	w := c.do("POST", "/api/auth/login", `{"username":"mike","password":"longenough1"}`)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Errorf("after 5 failures: %d retry-after %q, want 429 with a wait", w.Code, w.Header().Get("Retry-After"))
	}
}

func TestMediaAccessEndsWhenTheBrowserSessionEnds(t *testing.T) {
	db := openTestDB(t)
	sessions := playback.NewSessions(schedule.SystemClock{})
	p := NewPlaybackHTTP(db, sessions, nil)
	c := newAuthClientForHandler(t, db, auth.RequireAuth(db, NewRouter(t.TempDir(), t.TempDir(), db, p)))
	c.do("POST", "/api/auth/setup", `{"username":"mike","password":"longenough1"}`)

	w := c.do("POST", "/api/playback/probe", "")
	var issued struct{ Token string }
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &issued) != nil {
		t.Fatalf("probe with a session cookie: %d %s", w.Code, w.Body.String())
	}
	probe := func() int {
		rec := httptest.NewRecorder()
		p.MediaHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/media/0/probe?bytes=16&token="+issued.Token, nil))
		return rec.Code
	}
	if code := probe(); code != 200 {
		t.Fatalf("media request while signed in: %d", code)
	}
	c.do("POST", "/api/auth/logout", "")
	if code := probe(); code != 401 {
		t.Errorf("media request after sign-out: %d, want 401 (revoked with its session)", code)
	}
}

func TestSessionsListRevokeAndPasswordChange(t *testing.T) {
	db := openTestDB(t)
	handler := auth.RequireAuth(db, NewRouter(t.TempDir(), t.TempDir(), db))
	laptop := newAuthClientForHandler(t, db, handler)
	phone := newAuthClientForHandler(t, db, handler)
	laptop.do("POST", "/api/auth/setup", `{"username":"mike","password":"longenough1"}`)
	phone.do("POST", "/api/auth/login", `{"username":"mike","password":"longenough1"}`)

	var list []sessionResponse
	w := laptop.do("GET", "/api/auth/sessions", "")
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list) != 2 {
		t.Fatalf("sessions = %s (%v)", w.Body.String(), err)
	}
	current := 0
	var phoneID int64
	for _, s := range list {
		if s.Current {
			current++
		} else {
			phoneID = s.ID
		}
	}
	if current != 1 || phoneID == 0 {
		t.Fatalf("exactly one session must be marked current: %+v", list)
	}

	if w := laptop.do("DELETE", "/api/auth/sessions/"+strconv.FormatInt(phoneID, 10), ""); w.Code != 204 {
		t.Fatalf("revoke: %d", w.Code)
	}
	if w := phone.do("GET", "/api/profiles", ""); w.Code != 401 {
		t.Errorf("revoked phone: %d, want 401", w.Code)
	}
	if w := laptop.do("GET", "/api/profiles", ""); w.Code != 200 {
		t.Errorf("laptop must stay signed in: %d", w.Code)
	}

	phone.do("POST", "/api/auth/login", `{"username":"mike","password":"longenough1"}`)
	if w := laptop.do("POST", "/api/auth/password", `{"currentPassword":"nope-nope-nope","newPassword":"longenough2"}`); w.Code != 403 {
		t.Errorf("wrong current password: %d, want 403 (a 401 would sign the browser out)", w.Code)
	}
	if w := laptop.do("POST", "/api/auth/password", `{"currentPassword":"longenough1","newPassword":"123"}`); w.Code != 400 {
		t.Errorf("weak new password: %d, want 400", w.Code)
	}
	if w := laptop.do("POST", "/api/auth/password", `{"currentPassword":"longenough1","newPassword":"longenough2"}`); w.Code != 204 {
		t.Fatalf("change password: %d", w.Code)
	}
	if w := phone.do("GET", "/api/profiles", ""); w.Code != 401 {
		t.Errorf("phone after a password change: %d, want signed out", w.Code)
	}
	if w := laptop.do("GET", "/api/profiles", ""); w.Code != 200 {
		t.Errorf("laptop after its own password change: %d, want still signed in", w.Code)
	}

	phone.do("POST", "/api/auth/login", `{"username":"mike","password":"longenough2"}`)
	if w := laptop.do("POST", "/api/auth/sessions/revoke", `{"scope":"bogus"}`); w.Code != 400 {
		t.Errorf("bad scope: %d", w.Code)
	}
	w = laptop.do("POST", "/api/auth/sessions/revoke", `{"scope":"others"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"signedOut":1`) {
		t.Errorf("revoke others: %d %s", w.Code, w.Body.String())
	}
	if w := laptop.do("GET", "/api/profiles", ""); w.Code != 200 {
		t.Errorf("laptop after revoking others: %d", w.Code)
	}
	if w := laptop.do("POST", "/api/auth/sessions/revoke", `{"scope":"all"}`); w.Code != 200 || laptop.cookie != nil {
		t.Errorf("revoke all: %d cookie %v", w.Code, laptop.cookie)
	}
	if w := laptop.do("GET", "/api/profiles", ""); w.Code != 401 {
		t.Errorf("after signing out everywhere: %d", w.Code)
	}
}

func TestSessionLifetimeSettingAppliesToNewSignInsOnly(t *testing.T) {
	c := newAuthClient(t)
	c.do("POST", "/api/auth/setup", `{"username":"mike","password":"longenough1"}`)
	sessions := func() []sessionResponse {
		var list []sessionResponse
		if err := json.Unmarshal(c.do("GET", "/api/auth/sessions", "").Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		return list
	}
	if w := c.do("GET", "/api/auth/session-lifetime", ""); !strings.Contains(w.Body.String(), `"days":30`) {
		t.Fatalf("default: %s", w.Body.String())
	}
	if s := sessions(); len(s) != 1 || s[0].ExpiresAt == nil {
		t.Fatalf("the first session should expire: %+v", s)
	}
	for _, body := range []string{`{"days":-1}`, `{"days":3651}`, `{"days":"x"}`, `{"days":1,"extra":1}`} {
		if w := c.do("PUT", "/api/auth/session-lifetime", body); w.Code != 400 {
			t.Errorf("%s: %d, want 400", body, w.Code)
		}
	}
	if w := c.do("PUT", "/api/auth/session-lifetime", `{"days":0}`); w.Code != 200 {
		t.Fatalf("never: %d %s", w.Code, w.Body.String())
	}
	// Existing sessions keep their expiry; the next sign-in never expires.
	c.do("POST", "/api/auth/login", `{"username":"mike","password":"longenough1"}`)
	never, dated := 0, 0
	for _, s := range sessions() {
		if s.ExpiresAt == nil {
			never++
		} else {
			dated++
		}
	}
	if never != 1 || dated != 1 {
		t.Errorf("never-expiring %d, dated %d, want 1 and 1", never, dated)
	}
	if ck := c.cookie; ck == nil || ck.MaxAge < 3000*24*3600 {
		t.Errorf("a never-expiring session needs a long cookie: %+v", ck)
	}
	if w := c.do("PUT", "/api/auth/session-lifetime", `{"days":7}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if ck := func() *http.Cookie {
		c.do("POST", "/api/auth/login", `{"username":"mike","password":"longenough1"}`)
		return c.cookie
	}(); ck.MaxAge != 7*24*3600 {
		t.Errorf("seven days = %d seconds, got %d", 7*24*3600, ck.MaxAge)
	}
}
