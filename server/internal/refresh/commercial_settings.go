package refresh

import (
	"context"
	"database/sql"

	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/commercial"
)

// SaveCommercialSettings commits preferences and pending schedule work together.
// Break settings reuse existing lineups without scanning or classifying media;
// volume-only edits take effect on new streams without any schedule work.
func SaveCommercialSettings(ctx context.Context, db *sql.DB, next commercial.Settings) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	previous, err := commercial.GetSettings(ctx, tx)
	if err != nil {
		return err
	}
	if err = commercial.SaveSettings(ctx, tx, next); err != nil {
		return err
	}
	previous.VolumeOffsetDB = next.VolumeOffsetDB
	changed := previous != next
	if changed {
		channels, err := channel.List(ctx, tx)
		if err != nil {
			return err
		}
		ids := make([]int64, 0, len(channels))
		for _, ch := range channels {
			ids = append(ids, ch.ID)
		}
		if err = queueChannels(ctx, tx, ids, false); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if changed {
		NotifyUpdate(db, false)
	}
	return nil
}
