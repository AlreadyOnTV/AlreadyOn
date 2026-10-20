package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/alreadyonapp/alreadyon/server/internal/eligibility"
)

// CatalogEntry is a movie, a grouped show, or an episode. ID is a representative
// identified item; show edits resolve membership again inside the transaction.
type CatalogEntry struct {
	ID                     int64    `json:"id"`
	TMDbID                 int      `json:"tmdbId"`
	Path                   string   `json:"path"`
	MediaType              string   `json:"mediaType"`
	Title                  string   `json:"title"`
	SeriesTitle            *string  `json:"seriesTitle"`
	Season                 *int     `json:"season"`
	Episode                *int     `json:"episode"`
	Year                   *int     `json:"year"`
	Genres                 []string `json:"genres"`
	Certification          *string  `json:"certification"`
	IsManual               bool     `json:"isManual"`
	Reason                 string   `json:"reason"`
	Audience               string   `json:"audience"`
	AudienceOverride       *string  `json:"audienceOverride"`
	AudienceSource         string   `json:"audienceSource"`
	NeedsReview            bool     `json:"needsReview"`
	Matched                bool     `json:"matched"`
	Files                  int      `json:"files"`
	MatchedFiles           int      `json:"matchedFiles"`
	Mixed                  bool     `json:"mixed"`
	ParentAudienceOverride *string  `json:"parentAudienceOverride"`
	Certifications         []string `json:"certifications"`
	showID                 int64
	searchMatch            bool
	key                    string
}
type CatalogQuery struct {
	View, Search, Match, Audience, Genre, Rating, Source, Sort string
	ShowID                                                     int64
	Limit, Offset                                              int
}
type CatalogPage struct {
	Total int            `json:"total"`
	Items []CatalogEntry `json:"items"`
}

func matchesCatalogValues(values []string, selected string) bool {
	if selected == "" {
		return true
	}
	for _, value := range strings.Split(selected, ",") {
		if slices.Contains(values, strings.TrimSpace(value)) {
			return true
		}
	}
	return false
}

