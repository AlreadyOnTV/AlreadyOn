package httpapi

import (
	"database/sql"
	"errors"
	"github.com/alreadyonapp/alreadyon/server/internal/serversettings"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"github.com/alreadyonapp/alreadyon/server/internal/logging"
)

type authStatusResponse struct {
	// SetupRequired is true until the server account has been created.
	SetupRequired bool   `json:"setupRequired"`
	Authenticated bool   `json:"authenticated"`
	Username      string `json:"username,omitempty"`
	// Role is the signed-in account's role: "owner" or "viewer".
	Role string `json:"role,omitempty"`
	// MustChangePassword is true when the account must choose a new password
	// before anything but this status, POST /api/auth/password and sign-out works.
	MustChangePassword bool `json:"mustChangePassword"`
	// PasswordLocked is true when only an owner may change this account's password.
	PasswordLocked bool `json:"passwordLocked"`
}

func statusOf(u auth.User) authStatusResponse {
	return authStatusResponse{
		Authenticated: true, Username: u.Username, Role: string(u.Role),
		MustChangePassword: u.MustChangePassword, PasswordLocked: u.PasswordLocked,
	}
}

type credentialsBody struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func clientAddr(r *http.Request) string {
	return auth.ClientAddress(r)
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, cookieName, token string, expires time.Time) {
	// Browsers cap cookie lifetimes anyway; "never" is asked for as ten years.
	maxAge := int((min(time.Until(expires), auth.MaxSessionDays*24*time.Hour) + time.Second/2) / time.Second)
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: token, Path: "/", Expires: time.Now().Add(time.Duration(maxAge) * time.Second), MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil,
	})
}

func statusFor(r *http.Request, db *sql.DB) (authStatusResponse, error) {
	exists, err := auth.AccountExists(r.Context(), db)
	if err != nil {
		return authStatusResponse{}, err
	}
	if u, ok := auth.UserFrom(r.Context()); ok && exists {
		return statusOf(u), nil
	}
	return authStatusResponse{SetupRequired: !exists}, nil
}

// signIn starts a session for the request and answers with the new status.
func signIn(w http.ResponseWriter, r *http.Request, db *sql.DB, cookieName string, code int, u auth.User) {
	signInSession(w, r, db, cookieName, code, u, false)
}

// Native PIN pairing uses the same revocable session ledger with no automatic expiry.
func signInSession(w http.ResponseWriter, r *http.Request, db *sql.DB, cookieName string, code int, u auth.User, native bool) {
	create := auth.CreateSession
	if native {
		create = auth.CreateNativeSession
	}
	token, id, expires, err := create(r.Context(), db, time.Now(), u.ID, r.UserAgent())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not start a session")
		return
	}
	if native {
		setNativeSessionCookie(w, r, cookieName, token)
	} else {
		setSessionCookie(w, r, cookieName, token, expires)
	}
	_ = auth.RecordSessionIP(r.Context(), db, id, r.RemoteAddr)
	logging.Info("signed in: %s (%s) session %d started, valid until %s", u.Username, u.Role, id, expires.Format(time.RFC3339))
	writeJSON(w, code, statusOf(u))
}

// Native stores honor a durable expiry directly; session revocation remains authoritative.
func setNativeSessionCookie(w http.ResponseWriter, r *http.Request, name, token string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: token, Path: "/", Expires: time.Unix(auth.NeverExpiresUnix, 0), HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode})
}

