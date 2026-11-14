package channel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/alreadyonapp/alreadyon/server/internal/workrevision"
	"slices"
	"strings"
)

// The per-channel visibility choices.
const (
	VisibilityAuto = "auto"
	VisibilityShow = "show"
	VisibilityHide = "hide"
)

var ErrInvalidVisibility = errors.New("channel: visibility must be auto, show or hide")

// SetVisibility sets the choice for every channel in ids (a single id works for
// a per-row button) and updates which channels the guide shows. Unknown ids are
// ignored rather than failing the batch.
func SetVisibility(ctx context.Context, db *sql.DB, ids []int64, visibility string) error {
	if !slices.Contains([]string{VisibilityAuto, VisibilityShow, VisibilityHide}, visibility) {
		return ErrInvalidVisibility
	}
	if len(ids) == 0 {
		return nil
	}
	placeholders := make([]string, len(ids))
	args := []any{visibility}
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	query := fmt.Sprintf(`UPDATE channels SET visibility = ?, updated_at_unix = unixepoch() WHERE id IN (%s)`, strings.Join(placeholders, ","))
	if _, err := workrevision.Exec(ctx, db, query, args...); err != nil {
		return fmt.Errorf("channel: set visibility: %w", err)
	}
	_, _, err := ApplyAutoVisibility(ctx, db)
	return err
}

// ApplyAutoVisibility brings every channel's effective Hidden in line with its
// visibility choice: hide hides, show shows, and auto shows a channel only while
// it has some show or movie (not the stricter two-show bar of Populate). It runs
// after every change to a choice and after every lineup refresh. Returns how
// many channels were newly hidden and newly shown.
func ApplyAutoVisibility(ctx context.Context, db *sql.DB) (hidden, shown int, err error) {
	all, err := List(ctx, db)
	if err != nil {
		return 0, 0, err
	}
	counts, err := SeriesCounts(ctx, db)
	if err != nil {
		return 0, 0, err
	}
	var toHide, toShow []int64
	for _, c := range all {
		wantHidden := c.Visibility == VisibilityHide || (c.Visibility == VisibilityAuto && counts[c.ID] == 0)
		switch {
		case wantHidden && !c.Hidden:
			toHide = append(toHide, c.ID)
		case !wantHidden && c.Hidden:
			toShow = append(toShow, c.ID)
		}
	}
	if len(toHide) > 0 {
		if err := SetHidden(ctx, db, toHide, true); err != nil {
			return 0, 0, err
		}
	}
	if len(toShow) > 0 {
		if err := SetHidden(ctx, db, toShow, false); err != nil {
			return 0, 0, err
		}
	}
	return len(toHide), len(toShow), nil
}
