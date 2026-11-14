package refresh

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/commercial"
	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/lineup"
	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
	"github.com/alreadyonapp/alreadyon/server/internal/storage"
	"github.com/alreadyonapp/alreadyon/server/internal/workrevision"
)

// Synthetic settings and channels keep bulk/cancellation tests independent of media or accounts.
func updateFixture(t *testing.T) (*sql.DB, []int64) {
	t.Helper()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { updateSignals.Delete(db.DB); db.Close() })
	if _, err = library.SaveFolderSettings(t.Context(), db.DB, library.FolderSettings{TV: []string{"/tv"}, Movie: []string{"/movies"}, Commercial: []string{"/ads", "/other-ads"}}); err != nil {
		t.Fatal(err)
	}
	ids := []int64{}
	for _, number := range []int{133, 121, 200, 998} {
		var id int64
		if err := db.QueryRow(`INSERT INTO channels(number,name) VALUES(?,'Synthetic') RETURNING id`, number).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		if number == 998 {
			continue
		}
		mediaType := "movie"
		if number == 121 {
			mediaType = "tv"
		}
		paths := []string{"/tv"}
		if _, err := channel.SetFilter(t.Context(), db.DB, id, channel.Filter{AudienceTier: "adult", MediaType: mediaType, Genres: []string{"comedy"}, FilterExtra: channel.FilterExtra{IncludedPaths: &paths}}); err != nil {
			t.Fatal(err)
		}
	}
	return db.DB, ids
}
func mustChoices(t *testing.T, db *sql.DB, c PathChoices) UpdateStatus {
	t.Helper()
	s, err := SavePathChoices(t.Context(), db, c)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func pendingCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM library_update_channels`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func realSteps() updateSteps {
	return updateSteps{prepare: prepareLocked, rebuild: lineup.BuildFilteredChannels, regenerate: schedule.Regenerate}
}

func TestPathChoicesMergeLatestWithoutRepeatingUnchangedChannels(t *testing.T) {
	db, ids := updateFixture(t)
	choice := PathChoices{Kind: "movies", Path: "/movies", Movies: true}
	s := mustChoices(t, db, choice)
	if s.Revision != 1 || pendingCount(t, db) != 1 {
		t.Fatalf("unexpected bulk work %+v", s)
	}
	if same := mustChoices(t, db, choice); same.Revision != s.Revision {
		t.Fatal("no-op queued work")
	}
	mustChoices(t, db, PathChoices{Kind: "tv", Path: "/tv", TV: true})
	mustChoices(t, db, PathChoices{Kind: "movies", Path: "/movies"})
	filters, err := channel.ListFilters(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	if len(*filters[ids[0]].IncludedPaths) != 0 || len(*filters[ids[2]].IncludedPaths) != 0 {
		t.Fatal("latest path choices lost")
	}
	if !reflect.DeepEqual(*filters[ids[1]].IncludedPaths, []string{"/tv"}) {
		t.Fatal("independent TV choice lost")
	}
	if filters[ids[0]].Genres[0] != "comedy" {
		t.Fatal("unrelated rule changed")
	}
	if pendingCount(t, db) != 2 {
		t.Fatal("dirty sets did not merge")
	}
	var builds, regenerations int
	steps := realSteps()
	steps.rebuild = func(ctx context.Context, db *sql.DB, ids []int64) error {
		builds++
		return lineup.BuildFilteredChannels(ctx, db, ids)
	}
	steps.regenerate = func(ctx context.Context, db *sql.DB, id int64, now time.Time) error {
		regenerations++
		return schedule.Regenerate(ctx, db, id, now)
	}
	if err := processUpdateUsing(t.Context(), db, steps); err != nil {
		t.Fatal(err)
	}
	if builds != 1 || regenerations != 2 {
		t.Fatalf("repeated work: catalog batches=%d schedules=%d", builds, regenerations)
	}
	done, _ := GetUpdateStatus(t.Context(), db)
	if done.State != "idle" || done.AppliedRevision != done.Revision || pendingCount(t, db) != 0 {
		t.Fatalf("not completed: %+v", done)
	}
}

func TestNewPreferencesCancelObsoleteUpdateAndRetainPendingPaths(t *testing.T) {
	db, _ := updateFixture(t)
	mustChoices(t, db, PathChoices{Kind: "movies", Path: "/movies", Movies: true})
	started := make(chan struct{})
	finished := make(chan error, 1)
	steps := realSteps()
	steps.rebuild = func(ctx context.Context, _ *sql.DB, _ []int64) error { close(started); <-ctx.Done(); return ctx.Err() }
	go func() { finished <- processUpdateUsing(t.Context(), db, steps) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	mustChoices(t, db, PathChoices{Kind: "tv", Path: "/tv", TV: true})
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("old work: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("obsolete job not canceled")
	}
	if pendingCount(t, db) != 2 {
		t.Fatal("cancellation lost an earlier path's dirty channel")
	}
	if err := processUpdate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	done, _ := GetUpdateStatus(t.Context(), db)
	if done.State != "idle" || done.NeedsScan {
		t.Fatalf("selection-only edit rescanned or did not finish %+v", done)
	}
}

func TestDiscoverySurvivesPreferenceEditButFolderEditCancelsIt(t *testing.T) {
	db, _ := updateFixture(t)
	ctx, stop, err := ScanContext(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	mustChoices(t, db, PathChoices{Kind: "movies", Path: "/movies", Movies: true})
	if ctx.Err() != nil {
		t.Fatal("selection change canceled useful scan")
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := QueueFolderScan(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	NotifyUpdate(db, true)
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("folder change did not cancel old scan")
	}
}

func TestDiscoveryUsesLatestPreferencesWithoutScanningTwice(t *testing.T) {
	db, _ := updateFixture(t)
	tx, _ := db.BeginTx(t.Context(), nil)
	if err := QueueFolderScan(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	var scans int
	steps := realSteps()
	steps.prepare = func(ctx context.Context, _ *sql.DB, _ scanMode) (Result, error) {
		scans++
		close(started)
		select {
		case <-release:
			return Result{}, ctx.Err()
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
	go func() { done <- processUpdateUsing(t.Context(), db, steps) }()
	<-started
	wanted := mustChoices(t, db, PathChoices{Kind: "movies", Path: "/movies", Movies: true})
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	actual, _ := GetUpdateStatus(t.Context(), db)
	if scans != 1 || actual.NeedsScan || actual.AppliedRevision != wanted.Revision {
		t.Fatalf("did not fold latest preference into scan: %+v scans=%d", actual, scans)
	}
}

func TestFailureRetainsSavedChoicesAndRetryCompletes(t *testing.T) {
	db, ids := updateFixture(t)
	mustChoices(t, db, PathChoices{Kind: "movies", Path: "/movies", Movies: true})
	steps := realSteps()
	steps.rebuild = func(context.Context, *sql.DB, []int64) error { return errors.New("synthetic failure") }
	if err := processUpdateUsing(t.Context(), db, steps); err == nil {
		t.Fatal("expected failure")
	}
	state, _ := GetUpdateStatus(t.Context(), db)
	if state.State != "failed" || state.Error == "" || pendingCount(t, db) == 0 {
		t.Fatalf("failure lost pending work: %+v", state)
	}
	f, _ := channel.GetFilter(t.Context(), db, ids[0])
	if !reflect.DeepEqual(*f.IncludedPaths, []string{"/tv", "/movies"}) {
		t.Fatal("failure reverted saved choices")
	}
	if _, err := RetryUpdates(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if err := processUpdate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	state, _ = GetUpdateStatus(t.Context(), db)
	if state.State != "idle" {
		t.Fatal(state)
	}
}

func TestBulkChoiceRollbackIsAtomic(t *testing.T) {
	db, ids := updateFixture(t)
	if _, err := db.Exec(`CREATE TRIGGER reject_custom BEFORE UPDATE ON channel_filters WHEN OLD.channel_id=` + fmtID(ids[2]) + ` BEGIN SELECT RAISE(ABORT,'synthetic write failure'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := SavePathChoices(t.Context(), db, PathChoices{Kind: "movies", Path: "/movies", Movies: true, TV: true, Custom: true})
	if err == nil {
		t.Fatal("expected rejected transaction")
	}
	f, _ := channel.GetFilter(t.Context(), db, ids[0])
	state, _ := GetUpdateStatus(t.Context(), db)
	if !reflect.DeepEqual(*f.IncludedPaths, []string{"/tv"}) || state.Revision != 0 || pendingCount(t, db) != 0 {
		t.Fatal("partial preference or job commit")
	}
}
func fmtID(id int64) string { return strconv.FormatInt(id, 10) }

func TestCommercialBulkPreservesEnablementAndExplicitEmptySelection(t *testing.T) {
	db, ids := updateFixture(t)
	for _, path := range []string{"/ads", "/other-ads"} {
		mustChoices(t, db, PathChoices{Kind: "commercials", Path: path, Movies: true, TV: true})
	}
	for _, id := range ids[2:] {
		p, err := commercial.GetChannelPolicy(t.Context(), db, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(p.EffectiveFolders) != 0 || !p.ExplicitFolders || p.Enabled != nil {
			t.Fatalf("commercial overrides %+v", p)
		}
	}
	if err := processUpdate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
}

func waitApplied(t *testing.T, db *sql.DB) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		state, err := GetUpdateStatus(t.Context(), db)
		if err != nil {
			t.Fatal(err)
		}
		if state.State == "idle" && state.Revision == state.AppliedRevision {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("pending: %+v", state)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
func TestWorkerDebouncesAndRecoversPersistedRunningWork(t *testing.T) {
	db, _ := updateFixture(t)
	mustChoices(t, db, PathChoices{Kind: "movies", Path: "/movies", Movies: true})
	mustChoices(t, db, PathChoices{Kind: "tv", Path: "/tv", TV: true})
	if _, err := db.Exec(`UPDATE library_update_state SET state='updating'`); err != nil {
		t.Fatal(err)
	}
	// Reopen the actual temporary database, discarding every in-memory signal.
	var seq int
	var name, file string
	if err := db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &file); err != nil {
		t.Fatal(err)
	}
	updateSignals.Delete(db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.Open(filepath.Dir(file))
	if err != nil {
		t.Fatal(err)
	}
	db = reopened.DB
	t.Cleanup(func() { db.Close() })
	var runs atomic.Int32
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runUpdateWorker(ctx, db, func(ctx context.Context, db *sql.DB) error { runs.Add(1); return processUpdate(ctx, db) })
	}()
	t.Cleanup(func() { cancel(); <-done })
	waitApplied(t, db)
	if runs.Load() != 1 {
		t.Fatalf("expected one coalesced replay, got %d", runs.Load())
	}
}

// Modal counts reflect stored path checkboxes, including legacy exclusions,
// explicit empty lists and commercial selections with breaks switched off.
func TestPathChannelCountsFollowSavedSelections(t *testing.T) {
	db, ids := updateFixture(t)
	counts, err := GetPathChannelCounts(t.Context(), db, "tv", "/tv")
	if err != nil {
		t.Fatal(err)
	}
	if counts != (PathChannelCounts{Movies: 1, TV: 1, Custom: 1}) {
		t.Fatalf("initial counts %+v", counts)
	}
	mustChoices(t, db, PathChoices{Kind: "movies", Path: "/movies", Movies: true})
	counts, err = GetPathChannelCounts(t.Context(), db, "movies", "/movies")
	if err != nil {
		t.Fatal(err)
	}
	if counts != (PathChannelCounts{Movies: 1}) {
		t.Fatalf("saved but not yet applied count %+v", counts)
	}
	f, err := channel.GetFilter(t.Context(), db, ids[1])
	if err != nil {
		t.Fatal(err)
	}
	f.IncludedPaths = nil
	f.ExcludedPaths = []string{"/tv/"}
	if _, err := channel.SetFilter(t.Context(), db, ids[1], *f); err != nil {
		t.Fatal(err)
	}
	counts, err = GetPathChannelCounts(t.Context(), db, "tv", "/tv")
	if err != nil {
		t.Fatal(err)
	}
	if counts.TV != 0 {
		t.Fatal("legacy exclusion counted as selected")
	}
	off := false
	if err := commercial.SaveChannelSettings(t.Context(), db, ids[0], commercial.ChannelSettings{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	counts, err = GetPathChannelCounts(t.Context(), db, "commercials", "/ads")
	if err != nil {
		t.Fatal(err)
	}
	if counts != (PathChannelCounts{Movies: 1, TV: 1, Custom: 2}) {
		t.Fatalf("commercial folder selections %+v", counts)
	}
	mustChoices(t, db, PathChoices{Kind: "commercials", Path: "/ads", Movies: true, TV: true})
	counts, err = GetPathChannelCounts(t.Context(), db, "commercials", "/ads")
	if err != nil {
		t.Fatal(err)
	}
	if counts.Custom != 0 {
		t.Fatal("disabled custom path still counted")
	}
	if _, err := GetPathChannelCounts(t.Context(), db, "movies", "/unconfigured"); !errors.Is(err, ErrInvalidPathSelection) {
		t.Fatal("unconfigured counts accepted")
	}
}

// Obsolete derivations roll back, and applying the latest path selection never
// removes or rewrites the program a viewer is already watching.
func TestLatestPathUpdatePreservesCurrentAiringAndFencesStaleWrites(t *testing.T) {
	db, ids := updateFixture(t)
	var mediaID int64
	if err := db.QueryRow(`INSERT INTO media_files(root_kind,path,size_bytes,modified_unix,first_seen_unix,last_seen_unix,duration_seconds,status) VALUES('media','/movies/one.mkv',1,1,1,1,600,'ok') RETURNING id`).Scan(&mediaID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO identified_items(media_file_id,media_type,title,genre_tags) VALUES(?,'movie','Synthetic','["comedy"]')`, mediaID); err != nil {
		t.Fatal(err)
	}
	old := mustChoices(t, db, PathChoices{Kind: "movies", Path: "/movies", Movies: true})
	if err := processUpdate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	before, _, err := schedule.Current(t.Context(), db, schedule.FixedClock{Time: now}, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if before.MediaFileID != mediaID {
		t.Fatalf("no active synthetic airing %+v", before)
	}
	mustChoices(t, db, PathChoices{Kind: "movies", Path: "/movies"})
	stale := workrevision.With(t.Context(), old.Revision)
	if err := lineup.BuildFilteredChannels(stale, db, []int64{ids[0]}); !errors.Is(err, context.Canceled) {
		t.Fatalf("stale lineup committed: %v", err)
	}
	var items int
	if err := db.QueryRow(`SELECT count(*) FROM channel_items WHERE channel_id=?`, ids[0]).Scan(&items); err != nil || items != 1 {
		t.Fatal("stale lineup did not roll back", err, items)
	}
	if err := schedule.Regenerate(stale, db, ids[0], now); !errors.Is(err, context.Canceled) {
		t.Fatalf("stale schedule committed: %v", err)
	}
	if err := processUpdate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	after, _, err := schedule.Current(t.Context(), db, schedule.FixedClock{Time: now}, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("current airing changed: before=%+v after=%+v", before, after)
	}
	var future int
	if err := db.QueryRow(`SELECT count(*) FROM schedule_entries WHERE channel_id=? AND starts_at_unix>?`, ids[0], now.Unix()).Scan(&future); err != nil || future != 0 {
		t.Fatal("disabled path remains in future schedule", err, future)
	}
}

// New installs can add a root and customize it before discovery has seeded the defaults.
func TestCustomizeBeforeFirstScanInitializesDefaultFilters(t *testing.T) {
	db, ids := updateFixture(t)
	if _, err := db.Exec(`DELETE FROM channel_filters WHERE channel_id IN (?,?)`, ids[0], ids[1]); err != nil {
		t.Fatal(err)
	}
	mustChoices(t, db, PathChoices{Kind: "movies", Path: "/movies"})
	filters, err := channel.ListFilters(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids[:2] {
		f, ok := filters[id]
		if !ok || f.IncludedPaths == nil || slices.Contains(*f.IncludedPaths, "/movies") {
			t.Fatalf("early preference lost: %+v", f)
		}
	}
	if pendingCount(t, db) != 2 {
		t.Fatal("newly initialized defaults were not queued")
	}
	if _, ok := filters[ids[3]]; ok {
		t.Fatal("unfiltered custom channel was converted")
	}
}

func TestFirstFolderScanSeedsDefaultsWithoutExtraRebuildPass(t *testing.T) {
	db, ids := updateFixture(t)
	var legacy int64
	if err := db.QueryRow(`INSERT INTO channels(number,name,visibility,hidden) VALUES(180,'Legacy','show',0) RETURNING id`).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM channel_filters WHERE channel_id IN (?,?)`, ids[0], ids[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := library.SaveFolderSettings(t.Context(), db, library.FolderSettings{TV: []string{}, Movie: []string{}, Commercial: []string{}}); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := QueueFolderScan(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var builds int
	steps := realSteps()
	steps.rebuild = func(ctx context.Context, db *sql.DB, ids []int64) error {
		builds++
		return lineup.BuildFilteredChannels(ctx, db, ids)
	}
	if err := processUpdateUsing(t.Context(), db, steps); err != nil {
		t.Fatal(err)
	}
	filters, err := channel.ListFilters(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := filters[ids[0]]; !ok {
		t.Fatal("movie default not initialized")
	}
	if _, ok := filters[ids[1]]; !ok {
		t.Fatal("TV default not initialized")
	}
	if builds != 1 {
		t.Fatalf("repeated catalog rebuild: %d", builds)
	}
	var hidden bool
	if err := db.QueryRow(`SELECT hidden FROM channels WHERE id=?`, legacy).Scan(&hidden); err != nil || !hidden {
		t.Fatal("full scan did not reconcile retired provider visibility", err)
	}
}

// A new edit retains unfinished work, reuses committed lineups, and does not
// redo a channel whose settings and schedule were already fully applied.
func TestSupersedingEditKeepsOnlyUnfinishedChannels(t *testing.T) {
	db, ids := updateFixture(t)
	mustChoices(t, db, PathChoices{Kind: "movies", Path: "/movies", Movies: true, TV: true})
	started := make(chan struct{})
	done := make(chan error, 1)
	calls := map[int64]int{}
	steps := realSteps()
	steps.regenerate = func(ctx context.Context, db *sql.DB, id int64, now time.Time) error {
		calls[id]++
		if id == ids[1] {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}
		return schedule.Regenerate(ctx, db, id, now)
	}
	go func() { done <- processUpdateUsing(t.Context(), db, steps) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not reach second channel")
	}
	mustChoices(t, db, PathChoices{Kind: "commercials", Path: "/ads", Movies: true, TV: true})
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("obsolete work result %v", err)
	}
	var completed int
	if err := db.QueryRow(`SELECT count(*) FROM library_update_channels WHERE channel_id=?`, ids[0]).Scan(&completed); err != nil || completed != 0 {
		t.Fatal("completed channel remained dirty", err)
	}
	steps = realSteps()
	steps.rebuild = func(context.Context, *sql.DB, []int64) error { t.Error("committed lineups rebuilt again"); return nil }
	steps.regenerate = func(ctx context.Context, db *sql.DB, id int64, now time.Time) error {
		calls[id]++
		return schedule.Regenerate(ctx, db, id, now)
	}
	if err := processUpdateUsing(t.Context(), db, steps); err != nil {
		t.Fatal(err)
	}
	if calls[ids[0]] != 1 {
		t.Fatalf("completed unaffected channel repeated %d times", calls[ids[0]])
	}
	if pendingCount(t, db) != 0 {
		t.Fatal("unfinished channels were lost")
	}
}
