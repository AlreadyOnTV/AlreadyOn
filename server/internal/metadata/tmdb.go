// Package metadata classifies movies and episodes into a small internal
// genre taxonomy via The Movie Database (TMDb), so default channel
// lineups (see internal/lineup) can be populated with content that
// actually matches each channel's theme. See #79.
//
// Free for non-commercial use (this is a self-hosted personal project):
// https://developer.themoviedb.org. A read access token is required —
// see docs/DEVELOPMENT.md for how to obtain and configure one
// (configured on the Admin Server tab). Per TMDb's terms, any UI surfacing this
// data must credit "This product uses the TMDB API but is not endorsed
// or certified by TMDB." (see Server Settings > Server, #77).
package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/governor"
)

// Tag is AlreadyOn's own small genre taxonomy, independent of any one
// provider's vocabulary — see genre_mappings.go for how TMDb's genre IDs
// map onto it.
type Tag string

const (
	TagAction      Tag = "action"
	TagAdventure   Tag = "adventure"
	TagAnimation   Tag = "animation"
	TagAnime       Tag = "anime"
	TagComedy      Tag = "comedy"
	TagCrime       Tag = "crime"
	TagDocumentary Tag = "documentary"
	TagDrama       Tag = "drama"
	TagFamily      Tag = "family"
	TagFantasy     Tag = "fantasy"
	TagHistory     Tag = "history"
	TagHorror      Tag = "horror"
	TagKids        Tag = "kids"
	TagMusic       Tag = "music"
	TagMystery     Tag = "mystery"
	TagReality     Tag = "reality"
	TagRomance     Tag = "romance"
	TagSciFi       Tag = "scifi"
	TagThriller    Tag = "thriller"
	TagWar         Tag = "war"
	TagWestern     Tag = "western"
)

const defaultAPIBase = "https://api.themoviedb.org/3"

// Client looks up genre classifications from TMDb. The zero value with
// an empty APIKey is valid — every method just returns (nil, nil) — so
// callers never need to branch on "is TMDb configured" themselves.
type Client struct {
	APIKey     string
	HTTPClient *http.Client
	// BaseURL defaults to the real TMDb API (see NewClient) — overridden
	// in tests to point at a local mock server instead.
	BaseURL string
}

// NewClient returns a Client with a bounded request timeout. Passing an
// empty apiKey is fine — see Client's doc comment.
func NewClient(apiKey string) *Client {
	return &Client{APIKey: apiKey, HTTPClient: &http.Client{Transport: governor.BackgroundTransport{}, Timeout: 10 * time.Second}, BaseURL: defaultAPIBase}
}

func (c *Client) enabled() bool { return c != nil && c.APIKey != "" }

type searchResponse struct {
	Results []searchResult `json:"results"`
}

type searchResult struct {
	ID               int    `json:"id"`
	GenreIDs         []int  `json:"genre_ids"`
	OriginalLanguage string `json:"original_language"`

	// Used to tell apart same-titled results (see pickBest); TV results carry
	// name/first_air_date, movie results title/release_date.
	Name          string `json:"name"`
	OriginalName  string `json:"original_name"`
	Title         string `json:"title"`
	OriginalTitle string `json:"original_title"`
	FirstAirDate  string `json:"first_air_date"`
	ReleaseDate   string `json:"release_date"`
}

// ClassifyMovie looks up title (optionally narrowed by year, when > 0)
// against TMDb's movie search and returns the best match's genres
// mapped onto Tag. No match, no results, or a disabled client all
// return (nil, nil) — classification failing is never treated as an
// error a caller needs to handle specially, since unclassified content
// is a normal, expected state (see internal/lineup).
func (c *Client) ClassifyMovie(ctx context.Context, title string, year int) ([]Tag, error) {
	if !c.enabled() {
		return nil, nil
	}
	result, err := c.lookup(ctx, kindMovie, title, year)
	if err != nil || result == nil {
		return nil, err
	}
	return tagsFromGenreIDs(movieGenreTags, result.GenreIDs), nil
}

