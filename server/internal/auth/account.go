// Package auth implements the server's sign-in, browser sessions and API
// keys for the management listener. See docs/PROJECT_BRIEF.md Epic 5.
package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/alreadyonapp/alreadyon/server/internal/serversettings"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxUsernameLength = 64
	MinPasswordLength = 4
	// MaxPasswordBytes bounds the work a single request can ask of argon2.
	MaxPasswordBytes = 256
)

var (
	ErrInvalidUsername = fmt.Errorf("auth: username must be 1-%d characters with no control characters", MaxUsernameLength)
	ErrInvalidPassword = fmt.Errorf("auth: password must be %d-%d characters", MinPasswordLength, MaxPasswordBytes)
	ErrAccountExists   = errors.New("auth: the first account already exists")
	ErrWrongPassword   = errors.New("auth: the current password is wrong")
	ErrNoAccount       = errors.New("auth: the server has no account yet")
)

// dummyHash is verified against when the username is wrong, so a wrong
// username costs the same as a wrong password.
var dummyHash, _ = HashPassword("not the password")

// AccountExists reports whether the first-run setup has created a user.
func AccountExists(ctx context.Context, db *sql.DB) (bool, error) {
	var n int
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM users)`).Scan(&n); err != nil {
		return false, fmt.Errorf("auth: check account: %w", err)
	}
	return n > 0, nil
}

// ValidateUsername returns the cleaned username or ErrInvalidUsername.
func ValidateUsername(username string) (string, error) {
	username = strings.TrimSpace(username)
	if username == "" || utf8.RuneCountInString(username) > MaxUsernameLength || strings.IndexFunc(username, unicode.IsControl) >= 0 {
		return "", ErrInvalidUsername
	}
	return username, nil
}

// ValidateCredentials checks a new username and password and returns the
// cleaned username.
func ValidateCredentials(username, password string) (string, error) {
	username, err := ValidateUsername(username)
	if err != nil {
		return "", err
	}
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	return username, nil
}

// CreateAccount is the first-run setup: it creates the first owner only if
// the server has no users yet. The insert is conditional in one statement, so
// concurrent first-run requests are safe: one succeeds and the rest get
// ErrAccountExists. Later accounts are added by an owner with CreateUser.
func CreateAccount(ctx context.Context, db *sql.DB, username, password string) (User, error) {
	return CreateNamedAccount(ctx, db, username, password, "AlreadyOn")
}

// CreateNamedAccount stores the instance identity and first owner in one transaction.
func CreateNamedAccount(ctx context.Context, db *sql.DB, username, password, serverName string) (User, error) {
	serverName, err := serversettings.ValidateServerName(serverName)
	if err != nil {
		return User{}, err
	}
	username, err = ValidateCredentials(username, password)
	if err != nil {
		return User{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("auth: create account: %w", err)
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO users (username, password_hash, role)
		SELECT ?, ?, 'owner' WHERE NOT EXISTS (SELECT 1 FROM users) RETURNING id`, username, hash).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrAccountExists
	}
	if err != nil {
		return User{}, fmt.Errorf("auth: create account: %w", err)
	}
	if err := seedDefaultProfiles(ctx, tx, id); err != nil {
		return User{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE server_settings SET server_name = ?, updated_at_unix = unixepoch() WHERE id = 1`, serverName); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("auth: create account: %w", err)
	}
	return GetUser(ctx, db, id)
}

// Authenticate checks a username (case-insensitive) and password and returns
// the matching enabled user. A wrong username costs the same argon2 work as a
// wrong password, and a disabled account never authenticates.
func Authenticate(ctx context.Context, db *sql.DB, username, password string) (User, bool, error) {
	var hash string
	var u User
	var role string
	err := db.QueryRowContext(ctx, `SELECT `+userColumns+`, password_hash FROM users WHERE username = ?`, strings.TrimSpace(username)).
		Scan(&u.ID, &u.Username, &role, &u.Disabled, &u.MustChangePassword, &u.PasswordLocked, &u.MaxStreams, &u.CreatedAtUnix, &u.UpdatedAtUnix, &hash)
	u.Role = Role(role)
	if errors.Is(err, sql.ErrNoRows) {
		VerifyPassword(dummyHash, password)
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, fmt.Errorf("auth: authenticate: %w", err)
	}
	if !VerifyPassword(hash, password) || u.Disabled {
		return User{}, false, nil
	}
	return u, true, nil
}

// ValidatePassword checks a new password on its own.
func ValidatePassword(password string) error {
	if utf8.RuneCountInString(password) < MinPasswordLength || len(password) > MaxPasswordBytes {
		return ErrInvalidPassword
	}
	return nil
}

// ChangePassword replaces a user's own password after checking the current
// one, clears any must-change requirement and ends every session of that user
// except keepSession. A password-locked account must ask an owner instead.
func ChangePassword(ctx context.Context, db *sql.DB, userID int64, current, next string, keepSession int64) error {
	if err := ValidatePassword(next); err != nil {
		return err
	}
	var hash string
	var locked bool
	if err := db.QueryRowContext(ctx, `SELECT password_hash, password_locked FROM users WHERE id = ?`, userID).Scan(&hash, &locked); errors.Is(err, sql.ErrNoRows) {
		return ErrUserNotFound
	} else if err != nil {
		return fmt.Errorf("auth: change password: %w", err)
	}
	if locked {
		return ErrPasswordLocked
	}
	if !VerifyPassword(hash, current) {
		return ErrWrongPassword
	}
	return setPassword(ctx, db, userID, "", next, false, keepSession)
}

// ChangeUsername renames the signed-in account after confirming its current
// password. Like ChangePassword it is refused for a password-locked account,
// whose credentials (a shared demo sign-in) only an owner may change. Sessions
// are keyed by account id, so every signed-in browser and app stays signed in.
func ChangeUsername(ctx context.Context, db *sql.DB, userID int64, current, next string) (string, error) {
	name, err := ValidateUsername(next)
	if err != nil {
		return "", err
	}
	var hash string
	var locked bool
	if err := db.QueryRowContext(ctx, `SELECT password_hash, password_locked FROM users WHERE id = ?`, userID).Scan(&hash, &locked); errors.Is(err, sql.ErrNoRows) {
		return "", ErrUserNotFound
	} else if err != nil {
		return "", fmt.Errorf("auth: change username: %w", err)
	}
	if locked {
		return "", ErrPasswordLocked
	}
	if !VerifyPassword(hash, current) {
		return "", ErrWrongPassword
	}
	if _, err := db.ExecContext(ctx, `UPDATE users SET username = ?, updated_at_unix = unixepoch() WHERE id = ?`, name, userID); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return "", ErrUsernameTaken
		}
		return "", fmt.Errorf("auth: change username: %w", err)
	}
	return name, nil
}

// ResetPassword sets a password without knowing the old one, for the local
// reset tool reached only from a shell on the server. An empty username
// resets the first owner; an existing username resets that user; any other
// username renames the first owner (the single-account tool's behavior). The
// user is signed out everywhere and need not change the password again.
func ResetPassword(ctx context.Context, db *sql.DB, username, next string) error {
	if err := ValidatePassword(next); err != nil {
		return err
	}
	if username != "" {
		var err error
		if username, err = ValidateUsername(username); err != nil {
			return err
		}
	}
	var id int64
	err := db.QueryRowContext(ctx, `SELECT id FROM users WHERE username = ?`, username).Scan(&id)
	rename := ""
	if errors.Is(err, sql.ErrNoRows) {
		rename = username
		err = db.QueryRowContext(ctx, `SELECT id FROM users WHERE role = 'owner' ORDER BY id LIMIT 1`).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoAccount
		}
	}
	if err != nil {
		return fmt.Errorf("auth: reset password: %w", err)
	}
	return setPassword(ctx, db, id, rename, next, false, 0)
}

func setPassword(ctx context.Context, db *sql.DB, userID int64, username, next string, mustChange bool, keepSession int64) error {
	hash, err := HashPassword(next)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("auth: set password: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE users SET password_hash = ?, username = COALESCE(NULLIF(?, ''), username), must_change_password = ?, updated_at_unix = unixepoch()
		WHERE id = ?`, hash, username, mustChange, userID); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrUsernameTaken
		}
		return fmt.Errorf("auth: set password: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM browser_sessions WHERE user_id = ? AND id <> ?`, userID, keepSession); err != nil {
		return fmt.Errorf("auth: set password: %w", err)
	}
	return tx.Commit()
}
