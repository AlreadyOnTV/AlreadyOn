// Command server is the AlreadyOn backend entrypoint.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/admincli/accesskey"
	"github.com/alreadyonapp/alreadyon/server/internal/admincli/clearsessions"
	"github.com/alreadyonapp/alreadyon/server/internal/admincli/resetpassword"
	"github.com/alreadyonapp/alreadyon/server/internal/admincli/tlscli"
	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/config"
	"github.com/alreadyonapp/alreadyon/server/internal/ddns"
	"github.com/alreadyonapp/alreadyon/server/internal/governor"
	"github.com/alreadyonapp/alreadyon/server/internal/httpapi"
	"github.com/alreadyonapp/alreadyon/server/internal/instancetls"
	"github.com/alreadyonapp/alreadyon/server/internal/keyframes"
	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/lineup"
	"github.com/alreadyonapp/alreadyon/server/internal/logging"
	"github.com/alreadyonapp/alreadyon/server/internal/loudness"
	"github.com/alreadyonapp/alreadyon/server/internal/metadata"
	"github.com/alreadyonapp/alreadyon/server/internal/metrics"
	"github.com/alreadyonapp/alreadyon/server/internal/playback"
	"github.com/alreadyonapp/alreadyon/server/internal/refresh"
	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
	"github.com/alreadyonapp/alreadyon/server/internal/serversettings"
	"github.com/alreadyonapp/alreadyon/server/internal/status"
	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

func main() {
	// Container aliases share one executable instead of duplicating Go/SQLite runtimes.
	// Dispatch before server configuration or startup; standalone developer commands remain.
	switch filepath.Base(os.Args[0]) {
	case "alreadyon-access-key":
		accesskey.Run()
		return
	case "alreadyon-clear-browser-sessions":
		clearsessions.Run()
		return
	case "alreadyon-reset-password":
		resetpassword.Run()
		return
	case "alreadyon-tls":
		tlscli.Run()
		return
	}

	if run() {
		// Started again in place (same process ID, so a container keeps running).
		exe, err := os.Executable()
		if err == nil {
			err = syscall.Exec(exe, os.Args, os.Environ())
		}
		logging.Fatal("restart failed, start the server again by hand: %v", err)
	}
}

