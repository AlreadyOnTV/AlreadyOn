package commercial

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/alreadyonapp/alreadyon/server/internal/library"
)

// ChannelSettings overrides enablement and folder selection only. A nil Enabled
// inherits the global setting; an empty Folders list selects all configured roots.
type ChannelSettings struct {
	Enabled *bool    `json:"enabled"`
	Folders []string `json:"folders"`
}

// ChannelPolicy includes effective values so administrators can distinguish
// inherited settings and a removed-folder fallback from explicit overrides.
type ChannelPolicy struct {
	ChannelSettings
	GlobalEnabled    bool     `json:"globalEnabled"`
	EffectiveEnabled bool     `json:"effectiveEnabled"`
	AvailableFolders []string `json:"availableFolders"`
	EffectiveFolders []string `json:"effectiveFolders"`
	FoldersFallback  bool     `json:"foldersFallback"`
}

var ErrInvalidChannelFolder = errors.New("commercial: select folders from the configured commercial folders")

// GetChannelPolicy resolves saved choices against the current global settings.
// If any selected folder was removed, the complete selection falls back to all.
func GetChannelPolicy(ctx context.Context, db *sql.DB, id int64) (ChannelPolicy, error) {
	global, err := GetSettings(ctx, db)
	if err != nil {
		return ChannelPolicy{}, err
	}
	folders, err := library.GetFolderSettings(ctx, db)
	if err != nil {
		return ChannelPolicy{}, err
	}
	p := ChannelPolicy{ChannelSettings: ChannelSettings{Folders: []string{}}, GlobalEnabled: global.Enabled,
		EffectiveEnabled: global.Enabled, AvailableFolders: folders.Effective().Commercial.Values}
	var enabled sql.NullBool
	var raw string
	err = db.QueryRowContext(ctx, `SELECT enabled, folders_json FROM channel_commercial_settings WHERE channel_id=?`, id).Scan(&enabled, &raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return p, err
	}
	if err == nil {
		if enabled.Valid {
			p.Enabled = &enabled.Bool
			p.EffectiveEnabled = enabled.Bool
		}
		if err := json.Unmarshal([]byte(raw), &p.Folders); err != nil {
			return p, err
		}
	}
	p.EffectiveFolders = append([]string{}, p.AvailableFolders...)
	if len(p.Folders) > 0 {
		available := map[string]bool{}
		for _, folder := range p.AvailableFolders {
			available[folder] = true
		}
		for _, folder := range p.Folders {
			if !available[folder] {
				p.FoldersFallback = true
			}
		}
		if !p.FoldersFallback {
			p.EffectiveFolders = append([]string{}, p.Folders...)
		}
	}
	return p, nil
}

// SaveChannelSettings replaces the override, rejecting unknown folders before
// writing. Paths are the stable references used by the global folder settings.
func SaveChannelSettings(ctx context.Context, db *sql.DB, id int64, s ChannelSettings) error {
	folders, err := library.GetFolderSettings(ctx, db)
	if err != nil {
		return err
	}
	available := map[string]bool{}
	for _, folder := range folders.Effective().Commercial.Values {
		available[folder] = true
	}
	if len(s.Folders) > 50 {
		return ErrInvalidChannelFolder
	}
	clean := []string{}
	seen := map[string]bool{}
	for _, folder := range s.Folders {
		if !available[folder] {
			return ErrInvalidChannelFolder
		}
		if !seen[folder] {
			clean = append(clean, folder)
			seen[folder] = true
		}
	}
	raw, err := json.Marshal(clean)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO channel_commercial_settings(channel_id,enabled,folders_json) VALUES(?,?,?)
 ON CONFLICT(channel_id) DO UPDATE SET enabled=excluded.enabled,folders_json=excluded.folders_json`, id, s.Enabled, string(raw))
	if err != nil {
		return fmt.Errorf("commercial: save channel settings: %w", err)
	}
	return nil
}

// IncludesPath compares directory boundaries rather than string prefixes. Scanner
// paths are canonical, so resolve configured symlink roots before comparing them.
func (p ChannelPolicy) IncludesPath(path string) bool {
	if len(p.Folders) == 0 {
		return true
	} // Preserve the existing global pool.
	for _, folder := range p.EffectiveFolders {
		if real, err := filepath.EvalSymlinks(folder); err == nil {
			folder = real
		}
		rel, err := filepath.Rel(folder, path)
		if err == nil && rel != "." && rel != ".." && !filepath.IsAbs(rel) {
			first := rel
			for i, c := range rel {
				if c == filepath.Separator {
					first = rel[:i]
					break
				}
			}
			if first != ".." {
				return true
			}
		}
	}
	return false
}
