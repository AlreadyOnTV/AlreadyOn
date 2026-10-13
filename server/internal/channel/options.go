package channel

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/alreadyonapp/alreadyon/server/internal/eligibility"
)

type FilterExtra struct {
	// ExcludedGenres takes precedence over OR-matched included genres.
	ExcludedGenres []string `json:"excludedGenres,omitempty"`
	// RequireKnownYear keeps undated titles out of restricted default channels.
	RequireKnownYear bool      `json:"requireKnownYear,omitempty"`
	AgeRestrictionID string    `json:"ageRestrictionId,omitempty"`
	Eras             []string  `json:"eras,omitempty"`
	TitlePhrases     []string  `json:"titlePhrases,omitempty"`
	IncludedPaths    *[]string `json:"includedPaths,omitempty"`
	ExcludedPaths    []string  `json:"excludedPaths,omitempty"`
}

type FilterOption struct {
	DefaultPosition int      `json:"defaultPosition"`
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Ratings         []string `json:"ratings,omitempty"`
	AllowUnknown    bool     `json:"allowUnknown,omitempty"`
	Unrestricted    bool     `json:"unrestricted,omitempty"`
	StartYear       int      `json:"startYear,omitempty"`
	EndYear         int      `json:"endYear,omitempty"`
}
type FilterCatalog map[string][]FilterOption

func ListOptions(ctx context.Context, db *sql.DB) (FilterCatalog, error) {
	rows, err := db.QueryContext(ctx, `SELECT kind,id,definition,default_position FROM channel_filter_options ORDER BY kind,position,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := FilterCatalog{"age": {}, "era": {}}
	for rows.Next() {
		var kind, id, raw string
		var defaultPosition int
		if err := rows.Scan(&kind, &id, &raw, &defaultPosition); err != nil {
			return nil, err
		}
		var option FilterOption
		if err := json.Unmarshal([]byte(raw), &option); err != nil {
			return nil, err
		}
		option.ID = id
		option.DefaultPosition = defaultPosition
		out[kind] = append(out[kind], option)
	}
	return out, rows.Err()
}

func validateOption(kind string, o FilterOption) error {
	if kind != "age" && kind != "era" {
		return fmt.Errorf("%w: invalid option type", ErrInvalidFilterOptions)
	}
	if len(o.ID) == 0 || len(o.ID) > 80 || strings.ContainsAny(o.ID, "/ \\") {
		return fmt.Errorf("%w: invalid option ID", ErrInvalidFilterOptions)
	}
	o.Name = strings.TrimSpace(o.Name)
	if len(o.Name) == 0 || len(o.Name) > 100 || strings.ContainsFunc(o.Name, unicode.IsControl) {
		return fmt.Errorf("%w: name must be 1-100 characters", ErrInvalidFilterOptions)
	}
	if len(o.Ratings) > 30 {
		return fmt.Errorf("%w: too many values", ErrInvalidFilterOptions)
	}
	for _, r := range o.Ratings {
		if !slices.Contains(eligibility.MovieCertifications, r) && !slices.Contains(eligibility.TVCertifications, r) {
			return fmt.Errorf("%w: unknown US rating %s", ErrInvalidFilterOptions, r)
		}
	}
	if kind == "era" && (o.StartYear < 0 || o.EndYear < 0 || o.StartYear > 9999 || o.EndYear > 9999 || (o.EndYear > 0 && o.StartYear > o.EndYear)) {
		return fmt.Errorf("%w: invalid year range", ErrInvalidFilterOptions)
	}
	return nil
}
func SaveOption(ctx context.Context, db *sql.DB, kind string, o FilterOption) error {
	if err := validateOption(kind, o); err != nil {
		return err
	}
	o.Name = strings.TrimSpace(o.Name)
	raw, err := json.Marshal(o)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO channel_filter_options(kind,id,definition,position,default_position) VALUES(?,?,?,(SELECT COALESCE(MAX(position),-1)+1 FROM channel_filter_options WHERE kind=?),(SELECT COALESCE(MAX(default_position),-1)+1 FROM channel_filter_options WHERE kind=?)) ON CONFLICT(kind,id) DO UPDATE SET definition=excluded.definition`, kind, o.ID, string(raw), kind, kind)
	return err
}
func DeleteOption(ctx context.Context, db *sql.DB, kind, id string) error {
	if kind != "age" && kind != "era" {
		return ErrInvalidFilterOptions
	}
	filters, err := ListFilters(ctx, db)
	if err != nil {
		return err
	}
	for _, f := range filters {
		age := f.AgeRestrictionID
		if age == "" {
			age = f.AudienceTier
		}
		eras := f.Eras
		if len(eras) == 0 {
			era := f.Era
			if era == "" {
				era = "both"
			}
			eras = []string{era}
		}
		if (kind == "age" && age == id) || (kind == "era" && slices.Contains(eras, id)) {
			return fmt.Errorf("%w: used by a channel; change that channel first", ErrInvalidFilterOptions)
		}
	}
	result, err := db.ExecContext(ctx, `DELETE FROM channel_filter_options WHERE kind=? AND id=?`, kind, id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: option not found", ErrInvalidFilterOptions)
	}
	return nil
}
func ResolveFilter(f Filter, catalog FilterCatalog) (Filter, error) {
	f.ResolvedIncludedPaths = nil
	if f.IncludedPaths != nil {
		f.ResolvedIncludedPaths = []string{}
		for _, p := range *f.IncludedPaths {
			if resolved, err := filepath.EvalSymlinks(p); err == nil {
				p = resolved
			}
			f.ResolvedIncludedPaths = append(f.ResolvedIncludedPaths, p)
		}
	}
	f.ResolvedExcludedPaths = append([]string{}, f.ExcludedPaths...)
	for _, p := range f.ExcludedPaths {
		if resolved, err := filepath.EvalSymlinks(p); err == nil && resolved != p {
			f.ResolvedExcludedPaths = append(f.ResolvedExcludedPaths, resolved)
		}
	}
	find := func(kind, id string) (FilterOption, error) {
		for _, o := range catalog[kind] {
			if o.ID == id {
				return o, nil
			}
		}
		return FilterOption{}, fmt.Errorf("%w: unknown %s %q", ErrInvalidFilterOptions, kind, id)
	}
	age := f.AgeRestrictionID
	if age == "" {
		age = f.AudienceTier
	}
	o, err := find("age", age)
	if err != nil {
		return f, err
	}
	f.AgeOption = &o
	eras := f.Eras
	if len(eras) == 0 {
		era := f.Era
		if era == "" {
			era = "both"
		}
		eras = []string{era}
	}
	f.EraOptions = nil
	for _, id := range eras {
		o, err := find("era", id)
		if err != nil {
			return f, err
		}
		f.EraOptions = append(f.EraOptions, o)
	}
	return f, nil
}
func Resolve(ctx context.Context, db *sql.DB, f Filter) (Filter, error) {
	catalog, err := ListOptions(ctx, db)
	if err != nil {
		return f, err
	}
	return ResolveFilter(f, catalog)
}
func normalizeExtra(f Filter) (Filter, error) {
	if len(f.Eras) > 100 || len(f.TitlePhrases) > 50 || len(f.ExcludedPaths) > 100 {
		return f, fmt.Errorf("%w: too many filter values", ErrInvalidFilterOptions)
	}
	phrases := []string{}
	for _, p := range f.TitlePhrases {
		p = strings.TrimSpace(p)
		if len(p) == 0 {
			continue
		}
		if len(p) > MaxTitleFilterLength || strings.ContainsFunc(p, unicode.IsControl) {
			return f, ErrInvalidTitleFilter
		}
		if !slices.Contains(phrases, p) {
			phrases = append(phrases, p)
		}
	}
	f.TitlePhrases = phrases
	if f.IncludedPaths != nil {
		if len(*f.IncludedPaths) > 100 {
			return f, ErrInvalidFilterOptions
		}
		for i, p := range *f.IncludedPaths {
			if !filepath.IsAbs(p) || len(p) > 4096 {
				return f, ErrInvalidFilterOptions
			}
			(*f.IncludedPaths)[i] = filepath.Clean(p)
		}
	}
	for i, p := range f.ExcludedPaths {
		if !filepath.IsAbs(p) || len(p) > 4096 {
			return f, fmt.Errorf("%w: excluded paths must be absolute", ErrInvalidFilterOptions)
		}
		f.ExcludedPaths[i] = filepath.Clean(p)
	}
	return f, nil
}