// ClassifyEpisode looks up seriesTitle (optionally narrowed by year,
// when > 0 — a series' first air date) against TMDb's TV search and
// returns the best match's genres mapped onto Tag, adding TagAnime when
// the match is Animation originating in Japanese. See ClassifyMovie for
// the no-match/disabled-client behavior.
func (c *Client) ClassifyEpisode(ctx context.Context, seriesTitle string, year int) ([]Tag, error) {
	if !c.enabled() {
		return nil, nil
	}
	result, err := c.lookup(ctx, kindTV, seriesTitle, year)
	if err != nil || result == nil {
		return nil, err
	}
	tags := tagsFromGenreIDs(tvGenreTags, result.GenreIDs)
	if result.OriginalLanguage == "ja" {
		for _, t := range tags {
			if t == TagAnimation {
				tags = append(tags, TagAnime)
				break
			}
		}
	}
	return tags, nil
}

// FindMovieID looks up title (optionally narrowed by year) against
// TMDb's movie search and returns the best match's own TMDb id, or 0 if
// there's no match or the client is disabled — the id MovieDetails
// needs, kept separate from ClassifyMovie so a caller only pays for an
// extra details fetch when it actually wants overview/release date/
// certification, not just genres.
func (c *Client) FindMovieID(ctx context.Context, title string, year int) (int, error) {
	if !c.enabled() {
		return 0, nil
	}
	result, err := c.lookup(ctx, kindMovie, title, year)
	if err != nil || result == nil {
		return 0, err
	}
	return result.ID, nil
}

// FindTVID is FindMovieID for a TV show's search endpoint — see
// TVDetails.
func (c *Client) FindTVID(ctx context.Context, title string, year int) (int, error) {
	if !c.enabled() {
		return 0, nil
	}
	result, err := c.lookup(ctx, kindTV, title, year)
	if err != nil || result == nil {
		return 0, err
	}
	return result.ID, nil
}

// get issues an authenticated GET against path (BaseURL-relative,
// already including any query string) and decodes the JSON response
// body into out — the shared request/response boilerplate every
// details/ratings endpoint below needs, factored out of search so
// adding one of these isn't another full copy of it.
func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return fmt.Errorf("metadata: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("metadata: tmdb request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("metadata: tmdb request: unexpected status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("metadata: decode tmdb response: %w", err)
	}
	return nil
}

// MovieDetails is the subset of a movie's TMDb details AlreadyOn keeps
// (see docs/PROJECT_BRIEF.md) — enough for eligibility, filtering, and
// EPG display, deliberately not artwork/cast/trailers/popularity.
type MovieDetails struct {
	Overview      string
	ReleaseDate   string // ISO "YYYY-MM-DD"; "" if TMDb has none
	Certification string // US certification (e.g. "PG-13"); "" if unavailable
}

type movieDetailsResponse struct {
	Overview    string `json:"overview"`
	ReleaseDate string `json:"release_date"`
}

type releaseDatesResponse struct {
	Results []releaseDateCountry `json:"results"`
}

type releaseDateCountry struct {
	Country      string             `json:"iso_3166_1"`
	ReleaseDates []releaseDateEntry `json:"release_dates"`
}

type releaseDateEntry struct {
	Certification string `json:"certification"`
	Type          int    `json:"type"` // 3 == Theatrical, per TMDb's own enum
}

// usCertification finds the US entry among results and picks its best
// certification: a non-empty theatrical (type 3) release's, if any,
// else the first non-empty certification of any release type — some
// movies only ever had a limited/digital release, which still carries
// a real MPAA rating worth keeping over having none at all.
func usCertification(results []releaseDateCountry) string {
	for _, r := range results {
		if r.Country != "US" {
			continue
		}
		fallback := ""
		for _, rd := range r.ReleaseDates {
			if rd.Certification == "" {
				continue
			}
			if rd.Type == 3 {
				return rd.Certification
			}
			if fallback == "" {
				fallback = rd.Certification
			}
		}
		return fallback
	}
	return ""
}

