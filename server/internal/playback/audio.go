package playback

import (
	"errors"
	"slices"
	"strings"

	"github.com/alreadyonapp/alreadyon/server/internal/library"
)

// AudioTrack describes every selectable source audio stream, including alternate
// mixes and commentary in the same language.
type AudioTrack struct {
	Index    int    `json:"index"`
	Language string `json:"language,omitempty"`
	Title    string `json:"title,omitempty"`
	Codec    string `json:"codec"`
	Channels int    `json:"channels"`
	Default  bool   `json:"default,omitempty"`
}

func AudioTracks(source library.ProbeResult) []AudioTrack {
	tracks := []AudioTrack{}
	for _, s := range source.Streams {
		if s.Kind == library.StreamAudio {
			tracks = append(tracks, AudioTrack{s.Index, s.Language, s.Title, s.Codec, s.Channels, s.Default})
		}
	}
	slices.SortFunc(tracks, func(a, b AudioTrack) int { return a.Index - b.Index })
	return tracks
}

func audioLanguage(code string) string {
	code = strings.ToLower(strings.Split(strings.ReplaceAll(code, "_", "-"), "-")[0])
	for _, group := range strings.Fields("en,eng es,spa fr,fra,fre de,deu,ger it,ita pt,por ja,jpn ko,kor zh,zho,chi ru,rus nl,nld,dut sv,swe da,dan no,nor,nob fi,fin pl,pol tr,tur ar,ara he,heb hi,hin cs,ces,cze el,ell,gre hu,hun th,tha uk,ukr") {
		codes := strings.Split(group, ",")
		if slices.Contains(codes, code) {
			return codes[0]
		}
	}
	return code
}

// SelectAudio returns a private stream slice with the selected audio first, so
// planning, decoder checks, FFmpeg mapping and details use the same track.
// Preserve original stream indices and all tracks for the selection menu.
func SelectAudio(source library.ProbeResult, language string, explicit *int) (library.ProbeResult, error) {
	tracks := AudioTracks(source)
	if len(tracks) == 0 {
		if explicit != nil {
			return source, errors.New("audio track is not available for this title")
		}
		return source, nil
	}
	chosen := tracks[0].Index
	if explicit != nil {
		if !slices.ContainsFunc(tracks, func(t AudioTrack) bool { return t.Index == *explicit }) {
			return source, errors.New("audio track is not available for this title")
		}
		chosen = *explicit
	} else {
		if language == "" {
			language = "en"
		}
		for _, t := range tracks {
			if t.Default {
				chosen = t.Index
				break
			}
		}
		found := false
		for _, t := range tracks {
			if audioLanguage(t.Language) == audioLanguage(language) {
				if !found || t.Default {
					chosen = t.Index
					found = true
				}
				if t.Default {
					break
				}
			}
		}
	}
	source.Streams = slices.Clone(source.Streams)
	slices.SortStableFunc(source.Streams, func(a, b library.StreamInfo) int {
		if a.Index == b.Index {
			return 0
		}
		if a.Kind == library.StreamAudio && a.Index == chosen {
			return -1
		}
		if b.Kind == library.StreamAudio && b.Index == chosen {
			return 1
		}
		return 0
	})
	return source, nil
}
