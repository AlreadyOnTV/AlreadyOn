package playback

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Settings struct {
	// MaxProcesses is the administrator's resource-slot limit; a two-rendition
	// ladder consumes two slots. It is used only when AutoProcesses is off.
	MaxProcesses  int  `json:"maxProcesses"`
	AutoProcesses bool `json:"autoProcesses"`
	MaxRenditions int  `json:"maxRenditions"`
	MaxViewers    int  `json:"maxViewers"`
	LeaseSeconds  int  `json:"leaseSeconds"`
	// Ladder adds a small 360p rendition to large conversions so a stream can
	// start on it. It costs a lot of CPU (see docs/PLAYBACK.md), so it is off
	// unless the administrator turns it on.
	Ladder bool `json:"ladder"`
}

// MinLeaseSeconds leaves room for the browser's ten-second heartbeat plus
// request time, and for a prepared entry waiting up to eight seconds to attach.
const MinLeaseSeconds = 20

func DefaultSettings() Settings {
	return Settings{MaxProcesses: 2, AutoProcesses: true, MaxRenditions: 8, MaxViewers: 256, LeaseSeconds: 30}
}
func (s Settings) Lease() time.Duration { return time.Duration(s.LeaseSeconds) * time.Second }
func (s Settings) Validate() error {
	if s.MaxProcesses < 1 || s.MaxProcesses > MaxParallel || s.MaxRenditions < 1 || s.MaxRenditions > 32 || s.MaxViewers < 1 || s.MaxViewers > 1024 || s.LeaseSeconds < MinLeaseSeconds || s.LeaseSeconds > 120 {
		return errors.New("playback: invalid resource limits")
	}
	return nil
}
func GetSettings(ctx context.Context, db *sql.DB) (Settings, error) {
	var s Settings
	err := db.QueryRowContext(ctx, `SELECT max_processes,auto_processes,max_renditions,max_viewers,lease_seconds,ladder FROM playback_settings WHERE id=1`).Scan(&s.MaxProcesses, &s.AutoProcesses, &s.MaxRenditions, &s.MaxViewers, &s.LeaseSeconds, &s.Ladder)
	if s.MaxProcesses > MaxParallel {
		s.MaxProcesses = MaxParallel
	}
	return s, err
}
func SaveSettings(ctx context.Context, db *sql.DB, s Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `UPDATE playback_settings SET max_processes=?,auto_processes=?,max_renditions=?,max_viewers=?,lease_seconds=?,ladder=? WHERE id=1`, s.MaxProcesses, s.AutoProcesses, s.MaxRenditions, s.MaxViewers, s.LeaseSeconds, s.Ladder)
	return err
}
