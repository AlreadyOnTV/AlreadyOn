package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"time"
)

// DefaultSessionDays is how long a browser stays signed in after it signs in
// until the administrator chooses otherwise; 0 days means it never expires.
const DefaultSessionDays = 30

// MaxSessionDays bounds the setting (ten years).
const MaxSessionDays = 3650

// NeverExpiresUnix stands in for "no expiry" in a session's expiry column
// (the last second of 9999).
const NeverExpiresUnix = 253402300799

var ErrInvalidSessionDays = errors.New("auth: days must be 0 (never expires) to 3650")

// SessionDays is the configured lifetime in days; 0 never expires.
func SessionDays(ctx context.Context, db *sql.DB) int {
	days := DefaultSessionDays
	if err := db.QueryRowContext(ctx, `SELECT session_days FROM server_settings WHERE id = 1`).Scan(&days); err != nil {
		return DefaultSessionDays
	}
	return days
}

// SetSessionDays saves the lifetime used by future sign-ins.
func SetSessionDays(ctx context.Context, db *sql.DB, days int) error {
	if days < 0 || days > MaxSessionDays {
		return ErrInvalidSessionDays
	}
	_, err := db.ExecContext(ctx, `UPDATE server_settings SET session_days = ? WHERE id = 1`, days)
	return err
}

var ErrNoSession = errors.New("auth: no valid session")

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CreateSession starts a browser session for a user and returns its raw
// token, which is never stored, and the row id.
func CreateSession(ctx context.Context, db *sql.DB, now time.Time, userID int64, userAgent string) (token string, id int64, expires time.Time, err error) {
	return createSession(ctx, db, now, userID, userAgent, SessionDays(ctx, db))
}

// CreateNativeSession has no absolute expiry; revocation and inactivity still apply.
// Browser lifetime settings do not silently sign a living-room device out.
func CreateNativeSession(ctx context.Context, db *sql.DB, now time.Time, userID int64, userAgent string) (string, int64, time.Time, error) {
	return createSession(ctx, db, now, userID, userAgent, 0)
}

func createSession(ctx context.Context, db *sql.DB, now time.Time, userID int64, userAgent string, days int) (token string, id int64, expires time.Time, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", 0, time.Time{}, fmt.Errorf("auth: create session: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	if days == 0 {
		expires = time.Unix(NeverExpiresUnix, 0)
	} else {
		expires = now.Add(time.Duration(days) * 24 * time.Hour)
	}
	if len(userAgent) > 200 {
		userAgent = userAgent[:200]
	}
	// Expired rows are dropped as new ones are added, so the table stays small.
	if err = PruneSessions(ctx, db, now); err != nil {
		return "", 0, time.Time{}, fmt.Errorf("auth: create session: %w", err)
	}
	err = db.QueryRowContext(ctx, `
		INSERT INTO browser_sessions (user_id, token_hash, created_at_unix, expires_at_unix, user_agent, last_active_at_unix) VALUES (?, ?, ?, ?, ?, ?) RETURNING id`,
		userID, hashToken(token), now.Unix(), expires.Unix(), userAgent, now.Unix()).Scan(&id)
	if err != nil {
		return "", 0, time.Time{}, fmt.Errorf("auth: create session: %w", err)
	}
	return token, id, expires, nil
}

// LookupSession returns the live session for token and its enabled user.
func LookupSession(ctx context.Context, db *sql.DB, token string, now time.Time) (int64, User, error) {
	if token == "" || len(token) > 128 {
		return 0, User{}, ErrNoSession
	}
	var id int64
	var active sql.NullInt64
	var u User
	var role string
	err := db.QueryRowContext(ctx, `
		SELECT s.id, s.last_active_at_unix, u.id, u.username, u.role, u.disabled, u.must_change_password, u.password_locked, u.max_streams, u.created_at_unix, u.updated_at_unix
		FROM browser_sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at_unix > ? AND u.disabled = 0`+activeSessionSQL, hashToken(token), now.Unix(), now.Unix()).
		Scan(&id, &active, &u.ID, &u.Username, &role, &u.Disabled, &u.MustChangePassword, &u.PasswordLocked, &u.MaxStreams, &u.CreatedAtUnix, &u.UpdatedAtUnix)
	u.Role = Role(role)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, User{}, ErrNoSession
	}
	if err != nil {
		return 0, User{}, fmt.Errorf("auth: lookup session: %w", err)
	}
	recordSessionActivity(ctx, db, id, now, active)
	return id, u, nil
}

// DeleteSession ends one of a user's sessions; ending an unknown one, or
// another user's, is not an error and changes nothing.
func DeleteSession(ctx context.Context, db *sql.DB, userID, id int64) error {
	if _, err := db.ExecContext(ctx, `DELETE FROM browser_sessions WHERE id = ? AND user_id = ?`, id, userID); err != nil {
		return fmt.Errorf("auth: delete session: %w", err)
	}
	return nil
}

