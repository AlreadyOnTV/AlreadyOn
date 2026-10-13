package lineup

import (
	"context"
	"database/sql"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/metadata"
)

// bestEffortChannels reserve twelve broad themes instead of reusing the richer
// TMDb catalog's names. Library Mix includes unknown identities without guessing
// their type, date, genre or age rating. Empty themes remain hidden.
var bestEffortChannels = []struct {
	number int
	name   string
	filter channel.Filter
}{
	{180, "Free: Library Mix", channel.Filter{AudienceTier: "adult", MediaType: "both", Era: "both"}},
	{181, "Free: TV Mix", channel.Filter{AudienceTier: "adult", MediaType: "tv", Era: "both"}},
	{182, "Free: Movie Mix", channel.Filter{AudienceTier: "adult", MediaType: "movie", Era: "both"}},
	{183, "Free: Nostalgia TV", channel.Filter{AudienceTier: "adult", MediaType: "tv", Era: "nostalgia"}},
	{184, "Free: Modern TV", channel.Filter{AudienceTier: "adult", MediaType: "tv", Era: "modern"}},
	{185, "Free: Nostalgia Movies", channel.Filter{AudienceTier: "adult", MediaType: "movie", Era: "nostalgia"}},
	{186, "Free: Modern Movies", channel.Filter{AudienceTier: "adult", MediaType: "movie", Era: "modern"}},
	{187, "Free: Comedy TV", channel.Filter{AudienceTier: "adult", MediaType: "tv", Era: "both", Genres: []string{"comedy"}}},
	{188, "Free: Drama TV", channel.Filter{AudienceTier: "adult", MediaType: "tv", Era: "both", Genres: []string{"drama", "crime"}}},
	{189, "Free: Animation TV", channel.Filter{AudienceTier: "adult", MediaType: "tv", Era: "both", Genres: []string{"animation", "anime"}}},
	{190, "Free: Action & Adventure Movies", channel.Filter{AudienceTier: "adult", MediaType: "movie", Era: "both", Genres: []string{"action", "adventure", "scifi", "fantasy"}}},
	{191, "Free: Comedy Movies", channel.Filter{AudienceTier: "adult", MediaType: "movie", Era: "both", Genres: []string{"comedy"}}},
}

// syncProviderChannels selects the automatic catalog from settings. It never
// deletes the richer defaults or their saved filters: switching back to TMDb
// can repopulate them. Custom channels (200+) remain independent of mode.
func syncProviderChannels(ctx context.Context, db *sql.DB) error {
	settings, err := metadata.GetSettings(ctx, db)
	if err != nil {
		return err
	}
	if err := clearInactiveProviderChannels(ctx, db, settings); err != nil {
		return err
	}
	if settings.TMDbAPIKey != "" || !settings.KeylessEnabled {
		return nil
	}
	pool, err := loadBestEffortPool(ctx, db)
	if err != nil {
		return err
	}
	for _, spec := range bestEffortChannels {
		if _, err := db.ExecContext(ctx, `INSERT INTO channels (number, name) VALUES (?, ?) ON CONFLICT(number) DO NOTHING`, spec.number, spec.name); err != nil {
			return err
		}
		var id int64
		if err := db.QueryRowContext(ctx, `SELECT id FROM channels WHERE number = ?`, spec.number).Scan(&id); err != nil {
			return err
		}
		f, err := channel.GetFilter(ctx, db, id)
		if err != nil {
			return err
		}
		if f == nil {
			saved, err := channel.SetFilter(ctx, db, id, spec.filter)
			if err != nil {
				return err
			}
			f = &saved
		}
		resolved, err := channel.Resolve(ctx, db, *f)
		if err != nil {
			return err
		}
		// Saved filters remain editable. Strict dates apply to era-specific free
		// themes, while the default Library Mix imposes no classification gate.
		requiresDate := resolved.Era != "both"
		if len(resolved.EraOptions) > 0 {
			requiresDate = true
			for _, era := range resolved.EraOptions {
				// Resolve always includes the All option for an unrestricted
				// era. Its presence must not exclude unknown dates from mixes.
				if era.StartYear == 0 && era.EndYear == 0 {
					requiresDate = false
				}
			}
		}
		series := seriesWhere(pool, func(it classifiedItem) bool {
			if requiresDate && it.ReleaseYear == 0 {
				return false
			}
			return filterMatches(resolved, it)
		})
		// Keep the entire pool, rather than stopping after the old demo's 14
		// hours. The bounded scheduler rotates all usable discovered files.
		if err := replaceChannelItems(ctx, db, id, buildLineup(series, math.Inf(1))); err != nil {
			return err
		}
	}
	// Auto visibility normally counts identified titles. A raw unknown file is
	// a playable title too (channel.SeriesCounts), never a child-safe title.
	_, _, err = channel.ApplyAutoVisibility(ctx, db)
	return err
}

// clearInactiveProviderChannels disables only automatic catalogs. Rich filters
// and visibility choices survive mode changes; the next TMDb refresh restores
// their effective visibility. Free/demo catalogs are transient reserved rows.
func clearInactiveProviderChannels(ctx context.Context, db *sql.DB, settings metadata.Settings) error {
	if settings.TMDbAPIKey == "" {
		if _, err := db.ExecContext(ctx, `DELETE FROM channel_items WHERE channel_id IN (SELECT id FROM channels WHERE number BETWEEN 101 AND 136)`); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, `UPDATE channels SET hidden = 1 WHERE number BETWEEN 101 AND 136`); err != nil {
			return err
		}
	}
	if settings.TMDbAPIKey != "" || !settings.KeylessEnabled {
		if _, err := db.ExecContext(ctx, `DELETE FROM channel_items WHERE channel_id IN (SELECT id FROM channels WHERE number BETWEEN 180 AND 191)`); err != nil {
			return err
		}
		_, err := db.ExecContext(ctx, `UPDATE channels SET hidden = 1 WHERE number BETWEEN 180 AND 191`)
		return err
	}
	return nil
}

// loadBestEffortPool includes healthy unidentified files without fabricating
// identified_items rows. Guide/playback already fall back to a filename title.
func loadBestEffortPool(ctx context.Context, db *sql.DB) ([]classifiedItem, error) {
	pool, err := loadPool(ctx, db, false)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT media_file_id, keyless_release_date FROM identified_items WHERE keyless_release_date IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	dates := map[int64]int{}
	for rows.Next() {
		var id int64
		var date string
		if err := rows.Scan(&id, &date); err != nil {
			rows.Close()
			return nil, err
		}
		if len(date) >= 4 {
			dates[id], _ = strconv.Atoi(date[:4])
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for i := range pool {
		if pool[i].ReleaseYear == 0 {
			pool[i].ReleaseYear = dates[pool[i].MediaFileID]
		}
	}
	rows, err = db.QueryContext(ctx, `SELECT mf.id, mf.path, mf.duration_seconds FROM media_files mf LEFT JOIN identified_items ii ON ii.media_file_id = mf.id WHERE ii.id IS NULL AND mf.root_kind = 'media' AND mf.status = 'ok' AND mf.duration_seconds > 0 ORDER BY mf.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var it classifiedItem
		if err := rows.Scan(&it.MediaFileID, &it.Path, &it.DurationSeconds); err != nil {
			return nil, err
		}
		it.Title = strings.TrimSuffix(filepath.Base(it.Path), filepath.Ext(it.Path))
		pool = append(pool, it)
	}
	return pool, rows.Err()
}
