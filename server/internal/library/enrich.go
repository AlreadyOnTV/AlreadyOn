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
	"github.com/alreadyonapp/alreadyon/server/internal/eligibility"
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
		WHERE ii.metadata_fetched_at_unix IS NULL
		   OR EXISTS (SELECT 1 FROM tv_shows s WHERE s.id=ii.show_id AND s.tmdb_id<0)`)
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
		lookup := lookupName(seriesTitle, p.path)
		var existingID int64
		var existingTitle string
		var existingTMDbID int
		if err := db.QueryRowContext(ctx, `SELECT s.id,s.title,s.tmdb_id FROM identified_items i JOIN tv_shows s ON s.id=i.show_id WHERE i.id=? AND s.local_series_title IS NOT NULL`, p.id).Scan(&existingID, &existingTitle, &existingTMDbID); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("library: enrich metadata: look up episode show: %w", err)
		}
		if existingID > 0 {
			seriesTitle = existingTitle
			parsed := metadata.ParseName(lookup)
			parsed.Title = existingTitle
			lookup = parsed.String()
		}
		show, err := resolveShow(ctx, db, client, showCache, seriesTitle, lookup, existingID, existingTMDbID)
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

// resolveShow retains an episode's existing parent. Negative-ID parents
// are reconciled only when the provider ID or a unique year-qualified
// identity confirms the match.
func resolveShow(ctx context.Context, db *sql.DB, client *metadata.Client, cache map[string]showRef, seriesTitle, lookup string, existingID int64, existingTMDbID int) (showRef, error) {
	if ref, ok := cache[lookup]; ok {
		return ref, nil
	}

	var localID int64
	var tmdbID int
	var fetched sql.NullInt64
	if existingID > 0 {
		localID, tmdbID = existingID, existingTMDbID
		if tmdbID > 0 {
			err := db.QueryRowContext(ctx, `SELECT metadata_fetched_at_unix FROM tv_shows WHERE id=?`, localID).Scan(&fetched)
			if err != nil {
				return showRef{}, fmt.Errorf("library: enrich metadata: look up show %d: %w", localID, err)
			}
			parsed := metadata.ParseName(lookup)
			if parsed.TMDbID != 0 && parsed.TMDbID != tmdbID {
				ref := showRef{localID: sql.NullInt64{Int64: localID, Valid: true}, tmdbID: tmdbID}
				cache[lookup] = ref
				return ref, nil
			}
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
	} else if lookup == seriesTitle {
		err := db.QueryRowContext(ctx, `SELECT id, tmdb_id, metadata_fetched_at_unix FROM tv_shows WHERE title = ? AND tmdb_id > 0 AND (SELECT count(*) FROM tv_shows WHERE title = ? AND tmdb_id > 0)=1`, seriesTitle, seriesTitle).Scan(&localID, &tmdbID, &fetched)
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

	err = db.QueryRowContext(ctx, `SELECT id FROM tv_shows WHERE tmdb_id = ?`, foundID).Scan(&localID)
	if err == nil {
		if existingID > 0 && existingTMDbID < 0 {
			merged, mergeErr := mergeDuplicateShow(ctx, db, existingID, localID, lookup, foundID)
			if mergeErr != nil {
				return showRef{}, mergeErr
			}
			if !merged {
				ref := showRef{localID: sql.NullInt64{Int64: existingID, Valid: true}}
				cache[lookup] = ref
				return ref, nil
			}
		}
		ref := showRef{localID: sql.NullInt64{Int64: localID, Valid: true}, tmdbID: foundID}
		cache[lookup] = ref
		return ref, nil
	}
	if err != sql.ErrNoRows {
		return showRef{}, fmt.Errorf("library: enrich metadata: look up show by tmdb id %d: %w", foundID, err)
	}
	if existingID > 0 && existingTMDbID < 0 {
		details, err := client.TVDetails(ctx, foundID)
		if err != nil {
			return showRef{}, nil
		}
		matches, err := placeholderMatchesDetails(ctx, db, existingID, lookup, foundID, details)
		if err != nil {
			return showRef{}, err
		}
		if !matches {
			ref := showRef{localID: sql.NullInt64{Int64: existingID, Valid: true}}
			cache[lookup] = ref
			return ref, nil
		}
		var currentRating sql.NullString
		var currentRatingManual bool
		if err := db.QueryRowContext(ctx, `SELECT content_rating,content_rating_manual FROM tv_shows WHERE id=?`, existingID).Scan(&currentRating, &currentRatingManual); err != nil {
			return showRef{}, fmt.Errorf("library: enrich metadata: look up local show rating %d: %w", existingID, err)
		}
		providerRating := sql.NullString{String: details.Certification, Valid: details.Certification != ""}
		rating := preferredCertification(currentRating, providerRating)
		if currentRatingManual {
			rating = currentRating
		}
		_, err = db.ExecContext(ctx, `UPDATE tv_shows SET tmdb_id=?,overview=coalesce(NULLIF(?,''),overview),first_air_date=coalesce(NULLIF(?,''),first_air_date),last_air_date=coalesce(NULLIF(?,''),last_air_date),content_rating=?,metadata_fetched_at_unix=unixepoch() WHERE id=?`, foundID, details.Overview, details.FirstAirDate, details.LastAirDate, rating, existingID)
		if err != nil {
			return showRef{}, fmt.Errorf("library: enrich metadata: update local show %d: %w", existingID, err)
		}
		ref := showRef{localID: sql.NullInt64{Int64: existingID, Valid: true}, tmdbID: foundID}
		cache[lookup] = ref
		return ref, nil
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

func placeholderMatchesDetails(ctx context.Context, db *sql.DB, showID int64, lookup string, tmdbID int, details metadata.TVDetails) (bool, error) {
	var localTitle string
	var localYear sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT local_series_title,local_series_year FROM tv_shows WHERE id=?`, showID).Scan(&localTitle, &localYear); err != nil {
		return false, err
	}
	parsed := metadata.ParseName(lookup)
	if parsed.TMDbID > 0 && parsed.TMDbID != tmdbID {
		return false, nil
	}
	expectedYear := parsed.Year
	if localYear.Valid {
		if expectedYear > 0 && expectedYear != int(localYear.Int64) {
			return false, nil
		}
		expectedYear = int(localYear.Int64)
	}
	providerYear := 0
	if len(details.FirstAirDate) >= 4 {
		providerYear, _ = strconv.Atoi(details.FirstAirDate[:4])
	}
	if expectedYear > 0 && providerYear > 0 {
		difference := expectedYear - providerYear
		if difference < 0 {
			difference = -difference
		}
		if difference > 1 {
			return false, nil
		}
	}
	localRegion := metadata.ParseName(localTitle).Region
	if parsed.Region != "" {
		localRegion = parsed.Region
	}
	providerRegion := metadata.ParseName(details.Name).Region
	if (localRegion != "" || providerRegion != "") && localRegion != providerRegion {
		return false, nil
	}
	hasProviderID := parsed.TMDbID > 0 || parsed.IMDbID != "" || parsed.TVDBID > 0
	if !hasProviderID && (!metadata.SameTitle(localTitle, details.Name) || expectedYear == 0 || providerYear != expectedYear) {
		return false, nil
	}
	return true, nil
}

