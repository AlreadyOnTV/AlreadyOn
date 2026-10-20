package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/alreadyonapp/alreadyon/server/internal/activity"
	"github.com/alreadyonapp/alreadyon/server/internal/governor"
	"github.com/alreadyonapp/alreadyon/server/internal/metadata"
)

// ClassifyGenres looks up genre tags (via client) for every
// identified_items row that doesn't have them yet, and persists the
// result. A row with no TMDb match gets a persisted empty array (not
// left NULL), so it isn't retried on every future call — see migration
// 0008's doc comment. If client has no API key configured, this is a
// no-op: rows are left NULL so they're picked up once a key is added,
// rather than being permanently marked "classified" with nothing.
//
// Rows previously classified by the keyless fallback (ClassifyGenresKeyless)
// are re-classified here too, so TMDb's results replace them.
func ClassifyGenres(ctx context.Context, db *sql.DB, client *metadata.Client) error {
	if client == nil || client.APIKey == "" {
		return nil
	}
	return classifyPending(ctx, db, client, genreSourceTMDb,
		`genre_tags IS NULL OR genre_source = '`+genreSourceKeyless+`'`)
}

const (
	genreSourceTMDb    = "tmdb"
	genreSourceKeyless = "keyless"
)

// keylessRunning keeps a slow keyless pass (startup, content polling, and
// the Refresh Schedule button can all trigger one) from running twice at
// once and duplicating every provider lookup.
var keylessRunning sync.Mutex

// ClassifyGenresKeyless classifies not-yet-classified rows using
// providers that need no API key (metadata.KeylessClient), for installs
// without a TMDb key. Returns immediately if another keyless pass is
// already running.
func ClassifyGenresKeyless(ctx context.Context, db *sql.DB, client *metadata.KeylessClient) error {
	if client == nil || !keylessRunning.TryLock() {
		return nil
	}
	defer keylessRunning.Unlock()
	return classifyPending(ctx, db, client, genreSourceKeyless, `keyless_fetched_at_unix IS NULL`)
}

type genreClassifier interface {
	ClassifyMovie(ctx context.Context, title string, year int) ([]metadata.Tag, error)
	ClassifyEpisode(ctx context.Context, seriesTitle string, year int) ([]metadata.Tag, error)
}

func classifyPending(ctx context.Context, db *sql.DB, client genreClassifier, source, where string) error {
	type pending struct {
		id          int64
		mediaType   string
		title       string
		seriesTitle sql.NullString
		releaseYear sql.NullInt64
		path        string
	}

	rows, err := db.QueryContext(ctx, `
		SELECT ii.id, ii.media_type, ii.title, ii.series_title, ii.release_year, mf.path
		FROM identified_items ii
		JOIN media_files mf ON mf.id = ii.media_file_id
		WHERE `+where)
	if err != nil {
		return fmt.Errorf("library: classify genres: %w", err)
	}
	var items []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.mediaType, &p.title, &p.seriesTitle, &p.releaseYear, &p.path); err != nil {
			rows.Close()
			return fmt.Errorf("library: classify genres: %w", err)
		}
		items = append(items, p)
	}
	closeErr := rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("library: classify genres: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("library: classify genres: %w", closeErr)
	}

	type lookupKey struct {
		mediaType, title string
		year             int64
	}
	cache := make(map[lookupKey][]metadata.Tag)
	details := make(map[lookupKey]metadata.KeylessResult)

	task, finish := activity.Default.Start("Metadata Classification", "items", len(items))
	defer finish()
	for i, p := range items {
		if err := governor.Checkpoint(ctx); err != nil {
			return err
		}
		activity.Default.Progress(task, i, len(items))
		if err := ctx.Err(); err != nil {
			return err
		}
		title := p.title
		if p.mediaType != "movie" && p.seriesTitle.String != "" {
			title = p.seriesTitle.String
		}
		title = lookupName(title, p.path)
		key := lookupKey{p.mediaType, strings.ToLower(title), p.releaseYear.Int64}
		tags, cached := cache[key]
		if !cached {
			var classifyErr error
			if keyless, ok := client.(*metadata.KeylessClient); ok {
				var result metadata.KeylessResult
				if p.mediaType == "movie" {
					result, classifyErr = keyless.LookupMovie(ctx, title, int(p.releaseYear.Int64))
				} else {
					result, classifyErr = keyless.LookupEpisode(ctx, title, int(p.releaseYear.Int64))
				}
				tags = result.Tags
				details[key] = result
			} else if p.mediaType == "movie" {
				tags, classifyErr = client.ClassifyMovie(ctx, title, int(p.releaseYear.Int64))
			} else {
				tags, classifyErr = client.ClassifyEpisode(ctx, title, int(p.releaseYear.Int64))
			}
			if classifyErr != nil {
				// A single lookup failing (network hiccup, rate limit) shouldn't
				// abandon classifying everything else — leave genre_tags NULL
				// so this row is simply retried on the next call.
				continue
			}
			cache[key] = tags
		}
		if tags == nil {
			tags = []metadata.Tag{} // marshal as "[]", never JSON null, so IS NULL keeps meaning "not attempted"
		}
		encoded, err := json.Marshal(tags)
		if err != nil {
			continue
		}
		if _, err := db.ExecContext(ctx, `UPDATE identified_items SET genre_tags = ?, genre_source = ?
 WHERE id = ? AND coalesce(genre_source,'')!='manual'
 AND (? != 'keyless' OR genre_tags IS NULL OR genre_tags='[]' OR genre_source='keyless')
 AND (? != 'tmdb' OR ? != '[]' OR genre_tags IS NULL OR genre_tags='[]')`, string(encoded), source, p.id, source, source, string(encoded)); err != nil {
			return fmt.Errorf("library: classify genres: save row %d: %w", p.id, err)
		}
		if result, ok := details[key]; ok {
			// Keep free dates separate so TMDb upgrades/manual corrections win.
			if _, err := db.ExecContext(ctx, `UPDATE identified_items SET keyless_release_date = NULLIF(?, ''), keyless_provider = NULLIF(?, ''), keyless_fetched_at_unix = unixepoch() WHERE id = ?`, result.ReleaseDate, result.Provider, p.id); err != nil {
				return fmt.Errorf("library: save free metadata: %w", err)
			}
		}
	}
	return nil
}

