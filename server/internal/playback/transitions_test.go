package playback

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

func TestProgramCommercialProgramRealFFmpeg(t *testing.T) {
	program := fixture(t) // 320x240/30fps MPEG-4 and MP2 audio
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adPath := filepath.Join(t.TempDir(), "silent-ad.mkv")
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=blue:size=640x360:rate=24:duration=3", "-c:v", "ffv1", adPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ad: %v %s", err, out)
	}
	ad, err := library.Probe(ctx, adPath)
	if err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	if _, err = db.DB.Exec(`INSERT INTO channels(id,number,name) VALUES(999,999,'test'); INSERT INTO media_files(id,root_kind,path,size_bytes,modified_unix,first_seen_unix,last_seen_unix,status) VALUES(991,'media','program',0,0,0,0,'ok'),(992,'commercial','ad',0,0,0,0,'ok')`); err != nil {
		t.Fatal(err)
	}
	for i, id := range []int{991, 992, 991} {
		kind := "program"
		offset := 0.0
		if i == 1 {
			kind = "commercial"
		}
		if i == 2 {
			offset = 1
		}
		if _, err = db.DB.Exec(`INSERT INTO schedule_entries(channel_id,media_file_id,kind,source_offset_seconds,duration_seconds,starts_at_unix,ends_at_unix) VALUES(999,?,?,?,1,?,?)`, id, kind, offset, now.Add(time.Duration(i)*time.Second).Unix(), now.Add(time.Duration(i+1)*time.Second).Unix()); err != nil {
			t.Fatal(err)
		}
	}
	clock := schedule.FixedClock{Time: now.Add(-9 * time.Second)}
	if _, _, err := TuneEntry(ctx, db.DB, clock, 999, now); err == nil {
		t.Fatal("unbounded lookahead accepted")
	}
	clock.Time = now.Add(-4 * time.Second)
	entry, jobClock, err := TuneEntry(ctx, db.DB, clock, 999, now)
	if err != nil || !jobClock.Now().Equal(now) || entry.SourceOffsetSeconds != 0 {
		t.Fatalf("preparation: %+v %v", entry, err)
	}
	for i := 0; i < 3; i++ {
		// Viewers reach each entry one presentation delay after the server does.
		clock.Time = now.Add(time.Duration(i)*time.Second + 300*time.Millisecond + PresentationDelay)
		entry, jobClock, err := TuneEntry(ctx, db.DB, clock, 999, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		source, path := program.Source, program.Path
		if i == 1 {
			source, path = ad, adPath
			if entry.Kind != schedule.Kind("commercial") {
				t.Fatal("wrong entry")
			}
		}
		plan, err := ChooseScheduled(source, clientCaps(), false, 0.3)
		if err != nil {
			t.Fatal(err)
		}
		job, err := ScheduledJob(entry, jobClock, path, source, plan, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		want := 300 * time.Millisecond
		if i == 2 {
			want += time.Second
		}
		if job.Offset != want {
			t.Fatalf("source resume offset %v want %v", job.Offset, want)
		}
		if err := (Runner{}).Run(ctx, job); err != nil {
			t.Fatal(err)
		}
		if _, err := HLSOrigin(job.OutputDir); err != nil {
			t.Fatal(err)
		}
		output, err := library.Probe(ctx, filepath.Join(job.OutputDir, "segment-000000000.ts"))
		if err != nil {
			t.Fatal(err)
		}
		video, audio := Tracks(output)
		if video.Codec != "h264" {
			t.Fatalf("video: %+v", video)
		}
		if i == 1 {
			if video.Width != 640 || video.FrameRate != 24 || audio.Index >= 0 {
				t.Fatalf("silent ad: %+v", output)
			}
		} else if video.Width != 320 || video.FrameRate != 30 || audio.Codec != "aac" {
			t.Fatalf("program: %+v", output)
		}
		cmd = exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", filepath.Join(job.OutputDir, "index.m3u8"), "-f", "null", "-")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("decode: %v %s", err, out)
		}
	}
}