func handleAuthStatus(db *sql.DB, cookieName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp, err := statusFor(r, db)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		// Refresh legacy native cookies only after middleware authenticates the migrated session.
		if principal := auth.PrincipalID(r.Context()); r.TLS != nil && resp.Authenticated && principal < 0 {
			var expiry int64
			var agent string
			if db.QueryRowContext(r.Context(), "SELECT expires_at_unix,user_agent FROM browser_sessions WHERE id=?", -principal).Scan(&expiry, &agent) == nil && expiry == auth.NeverExpiresUnix && (agent == "AlreadyOn-Android/TV" || agent == "AlreadyOn-Android/Mobile") {
				if cookie, err := r.Cookie(cookieName); err == nil {
					setNativeSessionCookie(w, r, cookieName, cookie.Value)
				}
			}
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func credentialMessage(err error) string {
	return strings.TrimPrefix(err.Error(), "auth: ")
}

// handleAuthSetup serves POST /api/auth/setup: creates the one server account
// on first run and signs the browser in. It is refused once an account exists.
func handleAuthSetup(db *sql.DB, cookieName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			credentialsBody
			ServerName *string `json:"serverName"`
		}
		if !decodeStrict(w, r, &body) {
			return
		}
		// An initialized server must not spend Argon2 work on setup requests.
		exists, err := auth.AccountExists(r.Context(), db)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "could not check account setup")
			return
		}
		if exists {
			writeJSONError(w, http.StatusConflict, "The server already has an account. Sign in instead.")
			return
		}
		serverName := "AlreadyOn" // Backward-compatible default for existing API setup clients.
		if body.ServerName != nil {
			serverName = *body.ServerName
		}
		user, err := auth.CreateNamedAccount(r.Context(), db, body.Username, body.Password, serverName)
		switch {
		case errors.Is(err, serversettings.ErrInvalidServerName):
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		case errors.Is(err, auth.ErrInvalidUsername), errors.Is(err, auth.ErrInvalidPassword):
			writeJSONError(w, http.StatusBadRequest, credentialMessage(err))
			return
		case errors.Is(err, auth.ErrAccountExists):
			writeJSONError(w, http.StatusConflict, "The server already has an account. Sign in instead.")
			return
		case err != nil:
			writeJSONError(w, http.StatusInternalServerError, "could not create the account")
			return
		}
		logging.Info("server owner account created")
		signIn(w, r, db, cookieName, http.StatusCreated, user)
	}
}

// handleAuthLogin serves POST /api/auth/login.
func handleAuthLogin(db *sql.DB, limiter *auth.Limiter, cookieName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body credentialsBody
		if !decodeStrict(w, r, &body) {
			return
		}
		client := clientAddr(r)
		if ok, wait := limiter.Allow(client); !ok {
			seconds := int(wait.Round(time.Second) / time.Second)
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			writeJSONError(w, http.StatusTooManyRequests, "Too many failed sign-in attempts. Try again in "+wait.Round(time.Second).String()+".")
			return
		}
		exists, err := auth.AccountExists(r.Context(), db)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !exists {
			writeJSONError(w, http.StatusConflict, "The server has no account yet. Create one first.")
			return
		}
		user, ok, err := auth.Authenticate(r.Context(), db, body.Username, body.Password)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !ok {
			limiter.Fail(client)
			logging.Warn("failed sign-in attempt from %s", client)
			writeJSONError(w, http.StatusUnauthorized, "That username or password wasn't accepted.")
			return
		}
		limiter.Succeed(client)
		signIn(w, r, db, cookieName, http.StatusOK, user)
	}
}

// handleAuthLogout serves POST /api/auth/logout: ends this browser's session.
func handleAuthLogout(db *sql.DB, cookieName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if u, ok := auth.UserFrom(r.Context()); ok && auth.PrincipalID(r.Context()) < 0 {
			if err := auth.DeleteSession(r.Context(), db, u.ID, -auth.PrincipalID(r.Context())); err != nil {
				writeJSONError(w, http.StatusInternalServerError, "internal error")
				return
			}
		}
		clearSessionCookie(w, r, cookieName)
		w.WriteHeader(http.StatusNoContent)
	}
}

type sessionResponse struct {
	DeviceName   string  `json:"deviceName"`
	DeviceType   string  `json:"deviceType"`
	IPAddress    string  `json:"ipAddress"`
	LastActiveAt *string `json:"lastActiveAt"`
	ID           int64   `json:"id"`
	UserAgent    string  `json:"userAgent"`
	CreatedAt    string  `json:"createdAt"`
	// ExpiresAt is null when the session never expires.
	ExpiresAt *string `json:"expiresAt"`
	// Current marks the session making this request.
	Current bool `json:"current"`
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request, cookieName string) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil})
}

