package refresh

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/commercial"
	"github.com/alreadyonapp/alreadyon/server/internal/metadata"
)

func TestCommercialSettingsBackgroundCoalescing(t *testing.T) {
	db, ids := updateFixture(t)
	settings, err := commercial.GetSettings(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	settings.VolumeOffsetDB = -3
	if err = SaveCommercialSettings(t.Context(), db, settings); err != nil {
		t.Fatal(err)
	}
	state, _ := GetUpdateStatus(t.Context(), db)
	if state.Revision != 0 || pendingCount(t, db) != 0 {
		t.Fatal("volume-only edit queued schedules", state)
	}
	settings.Enabled = !settings.Enabled
	if err = SaveCommercialSettings(t.Context(), db, settings); err != nil {
		t.Fatal(err)
	}
	settings.TVCommercialsPerBreak = 3
	if err = SaveCommercialSettings(t.Context(), db, settings); err != nil {
		t.Fatal(err)
	}
	state, _ = GetUpdateStatus(t.Context(), db)
	if state.Revision != 2 || state.NeedsScan || pendingCount(t, db) != len(ids) {
		t.Fatalf("edits not merged: %+v", state)
	}
	if err = SaveCommercialSettings(t.Context(), db, settings); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := GetUpdateStatus(t.Context(), db)
	if unchanged.Revision != state.Revision {
		t.Fatal("unchanged save queued work")
	}
	scanCtx, stop, err := ScanContext(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	settings.MovieCommercialsPerBreak = 4
	if err = SaveCommercialSettings(t.Context(), db, settings); err != nil {
		t.Fatal(err)
	}
	if scanCtx.Err() != nil {
		t.Fatal("commercial settings canceled useful scan")
	}
	calls := 0
	err = processUpdateUsing(t.Context(), db, updateSteps{
		prepare: func(context.Context, *sql.DB, scanMode) (Result, error) {
			t.Fatal("commercial edit scanned media")
			return Result{}, nil
		},
		rebuild: func(context.Context, *sql.DB, []int64) error { t.Fatal("commercial edit rebuilt lineups"); return nil },
		regenerate: func(ctx context.Context, db *sql.DB, id int64, now time.Time) error {
			got, err := commercial.GetSettings(ctx, db)
			if err != nil || got != settings {
				t.Fatalf("worker used stale settings: %+v %v", got, err)
			}
			calls++
			return nil
		},
	})
	if err != nil || calls != len(ids) || pendingCount(t, db) != 0 {
		t.Fatalf("worker calls=%d err=%v", calls, err)
	}
}

func TestCommercialSettingsQueueFailureRollsBackPreferences(t *testing.T) {
	db, _ := updateFixture(t)
	before, err := commercial.GetSettings(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TRIGGER reject_commercial_work BEFORE INSERT ON library_update_channels BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`); err != nil {
		t.Fatal(err)
	}
	next := before
	next.Enabled = !before.Enabled
	if err = SaveCommercialSettings(t.Context(), db, next); err == nil {
		t.Fatal("accepted failed queue")
	}
	got, err := commercial.GetSettings(t.Context(), db)
	state, _ := GetUpdateStatus(t.Context(), db)
	if err != nil || got != before || state.Revision != 0 {
		t.Fatalf("partial save: %+v %+v %v", got, state, err)
	}
}

func TestSettingsWorkMergesChannelChangesAndCancelsObsoleteGeneration(t *testing.T) {
	db, ids := updateFixture(t)
	mustChoices(t, db, PathChoices{Kind: "movies", Path: "/movies", Movies: true})
	started := make(chan struct{})
	finished := make(chan error, 1)
	steps := realSteps()
	steps.regenerate = func(ctx context.Context, _ *sql.DB, _ int64, _ time.Time) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	go func() { finished <- processUpdateUsing(t.Context(), db, steps) }()
	<-started
	settings, err := commercial.GetSettings(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	settings.Enabled = !settings.Enabled
	if err = SaveCommercialSettings(t.Context(), db, settings); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-finished:
		if err != context.Canceled {
			t.Fatalf("old generation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("commercial save did not cancel stale work")
	}
	// A later channel-specific override supersedes just that channel, preserving global settings.
	err = MutateSettings(t.Context(), db, func(tx *sql.Tx) (SettingsWork, error) {
		disabled := false
		err := commercial.SaveChannelSettings(t.Context(), tx, ids[0], commercial.ChannelSettings{Enabled: &disabled, Folders: []string{}})
		return SettingsWork{Changed: true, Channels: []int64{ids[0]}}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	steps.regenerate = func(ctx context.Context, db *sql.DB, id int64, _ time.Time) error {
		got, err := commercial.GetSettings(ctx, db)
		if err != nil || got != settings {
			t.Fatalf("global edit lost: %+v %v", got, err)
		}
		policy, err := commercial.GetChannelPolicy(ctx, db, ids[0])
		if err != nil || policy.Enabled == nil || *policy.Enabled {
			t.Fatalf("overlap lost: %+v %v", policy, err)
		}
		calls++
		return nil
	}
	if err = processUpdateUsing(t.Context(), db, steps); err != nil || calls != len(ids) {
		t.Fatalf("merged work: calls=%d err=%v", calls, err)
	}
}

func TestMetadataWorkIsDurableAndUsesLatestSettingsWithoutScan(t *testing.T) {
	db, ids := updateFixture(t)
	scanCtx, stop, err := ScanContext(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	for _, key := range []string{"old-provider", "latest-provider"} {
		err = MutateSettings(t.Context(), db, func(tx *sql.Tx) (SettingsWork, error) {
			err := metadata.SaveSettings(t.Context(), tx, metadata.Settings{TMDbAPIKey: key})
			return SettingsWork{Changed: true, Rebuild: true, Metadata: true}, err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if scanCtx.Err() == nil {
		t.Fatal("provider change did not cancel obsolete enrichment")
	}
	state, err := GetUpdateStatus(t.Context(), db)
	if err != nil || !state.NeedsMetadata || state.NeedsScan || state.Revision != 2 {
		t.Fatalf("metadata work not durable: %+v %v", state, err)
	}
	prepares, calls := 0, 0
	steps := realSteps()
	steps.prepare = func(ctx context.Context, db *sql.DB, mode scanMode) (Result, error) {
		if mode != scanNone {
			t.Fatal("provider change forced filesystem scan")
		}
		settings, err := metadata.GetSettings(ctx, db)
		if err != nil || settings.TMDbAPIKey != "latest-provider" {
			t.Fatalf("stale provider: %+v %v", settings, err)
		}
		prepares++
		return Result{}, nil
	}
	steps.regenerate = func(context.Context, *sql.DB, int64, time.Time) error { calls++; return nil }
	if err = processUpdateUsing(t.Context(), db, steps); err != nil || prepares != 1 || calls != len(ids) {
		t.Fatalf("metadata repeats: prepares=%d calls=%d err=%v", prepares, calls, err)
	}
	state, err = GetUpdateStatus(t.Context(), db)
	if err != nil || state.NeedsMetadata || state.AppliedRevision != 2 {
		t.Fatalf("metadata not applied: %+v %v", state, err)
	}
}