// run serves until stopped and reports whether an administrator asked for a restart.
func run() (restart bool) {
	zone, zoneWarning := config.ApplyTimeZone()
	cfg, err := config.Load()
	if err != nil {
		logging.Fatal("config: %v", err)
	}
	if zoneWarning != nil {
		logging.Warn("TZ is not a usable time zone (%v); using %s", zoneWarning, zone)
	}
	logging.Info("time zone: %s", zone)

	db, err := storage.Open(cfg.DataDir)
	if err != nil {
		logging.Fatal("storage: %v", err)
	}
	defer db.Close()
	if err := storage.MigrateProfileIconStorage(cfg.DataDir); err != nil {
		logging.Fatal("profile icon storage: %v", err)
	}
	certificateManager, err := instancetls.Open(context.Background(), db.DB, cfg.DataDir)
	if err != nil {
		logging.Fatal("TLS configuration: %v", err)
	}
	logging.Info("instance TLS CA SHA-256: %s", certificateManager.Status().Fingerprint)
	sessionCookieName, err := auth.SessionCookieName(context.Background(), db.DB)
	if err != nil {
		logging.Fatal("session configuration: %v", err)
	}

	if exists, err := auth.AccountExists(context.Background(), db.DB); err == nil && !exists {
		logging.Warn("no server account yet: the first person to open the web UI will be asked to create one")
	}

	seeded, err := channel.SeedDefaults(context.Background(), db.DB)
	if err != nil {
		logging.Error("channel: seed defaults: %v", err)
	} else if seeded > 0 {
		logging.Info("seeded %d default channel(s)", seeded)
	}

	scanCfg, err := library.LoadScanConfig(context.Background(), db.DB)
	if err != nil {
		logging.Error("library: %v (no folders will be scanned)", err)
	}
	folders := scanCfg.Folders
	logging.Info("content folders: tv=%v (%s), movies=%v (%s), commercials=%v (%s)",
		folders.TV.Values, folders.TV.Source, folders.Movie.Values, folders.Movie.Source,
		folders.Commercial.Values, folders.Commercial.Source)
	for _, rerr := range scanCfg.Problems {
		logging.Warn("content root problem: %v", rerr)
	}
	for _, r := range scanCfg.Roots {
		logging.Info("content root ready: %s root %s (resolved: %s)", r.Kind, r.ConfiguredPath, r.RealPath)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		sweep := func() {
			if err := auth.PruneSessions(ctx, db.DB, time.Now()); err != nil {
				logging.Warn("session cleanup failed: %v", err)
			}
		}
		sweep()
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweep()
			}
		}
	}()
	ddnsManager := ddns.New(db.DB)
	go ddnsManager.Start(ctx)
	// A dedicated bounded plaintext listener exposes challenge tokens only.
	challengeSrv := &http.Server{Addr: challengeAddress(cfg.ManagementAddr), Handler: certificateManager.ChallengeHandler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8192}
	go func() {
		if err := challengeSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logging.Error("ACME challenge listener unavailable: %v", err)
		}
	}()
	defer challengeSrv.Close()
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		certificateManager.RenewACME(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				certificateManager.RenewACME(ctx)
			}
		}
	}()
	// Renew under the same CA without restarting playback or changing trust.
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := certificateManager.Renew(ctx); err != nil {
					logging.Error("TLS renewal failed; retained current certificate: %v", err)
				}
			}
		}
	}()
	// One governor keeps all background analysis, keyframes first, within the CPU budget.
	background := governor.New(runtime.NumCPU())
	library.SetBackgroundGovernor(background)
	governor.SetDefault(background)
	go background.Run(ctx, governor.ReadProc, governor.DefaultInterval)
	go keyframes.Run(ctx, db.DB, background)
	go loudness.Run(ctx, db.DB, background)
	go metrics.Collect(ctx, db.DB, cfg.DataDir, func(ctx context.Context) []string {
		scan, err := library.LoadScanConfig(ctx, db.DB)
		if err != nil {
			return nil
		}
		paths := make([]string, 0, len(scan.Roots))
		for _, root := range scan.Roots {
			paths = append(paths, root.RealPath)
		}
		return paths
	})
	playbackSettings, err := playback.GetSettings(ctx, db.DB)
	if err != nil {
		logging.Fatal("playback settings: %v", err)
	}
	hardware := &playback.Hardware{}
	// Transcode output is scratch space: /config/transcode in the container, which
	// the compose file can mount as a tmpfs. Drop the folder older versions used.
	transcodeDir := filepath.Join(cfg.DataDir, "transcode")
	_ = os.RemoveAll(filepath.Join(cfg.DataDir, "playback-cache"))
	streams, err := playback.NewManager(transcodeDir, schedule.SystemClock{}, playback.Runner{OnHardwareFailure: hardware.Failed, OnDecodeFailure: hardware.DecodeFailed, OnToneMapFailure: hardware.ToneMapFailed}, playbackSettings)
	if err != nil {
		logging.Fatal("playback manager: %v", err)
	}
	defer streams.Close()
	if missing := playback.MissingCommonDecoders(); len(missing) > 0 {
		logging.Warn("FFmpeg cannot decode %s: titles using those codecs can't be converted for browser playback. Install a full FFmpeg build (on Fedora, RPM Fusion's ffmpeg instead of ffmpeg-free)", strings.Join(missing, ", "))
	}
	go streams.Serve(ctx)
	playbackHTTP := httpapi.NewPlaybackHTTP(db.DB, playback.NewSessions(schedule.SystemClock{}), streams)
	playbackHTTP.Hardware = hardware
	capacity := &playback.Capacity{
		Context:  ctx,
		Hardware: hardware,
		Encoder: func(c context.Context) string {
			ss, err := serversettings.Get(c, db.DB)
			if err != nil {
				return ""
			}
			return hardware.Resolve(c, ss.Transcoding, ss.HardwareDecoding)
		},
		Busy: func() bool { processes, _, viewers := streams.Counts(); return processes > 0 || viewers > 0 },
		Save: func(c context.Context, r playback.CapacityRecord) {
			if err := playback.SaveCapacity(c, db.DB, r); err != nil {
				logging.Warn("server capacity: could not save the result: %v", err)
			}
		},
		// The detected parallel-conversion limit reaches the manager as soon as it changes.
		OnChange: func() {
			s, err := playback.GetSettings(ctx, db.DB)
			if err == nil {
				err = streams.Configure(playback.EffectiveSettings(s, playbackHTTP.Capacity))
			}
			if err != nil {
				logging.Warn("parallel conversions: could not apply the detected limit: %v", err)
			}
		},
	}
	capacity.HardwareDecoding = func(c context.Context) bool {
		ss, err := serversettings.Get(c, db.DB)
		return err == nil && ss.HardwareDecoding
	}
	playbackHTTP.Capacity = capacity
	tuner := &playback.Tuner{
		Context:  ctx,
		Hardware: hardware,
		Encoder: func(c context.Context) string {
			ss, err := serversettings.Get(c, db.DB)
			if err != nil {
				return ""
			}
			return hardware.Resolve(c, ss.Transcoding, ss.HardwareDecoding)
		},
		Processes: func(c context.Context) int {
			s, err := playback.GetSettings(c, db.DB)
			if err != nil {
				return 1
			}
			return playback.EffectiveSettings(s, capacity).MaxProcesses
		},
		Busy: func() bool { processes, _, viewers := streams.Counts(); return processes > 0 || viewers > 0 },
		Save: func(c context.Context, threads int, at time.Time) {
			if err := serversettings.SaveTunedThreads(c, db.DB, threads, at); err != nil {
				logging.Warn("transcode threads: could not save the result: %v", err)
			}
		},
	}
	if threads, at, err := serversettings.TunedThreads(ctx, db.DB); err == nil {
		tuner.Load(threads, at)
	}
	playbackHTTP.Tuner = tuner
	capacity.Threads = tuner.Threads
	capacity.TunerRunning = func() bool { return tuner.Status().State == "running" }
	if record, err := playback.LoadCapacity(ctx, db.DB); err == nil {
		capacity.Load(record)
	}
	// Measured in the background at every start; started before the listeners so no stream can begin first.
	tuner.Start(false)
	// The speed measurement waits for the thread count; the parallel limit is
	// worked out at once from the last measurement and the hardware now present.
	capacity.Start(false)
	capacity.RecomputeProcesses(ctx)
	// Browser fixtures own exact schedule entries and measurement timing. Only the
	// separately tagged E2E binary disables these unrelated wall-clock jobs.
	if periodicJobsEnabled {
		go runHourlyAt(ctx, config.WorkerLimitOffset, func() { capacity.RecomputeProcesses(ctx) })
		go runHourlyAt(ctx, config.CapacityMeasureOffset, func() { capacity.Start(false) })
		go runScheduleMaintenance(ctx, db.DB)
	}
	go runContentPolling(ctx, db.DB)
	go runFolderWatcher(ctx, db.DB)
	go runMetadataWatcher(ctx, db.DB)

	logging.StartHeartbeat(ctx, 15*time.Minute, 30*time.Second)

	// Begun before the listener starts so a status request never sees a
	// startup that hasn't been accounted for; with no media folders there
	// is nothing to do, so it ends immediately.
	status.Default.SetMediaRoots(scanCfg.MediaRootCount())
	status.Default.Begin(status.TriggerStartup, status.PhaseScanning)
	if len(scanCfg.Roots) == 0 {
		status.Default.End()
	}

	restartRequested := make(chan struct{}, 1)
	management := http.NewServeMux()
	httpapi.RegisterTLS(management, certificateManager)
	httpapi.RegisterDDNS(management, ddnsManager)
	management.Handle("POST /api/server/restart", httpapi.RestartHandler(func() {
		select {
		case restartRequested <- struct{}{}:
		default:
		}
	}))
	management.Handle("/", httpapi.NewRouterWithSessionCookie(cfg.WebDir, filepath.Join(cfg.DataDir, "profile-icons"), db.DB, sessionCookieName, playbackHTTP))
	srv := &http.Server{
		Addr:              listenerAddress(cfg.ManagementAddr),
		Handler:           auth.RequireAuthWithSessionCookie(db.DB, sessionCookieName, management),
		ReadHeaderTimeout: 5 * time.Second,
		// Management bodies are bounded; a slow sender must not hold an
		// authentication slot indefinitely. Streaming responses have no write deadline.
		ReadTimeout: 30 * time.Second,
		IdleTimeout: 60 * time.Second,
		TLSConfig:   certificateManager.Config(),
	}

	go func() {
		logging.Info("HTTPS application listener on %s", cfg.ManagementAddr)
		if err := serveListener(srv); err != nil && err != http.ErrServerClosed {
			logging.Fatal("server: %v", err)
		}
	}()

	// The library pipeline can take minutes on a real library, so it runs
	// after the listener is up rather than delaying it (#107).
	bootstrapDone := make(chan struct{})
	go func() {
		defer close(bootstrapDone)
		if len(scanCfg.Roots) > 0 {
			runBootstrap(ctx, db.DB)
		}
	}()

	select {
	case <-ctx.Done():
		logging.Info("shutting down")
	case <-restartRequested:
		logging.Info("shutting down to restart")
		restart = true
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logging.Error("shutdown: %v", err)
	}
	select {
	case <-bootstrapDone:
	case <-shutdownCtx.Done():
		logging.Warn("shutdown: startup library pipeline did not stop in time")
	}
	return restart
}