// Read all lightweight metadata once, then group by stable provider identity.
// No paths or episode counts are fetched through a per-show query (N+1).
func Catalog(ctx context.Context, db *sql.DB, q CatalogQuery) (CatalogPage, error) {
	if q.View != "movies" && q.View != "shows" && q.View != "episodes" {
		return CatalogPage{}, invalid("invalid view")
	}
	for key, value := range map[string]string{"match": q.Match, "audience": q.Audience, "source": q.Source, "sort": q.Sort} {
		allowed := map[string][]string{"match": {"", "matched", "unmatched", "review"}, "audience": {"", "young_child", "teen", "adult"}, "source": {"", "manual", "automatic"}, "sort": {"", "title", "title-desc", "year", "year-asc", "audience", "audience-desc", "rating", "rating-desc", "channels", "channels-desc", "episode", "episodes"}}
		if !slices.Contains(allowed[key], value) {
			return CatalogPage{}, invalid("invalid %s", key)
		}
	}
	if q.Offset < 0 || q.Limit < 1 || q.Limit > 200 {
		return CatalogPage{}, invalid("invalid pagination")
	}

	rows, err := db.QueryContext(ctx, `SELECT i.id,coalesce(s.id,0),coalesce(CASE WHEN i.media_type='episode' THEN s.tmdb_id ELSE i.tmdb_id END,0),m.path,i.media_type,i.title,coalesce(s.title,i.series_title),i.season_number,i.episode_number,
 CASE WHEN i.media_type='episode' THEN coalesce(s.premiere_year_override,cast(substr(coalesce(s.first_air_date,i.keyless_release_date),1,4) as integer),i.release_year) ELSE i.release_year END,
 coalesce(CASE WHEN i.genre_source='manual' THEN i.genre_tags END,s.genre_tags,i.genre_tags,'[]'),coalesce(CASE WHEN i.content_rating_manual=1 THEN i.content_rating END,s.content_rating,i.content_rating,''),
 (i.is_manual_correction=1 OR i.genre_source='manual' OR i.content_rating_manual=1 OR i.audience_override IS NOT NULL OR s.audience_override IS NOT NULL OR s.genre_source='manual' OR s.content_rating_manual=1 OR s.premiere_year_override IS NOT NULL OR (s.local_series_title IS NOT NULL AND s.title!=s.local_series_title)),
 coalesce(i.audience_override,''),coalesce(s.audience_override,''),
 m.id IN (SELECT ci.media_file_id FROM channel_items ci JOIN channels c ON c.id=ci.channel_id WHERE c.number NOT BETWEEN 180 AND 199)
 FROM identified_items i JOIN media_files m ON m.id=i.media_file_id LEFT JOIN tv_shows s ON s.id=i.show_id
 WHERE m.root_kind='media' AND m.status!='missing' ORDER BY i.id`)
	if err != nil {
		return CatalogPage{}, err
	}
	var entries []CatalogEntry
	for rows.Next() {
		var e CatalogEntry
		var tags, cert, itemOverride, showOverride string
		var year, season, episode sql.NullInt64
		var series sql.NullString
		var manual sql.NullBool
		if err = rows.Scan(&e.ID, &e.showID, &e.TMDbID, &e.Path, &e.MediaType, &e.Title, &series, &season, &episode, &year, &tags, &cert, &manual, &itemOverride, &showOverride, &e.Matched); err != nil {
			rows.Close()
			return CatalogPage{}, err
		}
		e.IsManual = manual.Bool
		if e.TMDbID < 0 {
			e.TMDbID = 0
		}
		if showOverride != "" {
			v := showOverride
			e.ParentAudienceOverride = &v
		}
		if e.Matched {
			e.MatchedFiles = 1
		}
		if series.Valid {
			v := series.String
			e.SeriesTitle = &v
		}
		if year.Valid && year.Int64 > 0 {
			v := int(year.Int64)
			e.Year = &v
		}
		if season.Valid {
			v := int(season.Int64)
			e.Season = &v
		}
		if episode.Valid {
			v := int(episode.Int64)
			e.Episode = &v
		}
		if cert != "" {
			e.Certification = &cert
			e.Certifications = []string{cert}
		}
		if err = json.Unmarshal([]byte(tags), &e.Genres); err != nil {
			rows.Close()
			return CatalogPage{}, err
		}
		override := itemOverride
		e.AudienceSource = "Automatic"
		if override != "" {
			e.AudienceOverride = &override
			e.AudienceSource = "Manual"
		} else if showOverride != "" {
			override = showOverride
			e.AudienceSource = "Inherited from show"
		}
		tier, review := eligibility.Audience(e.MediaType, cert, e.Genres, override)

		e.searchMatch = strings.Contains(strings.ToLower(e.Title+" "+series.String+" "+e.Path), strings.ToLower(q.Search))
		e.Audience = string(tier)
		e.NeedsReview = review
		e.Files = 1
		if e.MediaType == "movie" {
			e.key = fmt.Sprintf("movie:%d", e.ID)
		} else if e.showID > 0 {
			e.key = fmt.Sprintf("show:%d", e.showID)
		} else {
			e.key = "series:" + series.String + ":"
			if e.Year != nil {
				e.key += fmt.Sprint(*e.Year)
			}
		}
		if review {
			e.Reason = "Audience needs review; excluded from restricted default channels."
		} else if !e.Matched {
			e.Reason = "Not assigned to a channel; review metadata and Apply Channels."
			if len(e.Genres) == 0 {
				e.Reason = "Genres are missing; genre channels cannot match."
			} else if e.Year == nil {
				e.Reason = "Premiere/release year is missing; restricted defaults require a known year."
			}
		}
		entries = append(entries, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return CatalogPage{}, err
	}
	showKey := ""
	if q.View == "episodes" {
		for _, e := range entries {
			if e.ID == q.ShowID && e.MediaType == "episode" {
				showKey = e.key
				break
			}
		}
		if showKey == "" {
			return CatalogPage{}, ErrItemNotFound
		}
	}
	grouped := map[string]int{}
	all := []CatalogEntry{}
	for _, e := range entries {
		if q.View == "movies" && e.MediaType != "movie" {
			continue
		}
		if q.View == "shows" && e.MediaType != "episode" {
			continue
		}
		if q.View == "episodes" && e.key != showKey {
			continue
		}
		if q.View == "shows" {
			if idx, ok := grouped[e.key]; ok {
				v := &all[idx]
				if v.Audience != e.Audience || v.AudienceSource != e.AudienceSource || stringValue(v.Certification) != stringValue(e.Certification) || fmt.Sprint(v.Genres) != fmt.Sprint(e.Genres) {
					v.Mixed = true
				}
				v.Files++
				for _, g := range e.Genres {
					if !slices.Contains(v.Genres, g) {
						v.Genres = append(v.Genres, g)
					}
				}
				for _, c := range e.Certifications {
					if !slices.Contains(v.Certifications, c) {
						v.Certifications = append(v.Certifications, c)
					}
				}
				v.MatchedFiles += e.MatchedFiles
				v.searchMatch = v.searchMatch || e.searchMatch
				v.Matched = v.Matched || e.Matched
				v.IsManual = v.IsManual || e.IsManual
				v.NeedsReview = v.NeedsReview || e.NeedsReview
				v.Audience = string(eligibility.Max(eligibility.ParseTier(v.Audience), eligibility.ParseTier(e.Audience)))
				continue
			}
			grouped[e.key] = len(all)
			e.AudienceOverride = e.ParentAudienceOverride

			if e.SeriesTitle != nil {
				e.Title = *e.SeriesTitle
			}
			e.Season = nil
			e.Episode = nil
		}
		all = append(all, e)
	}
	filtered := []CatalogEntry{}
	for _, e := range all {
		if q.View == "shows" {
			if e.Mixed {
				e.AudienceSource = "Episode values vary"
			} else if e.ParentAudienceOverride != nil {
				e.AudienceSource = "Manual"
			}
		}
		if !e.searchMatch {
			continue
		}
		if q.Match == "matched" && !e.Matched || q.Match == "unmatched" && e.Matched || q.Match == "review" && !e.NeedsReview {
			continue
		}
		if q.Audience != "" && e.Audience != q.Audience {
			continue
		}
		if !matchesCatalogValues(e.Certifications, q.Rating) {
			continue
		}
		if q.Source == "manual" && !e.IsManual || q.Source == "automatic" && e.IsManual {
			continue
		}
		if !matchesCatalogValues(e.Genres, q.Genre) {
			continue
		}
		filtered = append(filtered, e)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		a, b := filtered[i], filtered[j]
		switch q.Sort {
		case "title-desc":
			return strings.ToLower(a.Title) > strings.ToLower(b.Title)
		case "year", "year-asc":
			ay, by := 0, 0
			if a.Year != nil {
				ay = *a.Year
			}
			if b.Year != nil {
				by = *b.Year
			}
			if ay != by {
				if q.Sort == "year-asc" {
					return ay < by
				}
				return ay > by
			}
		case "audience", "audience-desc":
			aa, ba := strings.ToLower(a.Audience), strings.ToLower(b.Audience)
			if aa != ba {
				if q.Sort == "audience-desc" {
					return aa > ba
				}
				return aa < ba
			}
		case "rating", "rating-desc":
			ar, br := strings.Join(a.Certifications, ","), strings.Join(b.Certifications, ",")
			if ar != br {
				if q.Sort == "rating-desc" {
					return ar > br
				}
				return ar < br
			}
		case "channels", "channels-desc":
			if a.Matched != b.Matched {
				if q.Sort == "channels-desc" {
					return !a.Matched
				}
				return a.Matched
			}
		case "episodes":
			if a.Files != b.Files {
				return a.Files > b.Files
			}
		case "episode":
			as, bs, ae, be := 0, 0, 0, 0
			if a.Season != nil {
				as = *a.Season
			}
			if b.Season != nil {
				bs = *b.Season
			}
			if a.Episode != nil {
				ae = *a.Episode
			}
			if b.Episode != nil {
				be = *b.Episode
			}
			if as != bs {
				return as < bs
			}
			if ae != be {
				return ae < be
			}
		}
		return strings.ToLower(a.Title) < strings.ToLower(b.Title)
	})
	total := len(filtered)
	start := min(q.Offset, total)
	end := min(start+q.Limit, total)
	return CatalogPage{Total: total, Items: filtered[start:end]}, nil
}

