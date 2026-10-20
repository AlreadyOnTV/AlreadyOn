package library

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Identification is the result of matching a file to a movie or TV
// episode from its folder structure and filename conventions only (no
// external metadata provider).
type Identification struct {
	IsEpisode bool
	Show      string // episodes only
	Season    int    // episodes only
	Episode   int    // episodes only
	Year      int    // movies only, when present in the filename
	Title     string
}

var (
	// "S01E02", "s1e2", etc. Checked before any year pattern, so a show
	// whose title happens to look like a year (e.g. "24") or a movie
	// title containing "se" followed by a digit doesn't get misread.
	// Some libraries organize shorts/daily shows by year (S1940E01).
	// Bound year seasons to 1900–2099 rather than accepting arbitrary long numbers.
	seasonEpisodeRe = regexp.MustCompile(`(?i)s(\d{1,2}|(?:19|20)\d{2})e(\d{1,3})`)

	// Keep alternate "1x02" seasons short: year-like numbers here are
	// ambiguous with resolution tags such as "1920x800".
	altSeasonEpisodeRe = regexp.MustCompile(`(?i)\b(\d{1,2})x(\d{2,3})\b`)

	// A year in parentheses is a deliberate, reliable signal (the
	// standard Plex/Jellyfin movie-naming convention); a bare year is a
	// weaker fallback, tried second.
	parenYearRe = regexp.MustCompile(`\((\d{4})\)`)
	bareYearRe  = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)

	seasonFolderRe = regexp.MustCompile(`(?i)^s(?:eason)?\s*(?:\d{1,2}|(?:19|20)\d{2})$`)

	// The rest of a multi-episode marker after the first episode: the "-E02"
	// in "S01E01-E02" and the "E02" in "S01E01E02".
	extraEpisodesRe = regexp.MustCompile(`(?i)^(?:\s*-?\s*e\d{1,3})+`)

	qualityTagsRe = regexp.MustCompile(`(?i)\b(720p|1080p|2160p|4k|hdtv|web[- ]?dl|webrip|bluray|dvdrip|x264|x265|hevc|aac\d*|dts)\b.*$`)

	spaceCollapseRe = regexp.MustCompile(`\s+`)
)

// Identify attempts to classify relativePath (a file path relative to
// its configured media/commercial root) as a movie or episode using
// only its filename and enclosing folder names. The second return value
// reports whether identification succeeded — a file with neither a
// season/episode pattern nor a plausible year is genuinely ambiguous
// without an external metadata lookup, which is out of scope here.
func Identify(relativePath string) (Identification, bool) {
	dir, file := filepath.Split(relativePath)
	base := strings.TrimSuffix(file, filepath.Ext(file))
	normalized := normalizeTitle(base)

	if loc := seasonEpisodeRe.FindStringSubmatchIndex(normalized); loc != nil {
		return identifyEpisode(normalized, dir, loc), true
	}
	if loc := altSeasonEpisodeRe.FindStringSubmatchIndex(normalized); loc != nil {
		return identifyEpisode(normalized, dir, loc), true
	}

	if loc := parenYearRe.FindStringSubmatchIndex(normalized); loc != nil {
		return identifyMovie(normalized, loc), true
	}
	if loc := bareYearRe.FindStringSubmatchIndex(normalized); loc != nil {
		return identifyMovie(normalized, loc), true
	}

	return Identification{}, false
}

func identifyEpisode(normalized, dir string, loc []int) Identification {
	season, _ := strconv.Atoi(normalized[loc[2]:loc[3]])
	episode, _ := strconv.Atoi(normalized[loc[4]:loc[5]])

	show := cleanFragment(normalized[:loc[0]])
	if show == "" {
		show = showFromFolder(dir)
	}
	title := stripQualityTags(cleanFragment(extraEpisodesRe.ReplaceAllString(normalized[loc[1]:], "")))

	return Identification{
		IsEpisode: true,
		Show:      show,
		Season:    season,
		Episode:   episode,
		Title:     title,
	}
}

func identifyMovie(normalized string, loc []int) Identification {
	year, _ := strconv.Atoi(normalized[loc[2]:loc[3]])
	title := cleanFragment(normalized[:loc[0]])
	return Identification{
		IsEpisode: false,
		Year:      year,
		Title:     title,
	}
}

// showFromFolder walks a file's enclosing directories from nearest to
// furthest, skipping any that look like a season folder ("Season 01",
// "S01"), and returns the first remaining name as the show's title. Only
// meaningful when the filename itself didn't already carry the show
// name (a common convention when files are already organized into a
// show's own folder).
func showFromFolder(dir string) string {
	return normalizeTitle(showFolderName(dir))
}

// showFolderName is showFromFolder without normalizing the name, so
// "{imdb-tt123}" tags and "(2019)" years survive for provider lookups.
func showFolderName(dir string) string {
	dir = strings.TrimSuffix(filepath.ToSlash(dir), "/")
	if dir == "" || dir == "." {
		return ""
	}
	parts := strings.Split(dir, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		p := strings.TrimSpace(parts[i])
		if p == "" || seasonFolderRe.MatchString(p) {
			continue
		}
		return p
	}
	return ""
}

func normalizeTitle(s string) string {
	s = strings.ReplaceAll(s, ".", " ")
	s = strings.ReplaceAll(s, "_", " ")
	s = spaceCollapseRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func cleanFragment(s string) string {
	return strings.Trim(strings.TrimSpace(s), "-–— ")
}

func stripQualityTags(s string) string {
	stripped := qualityTagsRe.ReplaceAllString(s, "")
	// The quality-tag match itself starts at the tag word (e.g.
	// "WEBDL"), not at a preceding "[" — a title like "400 Boys
	// [WEBDL-1080p]..." would otherwise end up as "400 Boys [" once the
	// tag and everything after it is removed. Trim any such dangling
	// bracket/dash left right at the end.
	return strings.TrimRight(strings.TrimSpace(stripped), " ([{-")
}
