package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

func openAuthDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db.DB
}

// mustOwner creates the first-run owner account and returns its id.
func mustOwner(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	u, err := CreateAccount(context.Background(), db, "owner", "longenough1")
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func TestPasswordHashing(t *testing.T) {
	hash, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") || strings.Contains(hash, "correct horse") {
		t.Errorf("unexpected hash format: %q", hash)
	}
	if !VerifyPassword(hash, "correct horse battery") || VerifyPassword(hash, "correct horse batterY") {
		t.Error("verification must accept the password and reject others")
	}
	other, _ := HashPassword("correct horse battery")
	if other == hash {
		t.Error("two hashes of one password must differ (random salt)")
	}
	for _, bad := range []string{"", "plain", "$argon2id$v=19$m=99999999,t=2,p=1$YQ$YQ", "$argon2i$v=19$m=19456,t=2,p=1$YQ$YQ"} {
		if VerifyPassword(bad, "x") {
			t.Errorf("malformed hash %q accepted", bad)
		}
	}
}

// The shared account policy permits short plain passwords without complexity.
func TestFourCharacterPasswordPolicy(t *testing.T) {
	for _, password := range []string{"1234", "aaaa", "中文密码"} {
		if err := ValidatePassword(password); err != nil {
			t.Errorf("four-character password rejected: %v", err)
		}
	}
	if err := ValidatePassword("123"); !errors.Is(err, ErrInvalidPassword) {
		t.Errorf("three characters accepted: %v", err)
	}
}

func TestCreateAccountValidationAndSingleAccount(t *testing.T) {
	ctx, db := context.Background(), openAuthDB(t)
	if exists, _ := AccountExists(ctx, db); exists {
		t.Fatal("fresh database must have no account")
	}
	for name, c := range map[string][2]string{
		"empty username": {"  ", "longenough1"},
		"control chars":  {"bad\x00name", "longenough1"},
		"long username":  {strings.Repeat("a", MaxUsernameLength+1), "longenough1"},
		"short password": {"mike", "123"},
		"huge password":  {"mike", strings.Repeat("a", MaxPasswordBytes+1)},
	} {
		if _, err := CreateAccount(ctx, db, c[0], c[1]); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := CreateAccount(ctx, db, " mike ", "longenough1"); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if _, err := CreateAccount(ctx, db, "other", "longenough2"); !errors.Is(err, ErrAccountExists) {
		t.Errorf("second account: err = %v, want ErrAccountExists", err)
	}
	if u, ok, err := Authenticate(ctx, db, "MIKE", "longenough1"); err != nil || !ok || u.Username != "mike" || u.Role != RoleOwner {
		t.Errorf("Authenticate right = %+v, %v, %v", u, ok, err)
	}
	for _, c := range [][2]string{{"mike", "wrong-password"}, {"someone", "longenough1"}, {"", ""}} {
		if _, ok, _ := Authenticate(ctx, db, c[0], c[1]); ok {
			t.Errorf("Authenticate accepted %v", c)
		}
	}
}

func TestConcurrentFirstRunCreatesExactlyOneAccount(t *testing.T) {
	ctx, db := context.Background(), openAuthDB(t)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := CreateAccount(ctx, db, "mike", "longenough1"); results <- err }()
	}
	wg.Wait()
	close(results)
	created := 0
	for err := range results {
		if err == nil {
			created++
		}
	}
	if created != 1 {
		t.Errorf("%d concurrent setups succeeded, want exactly 1", created)
	}
}

