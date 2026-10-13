package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/governor"
)

// KeylessClient classifies content using providers that need no account
// or API key (#112): TVmaze for TV series and Wikidata for movies. It's
// the fallback until an admin configures TMDb.
//
// TVmaze data is CC BY-SA and must be credited (see the Server Settings
// attribution). Wikidata data is CC0. Both ask for a descriptive
// User-Agent; TVmaze allows at least 20 calls per 10 seconds, and the
// Wikidata Query Service asks clients to stay well within its limits, so
// requests are throttled per provider below.
type KeylessClient struct {
	HTTPClient       *http.Client
	TVmazeBase       string
	WikidataEndpoint string
	UserAgent        string
	// MinInterval spaces consecutive requests to the same provider.
	MinInterval map[string]time.Duration

	mu       sync.Mutex
	lastCall map[string]time.Time
}

const (
	providerTVmaze   = "tvmaze"
	providerWikidata = "wikidata"
)

// NewKeylessClient returns a KeylessClient pointed at the real providers.
func NewKeylessClient() *KeylessClient {
	return &KeylessClient{
		HTTPClient:       &http.Client{Transport: governor.BackgroundTransport{}, Timeout: 20 * time.Second},
		TVmazeBase:       "https://api.tvmaze.com",
		WikidataEndpoint: "https://query.wikidata.org/sparql",
		UserAgent:        "AlreadyOn (+https://github.com/AlreadyOnTV/AlreadyOn)",
		MinInterval: map[string]time.Duration{
			providerTVmaze:   600 * time.Millisecond,
			providerWikidata: 1500 * time.Millisecond,
		},
	}
}

// KeylessResult carries best-effort tags and a provider date. Dates remain
// separate from TMDb data; neither genres nor viewer scores establish an age rating.
type KeylessResult struct {
	Tags        []Tag
	ReleaseDate string
	Provider    string
}

func tvmazeResult(show tvmazeShow, confirmed bool) KeylessResult {
	result := KeylessResult{Tags: tvmazeTags(show.Genres, show.Type, show.Language), Provider: providerTVmaze}
	if confirmed {
		result.ReleaseDate = validProviderDate(show.Premiered)
	}
	return result
}

// validProviderDate rejects malformed dates instead of turning them into an era.
func validProviderDate(raw string) string {
	if len(raw) < 10 {
		return ""
	}
	date := raw[:10]
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return ""
	}
	return date
}

type tvmazeShow struct {
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Language  string   `json:"language"`
	Genres    []string `json:"genres"`
	Premiered string   `json:"premiered"`
}

type tvmazeSearchResult struct {
	Show tvmazeShow `json:"show"`
}

// ClassifyEpisode looks seriesTitle (a Plex-style name, see ParseName) up
// on TVmaze and maps the best match's genres and show type onto Tag: by
// {imdb-ttN} tag when there is one, else by searching spellings of the
// title, preferring an exact title that premiered within a year of year
// (or of the year in the name). No match returns (nil, nil).
func (c *KeylessClient) ClassifyEpisode(ctx context.Context, title string, year int) ([]Tag, error) {
	result, err := c.LookupEpisode(ctx, title, year)
	return result.Tags, err
}

// LookupEpisode also retains the premiere date of an exact title/ID match.
func (c *KeylessClient) LookupEpisode(ctx context.Context, seriesTitle string, year int) (KeylessResult, error) {
	p := ParseName(seriesTitle)
	if year == 0 {
		year = p.Year
	}
	if p.IMDbID != "" {
		var show tvmazeShow
		u := c.TVmazeBase + "/lookup/shows?imdb=" + url.QueryEscape(p.IMDbID)
		if err := c.getJSON(ctx, providerTVmaze, u, "application/json", &show); err == nil && show.Name != "" {
			return tvmazeResult(show, true), nil
		}
	}
	var fallback *tvmazeShow
	for _, variant := range titleVariants(p.Title) {
		u := c.TVmazeBase + "/search/shows?q=" + url.QueryEscape(variant)
		var results []tvmazeSearchResult
		if err := c.getJSON(ctx, providerTVmaze, u, "application/json", &results); err != nil {
			return KeylessResult{}, err
		}
		if len(results) == 0 {
			continue
		}
		if fallback == nil {
			fallback = &results[0].Show
		}
		want := normalizeForMatch(p.Title)
		var sameTitle *tvmazeShow
		for i := range results {
			s := &results[i].Show
			if normalizeForMatch(s.Name) != want {
				continue
			}
			if year == 0 {
				return tvmazeResult(*s, true), nil
			}
			if y := leadingYear(s.Premiered); y > 0 && absInt(y-year) <= 1 {
				return tvmazeResult(*s, true), nil
			}
			if sameTitle == nil {
				sameTitle = s
			}
		}
		if sameTitle != nil && fallback == &results[0].Show {
			fallback = sameTitle
		}
	}
	if fallback == nil {
		return KeylessResult{}, nil
	}
	return tvmazeResult(*fallback, false), nil
}

