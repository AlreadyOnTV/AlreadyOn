package playback

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/library"
)

// SubtitleTrack is a text subtitle stream the browser can show. Bitmap formats
// (PGS, VobSub) are left out: they would need OCR to become text.
type SubtitleTrack struct {
	Index    int    `json:"index"`
	Language string `json:"language,omitempty"`
	Title    string `json:"title,omitempty"`
	Codec    string `json:"codec"`
	Default  bool   `json:"default,omitempty"`
	Forced   bool   `json:"forced,omitempty"`
}

var textSubtitleCodecs = map[string]bool{"subrip": true, "srt": true, "ass": true, "ssa": true, "mov_text": true, "webvtt": true, "text": true}

// SubtitleTracks lists the text subtitle streams of a source.
func SubtitleTracks(src library.ProbeResult) []SubtitleTrack {
	var out []SubtitleTrack
	for _, s := range src.Streams {
		if s.Kind == library.StreamSubtitle && textSubtitleCodecs[s.Codec] {
			out = append(out, SubtitleTrack{Index: s.Index, Language: s.Language, Title: s.Title, Codec: s.Codec, Default: s.Default, Forced: s.Forced})
		}
	}
	return out
}

// Cue is one subtitle line in source-file time.
type Cue struct {
	Start float64 `json:"s"`
	End   float64 `json:"e"`
	Text  string  `json:"t"`
}

const (
	maxSubtitleBytes = 16 << 20
	maxCues          = 30000
)

var (
	srtTime  = regexp.MustCompile(`^(\d+):(\d{2}):(\d{2})[,.](\d{1,3})\s*-->\s*(\d+):(\d{2}):(\d{2})[,.](\d{1,3})`)
	markup   = regexp.MustCompile(`<[^>]*>|\{[^}]*\}`)
	blankRun = regexp.MustCompile(`\n{2,}`)
)

func clock(h, m, s, ms string) float64 {
	hh, _ := strconv.Atoi(h)
	mm, _ := strconv.Atoi(m)
	ss, _ := strconv.Atoi(s)
	frac, _ := strconv.Atoi((ms + "00")[:3])
	return float64(hh*3600+mm*60+ss) + float64(frac)/1000
}

// ParseSRT reads SubRip text into cues, dropping styling markup.
func ParseSRT(text string) []Cue {
	var cues []Cue
	for _, block := range blankRun.Split(strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n"), -1) {
		lines := strings.Split(block, "\n")
		for i, line := range lines {
			m := srtTime.FindStringSubmatch(strings.TrimSpace(line))
			if m == nil {
				continue
			}
			body := strings.TrimSpace(markup.ReplaceAllString(strings.Join(lines[i+1:], "\n"), ""))
			if body != "" && len(cues) < maxCues {
				cues = append(cues, Cue{Start: clock(m[1], m[2], m[3], m[4]), End: clock(m[5], m[6], m[7], m[8]), Text: body})
			}
			break
		}
	}
	return cues
}

// ExtractSubtitles has FFmpeg convert one subtitle stream to SubRip and parses
// it. It reads the whole file, so on a large remux it can take minutes.
func ExtractSubtitles(ctx context.Context, path string, index int) ([]Cue, error) {
	if index < 0 {
		return nil, errors.New("playback: invalid subtitle stream")
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin", "-protocol_whitelist", "file",
		"-i", path, "-map", fmt.Sprintf("0:%d", index), "-f", "srt", "pipe:1")
	cmd.WaitDelay = 5 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(out, maxSubtitleBytes+1))
	if len(raw) > maxSubtitleBytes {
		_ = cmd.Cancel()
		_ = cmd.Wait()
		return nil, errors.New("playback: subtitle track is too large")
	}
	if err := cmd.Wait(); err != nil || readErr != nil {
		return nil, fmt.Errorf("playback: subtitle extraction failed: %v %s", err, strings.TrimSpace(stderr.String()))
	}
	return ParseSRT(string(raw)), nil
}

// SubtitleState is where an extraction stands.
type SubtitleState int

const (
	SubtitlePending SubtitleState = iota
	SubtitleReady
	SubtitleFailed
)

type subtitleEntry struct {
	state  SubtitleState
	cues   []Cue
	failed time.Time
}

// Subtitles extracts tracks in the background, a couple at a time, and keeps
// a bounded number of results in memory. A failed extraction is retried after
// a short pause.
type Subtitles struct {
	mu      sync.Mutex
	entries map[string]*subtitleEntry
	order   []string
	slots   chan struct{}
	extract func(ctx context.Context, path string, index int) ([]Cue, error)
}

const (
	maxCachedTracks  = 24
	retryFailedAfter = 30 * time.Second
	extractTimeout   = 15 * time.Minute
)

func NewSubtitles() *Subtitles { return NewSubtitlesWith(ExtractSubtitles) }

// NewSubtitlesWith uses a different extractor, so tests need no FFmpeg.
func NewSubtitlesWith(extract func(ctx context.Context, path string, index int) ([]Cue, error)) *Subtitles {
	return &Subtitles{entries: map[string]*subtitleEntry{}, slots: make(chan struct{}, 2), extract: extract}
}

// Get returns the cues if they are ready, and starts the extraction if nobody has.
// modTime keys the cache so a replaced file is not served stale subtitles.
func (s *Subtitles) Get(path string, modTime int64, index int) ([]Cue, SubtitleState) {
	key := fmt.Sprintf("%s|%d|%d", path, modTime, index)
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.entries[key]; e != nil {
		if e.state == SubtitleFailed && time.Since(e.failed) > retryFailedAfter {
			delete(s.entries, key)
		} else {
			return e.cues, e.state
		}
	}
	e := &subtitleEntry{}
	s.entries[key] = e
	s.order = append(s.order, key)
	for len(s.order) > maxCachedTracks {
		delete(s.entries, s.order[0])
		s.order = s.order[1:]
	}
	go func() {
		s.slots <- struct{}{}
		defer func() { <-s.slots }()
		ctx, cancel := context.WithTimeout(context.Background(), extractTimeout)
		defer cancel()
		cues, err := s.extract(ctx, path, index)
		s.mu.Lock()
		defer s.mu.Unlock()
		if err != nil {
			e.state, e.failed = SubtitleFailed, time.Now()
			return
		}
		e.state, e.cues = SubtitleReady, cues
	}()
	return nil, SubtitlePending
}
