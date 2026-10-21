package lineup

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/alreadyonapp/alreadyon/server/internal/channel"
)

// defaultFilterFor supplies the approved age, genre and era rules.
// Unknown years can use Modern/All defaults; Classics requires a known year.
func defaultFilterFor(number int) (channel.Filter, bool) {
	f, ok := defaultFilters[number]
	return f, ok
}

var defaultFilters = map[int]channel.Filter{
	101: {AudienceTier: "young_child", MediaType: "tv", Era: "nostalgia", Genres: []string{"animation", "anime"}, FilterExtra: channel.FilterExtra{RequireKnownYear: true, MatchAudience: true}},
	102: {AudienceTier: "young_child", MediaType: "tv", Era: "modern", Genres: []string{"animation", "anime"}, FilterExtra: channel.FilterExtra{RequireKnownYear: false, MatchAudience: true}},
	103: {AudienceTier: "young_child", MediaType: "tv", Era: "nostalgia", Genres: []string{}, FilterExtra: channel.FilterExtra{RequireKnownYear: true, MatchAudience: true, ExcludedGenres: []string{"animation", "anime"}}},
	104: {AudienceTier: "young_child", MediaType: "tv", Era: "modern", Genres: []string{}, FilterExtra: channel.FilterExtra{RequireKnownYear: false, MatchAudience: true, ExcludedGenres: []string{"animation", "anime"}}},
	105: {AudienceTier: "young_child", MediaType: "movie", Era: "nostalgia", Genres: []string{}, FilterExtra: channel.FilterExtra{RequireKnownYear: true, MatchAudience: true}},
	106: {AudienceTier: "young_child", MediaType: "movie", Era: "modern", Genres: []string{}, FilterExtra: channel.FilterExtra{RequireKnownYear: false, MatchAudience: true}},
	107: {AudienceTier: "teen", MediaType: "tv", Era: "nostalgia", Genres: []string{"animation"}, FilterExtra: channel.FilterExtra{RequireKnownYear: true, MatchAudience: true, ExcludedGenres: []string{"anime"}}},
	108: {AudienceTier: "teen", MediaType: "tv", Era: "modern", Genres: []string{"animation"}, FilterExtra: channel.FilterExtra{RequireKnownYear: false, MatchAudience: true, ExcludedGenres: []string{"anime"}}},
	109: {AudienceTier: "teen", MediaType: "tv", Era: "both", Genres: []string{"anime"}, FilterExtra: channel.FilterExtra{RequireKnownYear: false, MatchAudience: true}},
	110: {AudienceTier: "teen", MediaType: "tv", Era: "both", Genres: []string{"anime"}, FilterExtra: channel.FilterExtra{RequireKnownYear: false, MatchAudience: true}},
	111: {AudienceTier: "teen", MediaType: "tv", Era: "nostalgia", Genres: []string{"reality"}, FilterExtra: channel.FilterExtra{RequireKnownYear: true, MatchAudience: true, ExcludedGenres: []string{"animation", "anime"}}},
	112: {AudienceTier: "teen", MediaType: "tv", Era: "modern", Genres: []string{"reality"}, FilterExtra: channel.FilterExtra{RequireKnownYear: false, MatchAudience: true, ExcludedGenres: []string{"animation", "anime"}}},
	113: {AudienceTier: "teen", MediaType: "tv", Era: "nostalgia", Genres: []string{}, FilterExtra: channel.FilterExtra{RequireKnownYear: true, MatchAudience: true}},
	114: {AudienceTier: "teen", MediaType: "tv", Era: "modern", Genres: []string{}, FilterExtra: channel.FilterExtra{RequireKnownYear: false, MatchAudience: true}},
	115: {AudienceTier: "teen", MediaType: "tv", Era: "nostalgia", Genres: []string{"fantasy", "scifi"}, FilterExtra: channel.FilterExtra{RequireKnownYear: true, MatchAudience: true, ExcludedGenres: []string{"animation", "anime"}}},
	116: {AudienceTier: "teen", MediaType: "tv", Era: "modern", Genres: []string{"fantasy", "scifi"}, FilterExtra: channel.FilterExtra{RequireKnownYear: false, MatchAudience: true, ExcludedGenres: []string{"animation", "anime"}}},
	117: {AudienceTier: "teen", MediaType: "tv", Era: "nostalgia", Genres: []string{"action", "adventure"}, FilterExtra: channel.FilterExtra{RequireKnownYear: true, MatchAudience: true, ExcludedGenres: []string{"animation", "anime"}}},
	118: {AudienceTier: "teen", MediaType: "tv", Era: "modern", Genres: []string{"action", "adventure"}, FilterExtra: channel.FilterExtra{RequireKnownYear: false, MatchAudience: true, ExcludedGenres: []string{"animation", "anime"}}},
	119: {AudienceTier: "teen", MediaType: "movie", Era: "nostalgia", Genres: []string{}, FilterExtra: channel.FilterExtra{RequireKnownYear: true, MatchAudience: true}},
	120: {AudienceTier: "teen", MediaType: "movie", Era: "modern", Genres: []string{}, FilterExtra: channel.FilterExtra{RequireKnownYear: false, MatchAudience: true}},
	121: {AudienceTier: "adult", MediaType: "tv", Era: "both", Genres: []string{"animation"}, FilterExtra: channel.FilterExtra{RequireKnownYear: false, MatchAudience: true, ExcludedGenres: []string{"anime"}}},
	122: {AudienceTier: "adult", MediaType: "tv", Era: "both", Genres: []string{"animation"}, FilterExtra: channel.FilterExtra{RequireKnownYear: false, MatchAudience: true, ExcludedGenres: []string{"anime"}}},
	123: {AudienceTier: "adult", MediaType: "tv", Era: "both", Genres: []string{"anime"}, FilterExtra: channel.FilterExtra{RequireKnownYear: false, MatchAudience: true}},
	124: {AudienceTier: "adult", MediaType: "tv", Era: "both", Genres: []string{"anime"}, FilterExtra: channel.FilterExtra{RequireKnownYear: false, MatchAudience: true}},
	125: {AudienceTier: "adult", MediaType: "tv", Era: "both", Genres: []string{"reality"}, FilterExtra: channel.FilterExtra{RequireKnownYear: false, MatchAudience: true, ExcludedGenres: []string{"animation", "anime"}}},
	126: {AudienceTier: "adult", MediaType: "tv", Era: "both", Genres: []string{"reality"}, FilterExtra: channel.FilterExtra{RequireKnownYear: false, MatchAudience: true, ExcludedGenres: []string{"animation", "anime"}}},
	127: {AudienceTier: "adult", MediaType: "tv", Era: "both", Genres: []string{}, FilterExtra: channel.FilterExtra{RequireKnownYear: false, MatchAudience: true}},
	128: {AudienceTier: "adult", MediaType: "tv", Era: "both", Genres: []string{}, FilterExtra: channel.FilterExtra{RequireKnownYear: false, MatchAudience: true}},
	129: {AudienceTier: "adult", MediaType: "tv", Era: "both", Genres: []string{"fantasy", "scifi"}, FilterExtra: channel.FilterExtra{RequireKnownYear: false, MatchAudience: true, ExcludedGenres: []string{"animation", "anime"}}},
	130: {AudienceTier: "adult", MediaType: "tv", Era: "both", Genres: []string{"fantasy", "scifi"}, FilterExtra: channel.FilterExtra{RequireKnownYear: false, MatchAudience: true, ExcludedGenres: []string{"animation", "anime"}}},
	131: {AudienceTier: "adult", MediaType: "tv", Era: "both", Genres: []string{"action", "adventure"}, FilterExtra: channel.FilterExtra{RequireKnownYear: false, MatchAudience: true, ExcludedGenres: []string{"animation", "anime"}}},
	132: {AudienceTier: "adult", MediaType: "tv", Era: "both", Genres: []string{"action", "adventure"}, FilterExtra: channel.FilterExtra{RequireKnownYear: false, MatchAudience: true, ExcludedGenres: []string{"animation", "anime"}}},
	133: {AudienceTier: "adult", MediaType: "movie", Era: "both", Genres: []string{}, FilterExtra: channel.FilterExtra{RequireKnownYear: false, MatchAudience: true}},
	134: {AudienceTier: "adult", MediaType: "movie", Era: "both", Genres: []string{}, FilterExtra: channel.FilterExtra{RequireKnownYear: false, MatchAudience: true}},
	135: {AudienceTier: "adult", MediaType: "movie", Era: "both", Genres: []string{"action", "adventure", "crime", "thriller"}, FilterExtra: channel.FilterExtra{RequireKnownYear: false, MatchAudience: true}},
	136: {AudienceTier: "adult", MediaType: "movie", Era: "both", Genres: []string{"fantasy", "scifi", "horror"}, FilterExtra: channel.FilterExtra{RequireKnownYear: false, MatchAudience: true}},
	137: {AudienceTier: "teen", MediaType: "tv", Era: "nostalgia", Genres: []string{"comedy"}, FilterExtra: channel.FilterExtra{RequireKnownYear: true, MatchAudience: true, ExcludedGenres: []string{"animation", "anime"}}},
	138: {AudienceTier: "teen", MediaType: "tv", Era: "modern", Genres: []string{"comedy"}, FilterExtra: channel.FilterExtra{RequireKnownYear: false, MatchAudience: true, ExcludedGenres: []string{"animation", "anime"}}},
}

