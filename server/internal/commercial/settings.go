// Package commercial holds global commercial-break settings and channel
// enablement/folder overrides. Scheduling applies the effective policy.
// See docs/PROJECT_BRIEF.md Epic 3, issue #24.
package commercial

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/alreadyonapp/alreadyon/server/internal/storage"
	"math"
)

// Mode selects the break-insertion strategy internal/schedule's expand
// uses.
type Mode string

const (
	// ModeBetweenOnly inserts a break only between one whole program (an
	// episode or movie) and the next — the default. Nothing is ever
	// interrupted mid-playback.
	ModeBetweenOnly Mode = "between-only"
	// ModeInterval additionally inserts breaks partway through a single
	// program, roughly every IntervalMinutes of its own content, never
	// within the first/last few minutes of it — see
	// internal/schedule.TVBufferSeconds/MovieBufferSeconds.
	ModeInterval Mode = "interval"
)

func (m Mode) valid() bool {
	switch m {
	case ModeBetweenOnly, ModeInterval:
		return true
	default:
		return false
	}
}

func validIntervalMinutes(n int) bool {
	switch n {
	case 15, 30, 60:
		return true
	default:
		return false
	}
}

func validCommercialsPerBreak(n int) bool {
	return n >= 1 && n <= 5
}

// Settings is the single, server-wide commercial-break configuration.
type Settings struct {
	VolumeOffsetDB float64
	Enabled        bool
	Mode           Mode
	// IntervalMinutes is only meaningful when Mode == ModeInterval: 15,
	// 30, or 60.
	IntervalMinutes int
	// TVCommercialsPerBreak/MovieCommercialsPerBreak (1-5 each) size
	// every break this settings value produces, in both modes — a movie
	// conventionally gets a longer break than a TV episode, so it has
	// its own count.
	TVCommercialsPerBreak    int
	MovieCommercialsPerBreak int
}

// CommercialsPerBreak returns the configured break size for a program of
// the given media type ("movie" gets its own count; anything else — in
// practice, "episode" — is treated as a TV show).
func (s Settings) CommercialsPerBreak(mediaType string) int {
	if mediaType == "movie" {
		return s.MovieCommercialsPerBreak
	}
	return s.TVCommercialsPerBreak
}

var (
	ErrInvalidVolumeOffset             = errors.New("commercial: volume offset must be between -30 and 12 dB")
	ErrInvalidMode                     = errors.New("commercial: invalid mode")
	ErrInvalidIntervalMinutes          = errors.New("commercial: interval minutes must be 15, 30, or 60")
	ErrInvalidTVCommercialsPerBreak    = errors.New("commercial: tv commercials per break must be between 1 and 5")
	ErrInvalidMovieCommercialsPerBreak = errors.New("commercial: movie commercials per break must be between 1 and 5")
)

// GetSettings returns the current settings. There's always exactly one
// row (seeded by migration 0007, reshaped by 0015), so a missing row is
// a schema bug, not a valid "unconfigured" state.
func GetSettings(ctx context.Context, db storage.Querier) (Settings, error) {
	var s Settings
	var mode string
	var enabled int
	err := db.QueryRowContext(ctx, `
		SELECT enabled, mode, interval_minutes, tv_commercials_per_break, movie_commercials_per_break, volume_offset_db
		FROM commercial_settings WHERE id = 1`).
		Scan(&enabled, &mode, &s.IntervalMinutes, &s.TVCommercialsPerBreak, &s.MovieCommercialsPerBreak, &s.VolumeOffsetDB)
	if err != nil {
		return Settings{}, fmt.Errorf("commercial: get settings: %w", err)
	}
	s.Enabled = enabled != 0
	s.Mode = Mode(mode)
	return s, nil
}

// SaveSettings validates and persists s, replacing the single settings
// row.
func SaveSettings(ctx context.Context, db *sql.DB, s Settings) error {
	if math.IsNaN(s.VolumeOffsetDB) || math.IsInf(s.VolumeOffsetDB, 0) || s.VolumeOffsetDB < -30 || s.VolumeOffsetDB > 12 {
		return ErrInvalidVolumeOffset
	}
	if !s.Mode.valid() {
		return ErrInvalidMode
	}
	if !validIntervalMinutes(s.IntervalMinutes) {
		return ErrInvalidIntervalMinutes
	}
	if !validCommercialsPerBreak(s.TVCommercialsPerBreak) {
		return ErrInvalidTVCommercialsPerBreak
	}
	if !validCommercialsPerBreak(s.MovieCommercialsPerBreak) {
		return ErrInvalidMovieCommercialsPerBreak
	}
	_, err := db.ExecContext(ctx, `
		UPDATE commercial_settings
		SET enabled = ?, mode = ?, interval_minutes = ?, tv_commercials_per_break = ?, movie_commercials_per_break = ?, volume_offset_db = ?, updated_at_unix = unixepoch()
		WHERE id = 1`,
		s.Enabled, string(s.Mode), s.IntervalMinutes, s.TVCommercialsPerBreak, s.MovieCommercialsPerBreak, s.VolumeOffsetDB)
	if err != nil {
		return fmt.Errorf("commercial: save settings: %w", err)
	}
	return nil
}
