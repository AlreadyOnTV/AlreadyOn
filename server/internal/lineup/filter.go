package lineup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"

	"github.com/alreadyonapp/alreadyon/server/internal/activity"
	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/eligibility"
	"github.com/alreadyonapp/alreadyon/server/internal/governor"
)

// ErrNoFilter is returned when asked to build a channel that has no filter.
var ErrNoFilter = errors.New("lineup: channel has no filter")

// FilterResult says how much library content matches a filter.
type FilterResult struct {
	// Titles counts distinct movies and shows.
	Titles int
	// Items counts individual files (a show's episodes each count).
	Items int
}

// contains reports whether year falls within the era. An unknown year (0)
// is included so a missing release date never
// hides otherwise good content.
func (e eraBound) contains(year int) bool {
	if year == 0 {
		return true
	}
	if e.MinYear > 0 && year < e.MinYear {
		return false
	}
	if e.MaxYear > 0 && year > e.MaxYear {
		return false
	}
	return true
}

// filterMatches applies a custom channel's filter to one item. The
// audience check comes first and fails closed on an unknown rating
// (eligibility.RequiredTier), unlike the permissive built-in rules.
func filterMatches(f channel.Filter, it classifiedItem) bool {
	return len(filterMismatches(f, it)) == 0
}

// Shared by scheduling and unmatched diagnostics so explanations cannot drift.
func filterMismatches(f channel.Filter, it classifiedItem) []string {
	var reasons []string
	tags := make([]string, 0, len(it.Tags))
	for _, tag := range it.Tags {
		tags = append(tags, string(tag))
	}
	override := it.AudienceOverride
	if override == "" {
		override = it.ShowAudienceOverride
	}
	audience, review := eligibility.Audience(it.MediaType, it.Certification, tags, override)

	if f.MatchAudience && audience != eligibility.Tier(f.AudienceTier) {
		reason := "intended audience is " + string(audience)
		if review {
			reason += " (needs review)"
		}
		reasons = append(reasons, reason)
	}
	// Manual restrictions also apply to custom channels.
	if override != "" && !eligibility.Eligible(eligibility.Tier(f.AudienceTier), audience) {
		reasons = append(reasons, "manual audience restriction")
	}

	if f.RequireKnownYear && it.ReleaseYear == 0 {
		reasons = append(reasons, "premiere/release year is unknown")
	}
	for _, excluded := range f.ExcludedGenres {
		for _, tag := range it.Tags {
			if excluded == string(tag) {
				reasons = append(reasons, "excluded genre: "+excluded)
				break
			}
		}
	}
	if ((override == "" || !f.MatchAudience) && f.AgeOption != nil && !f.AgeOption.Unrestricted && !slices.Contains(f.AgeOption.Ratings, it.Certification) && !(f.AgeOption.AllowUnknown && !eligibility.ValidCertification(it.MediaType, it.Certification))) || (f.AgeOption == nil && !eligibility.Eligible(eligibility.Tier(f.AudienceTier), func() eligibility.Tier {
		if override != "" {
			return audience
		}
		return eligibility.RequiredTier(it.MediaType, it.Certification)
	}())) {
		reasons = append(reasons, fmt.Sprintf("rating %q is outside %s", it.Certification, f.AudienceTier))
	}
	if (f.MediaType == "tv" && it.MediaType != "episode") || (f.MediaType == "movie" && it.MediaType != "movie") {
		reasons = append(reasons, "requires "+f.MediaType+" content")
	}
	if len(f.EraOptions) == 0 && ((f.Era == "modern" && !modernEra.contains(it.ReleaseYear)) || (f.Era == "nostalgia" && !nostalgiaEra.contains(it.ReleaseYear))) {
		reasons = append(reasons, fmt.Sprintf("premiere/release year %d is outside %s", it.ReleaseYear, f.Era))
	}
	if len(f.Genres) > 0 {
		found := false
		for _, want := range f.Genres {
			for _, have := range it.Tags {
				if want == string(have) {
					found = true
				}
			}
		}
		if !found {
			reasons = append(reasons, "requires a genre from "+strings.Join(f.Genres, ", "))
		}
	}
	if len(f.EraOptions) > 0 {
		match := false
		for _, o := range f.EraOptions {
			if (eraBound{MinYear: o.StartYear, MaxYear: o.EndYear}).contains(it.ReleaseYear) {
				match = true
			}
		}
		if !match {
			reasons = append(reasons, fmt.Sprintf("premiere/release year %d is outside selected date ranges (%s)", it.ReleaseYear, strings.Join(func() []string {
				var names []string
				for _, o := range f.EraOptions {
					names = append(names, o.ID)
				}
				return names
			}(), ", ")))
		}
	}
	phrases := f.TitlePhrases
	if len(phrases) == 0 && f.TitleContains != "" {
		phrases = []string{f.TitleContains}
	}
	if len(phrases) > 0 {
		match := false
		for _, p := range phrases {
			if strings.Contains(strings.ToLower(it.Title), strings.ToLower(p)) {
				match = true
			}
		}
		if !match {
			reasons = append(reasons, "title must contain one of: "+strings.Join(phrases, ", "))
		}
	}
	if f.IncludedPaths != nil {
		included := false
		roots := f.ResolvedIncludedPaths
		if roots == nil {
			roots = *f.IncludedPaths
		}
		for _, root := range roots {
			rel, err := filepath.Rel(root, it.Path)
			if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				included = true
				break
			}
		}
		if !included {
			reasons = append(reasons, "excluded media path: not in included paths")
		}
	}
	paths := f.ResolvedExcludedPaths
	if paths == nil {
		paths = f.ExcludedPaths
	}
	for _, root := range paths {
		rel, err := filepath.Rel(root, it.Path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			reasons = append(reasons, "excluded media path: "+root)
			break
		}
	}
	return reasons
}