func MoveOption(ctx context.Context, db *sql.DB, kind, id, direction string, target ...string) error {
	if kind != "age" && kind != "era" {
		return ErrInvalidFilterOptions
	}
	if direction != "up" && direction != "down" && !(direction == "target" && len(target) == 1) {
		return ErrInvalidFilterOptions
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM channel_filter_options WHERE kind=? ORDER BY position,id`, kind)
	if err != nil {
		return err
	}
	ids := []string{}
	index := -1
	for rows.Next() {
		var current string
		if err := rows.Scan(&current); err != nil {
			rows.Close()
			return err
		}
		if current == id {
			index = len(ids)
		}
		ids = append(ids, current)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if index < 0 {
		return ErrInvalidFilterOptions
	}
	next := index - 1
	if direction == "down" {
		next = index + 1
	}
	if direction == "target" {
		next = -1
		for i, candidate := range ids {
			if candidate == target[0] {
				next = i
			}
		}
		if next < 0 {
			return ErrInvalidFilterOptions
		}
		moving := ids[index]
		ids = append(ids[:index], ids[index+1:]...)
		ids = append(ids, "")
		copy(ids[next+1:], ids[next:])
		ids[next] = moving
	} else if next >= 0 && next < len(ids) {
		ids[index], ids[next] = ids[next], ids[index]
	}
	for position, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE channel_filter_options SET position=? WHERE kind=? AND id=?`, position, kind, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RestoreOptionOrder changes only display positions, retaining definitions and references.
func RestoreOptionOrder(ctx context.Context, db *sql.DB, kind string) error {
	if kind != "age" && kind != "era" {
		return ErrInvalidFilterOptions
	}
	_, err := db.ExecContext(ctx, `UPDATE channel_filter_options SET position=default_position WHERE kind=?`, kind)
	return err
}
