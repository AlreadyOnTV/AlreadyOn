package playback

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/alreadyonapp/alreadyon/server/internal/config"
	"github.com/alreadyonapp/alreadyon/server/internal/library"
)

type subtitleSidecar struct {
	root, directory, name string
	info                  os.FileInfo
}

type subtitleVariant struct {
	language    string
	forced, sdh bool
}

var sidecarLanguage = regexp.MustCompile(`^[a-z]{2,3}([_-][a-z]{2,8}|-[0-9]{3})*$`)

func sidecarTrack(stem, name string) (SubtitleTrack, bool) {
	ext := filepath.Ext(name)
	if !strings.EqualFold(ext, ".srt") {
		return SubtitleTrack{}, false
	}
	base := strings.TrimSuffix(name, ext)
	track := SubtitleTrack{Codec: "subrip", Language: "und", Title: "External SRT"}
	if base != stem {
		suffix, ok := strings.CutPrefix(base, stem+".")
		if !ok || suffix == "" {
			return SubtitleTrack{}, false
		}
		languageSet := false
		for _, part := range strings.Split(strings.ToLower(suffix), ".") {
			switch part {
			case "forced":
				if track.Forced {
					return SubtitleTrack{}, false
				}
				track.Forced = true
			case "sdh":
				if track.SDH {
					return SubtitleTrack{}, false
				}
				track.SDH = true
				track.Title = "External SRT (SDH)"
			case "default":
				if track.Default {
					return SubtitleTrack{}, false
				}
				track.Default = true
			default:
				if languageSet || !sidecarLanguage.MatchString(part) {
					return SubtitleTrack{}, false
				}
				track.Language = strings.ReplaceAll(part, "_", "-")
				languageSet = true
			}
		}
	}
	// Negative IDs cannot conflict with embedded streams. A filename-derived ID
	// also prevents removing a neighboring file from changing a selection.
	sum := sha256.Sum256([]byte(name))
	track.Index = -1 - int(binary.BigEndian.Uint32(sum[:4])&0x7fffffff)
	return track, true
}

func subtitleKey(t SubtitleTrack) subtitleVariant {
	language := audioLanguage(t.Language)
	if language == "und" {
		language = ""
	}
	return subtitleVariant{language, t.Forced, t.SDH}
}

// SourceSubtitles adds sidecars to the existing embedded catalog. Embedded
// tracks take precedence for the same language, forced and SDH flags. Among
// sidecars for a variant, default wins, then filename order.
func SourceSubtitles(ctx context.Context, db *sql.DB, path string, source library.ProbeResult) []SubtitleTrack {
	tracks := SubtitleTracks(source)
	if tracks == nil {
		tracks = []SubtitleTrack{}
	}
	if !sidecarsSupported {
		return tracks
	}
	real, err := AllowedPath(ctx, db, path)
	if err != nil {
		return tracks
	}
	cfg, err := library.LoadScanConfig(ctx, db)
	if err != nil {
		return tracks
	}
	for _, root := range cfg.Roots {
		if config.PathWithinRoot(root, real) {
			return appendSidecars(tracks, root.RealPath, real)
		}
	}
	return tracks
}

func appendSidecars(tracks []SubtitleTrack, rootPath, videoPath string) []SubtitleTrack {
	dir, err := filepath.Rel(rootPath, filepath.Dir(videoPath))
	if err != nil || !filepath.IsLocal(dir) {
		return tracks
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return tracks
	}
	defer root.Close()
	folder, err := root.OpenRoot(dir)
	if err != nil {
		return tracks
	}
	defer folder.Close()
	entries, err := fs.ReadDir(folder.FS(), ".")
	if err != nil {
		return tracks
	}
	seen := make(map[subtitleVariant]bool)
	for _, track := range tracks {
		seen[subtitleKey(track)] = true
	}
	stem := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	var candidates []SubtitleTrack
	ids := make(map[int]int)
	for _, entry := range entries {
		track, ok := sidecarTrack(stem, entry.Name())
		if !ok {
			continue
		}
		info, err := folder.Lstat(entry.Name())
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxSubtitleBytes {
			continue
		}
		track.sidecar = &subtitleSidecar{rootPath, dir, entry.Name(), info}
		candidates = append(candidates, track)
		ids[track.Index]++
	}
	slices.SortStableFunc(candidates, func(a, b SubtitleTrack) int {
		if a.Default == b.Default {
			return 0
		}
		if a.Default {
			return -1
		}
		return 1
	})
	for _, track := range candidates {
		key := subtitleKey(track)
		if !seen[key] && ids[track.Index] == 1 {
			tracks = append(tracks, track)
			seen[key] = true
		}
	}
	return tracks
}

