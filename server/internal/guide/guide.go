// Package guide resolves schedule.Entry (a channel id, media file id,
// and time window) into what a program-guide UI actually needs to
// display: titles, episode info, and commercial-break labeling. See
// docs/PROJECT_BRIEF.md Epic 3, issue #25.
package guide

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
)

// Program is one schedule.Entry, resolved to display metadata.
// Timestamps are always in UTC (inherited from schedule.Entry).
type Program struct {
	ChannelID     int64
	MediaFileID   int64
	Kind          schedule.Kind
	Title         string
	SeriesTitle   string
	SeasonNumber  int
	EpisodeNumber int
	MediaType     string // "movie" or "episode"; empty for a commercial break
	// Overview is the movie's own synopsis, or — for an episode — that
	// specific episode's own description (never the parent show's own
	// overview; see internal/library.EnrichMetadata). "" when not yet
	// fetched from TMDb, or nothing came back.
	Overview string
	// Year is a movie's release year (0 when unknown); ContentRating is the movie's
	// or the show's certification; AirDate is an episode's original air date, or
	// a movie's release date, as YYYY-MM-DD ("" when unknown).
	Year          int
	ContentRating string
	AirDate       string
	StartsAt      time.Time
	EndsAt        time.Time
	// SourceOffsetSeconds is where within MediaFileID's own file this
	// entry starts — 0 unless a mid-program commercial break (see
	// commercial.ModeInterval) split it into multiple segments; a
	// player seeks here rather than always starting the file from its
	// own beginning.
	SourceOffsetSeconds float64
}

// ForEntry describes the exact entry being played, including delayed and prefetched
// streams. Looking up the wall-clock guide could describe a different program.
func ForEntry(ctx context.Context, db *sql.DB, entry schedule.Entry) (Program, error) {
	meta, err := loadMetadata(ctx, db, []schedule.Entry{entry})
	if err != nil {
		return Program{}, err
	}
	return toProgram(entry, meta), nil
}

// commercialBreakTitle is used for every Kind == KindCommercial entry —
// breaks aren't identified content, so there's no real title to resolve
// (see loadMetadata).
const commercialBreakTitle = "Commercial Break"

// windowBatchLimit bounds how many schedule.Upcoming entries Window will
// ever fetch from the DB in one call — generous enough to cover any
// realistic guide window (even one built almost entirely of short
// commercial breaks) without an unbounded query.
const windowBatchLimit = 500

// Preview returns the program currently playing on channelID and how
// far into it playback is — the lightweight "what's on now" lookup for
// a live preview bar or channel-surfing.
func Preview(ctx context.Context, db *sql.DB, clock schedule.Clock, channelID int64) (Program, time.Duration, error) {
	entry, offset, err := schedule.Current(ctx, db, clock, channelID)
	if err != nil {
		return Program{}, 0, err
	}
	meta, err := loadMetadata(ctx, db, []schedule.Entry{entry})
	if err != nil {
		return Program{}, 0, err
	}
	return toProgram(entry, meta), offset, nil
}

// Window returns every program (and inserted commercial break) airing
// on channelID from clock's current time through dur later — enough for
// a program-guide UI to render a whole visible time span in one call.
// Window(...)[0] always matches what Preview returns.
func Window(ctx context.Context, db *sql.DB, clock schedule.Clock, channelID int64, dur time.Duration) ([]Program, error) {
	entries, err := schedule.Upcoming(ctx, db, clock, channelID, windowBatchLimit)
	if err != nil {
		return nil, err
	}

	cutoff := clock.Now().UTC().Add(dur)
	var clipped []schedule.Entry
	for _, e := range entries {
		if e.StartsAt.After(cutoff) {
			break
		}
		clipped = append(clipped, e)
	}

	meta, err := loadMetadata(ctx, db, clipped)
	if err != nil {
		return nil, err
	}
	programs := make([]Program, len(clipped))
	for i, e := range clipped {
		programs[i] = toProgram(e, meta)
	}
	return programs, nil
}

func toProgram(e schedule.Entry, meta map[int64]mediaMeta) Program {
	p := Program{
		ChannelID:           e.ChannelID,
		MediaFileID:         e.MediaFileID,
		Kind:                e.Kind,
		StartsAt:            e.StartsAt,
		EndsAt:              e.EndsAt,
		SourceOffsetSeconds: e.SourceOffsetSeconds,
	}
	if e.Kind == schedule.KindCommercial {
		p.Title = commercialBreakTitle
		return p
	}
	m := meta[e.MediaFileID]
	p.Title = m.title
	p.SeriesTitle = m.seriesTitle
	p.SeasonNumber = m.seasonNumber
	p.EpisodeNumber = m.episodeNumber
	p.MediaType = m.mediaType
	p.Overview = m.overview
	p.Year, p.ContentRating, p.AirDate = m.year, m.contentRating, m.airDate
	return p
}

type mediaMeta struct {
	title         string
	seriesTitle   string
	seasonNumber  int
	episodeNumber int
	mediaType     string
	overview      string
	year          int
	contentRating string
	airDate       string
}

// loadMetadata batch-resolves every non-commercial entry's MediaFileID
// to its identified_items row, falling back to a cleaned-up filename
// for a file that hasn't been identified yet (or never will be, e.g. a
// manually-added file with no metadata match).
func loadMetadata(ctx context.Context, db *sql.DB, entries []schedule.Entry) (map[int64]mediaMeta, error) {
	ids := make([]int64, 0, len(entries))
	seen := make(map[int64]bool, len(entries))
	for _, e := range entries {
		if e.Kind == schedule.KindCommercial || seen[e.MediaFileID] {
			continue
		}
		seen[e.MediaFileID] = true
		ids = append(ids, e.MediaFileID)
	}
	if len(ids) == 0 {
		return map[int64]mediaMeta{}, nil
	}

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf(`
		SELECT mf.id, mf.path,
		       COALESCE(ii.media_type, ''), COALESCE(ii.title, ''), COALESCE(ii.series_title, ''),
		       COALESCE(ii.season_number, 0), COALESCE(ii.episode_number, 0),
		       COALESCE(ii.overview, ''),
		       COALESCE(ii.release_year, CAST(substr(ii.release_date, 1, 4) AS INTEGER), 0),
		       COALESCE(ii.content_rating, ts.content_rating, ''),
		       COALESCE(ii.episode_air_date, ii.release_date, '')
		FROM media_files mf
		LEFT JOIN identified_items ii ON ii.media_file_id = mf.id
		LEFT JOIN tv_shows ts ON ts.id = ii.show_id
		WHERE mf.id IN (%s)`, strings.Join(placeholders, ","))

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("guide: load metadata: %w", err)
	}
	defer rows.Close()

	result := make(map[int64]mediaMeta, len(ids))
	for rows.Next() {
		var id int64
		var path string
		var m mediaMeta
		if err := rows.Scan(&id, &path, &m.mediaType, &m.title, &m.seriesTitle, &m.seasonNumber, &m.episodeNumber,
			&m.overview, &m.year, &m.contentRating, &m.airDate); err != nil {
			return nil, fmt.Errorf("guide: load metadata: %w", err)
		}
		if m.title == "" {
			m.title = titleFromPath(path)
		}
		result[id] = m
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("guide: load metadata: %w", err)
	}
	return result, nil
}

// titleFromPath falls back to a filename with its extension and any
// bracketed/parenthesized release tags stripped, so an unidentified
// file still shows something readable instead of a blank title.
func titleFromPath(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	return strings.TrimSpace(base)
}