func mergeDuplicateShow(ctx context.Context, db *sql.DB, sourceID, targetID int64, lookup string, tmdbID int) (bool, error) {
	type parent struct {
		tmdbID              int
		title               string
		localTitle          sql.NullString
		localYear           sql.NullInt64
		firstAirDate        sql.NullString
		contentRating       sql.NullString
		contentRatingManual bool
		genreTags           sql.NullString
		genreSource         sql.NullString
		audienceOverride    sql.NullString
		premiereYear        sql.NullInt64
	}
	load := func(id int64) (parent, error) {
		var p parent
		err := db.QueryRowContext(ctx, `SELECT tmdb_id,title,local_series_title,local_series_year,first_air_date,content_rating,content_rating_manual,genre_tags,genre_source,audience_override,premiere_year_override FROM tv_shows WHERE id=?`, id).
			Scan(&p.tmdbID, &p.title, &p.localTitle, &p.localYear, &p.firstAirDate, &p.contentRating, &p.contentRatingManual, &p.genreTags, &p.genreSource, &p.audienceOverride, &p.premiereYear)
		return p, err
	}
	source, err := load(sourceID)
	if err != nil {
		return false, err
	}
	target, err := load(targetID)
	if err != nil {
		return false, err
	}
	if source.tmdbID >= 0 || target.tmdbID != tmdbID {
		return false, nil
	}
	parsed := metadata.ParseName(lookup)
	confirmed := parsed.TMDbID == tmdbID
	sourceRegion := metadata.ParseName(source.localTitle.String).Region
	targetRegion := metadata.ParseName(target.title).Region
	if parsed.Region != "" {
		sourceRegion = parsed.Region
	}
	if (sourceRegion != "" || targetRegion != "") && sourceRegion != targetRegion {
		return false, nil
	}
	if source.localYear.Valid && target.localYear.Valid && source.localYear.Int64 != target.localYear.Int64 {
		return false, nil
	}
	if source.localYear.Valid && target.firstAirDate.Valid && len(target.firstAirDate.String) >= 4 {
		targetYear, err := strconv.Atoi(target.firstAirDate.String[:4])
		if err == nil {
			yearDifference := int(source.localYear.Int64) - targetYear
			if yearDifference < 0 {
				yearDifference = -yearDifference
			}
			if yearDifference > 1 {
				return false, nil
			}
		}
	}
	if !confirmed {
		year := parsed.Year
		if source.localYear.Valid {
			if year > 0 && year != int(source.localYear.Int64) {
				return false, nil
			}
			year = int(source.localYear.Int64)
		}
		if year == 0 || !metadata.SameTitle(source.localTitle.String, target.title) || !strings.HasPrefix(target.firstAirDate.String, strconv.Itoa(year)) {
			return false, nil
		}
		rows, err := db.QueryContext(ctx, `SELECT id,title,first_air_date FROM tv_shows WHERE tmdb_id>0`)
		if err != nil {
			return false, err
		}
		matches := 0
		for rows.Next() {
			var candidateID int64
			var title, airDate string
			if err := rows.Scan(&candidateID, &title, &airDate); err != nil {
				rows.Close()
				return false, err
			}
			if metadata.SameTitle(source.localTitle.String, title) && strings.HasPrefix(airDate, strconv.Itoa(year)) {
				matches++
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return false, err
		}
		if err := rows.Close(); err != nil {
			return false, err
		}
		confirmed = matches == 1
	}
	if !confirmed || conflictingParentCorrections(source, target) {
		return false, nil
	}
	contentRating := preferredCertification(target.contentRating, source.contentRating)
	contentRatingManual := target.contentRatingManual || source.contentRatingManual
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE tv_shows SET
		content_rating=?,content_rating_manual=?,
		overview=coalesce(overview,(SELECT overview FROM tv_shows WHERE id=?)),
		first_air_date=coalesce(first_air_date,(SELECT first_air_date FROM tv_shows WHERE id=?)),
		last_air_date=coalesce(last_air_date,(SELECT last_air_date FROM tv_shows WHERE id=?)),
		genre_tags=CASE WHEN (SELECT genre_source FROM tv_shows WHERE id=?)='manual' AND coalesce(genre_source,'')!='manual' THEN (SELECT genre_tags FROM tv_shows WHERE id=?) ELSE coalesce(genre_tags,(SELECT genre_tags FROM tv_shows WHERE id=?)) END,
		genre_source=CASE WHEN coalesce(genre_source,'')!='manual' THEN coalesce((SELECT genre_source FROM tv_shows WHERE id=?),genre_source) ELSE genre_source END,
		audience_override=coalesce(audience_override,(SELECT audience_override FROM tv_shows WHERE id=?)),
		premiere_year_override=coalesce(premiere_year_override,(SELECT premiere_year_override FROM tv_shows WHERE id=?)),
		title=CASE WHEN (SELECT local_series_title FROM tv_shows WHERE id=?) IS NOT NULL AND (SELECT title FROM tv_shows WHERE id=?)!=(SELECT local_series_title FROM tv_shows WHERE id=?) AND (local_series_title IS NULL OR title=local_series_title) THEN (SELECT title FROM tv_shows WHERE id=?) ELSE title END,
		local_series_title=coalesce(local_series_title,(SELECT local_series_title FROM tv_shows WHERE id=?)),
		local_series_year=coalesce(local_series_year,(SELECT local_series_year FROM tv_shows WHERE id=?))
		WHERE id=?`, contentRating, contentRatingManual, sourceID, sourceID, sourceID, sourceID, sourceID, sourceID, sourceID, sourceID, sourceID, sourceID, sourceID, sourceID, sourceID, sourceID, sourceID, targetID); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE identified_items SET show_id=? WHERE show_id=?`, targetID, sourceID); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM tv_shows WHERE id=?`, sourceID); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func preferredCertification(primary, fallback sql.NullString) sql.NullString {
	if !fallback.Valid || fallback.String == "" {
		return primary
	}
	if !primary.Valid || primary.String == "" {
		return fallback
	}
	primaryTier := eligibility.RequiredTier("episode", primary.String)
	fallbackTier := eligibility.RequiredTier("episode", fallback.String)
	if eligibility.Max(primaryTier, fallbackTier) == fallbackTier && primaryTier != fallbackTier {
		return fallback
	}
	return primary
}

func conflictingParentCorrections(source, target struct {
	tmdbID              int
	title               string
	localTitle          sql.NullString
	localYear           sql.NullInt64
	firstAirDate        sql.NullString
	contentRating       sql.NullString
	contentRatingManual bool
	genreTags           sql.NullString
	genreSource         sql.NullString
	audienceOverride    sql.NullString
	premiereYear        sql.NullInt64
}) bool {
	if source.audienceOverride.Valid && target.audienceOverride.Valid && source.audienceOverride.String != target.audienceOverride.String {
		return true
	}
	if source.contentRating.Valid && target.contentRating.Valid && source.contentRating.String != target.contentRating.String && (source.contentRatingManual || target.contentRatingManual) {
		return true
	}
	if source.genreSource.String == "manual" && target.genreSource.String == "manual" && source.genreTags.String != target.genreTags.String {
		return true
	}
	if source.premiereYear.Valid && target.premiereYear.Valid && source.premiereYear.Int64 != target.premiereYear.Int64 {
		return true
	}
	sourceTitleManual := source.localTitle.Valid && source.title != source.localTitle.String
	targetTitleManual := target.localTitle.Valid && target.title != target.localTitle.String
	return sourceTitleManual && targetTitleManual && !metadata.SameTitle(source.title, target.title)
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