// MovieDetails fetches tmdbID's overview, release date, and US
// certification. A disabled client or tmdbID<=0 returns a zero value,
// not an error — same "not configured/not found" convention as
// ClassifyMovie. The certification lookup is a second request (TMDb has
// no single endpoint returning both); a failure there doesn't fail the
// whole call, since overview/release_date are still useful on their own.
func (c *Client) MovieDetails(ctx context.Context, tmdbID int) (MovieDetails, error) {
	if !c.enabled() || tmdbID <= 0 {
		return MovieDetails{}, nil
	}
	var details movieDetailsResponse
	if err := c.get(ctx, fmt.Sprintf("/movie/%d", tmdbID), &details); err != nil {
		return MovieDetails{}, err
	}
	var dates releaseDatesResponse
	cert := ""
	if err := c.get(ctx, fmt.Sprintf("/movie/%d/release_dates", tmdbID), &dates); err == nil {
		cert = usCertification(dates.Results)
	}
	return MovieDetails{Overview: details.Overview, ReleaseDate: details.ReleaseDate, Certification: cert}, nil
}

// TVDetails is the subset of a show's TMDb details AlreadyOn keeps —
// see MovieDetails.
type TVDetails struct {
	Overview      string
	FirstAirDate  string // ISO "YYYY-MM-DD"
	LastAirDate   string // ISO "YYYY-MM-DD"; "" for a still-airing/unknown-ended show
	Certification string // US TV content rating (e.g. "TV-14"); "" if unavailable
}

type tvDetailsResponse struct {
	Overview     string `json:"overview"`
	FirstAirDate string `json:"first_air_date"`
	LastAirDate  string `json:"last_air_date"`
}

type contentRatingsResponse struct {
	Results []struct {
		Country string `json:"iso_3166_1"`
		Rating  string `json:"rating"`
	} `json:"results"`
}

// TVDetails fetches tmdbID's overview, first/last air date, and US
// content rating. See MovieDetails for the disabled-client/not-found
// and partial-failure conventions.
func (c *Client) TVDetails(ctx context.Context, tmdbID int) (TVDetails, error) {
	if !c.enabled() || tmdbID <= 0 {
		return TVDetails{}, nil
	}
	var details tvDetailsResponse
	if err := c.get(ctx, fmt.Sprintf("/tv/%d", tmdbID), &details); err != nil {
		return TVDetails{}, err
	}
	var ratings contentRatingsResponse
	cert := ""
	if err := c.get(ctx, fmt.Sprintf("/tv/%d/content_ratings", tmdbID), &ratings); err == nil {
		for _, r := range ratings.Results {
			if r.Country == "US" {
				cert = r.Rating
				break
			}
		}
	}
	return TVDetails{Overview: details.Overview, FirstAirDate: details.FirstAirDate, LastAirDate: details.LastAirDate, Certification: cert}, nil
}

// EpisodeDetails is the subset of one episode's TMDb details AlreadyOn
// keeps — its own overview/air date, on top of the season/episode
// numbers and title already known locally (see internal/library).
type EpisodeDetails struct {
	Overview string
	AirDate  string // ISO "YYYY-MM-DD"
}

type episodeDetailsResponse struct {
	Overview string `json:"overview"`
	AirDate  string `json:"air_date"`
}

// EpisodeDetails fetches one episode's own overview and air date via
// showTMDbID's season/episode endpoint. A disabled client, missing
// show id, or nonexistent season/episode all return a zero value, not
// an error.
func (c *Client) EpisodeDetails(ctx context.Context, showTMDbID, season, episode int) (EpisodeDetails, error) {
	if !c.enabled() || showTMDbID <= 0 {
		return EpisodeDetails{}, nil
	}
	var details episodeDetailsResponse
	path := fmt.Sprintf("/tv/%d/season/%d/episode/%d", showTMDbID, season, episode)
	if err := c.get(ctx, path, &details); err != nil {
		return EpisodeDetails{}, err
	}
	return EpisodeDetails{Overview: details.Overview, AirDate: details.AirDate}, nil
}

// tagsFromGenreIDs maps every id present in genreMap onto its Tag(s),
// silently dropping any id this taxonomy has no use for.
func tagsFromGenreIDs(genreMap map[int][]Tag, ids []int) []Tag {
	var tags []Tag
	for _, id := range ids {
		tags = append(tags, genreMap[id]...)
	}
	return tags
}
