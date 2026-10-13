// Package schedule generates and persists deterministic per-channel
// timelines, including commercial breaks. See docs/PROJECT_BRIEF.md
// Epic 3.
package schedule

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/activity"
	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/commercial"
	"github.com/alreadyonapp/alreadyon/server/internal/governor"
	"github.com/alreadyonapp/alreadyon/server/internal/refreshlog"
)

// Entry is one scheduled play-through of a channel's lineup item, or an
// inserted commercial break — see Kind.
// Timestamps are always in UTC.
type Entry struct {
	ChannelID   int64
	MediaFileID int64
	Kind        Kind
	StartsAt    time.Time
	EndsAt      time.Time
	// SourceOffsetSeconds is where within MediaFileID's own file this
	// entry starts — 0 unless ModeInterval (see commercial.Settings)
	// split a single program into multiple segments around one or more
	// mid-program commercial breaks; a future player seeks here rather
	// than always starting the file from its own beginning.
	SourceOffsetSeconds float64
}

// ErrEmptyLineup is returned when a channel has no playable content —
// either no lineup items at all, or none with a known, positive
// duration (a file that hasn't been probed yet, or is marked
// corrupt/unreadable, can't occupy time in a timeline) — or, after
// Regenerate has run at least once, simply no schedule_entries rows
// covering the requested time at all.
var ErrEmptyLineup = errors.New("schedule: channel has no playable lineup items")

// TrailingWindow/LeadingWindow bound how much of a channel's schedule
// Regenerate keeps committed at any time: from this far in the past
// (so a guide reload always finds the viewer's current moment already
// covered, and so "what just aired" stays queryable for a little while)
// through this far in the future (so the guide can show a long look-
// ahead without regenerating on every request). See Regenerate.
const (
	TrailingWindow = 1 * time.Hour
	LeadingWindow  = 48 * time.Hour
)

type lineupItem struct {
	MediaFileID     int64
	DurationSeconds float64
	Kind            Kind
	MediaType       string  // "movie" or "episode" — decides which of commercial.Settings' two per-break counts/buffers applies; irrelevant (left "") for a commercial pool item
	SourceOffset    float64 // where within MediaFileID this item starts; see Entry.SourceOffsetSeconds
}

// loadExpandedLineup loads channelID's own program lineup and, if
// commercial breaks are enabled and a commercial pool exists, interleaves
// them per the configured settings (see expand) — one full pass through
// the lineup, which Regenerate then lays out repeatedly (looped) across
// real wall-clock time.
func loadExpandedLineup(ctx context.Context, db *sql.DB, channelID int64) ([]lineupItem, error) {
	programs, err := loadLineup(ctx, db, channelID)
	if err != nil {
		return nil, err
	}

	settings, err := channelCommercialSettings(ctx, db, channelID)
	if err != nil {
		return nil, fmt.Errorf("schedule: load commercial settings: %w", err)
	}

	var pool []lineupItem
	if settings.Enabled {
		pool, err = loadChannelCommercialPool(ctx, db, channelID)
		if err != nil {
			return nil, err
		}
	}

	return expand(programs, pool, settings), nil
}

// Current returns the entry playing on channelID at clock's current
// time, and how far into it playback is — a straightforward query
// against whatever Regenerate has already committed; see that function
// for how the persisted schedule is kept up to date.
func Current(ctx context.Context, db *sql.DB, clock Clock, channelID int64) (Entry, time.Duration, error) {
	now := clock.Now().UTC()
	e, err := queryOne(ctx, db, `
		SELECT channel_id, media_file_id, kind, source_offset_seconds, duration_seconds, starts_at_unix, ends_at_unix
		FROM schedule_entries
		WHERE channel_id = ? AND starts_at_unix <= ? AND ends_at_unix > ?
		ORDER BY starts_at_unix DESC LIMIT 1`, channelID, now.Unix(), now.Unix())
	if err != nil {
		return Entry{}, 0, err
	}
	return e, now.Sub(e.StartsAt), nil
}

