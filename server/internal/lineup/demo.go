package lineup

import (
	"context"
	"database/sql"
)

// IsDemoChannelNumber identifies the reserved legacy demonstration channels.
func IsDemoChannelNumber(n int) bool { return n >= 196 && n <= 199 }

// SyncDemoChannels retires broad fallback mixes now that free metadata is
// automatic. Empty audience-specific channels stay empty instead of accepting
// unrelated titles. Keep legacy rows/airings intact for an in-flight playback.
func SyncDemoChannels(ctx context.Context, db *sql.DB) (int, error) {
	_, err := db.ExecContext(ctx, `UPDATE channels SET hidden=1 WHERE number BETWEEN 196 AND 199`)
	return 0, err
}
