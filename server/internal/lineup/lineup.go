// Package lineup fills channels with real library content matched to each
// channel's filter (see filter.go). The built-in default channels start
// from a filter equivalent to their original theme rules (defaults.go), so
// a fresh install already has something scheduled instead of empty
// channels. See #79 and the Epic 19 follow-on work it grew out of.
package lineup

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/metadata"
)

// targetSeconds is the aspirational amount of content a well-stocked
// library would give each default channel — 2 hours of headroom past
// the guide's 12-hour max time span setting, matching the margin used
// for the Guide's mock data earlier in this project's development.
// It's a stopping point, not a guarantee: channel_items forbids the
// same media file appearing twice in one channel's lineup (#21's
// AppendItems/ErrDuplicateItem), so a channel whose distinct eligible
// content adds up to less than this simply gets everything it has,
// once each. That's not a gap in the guide, though — the schedule
// engine (#22) already loops a channel's lineup indefinitely, so a
// short lineup just repeats sooner, exactly like a real rerun block.
const targetSeconds = 14 * 60 * 60

// candidate is one piece of eligible content: a single episode, or a
// movie (which is simply the only entry in its own one-item series).
type candidate struct {
	MediaFileID     int64
	DurationSeconds float64
}

// contentSeries groups a show's episodes (sorted into natural viewing
// order) or a single movie, for buildLineup's round-robin.
type contentSeries struct {
	Items []candidate
}

// Populate fills every channel that has a filter (see filter.go, and
// defaults.go for the built-in channels' starting filters) and no
// configured content yet with matching library content, up to
// targetSeconds. Channels the admin has already put content on are left
// untouched. Returns how many channels ended up with a lineup. Runs
// automatically at server startup.
func Populate(ctx context.Context, db *sql.DB) (int, error) {
	return build(ctx, db, true)
}

// Refresh rebuilds *every* filtered channel's lineup from scratch —
// including ones an earlier Populate (or a previous Refresh) already
// filled — using whatever's classified right now. Unlike Populate, this
// is never automatic: it's the Channels admin tab's "Refresh Schedule"
// button, an explicit, deliberate request to regenerate, so the usual
// "don't touch a channel that already has content" caution doesn't apply
// here. Channels without a filter keep their hand-managed lineup.
// Returns how many channels ended up with a lineup.
func Refresh(ctx context.Context, db *sql.DB) (int, error) {
	return build(ctx, db, false)
}

func build(ctx context.Context, db *sql.DB, emptyOnly bool) (int, error) {
	if _, err := SeedDefaultFilters(ctx, db); err != nil {
		return 0, err
	}
	built, err := rebuildFilteredChannels(ctx, db, emptyOnly)
	if err != nil {
		return built, err
	}
	_, _, err = channel.ApplyAutoVisibility(ctx, db)
	if err == nil {
		err = syncProviderChannels(ctx, db)
	}
	return built, err
}

type classifiedItem struct {
	Path            string
	MediaFileID     int64
	DurationSeconds float64
	MediaType       string
	// Title is the movie's title, or the show's name for an episode.
	Title         string
	SeriesTitle   string // "" for movies
	SeasonNumber  int
	EpisodeNumber int
	ReleaseYear   int
	Tags          []metadata.Tag
	// Certification is the movie's own content_rating, or — for an
	// episode — its parent show's (identified_items has no per-episode
	// certification; see #79's migration 0020 and
	// docs/PROJECT_BRIEF.md's "TV Show vs Episode Eligibility" section).
	// "" means not yet known — see categoryRule.matches for how that's
	// handled (permissively, unlike internal/eligibility's viewer-facing
	// default-deny).
	Certification string
}

// loadClassifiedPool returns every probed, healthy, classified media
// file in the library (regardless of which channel(s) it might end up
// on — the same content can air on more than one matching channel, like
// real syndication).
func loadClassifiedPool(ctx context.Context, db *sql.DB) ([]classifiedItem, error) {
	return loadPool(ctx, db, true)
}

