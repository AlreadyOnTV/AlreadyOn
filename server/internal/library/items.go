package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/alreadyonapp/alreadyon/server/internal/eligibility"
	"github.com/alreadyonapp/alreadyon/server/internal/jsonopt"
	"github.com/alreadyonapp/alreadyon/server/internal/metadata"
)

// ItemPatch is a manual correction to identified library content (#103).
// Only the fields present change. Where a field can be cleared, null does
// so: for Genres and Certification that hands the item back to automatic
// classification and metadata.
type ItemPatch struct {
	AudienceOverride jsonopt.Optional[string]   `json:"audienceOverride"`
	MediaType        *string                    `json:"mediaType"`
	Title            *string                    `json:"title"`
	SeriesTitle      jsonopt.Optional[string]   `json:"seriesTitle"`
	Year             jsonopt.Optional[int]      `json:"year"`
	Season           jsonopt.Optional[int]      `json:"season"`
	Episode          jsonopt.Optional[int]      `json:"episode"`
	Genres           jsonopt.Optional[[]string] `json:"genres"`
	Certification    jsonopt.Optional[string]   `json:"certification"`
}

var (
	// ErrItemNotFound means an id doesn't match any identified item.
	ErrItemNotFound = errors.New("library: item not found")
	// ErrInvalidItem wraps every validation failure with what is wrong.
	ErrInvalidItem = errors.New("library: invalid item")
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidItem, fmt.Sprintf(format, args...))
}

const maxTitleLength = 200

type itemRow struct {
	id                int64
	mediaType         string
	title             string
	seriesTitle       sql.NullString
	season, episode   sql.NullInt64
	year              sql.NullInt64
	genreTags         sql.NullString
	genreSource       sql.NullString
	contentRating     sql.NullString
	contentRatingManu bool
}

// UpdateItems applies patch to every item in ids, all or nothing: if any id
// is unknown or the patch is invalid for any of them, nothing is written.
// It returns how many items changed.
//
// Corrections are protected from automation: identification fields set
// is_manual_correction (rescans no longer overwrite them), genres are
// recorded with source "manual", and a chosen certification is flagged so
// TMDb enrichment leaves it. Changing what an item is (type, title, series,
// year, season or episode) also clears its automatic lookups so the next
// refresh classifies and enriches the corrected item. No refresh runs here.
func UpdateItems(ctx context.Context, db *sql.DB, ids []int64, patch ItemPatch) (int, error) {
	if len(ids) == 0 {
		return 0, invalid("no items selected")
	}
	if patchIsEmpty(patch) {
		return 0, invalid("nothing to change")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("library: update items: %w", err)
	}
	defer tx.Rollback()

	seen := map[int64]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if err := updateOne(ctx, tx, id, patch); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("library: update items: %w", err)
	}
	return len(seen), nil
}

func patchIsEmpty(p ItemPatch) bool {
	return p.MediaType == nil && p.Title == nil && !p.SeriesTitle.Set && !p.Year.Set && !p.Season.Set &&
		!p.Episode.Set && !p.Genres.Set && !p.Certification.Set && !p.AudienceOverride.Set
}

