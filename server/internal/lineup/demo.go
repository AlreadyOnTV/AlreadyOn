package lineup

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/alreadyonapp/alreadyon/server/internal/governor"
	"github.com/alreadyonapp/alreadyon/server/internal/metadata"
)

// demoChannelSpec describes one fallback demo channel (#111). Numbers sit
// in the reserved built-in range, so an admin can never create a custom
// channel that collides with them.
type demoChannelSpec struct {
	Number    int
	Name      string
	MediaType string // "episode" or "movie"
	Slot      int    // which half of that media type's pool: 0 or 1
}

var demoChannels = []demoChannelSpec{
	{Number: 196, Name: "Demo: TV Mix 1", MediaType: "episode", Slot: 0},
	{Number: 197, Name: "Demo: TV Mix 2", MediaType: "episode", Slot: 1},
	{Number: 198, Name: "Demo: Movie Mix 1", MediaType: "movie", Slot: 0},
	{Number: 199, Name: "Demo: Movie Mix 2", MediaType: "movie", Slot: 1},
}

// IsDemoChannelNumber reports whether n is one of the fallback demo
// channel numbers.
func IsDemoChannelNumber(n int) bool {
	for _, d := range demoChannels {
		if d.Number == n {
			return true
		}
	}
	return false
}

// SyncDemoChannels keeps the fallback demo channels in step with whether
// real channel matching is producing anything the guide will show. When
// no other visible channel has content (typically: no metadata provider
// key, so nothing is classified — or only channels too thin to display,
// see minDistinctSeriesToDisplay), it creates up to four demo channels —
// two TV, two movie — filled with every identified library item of that
// type regardless of classification. As soon as any visible non-demo
// channel has content, the demo channels are deleted. Returns how many
// demo channels exist afterward.
func SyncDemoChannels(ctx context.Context, db *sql.DB) (int, error) {
	if err := governor.Checkpoint(ctx); err != nil {
		return 0, err
	}
	settings, err := metadata.GetSettings(ctx, db)
	if err != nil {
		return 0, err
	}
	if err := clearInactiveProviderChannels(ctx, db, settings); err != nil {
		return 0, err
	}
	if settings.TMDbAPIKey == "" && settings.KeylessEnabled {
		_, err := db.ExecContext(ctx, `DELETE FROM channels WHERE number BETWEEN 196 AND 199`)
		return 0, err
	}
	placeholders := make([]string, len(demoChannels))
	args := make([]any, len(demoChannels))
	for i, d := range demoChannels {
		placeholders[i] = "?"
		args[i] = d.Number
	}
	inList := strings.Join(placeholders, ",")

	var realWithContent int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT c.id) FROM channels c
		JOIN channel_items ci ON ci.channel_id = c.id
		WHERE c.hidden = 0 AND c.number NOT IN (`+inList+`)`, args...).Scan(&realWithContent); err != nil {
		return 0, fmt.Errorf("lineup: demo channels: count real channels: %w", err)
	}
	if realWithContent > 0 {
		if _, err := db.ExecContext(ctx, `DELETE FROM channels WHERE number IN (`+inList+`)`, args...); err != nil {
			return 0, fmt.Errorf("lineup: demo channels: remove: %w", err)
		}
		return 0, nil
	}

	pool, err := loadIdentifiedPool(ctx, db)
	if err != nil {
		return 0, err
	}
	halves := map[string][2][]contentSeries{
		"episode": splitSeries(groupAllSeries(pool, "episode")),
		"movie":   splitSeries(groupAllSeries(pool, "movie")),
	}

	active := 0
	for _, spec := range demoChannels {
		series := halves[spec.MediaType][spec.Slot]
		if len(series) == 0 {
			if _, err := db.ExecContext(ctx, `DELETE FROM channels WHERE number = ?`, spec.Number); err != nil {
				return active, fmt.Errorf("lineup: demo channel %d: remove: %w", spec.Number, err)
			}
			continue
		}
		if err := saveDemoChannel(ctx, db, spec, buildLineup(series, targetSeconds)); err != nil {
			return active, err
		}
		active++
	}
	return active, nil
}

// loadIdentifiedPool is loadClassifiedPool without the classification
// requirement: every healthy, identified library file.
func loadIdentifiedPool(ctx context.Context, db *sql.DB) ([]classifiedItem, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT mf.id, mf.duration_seconds, ii.media_type,
		       COALESCE(NULLIF(ii.series_title, ''), ii.title), COALESCE(ii.season_number, 0), COALESCE(ii.episode_number, 0)
		FROM media_files mf
		JOIN identified_items ii ON ii.media_file_id = mf.id
		WHERE mf.root_kind = 'media' AND mf.status = 'ok' AND mf.duration_seconds > 0`)
	if err != nil {
		return nil, fmt.Errorf("lineup: load identified pool: %w", err)
	}
	defer rows.Close()

	var items []classifiedItem
	for rows.Next() {
		var it classifiedItem
		if err := rows.Scan(&it.MediaFileID, &it.DurationSeconds, &it.MediaType,
			&it.SeriesTitle, &it.SeasonNumber, &it.EpisodeNumber); err != nil {
			return nil, fmt.Errorf("lineup: load identified pool: %w", err)
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// groupAllSeries groups pool items of mediaType into series sorted by
// title (episodes in natural order; each movie is its own series).
func groupAllSeries(pool []classifiedItem, mediaType string) []contentSeries {
	type group struct {
		title string
		items []classifiedItem
	}
	byKey := make(map[string]*group)
	for _, it := range pool {
		if it.MediaType != mediaType {
			continue
		}
		key := strings.ToLower(it.SeriesTitle)
		if mediaType == "movie" {
			key = fmt.Sprintf("%s\x00%d", key, it.MediaFileID)
		}
		g, ok := byKey[key]
		if !ok {
			g = &group{title: key}
			byKey[key] = g
		}
		g.items = append(g.items, it)
	}

	groups := make([]*group, 0, len(byKey))
	for _, g := range byKey {
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].title < groups[j].title })

	series := make([]contentSeries, 0, len(groups))
	for _, g := range groups {
		sort.Slice(g.items, func(i, j int) bool {
			if g.items[i].SeasonNumber != g.items[j].SeasonNumber {
				return g.items[i].SeasonNumber < g.items[j].SeasonNumber
			}
			if g.items[i].EpisodeNumber != g.items[j].EpisodeNumber {
				return g.items[i].EpisodeNumber < g.items[j].EpisodeNumber
			}
			return g.items[i].MediaFileID < g.items[j].MediaFileID
		})
		items := make([]candidate, len(g.items))
		for i, it := range g.items {
			items[i] = candidate{MediaFileID: it.MediaFileID, DurationSeconds: it.DurationSeconds}
		}
		series = append(series, contentSeries{Items: items})
	}
	return series
}

