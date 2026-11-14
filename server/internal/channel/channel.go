// Package channel implements channel definitions and content ordering.
// See docs/PROJECT_BRIEF.md Epic 3.
package channel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/alreadyonapp/alreadyon/server/internal/storage"
	"strings"
)

// Channel is a user-defined lineup of library content: a channel number
// (what a viewer tunes to, like a real cable channel), a display name,
// and its content selection rules.
type Channel struct {
	ID     int64
	Number int
	Name   string
	// Visibility is the admin's choice: auto (shown while it has content),
	// show (always) or hide (never). See SetVisibility.
	Visibility string
	// Hidden is the effective result: it excludes this channel from the guide
	// (GET /api/guide). It follows Visibility (see ApplyAutoVisibility).
	// Channel management (this package's own API) always sees every
	// channel regardless of Hidden.
	Hidden bool
}

// Draft is the input to Create/Update: a Channel without its
// server-assigned ID.
type Draft struct {
	Number int
	Name   string
}

// DefaultChannelRangeStart and DefaultChannelRangeEnd bound the channel
// numbers reserved for AlreadyOn's built-in default channels (seeded by
// migration 0007). Custom channels must use a number outside this
// range — 200+ is the recommended convention (docs/PROJECT_BRIEF.md).
const (
	DefaultChannelRangeStart = 100
	DefaultChannelRangeEnd   = 199
)

var (
	ErrNotFound        = errors.New("channel: not found")
	ErrInvalidName     = errors.New("channel: name is required")
	ErrInvalidNumber   = errors.New("channel: number must be positive")
	ErrDuplicateNumber = errors.New("channel: number already in use")
	ErrReservedNumber  = fmt.Errorf("channel: numbers %d-%d are reserved for built-in default channels", DefaultChannelRangeStart, DefaultChannelRangeEnd)
)

func (d Draft) validate() error {
	if strings.TrimSpace(d.Name) == "" {
		return ErrInvalidName
	}
	if d.Number <= 0 {
		return ErrInvalidNumber
	}
	return nil
}

func isReservedNumber(n int) bool {
	return n >= DefaultChannelRangeStart && n <= DefaultChannelRangeEnd
}

// IsBuiltInNumber reports whether n is in the range reserved for the
// built-in default channels.
func IsBuiltInNumber(n int) bool { return isReservedNumber(n) }