func updateOne(ctx context.Context, tx *sql.Tx, id int64, p ItemPatch) error {
	if p.AudienceOverride.Set && p.AudienceOverride.Value != nil {
		switch *p.AudienceOverride.Value {
		case "young_child", "teen", "adult":
		default:
			return invalid("audienceOverride must be young_child, teen, adult or null")
		}
	}
	var r itemRow
	var manual int
	err := tx.QueryRowContext(ctx, `
		SELECT id, media_type, title, series_title, season_number, episode_number, release_year,
		       genre_tags, genre_source, content_rating, content_rating_manual
		FROM identified_items WHERE id = ?`, id).
		Scan(&r.id, &r.mediaType, &r.title, &r.seriesTitle, &r.season, &r.episode, &r.year, &r.genreTags, &r.genreSource, &r.contentRating, &manual)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %d", ErrItemNotFound, id)
	}
	if err != nil {
		return fmt.Errorf("library: load item %d: %w", id, err)
	}
	r.contentRatingManu = manual == 1

	identityChanged := p.MediaType != nil || p.Title != nil || p.SeriesTitle.Set || p.Year.Set || p.Season.Set || p.Episode.Set

	if p.MediaType != nil {
		if *p.MediaType != "movie" && *p.MediaType != "episode" {
			return invalid("mediaType must be movie or episode")
		}
		r.mediaType = *p.MediaType
	}
	if p.Title != nil {
		t := strings.TrimSpace(*p.Title)
		if t == "" || utf8.RuneCountInString(t) > maxTitleLength {
			return invalid("title must be 1-%d characters", maxTitleLength)
		}
		r.title = t
	}
	if p.SeriesTitle.Set {
		r.seriesTitle = sql.NullString{}
		if p.SeriesTitle.Value != nil {
			s := strings.TrimSpace(*p.SeriesTitle.Value)
			if utf8.RuneCountInString(s) > maxTitleLength {
				return invalid("seriesTitle must be at most %d characters", maxTitleLength)
			}
			r.seriesTitle = sql.NullString{String: s, Valid: s != ""}
		}
	}
	if err := setOptionalInt(&r.year, p.Year, "year", 1870, 2100); err != nil {
		return err
	}
	if err := setOptionalInt(&r.season, p.Season, "season", 0, 1000); err != nil {
		return err
	}
	if err := setOptionalInt(&r.episode, p.Episode, "episode", 0, 100000); err != nil {
		return err
	}

	if r.mediaType == "movie" {
		r.seriesTitle, r.season, r.episode = sql.NullString{}, sql.NullInt64{}, sql.NullInt64{}
	} else if !r.seriesTitle.Valid {
		return invalid("an episode needs a seriesTitle")
	}

	genresProvided := p.Genres.Set
	if genresProvided && p.Genres.Value != nil {
		clean := make([]string, 0, len(*p.Genres.Value))
		for _, g := range *p.Genres.Value {
			if !metadata.ValidTag(g) {
				return invalid("unknown genre %q", g)
			}
			if !slices.Contains(clean, g) {
				clean = append(clean, g)
			}
		}
		enc, err := json.Marshal(clean)
		if err != nil {
			return err
		}
		r.genreTags = sql.NullString{String: string(enc), Valid: true}
		r.genreSource = sql.NullString{String: "manual", Valid: true}
	} else if genresProvided {
		r.genreTags, r.genreSource = sql.NullString{}, sql.NullString{}
	} else if identityChanged && r.genreSource.String != "manual" {
		// Corrected title: let the next refresh classify it again.
		r.genreTags, r.genreSource = sql.NullString{}, sql.NullString{}
	}

	refetch := identityChanged
	if p.Certification.Set {
		if p.Certification.Value == nil || *p.Certification.Value == "" {
			r.contentRating, r.contentRatingManu = sql.NullString{}, false
			refetch = true
		} else {
			c := *p.Certification.Value
			if !eligibility.ValidCertification(r.mediaType, c) {
				return invalid("certification %q isn't a known %s rating", c, ratingKind(r.mediaType))
			}
			r.contentRating, r.contentRatingManu = sql.NullString{String: c, Valid: true}, true
		}
	} else if p.MediaType != nil && r.contentRating.Valid && r.contentRatingManu && !eligibility.ValidCertification(r.mediaType, r.contentRating.String) {
		return invalid("this item's certification %q isn't a %s rating; set a new certification too", r.contentRating.String, ratingKind(r.mediaType))
	}

	manualFlag := 0
	if identityChanged {
		manualFlag = 1
	}
	ratingManual := 0
	if r.contentRatingManu {
		ratingManual = 1
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE identified_items SET
			media_type = ?, title = ?, series_title = ?, season_number = ?, episode_number = ?, release_year = ?,
			genre_tags = ?, genre_source = ?, content_rating = ?, content_rating_manual = ?,
			is_manual_correction = CASE WHEN ? = 1 THEN 1 ELSE is_manual_correction END,
			metadata_fetched_at_unix = CASE WHEN ? THEN NULL ELSE metadata_fetched_at_unix END,
            -- Identity corrections invalidate free dates just like TMDb links.
            keyless_release_date = CASE WHEN ? THEN NULL ELSE keyless_release_date END,
            keyless_provider = CASE WHEN ? THEN NULL ELSE keyless_provider END,
            keyless_fetched_at_unix = CASE WHEN ? THEN NULL ELSE keyless_fetched_at_unix END,
			show_id = CASE WHEN ? THEN NULL ELSE show_id END,
			updated_at_unix = unixepoch()
		WHERE id = ?`,
		r.mediaType, r.title, r.seriesTitle, r.season, r.episode, r.year,
		r.genreTags, r.genreSource, r.contentRating, ratingManual,
		manualFlag, refetch, identityChanged, identityChanged, identityChanged, p.MediaType != nil || p.SeriesTitle.Set, id)
	if err != nil {
		return fmt.Errorf("library: update item %d: %w", id, err)
	}
	if p.AudienceOverride.Set {
		if _, err := tx.ExecContext(ctx, `UPDATE identified_items SET audience_override=? WHERE id=?`, p.AudienceOverride.Value, id); err != nil {
			return err
		}
	}
	return nil
}

func ratingKind(mediaType string) string {
	if mediaType == "movie" {
		return "movie"
	}
	return "TV"
}

func setOptionalInt(dst *sql.NullInt64, o jsonopt.Optional[int], name string, lo, hi int) error {
	if !o.Set {
		return nil
	}
	if o.Value == nil {
		*dst = sql.NullInt64{}
		return nil
	}
	if *o.Value < lo || *o.Value > hi {
		return invalid("%s must be between %d and %d", name, lo, hi)
	}
	*dst = sql.NullInt64{Int64: int64(*o.Value), Valid: true}
	return nil
}
