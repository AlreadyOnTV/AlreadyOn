package library

import (
	"path/filepath"
	"strings"

	"github.com/alreadyonapp/alreadyon/server/internal/metadata"
)

// lookupName is the name to search a metadata provider with for an item
// shown as display (a series or movie title), found at path. Sonarr,
// Radarr and Plex put the year, region and {imdb-…}/{tmdb-…}/{tvdb-…} ID
// tags in the show or movie folder — often not in the file name, so not in
// display — and those tell same-named titles apart, so they are added back
// here. The folder's year and region only count when the folder names the
// same title (a flat "/TV Shows/Show - S01E01.mkv" layout must not pick
// up "TV Shows"), while ID tags in the folder or file name always count.
func lookupName(display, path string) string {
	p := metadata.ParseName(display)
	dir, file := filepath.Split(path)

	if folder := showFolderName(dir); folder != "" && metadata.SameTitle(folder, display) {
		f := metadata.ParseName(folder)
		if p.Year == 0 {
			p.Year = f.Year
		}
		if p.Region == "" {
			p.Region = f.Region
		}
		mergeIDs(&p, f)
	}
	mergeIDs(&p, metadata.ParseName(strings.TrimSuffix(file, filepath.Ext(file))))
	return p.String()
}

func mergeIDs(into *metadata.ParsedName, from metadata.ParsedName) {
	if into.TMDbID == 0 {
		into.TMDbID = from.TMDbID
	}
	if into.IMDbID == "" {
		into.IMDbID = from.IMDbID
	}
	if into.TVDBID == 0 {
		into.TVDBID = from.TVDBID
	}
}