// runBootstrap is the startup library pipeline: scan, classify/enrich,
// populate default channels, demo channels, and schedules. It holds the
// refresh lock (refresh.Exclusive) so a Refresh Schedule click or
// content poll can't overlap it.
func runBootstrap(ctx context.Context, db *sql.DB) {
	start := time.Now()
	logging.Info("startup: library pipeline starting (the web UI is already available)")
	runKeyless := true

	refresh.Exclusive(func() {
		defer status.Default.End()
		if err := runStartupScan(ctx, db); err != nil {
			status.Default.RecordError(err)
		}
		if ctx.Err() != nil {
			return
		}

		status.Default.SetPhase(status.PhaseClassifying)
		metaSettings, err := metadata.GetSettings(ctx, db)
		if err != nil {
			logging.Error("metadata: get settings: %v", err)
			status.Default.RecordError(err)
		}
		tmdbKey := metaSettings.TMDbAPIKey

		if tmdbKey == "" {
			runKeyless = true
			// WARN, not INFO: if this key was configured before, a still-empty
			// Server Settings row after a restart is worth an admin's
			// attention (see #93) — this line, visible on the Logs page,
			// is the signal, whether the cause is a botched migration/restore
			// or the admin genuinely never set one up.
			logging.Warn("TMDb classification skipped: no API key configured (set one on the Server tab)")
		} else {
			logging.Info("TMDb API key configured (Server Settings)")
			phaseStart := time.Now()
			logging.Info("startup: TMDb classification starting")
			client := metadata.NewClient(tmdbKey)
			if err := library.ClassifyGenres(ctx, db, client); err != nil {
				logging.Error("library: classify genres: %v", err)
				status.Default.RecordError(err)
			}
			if err := library.EnrichMetadata(ctx, db, client); err != nil {
				logging.Error("library: enrich metadata: %v", err)
				status.Default.RecordError(err)
			}
			logging.Info("startup: TMDb classification finished in %s", time.Since(phaseStart).Round(time.Millisecond))
		}
		if ctx.Err() != nil {
			return
		}

		status.Default.SetPhase(status.PhasePopulating)
		if populated, err := lineup.Populate(ctx, db); err != nil {
			logging.Error("lineup: populate default channels: %v", err)
			status.Default.RecordError(err)
		} else if populated > 0 {
			logging.Info("populated %d default channel(s) with matching library content", populated)
		}

		if demo, err := lineup.SyncDemoChannels(ctx, db); err != nil {
			logging.Error("lineup: sync demo channels: %v", err)
			status.Default.RecordError(err)
		} else if demo > 0 {
			logging.Warn("no channel matched library content; showing %d demo channel(s) until a metadata provider key is configured", demo)
		}

		status.Default.SetPhase(status.PhaseScheduling)
		if regenerated, err := schedule.RegenerateAll(ctx, db, time.Now()); err != nil {
			logging.Error("schedule: regenerate all: %v", err)
			status.Default.RecordError(err)
		} else {
			logging.Info("regenerated %d channel schedule(s)", regenerated)
		}
	})
	if ctx.Err() != nil {
		return
	}
	logging.Info("startup: library pipeline finished in %s", time.Since(start).Round(time.Millisecond))

	if runKeyless {
		runKeylessClassification(ctx, db)
	}
}

