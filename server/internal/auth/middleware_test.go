package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

func TestManagementAuthentication(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	raw, key, err := GenerateAPIKey(context.Background(), db.DB, mustOwner(t, db.DB), "test")
	if err != nil {
		t.Fatal(err)
	}
	h := RequireAuth(db.DB, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if PrincipalID(r.Context()) != key.ID {
			t.Error("missing authenticated identity")
		}
		w.WriteHeader(204)
	}))
	for index, path := range []string{"/api/guide", "/api/admin/api-keys", "/api/profiles/1/pin", "/api/playback/sessions"} {
		for _, token := range []string{"", "bad", raw} {
			r := httptest.NewRequest("POST", path, nil)
			r.RemoteAddr = fmt.Sprintf("192.0.2.%d:1234", index+1)
			if token != "" {
				r.Header.Set("Authorization", "Bearer "+token)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			want := 401
			if token == raw {
				want = 204
			}
			if w.Code != want {
				t.Fatalf("%s: %d", path, w.Code)
			}
		}
	}
	if err := RevokeAPIKey(context.Background(), db.DB, key.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := VerifyAPIKey(context.Background(), db.DB, raw); err == nil {
		t.Fatal("revoked key accepted")
	}
}

// The API docs page (Swagger UI) and the web app can be opened through any
// name the server answers to; their own POSTs must be same-origin each time,
// while a page on another site must still be refused.
func TestSameOriginAcceptsEveryNameTheServerIsReachedBy(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := RequireAuth(db.DB, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	post := func(host string, headers map[string]string) int {
		r := httptest.NewRequest("POST", "http://"+host+"/api/auth/logout", nil)
		r.Host = host
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for _, host := range []string{"127.0.0.1:19900", "localhost:19900", "media-box:19900", "media-box.lan", "tv.example.com"} {
		if got := post(host, map[string]string{"Origin": "http://" + host}); got != 204 {
			t.Errorf("Origin matching Host %s: %d, want 204", host, got)
		}
		if got := post(host, map[string]string{"Origin": "https://" + host, "Sec-Fetch-Site": "same-origin"}); got != 204 {
			t.Errorf("Sec-Fetch-Site same-origin on %s: %d, want 204", host, got)
		}
		if got := post(host, nil); got != 204 {
			t.Errorf("a non-browser client on %s: %d, want 204", host, got)
		}
	}
	// A rewritten Host is not authorized by an untrusted forwarding header.
	if got := post("127.0.0.1:19900", map[string]string{"Origin": "http://tv.example.com", "X-Forwarded-Host": "tv.example.com"}); got != 403 {
		t.Errorf("untrusted forwarded public name: %d, want 403", got)
	}
	// Another site, or the same machine under a different name than the page came from, is cross-origin.
	for name, headers := range map[string]map[string]string{
		"another site":            {"Origin": "https://evil.example"},
		"other name, same host":   {"Origin": "http://127.0.0.1:19900"},
		"other port":              {"Origin": "http://localhost:3000"},
		"browser says cross-site": {"Sec-Fetch-Site": "cross-site", "Origin": "http://localhost:19900"},
		"browser says same-site":  {"Sec-Fetch-Site": "same-site"},
		"forged forwarded host":   {"Origin": "https://evil.example", "X-Forwarded-Host": "tv.example.com"},
		"matching forged host":    {"Origin": "http://evil.example", "X-Forwarded-Host": "evil.example"},
		"origin with userinfo":    {"Origin": "http://user@localhost:19900"},
	} {
		if got := post("localhost:19900", headers); got != 403 {
			t.Errorf("%s: %d, want 403", name, got)
		}
	}
}

// HTTPS requests must not accept an HTTP origin merely because Host matches.
func TestTLSOriginScheme(t *testing.T) {
	for _, test := range []struct {
		origin string
		want   bool
	}{
		{"https://localhost:19900", true}, {"http://localhost:19900", false},
		{"https://evil.example", false},
	} {
		r := httptest.NewRequest("POST", "https://localhost:19900/api/auth/logout", nil)
		r.Header.Set("Origin", test.origin)
		if got := sameOrigin(r); got != test.want {
			t.Errorf("origin %s: %v", test.origin, got)
		}
	}
}

// Blocked peers cannot probe even valid credentials or rotate forwarded headers.
func TestAPIKeyThrottle(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	raw, _, err := GenerateAPIKey(context.Background(), db.DB, mustOwner(t, db.DB), "test")
	if err != nil {
		t.Fatal(err)
	}
	cookie, err := SessionCookieName(context.Background(), db.DB)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(10000, 0)
	limiter := NewLimiter(func() time.Time { return now })
	calls := 0
	h := requireAuthWithLimiter(db.DB, cookie, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(204) }), limiter)
	request := func(token, peer string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/guide", nil)
		r.RemoteAddr = peer
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Forwarded-For", token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for i := 0; i < freeAttempts; i++ {
		if w := request("wrong", "192.0.2.1:1234"); w.Code != 401 {
			t.Fatalf("failure %d: %d", i, w.Code)
		}
	}
	if w := request(raw, "192.0.2.1:5678"); w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal("locked peer could probe")
	}
	if calls != 0 {
		t.Fatal("locked request reached handler")
	}
	if w := request(raw, "192.0.2.2:1234"); w.Code != 204 {
		t.Fatal("independent peer blocked")
	}
	now = now.Add(baseLockout)
	if w := request(raw, "192.0.2.1:1234"); w.Code != 204 {
		t.Fatal("lockout did not expire")
	}
	if _, exists := limiter.clients["192.0.2.1"]; exists {
		t.Fatal("success did not reset")
	}
}