// splitSeries alternates series between two channels so each gets a mix
// across the alphabet.
func splitSeries(series []contentSeries) [2][]contentSeries {
	var out [2][]contentSeries
	for i, s := range series {
		out[i%2] = append(out[i%2], s)
	}
	return out
}

// saveDemoChannel creates the demo channel if missing (visible by
// default) and replaces its lineup. An existing channel's name and
// hidden flag are left as the admin set them.
func saveDemoChannel(ctx context.Context, db *sql.DB, spec demoChannelSpec, mediaFileIDs []int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("lineup: demo channel %d: %w", spec.Number, err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO channels (number, name) VALUES (?, ?)
		ON CONFLICT (number) DO NOTHING`, spec.Number, spec.Name); err != nil {
		return fmt.Errorf("lineup: demo channel %d: create: %w", spec.Number, err)
	}
	var channelID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM channels WHERE number = ?`, spec.Number).Scan(&channelID); err != nil {
		return fmt.Errorf("lineup: demo channel %d: %w", spec.Number, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM channel_items WHERE channel_id = ?`, channelID); err != nil {
		return fmt.Errorf("lineup: demo channel %d: clear: %w", spec.Number, err)
	}
	for position, mediaFileID := range mediaFileIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO channel_items (channel_id, media_file_id, position) VALUES (?, ?, ?)`,
			channelID, mediaFileID, position); err != nil {
			return fmt.Errorf("lineup: demo channel %d: save: %w", spec.Number, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("lineup: demo channel %d: %w", spec.Number, err)
	}
	return nil
}