// runContentPolling re-scans the current content folders on a timer (see
// library.PollSettings, admin-configurable via Server Settings > Media >
// Polling Interval), on top of the startup scan. The interval and the
// folders themselves are re-read every cycle, so a change the admin just
// saved takes effect on the next cycle without a restart. When a scan
// finds new files it runs the same reclassify+refresh pipeline as the
// Channels tab's "Refresh Schedule" button, so new content shows up in
// the guide without anyone clicking anything (refresh.PollOnce does the
// scan under the same lock as every other run). Returns once ctx is done.
func runContentPolling(ctx context.Context, db *sql.DB) {
	for {
		interval := time.Hour
		if settings, err := library.GetPollSettings(ctx, db); err != nil {
			logging.Error("content polling: get poll settings: %v", err)
		} else {
			interval = time.Duration(settings.IntervalMinutes) * time.Minute
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}

		res, err := refresh.PollOnce(ctx, db)
		switch {
		case errors.Is(err, refresh.ErrBusy):
			logging.Info("content polling: another library run is in progress; skipping this cycle")
		case err != nil:
			logging.Error("content polling: %v", err)
		case res.FilesAdded > 0:
			logging.Info("content polling: %d new file(s) detected; refresh complete (classified=%v enriched=%v channelsRefreshed=%d)",
				res.FilesAdded, res.Classified, res.Enriched, res.ChannelsRefreshed)
		}
	}
}

