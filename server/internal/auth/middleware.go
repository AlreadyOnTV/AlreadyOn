package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"

	"github.com/alreadyonapp/alreadyon/server/internal/logging"
	"net/url"
	"strings"
	"time"
)

type principalKey struct{}

// PrincipalID identifies who a management request is made as: a positive id is
// an API key, a negative id a browser session, zero nobody (see PrincipalActive).
func PrincipalID(ctx context.Context) int64 { id, _ := ctx.Value(principalKey{}).(int64); return id }

type userKey struct{}

type checkedKey struct{}

// Checked reports whether RequireAuth processed this request, signed in or
// not. Test-only routers use it to tell a bare request from an anonymous one.
func Checked(ctx context.Context) bool { return ctx.Value(checkedKey{}) != nil }

// WithUser returns ctx carrying the signed-in user. RequireAuth sets it;
// handlers read it with UserFrom.
func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, userKey{}, u)
}

// UserFrom returns the signed-in user of a request, if any. Handlers that
// need a user must refuse the request when ok is false (fail closed).
func UserFrom(ctx context.Context) (u User, ok bool) {
	u, ok = ctx.Value(userKey{}).(User)
	return u, ok && u.ID > 0
}

// VerifyAPIKey returns the key's id and the enabled user it acts as.
func VerifyAPIKey(ctx context.Context, db *sql.DB, raw string) (int64, User, error) {
	// Local test-key provisioning permits short credentials. Authentication
	// still requires an explicitly registered hash; no default key is accepted.
	if len(raw) < 4 || len(raw) > 128 {
		return 0, User{}, sql.ErrNoRows
	}
	sum := sha256.Sum256([]byte(raw))
	var id int64
	var u User
	var role string
	err := db.QueryRowContext(ctx, `
		SELECT k.id, u.id, u.username, u.role, u.disabled, u.must_change_password, u.password_locked, u.max_streams, u.created_at_unix, u.updated_at_unix
		FROM api_keys k JOIN users u ON u.id = k.user_id
		WHERE k.key_hash = ? AND k.key_prefix <> 'test-key' AND u.disabled = 0 AND u.role = 'owner'`, hex.EncodeToString(sum[:])).
		Scan(&id, &u.ID, &u.Username, &role, &u.Disabled, &u.MustChangePassword, &u.PasswordLocked, &u.MaxStreams, &u.CreatedAtUnix, &u.UpdatedAtUnix)
	if errors.Is(err, sql.ErrNoRows) {
		var hash string
		id, hash, err = findTestingKey(ctx, db, raw)
		if err != nil {
			return 0, User{}, err
		}
		if !strings.HasPrefix(hash, "$argon2id$") {
			upgraded, hashErr := HashPassword(raw)
			if hashErr != nil {
				return 0, User{}, hashErr
			}
			// A concurrent revocation must never be undone by migration.
			if _, err = db.ExecContext(ctx, `UPDATE api_keys SET key_hash=? WHERE id=? AND key_hash=? AND key_prefix='test-key'`, upgraded, id, hash); err != nil {
				return 0, User{}, err
			}
			hash = upgraded
		}
		err = db.QueryRowContext(ctx, `
			SELECT k.id, u.id, u.username, u.role, u.disabled, u.must_change_password, u.password_locked, u.max_streams, u.created_at_unix, u.updated_at_unix
			FROM api_keys k JOIN users u ON u.id=k.user_id
			WHERE k.id=? AND k.key_hash=? AND k.key_prefix='test-key' AND u.disabled=0 AND u.role='owner'`, id, hash).
			Scan(&id, &u.ID, &u.Username, &role, &u.Disabled, &u.MustChangePassword, &u.PasswordLocked, &u.MaxStreams, &u.CreatedAtUnix, &u.UpdatedAtUnix)
	}
	u.Role = Role(role)
	return id, u, err
}

// MaxAPIBodyBytes caps every protected request body, so a signed-in account
// (including a viewer) cannot exhaust server memory through a handler that
// decodes JSON without its own limit.
const MaxAPIBodyBytes = 8 << 20

// publicRoutes are the only protected-prefix routes usable without signing in:
// they are how a browser learns the sign-in state, creates the first account
// and signs in or out.
var publicRoutes = map[string]bool{
	"GET /api/tv/server":      true,
	"GET /api/tv/trust":       true,
	"POST /api/auth/tv-login": true,
	"GET /api/auth/status":    true,
	"POST /api/auth/setup":    true,
	"POST /api/auth/login":    true,
	"POST /api/auth/logout":   true,
}

// RequireAuth requires a browser session cookie or an API key bearer token for
// every API route, the API documentation and uploaded profile icons. The web
// application's own static files stay public so the sign-in page can load.
// Requests that could carry a cookie without the page having asked for it must
// come from the same origin, which is the CSRF defense.
func RequireAuth(db *sql.DB, next http.Handler) http.Handler {
	cookieName, err := SessionCookieName(context.Background(), db)
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "session configuration unavailable", http.StatusInternalServerError)
		})
	}
	return RequireAuthWithSessionCookie(db, cookieName, next)
}