// showRef identifies a resolved (or deliberately unresolved) tv_shows
// row for one series, within one EnrichMetadata call — see
// resolveShow. localID.Valid is false when TMDb had no match for this
// series (or the lookup itself failed); tmdbID is only meaningful when
// localID is valid.
type showRef struct {
	localID sql.NullInt64
	tmdbID  int
}

// EnrichMetadata fetches and persists overview/date/certification
// details (via client) for every identified_items row that doesn't
// have them yet: a movie gets its own TMDb movie details directly; an
// episode shares one tv_shows row per series — resolved (or created)
// once per series per call via resolveShow, never re-searched per
// episode — plus that specific episode's own EpisodeDetails.
// metadata_fetched_at_unix is stamped even on a genuine "no TMDb match"
// outcome, the same NULL-vs-attempted convention genre_tags (see
// ClassifyGenres) already uses, so a row isn't retried forever. A
// disabled client (no API key) is a no-op, same as ClassifyGenres.
func EnrichMetadata(ctx context.Context, db *sql.DB, client *metadata.Client) error {
	if client == nil || client.APIKey == "" {
		return nil
	}

	type pending struct {
		id            int64
		mediaType     string
		title         string
		seriesTitle   sql.NullString
		releaseYear   sql.NullInt64
		seasonNumber  sql.NullInt64
		episodeNumber sql.NullInt64
		path          string
	}

	rows, err := db.QueryContext(ctx, `
		SELECT ii.id, ii.media_type, ii.title, ii.series_title, ii.release_year, ii.season_number, ii.episode_number, mf.path
		FROM identified_items ii
		JOIN media_files mf ON mf.id = ii.media_file_id
		WHERE ii.metadata_fetched_at_unix IS NULL`)
	if err != nil {
		return fmt.Errorf("library: enrich metadata: %w", err)
	}
	var items []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.mediaType, &p.title, &p.seriesTitle, &p.releaseYear, &p.seasonNumber, &p.episodeNumber, &p.path); err != nil {
			rows.Close()
			return fmt.Errorf("library: enrich metadata: %w", err)
		}
		items = append(items, p)
	}
	closeErr := rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("library: enrich metadata: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("library: enrich metadata: %w", closeErr)
	}

	showCache := map[string]showRef{}

	task, finish := activity.Default.Start("Metadata Enrichment", "items", len(items))
	defer finish()
	for i, p := range items {
		if err := governor.Checkpoint(ctx); err != nil {
			return err
		}
		activity.Default.Progress(task, i, len(items))
		if p.mediaType == "movie" {
			if err := enrichMovie(ctx, db, client, p.id, lookupName(p.title, p.path), int(p.releaseYear.Int64)); err != nil {
				return err
			}
			continue
		}
		seriesTitle := p.seriesTitle.String
		if seriesTitle == "" {
			seriesTitle = p.title
		}
		var existingID int64
		var existingTitle string
		_ = db.QueryRowContext(ctx, `SELECT s.id,s.title FROM identified_items i JOIN tv_shows s ON s.id=i.show_id WHERE i.id=? AND s.local_series_title IS NOT NULL`, p.id).Scan(&existingID, &existingTitle)
		lookup := lookupName(seriesTitle, p.path)
		if existingID > 0 {
			seriesTitle = existingTitle
			lookup = existingTitle
		}
		show, err := resolveShow(ctx, db, client, showCache, seriesTitle, lookup)
		if err != nil {
			return err
		}
		if err := enrichEpisode(ctx, db, client, p.id, show, int(p.seasonNumber.Int64), int(p.episodeNumber.Int64)); err != nil {
			return err
		}
	}
	return nil
}