// runFolderWatcher rescans the library whenever an admin saves different
// content folders or exclusion patterns (see refresh.NotifyFoldersChanged),
// so the guide follows the new settings without a restart. Rapid saves are
// coalesced; a run already in progress is waited for, never overlapped.
func runFolderWatcher(ctx context.Context, db *sql.DB) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-refresh.FoldersChanged():
		}
		// Let a burst of saves settle, then scan once.
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
		select {
		case <-refresh.FoldersChanged():
		default:
		}

		logging.Info("content folders changed: rescanning")
		res, err := refresh.Rescan(ctx, db)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			logging.Error("content folders changed: rescan: %v", err)
			continue
		}
		logging.Info("content folders changed: rescan complete (added=%d channelsRefreshed=%d)", res.FilesAdded, res.ChannelsRefreshed)
	}
}

// runMetadataWatcher rebuilds provider-mode channels after settings saves.
// It shares the refresh lock and stops with the server; bursts are coalesced.
func runMetadataWatcher(ctx context.Context, db *sql.DB) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-refresh.MetadataChanged():
		}
		if _, err := refresh.ApplyMetadataChange(ctx, db); err != nil && ctx.Err() == nil {
			logging.Error("metadata settings changed: refresh: %v", err)
		}
	}
}

// runScheduleMaintenance periodically brings every channel's persisted
// schedule (see schedule.RegenerateAll) back up to its full look-ahead
// window and prunes anything past its trailing edge — the thing that
// actually keeps the guide's "now" moving forward across a long-running
// server, on top of the explicit triggers (Refresh Schedule button,
// commercial-settings save, content polling) that already call this via
// internal/refresh.Run. It runs at the top of every hour (config.ScheduleMaintenanceOffset),
// independent of content polling. Returns once ctx is done.
func runScheduleMaintenance(ctx context.Context, db *sql.DB) {
	runHourlyAt(ctx, config.ScheduleMaintenanceOffset, func() {
		if regenerated, err := schedule.RegenerateAll(ctx, db, time.Now()); err != nil {
			logging.Error("schedule maintenance: regenerate all: %v", err)
		} else {
			logging.Info("schedule maintenance: regenerated %d channel schedule(s)", regenerated)
		}
	})
}

