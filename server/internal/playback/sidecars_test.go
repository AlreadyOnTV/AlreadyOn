//go:build linux || darwin

package playback

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/library"
)

const sidecarFixture = "1\r\n00:00:01,000 --> 00:00:04,000\r\n<i>First</i>\r\n\r\n2\r\n00:00:02,000 --> 00:00:03,000\r\nOverlaps\r\n"

func writeSidecar(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSidecarCatalogVariantPrecedence(t *testing.T) {
	dir := t.TempDir()
	outside := writeSidecar(t, t.TempDir(), "outside.srt", sidecarFixture)
	for _, name := range []string{"Film.en.srt", "Film.eng.forced.srt", "Film.en.forced.default.SRT", "Film.en.sdh.srt", "Film.es-MX.srt", "Film.srt", "FilmOther.de.srt", "Film.en.other.srt"} {
		writeSidecar(t, dir, name, sidecarFixture)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "Film.fr.srt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "Film.it.srt"), 0700); err != nil {
		t.Fatal(err)
	}
	oversized, err := os.Create(filepath.Join(dir, "Film.de.srt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := oversized.Truncate(maxSubtitleBytes + 1); err != nil {
		t.Fatal(err)
	}
	oversized.Close()
	source := library.ProbeResult{Streams: []library.StreamInfo{
		{Index: 2, Kind: library.StreamSubtitle, Codec: "subrip", Language: "eng"},
		{Index: 3, Kind: library.StreamSubtitle, Codec: "hdmv_pgs_subtitle", Language: "fra"},
	}}
	video := filepath.Join(dir, "Film.mp4")
	tracks := appendSidecars(SubtitleTracks(source), dir, video)
	if len(tracks) != 5 || tracks[0].Index != 2 {
		t.Fatalf("unexpected tracks: %+v", tracks)
	}
	for _, name := range []string{"Film.en.forced.default.SRT", "Film.en.sdh.srt", "Film.es-MX.srt", "Film.srt"} {
		expected, _ := sidecarTrack("Film", name)
		if !slices.ContainsFunc(tracks, func(track SubtitleTrack) bool {
			return track.Index == expected.Index && track.Language == expected.Language && track.Forced == expected.Forced && track.SDH == expected.SDH && track.Default == expected.Default
		}) {
			t.Errorf("missing metadata for %q in %+v", name, tracks)
		}
	}
	if tracks := appendSidecars(nil, t.TempDir(), video); len(tracks) != 0 {
		t.Fatal("discovered tracks outside the configured root")
	}
}

func TestSidecarParsingAndReplacement(t *testing.T) {
	dir := t.TempDir()
	path := writeSidecar(t, dir, "Film.en.srt", "\ufeff"+sidecarFixture)
	video := filepath.Join(dir, "Film.mp4")
	track := appendSidecars(nil, dir, video)[0]
	cues, err := track.sidecar.cues()
	if err != nil || len(cues) != 2 || cues[0] != (Cue{Start: 1, End: 4, Text: "First"}) {
		t.Fatalf("sidecar cues: %+v, %v", cues, err)
	}
	for _, invalid := range []string{
		"invalid", "\xff" + sidecarFixture, strings.Replace(sidecarFixture, "00:00:04", "00:00:00", 1),
		strings.Replace(sidecarFixture, "00:00:04", "00:99:04", 1),
		strings.Replace(sidecarFixture, "<i>First</i>", strings.Repeat("a", maxCueBytes+1), 1),
	} {
		if _, err := parseSidecarSRT([]byte(invalid)); err == nil {
			t.Error("accepted malformed sidecar")
		}
	}
	cache := NewSubtitles()
	waitCues := func(track SubtitleTrack) []Cue {
		t.Helper()
		for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
			cues, state := cache.GetSidecar(track)
			switch state {
			case SubtitleReady:
				return cues
			case SubtitleFailed:
				t.Fatal("sidecar extraction failed")
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("sidecar extraction did not finish")
		return nil
	}
	if got := waitCues(track); got[0].Text != "First" {
		t.Fatalf("initial cached cues: %+v", got)
	}
	replacement := writeSidecar(t, dir, "new.srt", strings.Replace(sidecarFixture, "First", "Other", 1))
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	if _, err := track.sidecar.cues(); err == nil {
		t.Error("opened a replaced file using the old catalog entry")
	}
	replaced := appendSidecars(nil, dir, video)[0]
	if got := waitCues(replaced); replaced.Index != track.Index || got[0].Text != "Other" {
		t.Fatalf("replaced track ID or cues incorrect: %+v", got)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	target := writeSidecar(t, t.TempDir(), "escape.srt", sidecarFixture)
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := replaced.sidecar.cues(); err == nil || strings.Contains(err.Error(), target) {
		t.Fatalf("symlink replacement error: %v", err)
	}
}