// UpdateCatalog commits a selection atomically. Show corrections are stored on
// the parent, so future episodes inherit them and explicit episode edits survive.
func UpdateCatalog(ctx context.Context, db *sql.DB, ids []int64, view string, p ItemPatch) (int, error) {
	if view != "movies" && view != "shows" && view != "episodes" {
		return 0, invalid("invalid view")
	}
	if len(ids) == 0 || len(ids) > 1000 {
		return 0, invalid("select 1-1000 rows")
	}
	if patchIsEmpty(p) {
		return 0, invalid("nothing to change")
	}
	if view == "shows" && (p.MediaType != nil || p.Season.Set || p.Episode.Set || p.SeriesTitle.Set) {
		return 0, invalid("show edits cannot change episode identity fields")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	seen := map[int64]bool{}
	updated := 0
	for _, id := range ids {
		var kind string
		var show sql.NullInt64
		var series sql.NullString
		var year sql.NullInt64
		err = tx.QueryRowContext(ctx, `SELECT media_type,show_id,series_title,release_year FROM identified_items WHERE id=?`, id).Scan(&kind, &show, &series, &year)
		if err == sql.ErrNoRows {
			return 0, ErrItemNotFound
		}
		if err != nil {
			return 0, err
		}
		if (view == "movies" && kind != "movie") || (view != "movies" && kind != "episode") {
			return 0, invalid("selection does not match view")
		}
		if view != "shows" {
			if seen[id] {
				continue
			}
			seen[id] = true
			if err = updateOne(ctx, tx, id, p); err != nil {
				return 0, err
			}
			updated++
			continue
		}
		// Reuse item validation without applying show values to a representative
		// episode. In particular, no sibling's manual audience is overwritten.
		validation := p
		if p.Title != nil {
			validation.Title = nil
			validation.SeriesTitle.Set = true
			validation.SeriesTitle.Value = p.Title
		}
		if _, err = tx.ExecContext(ctx, `SAVEPOINT validate_show`); err != nil {
			return 0, err
		}
		if err = updateOne(ctx, tx, id, validation); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, `ROLLBACK TO validate_show`); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, `RELEASE validate_show`); err != nil {
			return 0, err
		}
		if !show.Valid {
			// Negative provider IDs are internal placeholders, never sent to TMDb.
			// Original series identity is retained even when the display title changes.
			err = tx.QueryRowContext(ctx, `INSERT INTO tv_shows(tmdb_id,title,local_series_title,local_series_year)
    VALUES((SELECT min(coalesce(min(tmdb_id),0),0)-1 FROM tv_shows),?,?,?) RETURNING id`, series, series, year).Scan(&show.Int64)
			if err != nil {
				return 0, err
			}
			show.Valid = true
			if _, err = tx.ExecContext(ctx, `UPDATE identified_items SET show_id=? WHERE show_id IS NULL AND media_type='episode' AND series_title IS ? AND release_year IS ?`, show, series, year); err != nil {
				return 0, err
			}
		}
		if seen[show.Int64] {
			continue
		}
		seen[show.Int64] = true
		if _, err = tx.ExecContext(ctx, `UPDATE tv_shows SET local_series_title=coalesce(local_series_title,?),local_series_year=coalesce(local_series_year,?) WHERE id=?`, series, year, show); err != nil {
			return 0, err
		}
		if p.AudienceOverride.Set {
			if _, err = tx.ExecContext(ctx, `UPDATE tv_shows SET audience_override=? WHERE id=?`, p.AudienceOverride.Value, show); err != nil {
				return 0, err
			}
		}
		if p.Title != nil {
			if _, err = tx.ExecContext(ctx, `UPDATE tv_shows SET title=? WHERE id=?`, strings.TrimSpace(*p.Title), show); err != nil {
				return 0, err
			}
		}
		if p.Year.Set {
			if _, err = tx.ExecContext(ctx, `UPDATE tv_shows SET premiere_year_override=? WHERE id=?`, p.Year.Value, show); err != nil {
				return 0, err
			}
		}
		if p.Genres.Set {
			var value any
			var source any
			if p.Genres.Value != nil {
				raw, _ := json.Marshal(*p.Genres.Value)
				value = string(raw)
				source = "manual"
			}
			if _, err = tx.ExecContext(ctx, `UPDATE tv_shows SET genre_tags=?,genre_source=? WHERE id=?`, value, source, show); err != nil {
				return 0, err
			}
		}
		if p.Certification.Set {
			if p.Certification.Value == nil || *p.Certification.Value == "" {
				if _, err = tx.ExecContext(ctx, `UPDATE identified_items SET metadata_fetched_at_unix=NULL WHERE show_id=?`, show); err != nil {
					return 0, err
				}
			}
			manual := p.Certification.Value != nil && *p.Certification.Value != ""
			if _, err = tx.ExecContext(ctx, `UPDATE tv_shows SET content_rating=?,content_rating_manual=?,metadata_fetched_at_unix=NULL WHERE id=?`, p.Certification.Value, manual, show); err != nil {
				return 0, err
			}
		}
		updated++
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return updated, nil
}

func stringValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