var errInvalidSidecar = errors.New("playback: invalid or unavailable SRT sidecar")

func (s *subtitleSidecar) cues() ([]Cue, error) {
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, errInvalidSidecar
	}
	defer root.Close()
	f, err := openSidecar(root, filepath.Join(s.directory, s.name))
	if err != nil {
		return nil, errInvalidSidecar
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(info, s.info) || info.Size() != s.info.Size() || !info.ModTime().Equal(s.info.ModTime()) {
		return nil, errInvalidSidecar
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxSubtitleBytes+1))
	if err != nil || len(raw) > maxSubtitleBytes {
		return nil, errInvalidSidecar
	}
	return parseSidecarSRT(raw)
}

const maxCueBytes = 16 << 10

var sidecarTime = regexp.MustCompile(`^([0-9]{2,5}):([0-5][0-9]):([0-5][0-9]),([0-9]{3})[ \t]+-->[ \t]+([0-9]{2,5}):([0-5][0-9]):([0-5][0-9]),([0-9]{3})$`)
var sidecarBlank = regexp.MustCompile(`\n(?:[ \t]*\n)+`)

func parseSidecarSRT(raw []byte) ([]Cue, error) {
	text := string(raw)
	if !utf8.Valid(raw) || strings.ContainsRune(text, 0) {
		return nil, errInvalidSidecar
	}
	text = strings.TrimPrefix(text, "\ufeff")
	text = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"))
	var cues []Cue
	// Keep serialized cues within the Android client's response limit.
	jsonBudget := (8 << 20) - len(`{"cues":[]}
`)
	for _, block := range sidecarBlank.Split(text, maxCues+1) {
		number, rest, ok := strings.Cut(block, "\n")
		seq, err := strconv.Atoi(strings.TrimSpace(number))
		if !ok || err != nil || seq < 0 {
			return nil, errInvalidSidecar
		}
		line, body, ok := strings.Cut(rest, "\n")
		m := sidecarTime.FindStringSubmatch(strings.TrimSpace(line))
		if !ok || m == nil || len(body) > maxCueBytes || len(cues) == maxCues {
			return nil, errInvalidSidecar
		}
		start, end := clock(m[1], m[2], m[3], m[4]), clock(m[5], m[6], m[7], m[8])
		body = strings.TrimSpace(markup.ReplaceAllString(body, ""))
		if end <= start || body == "" {
			return nil, errInvalidSidecar
		}
		cue := Cue{Start: start, End: end, Text: body}
		encoded, err := json.Marshal(cue)
		jsonBudget -= len(encoded) + 1
		if err != nil || jsonBudget < 0 {
			return nil, errInvalidSidecar
		}
		cues = append(cues, cue)
	}
	// Overlap is legal in SRT. Sort cues for clients that seek by start time.
	slices.SortStableFunc(cues, func(a, b Cue) int {
		if a.Start < b.Start {
			return -1
		}
		if a.Start > b.Start {
			return 1
		}
		return 0
	})
	return cues, nil
}

func (s *Subtitles) GetSidecar(track SubtitleTrack) ([]Cue, SubtitleState) {
	sidecar := track.sidecar
	key := fmt.Sprintf("%q|%q|%q|%s|%d|%d", sidecar.root, sidecar.directory, sidecar.name, sidecarFileID(sidecar.info), sidecar.info.ModTime().UnixNano(), sidecar.info.Size())
	return s.get(key, func(context.Context) ([]Cue, error) { return sidecar.cues() })
}
