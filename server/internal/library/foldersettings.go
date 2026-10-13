package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/alreadyonapp/alreadyon/server/internal/config"
)

// Built-in defaults for the content folders and scan exclusions, used
// until an admin saves their own (#114). All lowercase to match the
// documented container mounts.
var (
	DefaultTVPaths         = []string{"/data/media/tv_shows"}
	DefaultMoviePaths      = []string{"/data/media/movies"}
	DefaultCommercialPaths = []string{"/data/media/commercials"}
	// Patterns are filepath.Match globs against a file or directory's base
	// name (see Scanner.Exclude), so no trailing slashes.
	DefaultExclusionPatterns = []string{"*.sample.*", "sample", "extras"}
)

const (
	maxFolderPaths       = 50
	maxExclusionPatterns = 100
	maxPathLength        = 4096
	maxPatternLength     = 200
)

// FolderSource says where an effective list comes from.
type FolderSource string

const (
	SourceDefault FolderSource = "default"
	SourceAdmin   FolderSource = "admin"
)

// FolderSettings is what an admin has saved. A nil list means "not set,
// use the default"; a non-nil empty list is an explicit "none".
type FolderSettings struct {
	TV, Movie, Commercial, Exclusions []string
}

// EffectiveList is one list after applying defaults.
type EffectiveList struct {
	Values  []string
	Default []string
	Source  FolderSource
}

// EffectiveFolders is FolderSettings with defaults applied.
type EffectiveFolders struct {
	TV, Movie, Commercial, Exclusions EffectiveList
}

// Effective applies the built-in defaults to anything the admin hasn't set.
func (s FolderSettings) Effective() EffectiveFolders {
	return EffectiveFolders{
		TV:         effective(s.TV, DefaultTVPaths),
		Movie:      effective(s.Movie, DefaultMoviePaths),
		Commercial: effective(s.Commercial, DefaultCommercialPaths),
		Exclusions: effective(s.Exclusions, DefaultExclusionPatterns),
	}
}

// ValidateRoots resolves the effective folders into scanner roots. TV
// and movie folders are both media roots (the scanner identifies content
// by path and name, not by which list it came from); a folder listed in
// both is scanned once. Every problem is returned, valid roots still work.
func (e EffectiveFolders) ValidateRoots() ([]config.ValidatedRoot, []error) {
	mediaPaths := append(append([]string{}, e.TV.Values...), e.Movie.Values...)
	media, errs := config.ValidateRoots(config.RootKindMedia, dedupe(mediaPaths))
	commercial, commercialErrs := config.ValidateRoots(config.RootKindCommercial, e.Commercial.Values)
	return append(media, commercial...), append(errs, commercialErrs...)
}

// ScanConfig is everything a scan needs, resolved from the folder
// settings as they are right now. Callers load it per scan instead of
// holding on to roots, so a change an admin saves is picked up by the
// next scan without a restart.
type ScanConfig struct {
	Folders EffectiveFolders
	Roots   []config.ValidatedRoot
	// Problems lists folders that couldn't be used (missing, unreadable);
	// the usable ones are still in Roots.
	Problems []error
}

// LoadScanConfig reads the saved folder settings and validates them.
func LoadScanConfig(ctx context.Context, db *sql.DB) (ScanConfig, error) {
	saved, err := GetFolderSettings(ctx, db)
	if err != nil {
		return ScanConfig{}, err
	}
	folders := saved.Effective()
	roots, problems := folders.ValidateRoots()
	return ScanConfig{Folders: folders, Roots: roots, Problems: problems}, nil
}

// MediaRootCount is how many usable TV/movie folders are configured.
func (c ScanConfig) MediaRootCount() int {
	n := 0
	for _, r := range c.Roots {
		if r.Kind == config.RootKindMedia {
			n++
		}
	}
	return n
}

