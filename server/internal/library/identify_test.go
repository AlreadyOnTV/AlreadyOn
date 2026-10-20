package library

import "testing"

func TestIdentify(t *testing.T) {
	cases := []struct {
		name string
		path string
		want Identification
	}{
		{
			name: "year season takes precedence over movie year",
			path: "Tom and Jerry (1940)/Season 1940/Tom and Jerry (1940) - S1940E01 - Puss Gets The Boot [Bluray-720p].mkv",
			want: Identification{IsEpisode: true, Show: "Tom and Jerry (1940)", Season: 1940, Episode: 1, Title: "Puss Gets The Boot"},
		},
		{
			name: "year season folder supplies missing series name",
			path: "Example Show/Season 2026/S2026E123 - News.mkv",
			want: Identification{IsEpisode: true, Show: "Example Show", Season: 2026, Episode: 123, Title: "News"},
		},
		{
			name: "abbreviated year season folder and multi episode marker",
			path: "Example Show/S1940/S1940E01-E02 - Double.mkv",
			want: Identification{IsEpisode: true, Show: "Example Show", Season: 1940, Episode: 1, Title: "Double"},
		},
		{
			name: "cropped resolution does not become a year season",
			path: "Movie (2020) 1920x800.mkv",
			want: Identification{Title: "Movie", Year: 2020},
		},
		{
			name: "arbitrary four digit season does not override movie year",
			path: "Movie (2020) - S1234E01.mkv",
			want: Identification{Title: "Movie", Year: 2020},
		},
		{
			name: "five digit season does not partially match",
			path: "Movie (2020) - S19400E01.mkv",
			want: Identification{Title: "Movie", Year: 2020},
		},
		{
			name: "standard episode with show, season folder, and title",
			path: "Bramble & Fox/Season 02/Bramble & Fox - S02E09 - Rain Day.mkv",
			want: Identification{IsEpisode: true, Show: "Bramble & Fox", Season: 2, Episode: 9, Title: "Rain Day"},
		},
		{
			name: "scene-style dotted filename with quality tag",
			path: "Show.Name.S01E02.Episode.Title.720p.mkv",
			want: Identification{IsEpisode: true, Show: "Show Name", Season: 1, Episode: 2, Title: "Episode Title"},
		},
		{
			name: "filename has no show name, falls back to grandparent folder",
			path: "Bramble & Fox/Season 2/S02E09.mkv",
			want: Identification{IsEpisode: true, Show: "Bramble & Fox", Season: 2, Episode: 9, Title: ""},
		},
		{
			name: "abbreviated season folder (S02, not Season 02)",
			path: "Bramble & Fox/S02/S02E09.mkv",
			want: Identification{IsEpisode: true, Show: "Bramble & Fox", Season: 2, Episode: 9, Title: ""},
		},
		{
			name: "alternate 1x02 style",
			path: "Show Name - 1x02 - Episode Title.mkv",
			want: Identification{IsEpisode: true, Show: "Show Name", Season: 1, Episode: 2, Title: "Episode Title"},
		},
		{
			name: "show title that looks like a year, episode pattern takes priority",
			path: "1923 S01E02 Chapter Two.mkv",
			want: Identification{IsEpisode: true, Show: "1923", Season: 1, Episode: 2, Title: "Chapter Two"},
		},
		{
			name: "movie with parenthesized year in its own folder",
			path: "Movies/Movie Title (2020)/Movie Title (2020).mkv",
			want: Identification{IsEpisode: false, Title: "Movie Title", Year: 2020},
		},
		{
			name: "scene-style dotted movie filename with bare year and quality tag",
			path: "Movie.Title.2020.1080p.mkv",
			want: Identification{IsEpisode: false, Title: "Movie Title", Year: 2020},
		},
		{
			name: "title containing 'se' + digit does not false-positive as an episode",
			path: "Se7en (1995).mkv",
			want: Identification{IsEpisode: false, Title: "Se7en", Year: 1995},
		},
		{
			name: "resolution tag 1920x1080 does not false-positive as 1x02-style episode",
			path: "Movie Title (2011) 1920x1080.mkv",
			want: Identification{IsEpisode: false, Title: "Movie Title", Year: 2011},
		},
		{
			// Real-world scene naming: the quality tag starts inside a
			// bracket group, not at the start of the string — the
			// dangling "[" this used to leave behind must be trimmed too.
			name: "bracketed quality tag group leaves no dangling bracket",
			path: "Show Name (2019) - S04E01 - Episode Title [WEBDL-1080p][EAC3 Atmos 5.1][x264]-FLUX.mkv",
			want: Identification{IsEpisode: true, Show: "Show Name (2019)", Season: 4, Episode: 1, Title: "Episode Title"},
		},
		{
			// Plex and Sonarr name a two-episode file with a range; the
			// second episode's marker is not part of the title.
			name: "multi-episode range",
			path: "Show Name (2005) - S02E18-E19 - Double Feature [WEBDL-1080p].mkv",
			want: Identification{IsEpisode: true, Show: "Show Name (2005)", Season: 2, Episode: 18, Title: "Double Feature"},
		},
		{
			name: "multi-episode run-together",
			path: "Show Name - S02E18E19 - Double Feature.mkv",
			want: Identification{IsEpisode: true, Show: "Show Name", Season: 2, Episode: 18, Title: "Double Feature"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Identify(tc.path)
			if !ok {
				t.Fatalf("Identify(%q) failed, want success with %+v", tc.path, tc.want)
			}
			if got != tc.want {
				t.Errorf("Identify(%q) = %+v, want %+v", tc.path, got, tc.want)
			}
		})
	}
}

func TestIdentify_AmbiguousFiles_ReturnFalse(t *testing.T) {
	cases := []string{
		"Video001.mkv",
		"IMG_1234.mkv",
		"Untitled.mkv",
		// No parenthesized year and 1408 isn't a plausible year (out of
		// the 1900-2099 range this package treats as a real signal) —
		// genuinely unresolvable from the filename alone, without an
		// external metadata lookup this package deliberately doesn't do.
		"Room 1408.mkv",
	}
	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			if _, ok := Identify(path); ok {
				t.Errorf("Identify(%q) unexpectedly succeeded, want an ambiguous (false) result", path)
			}
		})
	}
}

// Explicit year-season bounds must not silently grow into arbitrary numeric IDs.
func TestIdentifyYearSeasonBounds(t *testing.T) {
	for _, tc := range []struct {
		path    string
		episode bool
	}{
		{"Show (1940) S1900E01.mkv", true},
		{"Show (1940) S2099E01.mkv", true},
		{"Movie (1940) S1899E01.mkv", false},
		{"Movie (1940) S2100E01.mkv", false},
	} {
		got, ok := Identify(tc.path)
		if !ok || got.IsEpisode != tc.episode {
			t.Fatalf("%s: %+v, ok=%v", tc.path, got, ok)
		}
	}
}