// Upcoming returns up to limit entries in chronological order, starting
// with whatever is currently playing on channelID. Upcoming(...)[0]
// always matches what Current returns. Never returns more than what's
// actually committed — see Regenerate's LeadingWindow — so a very large
// limit against a channel whose schedule hasn't been regenerated in a
// while may return fewer than limit entries without that being an error.
func Upcoming(ctx context.Context, db *sql.DB, clock Clock, channelID int64, limit int) ([]Entry, error) {
	if limit <= 0 {
		return nil, nil
	}
	now := clock.Now().UTC()

	rows, err := db.QueryContext(ctx, `
		SELECT channel_id, media_file_id, kind, source_offset_seconds, duration_seconds, starts_at_unix, ends_at_unix
		FROM schedule_entries
		WHERE channel_id = ? AND ends_at_unix > ?
		ORDER BY starts_at_unix ASC LIMIT ?`, channelID, now.Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("schedule: upcoming for channel %d: %w", channelID, err)
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("schedule: upcoming for channel %d: %w", channelID, err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("schedule: upcoming for channel %d: %w", channelID, err)
	}
	if len(entries) == 0 {
		return nil, ErrEmptyLineup
	}
	return entries, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanEntry(row rowScanner) (Entry, error) {
	var e Entry
	var kind string
	var duration float64
	var startsAtUnix, endsAtUnix int64
	if err := row.Scan(&e.ChannelID, &e.MediaFileID, &kind, &e.SourceOffsetSeconds, &duration, &startsAtUnix, &endsAtUnix); err != nil {
		return Entry{}, err
	}
	e.Kind = Kind(kind)
	e.StartsAt = time.Unix(startsAtUnix, 0).UTC()
	e.EndsAt = time.Unix(endsAtUnix, 0).UTC()
	return e, nil
}

func queryOne(ctx context.Context, db *sql.DB, query string, args ...any) (Entry, error) {
	e, err := scanEntry(db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, ErrEmptyLineup
	}
	if err != nil {
		return Entry{}, fmt.Errorf("schedule: query: %w", err)
	}
	return e, nil
}

// RegenerateAll calls Regenerate for every channel (built-in default or
// custom) — the "commercial settings changed" / "new content detected"
// / "Refresh Schedule button clicked" entry point (see
// internal/refresh.Run, which every one of those actually goes
// through). Returns how many channels were (re)generated.
func RegenerateAll(ctx context.Context, db *sql.DB, now time.Time) (int, error) {
	channels, err := channel.List(ctx, db)
	if err != nil {
		return 0, fmt.Errorf("schedule: regenerate all: %w", err)
	}
	task, finish := activity.Default.Start("Channel Schedule Maintenance", "channels", len(channels))
	defer finish()
	regenerated := 0
	for _, c := range channels {
		if err := governor.Checkpoint(ctx); err != nil {
			return regenerated, err
		}
		activity.Default.Progress(task, regenerated, len(channels))
		if err := Regenerate(ctx, db, c.ID, now); err != nil {
			return regenerated, fmt.Errorf("schedule: regenerate channel %d: %w", c.ID, err)
		}
		regenerated++
	}
	return regenerated, nil
}

// Regenerate brings channelID's persisted schedule_entries up to date
// as of now: playable entries already airing or in the past are never touched —
// specifically, whatever entry is currently playing (starts_at <= now <
// ends_at) is kept exactly as committed, so a viewer partway through it
// never sees it change — but everything from the moment that entry ends
// onward is deleted and laid out fresh from channelID's current lineup
// and commercial settings (looped as many times as needed), through
// LeadingWindow ahead. Anything older than TrailingWindow behind now is
// pruned. Idempotent and safe to call often (a periodic maintenance
// ticker in main.go does, purely to keep the leading/trailing edges
// sliding forward with time) — regenerating twice in a row with nothing
// else changed reproduces the same future, just recomputed.
func Regenerate(ctx context.Context, db *sql.DB, channelID int64, now time.Time) error {
	if err := regenerateChannel(ctx, db, channelID, now); err != nil {
		return err
	}
	// Only the "last updated" note depends on this; never fail a schedule over it.
	_ = refreshlog.Record(ctx, db, refreshlog.Schedule, time.Now())
	return nil
}

func regenerateChannel(ctx context.Context, db *sql.DB, channelID int64, now time.Time) error {
	now = now.UTC()
	// A missing source must not be retained as the currently airing program or ad.
	if _, err := db.ExecContext(ctx, `DELETE FROM schedule_entries
 WHERE channel_id=? AND ends_at_unix>? AND NOT EXISTS
 (SELECT 1 FROM media_files WHERE id=schedule_entries.media_file_id AND status='ok')`, channelID, now.Unix()); err != nil {
		return fmt.Errorf("schedule: remove unavailable sources: %w", err)
	}
	rotation, err := loadRotation(ctx, db, channelID)
	if err != nil {
		return err
	}
	if len(rotation) > 0 {
		return regenerateRotation(ctx, db, channelID, now, rotation)
	}
	windowStart := now.Add(-TrailingWindow)
	windowEnd := now.Add(LeadingWindow)

	if _, err := db.ExecContext(ctx, `DELETE FROM schedule_entries WHERE channel_id = ? AND ends_at_unix <= ?`,
		channelID, windowStart.Unix()); err != nil {
		return fmt.Errorf("schedule: regenerate channel %d: prune: %w", channelID, err)
	}

	sequence, err := loadExpandedLineup(ctx, db, channelID)
	if err != nil {
		return err
	}
	total := totalDuration(sequence)
	if total <= 0 {
		// Nothing playable — clear anything stale rather than leaving
		// old entries behind for content that no longer exists.
		var filtered int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM channel_filters WHERE channel_id=?`, channelID).Scan(&filtered); err != nil {
			return err
		}
		query := `DELETE FROM schedule_entries WHERE channel_id = ?`
		args := []any{channelID}
		if filtered > 0 {
			query += ` AND (starts_at_unix > ? OR ends_at_unix <= ?)`
			args = append(args, now.Unix(), windowStart.Unix())
		}
		_, err := db.ExecContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("schedule: regenerate channel %d: clear: %w", channelID, err)
		}
		return nil
	}

	genFrom, err := regenerateFromPoint(ctx, db, channelID, now, windowStart)
	if err != nil {
		return fmt.Errorf("schedule: regenerate channel %d: %w", channelID, err)
	}

	ads, err := loadChannelCommercialPool(ctx, db, channelID)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("schedule: regenerate channel %d: %w", channelID, err)
	}
	defer tx.Rollback()

	// Manual lineups repeat their program sequence, but commercials must keep
	// walking the whole library across loops and refreshes. Resume after the
	// last retained commercial rather than restarting at the pool's first item.
	adIndex := 0
	var lastAd int64
	err = tx.QueryRowContext(ctx, `SELECT media_file_id FROM schedule_entries
 WHERE channel_id=? AND kind='commercial' AND starts_at_unix<?
 ORDER BY starts_at_unix DESC LIMIT 1`, channelID, genFrom.Unix()).Scan(&lastAd)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	for i, ad := range ads {
		if ad.MediaFileID == lastAd {
			adIndex = (i + 1) % len(ads)
			break
		}
	}
	t := genFrom
	idx := 0
	for t.Before(windowEnd) {
		item := sequence[idx%len(sequence)]
		if item.Kind == KindCommercial && len(ads) > 0 {
			ad := ads[adIndex%len(ads)]
			adIndex++
			item.MediaFileID, item.DurationSeconds = ad.MediaFileID, ad.DurationSeconds
		}
		idx++
		endsAt := t.Add(durationFromSeconds(item.DurationSeconds))
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO schedule_entries (channel_id, media_file_id, kind, source_offset_seconds, duration_seconds, starts_at_unix, ends_at_unix)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			channelID, item.MediaFileID, string(item.Kind), item.SourceOffset, item.DurationSeconds, t.Unix(), endsAt.Unix()); err != nil {
			return fmt.Errorf("schedule: regenerate channel %d: insert: %w", channelID, err)
		}
		t = endsAt
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("schedule: regenerate channel %d: %w", channelID, err)
	}
	return nil
}

// regenerateFromPoint deletes whatever's not yet started (or, if
// nothing at all currently covers `now`, everything) and returns the
// wall-clock point new entries should be laid out from — either right
// after the currently-playing entry ends, or windowStart if there isn't
// one.
func regenerateFromPoint(ctx context.Context, db *sql.DB, channelID int64, now, windowStart time.Time) (time.Time, error) {
	var currentEndsAtUnix int64
	err := db.QueryRowContext(ctx, `
		SELECT ends_at_unix FROM schedule_entries
		WHERE channel_id = ? AND starts_at_unix <= ? AND ends_at_unix > ?
		ORDER BY starts_at_unix DESC LIMIT 1`, channelID, now.Unix(), now.Unix()).Scan(&currentEndsAtUnix)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := db.ExecContext(ctx, `DELETE FROM schedule_entries WHERE channel_id = ?`, channelID); err != nil {
			return time.Time{}, fmt.Errorf("clear: %w", err)
		}
		return windowStart, nil
	case err != nil:
		return time.Time{}, fmt.Errorf("find current entry: %w", err)
	default:
		genFrom := time.Unix(currentEndsAtUnix, 0).UTC()
		if _, err := db.ExecContext(ctx, `DELETE FROM schedule_entries WHERE channel_id = ? AND starts_at_unix >= ?`,
			channelID, genFrom.Unix()); err != nil {
			return time.Time{}, fmt.Errorf("clear future: %w", err)
		}
		return genFrom, nil
	}
}

// totalDuration sums every item's duration.
func totalDuration(items []lineupItem) float64 {
	var total float64
	for _, it := range items {
		total += it.DurationSeconds
	}
	return total
}

func durationFromSeconds(s float64) time.Duration {
	return time.Duration(s * float64(time.Second))
}

func loadLineup(ctx context.Context, db *sql.DB, channelID int64) ([]lineupItem, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT ci.media_file_id, mf.duration_seconds, COALESCE(ii.media_type, '')
		FROM channel_items ci
		JOIN media_files mf ON mf.id = ci.media_file_id
		LEFT JOIN identified_items ii ON ii.media_file_id = ci.media_file_id
		WHERE ci.channel_id = ? AND mf.status = 'ok'
		ORDER BY ci.position`, channelID)
	if err != nil {
		return nil, fmt.Errorf("schedule: load lineup for channel %d: %w", channelID, err)
	}
	defer rows.Close()

	var items []lineupItem
	for rows.Next() {
		var mediaFileID int64
		var duration sql.NullFloat64
		var mediaType string
		if err := rows.Scan(&mediaFileID, &duration, &mediaType); err != nil {
			return nil, fmt.Errorf("schedule: load lineup for channel %d: %w", channelID, err)
		}
		if !duration.Valid || duration.Float64 <= 0 {
			continue // not yet probed, or invalid — can't occupy time in the timeline
		}
		items = append(items, lineupItem{MediaFileID: mediaFileID, DurationSeconds: duration.Float64, MediaType: mediaType})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("schedule: load lineup for channel %d: %w", channelID, err)
	}
	return items, nil
}

// loadCommercialPool returns every healthy, probed commercial file,
// ordered by id — a stable order is what makes expand's round-robin
// selection deterministic across calls.
func loadCommercialPool(ctx context.Context, db *sql.DB) ([]lineupItem, error) {
	return queryCommercialPool(ctx, db, commercial.ChannelPolicy{})
}

// Channel-specific filtering is shared by manual loops and rotating lineups.
func loadChannelCommercialPool(ctx context.Context, db *sql.DB, id int64) ([]lineupItem, error) {
	policy, err := commercial.GetChannelPolicy(ctx, db, id)
	if err != nil {
		return nil, err
	}
	return queryCommercialPool(ctx, db, policy)
}

func channelCommercialSettings(ctx context.Context, db *sql.DB, id int64) (commercial.Settings, error) {
	settings, err := commercial.GetSettings(ctx, db)
	if err != nil {
		return settings, err
	}
	policy, err := commercial.GetChannelPolicy(ctx, db, id)
	settings.Enabled = policy.EffectiveEnabled
	return settings, err
}

func queryCommercialPool(ctx context.Context, db *sql.DB, policy commercial.ChannelPolicy) ([]lineupItem, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, duration_seconds, path FROM media_files
		WHERE root_kind = 'commercial' AND status = 'ok' AND duration_seconds > 0
		ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("schedule: load commercial pool: %w", err)
	}
	defer rows.Close()

	var items []lineupItem
	for rows.Next() {
		var mediaFileID int64
		var duration float64
		var path string
		if err := rows.Scan(&mediaFileID, &duration, &path); err != nil {
			return nil, fmt.Errorf("schedule: load commercial pool: %w", err)
		}
		if policy.IncludesPath(path) {
			items = append(items, lineupItem{MediaFileID: mediaFileID, DurationSeconds: duration})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("schedule: load commercial pool: %w", err)
	}
	return items, nil
}
