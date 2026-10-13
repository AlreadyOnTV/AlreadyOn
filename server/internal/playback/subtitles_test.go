package playback

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/library"
)

func TestSubtitleTracksListsOnlyTextFormats(t *testing.T) {
	src := library.ProbeResult{Streams: []library.StreamInfo{
		{Index: 0, Kind: library.StreamVideo, Codec: "h264"},
		{Index: 2, Kind: library.StreamSubtitle, Codec: "subrip", Language: "eng", Title: "English", Default: true},
		{Index: 3, Kind: library.StreamSubtitle, Codec: "hdmv_pgs_subtitle", Language: "eng"},
		{Index: 4, Kind: library.StreamSubtitle, Codec: "ass", Language: "spa", Forced: true},
	}}
	got := SubtitleTracks(src)
	if len(got) != 2 || got[0].Index != 2 || !got[0].Default || got[1].Index != 4 || !got[1].Forced || got[1].Language != "spa" {
		t.Fatalf("tracks = %+v, want the SubRip and ASS tracks only", got)
	}
}

func TestParseSRT(t *testing.T) {
	cues := ParseSRT("\ufeff1\r\n00:00:01,500 --> 00:00:03,000\r\n<i>Hello</i> {\\an8}there\r\n\r\n2\r\n01:02:03,004 --> 01:02:05,000\r\nTwo\r\nlines\r\n\r\n3\r\n00:00:09,000 --> 00:00:10,000\r\n{\\an8}\r\n")
	if len(cues) != 2 {
		t.Fatalf("cues = %+v, want 2 (an empty cue is dropped)", cues)
	}
	if cues[0].Start != 1.5 || cues[0].End != 3 || cues[0].Text != "Hello there" {
		t.Errorf("first cue = %+v, want markup stripped", cues[0])
	}
	if cues[1].Start != 3723.004 || cues[1].Text != "Two\nlines" {
		t.Errorf("second cue = %+v", cues[1])
	}
}

func TestSubtitlesExtractOncePerTrackAndRetryFailures(t *testing.T) {
	var calls atomic.Int32
	fail := atomic.Bool{}
	fail.Store(true)
	s := NewSubtitlesWith(func(ctx context.Context, path string, index int) ([]Cue, error) {
		calls.Add(1)
		if fail.Load() {
			return nil, errors.New("boom")
		}
		return []Cue{{Start: 1, End: 2, Text: "hi"}}, nil
	})
	wait := func() SubtitleState {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if _, st := s.Get("/a.mkv", 1, 2); st != SubtitlePending {
				return st
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("extraction did not finish")
		return SubtitlePending
	}
	if st := wait(); st != SubtitleFailed {
		t.Fatalf("state = %v, want failed", st)
	}
	before := calls.Load()
	s.Get("/a.mkv", 1, 2)
	if calls.Load() != before {
		t.Error("a failure inside the pause was retried")
	}
	fail.Store(false)
	s.mu.Lock()
	for _, e := range s.entries {
		e.failed = time.Now().Add(-time.Minute)
	}
	s.mu.Unlock()
	s.Get("/a.mkv", 1, 2)
	if st := wait(); st != SubtitleReady {
		t.Fatalf("after the pause: %v, want ready", st)
	}
	if cues, _ := s.Get("/a.mkv", 1, 2); len(cues) != 1 || cues[0].Text != "hi" {
		t.Errorf("cues = %+v", cues)
	}
	n := calls.Load()
	s.Get("/a.mkv", 1, 2)
	s.Get("/a.mkv", 2, 2) // a replaced file is a different key
	time.Sleep(50 * time.Millisecond)
	if calls.Load() != n+1 {
		t.Errorf("calls = %d, want one more (only the modified file)", calls.Load())
	}
}

func TestExtractSubtitlesRealFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	srt := filepath.Join(dir, "en.srt")
	if err := exec.Command("sh", "-c", `printf '1\n00:00:01,000 --> 00:00:02,500\nHello <i>world</i>\n\n2\n00:00:03,000 --> 00:00:04,000\nBye\n' > `+srt).Run(); err != nil {
		t.Fatal(err)
	}
	mkv := filepath.Join(dir, "with-subs.mkv")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc2=duration=5:size=160x120:rate=10", "-i", srt,
		"-map", "0:v", "-map", "1:0", "-c:v", "mpeg4", "-c:s", "srt", "-metadata:s:s:0", "language=eng", mkv).CombinedOutput()
	if err != nil {
		t.Skipf("cannot build a fixture with subtitles: %v %s", err, out)
	}
	src, err := library.Probe(ctx, mkv)
	if err != nil {
		t.Fatal(err)
	}
	tracks := SubtitleTracks(src)
	if len(tracks) != 1 || tracks[0].Language != "eng" {
		t.Fatalf("tracks = %+v, want one English text track", tracks)
	}
	cues, err := ExtractSubtitles(ctx, mkv, tracks[0].Index)
	if err != nil || len(cues) != 2 || cues[0].Text != "Hello world" || cues[0].Start != 1 || cues[1].End != 4 {
		t.Fatalf("cues = %+v, %v", cues, err)
	}
	if _, err := ExtractSubtitles(ctx, mkv, 99); err == nil {
		t.Error("a stream that does not exist must fail")
	}
}
