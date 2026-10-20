package lineup

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alreadyonapp/alreadyon/server/internal/channel"

	"github.com/alreadyonapp/alreadyon/server/internal/metadata"
)

// UnmatchedItem is a library file that isn't on any channel, with a
// short reason (#102).
type UnmatchedItem struct {
	AudienceOverride     string
	ShowAudienceOverride string
	ID                   int64 // identified_items.id, what corrections are addressed by
	TMDbID               int
	MediaFileID          int64
	Path                 string
	MediaType            string // "movie" or "episode"
	Title                string
	SeriesTitle          string
	Season               int
	Episode              int
	Year                 int
	Genres               []string // nil when genres haven't been classified yet
	Certification        string
	IsManual             bool
	Reason               string
}

// UnmatchedPage is one page of unmatched items and the overall count.
type UnmatchedPage struct {
	Total int
	Items []UnmatchedItem
}

// unmatchedWhere selects healthy, playable library files that no channel
// airs. A movie counts as matched when its own file is in a lineup; an
// episode when any episode of its show is, since eligibility is decided per
// show and a lineup only takes as much of a show as fits. The fallback
// demo channels (196-199) don't count: they hold everything by design.
// Uncorrelated IN subqueries build the matched sets once per query. A
// correlated EXISTS per library episode rescans large channel pools and
// monopolizes the shared SQLite connection, stalling other API requests.
const unmatchedWhere = `
	FROM identified_items ii
	JOIN media_files mf ON mf.id = ii.media_file_id
	LEFT JOIN tv_shows ts ON ts.id = ii.show_id
	WHERE mf.root_kind = 'media' AND mf.status = 'ok' AND mf.duration_seconds > 0
	  AND NOT (
	    (ii.media_type = 'movie' AND mf.id IN (
	        SELECT ci.media_file_id FROM channel_items ci JOIN channels c ON c.id = ci.channel_id
	        WHERE c.number NOT BETWEEN 196 AND 199))
	    OR (ii.media_type = 'episode' AND COALESCE(ii.series_title, ii.title) IN (
	        SELECT COALESCE(i2.series_title, i2.title) FROM channel_items ci
	        JOIN channels c ON c.id = ci.channel_id
	        JOIN identified_items i2 ON i2.media_file_id = ci.media_file_id
	        WHERE i2.media_type = 'episode' AND c.number NOT BETWEEN 196 AND 199))
	  )`

// Unmatched returns a page of library files not on any channel, ordered by
// path. Files that haven't been probed yet are left out: they can't be
// scheduled, and metadata wouldn't change that.
func Unmatched(ctx context.Context, db *sql.DB, limit, offset int) (UnmatchedPage, error) {
	filters, err := channel.ListFilters(ctx, db)
	if err != nil {
		return UnmatchedPage{}, err
	}
	channels, err := channel.List(ctx, db)
	if err != nil {
		return UnmatchedPage{}, err
	}
	var page UnmatchedPage
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) `+unmatchedWhere).Scan(&page.Total); err != nil {
		return UnmatchedPage{}, fmt.Errorf("lineup: count unmatched: %w", err)
	}
	rows, err := db.QueryContext(ctx, `
		SELECT ii.id, mf.id, mf.path, ii.media_type, ii.title, COALESCE(ii.series_title, ''),
		       COALESCE(ii.season_number, 0), COALESCE(ii.episode_number, 0), COALESCE(CASE WHEN ii.media_type = 'episode' THEN COALESCE(ts.premiere_year_override,CAST(substr(COALESCE(ts.first_air_date,ii.keyless_release_date), 1, 4) AS INTEGER),ii.release_year) ELSE ii.release_year END, 0),
		       COALESCE(CASE WHEN ii.genre_source='manual' THEN ii.genre_tags END,ts.genre_tags,ii.genre_tags),
		       COALESCE(CASE WHEN ii.media_type = 'movie' THEN ii.content_rating ELSE COALESCE(CASE WHEN ii.content_rating_manual=1 THEN ii.content_rating END,ts.content_rating,ii.content_rating) END, ''),
		       ii.is_manual_correction,COALESCE(ii.audience_override,''),COALESCE(ts.audience_override,''),

 COALESCE(CASE WHEN ii.media_type='episode' THEN ts.tmdb_id ELSE ii.tmdb_id END,0) `+unmatchedWhere+`
		ORDER BY mf.path LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return UnmatchedPage{}, fmt.Errorf("lineup: list unmatched: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var it UnmatchedItem
		var tags sql.NullString
		var manual int
		if err := rows.Scan(&it.ID, &it.MediaFileID, &it.Path, &it.MediaType, &it.Title, &it.SeriesTitle,
			&it.Season, &it.Episode, &it.Year, &tags, &it.Certification, &manual, &it.AudienceOverride, &it.ShowAudienceOverride, &it.TMDbID); err != nil {
			return UnmatchedPage{}, fmt.Errorf("lineup: list unmatched: %w", err)
		}
		it.IsManual = manual == 1
		if tags.Valid {
			it.Genres = []string{}
			if err := json.Unmarshal([]byte(tags.String), &it.Genres); err != nil {
				return UnmatchedPage{}, fmt.Errorf("lineup: decode genre_tags: %w", err)
			}
		}
		it.Reason = explainUnmatched(it, channels, filters)
		page.Items = append(page.Items, it)
	}
	return page, rows.Err()
}

// Explain against current saved filters, not the original channel catalog.
func explainUnmatched(it UnmatchedItem, channels []channel.Channel, filters map[int64]channel.Filter) string {
	title := it.Title
	if it.MediaType == "episode" && it.SeriesTitle != "" {
		title = it.SeriesTitle
	}
	candidate := classifiedItem{AudienceOverride: it.AudienceOverride, ShowAudienceOverride: it.ShowAudienceOverride, Path: it.Path, MediaType: it.MediaType, Title: title, ReleaseYear: it.Year, Certification: it.Certification}
	for _, g := range it.Genres {
		candidate.Tags = append(candidate.Tags, metadata.Tag(g))
	}
	var eligible, nearest []string
	best := 100
	for _, c := range channels {
		f, ok := filters[c.ID]
		if !ok || (c.Number >= 196 && c.Number <= 199) {
			continue
		}
		blocked := filterMismatches(f, candidate)
		label := fmt.Sprintf("%d %s", c.Number, c.Name)
		if len(blocked) == 0 {
			eligible = append(eligible, label)
			continue
		}
		if len(blocked) < best {
			best = len(blocked)
			nearest = nil
		}
		if len(blocked) == best && len(nearest) < 3 {
			nearest = append(nearest, label+": "+strings.Join(blocked, "; "))
		}
	}
	if len(eligible) > 0 {
		return "Eligible but not assigned to a channel pool. Matching channels: " + strings.Join(eligible, ", ") + ". The channel pool needs rebuilding; refreshing guide times alone cannot fix this."
	}
	if len(nearest) == 0 {
		return "No configured automatic channel filters. Create a channel or assign this title manually."
	}
	return "No configured channel matches. Closest channels: " + strings.Join(nearest, " | ") + "."
}

func overlaps(a, b []metadata.Tag) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
