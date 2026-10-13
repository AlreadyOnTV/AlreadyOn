// Package refresh implements the "regenerate everything" pipeline
// shared by the Channels admin tab's "Refresh Schedule" button
// (httpapi.handleRefreshSchedule), the commercial-settings save handler,
// and the background content-polling loop (see main.go) — every place
// that needs to react to newly-detected content or changed settings by
// bringing the schedule back up to date, rather than duplicating this
// sequence at each call site.
package refresh

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/lineup"
	"github.com/alreadyonapp/alreadyon/server/internal/logging"
	"github.com/alreadyonapp/alreadyon/server/internal/metadata"
	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
	"github.com/alreadyonapp/alreadyon/server/internal/status"
)

// Result summarizes what Run actually did, for a caller (an HTTP
// response, a log line) that wants to report it.
type Result struct {
	Classified           bool
	Enriched             bool
	ChannelsRefreshed    int
	SchedulesRegenerated int
	// Scanned and FilesAdded are only set by runs that scanned the
	// folders first (Rescan, PollOnce).
	Scanned      bool
	FilesAdded   int
	FilesMissing int
}

type scanMode int

const (
	// scanNone: run the pipeline only (Refresh Schedule, settings saves).
	scanNone scanMode = iota
	// scanAlways: scan the folders, then always run the pipeline. Used when
	// the folder settings change, since removed files must leave the guide.
	scanAlways
	// scanIfNew: scan, and run the pipeline only if new files were found.
	scanIfNew
)

var foldersChanged = make(chan struct{}, 1)

// NotifyFoldersChanged tells the folder watcher (see FoldersChanged) that
// an admin saved different folders or exclusions. Never blocks; several
// notifications before the watcher wakes collapse into one.
func NotifyFoldersChanged() {
	select {
	case foldersChanged <- struct{}{}:
	default:
	}
}

// FoldersChanged delivers a value after each NotifyFoldersChanged.
func FoldersChanged() <-chan struct{} { return foldersChanged }

// Metadata saves are coalesced and consumed by the server, not HTTP handlers.
// Tests/embedded routers do not spawn unowned workers or external API requests.
var metadataChanged = make(chan struct{}, 1)

func NotifyMetadataChanged() {
	select {
	case metadataChanged <- struct{}{}:
	default:
	}
}
func MetadataChanged() <-chan struct{} { return metadataChanged }

// ApplyMetadataChange reconciles broad channels first, then enriches in the
// background worker. Slow free lookups cannot delay scheduling usable files.
func ApplyMetadataChange(ctx context.Context, db *sql.DB) (Result, error) {
	running.Lock()
	defer running.Unlock()
	if _, err := lineup.Refresh(ctx, db); err != nil {
		return Result{}, err
	}
	if _, err := lineup.SyncDemoChannels(ctx, db); err != nil {
		return Result{}, err
	}
	if _, err := schedule.RegenerateAll(ctx, db, time.Now()); err != nil {
		return Result{}, err
	}
	return runLocked(ctx, db, status.TriggerRefresh, scanNone)
}

var keylessClient = metadata.NewKeylessClient()

// ErrBusy is returned by Run when another refresh (or the startup
// pipeline, via Exclusive) is already running.
var ErrBusy = errors.New("refresh: a library refresh is already in progress")

var running sync.Mutex

// Exclusive runs fn while holding the same lock as Run, waiting for any
// in-flight Run to finish first. Used by the startup pipeline so a
// Refresh Schedule click or content poll can't overlap it.
func Exclusive(fn func()) {
	running.Lock()
	defer running.Unlock()
	fn()
}

// Run reclassifies genres and enriches overview/date/certification
// details (library.ClassifyGenres, library.EnrichMetadata) for anything
// not yet processed — using the TMDb key from Server Settings, or, when
// no key is set and the admin has enabled it, the no-key TVmaze/Wikidata
// fallback — then rebuilds every built-in default channel's
// lineup from scratch (lineup.Refresh), deliberately touching channels
// that already have content, since every caller of Run is an explicit
// request (or an explicit trigger, like new content appearing) to
// regenerate now — and finally brings every channel's persisted
// schedule (schedule.RegenerateAll) up to date with whatever the lineup
// and commercial settings now say, never disturbing an entry already
// airing (see schedule.Regenerate). Returns ErrBusy without doing
// anything if another refresh is already running. Progress is reported
// to status.Default as a status.TriggerRefresh run.
func Run(ctx context.Context, db *sql.DB) (Result, error) {
	return RunAs(ctx, db, status.TriggerRefresh)
}

// RunAs is Run, reporting to status.Default as trigger (e.g.
// status.TriggerPoll for the content-polling loop).
func RunAs(ctx context.Context, db *sql.DB, trigger status.Trigger) (Result, error) {
	if !running.TryLock() {
		return Result{}, ErrBusy
	}
	defer running.Unlock()
	return runLocked(ctx, db, trigger, scanNone)
}