func TestSessionsExpireAndAreRevocable(t *testing.T) {
	ctx, db := context.Background(), openAuthDB(t)
	now := time.Now()
	uid := mustOwner(t, db)
	token, id, expires, err := CreateSession(ctx, db, now, uid, "test browser")
	if err != nil {
		t.Fatal(err)
	}
	if want := now.Add(30 * 24 * time.Hour); !expires.Equal(want) {
		t.Errorf("expires = %v, want 30 days out (%v)", expires, want)
	}
	var stored string
	db.QueryRow(`SELECT token_hash FROM browser_sessions WHERE id=?`, id).Scan(&stored)
	if stored == token || stored == "" {
		t.Error("the raw token must not be stored")
	}
	if got, u, err := LookupSession(ctx, db, token, now.Add(29*24*time.Hour)); err != nil || got != id || u.ID != uid {
		t.Errorf("day 29: %d, %+v, %v", got, u, err)
	}
	if _, err := lookupID(ctx, db, token, now.Add(30*24*time.Hour+time.Second)); !errors.Is(err, ErrNoSession) {
		t.Errorf("day 30+: err = %v, want ErrNoSession", err)
	}
	if !PrincipalActive(ctx, db, -id, now) || PrincipalActive(ctx, db, -id, now.Add(31*24*time.Hour)) {
		t.Error("principal must follow session expiry")
	}
	if err := DeleteSession(ctx, db, uid+1, id); err != nil {
		t.Fatal(err)
	}
	if !PrincipalActive(ctx, db, -id, now) {
		t.Error("another account must not be able to revoke this session")
	}
	if err := DeleteSession(ctx, db, uid, id); err != nil {
		t.Fatal(err)
	}
	if _, err := lookupID(ctx, db, token, now); !errors.Is(err, ErrNoSession) || PrincipalActive(ctx, db, -id, now) {
		t.Error("a revoked session must stop working immediately")
	}
	if _, err := lookupID(ctx, db, "", now); err == nil {
		t.Error("empty token accepted")
	}
}

// lookupID returns only the session id, for tests that ignore the user.
func lookupID(ctx context.Context, db *sql.DB, token string, now time.Time) (int64, error) {
	id, _, err := LookupSession(ctx, db, token, now)
	return id, err
}

func TestLimiterLocksOutAndBacksOff(t *testing.T) {
	now := time.Now()
	l := NewLimiter(func() time.Time { return now })
	for i := 0; i < freeAttempts-1; i++ {
		l.Fail("a")
	}
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("locked out before the free attempts were used")
	}
	l.Fail("a")
	if ok, wait := l.Allow("a"); ok || wait != baseLockout {
		t.Fatalf("after %d failures: ok=%v wait=%v, want locked for %v", freeAttempts, ok, wait, baseLockout)
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Error("another client must not be locked out")
	}
	now = now.Add(baseLockout + time.Second)
	if ok, _ := l.Allow("a"); !ok {
		t.Error("lockout must end")
	}
	l.Fail("a")
	if _, wait := l.Allow("a"); wait != 2*baseLockout {
		t.Errorf("second lockout = %v, want doubled", wait)
	}
	for i := 0; i < 20; i++ {
		l.Fail("a")
	}
	if _, wait := l.Allow("a"); wait != maxLockout {
		t.Errorf("lockout = %v, want capped at %v", wait, maxLockout)
	}
	l.Succeed("a")
	if ok, _ := l.Allow("a"); !ok {
		t.Error("success must clear the lockout")
	}
}