func filterSeries(pool []classifiedItem, f channel.Filter) []contentSeries {
	return seriesWhere(pool, func(it classifiedItem) bool { return filterMatches(f, it) })
}

func resultOf(series []contentSeries) FilterResult {
	r := FilterResult{Titles: len(series)}
	for _, s := range series {
		r.Items += len(s.Items)
	}
	return r
}

// PreviewFilter counts what f would currently match, without saving
// anything. f must already be normalized (channel.Filter.Normalize).
func PreviewFilter(ctx context.Context, db *sql.DB, f channel.Filter) (FilterResult, error) {
	f, err := channel.Resolve(ctx, db, f)
	if err != nil {
		return FilterResult{}, err
	}
	pool, err := loadPool(ctx, db, false)
	if err != nil {
		return FilterResult{}, err
	}
	return resultOf(filterSeries(pool, f)), nil
}

// BuildFilteredChannel rebuilds one custom channel's lineup from its
// filter, replacing whatever was there. Returns ErrNoFilter if the channel
// has none.
func BuildFilteredChannel(ctx context.Context, db *sql.DB, channelID int64) (FilterResult, error) {
	f, err := channel.GetFilter(ctx, db, channelID)
	if err != nil {
		return FilterResult{}, err
	}
	if f == nil {
		return FilterResult{}, ErrNoFilter
	}
	pool, err := loadPool(ctx, db, false)
	if err != nil {
		return FilterResult{}, err
	}
	res, err := buildFiltered(ctx, db, channelID, *f, pool)
	if err != nil {
		return FilterResult{}, err
	}
	_, _, err = channel.ApplyAutoVisibility(ctx, db)
	return res, err
}

func buildFiltered(ctx context.Context, db *sql.DB, channelID int64, f channel.Filter, pool []classifiedItem) (FilterResult, error) {
	series := filterSeries(pool, f)
	// Keep the complete eligible pool; only the schedule is time bounded.
	ids := buildLineup(series, math.Inf(1))
	if err := replaceChannelItems(ctx, db, channelID, ids); err != nil {
		return FilterResult{}, err
	}
	return resultOf(series), nil
}

// rebuildFilteredChannels rebuilds every filtered channel (or only those
// with no content yet, when emptyOnly) and returns how many ended up with
// a lineup.
func rebuildFilteredChannels(ctx context.Context, db *sql.DB, emptyOnly bool) (int, error) {
	filters, err := channel.ListFilters(ctx, db)
	if err != nil {
		return 0, err
	}
	if len(filters) == 0 {
		return 0, nil
	}
	pool, err := loadPool(ctx, db, false)
	if err != nil {
		return 0, err
	}
	rebuilt := 0
	task, finish := activity.Default.Start("Channel Lineup Maintenance", "channels", len(filters))
	defer finish()
	completed := 0
	for id, f := range filters {
		if err := governor.Checkpoint(ctx); err != nil {
			return rebuilt, err
		}
		activity.Default.Progress(task, completed, len(filters))
		completed++
		if emptyOnly {
			var n int
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_items WHERE channel_id = ?`, id).Scan(&n); err != nil {
				return rebuilt, fmt.Errorf("lineup: count channel %d items: %w", id, err)
			}
			if n > 0 {
				continue
			}
		}
		res, err := buildFiltered(ctx, db, id, f, pool)
		if err != nil {
			return rebuilt, err
		}
		if res.Items > 0 {
			rebuilt++
		}
	}
	return rebuilt, nil
}

// replaceChannelItems swaps a channel's whole lineup in one transaction,
// so a concurrent rebuild can't see it half-written.
func replaceChannelItems(ctx context.Context, db *sql.DB, channelID int64, mediaFileIDs []int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("lineup: replace channel %d: %w", channelID, err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM channel_items WHERE channel_id = ?`, channelID); err != nil {
		return fmt.Errorf("lineup: replace channel %d: %w", channelID, err)
	}
	for position, id := range mediaFileIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO channel_items (channel_id, media_file_id, position) VALUES (?, ?, ?)`,
			channelID, id, position); err != nil {
			return fmt.Errorf("lineup: replace channel %d: %w", channelID, err)
		}
	}
	return tx.Commit()
}