// RequireAuthWithSessionCookie is RequireAuth with the database's cookie name
// already loaded. Server startup uses it so configuration errors fail before
// either listener begins accepting requests.
func RequireAuthWithSessionCookie(db *sql.DB, cookieName string, next http.Handler) http.Handler {
	return requireAuthWithLimiter(db, cookieName, next, NewLimiter(time.Now))
}

func requireAuthWithLimiter(db *sql.DB, cookieName string, next http.Handler, limiter *Limiter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Apply to the public login shell too; do not expose session-bearing pages
		// to framing or leak URL-scoped playback authorization in referrers.
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
		path := r.URL.Path
		isAPI := strings.HasPrefix(path, "/api/")
		if !isAPI && !strings.HasPrefix(path, "/profile-icons/") {
			next.ServeHTTP(w, r)
			return
		}
		// No API body legitimately approaches this (the largest is a 4 MiB
		// filter list); handlers that expect less apply their own tighter cap.
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, MaxAPIBodyBytes)
		}
		if isUnsafe(r.Method) && r.Header.Get("Authorization") == "" && !sameOrigin(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"cross-site request refused"}`))
			return
		}
		// Use the socket peer, never untrusted forwarding headers. Missing
		// credentials and expired browser cookies do not count as key guesses.
		client := ClientAddress(r)
		bearerAttempt := r.Header.Get("Authorization") != ""
		if bearerAttempt {
			if ok, wait := limiter.Allow(client); !ok {
				w.Header().Set("Retry-After", strconv.Itoa(int((wait+time.Second-1)/time.Second)))
				w.Header().Set("Cache-Control", "no-store")
				http.Error(w, "too many failed authentication attempts", http.StatusTooManyRequests)
				return
			}
		}
		principal, user := authenticate(r, db, cookieName)
		if bearerAttempt {
			if principal == 0 {
				limiter.Fail(client)
				logging.Warn("API-key authentication refused for %s", client)
			} else {
				limiter.Succeed(client)
			}
		}
		if principal == 0 && !publicRoutes[r.Method+" "+path] {
			if wantsDocsPage(r) {
				http.Redirect(w, r, "/?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
				return
			}
			reject(w)
			return
		}
		if isAPI {
			w.Header().Set("Cache-Control", "no-store")
		}
		if principal != 0 && !publicRoutes[r.Method+" "+path] {
			if !RoleAllows(user.Role, r) {
				refuse(w, http.StatusForbidden, "forbidden", "your account cannot use this")
				return
			}
			if user.MustChangePassword && !passwordChangeRoutes[r.Method+" "+path] {
				refuse(w, http.StatusForbidden, "password_change_required", "change your password to continue")
				return
			}
		}
		ctx := context.WithValue(context.WithValue(r.Context(), checkedKey{}, true), principalKey{}, principal)
		if principal != 0 {
			ctx = WithUser(ctx, user)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func authenticate(r *http.Request, db *sql.DB, cookieName string) (int64, User) {
	if header := r.Header.Get("Authorization"); header != "" {
		if !strings.HasPrefix(header, "Bearer ") {
			return 0, User{}
		}
		id, u, err := VerifyAPIKey(r.Context(), db, strings.TrimPrefix(header, "Bearer "))
		if err != nil {
			return 0, User{}
		}
		return id, u
	}
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return 0, User{}
	}
	id, u, err := LookupSession(r.Context(), db, cookie.Value, time.Now())
	if err != nil {
		return 0, User{}
	}
	// Address telemetry must not reject an otherwise valid session.
	_ = RecordSessionIP(r.Context(), db, id, r.RemoteAddr)
	return sessionPrincipal(id), u
}

func isUnsafe(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

// sameOrigin trusts the browser's own Sec-Fetch-Site, falling back to Origin
// for browsers that lack it. Requests with neither come from non-browser
// clients, which have no ambient cookie to abuse.
func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin" || site == "none"
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		// Forwarded headers are not authenticated. Proxies must preserve Host
		// until an explicit trusted-proxy policy has been implemented.
		return err == nil && u.Scheme == scheme && u.Host == r.Host && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
	}
	return true
}

// wantsDocsPage is a browser navigating to the API documentation, which is
// sent to the sign-in page instead of shown a JSON error.
func wantsDocsPage(r *http.Request) bool {
	return r.Method == http.MethodGet && (r.URL.Path == "/api/docs" || strings.HasPrefix(r.URL.Path, "/api/docs/")) &&
		strings.Contains(r.Header.Get("Accept"), "text/html")
}

func refuse(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"error":%q,"code":%q}`, message, code)
}

func reject(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("WWW-Authenticate", "Bearer")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"sign in required"}`))
}

// ClientAddress identifies the direct socket peer. Forwarded headers are ignored
// until a separately reviewed trusted-proxy policy exists (#39).
func ClientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