// SeedDefaultFilters gives every built-in default channel that has no filter
// its starting one, so every channel is built the same way and an admin can
// edit what a default channel airs. Channels that already have a filter are
// left alone. Returns how many filters it saved.
func SeedDefaultFilters(ctx context.Context, db *sql.DB) (int, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT c.id, c.number FROM channels c
		LEFT JOIN channel_filters f ON f.channel_id = c.id
		WHERE f.channel_id IS NULL AND c.number BETWEEN ? AND ?`,
		channel.DefaultChannelRangeStart, channel.DefaultChannelRangeEnd)
	if err != nil {
		return 0, fmt.Errorf("lineup: find default channels without a filter: %w", err)
	}
	type target struct {
		id     int64
		filter channel.Filter
	}
	var targets []target
	for rows.Next() {
		var id int64
		var number int
		if err := rows.Scan(&id, &number); err != nil {
			rows.Close()
			return 0, fmt.Errorf("lineup: find default channels without a filter: %w", err)
		}
		if f, ok := defaultFilterFor(number); ok {
			targets = append(targets, target{id, f})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	for _, t := range targets {
		if _, err := channel.SetFilter(ctx, db, t.id, t.filter); err != nil {
			return 0, fmt.Errorf("lineup: save default filter for channel %d: %w", t.id, err)
		}
	}
	return len(targets), nil
}