// A principal is whoever a management request is made as: a positive id is an
// API key, a negative id is a browser session. Playback authorizations record
// their issuing principal, and media requests stop working the moment it does.
func sessionPrincipal(sessionID int64) int64 { return -sessionID }

// PrincipalActive reports whether the API key or browser session behind a
// principal still exists, belongs to an enabled user and, for a session, has
// not expired.
func PrincipalActive(ctx context.Context, db *sql.DB, principal int64, now time.Time) bool {
	var one int
	switch {
	case principal > 0:
		return db.QueryRowContext(ctx, `SELECT 1 FROM api_keys k JOIN users u ON u.id = k.user_id WHERE k.id = ? AND u.disabled = 0`, principal).Scan(&one) == nil
	case principal < 0:
		var active sql.NullInt64
		if db.QueryRowContext(ctx, `
			SELECT s.last_active_at_unix FROM browser_sessions s JOIN users u ON u.id = s.user_id
			WHERE s.id = ? AND s.expires_at_unix > ? AND u.disabled = 0`+activeSessionSQL, -principal, now.Unix(), now.Unix()).Scan(&active) != nil {
			return false
		}
		recordSessionActivity(ctx, db, -principal, now, active)
		return true
	}
	return false
}

// Persist at most once every 30 seconds per session, including media activity.
// Telemetry failures must never prevent an otherwise valid sign-in/playback.
func recordSessionActivity(ctx context.Context, db *sql.DB, id int64, now time.Time, previous sql.NullInt64) {
	if previous.Valid && now.Unix()-previous.Int64 < 30 {
		return
	}
	_, _ = db.ExecContext(ctx, `UPDATE browser_sessions SET last_active_at_unix=? WHERE id=? AND (last_active_at_unix IS NULL OR last_active_at_unix<=?)`, now.Unix(), id, now.Unix()-30)
}

// SessionInfo describes one signed-in browser for the sessions list.
type SessionInfo struct {
	IPAddress        string
	LastActiveAtUnix sql.NullInt64
	ID               int64
	UserAgent        string
	CreatedAtUnix    int64
	ExpiresAtUnix    int64
}

// ListSessions returns a user's live browser sessions, newest first.
func ListSessions(ctx context.Context, db *sql.DB, userID int64, now time.Time) ([]SessionInfo, error) {
	if err := PruneSessions(ctx, db, now); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, user_agent, created_at_unix, expires_at_unix, last_active_at_unix, ip_address FROM browser_sessions
		WHERE user_id = ? AND expires_at_unix > ? ORDER BY created_at_unix DESC, id DESC`, userID, now.Unix())
	if err != nil {
		return nil, fmt.Errorf("auth: list sessions: %w", err)
	}
	defer rows.Close()
	var out []SessionInfo
	for rows.Next() {
		var s SessionInfo
		if err := rows.Scan(&s.ID, &s.UserAgent, &s.CreatedAtUnix, &s.ExpiresAtUnix, &s.LastActiveAtUnix, &s.IPAddress); err != nil {
			return nil, fmt.Errorf("auth: list sessions: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// RecordSessionIP stores only a valid socket peer address. It deliberately does
// not accept forwarding headers; a proxy deployment reports the observed proxy.
func RecordSessionIP(ctx context.Context, db *sql.DB, id int64, remote string) error {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil
	}
	// Media requests authenticate frequently. Avoid taking a SQLite write lock
	// when a session is still connecting from its previously observed address.
	var previous string
	if err := db.QueryRowContext(ctx, `SELECT ip_address FROM browser_sessions WHERE id=?`, id).Scan(&previous); err != nil {
		return err
	}
	if previous == ip.String() {
		return nil
	}
	_, err = db.ExecContext(ctx, `UPDATE browser_sessions SET ip_address=? WHERE id=? AND ip_address<>?`, ip.String(), id, ip.String())
	return err
}

// DeleteUserSessionsExcept ends every session of one user but keep (0 keeps
// none) and returns how many it ended.
func DeleteUserSessionsExcept(ctx context.Context, db *sql.DB, userID, keep int64) (int64, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM browser_sessions WHERE user_id = ? AND id <> ?`, userID, keep)
	if err != nil {
		return 0, fmt.Errorf("auth: delete sessions: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// DeleteSessionsExcept ends every user's browser session but keep (0 keeps none) and
// returns how many it ended.
func DeleteSessionsExcept(ctx context.Context, db *sql.DB, keep int64) (int64, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM browser_sessions WHERE id <> ?`, keep)
	if err != nil {
		return 0, fmt.Errorf("auth: delete sessions: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