// NewScanner returns a Scanner for these roots and exclusion patterns.
func (c ScanConfig) NewScanner(db *sql.DB) *Scanner {
	return &Scanner{DB: db, Roots: c.Roots, Exclude: c.Folders.Exclusions.Values, MinMediaSeconds: MinMediaRuntimeSeconds}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, p := range in {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

func effective(saved, def []string) EffectiveList {
	if saved == nil {
		return EffectiveList{Values: append([]string{}, def...), Default: def, Source: SourceDefault}
	}
	return EffectiveList{Values: saved, Default: def, Source: SourceAdmin}
}

var (
	ErrInvalidFolderPath = errors.New("library: invalid folder path")
	ErrInvalidPattern    = errors.New("library: invalid exclusion pattern")
	ErrTooManyEntries    = errors.New("library: too many entries")
)

// GetFolderSettings returns what the admin has saved (nil lists where
// nothing is saved). The singleton row is seeded by migration 0022.
func GetFolderSettings(ctx context.Context, db *sql.DB) (FolderSettings, error) {
	var tv, movie, commercial, exclusions sql.NullString
	err := db.QueryRowContext(ctx, `
		SELECT tv_paths, movie_paths, commercial_paths, exclusion_patterns
		FROM folder_settings WHERE id = 1`).Scan(&tv, &movie, &commercial, &exclusions)
	if err != nil {
		return FolderSettings{}, fmt.Errorf("library: get folder settings: %w", err)
	}
	var s FolderSettings
	for _, f := range []struct {
		raw sql.NullString
		dst *[]string
	}{{tv, &s.TV}, {movie, &s.Movie}, {commercial, &s.Commercial}, {exclusions, &s.Exclusions}} {
		if !f.raw.Valid {
			continue
		}
		list := []string{}
		if err := json.Unmarshal([]byte(f.raw.String), &list); err != nil {
			return FolderSettings{}, fmt.Errorf("library: decode folder settings: %w", err)
		}
		*f.dst = list
	}
	return s, nil
}

// SaveFolderSettings validates and stores s, replacing all four lists;
// nil lists are stored as NULL (back to the default). Callers wanting a
// partial update should read, modify, then save.
func SaveFolderSettings(ctx context.Context, db *sql.DB, s FolderSettings) (FolderSettings, error) {
	var err error
	if s.TV, err = cleanPaths(s.TV); err != nil {
		return FolderSettings{}, err
	}
	if s.Movie, err = cleanPaths(s.Movie); err != nil {
		return FolderSettings{}, err
	}
	if s.Commercial, err = cleanPaths(s.Commercial); err != nil {
		return FolderSettings{}, err
	}
	if s.Exclusions, err = cleanPatterns(s.Exclusions); err != nil {
		return FolderSettings{}, err
	}

	encode := func(list []string) (any, error) {
		if list == nil {
			return nil, nil
		}
		b, err := json.Marshal(list)
		return string(b), err
	}
	args := make([]any, 4)
	for i, list := range [][]string{s.TV, s.Movie, s.Commercial, s.Exclusions} {
		if args[i], err = encode(list); err != nil {
			return FolderSettings{}, fmt.Errorf("library: encode folder settings: %w", err)
		}
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE folder_settings SET tv_paths = ?, movie_paths = ?, commercial_paths = ?,
			exclusion_patterns = ?, updated_at_unix = unixepoch() WHERE id = 1`, args...); err != nil {
		return FolderSettings{}, fmt.Errorf("library: save folder settings: %w", err)
	}
	return s, nil
}

// cleanPaths trims, cleans and de-duplicates absolute folder paths,
// preserving order. nil stays nil.
func cleanPaths(in []string) ([]string, error) {
	if in == nil {
		return nil, nil
	}
	if len(in) > maxFolderPaths {
		return nil, fmt.Errorf("%w: at most %d folders", ErrTooManyEntries, maxFolderPaths)
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		p := strings.TrimSpace(raw)
		switch {
		case p == "" || len(p) > maxPathLength || strings.ContainsRune(p, 0):
			return nil, fmt.Errorf("%w: %q", ErrInvalidFolderPath, raw)
		case !filepath.IsAbs(p):
			return nil, fmt.Errorf("%w: %q must be an absolute path", ErrInvalidFolderPath, raw)
		}
		p = filepath.Clean(p)
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out, nil
}

// cleanPatterns trims and de-duplicates exclusion globs, rejecting any
// filepath.Match would reject. nil stays nil.
func cleanPatterns(in []string) ([]string, error) {
	if in == nil {
		return nil, nil
	}
	if len(in) > maxExclusionPatterns {
		return nil, fmt.Errorf("%w: at most %d patterns", ErrTooManyEntries, maxExclusionPatterns)
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		p := strings.TrimSpace(raw)
		if p == "" || len(p) > maxPatternLength || strings.ContainsRune(p, 0) {
			return nil, fmt.Errorf("%w: %q", ErrInvalidPattern, raw)
		}
		if _, err := filepath.Match(p, ""); err != nil {
			return nil, fmt.Errorf("%w: %q is not a valid glob", ErrInvalidPattern, raw)
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out, nil
}