// handleListSessions serves GET /api/auth/sessions: every browser currently
// signed in to the caller's own account.
func handleListSessions(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := requireUser(w, r)
		if !ok {
			return
		}
		list, err := auth.ListSessions(r.Context(), db, u.ID, time.Now())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		current := -auth.PrincipalID(r.Context())
		resp := make([]sessionResponse, 0, len(list))
		for _, s := range list {
			name, kind := sessionDevice(s.UserAgent)
			resp = append(resp, sessionResponse{
				DeviceName: name, DeviceType: kind, IPAddress: s.IPAddress,
				ID: s.ID, UserAgent: s.UserAgent, Current: s.ID == current,
				CreatedAt: time.Unix(s.CreatedAtUnix, 0).UTC().Format(time.RFC3339),
			})
			if s.LastActiveAtUnix.Valid {
				active := time.Unix(s.LastActiveAtUnix.Int64, 0).UTC().Format(time.RFC3339)
				resp[len(resp)-1].LastActiveAt = &active
			}
			if s.ExpiresAtUnix < auth.NeverExpiresUnix {
				at := time.Unix(s.ExpiresAtUnix, 0).UTC().Format(time.RFC3339)
				resp[len(resp)-1].ExpiresAt = &at
			}
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// handleRevokeSession serves DELETE /api/auth/sessions/{id}: signs one browser out.
func handleRevokeSession(db *sql.DB, cookieName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid session id")
			return
		}
		u, ok := requireUser(w, r)
		if !ok {
			return
		}
		// Scoped to the caller: another account's session id is a silent no-op.
		if err := auth.DeleteSession(r.Context(), db, u.ID, id); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		logging.Info("session %d signed out", id)
		if id == -auth.PrincipalID(r.Context()) {
			clearSessionCookie(w, r, cookieName)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleRevokeSessions serves POST /api/auth/sessions/revoke: signs out every
// other browser ("others") or every browser including this one ("all").
func handleRevokeSessions(db *sql.DB, cookieName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Scope string `json:"scope"`
		}
		if !decodeStrict(w, r, &body) {
			return
		}
		keep := -auth.PrincipalID(r.Context())
		switch body.Scope {
		case "others":
		case "all":
			keep = 0
		default:
			writeJSONError(w, http.StatusBadRequest, `scope must be "others" or "all"`)
			return
		}
		if keep < 0 {
			keep = 0 // an API key has no session of its own to keep
		}
		u, ok := requireUser(w, r)
		if !ok {
			return
		}
		n, err := auth.DeleteUserSessionsExcept(r.Context(), db, u.ID, keep)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		logging.Info("signed out %d session(s) (%s)", n, body.Scope)
		if keep == 0 {
			clearSessionCookie(w, r, cookieName)
		}
		writeJSON(w, http.StatusOK, map[string]int64{"signedOut": n})
	}
}

// handleChangePassword serves POST /api/auth/password. Wrong current passwords
// count toward the same lockout as failed sign-ins.
func handleChangePassword(db *sql.DB, limiter *auth.Limiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			CurrentPassword string `json:"currentPassword"`
			NewPassword     string `json:"newPassword"`
		}
		if !decodeStrict(w, r, &body) {
			return
		}
		client := clientAddr(r)
		if ok, wait := limiter.Allow(client); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Round(time.Second)/time.Second)))
			writeJSONError(w, http.StatusTooManyRequests, "Too many failed attempts. Try again in "+wait.Round(time.Second).String()+".")
			return
		}
		u, ok := requireUser(w, r)
		if !ok {
			return
		}
		keep := -auth.PrincipalID(r.Context())
		if keep < 0 {
			keep = 0
		}
		err := auth.ChangePassword(r.Context(), db, u.ID, body.CurrentPassword, body.NewPassword, keep)
		switch {
		case errors.Is(err, auth.ErrPasswordLocked):
			writeJSONCode(w, http.StatusForbidden, "password_locked", "Only an owner can change this account's password.")
		case errors.Is(err, auth.ErrInvalidPassword):
			writeJSONError(w, http.StatusBadRequest, credentialMessage(err))
		case errors.Is(err, auth.ErrWrongPassword):
			limiter.Fail(client)
			logging.Warn("password change refused for %s: wrong current password", client)
			writeJSONError(w, http.StatusForbidden, "The current password is wrong.")
		case errors.Is(err, auth.ErrNoAccount), errors.Is(err, auth.ErrUserNotFound):
			writeJSONError(w, http.StatusConflict, "The account no longer exists.")
		case err != nil:
			writeJSONError(w, http.StatusInternalServerError, "could not change the password")
		default:
			limiter.Succeed(client)
			logging.Info("password changed for %s; its other sessions signed out", u.Username)
			w.WriteHeader(http.StatusNoContent)
		}
	}
}

