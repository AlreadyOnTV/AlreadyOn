package lineup

import (
	"context"
	"database/sql"
	"github.com/alreadyonapp/alreadyon/server/internal/metadata"
	"github.com/alreadyonapp/alreadyon/server/internal/workrevision"
)

// Every provider configuration shares the audience-focused defaults. Legacy
// free mixes retain their schedules but no longer appear as a second catalog.
func syncProviderChannels(ctx context.Context, db *sql.DB) error {
	return clearInactiveProviderChannels(ctx, db, metadata.Settings{})
}
func clearInactiveProviderChannels(ctx context.Context, db *sql.DB, _ metadata.Settings) error {
	_, err := workrevision.Exec(ctx, db, `UPDATE channels SET hidden=1 WHERE number BETWEEN 180 AND 191`)
	return err
}

// SyncProviderVisibility retains the full-refresh visibility policy after a coalesced scan.
func SyncProviderVisibility(ctx context.Context, db *sql.DB) error {
	return syncProviderChannels(ctx, db)
}
