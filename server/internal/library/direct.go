package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"

	"github.com/alreadyonapp/alreadyon/server/internal/metadata"
)

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
		result, err = tx.ExecContext(ctx, `UPDATE identified_items SET title=?,tmdb_id=?,release_year=NULLIF(?,0),release_date=?,overview=?,genre_tags=?,genre_source='manual',
 content_rating=CASE WHEN content_rating_manual=1 THEN content_rating ELSE NULLIF(?,'') END,
 is_manual_correction=1,metadata_fetched_at_unix=unixepoch() WHERE id=?`, d.Title, tmdbID, year, d.Date, d.Overview, string(tags), d.Certification, id)
	} else {
		var local int64
		err = tx.QueryRowContext(ctx, `INSERT INTO tv_shows(tmdb_id,title,first_air_date,last_air_date,overview,content_rating,genre_tags,metadata_fetched_at_unix)
 VALUES(?,?,?,?,?,?,?,unixepoch()) ON CONFLICT(tmdb_id) DO UPDATE SET title=excluded.title,first_air_date=excluded.first_air_date,last_air_date=excluded.last_air_date,overview=excluded.overview,content_rating=excluded.content_rating,genre_tags=excluded.genre_tags,metadata_fetched_at_unix=unixepoch() RETURNING id`, tmdbID, d.Title, d.Date, d.LastDate, d.Overview, d.Certification, string(tags)).Scan(&local)
		if err != nil {
			return 0, err
		}
		result, err = tx.ExecContext(ctx, `UPDATE identified_items SET show_id=?,series_title=?,genre_tags=?,genre_source='manual',
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