// runHourlyAt calls job every hour at offset past the hour (local time) until
// ctx is done, so the hourly jobs never start together.
func runHourlyAt(ctx context.Context, offset time.Duration, job func()) {
	for {
		timer := time.NewTimer(time.Until(config.NextHourlyAt(time.Now(), offset)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			job()
		}
	}
}

// runKeylessClassification classifies the library through the no-key
// providers (TVmaze/Wikidata) in the background — rate-limited lookups
// can take minutes on a large library — then fills any still-empty
// default channels and retires the demo channels if real ones now have
// content.
func runKeylessClassification(ctx context.Context, db *sql.DB) {
	logging.Info("keyless metadata: classifying library via TVmaze/Wikidata (set a TMDb key for better results)")
	start := time.Now()
	// Not serialized by the refresh lock (it's the slow part), so only
	// report status if no other run is active.
	if status.Default.TryBegin(status.TriggerRefresh, status.PhaseClassifying) {
		defer status.Default.End()
	}
	if err := library.ClassifyGenresKeyless(ctx, db, metadata.NewKeylessClient()); err != nil {
		logging.Error("keyless metadata: classify: %v", err)
		return
	}
	var populated, demo int
	var err error
	refresh.Exclusive(func() {
		if populated, err = lineup.Populate(ctx, db); err != nil {
			err = fmt.Errorf("populate default channels: %w", err)
			return
		}
		if demo, err = lineup.SyncDemoChannels(ctx, db); err != nil {
			err = fmt.Errorf("sync demo channels: %w", err)
			return
		}
		if _, err = schedule.RegenerateAll(ctx, db, time.Now()); err != nil {
			err = fmt.Errorf("regenerate schedules: %w", err)
		}
	})
	if err != nil {
		logging.Error("keyless metadata: %v", err)
		return
	}
	logging.Info("keyless metadata: done in %s (populated %d channel(s), %d demo channel(s) remaining)",
		time.Since(start).Round(time.Second), populated, demo)
}

// runStartupScan performs one library scan at boot when roots are
// configured (content polling handles later rescans). Returns the scan's
// fatal error, if any; per-file problems are only logged.
func runStartupScan(parent context.Context, db *sql.DB) error {
	scanCfg, err := library.LoadScanConfig(parent, db)
	if err != nil {
		logging.Error("library scan: %v", err)
		return err
	}
	scanner := scanCfg.NewScanner(db)

	start := time.Now()
	logging.Info("library scan starting (%d root(s))", len(scanCfg.Roots))
	result, err := scanner.Scan(parent)
	if parent.Err() != nil {
		logging.Info("library scan stopped: shutting down")
		return nil
	}
	if err != nil {
		logging.Error("library scan: %v", err)
		return err
	}
	for _, e := range result.Errors {
		logging.Warn("library scan: %v", e)
	}
	logging.Info("library scan complete in %s: added=%d updated=%d unchanged=%d skipped=%d missing=%d errors=%d",
		time.Since(start).Round(time.Millisecond), result.FilesAdded, result.FilesUpdated, result.FilesUnchanged, result.FilesSkipped, result.FilesMissing, len(result.Errors))
	return nil
}