// handleChangeUsername serves POST /api/auth/username: the signed-in account
// renames itself after confirming its password. Wrong passwords count toward
// the sign-in lockout; a password-locked account cannot rename itself.
func handleChangeUsername(db *sql.DB, limiter *auth.Limiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			CurrentPassword string `json:"currentPassword"`
			NewUsername     string `json:"newUsername"`
		}
		if !decodeStrict(w, r, &body) {
			return
		}
		client := clientAddr(r)
		if ok, wait := limiter.Allow(client); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Round(time.Second)/time.Second)))
			writeJSONError(w, http.StatusTooManyRequests, "Too many failed attempts. Try again in "+wait.Round(time.Second).String()+".")
			return
		}
		u, ok := requireUser(w, r)
		if !ok {
			return
		}
		name, err := auth.ChangeUsername(r.Context(), db, u.ID, body.CurrentPassword, body.NewUsername)
		switch {
		case errors.Is(err, auth.ErrPasswordLocked):
			writeJSONCode(w, http.StatusForbidden, "password_locked", "Only an owner can change this account's username.")
		case errors.Is(err, auth.ErrInvalidUsername):
			writeJSONError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), "auth: "))
		case errors.Is(err, auth.ErrUsernameTaken):
			writeJSONCode(w, http.StatusConflict, "username_taken", "That username is already in use.")
		case errors.Is(err, auth.ErrWrongPassword):
			limiter.Fail(client)
			logging.Warn("username change refused for %s: wrong current password", client)
			writeJSONError(w, http.StatusForbidden, "The current password is wrong.")
		case errors.Is(err, auth.ErrUserNotFound):
			writeJSONError(w, http.StatusConflict, "The account no longer exists.")
		case err != nil:
			writeJSONError(w, http.StatusInternalServerError, "could not change the username")
		default:
			limiter.Succeed(client)
			logging.Info("account %d renamed from %q to %q", u.ID, u.Username, name)
			writeJSON(w, http.StatusOK, map[string]string{"username": name})
		}
	}
}

type sessionLifetimeBody struct {
	Days int `json:"days"`
}

// handleGetSessionLifetime serves GET /api/auth/session-lifetime.
func handleGetSessionLifetime(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, sessionLifetimeBody{Days: auth.SessionDays(r.Context(), db)})
	}
}

// handlePutSessionLifetime serves PUT /api/auth/session-lifetime: browsers that
// sign in from now on stay signed in this many days (0 never expires).
func handlePutSessionLifetime(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body sessionLifetimeBody
		if !decodeStrict(w, r, &body) {
			return
		}
		if err := auth.SetSessionDays(r.Context(), db, body.Days); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		logging.Info("browser sign-in lifetime set to %s", sessionDaysText(body.Days))
		writeJSON(w, http.StatusOK, body)
	}
}

func sessionDaysText(days int) string {
	if days == 0 {
		return "never expire"
	}
	return strconv.Itoa(days) + " days"
}

// requireUser returns the signed-in account, answering 401 when the request
// carries none. Every per-account handler fails closed through it.
func requireUser(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	u, ok := auth.UserFrom(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "authentication required")
	}
	return u, ok
}