// Create adds a new channel. Returns ErrReservedNumber if draft.Number
// falls in the built-in default range, or ErrDuplicateNumber if it's
// already in use by another channel.
func Create(ctx context.Context, db *sql.DB, draft Draft) (Channel, error) {
	if err := draft.validate(); err != nil {
		return Channel{}, err
	}
	if isReservedNumber(draft.Number) {
		return Channel{}, ErrReservedNumber
	}

	res, err := db.ExecContext(ctx, `
		INSERT INTO channels (number, name) VALUES (?, ?)`,
		draft.Number, strings.TrimSpace(draft.Name))
	if err != nil {
		if isUniqueConstraintError(err) {
			return Channel{}, ErrDuplicateNumber
		}
		return Channel{}, fmt.Errorf("channel: create: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Channel{}, fmt.Errorf("channel: create: %w", err)
	}
	return Get(ctx, db, id)
}

// Get returns the channel with the given id, or ErrNotFound.
func Get(ctx context.Context, db storage.Querier, id int64) (Channel, error) {
	var c Channel
	err := db.QueryRowContext(ctx, `SELECT id, number, name, hidden, visibility FROM channels WHERE id = ?`, id).
		Scan(&c.ID, &c.Number, &c.Name, &c.Hidden, &c.Visibility)
	if errors.Is(err, sql.ErrNoRows) {
		return Channel{}, ErrNotFound
	}
	if err != nil {
		return Channel{}, fmt.Errorf("channel: get %d: %w", id, err)
	}
	return c, nil
}

// List returns every channel, ordered by channel number.
func List(ctx context.Context, db storage.Querier) ([]Channel, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, number, name, hidden, visibility FROM channels ORDER BY number`)
	if err != nil {
		return nil, fmt.Errorf("channel: list: %w", err)
	}
	defer rows.Close()

	var channels []Channel
	for rows.Next() {
		var c Channel
		if err := rows.Scan(&c.ID, &c.Number, &c.Name, &c.Hidden, &c.Visibility); err != nil {
			return nil, fmt.Errorf("channel: list: %w", err)
		}
		channels = append(channels, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channel: list: %w", err)
	}
	return channels, nil
}

// Update replaces id's number/name with draft's. Returns
// ErrNotFound if id doesn't exist, ErrReservedNumber if draft.Number
// would move it into the built-in default range (a channel that's
// already there — one of the seeded defaults — can keep its own
// existing number; the check only blocks assigning a *new* one there),
// or ErrDuplicateNumber if draft.Number is already in use by a
// different channel.
func Update(ctx context.Context, db *sql.DB, id int64, draft Draft) (Channel, error) {
	if err := draft.validate(); err != nil {
		return Channel{}, err
	}

	if isReservedNumber(draft.Number) {
		current, err := Get(ctx, db, id)
		if err != nil {
			return Channel{}, err
		}
		if current.Number != draft.Number {
			return Channel{}, ErrReservedNumber
		}
	}

	res, err := db.ExecContext(ctx, `
		UPDATE channels SET number = ?, name = ?, updated_at_unix = unixepoch()
		WHERE id = ?`,
		draft.Number, strings.TrimSpace(draft.Name), id)
	if err != nil {
		if isUniqueConstraintError(err) {
			return Channel{}, ErrDuplicateNumber
		}
		return Channel{}, fmt.Errorf("channel: update %d: %w", id, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return Channel{}, fmt.Errorf("channel: update %d: %w", id, err)
	}
	if rows == 0 {
		return Channel{}, ErrNotFound
	}
	return Get(ctx, db, id)
}

// Delete removes the channel with the given id. Returns ErrNotFound if
// it doesn't exist.
func Delete(ctx context.Context, db *sql.DB, id int64) error {
	res, err := db.ExecContext(ctx, `DELETE FROM channels WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("channel: delete %d: %w", id, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("channel: delete %d: %w", id, err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// SetHidden sets Hidden for every channel in ids (bulk-capable — a
// single-id slice works just as well for a per-row toggle). Unknown ids
// are silently ignored, matching a bulk action's "best effort" nature
// rather than failing the whole batch over one stale id.
func SetHidden(ctx context.Context, db *sql.DB, ids []int64, hidden bool) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+1)
	args = append(args, hidden)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	query := fmt.Sprintf(`UPDATE channels SET hidden = ?, updated_at_unix = unixepoch() WHERE id IN (%s)`,
		strings.Join(placeholders, ","))
	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("channel: set hidden: %w", err)
	}
	return nil
}

// SeriesCounts returns, for every channel that has any content at all,
// how many distinct shows/movies are in its lineup — all of a show's
// episodes count as that one show, not one each (see #90). A channel
// with zero channel_items rows simply doesn't appear in the result
// (equivalent to a count of 0).
func SeriesCounts(ctx context.Context, db *sql.DB) (map[int64]int, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT ci.channel_id,
		       COUNT(DISTINCT CASE WHEN ii.media_type = 'episode' THEN COALESCE(NULLIF(ii.series_title, ''), ii.title, 'file:' || ci.media_file_id) ELSE 'file:' || ci.media_file_id END)
		FROM channel_items ci
		LEFT JOIN identified_items ii ON ii.media_file_id = ci.media_file_id
		GROUP BY ci.channel_id`)
	if err != nil {
		return nil, fmt.Errorf("channel: series counts: %w", err)
	}
	defer rows.Close()

	counts := make(map[int64]int)
	for rows.Next() {
		var channelID int64
		var count int
		if err := rows.Scan(&channelID, &count); err != nil {
			return nil, fmt.Errorf("channel: series counts: %w", err)
		}
		counts[channelID] = count
	}
	return counts, rows.Err()
}

// SeriesCount is SeriesCounts for a single channel — 0 if it has no
// content at all, without needing every other channel's count too.
func SeriesCount(ctx context.Context, db *sql.DB, channelID int64) (int, error) {
	var count int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT CASE WHEN ii.media_type = 'episode' THEN COALESCE(NULLIF(ii.series_title, ''), ii.title, 'file:' || ci.media_file_id) ELSE 'file:' || ci.media_file_id END)
		FROM channel_items ci
		LEFT JOIN identified_items ii ON ii.media_file_id = ci.media_file_id
		WHERE ci.channel_id = ?`, channelID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("channel: series count %d: %w", channelID, err)
	}
	return count, nil
}

// isUniqueConstraintError reports whether err came from violating a
// UNIQUE constraint (channels.number). modernc.org/sqlite doesn't export
// a typed error to check against, so this matches on the driver's own
// message text — verified against the actual error strings it returns
// in the accompanying tests.
func isUniqueConstraintError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint")
}