// loadPool is loadClassifiedPool, optionally also returning files whose
// genres haven't been classified yet (they carry no tags), which a custom
// channel with no genre filter should still be able to air.
func loadPool(ctx context.Context, db *sql.DB, classifiedOnly bool) ([]classifiedItem, error) {
	query := `
		SELECT mf.id, mf.duration_seconds, ii.media_type, mf.path,
		       CASE WHEN ii.media_type = 'movie' THEN ii.title ELSE COALESCE(ii.series_title, ii.title) END,
		       COALESCE(ii.series_title, ''), COALESCE(ii.season_number, 0), COALESCE(ii.episode_number, 0),
		       COALESCE(CASE WHEN ii.media_type = 'episode' THEN CAST(substr(ts.first_air_date, 1, 4) AS INTEGER) ELSE ii.release_year END, 0), ii.genre_tags,
		       COALESCE(CASE WHEN ii.media_type = 'movie' THEN ii.content_rating ELSE COALESCE(ii.content_rating, ts.content_rating) END, '')
		FROM media_files mf
		JOIN identified_items ii ON ii.media_file_id = mf.id
		LEFT JOIN tv_shows ts ON ts.id = ii.show_id
		WHERE mf.root_kind = 'media' AND mf.status = 'ok' AND mf.duration_seconds > 0`
	if classifiedOnly {
		query += ` AND ii.genre_tags IS NOT NULL`
	}
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("lineup: load classified pool: %w", err)
	}
	defer rows.Close()

	var items []classifiedItem
	for rows.Next() {
		var it classifiedItem
		var tagsJSON sql.NullString
		if err := rows.Scan(&it.MediaFileID, &it.DurationSeconds, &it.MediaType, &it.Path, &it.Title,
			&it.SeriesTitle, &it.SeasonNumber, &it.EpisodeNumber, &it.ReleaseYear, &tagsJSON, &it.Certification); err != nil {
			return nil, fmt.Errorf("lineup: load classified pool: %w", err)
		}
		if tagsJSON.Valid {
			if err := json.Unmarshal([]byte(tagsJSON.String), &it.Tags); err != nil {
				return nil, fmt.Errorf("lineup: decode genre_tags: %w", err)
			}
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// seriesWhere groups the items of pool that match into series.
func seriesWhere(pool []classifiedItem, match func(classifiedItem) bool) []contentSeries {
	byShow := make(map[string][]classifiedItem)
	var order []string // first-seen order, for a stable/deterministic result
	for _, it := range pool {
		if !match(it) {
			continue
		}
		key := it.SeriesTitle
		if it.MediaType != "episode" {
			key = fmt.Sprintf("file:%d", it.MediaFileID) // movies and unknown files each rotate independently
		}
		if _, seen := byShow[key]; !seen {
			order = append(order, key)
		}
		byShow[key] = append(byShow[key], it)
	}

	series := make([]contentSeries, 0, len(order))
	for _, key := range order {
		group := byShow[key]
		sort.Slice(group, func(i, j int) bool {
			if group[i].SeasonNumber != group[j].SeasonNumber {
				return group[i].SeasonNumber < group[j].SeasonNumber
			}
			return group[i].EpisodeNumber < group[j].EpisodeNumber
		})
		items := make([]candidate, len(group))
		for i, it := range group {
			items[i] = candidate{MediaFileID: it.MediaFileID, DurationSeconds: it.DurationSeconds}
		}
		series = append(series, contentSeries{Items: items})
	}
	return series
}

// buildLineup round-robins across seriesList, picking one item at a
// time until either targetSeconds is covered or every series has been
// used up — whichever comes first — and returns the ordered
// media_file_ids to save as the channel's lineup. No media file is ever
// picked twice (the schema forbids it — see targetSeconds' doc
// comment), so once a series' last item has been used it's permanently
// skipped for the rest of this run.
//
// Within a series, each turn advances to its next not-yet-used item in
// natural order (S1E1 before S1E2, etc.) — the "prefer whichever has
// aired fewer times" rule collapses to exactly this when nothing can
// repeat within a single run: the next unused episode always has the
// lowest (zero) count. Across series, a plain rotation (A, B, C, A, B,
// C, ...) is used, with one safety valve: a series already picked twice
// in a row is skipped in favor of another still-active one (when more
// than one remains) — plain round-robin already prevents 3-in-a-row on
// its own; the valve only matters if that's ever bypassed.
func buildLineup(seriesList []contentSeries, targetSeconds float64) []int64 {
	if len(seriesList) == 0 {
		return nil
	}
	// nextItem[i] is how many of series i's items have been used so far;
	// series i is exhausted once nextItem[i] == len(seriesList[i].Items).
	nextItem := make([]int, len(seriesList))
	remaining := len(seriesList) // series not yet exhausted

	var result []int64
	var total float64
	lastIdx := -1
	lastRunLength := 0
	turn := 0

	for total < targetSeconds && remaining > 0 {
		idx := turn % len(seriesList)
		turn++
		if nextItem[idx] >= len(seriesList[idx].Items) {
			continue // exhausted: permanently out of the rotation
		}
		if idx == lastIdx && lastRunLength >= 2 && remaining > 1 {
			continue // anti-clump: let another active series go first
		}

		item := seriesList[idx].Items[nextItem[idx]]
		result = append(result, item.MediaFileID)
		total += item.DurationSeconds
		nextItem[idx]++
		if nextItem[idx] == len(seriesList[idx].Items) {
			remaining--
		}

		if idx == lastIdx {
			lastRunLength++
		} else {
			lastIdx = idx
			lastRunLength = 1
		}
	}
	return result
}
