package channel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/alreadyonapp/alreadyon/server/internal/metadata"
)

// Filter defines what library content a channel airs (#123). The
// audience tier is a hard eligibility boundary (see internal/eligibility):
// content that needs a higher tier, or has an unknown rating, never
// matches. Everything else narrows the match; the zero/"both"/empty value
// of each means "don't narrow on this".
type Filter struct {
	FilterExtra
	AgeOption             *FilterOption
	EraOptions            []FilterOption
	ResolvedExcludedPaths []string
	ResolvedIncludedPaths []string
	// AudienceTier is "young_child", "teen" or "adult": the audience the
	// channel is for. Content the tier isn't eligible to see is excluded.
	AudienceTier string
	// Genres are metadata tag ids; content matches if it has any of them.
	// Empty means any genre.
	Genres []string
	// MediaType is "tv", "movie" or "both".
	MediaType string
	// Era is "modern" (2010 on), "nostalgia" (up to 2009) or "both".
	Era string
	// TitleContains matches a movie's title or a show's name,
	// case-insensitively, anywhere in it. Empty means no title filter.
	TitleContains string
}

const MaxTitleFilterLength = 100

var (
	ErrInvalidFilterOptions = errors.New("channel: invalid filter options")
	ErrInvalidAudienceTier  = errors.New("channel: audienceTier must be young_child, teen, or adult")
	ErrInvalidGenre         = errors.New("channel: unknown genre")
	ErrInvalidMediaType     = errors.New("channel: mediaType must be tv, movie, or both")
	ErrInvalidEra           = errors.New("channel: era must be modern, nostalgia, or both")
	ErrInvalidTitleFilter   = fmt.Errorf("channel: titleContains must be at most %d characters with no control characters", MaxTitleFilterLength)
)

// Normalize validates f and returns it cleaned up: title trimmed, genres
// de-duplicated in the order given, and empty MediaType/Era set to "both".
func (f Filter) Normalize() (Filter, error) {
	switch f.AudienceTier {
	case "young_child", "teen", "adult":
	default:
		return Filter{}, ErrInvalidAudienceTier
	}
	if f.MediaType == "" {
		f.MediaType = "both"
	}
	if f.MediaType != "tv" && f.MediaType != "movie" && f.MediaType != "both" {
		return Filter{}, ErrInvalidMediaType
	}
	if f.Era == "" {
		f.Era = "both"
	}
	if f.Era != "modern" && f.Era != "nostalgia" && f.Era != "both" {
		return Filter{}, ErrInvalidEra
	}
	for _, g := range f.ExcludedGenres {
		if !metadata.ValidTag(g) {
			return Filter{}, fmt.Errorf("%w: %q", ErrInvalidGenre, g)
		}
	}
	genres := make([]string, 0, len(f.Genres))
	for _, g := range f.Genres {
		if !metadata.ValidTag(g) {
			return Filter{}, fmt.Errorf("%w: %q", ErrInvalidGenre, g)
		}
		if !slices.Contains(genres, g) {
			genres = append(genres, g)
		}
	}
	f.Genres = genres
	f.TitleContains = strings.TrimSpace(f.TitleContains)
	if utf8.RuneCountInString(f.TitleContains) > MaxTitleFilterLength {
		return Filter{}, ErrInvalidTitleFilter
	}
	for _, r := range f.TitleContains {
		if unicode.IsControl(r) {
			return Filter{}, ErrInvalidTitleFilter
		}
	}
	return normalizeExtra(f)
}

// GetFilter returns the channel's filter, or nil if it has none.
func GetFilter(ctx context.Context, db *sql.DB, channelID int64) (*Filter, error) {
	filters, err := listFilters(ctx, db, `WHERE channel_id = ?`, channelID)
	if err != nil {
		return nil, err
	}
	if f, ok := filters[channelID]; ok {
		return &f, nil
	}
	return nil, nil
}

// ListFilters returns every channel's filter, keyed by channel id.
func ListFilters(ctx context.Context, db *sql.DB) (map[int64]Filter, error) {
	return listFilters(ctx, db, ``)
}

func listFilters(ctx context.Context, db *sql.DB, where string, args ...any) (map[int64]Filter, error) {
	rows, err := db.QueryContext(ctx, `SELECT channel_id, audience_tier, genres, media_type, era, title_contains, extra FROM channel_filters `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("channel: list filters: %w", err)
	}
	defer rows.Close()
	out := map[int64]Filter{}
	for rows.Next() {
		var id int64
		var f Filter
		var genres, extra string
		if err := rows.Scan(&id, &f.AudienceTier, &genres, &f.MediaType, &f.Era, &f.TitleContains, &extra); err != nil {
			return nil, fmt.Errorf("channel: list filters: %w", err)
		}
		if err := json.Unmarshal([]byte(genres), &f.Genres); err != nil {
			return nil, fmt.Errorf("channel: decode filter genres: %w", err)
		}
		if err := json.Unmarshal([]byte(extra), &f.FilterExtra); err != nil {
			return nil, err
		}
		if f.Genres == nil {
			f.Genres = []string{}
		}
		out[id] = f
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	catalog, err := ListOptions(ctx, db)
	if err != nil {
		return nil, err
	}
	for id, f := range out {
		resolved, err := ResolveFilter(f, catalog)
		if err != nil {
			return nil, err
		}
		out[id] = resolved
	}
	return out, nil
}

// SetFilter validates and saves f for a channel and returns the saved form.
func SetFilter(ctx context.Context, db *sql.DB, channelID int64, f Filter) (Filter, error) {
	f, err := f.Normalize()
	if err != nil {
		return Filter{}, err
	}
	if _, err := Get(ctx, db, channelID); err != nil {
		return Filter{}, err
	}
	if _, err := Resolve(ctx, db, f); err != nil {
		return Filter{}, err
	}
	extra, err := json.Marshal(f.FilterExtra)
	if err != nil {
		return Filter{}, err
	}
	genres, err := json.Marshal(f.Genres)
	if err != nil {
		return Filter{}, err
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO channel_filters (channel_id, audience_tier, genres, media_type, era, title_contains, extra)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (channel_id) DO UPDATE SET audience_tier = excluded.audience_tier, genres = excluded.genres,
			media_type = excluded.media_type, era = excluded.era, title_contains = excluded.title_contains,
			extra = excluded.extra, updated_at_unix = unixepoch()`,
		channelID, f.AudienceTier, string(genres), f.MediaType, f.Era, f.TitleContains, string(extra))
	if err != nil {
		return Filter{}, fmt.Errorf("channel: set filter: %w", err)
	}
	return f, nil
}

// ClearFilter removes a channel's filter. Its current lineup is left as
// is, from then on managed by hand again.
func ClearFilter(ctx context.Context, db *sql.DB, channelID int64) error {
	if _, err := db.ExecContext(ctx, `DELETE FROM channel_filters WHERE channel_id = ?`, channelID); err != nil {
		return fmt.Errorf("channel: clear filter: %w", err)
	}
	return nil
}
