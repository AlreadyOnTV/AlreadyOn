package library

import (
	"context"
	"database/sql"
	"fmt"
)

// SaveIdentification persists an automatic Identification onto the
// identified_items row for mediaFileID, creating it if this is the
// first identification for that file. If that row has been manually
// corrected (#17), this is a no-op — a rescan's automatic guess never
// overwrites an administrator's correction.
func SaveIdentification(ctx context.Context, db *sql.DB, mediaFileID int64, id Identification) error {
	mediaType, title, seriesTitle, season, episode, year := identificationSQLArgs(id)

	// Provider IDs/dates belong to the old media type. Invalidate them on a
	// type correction, retaining independently protected genres/rating/audience.
	result, err := db.ExecContext(ctx, `
		INSERT INTO identified_items (media_file_id, media_type, title, series_title, season_number, episode_number, release_year)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(media_file_id) DO UPDATE SET
			tmdb_id = CASE WHEN identified_items.media_type != excluded.media_type THEN NULL ELSE identified_items.tmdb_id END,
			show_id = CASE WHEN identified_items.media_type != excluded.media_type THEN NULL ELSE identified_items.show_id END,
			overview = CASE WHEN identified_items.media_type != excluded.media_type THEN NULL ELSE identified_items.overview END,
			release_date = CASE WHEN identified_items.media_type != excluded.media_type THEN NULL ELSE identified_items.release_date END,
			episode_air_date = CASE WHEN identified_items.media_type != excluded.media_type THEN NULL ELSE identified_items.episode_air_date END,
			metadata_fetched_at_unix = CASE WHEN identified_items.media_type != excluded.media_type THEN NULL ELSE identified_items.metadata_fetched_at_unix END,
			keyless_release_date = CASE WHEN identified_items.media_type != excluded.media_type THEN NULL ELSE identified_items.keyless_release_date END,
			keyless_provider = CASE WHEN identified_items.media_type != excluded.media_type THEN NULL ELSE identified_items.keyless_provider END,
			keyless_fetched_at_unix = CASE WHEN identified_items.media_type != excluded.media_type THEN NULL ELSE identified_items.keyless_fetched_at_unix END,
			genre_tags = CASE WHEN identified_items.media_type != excluded.media_type AND coalesce(identified_items.genre_source,'') != 'manual' THEN NULL ELSE identified_items.genre_tags END,
			genre_source = CASE WHEN identified_items.media_type != excluded.media_type AND coalesce(identified_items.genre_source,'') != 'manual' THEN NULL ELSE identified_items.genre_source END,
			content_rating = CASE WHEN identified_items.media_type != excluded.media_type AND identified_items.content_rating_manual = 0 THEN NULL ELSE identified_items.content_rating END,
			media_type = excluded.media_type,
			title = excluded.title,
			series_title = excluded.series_title,
			season_number = excluded.season_number,
			episode_number = excluded.episode_number,
			release_year = excluded.release_year,
			updated_at_unix = unixepoch()
		WHERE identified_items.is_manual_correction = 0`,
		mediaFileID, mediaType, title, seriesTitle, season, episode, year)
	if err != nil {
		return fmt.Errorf("library: save identification for media file %d: %w", mediaFileID, err)
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return nil // A protected identity must not acquire an automatic parent either.
	}
	if id.IsEpisode {
		// Attach only when the local identity names exactly one managed series.
		_, err = db.ExecContext(ctx, `UPDATE identified_items SET show_id=(SELECT CASE WHEN count(*)=1 THEN min(id) END FROM tv_shows WHERE local_series_title=? AND local_series_year IS ?) WHERE media_file_id=? AND show_id IS NULL`, seriesTitle, year, mediaFileID)
		if err != nil {
			return err
		}
	}

	return nil
}

// SetCorrection records an administrator's manual identification for
// mediaFileID and marks it as authoritative: SaveIdentification (called
// by every future rescan) will no longer overwrite it.
func SetCorrection(ctx context.Context, db *sql.DB, mediaFileID int64, id Identification) error {
	mediaType, title, seriesTitle, season, episode, year := identificationSQLArgs(id)

	_, err := db.ExecContext(ctx, `
		INSERT INTO identified_items (media_file_id, media_type, title, series_title, season_number, episode_number, release_year, is_manual_correction)
		VALUES (?, ?, ?, ?, ?, ?, ?, 1)
		ON CONFLICT(media_file_id) DO UPDATE SET
			media_type = excluded.media_type,
			title = excluded.title,
			series_title = excluded.series_title,
			season_number = excluded.season_number,
			episode_number = excluded.episode_number,
			release_year = excluded.release_year,
			is_manual_correction = 1,
			updated_at_unix = unixepoch()`,
		mediaFileID, mediaType, title, seriesTitle, season, episode, year)
	if err != nil {
		return fmt.Errorf("library: set correction for media file %d: %w", mediaFileID, err)
	}
	return nil
}

// ClearCorrection removes the manual-correction flag from mediaFileID's
// identification, if any. The row's current (corrected) values are left
// in place; the next rescan's automatic result will overwrite them
// normally, same as any other file.
func ClearCorrection(ctx context.Context, db *sql.DB, mediaFileID int64) error {
	res, err := db.ExecContext(ctx, `
		UPDATE identified_items SET is_manual_correction = 0, updated_at_unix = unixepoch()
		WHERE media_file_id = ?`, mediaFileID)
	if err != nil {
		return fmt.Errorf("library: clear correction for media file %d: %w", mediaFileID, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("library: clear correction for media file %d: %w", mediaFileID, err)
	}
	if rows == 0 {
		return fmt.Errorf("library: clear correction: no identified_items row for media file %d", mediaFileID)
	}
	return nil
}

// identificationSQLArgs maps an Identification onto identified_items'
// columns, leaving the fields that don't apply (season/episode for a
// movie, release_year for an episode) as SQL NULL rather than a
// misleading zero value.
func identificationSQLArgs(id Identification) (mediaType, title string, seriesTitle sql.NullString, season, episode, year sql.NullInt64) {
	mediaType = "movie"
	if id.IsEpisode {
		mediaType = "episode"
		seriesTitle = sql.NullString{String: id.Show, Valid: id.Show != ""}
		season = sql.NullInt64{Int64: int64(id.Season), Valid: true}
		episode = sql.NullInt64{Int64: int64(id.Episode), Valid: true}
	} else if id.Year > 0 {
		year = sql.NullInt64{Int64: int64(id.Year), Valid: true}
	}
	return mediaType, id.Title, seriesTitle, season, episode, year
}