func TestRequireAuthSessionsCSRFAndDocs(t *testing.T) {
	ctx, db := context.Background(), openAuthDB(t)
	uid := mustOwner(t, db)
	token, id, _, err := CreateSession(ctx, db, time.Now(), uid, "")
	if err != nil {
		t.Fatal(err)
	}
	cookieName, err := SessionCookieName(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	var seen int64
	h := RequireAuth(db, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = PrincipalID(r.Context()); w.WriteHeader(204) }))
	do := func(method, path string, mod func(*http.Request)) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		if mod != nil {
			mod(r)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	withCookie := func(r *http.Request) { r.AddCookie(&http.Cookie{Name: cookieName, Value: token}) }

	if w := do("GET", "/api/guide", withCookie); w.Code != 204 || seen != -id {
		t.Errorf("cookie session: %d principal %d, want 204 and %d", w.Code, seen, -id)
	}
	if w := do("GET", "/api/guide", nil); w.Code != 401 {
		t.Errorf("no credentials: %d", w.Code)
	}
	if w := do("GET", "/api/guide", func(r *http.Request) { r.AddCookie(&http.Cookie{Name: cookieName, Value: "forged"}) }); w.Code != 401 {
		t.Errorf("forged cookie: %d", w.Code)
	}
	if w := do("GET", "/profile-icons/x.png", nil); w.Code != 401 {
		t.Errorf("profile icon without a session: %d, want 401", w.Code)
	}
	if w := do("GET", "/assets/app.js", nil); w.Code != 204 {
		t.Errorf("static asset: %d, want public", w.Code)
	}
	for _, route := range []string{"GET /api/auth/status", "POST /api/auth/login", "POST /api/auth/setup", "POST /api/auth/logout"} {
		parts := strings.SplitN(route, " ", 2)
		if w := do(parts[0], parts[1], nil); w.Code != 204 {
			t.Errorf("%s: %d, want reachable while signed out", route, w.Code)
		}
	}

	// Cross-site writes cannot ride the cookie.
	cross := func(r *http.Request) { withCookie(r); r.Header.Set("Sec-Fetch-Site", "cross-site") }
	if w := do("POST", "/api/channels", cross); w.Code != 403 {
		t.Errorf("cross-site POST: %d, want 403", w.Code)
	}
	badOrigin := func(r *http.Request) { withCookie(r); r.Header.Set("Origin", "https://evil.example") }
	if w := do("POST", "/api/channels", badOrigin); w.Code != 403 {
		t.Errorf("foreign Origin POST: %d, want 403", w.Code)
	}
	if w := do("POST", "/api/auth/login", cross); w.Code != 403 {
		t.Errorf("cross-site login: %d, want 403", w.Code)
	}
	same := func(r *http.Request) { withCookie(r); r.Header.Set("Sec-Fetch-Site", "same-origin") }
	if w := do("POST", "/api/channels", same); w.Code != 204 {
		t.Errorf("same-origin POST: %d, want 204", w.Code)
	}
	sameHost := func(r *http.Request) { withCookie(r); r.Header.Set("Origin", "http://"+r.Host) }
	if w := do("POST", "/api/channels", sameHost); w.Code != 204 {
		t.Errorf("same-Origin POST: %d, want 204", w.Code)
	}

	// The API documentation sends a browser to sign in and back.
	w := do("GET", "/api/docs/index.html", func(r *http.Request) { r.Header.Set("Accept", "text/html,application/xhtml+xml") })
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/?next=%2Fapi%2Fdocs%2Findex.html" {
		t.Errorf("docs redirect: %d %q", w.Code, w.Header().Get("Location"))
	}
	if w := do("GET", "/api/docs/index.html", func(r *http.Request) { withCookie(r); r.Header.Set("Accept", "text/html") }); w.Code != 204 {
		t.Errorf("signed-in docs: %d, want passed through", w.Code)
	}
	if w := do("GET", "/api/openapi.yaml", nil); w.Code != 401 {
		t.Errorf("spec without a session: %d, want 401", w.Code)
	}

	if err := DeleteSession(ctx, db, uid, id); err != nil {
		t.Fatal(err)
	}
	if w := do("GET", "/api/guide", withCookie); w.Code != 401 {
		t.Errorf("revoked session: %d, want 401", w.Code)
	}
}

