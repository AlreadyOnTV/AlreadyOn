package playback

import (
	"context"
	"math"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/loudness"
)

func TestSelectAudioLanguageAndExplicitTrack(t *testing.T) {
	source := sourceInfo()
	source.Streams[1].Language = "jpn"
	source.Streams[1].Default = true
	english := source.Streams[1]
	english.Index, english.Language, english.Default = 2, "eng", false
	source.Streams = append(source.Streams, english)
	for _, tc := range []struct {
		language string
		explicit *int
		want     int
	}{{"", nil, 2}, {"en-US", nil, 2}, {"ja", nil, 1}, {"fr", nil, 1}, {"en", new(int), -1}} {
		selected, err := SelectAudio(source, tc.language, tc.explicit)
		if tc.want < 0 {
			if err == nil {
				t.Fatal("accepted a video index as audio")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		_, a := Tracks(selected)
		if a.Index != tc.want {
			t.Fatalf("%q chose %d, want %d", tc.language, a.Index, tc.want)
		}
		if source.Streams[1].Index != 1 {
			t.Fatal("mutated shared source")
		}
		caps := clientCaps()
		caps.SelectAudio = true
		p, err := Choose(selected, caps, true, false)
		if err != nil || p.Mode == "direct" || p.AudioTrackIndex != tc.want {
			t.Fatalf("wrong selected plan: %+v %v", p, err)
		}
		args, err := Arguments(Job{Source: selected, Plan: p, Path: filepath.Join(t.TempDir(), "source.mp4"), Duration: 3 * time.Second, OutputDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.Join(args, " "), "-map 0:"+strconv.Itoa(tc.want)) {
			t.Fatal("wrong audio mapping", args)
		}
	}
	index := 1
	selected, err := SelectAudio(source, "en", &index)
	_, a := Tracks(selected)
	if err != nil || a.Index != 1 || len(AudioTracks(selected)) != 2 {
		t.Fatal("explicit selection or menu lost", err)
	}
}

func TestCommercialVolumePlan(t *testing.T) {
	caps := clientCaps()
	caps.VolumeOffsetDB = -6
	p, err := Choose(sourceInfo(), caps, true, false)
	if err != nil || p.Mode != "transcode" || !p.VideoCopy || p.AudioCopy || p.VolumeOffsetDB != -6 {
		t.Fatalf("offset not applied with video copy: %+v %v", p, err)
	}
	caps.VolumeOffsetDB = 0
	p, err = Choose(sourceInfo(), caps, true, false)
	if err != nil || p.Mode != "direct" {
		t.Fatalf("zero offset changed direct playback: %+v %v", p, err)
	}
}

func TestCommercialVolumeRealFFmpeg(t *testing.T) {
	job := fixture(t)
	means := []float64{}
	for _, gain := range []float64{0, -6, 6} {
		job.OutputDir = t.TempDir()
		job.Plan.VolumeOffsetDB = gain
		if err := (Runner{}).Run(context.Background(), job); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command("ffmpeg", "-hide_banner", "-i", filepath.Join(job.OutputDir, "index.m3u8"), "-vn", "-af", "volumedetect", "-f", "null", "-").CombinedOutput()
		if err != nil {
			t.Fatalf("measure output: %v %s", err, out)
		}
		match := regexp.MustCompile(`mean_volume: (-?[0-9.]+) dB`).FindStringSubmatch(string(out))
		if len(match) != 2 {
			t.Fatal(string(out))
		}
		mean, _ := strconv.ParseFloat(match[1], 64)
		means = append(means, mean)
	}
	if math.Abs((means[1]-means[0])+6) > 0.5 || math.Abs((means[2]-means[0])-6) > 0.5 {
		t.Fatalf("gain not preserved: %v", means)
	}
	job.Plan.VolumeOffsetDB = -6
	job.Plan.Normalization = &loudness.Measurement{Integrated: -25, TruePeak: -10, Range: 5, Threshold: -35, GainDB: 9}
	args, err := Arguments(job)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(args, " ")
	if !strings.Contains(text, "linear=true,volume=-6.00dB") {
		t.Fatal("offset must follow normalization", text)
	}
}