// tvmazeGenreTags maps TVmaze's genre vocabulary onto Tag. Genres with
// no entry (Legal, Medical, Food, Travel, ...) don't help channel
// matching and are dropped.
var tvmazeGenreTags = map[string][]Tag{
	"Action":          {TagAction},
	"Adventure":       {TagAdventure},
	"Anime":           {TagAnime, TagAnimation},
	"Children":        {TagKids},
	"Comedy":          {TagComedy},
	"Crime":           {TagCrime},
	"Drama":           {TagDrama},
	"Espionage":       {TagThriller},
	"Family":          {TagFamily},
	"Fantasy":         {TagFantasy},
	"History":         {TagHistory},
	"Horror":          {TagHorror},
	"Music":           {TagMusic},
	"Mystery":         {TagMystery},
	"Romance":         {TagRomance},
	"Science-Fiction": {TagSciFi},
	"Supernatural":    {TagFantasy},
	"Thriller":        {TagThriller},
	"War":             {TagWar},
	"Western":         {TagWestern},
}

var tvmazeTypeTags = map[string][]Tag{
	"Animation":   {TagAnimation},
	"Documentary": {TagDocumentary},
	"Reality":     {TagReality},
	"Game Show":   {TagReality},
}

func tvmazeTags(genres []string, showType, language string) []Tag {
	var tags []Tag
	for _, g := range genres {
		tags = appendUnique(tags, tvmazeGenreTags[g]...)
	}
	tags = appendUnique(tags, tvmazeTypeTags[showType]...)
	if language == "Japanese" && containsTag(tags, TagAnimation) {
		tags = appendUnique(tags, TagAnime)
	}
	return tags
}

type sparqlResponse struct {
	Results struct {
		Bindings []map[string]struct {
			Value string `json:"value"`
		} `json:"bindings"`
	} `json:"results"`
}

// ClassifyMovie looks title up on Wikidata (restricted to films) and
// maps the best match's genre and film-type labels onto Tag. year (when
// > 0) must be within a year of the film's earliest publication date,
// so a same-titled remake isn't mistaken for the file's film.
func (c *KeylessClient) ClassifyMovie(ctx context.Context, title string, year int) ([]Tag, error) {
	result, err := c.LookupMovie(ctx, title, year)
	return result.Tags, err
}

// LookupMovie retains a publication date when the supplied year matched.
func (c *KeylessClient) LookupMovie(ctx context.Context, title string, year int) (KeylessResult, error) {
	query := fmt.Sprintf(`SELECT ?item ?num (MIN(YEAR(?date)) AS ?year) (MIN(?date) AS ?publicationDate) (GROUP_CONCAT(DISTINCT ?label; separator="|") AS ?labels) WHERE {
  SERVICE wikibase:mwapi {
    bd:serviceParam wikibase:endpoint "www.wikidata.org"; wikibase:api "EntitySearch";
      mwapi:search "%s"; mwapi:language "en"; mwapi:limit "10".
    ?item wikibase:apiOutputItem mwapi:item.
    ?num wikibase:apiOrdinal true.
  }
  ?item wdt:P31/wdt:P279* wd:Q11424.
  OPTIONAL { ?item wdt:P577 ?date. }
  OPTIONAL { { ?item wdt:P136 ?g. } UNION { ?item wdt:P31 ?g. } ?g rdfs:label ?label. FILTER(LANG(?label) = "en") }
} GROUP BY ?item ?num ORDER BY ?num`, sparqlEscape(title))

	u := c.WikidataEndpoint + "?query=" + url.QueryEscape(query)
	var resp sparqlResponse
	if err := c.getJSON(ctx, providerWikidata, u, "application/sparql-results+json", &resp); err != nil {
		return KeylessResult{}, err
	}
	for _, b := range resp.Results.Bindings {
		if year > 0 {
			y, _ := strconv.Atoi(b["year"].Value)
			if y == 0 || absInt(y-year) > 1 {
				continue
			}
		}
		result := KeylessResult{Tags: tagsFromLabels(strings.Split(b["labels"].Value, "|")), Provider: providerWikidata}
		// A matching supplied year disambiguates remakes. Without it the genre
		// lookup stays best effort, but the date cannot establish an era.
		if year > 0 {
			result.ReleaseDate = validProviderDate(b["publicationDate"].Value)
		}
		return result, nil
	}
	return KeylessResult{}, nil
}