func TestChangePasswordChecksTheOldOneAndEndsOtherSessions(t *testing.T) {
	ctx, db := context.Background(), openAuthDB(t)
	if err := ChangePassword(ctx, db, 1, "x", "longenough2", 0); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("no account: err = %v", err)
	}
	owner, err := CreateAccount(ctx, db, "mike", "longenough1")
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := CreateUser(ctx, db, NewUser{Username: "guest", Password: "guestpass", Role: RoleViewer})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	_, mine, _, _ := CreateSession(ctx, db, now, owner.ID, "mine")
	_, other, _, _ := CreateSession(ctx, db, now, owner.ID, "other")
	_, guest, _, _ := CreateSession(ctx, db, now, viewer.ID, "guest")

	if err := ChangePassword(ctx, db, owner.ID, "wrong-password", "longenough2", mine); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("wrong current: err = %v", err)
	}
	if err := ChangePassword(ctx, db, owner.ID, "longenough1", "123", mine); !errors.Is(err, ErrInvalidPassword) {
		t.Errorf("weak new: err = %v", err)
	}
	if _, ok, _ := Authenticate(ctx, db, "mike", "longenough1"); !ok {
		t.Fatal("a refused change must leave the old password working")
	}
	if err := ChangePassword(ctx, db, owner.ID, "longenough1", "longenough2", mine); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := Authenticate(ctx, db, "mike", "longenough1"); ok {
		t.Error("old password still works")
	}
	if _, ok, _ := Authenticate(ctx, db, "mike", "longenough2"); !ok {
		t.Error("new password rejected")
	}
	if !PrincipalActive(ctx, db, -mine, now) || PrincipalActive(ctx, db, -other, now) {
		t.Error("the changing session must stay signed in and every other one must end")
	}
	if !PrincipalActive(ctx, db, -guest, now) {
		t.Error("changing one user's password must not sign out another user")
	}
	if _, err := UpdateUser(ctx, db, viewer.ID, UserPatch{PasswordLocked: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	if err := ChangePassword(ctx, db, viewer.ID, "guestpass", "newguest", guest); !errors.Is(err, ErrPasswordLocked) {
		t.Errorf("locked account: err = %v, want ErrPasswordLocked", err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestResetPasswordSignsEveryoneOutAndCanRename(t *testing.T) {
	ctx, db := context.Background(), openAuthDB(t)
	if err := ResetPassword(ctx, db, "", "longenough2"); !errors.Is(err, ErrNoAccount) {
		t.Fatalf("no account: err = %v", err)
	}
	owner, _ := CreateAccount(ctx, db, "mike", "longenough1")
	_, id, _, _ := CreateSession(ctx, db, time.Now(), owner.ID, "")
	if err := ResetPassword(ctx, db, "  michael ", "longenough2"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := Authenticate(ctx, db, "michael", "longenough2"); !ok {
		t.Error("renamed account with the new password rejected")
	}
	if PrincipalActive(ctx, db, -id, time.Now()) {
		t.Error("a reset must end every session")
	}
	if err := ResetPassword(ctx, db, "", "123"); !errors.Is(err, ErrInvalidPassword) {
		t.Errorf("weak password: err = %v", err)
	}
	if err := ResetPassword(ctx, db, "", "longenough3"); err != nil {
		t.Fatal(err)
	}
	if u, _ := GetUser(ctx, db, owner.ID); u.Username != "michael" {
		t.Errorf("username = %q, want it kept when not given", u.Username)
	}
}

func TestSessionLastActivity(t *testing.T) {
	db := openAuthDB(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	uid := mustOwner(t, db)
	token, id, expires, err := CreateSession(ctx, db, now, uid, "activity test")
	if err != nil {
		t.Fatal(err)
	}
	active := func() int64 {
		t.Helper()
		var n int64
		if err := db.QueryRow(`SELECT last_active_at_unix FROM browser_sessions WHERE id=?`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if active() != now.Unix() {
		t.Fatal("sign-in activity missing")
	}
	if _, err := lookupID(ctx, db, token, now.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	if active() != now.Unix() {
		t.Fatal("activity writes not throttled")
	}
	if _, err := lookupID(ctx, db, token, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if active() != now.Add(time.Minute).Unix() {
		t.Fatal("browser activity not updated")
	}
	if !PrincipalActive(ctx, db, -id, now.Add(2*time.Minute)) {
		t.Fatal("media principal not active")
	}
	if active() != now.Add(2*time.Minute).Unix() {
		t.Fatal("media activity not updated")
	}
	if _, err := lookupID(ctx, db, "invalid", now.Add(3*time.Minute)); err != ErrNoSession {
		t.Fatal(err)
	}
	if _, err := lookupID(ctx, db, token, expires.Add(time.Second)); err != ErrNoSession {
		t.Fatal(err)
	}
	if active() != now.Add(2*time.Minute).Unix() {
		t.Fatal("invalid session changed activity")
	}
	if _, err := db.Exec(`UPDATE browser_sessions SET last_active_at_unix=NULL WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	list, err := ListSessions(ctx, db, uid, now)
	if err != nil || len(list) != 1 || list[0].LastActiveAtUnix.Valid {
		t.Fatal("legacy unknown activity", list, err)
	}
	if _, err := lookupID(ctx, db, token, now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if active() != now.Add(4*time.Minute).Unix() {
		t.Fatal("legacy session did not acquire activity")
	}
}

// Usernames are unique regardless of case on create, owner edits and self-service renames.
func TestUsernamesAreUniqueIgnoringCase(t *testing.T) {
	ctx, db := context.Background(), openAuthDB(t)
	mustOwner(t, db)
	if _, err := CreateUser(ctx, db, NewUser{Username: "OWNER", Password: "guestpass", Role: RoleViewer}); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("create duplicate: err = %v", err)
	}
	guest, err := CreateUser(ctx, db, NewUser{Username: "guest", Password: "guestpass", Role: RoleViewer})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateUser(ctx, db, guest.ID, UserPatch{Username: ptr("Owner")}); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("owner rename to duplicate: err = %v", err)
	}
	if _, err := ChangeUsername(ctx, db, guest.ID, "guestpass", "owner"); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("self rename to duplicate: err = %v", err)
	}
	if name, err := ChangeUsername(ctx, db, guest.ID, "guestpass", "Guest"); err != nil || name != "Guest" {
		t.Fatalf("recase own name: %q, %v", name, err)
	}
}

// At least one enabled owner must always remain: demoting, disabling or deleting
// the last one fails, including an owner acting on their own account.
func TestLastEnabledOwnerIsAlwaysKept(t *testing.T) {
	ctx, db := context.Background(), openAuthDB(t)
	first := mustOwner(t, db)
	second, err := CreateUser(ctx, db, NewUser{Username: "second", Password: "guestpass", Role: RoleOwner})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateUser(ctx, db, first, UserPatch{Role: ptr(RoleViewer)}); err != nil {
		t.Fatalf("demote one of two owners: %v", err)
	}
	for name, patch := range map[string]UserPatch{
		"demote":  {Role: ptr(RoleViewer)},
		"disable": {Disabled: ptr(true)},
	} {
		if _, err := UpdateUser(ctx, db, second.ID, patch); !errors.Is(err, ErrLastOwner) {
			t.Fatalf("%s last owner: err = %v", name, err)
		}
	}
	if _, err := DeleteUser(ctx, db, second.ID); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("delete last owner: err = %v", err)
	}
	// A disabled owner does not count, so re-enabling must come before demotion.
	if _, err := UpdateUser(ctx, db, first, UserPatch{Role: ptr(RoleOwner), Disabled: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateUser(ctx, db, second.ID, UserPatch{Role: ptr(RoleViewer)}); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("demote with only a disabled owner left: err = %v", err)
	}
}

// Native persistence still uses the revocation ledger and does not change browser expiry.
func TestNativeSessionPersistsUntilRevoked(t *testing.T) {
	db := openAuthDB(t)
	uid := mustOwner(t, db)
	now := time.Now()
	token, id, expires, err := CreateNativeSession(t.Context(), db, now, uid, "native fixture")
	if err != nil {
		t.Fatal(err)
	}
	if expires.Unix() != NeverExpiresUnix {
		t.Fatal("native session has automatic expiry")
	}
	if _, _, err := LookupSession(t.Context(), db, token, now.AddDate(2, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if err := DeleteSession(t.Context(), db, uid, id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LookupSession(t.Context(), db, token, now); !errors.Is(err, ErrNoSession) {
		t.Fatal("revoked native session accepted")
	}
	_, _, browserExpiry, err := CreateSession(t.Context(), db, now, uid, "browser fixture")
	if err != nil || !browserExpiry.Equal(now.Add(DefaultSessionDays*24*time.Hour)) {
		t.Fatal("browser lifetime changed")
	}
}
