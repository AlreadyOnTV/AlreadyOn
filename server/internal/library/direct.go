package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/alreadyonapp/alreadyon/server/internal/metadata"
)

// PreviewTMDb fetches provider metadata for the edit form without changing the
// selected item or its show's episodes.
func PreviewTMDb(ctx context.Context, db *sql.DB, client *metadata.Client, id int64, tmdbID int) (metadata.DirectDetails, error) {
	if id <= 0 || tmdbID <= 0 {
		return metadata.DirectDetails{}, invalid("item and TMDb IDs must be positive")
	}
	var mediaType string
	if err := db.QueryRowContext(ctx, `SELECT media_type FROM identified_items WHERE id=?`, id).Scan(&mediaType); err != nil {
		if err == sql.ErrNoRows {
			return metadata.DirectDetails{}, fmt.Errorf("%w: %d", ErrItemNotFound, id)
		}
		return metadata.DirectDetails{}, err
	}
	if mediaType != "movie" && mediaType != "episode" {
		return metadata.DirectDetails{}, invalid("item is not a movie or show")
	}
	details, err := client.LookupID(ctx, mediaType, tmdbID)
	if err != nil {
		return metadata.DirectDetails{}, invalid("TMDb lookup failed; verify the ID and configured token")
	}
	return details, nil
}

// LookupTMDb replaces the selected movie's identity or the selected show's
// identity for all its episodes. Network work finishes before the transaction.
func LookupTMDb(ctx context.Context, db *sql.DB, client *metadata.Client, id int64, tmdbID int) (int, error) {
	if tmdbID <= 0 {
		return 0, invalid("TMDb ID must be positive")
	}
	var mediaType, title string
	var showID sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT media_type,COALESCE(series_title,title),show_id FROM identified_items WHERE id=?`, id).Scan(&mediaType, &title, &showID); err != nil {
		if err == sql.ErrNoRows {
			return 0, ErrItemNotFound
		}
		return 0, err
	}
	d, err := client.LookupID(ctx, mediaType, tmdbID)
	if err != nil {
		return 0, invalid("TMDb lookup failed; verify the ID and configured token")
	}
	tags, err := json.Marshal(d.Tags)
	if err != nil {
		return 0, err
	}
	if d.Tags == nil {
		tags = []byte("[]")
	}
	year := 0
	if len(d.Date) >= 4 {
		year, _ = strconv.Atoi(d.Date[:4])
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var result sql.Result
	if mediaType == "movie" {
		result, err = tx.ExecContext(ctx, `UPDATE identified_items SET title=?,tmdb_id=?,release_year=NULLIF(?,0),release_date=?,overview=?,genre_tags=CASE WHEN genre_source='manual' THEN genre_tags ELSE ? END,genre_source=CASE WHEN genre_source='manual' THEN genre_source ELSE 'tmdb' END,
 content_rating=CASE WHEN content_rating_manual=1 THEN content_rating ELSE NULLIF(?,'') END,
 is_manual_correction=1,metadata_fetched_at_unix=unixepoch() WHERE id=?`, d.Title, tmdbID, year, d.Date, d.Overview, string(tags), d.Certification, id)
	} else {
		var local int64
		err = tx.QueryRowContext(ctx, `INSERT INTO tv_shows(tmdb_id,title,first_air_date,last_air_date,overview,content_rating,genre_tags,metadata_fetched_at_unix)
 VALUES(?,?,?,?,?,?,?,unixepoch()) ON CONFLICT(tmdb_id) DO UPDATE SET title=excluded.title,first_air_date=excluded.first_air_date,last_air_date=excluded.last_air_date,overview=excluded.overview,content_rating=CASE WHEN tv_shows.content_rating_manual=1 THEN tv_shows.content_rating ELSE excluded.content_rating END,genre_tags=CASE WHEN tv_shows.genre_source='manual' THEN tv_shows.genre_tags ELSE excluded.genre_tags END,metadata_fetched_at_unix=unixepoch() RETURNING id`, tmdbID, d.Title, d.Date, d.LastDate, d.Overview, d.Certification, string(tags)).Scan(&local)
		if err != nil {
			return 0, err
		}
		if showID.Valid && showID.Int64 != local {
			// A provider identity correction must not discard the old parent's
			// protected audience or other manually reviewed metadata.
			_, err = tx.ExecContext(ctx, `UPDATE tv_shows SET
    audience_override=coalesce((SELECT audience_override FROM tv_shows WHERE id=?),audience_override),
    premiere_year_override=coalesce((SELECT premiere_year_override FROM tv_shows WHERE id=?),premiere_year_override),
    genre_tags=coalesce((SELECT genre_tags FROM tv_shows WHERE id=? AND genre_source='manual'),genre_tags),
    genre_source=CASE WHEN EXISTS(SELECT 1 FROM tv_shows WHERE id=? AND genre_source='manual') THEN 'manual' ELSE genre_source END,
    content_rating=coalesce((SELECT content_rating FROM tv_shows WHERE id=? AND content_rating_manual=1),content_rating),
    content_rating_manual=max(content_rating_manual,(SELECT content_rating_manual FROM tv_shows WHERE id=?)),
    local_series_title=coalesce((SELECT local_series_title FROM tv_shows WHERE id=?),local_series_title),
    local_series_year=coalesce((SELECT local_series_year FROM tv_shows WHERE id=?),local_series_year)
    WHERE id=?`, showID, showID, showID, showID, showID, showID, showID, showID, local)
			if err != nil {
				return 0, err
			}
		}
		result, err = tx.ExecContext(ctx, `UPDATE identified_items SET show_id=?,series_title=?,genre_tags=CASE WHEN genre_source='manual' THEN genre_tags ELSE ? END,genre_source=CASE WHEN genre_source='manual' THEN genre_source ELSE 'tmdb' END,
 content_rating=CASE WHEN content_rating_manual=1 THEN content_rating ELSE NULLIF(?,'') END,
 is_manual_correction=1,overview=NULL,episode_air_date=NULL,metadata_fetched_at_unix=NULL
 WHERE media_type='episode' AND ((? IS NOT NULL AND show_id=?) OR (? IS NULL AND show_id IS NULL AND COALESCE(series_title,title)=?))`, local, d.Title, string(tags), d.Certification, showID, showID, showID, title)
	}
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}
