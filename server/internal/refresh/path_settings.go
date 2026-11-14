package refresh

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/commercial"
	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/lineup"
)

var ErrInvalidPathSelection = errors.New("select a configured movie, TV or commercial folder")

// PathChoices patches only one folder in current server-side channel settings.
// Other paths and rules are read inside the transaction, never from a stale UI snapshot.
type PathChoices struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Movies bool   `json:"movies"`
	TV     bool   `json:"tv"`
	Custom bool   `json:"custom"`
}

func pathGroup(c channel.Channel, f *channel.Filter) string {
	if !channel.IsBuiltInNumber(c.Number) {
		return "custom"
	}
	if f == nil {
		return ""
	}
	if f.MediaType == "movie" {
		return "movies"
	}
	return "tv"
}
func (p PathChoices) enabled(c channel.Channel, f *channel.Filter) (bool, bool) {
	switch pathGroup(c, f) {
	case "movies":
		return p.Movies, true
	case "tv":
		return p.TV, true
	case "custom":
		return p.Custom, true
	default:
		return false, false
	}
}

// SavePathChoices atomically persists the whole bulk edit and durable dirty set.
// It does no scanning, lineup loading or schedule generation on the request path.
func SavePathChoices(ctx context.Context, db *sql.DB, choice PathChoices) (UpdateStatus, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return UpdateStatus{}, err
	}
	defer tx.Rollback()
	folders, err := library.GetFolderSettings(ctx, tx)
	if err != nil {
		return UpdateStatus{}, err
	}
	effective := folders.Effective()
	var available []string
	switch choice.Kind {
	case "movies":
		available = effective.Movie.Values
	case "tv":
		available = effective.TV.Values
	case "commercials":
		available = effective.Commercial.Values
	default:
		return UpdateStatus{}, ErrInvalidPathSelection
	}
	if !slices.Contains(available, choice.Path) {
		return UpdateStatus{}, ErrInvalidPathSelection
	}
	paths := slices.Clone(effective.TV.Values)
	for _, p := range effective.Movie.Values {
		if !slices.Contains(paths, p) {
			paths = append(paths, p)
		}
	}
	channels, err := channel.List(ctx, tx)
	if err != nil {
		return UpdateStatus{}, err
	}
	filters, err := channel.ListSavedFilters(ctx, tx)
	if err != nil {
		return UpdateStatus{}, err
	}
	// Newly added folders can be customized before the first scan has seeded filters.
	// Only known built-in defaults are filled; existing rules and manual channels remain intact.
	previous := filters
	seeded, err := lineup.SeedDefaultFilters(ctx, tx)
	if err != nil {
		return UpdateStatus{}, err
	}
	if seeded > 0 {
		filters, err = channel.ListSavedFilters(ctx, tx)
		if err != nil {
			return UpdateStatus{}, err
		}
	}
	changed := []int64{}
	for _, c := range channels {
		if _, wasPresent := previous[c.ID]; !wasPresent {
			if _, exists := filters[c.ID]; exists {
				changed = append(changed, c.ID)
			}
		}
	}
	for _, c := range channels {
		var f *channel.Filter
		if filter, ok := filters[c.ID]; ok {
			f = &filter
		}
		enabled, applies := choice.enabled(c, f)
		if !applies {
			continue
		}
		if choice.Kind == "commercials" {
			policy, err := commercial.GetChannelPolicy(ctx, tx, c.ID)
			if err != nil {
				return UpdateStatus{}, err
			}
			if slices.Contains(policy.EffectiveFolders, choice.Path) == enabled {
				continue
			}
			next := setPath(policy.EffectiveFolders, choice.Path, enabled)
			if err := commercial.SaveChannelSettings(ctx, tx, c.ID, commercial.ChannelSettings{Enabled: policy.Enabled, Folders: next, ExplicitFolders: true}); err != nil {
				return UpdateStatus{}, err
			}
		} else {
			if f == nil {
				continue
			}
			selected := selectedPaths(*f, paths)
			if slices.Contains(selected, choice.Path) == enabled {
				continue
			}
			next := setPath(selected, choice.Path, enabled)
			if err := channel.SetIncludedPaths(ctx, tx, c.ID, *f, next); err != nil {
				return UpdateStatus{}, err
			}
		}
		if !slices.Contains(changed, c.ID) {
			changed = append(changed, c.ID)
		}
	}
	if len(changed) > 0 {
		if err := queueChannels(ctx, tx, changed, choice.Kind != "commercials" || seeded > 0); err != nil {
			return UpdateStatus{}, err
		}
	}
	result, err := GetUpdateStatus(ctx, tx)
	if err != nil {
		return UpdateStatus{}, err
	}
	if err := tx.Commit(); err != nil {
		return UpdateStatus{}, err
	}
	if len(changed) > 0 {
		NotifyUpdate(db, false)
	}
	return result, nil
}