// Rescan scans the current folder settings and then always runs the
// pipeline, so files that disappeared (a removed folder, a new exclusion)
// leave the guide and new ones appear. It waits for any run already in
// progress rather than failing with ErrBusy.
func Rescan(ctx context.Context, db *sql.DB) (Result, error) {
	running.Lock()
	defer running.Unlock()
	return runLocked(ctx, db, status.TriggerRefresh, scanAlways)
}

// TryRescan is Rescan for an explicit admin request: it returns ErrBusy
// instead of waiting when another run is in progress.
func TryRescan(ctx context.Context, db *sql.DB) (Result, error) {
	if !running.TryLock() {
		return Result{}, ErrBusy
	}
	defer running.Unlock()
	return runLocked(ctx, db, status.TriggerRefresh, scanAlways)
}

// PollOnce is one content-polling cycle: scan the current folders and, if
// new files turned up, run the pipeline. The scan happens under the same
// lock as every other run, so two scans can never overlap. Returns
// ErrBusy if another run holds the lock, and does nothing (no error) when
// no usable folder is configured.
func PollOnce(ctx context.Context, db *sql.DB) (Result, error) {
	if !running.TryLock() {
		return Result{}, ErrBusy
	}
	defer running.Unlock()
	cfg, err := library.LoadScanConfig(ctx, db)
	if err != nil {
		return Result{}, fmt.Errorf("refresh: load folder settings: %w", err)
	}
	if len(cfg.Roots) == 0 {
		return Result{}, nil
	}
	return runLocked(ctx, db, status.TriggerPoll, scanIfNew)
}

// runLocked is the pipeline; the caller holds the lock.
func runLocked(ctx context.Context, db *sql.DB, trigger status.Trigger, mode scanMode) (res Result, err error) {
	first := status.PhaseClassifying
	if mode != scanNone {
		first = status.PhaseScanning
	}
	status.Default.Begin(trigger, first)
	defer func() {
		status.Default.RecordError(err)
		status.Default.End()
	}()

	if mode != scanNone {
		scanCfg, cerr := library.LoadScanConfig(ctx, db)
		if cerr != nil {
			return res, fmt.Errorf("refresh: load folder settings: %w", cerr)
		}
		status.Default.SetMediaRoots(scanCfg.MediaRootCount())
		for _, p := range scanCfg.Problems {
			logging.Warn("content root problem: %v", p)
		}
		// No time limit: a scan runs to completion (per-file probes have their
		// own timeout), and the lock keeps a second one from starting.
		scan, serr := scanCfg.NewScanner(db).Scan(ctx)
		if serr != nil {
			return res, fmt.Errorf("refresh: library scan: %w", serr)
		}
		for _, e := range scan.Errors {
			logging.Warn("library scan: %v", e)
		}
		res.Scanned, res.FilesAdded, res.FilesMissing = true, scan.FilesAdded, scan.FilesMissing
		logging.Info("library scan complete: added=%d updated=%d unchanged=%d skipped=%d missing=%d errors=%d",
			scan.FilesAdded, scan.FilesUpdated, scan.FilesUnchanged, scan.FilesSkipped, scan.FilesMissing, len(scan.Errors))
		if mode == scanIfNew && scan.FilesAdded == 0 {
			return res, nil
		}
		status.Default.SetPhase(status.PhaseClassifying)
	}
	settings, err := metadata.GetSettings(ctx, db)
	if err != nil {
		return res, fmt.Errorf("refresh: get metadata settings: %w", err)
	}

	if tmdbKey := settings.TMDbAPIKey; tmdbKey != "" {
		client := metadata.NewClient(tmdbKey)
		if err := library.ClassifyGenres(ctx, db, client); err != nil {
			return res, fmt.Errorf("refresh: classify genres: %w", err)
		}
		res.Classified = true
		if err := library.EnrichMetadata(ctx, db, client); err != nil {
			return res, fmt.Errorf("refresh: enrich metadata: %w", err)
		}
		res.Enriched = true
	} else if settings.KeylessEnabled {
		if err := library.ClassifyGenresKeyless(ctx, db, keylessClient); err != nil {
			return res, fmt.Errorf("refresh: keyless classify: %w", err)
		}
		res.Classified, res.Enriched = true, true
	}

	status.Default.SetPhase(status.PhasePopulating)
	refreshed, err := lineup.Refresh(ctx, db)
	if err != nil {
		return res, fmt.Errorf("refresh: refresh channels: %w", err)
	}
	res.ChannelsRefreshed = refreshed

	if _, err := lineup.SyncDemoChannels(ctx, db); err != nil {
		return res, fmt.Errorf("refresh: sync demo channels: %w", err)
	}

	status.Default.SetPhase(status.PhaseScheduling)
	regenerated, err := schedule.RegenerateAll(ctx, db, time.Now())
	if err != nil {
		return res, fmt.Errorf("refresh: regenerate schedules: %w", err)
	}
	res.SchedulesRegenerated = regenerated

	return res, nil
}