// labelKeywords maps substrings of Wikidata's English genre/film-type
// labels ("science fiction film", "animated feature film", "romantic
// comedy", ...) onto Tag. Wikidata's genre vocabulary is open-ended, so
// keyword matching is more robust than a fixed list of item IDs.
var labelKeywords = []struct {
	keyword string
	tags    []Tag
}{
	{"action", []Tag{TagAction}},
	{"superhero", []Tag{TagAction}},
	{"adventure", []Tag{TagAdventure}},
	{"swashbuckler", []Tag{TagAdventure}},
	{"anime", []Tag{TagAnime, TagAnimation}},
	{"animated", []Tag{TagAnimation}},
	{"animation", []Tag{TagAnimation}},
	{"comedy", []Tag{TagComedy}},
	{"parody", []Tag{TagComedy}},
	{"crime", []Tag{TagCrime}},
	{"gangster", []Tag{TagCrime}},
	{"heist", []Tag{TagCrime}},
	{"noir", []Tag{TagCrime}},
	{"documentary", []Tag{TagDocumentary}},
	{"drama", []Tag{TagDrama}},
	{"family", []Tag{TagFamily}},
	{"children's", []Tag{TagFamily, TagKids}},
	{"fantasy", []Tag{TagFantasy}},
	{"fairy tale", []Tag{TagFantasy}},
	{"historical", []Tag{TagHistory}},
	{"history", []Tag{TagHistory}},
	{"horror", []Tag{TagHorror}},
	{"slasher", []Tag{TagHorror}},
	{"music", []Tag{TagMusic}},
	{"mystery", []Tag{TagMystery}},
	{"detective", []Tag{TagMystery}},
	{"romance", []Tag{TagRomance}},
	{"romantic", []Tag{TagRomance}},
	{"science fiction", []Tag{TagSciFi}},
	{"cyberpunk", []Tag{TagSciFi}},
	{"space opera", []Tag{TagSciFi}},
	{"thriller", []Tag{TagThriller}},
	{"suspense", []Tag{TagThriller}},
	{"western", []Tag{TagWestern}},
}

func tagsFromLabels(labels []string) []Tag {
	var tags []Tag
	for _, raw := range labels {
		label := strings.ReplaceAll(strings.ToLower(raw), "live-action", "")
		if label == "" {
			continue
		}
		for _, kw := range labelKeywords {
			if strings.Contains(label, kw.keyword) {
				tags = appendUnique(tags, kw.tags...)
			}
		}
		// Whole-word match only, so "Star Wars"-style labels don't count.
		for _, word := range strings.FieldsFunc(label, func(r rune) bool { return r < 'a' || r > 'z' }) {
			if word == "war" {
				tags = appendUnique(tags, TagWar)
			}
		}
	}
	return tags
}

// sparqlEscape makes s safe to embed inside a double-quoted SPARQL
// string literal.
func sparqlEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n', '\r', '\t':
			b.WriteByte(' ')
		default:
			if r >= 0x20 {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// getJSON performs a throttled GET, retrying once after a pause on HTTP
// 429 as TVmaze's docs recommend.
func (c *KeylessClient) getJSON(ctx context.Context, provider, rawURL, accept string, out any) error {
	for attempt := 0; ; attempt++ {
		if err := c.wait(ctx, provider); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return fmt.Errorf("metadata: %s: %w", provider, err)
		}
		req.Header.Set("User-Agent", c.UserAgent)
		req.Header.Set("Accept", accept)
		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			return fmt.Errorf("metadata: %s: %w", provider, err)
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt == 0 {
			resp.Body.Close()
			pause := 5 * time.Second
			if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && secs > 0 && secs <= 60 {
				pause = time.Duration(secs) * time.Second
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(pause):
			}
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("metadata: %s: unexpected status %d", provider, resp.StatusCode)
		}
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("metadata: %s: decode: %w", provider, err)
		}
		return nil
	}
}

func (c *KeylessClient) wait(ctx context.Context, provider string) error {
	c.mu.Lock()
	if c.lastCall == nil {
		c.lastCall = make(map[string]time.Time)
	}
	next := c.lastCall[provider].Add(c.MinInterval[provider])
	now := time.Now()
	if next.Before(now) {
		next = now
	}
	c.lastCall[provider] = next
	c.mu.Unlock()

	if d := time.Until(next); d > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}
	return nil
}

func leadingYear(date string) int {
	if len(date) < 4 {
		return 0
	}
	y, _ := strconv.Atoi(date[:4])
	return y
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func appendUnique(tags []Tag, add ...Tag) []Tag {
	for _, t := range add {
		if !containsTag(tags, t) {
			tags = append(tags, t)
		}
	}
	return tags
}

func containsTag(tags []Tag, t Tag) bool {
	for _, have := range tags {
		if have == t {
			return true
		}
	}
	return false
}