func selectedPaths(f channel.Filter, paths []string) []string {
	if f.IncludedPaths != nil {
		return slices.Clone(*f.IncludedPaths)
	}
	out := []string{}
	for _, p := range paths {
		excluded := false
		for _, root := range f.ExcludedPaths {
			root = filepath.Clean(root)
			if p == root || strings.HasPrefix(p, strings.TrimSuffix(root, "/")+"/") {
				excluded = true
				break
			}
		}
		if !excluded {
			out = append(out, p)
		}
	}
	return out
}
func setPath(selected []string, path string, enabled bool) []string {
	out := []string{}
	for _, p := range selected {
		if p != path {
			out = append(out, p)
		}
	}
	if enabled {
		out = append(out, path)
	}
	return out
}

func queueChannels(ctx context.Context, tx *sql.Tx, ids []int64, rebuild bool) error {
	var revision int64
	if err := tx.QueryRowContext(ctx, `UPDATE library_update_state SET revision=revision+1,state='queued',processed=0,error='' WHERE id=1 RETURNING revision`).Scan(&revision); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT INTO library_update_channels(channel_id,rebuild,revision) VALUES(?,?,?) ON CONFLICT(channel_id) DO UPDATE SET rebuild=max(rebuild,excluded.rebuild),revision=excluded.revision`, id, rebuild, revision); err != nil {
			return fmt.Errorf("queue channel: %w", err)
		}
	}
	return nil
}

// QueueFolderScan belongs to the folder-save transaction, so restart recovery
// cannot lose a successful folder edit between committing and waking the worker.
func QueueFolderScan(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `UPDATE library_update_state SET revision=revision+1,scan_revision=scan_revision+1,needs_scan=1,state='queued',processed=0,error='' WHERE id=1`)
	return err
}

// PathChannelCounts reports saved checkbox selections, independent of whether
// matching content exists or commercial breaks are currently switched on.
type PathChannelCounts struct {
	Movies int `json:"movies"`
	TV     int `json:"tv"`
	Custom int `json:"custom"`
}

func GetPathChannelCounts(ctx context.Context, db *sql.DB, kind, path string) (PathChannelCounts, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return PathChannelCounts{}, err
	}
	defer tx.Rollback()
	folders, err := library.GetFolderSettings(ctx, tx)
	if err != nil {
		return PathChannelCounts{}, err
	}
	effective := folders.Effective()
	var available []string
	switch kind {
	case "movies":
		available = effective.Movie.Values
	case "tv":
		available = effective.TV.Values
	case "commercials":
		available = effective.Commercial.Values
	default:
		return PathChannelCounts{}, ErrInvalidPathSelection
	}
	if !slices.Contains(available, path) {
		return PathChannelCounts{}, ErrInvalidPathSelection
	}
	paths := append(slices.Clone(effective.TV.Values), effective.Movie.Values...)
	channels, err := channel.List(ctx, tx)
	if err != nil {
		return PathChannelCounts{}, err
	}
	filters, err := channel.ListSavedFilters(ctx, tx)
	if err != nil {
		return PathChannelCounts{}, err
	}
	counts := PathChannelCounts{}
	for _, c := range channels {
		var f *channel.Filter
		if filter, ok := filters[c.ID]; ok {
			f = &filter
		}
		group := pathGroup(c, f)
		if group == "" {
			continue
		}
		var selected bool
		if kind == "commercials" {
			policy, err := commercial.GetChannelPolicy(ctx, tx, c.ID)
			if err != nil {
				return counts, err
			}
			selected = slices.Contains(policy.EffectiveFolders, path)
		} else if f != nil {
			selected = slices.Contains(selectedPaths(*f, paths), path)
		}
		if selected {
			switch group {
			case "movies":
				counts.Movies++
			case "tv":
				counts.TV++
			case "custom":
				counts.Custom++
			}
		}
	}
	return counts, tx.Commit()
}
