package refresh

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/lineup"
	"github.com/alreadyonapp/alreadyon/server/internal/logging"
	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
	"github.com/alreadyonapp/alreadyon/server/internal/status"
	"github.com/alreadyonapp/alreadyon/server/internal/storage"
	"github.com/alreadyonapp/alreadyon/server/internal/workrevision"
)

// UpdateStatus separates durable saved preferences from their background application.
type UpdateStatus struct {
	Revision        int64  `json:"revision"`
	AppliedRevision int64  `json:"appliedRevision"`
	State           string `json:"state"`
	Processed       int    `json:"processed"`
	Total           int    `json:"total"`
	Error           string `json:"error"`
	NeedsScan       bool   `json:"needsScan"`
}

func GetUpdateStatus(ctx context.Context, db storage.Querier) (UpdateStatus, error) {
	var s UpdateStatus
	err := db.QueryRowContext(ctx, `SELECT revision,applied_revision,state,processed,total,error,needs_scan FROM library_update_state WHERE id=1`).Scan(&s.Revision, &s.AppliedRevision, &s.State, &s.Processed, &s.Total, &s.Error, &s.NeedsScan)
	return s, err
}

type updateSignal struct {
	mu           sync.Mutex
	wake         chan struct{}
	scanCancel   context.CancelFunc
	updateCancel context.CancelFunc
}

// Signals hold no goroutines. The server owns the single worker's lifetime;
// embedded HTTP handlers only persist and signal work, including across restart.
var updateSignals sync.Map

func signalFor(db *sql.DB) *updateSignal {
	s, _ := updateSignals.LoadOrStore(db, &updateSignal{wake: make(chan struct{}, 1)})
	return s.(*updateSignal)
}
func NotifyUpdate(db *sql.DB, scanChanged bool) {
	s := signalFor(db)
	s.mu.Lock()
	if scanChanged && s.scanCancel != nil {
		s.scanCancel()
	}
	if s.updateCancel != nil {
		s.updateCancel()
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	s.mu.Unlock()
}

// ScanContext also covers startup and explicit scans: a folder edit invalidates
// their root snapshot, while a path preference edit leaves useful discovery running.
func ScanContext(parent context.Context, db *sql.DB) (context.Context, context.CancelFunc, error) {
	ctx, cancel := context.WithCancel(parent)
	s := signalFor(db)
	s.mu.Lock()
	s.scanCancel = cancel
	s.mu.Unlock()

	stop := func() { s.mu.Lock(); s.scanCancel = nil; s.mu.Unlock(); cancel() }
	var revision int64
	if err := db.QueryRowContext(ctx, `SELECT scan_revision FROM library_update_state WHERE id=1`).Scan(&revision); err != nil {
		stop()
		return ctx, func() {}, err
	}
	return workrevision.WithScan(ctx, revision), stop, nil
}

// RunUpdateWorker replays persisted work, debounces bursts, and runs one update at a time.
func RunUpdateWorker(ctx context.Context, db *sql.DB) { runUpdateWorker(ctx, db, processUpdate) }
func runUpdateWorker(ctx context.Context, db *sql.DB, process func(context.Context, *sql.DB) error) {
	s := signalFor(db)
	defer updateSignals.Delete(db)
	// Running states belong to the old process; preferences and dirty rows are durable.
	_, err := db.ExecContext(ctx, `UPDATE library_update_state SET state='queued' WHERE needs_scan=1 OR EXISTS(SELECT 1 FROM library_update_channels)`)
	if err != nil {
		logging.Error("library updates: recover: %v", err)
		return
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		}
		timer := time.NewTimer(500 * time.Millisecond)
	debounce:
		for {
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-s.wake:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(500 * time.Millisecond)
			case <-timer.C:
				break debounce
			}
		}
		if err := process(ctx, db); err != nil && ctx.Err() == nil && !errors.Is(err, context.Canceled) {
			logging.Error("library updates: %v", err)
		}
	}
}

func lockUpdates(ctx context.Context) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for !running.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	return nil
}

type updateSteps struct {
	prepare    func(context.Context, *sql.DB, scanMode) (Result, error)
	rebuild    func(context.Context, *sql.DB, []int64) error
	regenerate func(context.Context, *sql.DB, int64, time.Time) error
}

