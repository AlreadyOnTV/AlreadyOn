package metadata

import (
	"context"
	"database/sql"
	"fmt"
)

// Settings holds the admin-configured metadata provider options, set on
// the Server Settings page.
type Settings struct {
	// TMDbAPIKey is the TMDb v4 read access token; empty means not set.
	TMDbAPIKey string
	// KeylessEnabled is retained for compatibility; free providers are always enabled.
	KeylessEnabled bool
}

// GetSettings returns the current settings. There's always exactly one
// row (seeded by migration 0011), so a missing row is a schema bug, not
// a valid "unconfigured" state.
func GetSettings(ctx context.Context, db *sql.DB) (Settings, error) {
	var s Settings
	var tmdbKey sql.NullString
	err := db.QueryRowContext(ctx, `
		SELECT tmdb_api_key, keyless_enabled FROM metadata_settings WHERE id = 1`).
		Scan(&tmdbKey, &s.KeylessEnabled)
	if err != nil {
		return Settings{}, fmt.Errorf("metadata: get settings: %w", err)
	}
	s.TMDbAPIKey = tmdbKey.String
	s.KeylessEnabled = true
	return s, nil
}

// SaveSettings persists s. An empty key is stored as SQL NULL (not ""),
// matching GetSettings' "empty means unset" convention.
func SaveSettings(ctx context.Context, db *sql.DB, s Settings) error {
	_, err := db.ExecContext(ctx, `
		UPDATE metadata_settings SET tmdb_api_key = NULLIF(?, ''), keyless_enabled = ?,
			updated_at_unix = unixepoch() WHERE id = 1`,
		s.TMDbAPIKey, true)
	if err != nil {
		return fmt.Errorf("metadata: save settings: %w", err)
	}
	return nil
}