// enrichMovie fetches and persists one movie's own TMDb details.
// Leaves metadata_fetched_at_unix NULL (retry next call) on any lookup
// error, exactly like ClassifyGenres' per-row error handling.
func enrichMovie(ctx context.Context, db *sql.DB, client *metadata.Client, id int64, title string, releaseYear int) error {
	tmdbID, err := client.FindMovieID(ctx, title, releaseYear)
	if err != nil {
		return nil
	}
	if tmdbID == 0 {
		_, err := db.ExecContext(ctx, `UPDATE identified_items SET metadata_fetched_at_unix = unixepoch() WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("library: enrich metadata: mark row %d attempted: %w", id, err)
		}
		return nil
	}

	details, err := client.MovieDetails(ctx, tmdbID)
	if err != nil {
		return nil
	}

	// Only backfills a release_year the filename-based identification
	// never found — never overrides an already-known one.
	var releaseYearArg any
	if releaseYear == 0 && len(details.ReleaseDate) >= 4 {
		if y, err := strconv.Atoi(details.ReleaseDate[:4]); err == nil {
			releaseYearArg = y
		}
	}

	_, err = db.ExecContext(ctx, `
		UPDATE identified_items
		SET overview = NULLIF(?, ''), release_date = NULLIF(?, ''),
		    content_rating = CASE WHEN content_rating_manual = 1 THEN content_rating ELSE NULLIF(?, '') END,
		    tmdb_id = ?, release_year = COALESCE(release_year, ?),
		    metadata_fetched_at_unix = unixepoch()
		WHERE id = ?`,
		details.Overview, details.ReleaseDate, details.Certification, tmdbID, releaseYearArg, id)
	if err != nil {
		return fmt.Errorf("library: enrich metadata: save movie row %d: %w", id, err)
	}
	return nil
}

// resolveShow returns seriesTitle's tv_shows row, resolving (and
// caching, for the rest of this EnrichMetadata call) it in this order:
// an already-cached result, an existing tv_shows row by title, a TMDb
// search + an existing tv_shows row by the found tmdb_id (a previous
// run may have already created it under a different title string), or
// finally a fresh TVDetails fetch + insert. A show with no TMDb match
// caches (and returns) a not-valid localID, so every episode of the
// same still-unresolved series in this same call shares one search
// instead of each retrying it.
//
// lookup is what the provider is searched with (see lookupName). It is
// also the cache key, and when it carries a year, region or ID that
// seriesTitle doesn't, the by-title shortcut is skipped: two shows can
// share a title.
func resolveShow(ctx context.Context, db *sql.DB, client *metadata.Client, cache map[string]showRef, seriesTitle, lookup string) (showRef, error) {
	if ref, ok := cache[lookup]; ok {
		return ref, nil
	}

	var localID int64
	var tmdbID int
	var fetched sql.NullInt64
	if lookup == seriesTitle {
		err := db.QueryRowContext(ctx, `SELECT id, tmdb_id, metadata_fetched_at_unix FROM tv_shows WHERE title = ?`, seriesTitle).Scan(&localID, &tmdbID, &fetched)
		if err == nil && tmdbID > 0 {
			if !fetched.Valid {
				details, err := client.TVDetails(ctx, tmdbID)
				if err != nil {
					return showRef{}, nil
				}
				_, err = db.ExecContext(ctx, `UPDATE tv_shows SET content_rating=CASE WHEN content_rating_manual=1 THEN content_rating ELSE NULLIF(?,'') END,metadata_fetched_at_unix=unixepoch() WHERE id=?`, details.Certification, localID)
				if err != nil {
					return showRef{}, err
				}
			}
			ref := showRef{localID: sql.NullInt64{Int64: localID, Valid: true}, tmdbID: tmdbID}
			cache[lookup] = ref
			return ref, nil
		}
		if err != nil && err != sql.ErrNoRows {
			return showRef{}, fmt.Errorf("library: enrich metadata: look up show %q: %w", seriesTitle, err)
		}
	}

	foundID, err := client.FindTVID(ctx, lookup, 0)
	if err != nil || foundID == 0 {
		ref := showRef{localID: sql.NullInt64{Int64: localID, Valid: localID > 0}}
		cache[lookup] = ref
		return ref, nil
	}

	if localID > 0 && tmdbID < 0 {
		details, err := client.TVDetails(ctx, foundID)
		if err != nil {
			return showRef{}, nil
		}
		_, err = db.ExecContext(ctx, `UPDATE tv_shows SET first_air_date=?,last_air_date=?,overview=?,content_rating=CASE WHEN content_rating_manual=1 THEN content_rating ELSE NULLIF(?,'') END,metadata_fetched_at_unix=unixepoch() WHERE id=?`, details.FirstAirDate, details.LastAirDate, details.Overview, details.Certification, localID)
		if err != nil {
			return showRef{}, err
		}
		ref := showRef{localID: sql.NullInt64{Int64: localID, Valid: true}, tmdbID: foundID}
		cache[lookup] = ref
		return ref, nil
	}
	err = db.QueryRowContext(ctx, `SELECT id FROM tv_shows WHERE tmdb_id = ?`, foundID).Scan(&localID)
	if err == nil {
		ref := showRef{localID: sql.NullInt64{Int64: localID, Valid: true}, tmdbID: foundID}
		cache[lookup] = ref
		return ref, nil
	}
	if err != sql.ErrNoRows {
		return showRef{}, fmt.Errorf("library: enrich metadata: look up show by tmdb id %d: %w", foundID, err)
	}

	details, err := client.TVDetails(ctx, foundID)
	if err != nil {
		// Leave this series entirely unresolved (not cached) so a later
		// call retries it, rather than caching a permanent "no show"
		// result over what was really just a transient lookup failure.
		return showRef{}, nil
	}
	res, err := db.ExecContext(ctx, `
		INSERT INTO tv_shows (tmdb_id, title, overview, first_air_date, last_air_date, content_rating, metadata_fetched_at_unix)
		VALUES (?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), unixepoch())`,
		foundID, seriesTitle, details.Overview, details.FirstAirDate, details.LastAirDate, details.Certification)
	if err != nil {
		return showRef{}, fmt.Errorf("library: enrich metadata: save show %q: %w", seriesTitle, err)
	}
	newID, err := res.LastInsertId()
	if err != nil {
		return showRef{}, fmt.Errorf("library: enrich metadata: save show %q: %w", seriesTitle, err)
	}
	ref := showRef{localID: sql.NullInt64{Int64: newID, Valid: true}, tmdbID: foundID}
	cache[lookup] = ref
	return ref, nil
}

// enrichEpisode fetches and persists one episode's own overview/air
// date, and links it to show (see resolveShow). A show with no TMDb
// match still marks this row attempted (it genuinely has nothing more
// to fetch), same convention as enrichMovie's no-match branch.
func enrichEpisode(ctx context.Context, db *sql.DB, client *metadata.Client, id int64, show showRef, season, episode int) error {
	if !show.localID.Valid {
		_, err := db.ExecContext(ctx, `UPDATE identified_items SET metadata_fetched_at_unix = unixepoch() WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("library: enrich metadata: mark row %d attempted: %w", id, err)
		}
		return nil
	}

	if show.tmdbID <= 0 {
		_, err := db.ExecContext(ctx, `UPDATE identified_items SET show_id=?,metadata_fetched_at_unix=unixepoch() WHERE id=?`, show.localID, id)
		return err
	}
	details, err := client.EpisodeDetails(ctx, show.tmdbID, season, episode)
	if err != nil {
		return nil
	}

	_, err = db.ExecContext(ctx, `
		UPDATE identified_items
		SET overview = NULLIF(?, ''), episode_air_date = NULLIF(?, ''), show_id = ?,
		    metadata_fetched_at_unix = unixepoch()
		WHERE id = ?`,
		details.Overview, details.AirDate, show.localID, id)
	if err != nil {
		return fmt.Errorf("library: enrich metadata: save episode row %d: %w", id, err)
	}
	return nil
}