func processUpdate(parent context.Context, db *sql.DB) error {
	return processUpdateUsing(parent, db, updateSteps{prepare: prepareLocked, rebuild: lineup.BuildFilteredChannels, regenerate: schedule.Regenerate})
}
func processUpdateUsing(parent context.Context, db *sql.DB, steps updateSteps) (err error) {
	if err := lockUpdates(parent); err != nil {
		return err
	}
	defer running.Unlock()
	current, err := GetUpdateStatus(parent, db)
	if err != nil || (current.State == "idle" && !current.NeedsScan) || current.State == "failed" {
		return err
	}
	var revision = current.Revision
	status.Default.Begin(status.TriggerRefresh, status.PhasePopulating)
	defer status.Default.End()
	defer func() {
		if parent.Err() != nil {
			return
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			_, _ = db.ExecContext(parent, `UPDATE library_update_state SET state='failed',error='Library update failed. Retry to apply saved preferences.' WHERE id=1 AND revision=?`, revision)
		}
	}()
	scanned := current.NeedsScan
	if current.NeedsScan {
		logging.Info("content folders changed: rescanning")
		scanCtx, stop, scanErr := ScanContext(parent, db)
		if scanErr != nil {
			return scanErr
		}
		_, _ = db.ExecContext(parent, `UPDATE library_update_state SET state='scanning' WHERE id=1`)
		status.Default.SetPhase(status.PhaseScanning)
		_, err = steps.prepare(scanCtx, db, scanAlways)
		if err == nil {
			err = finishScan(scanCtx, db)
		}
		stop()
		if err != nil {
			return err
		}
	}
	// A selection edit during discovery is folded into this same derivation pass.
	current, err = GetUpdateStatus(parent, db)
	if err != nil {
		return err
	}
	if current.NeedsScan {
		NotifyUpdate(db, true)
		return context.Canceled
	}
	revision = current.Revision
	ctx, cancel := context.WithCancel(parent)
	s := signalFor(db)
	s.mu.Lock()
	s.updateCancel = cancel
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.updateCancel = nil; s.mu.Unlock(); cancel() }()
	ctx = workrevision.With(ctx, revision)
	if err := workrevision.Check(ctx, db); err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `SELECT channel_id,rebuild FROM library_update_channels ORDER BY channel_id`)
	if err != nil {
		return err
	}
	var ids, rebuild []int64
	for rows.Next() {
		var id int64
		var build bool
		if err = rows.Scan(&id, &build); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
		if build {
			rebuild = append(rebuild, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `UPDATE library_update_state SET state='updating',processed=0,total=? WHERE id=1 AND revision=?`, len(ids), revision)
	if err != nil {
		return err
	}
	status.Default.SetPhase(status.PhasePopulating)
	if len(rebuild) > 0 {
		if err = steps.rebuild(ctx, db, rebuild); err != nil {
			return err
		}
		if err = markLineupsReady(ctx, db, revision); err != nil {
			return err
		}
	}
	status.Default.SetPhase(status.PhaseScheduling)
	for i, id := range ids {
		if err = workrevision.Check(ctx, db); err != nil {
			return err
		}
		if _, getErr := channel.Get(ctx, db, id); errors.Is(getErr, channel.ErrNotFound) {
			continue
		} else if getErr != nil {
			return getErr
		}
		if err = steps.regenerate(ctx, db, id, time.Now()); err != nil {
			return err
		}
		if err = acknowledgeChannel(ctx, db, revision, id, i+1); err != nil {
			return err
		}
	}
	if _, err = lineup.SyncDemoChannels(ctx, db); err != nil {
		return err
	}
	if scanned {
		if err = lineup.SyncProviderVisibility(ctx, db); err != nil {
			return err
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = workrevision.Check(ctx, tx); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM library_update_channels WHERE revision<=?`, revision); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE library_update_state SET applied_revision=?,state='idle',processed=total,error='' WHERE id=1 AND revision=?`, revision, revision); err != nil {
		return err
	}
	err = tx.Commit()
	if err == nil && scanned {
		logging.Info("content folders changed: rescan complete (channelsRefreshed=%d)", len(ids))
	}
	return err
}

func finishScan(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := workrevision.CheckScan(ctx, tx); err != nil {
		return err
	}
	// A first folder scan must initialize default filters just as a normal refresh does.
	if _, err := lineup.SeedDefaultFilters(ctx, tx); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO library_update_channels(channel_id,rebuild,revision) SELECT id,1,(SELECT revision FROM library_update_state WHERE id=1) FROM channels WHERE true ON CONFLICT(channel_id) DO UPDATE SET rebuild=1,revision=excluded.revision`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE library_update_state SET needs_scan=0,state='queued' WHERE id=1`); err != nil {
		return err
	}
	return tx.Commit()
}

// Retrying a nonfailed job is a no-op: it must not cancel useful running work.
func RetryUpdates(ctx context.Context, db *sql.DB) (UpdateStatus, error) {
	result, err := db.ExecContext(ctx, `UPDATE library_update_state SET revision=revision+1,state='queued',error='' WHERE id=1 AND state='failed'`)
	if err != nil {
		return UpdateStatus{}, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return UpdateStatus{}, err
	}
	state, err := GetUpdateStatus(ctx, db)
	if err != nil {
		return state, err
	}
	if changed > 0 {
		NotifyUpdate(db, false)
	}
	return state, nil
}

// Persist phase completion so cancellation/restart reuses lineups already built
// and never repeats completed, unaffected channels from an interrupted batch.
func markLineupsReady(ctx context.Context, db *sql.DB, revision int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := workrevision.Check(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE library_update_channels SET rebuild=0 WHERE revision<=?`, revision); err != nil {
		return err
	}
	return tx.Commit()
}
func acknowledgeChannel(ctx context.Context, db *sql.DB, revision, id int64, processed int) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := workrevision.Check(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM library_update_channels WHERE channel_id=? AND revision<=?`, id, revision); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE library_update_state SET processed=? WHERE id=1 AND revision=?`, processed, revision); err != nil {
		return err
	}
	return tx.Commit()
}
