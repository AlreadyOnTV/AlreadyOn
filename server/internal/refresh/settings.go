package refresh

import (
	"context"
	"database/sql"

	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/lineup"
)

// SettingsWork describes derived work invalidated by a saved preference. Nil
// Channels means all channels; an empty nonnil slice only invalidates old work.
type SettingsWork struct {
	Changed  bool
	Channels []int64
	Rebuild  bool
	Metadata bool
}

// MutateSettings commits preferences and their durable work in one transaction.
// Save callbacks do no scanning, classification, lineup or schedule generation.
func MutateSettings(ctx context.Context, db *sql.DB, save func(*sql.Tx) (SettingsWork, error)) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	work, err := save(tx)
	if err != nil {
		return err
	}
	if work.Changed {
		ids := work.Channels
		if ids == nil {
			// Shared filter/provider saves retain full refresh's initialization
			// of missing built-in defaults without replacing saved custom rules.
			if work.Rebuild {
				if _, err := lineup.SeedDefaultFilters(ctx, tx); err != nil {
					return err
				}
			}
			channels, err := channel.List(ctx, tx)
			if err != nil {
				return err
			}
			ids = make([]int64, 0, len(channels))
			for _, c := range channels {
				ids = append(ids, c.ID)
			}
		}
		if err = queueChannels(ctx, tx, ids, work.Rebuild); err != nil {
			return err
		}
		if work.Metadata {
			if _, err = tx.ExecContext(ctx, `UPDATE library_update_state SET needs_metadata=1,scan_revision=scan_revision+1 WHERE id=1`); err != nil {
				return err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if work.Changed {
		NotifyUpdate(db, work.Metadata)
	}
	return nil
}
