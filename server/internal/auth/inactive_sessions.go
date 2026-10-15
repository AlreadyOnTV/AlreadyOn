package auth

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Inactivity applies to browser and native sessions even when absolute expiry is disabled.
// Missing historical activity uses creation time, never the time of a cleanup attempt.
const activeSessionSQL = ` AND ((SELECT remove_inactive_sessions FROM server_settings WHERE id=1)=0 OR COALESCE(s.last_active_at_unix,s.created_at_unix)>=?-(SELECT inactive_session_days FROM server_settings WHERE id=1)*86400)`

type InactiveSessionPolicy struct {
	Enabled bool `json:"enabled"`
	Days    int  `json:"days"`
}

func GetInactiveSessionPolicy(ctx context.Context, db *sql.DB) (InactiveSessionPolicy, error) {
	var policy InactiveSessionPolicy
	err := db.QueryRowContext(ctx, `SELECT remove_inactive_sessions,inactive_session_days FROM server_settings WHERE id=1`).Scan(&policy.Enabled, &policy.Days)
	return policy, err
}

func SetInactiveSessionPolicy(ctx context.Context, db *sql.DB, policy InactiveSessionPolicy) error {
	if policy.Days < 1 || policy.Days > MaxSessionDays {
		return errors.New("days must be between 1 and 3650")
	}
	_, err := db.ExecContext(ctx, `UPDATE server_settings SET remove_inactive_sessions=?,inactive_session_days=? WHERE id=1`, policy.Enabled, policy.Days)
	return err
}

// PruneSessions removes invalid sessions periodically; request-time checks prevent an
// idle cookie or scoped media principal being revived between cleanup passes.
func PruneSessions(ctx context.Context, db *sql.DB, now time.Time) error {
	_, err := db.ExecContext(ctx, `DELETE FROM browser_sessions WHERE expires_at_unix<=? OR ((SELECT remove_inactive_sessions FROM server_settings WHERE id=1)=1 AND COALESCE(last_active_at_unix,created_at_unix)<?-(SELECT inactive_session_days FROM server_settings WHERE id=1)*86400)`, now.Unix(), now.Unix())
	return err
}