// Viewers reach only the allowlisted routes; everything else is Owner-only,
// including routes added later and never listed. A required password change
// confines a signed-in user to status, password change and sign-out.
func TestRoleAndPasswordChangeGates(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	owner := mustOwner(t, db.DB)
	viewer, err := CreateUser(ctx, db.DB, NewUser{Username: "guest", Password: "guestpass", Role: RoleViewer})
	if err != nil {
		t.Fatal(err)
	}
	cookie, err := SessionCookieName(ctx, db.DB)
	if err != nil {
		t.Fatal(err)
	}
	var seen User
	h := RequireAuthWithSessionCookie(db.DB, cookie, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = UserFrom(r.Context())
		w.WriteHeader(204)
	}))
	session := func(uid int64) string {
		token, _, _, err := CreateSession(ctx, db.DB, time.Now(), uid, "")
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	do := func(token, method, path string) (int, string) {
		r := httptest.NewRequest(method, path, nil)
		r.AddCookie(&http.Cookie{Name: cookie, Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	ownerToken, viewerToken := session(owner), session(viewer.ID)

	allowed := [][2]string{
		{"GET", "/api/guide"}, {"GET", "/api/profiles"}, {"PATCH", "/api/profiles/3"},
		{"POST", "/api/channels/5/tune"}, {"POST", "/api/playback/sessions"},
		{"DELETE", "/api/playback/sessions"}, {"GET", "/api/docs/"},
		{"GET", "/api/docs/swagger-ui.css"}, {"GET", "/api/openapi.yaml"},
		{"POST", "/api/auth/password"}, {"GET", "/api/auth/sessions"},
		{"HEAD", "/api/guide"},
		{"GET", "/api/server/details"}, {"GET", "/api/server/metrics"}, {"GET", "/api/server/activity"},
	}
	refused := [][2]string{
		{"GET", "/api/users"}, {"POST", "/api/users"}, {"GET", "/api/admin/api-keys"},
		{"POST", "/api/admin/api-keys"}, {"GET", "/api/channels"}, {"POST", "/api/channels"},
		{"GET", "/api/settings"}, {"POST", "/api/library/scan"},
		{"GET", "/api/settings/folders/browse"}, {"GET", "/api/settings/folders"}, {"PUT", "/api/settings/folders"},
		{"GET", "/api/channels/5/preview"},
		{"POST", "/api/server/restart"}, {"PUT", "/api/settings/server"},
		{"POST", "/api/server/metrics"}, {"DELETE", "/api/server/activity"},
		{"GET", "/api/some-future-route"}, {"PUT", "/api/guide"},
	}
	for _, c := range allowed {
		if code, _ := do(viewerToken, c[0], c[1]); code != 204 {
			t.Errorf("viewer %s %s: %d, want 204", c[0], c[1], code)
		} else if seen.ID != viewer.ID || seen.Role != RoleViewer {
			t.Errorf("viewer %s %s: handler saw %+v", c[0], c[1], seen)
		}
	}
	for _, c := range refused {
		if code, body := do(viewerToken, c[0], c[1]); code != 403 || !strings.Contains(body, `"forbidden"`) {
			t.Errorf("viewer %s %s: %d %s, want 403 forbidden", c[0], c[1], code, body)
		}
		if code, _ := do(ownerToken, c[0], c[1]); code != 204 {
			t.Errorf("owner %s %s: %d, want 204", c[0], c[1], code)
		}
	}

	if _, err := UpdateUser(ctx, db.DB, viewer.ID, UserPatch{MustChangePassword: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	viewerToken = session(viewer.ID)
	for _, c := range [][2]string{{"GET", "/api/auth/status"}, {"POST", "/api/auth/password"}, {"POST", "/api/auth/logout"}} {
		if code, _ := do(viewerToken, c[0], c[1]); code != 204 {
			t.Errorf("must-change %s %s: %d, want 204", c[0], c[1], code)
		}
	}
	for _, c := range [][2]string{{"GET", "/api/guide"}, {"GET", "/api/profiles"}, {"POST", "/api/playback/sessions"}} {
		if code, body := do(viewerToken, c[0], c[1]); code != 403 || !strings.Contains(body, `"password_change_required"`) {
			t.Errorf("must-change %s %s: %d %s, want 403 password_change_required", c[0], c[1], code, body)
		}
	}

	if _, err := UpdateUser(ctx, db.DB, viewer.ID, UserPatch{Disabled: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	if code, _ := do(viewerToken, "GET", "/api/auth/status"); code != 204 {
		// /api/auth/status is public; it simply reports signed out.
		t.Errorf("disabled status: %d", code)
	}
	if code, _ := do(viewerToken, "GET", "/api/guide"); code != 401 {
		t.Errorf("disabled user: %d, want 401", code)
	}
}

// Every protected request body is capped, so a handler that reads without
// its own limit still cannot be made to buffer an unbounded upload.
func TestProtectedRequestBodiesAreCapped(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	owner := mustOwner(t, db.DB)
	cookie, err := SessionCookieName(ctx, db.DB)
	if err != nil {
		t.Fatal(err)
	}
	token, _, _, err := CreateSession(ctx, db.DB, time.Now(), owner, "")
	if err != nil {
		t.Fatal(err)
	}
	var read int64
	var readErr error
	h := RequireAuthWithSessionCookie(db.DB, cookie, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		read, readErr = io.Copy(io.Discard, r.Body)
		w.WriteHeader(204)
	}))
	r := httptest.NewRequest("POST", "/api/anything", io.LimitReader(zeroReader{}, MaxAPIBodyBytes+1024))
	r.AddCookie(&http.Cookie{Name: cookie, Value: token})
	h.ServeHTTP(httptest.NewRecorder(), r)
	var tooLarge *http.MaxBytesError
	if read > MaxAPIBodyBytes || !errors.As(readErr, &tooLarge) {
		t.Fatalf("read %d bytes, err %v; want the read stopped at %d", read, readErr, MaxAPIBodyBytes)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

// Supported reverse proxies preserve Host and re-encrypt; they never supply identity.
func TestReverseProxyHeadersCannotChangePeerOrOrigin(t *testing.T) {
	for _, peer := range []string{"192.0.2.10:4567", "[2001:db8::10]:4567"} {
		r := httptest.NewRequest("POST", "https://tv.example/api/auth/logout", nil)
		r.RemoteAddr = peer
		r.Header.Set("Origin", "https://tv.example")
		r.Header.Set("Forwarded", "for=127.0.0.1;host=evil.example;proto=http")
		r.Header.Set("X-Forwarded-For", "127.0.0.1")
		r.Header.Set("X-Real-IP", "127.0.0.1")
		r.Header.Set("X-Forwarded-Host", "evil.example")
		r.Header.Set("X-Forwarded-Proto", "http")
		want := "192.0.2.10"
		if strings.HasPrefix(peer, "[") {
			want = "2001:db8::10"
		}
		if got := ClientAddress(r); got != want {
			t.Fatalf("forwarded identity accepted: %s", got)
		}
		if !sameOrigin(r) {
			t.Fatal("valid preserved Host/HTTPS rejected")
		}
		r.Header.Set("Origin", "http://evil.example")
		if sameOrigin(r) {
			t.Fatal("forged forwarded origin accepted")
		}
	}
}
