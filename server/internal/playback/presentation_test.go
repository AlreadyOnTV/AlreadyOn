package playback

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

// Clients present a channel PresentationDelay behind the server clock. Entry
// selection and job positions must follow that presented moment, otherwise
// entries shorter than the delay are skipped and resumed programs replay or
// skip content. The client performs each step below at the server time shown.
func TestTuneEntryFollowsPresentedTime(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	boundary := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	if _, err = db.DB.Exec(`INSERT INTO channels(id,number,name) VALUES(999,999,'test'); INSERT INTO media_files(id,root_kind,path,size_bytes,modified_unix,first_seen_unix,last_seen_unix,status) VALUES(991,'media','program',0,0,0,0,'ok'),(992,'commercial','ad-a',0,0,0,0,'ok'),(993,'commercial','ad-b',0,0,0,0,'ok')`); err != nil {
		t.Fatal(err)
	}
	// Program, two ads shorter than the delay, then the program resumes.
	type slot struct {
		media         int64
		kind          string
		offset        float64
		start, finish time.Duration
	}
	slots := []slot{{991, "program", 0, -60 * time.Second, 0}, {992, "commercial", 0, 0, 2 * time.Second}, {993, "commercial", 0, 2 * time.Second, 4 * time.Second}, {991, "program", 60, 4 * time.Second, 64 * time.Second}}
	for _, s := range slots {
		if _, err = db.DB.Exec(`INSERT INTO schedule_entries(channel_id,media_file_id,kind,source_offset_seconds,duration_seconds,starts_at_unix,ends_at_unix) VALUES(999,?,?,?,?,?,?)`, s.media, s.kind, s.offset, (s.finish - s.start).Seconds(), boundary.Add(s.start).Unix(), boundary.Add(s.finish).Unix()); err != nil {
			t.Fatal(err)
		}
	}
	at := func(d time.Duration) schedule.Clock { return schedule.FixedClock{Time: boundary.Add(d)} }
	cases := []struct {
		name      string
		server    time.Duration // server clock when the request arrives
		requested time.Duration // entryStartsAt; -1 means an ordinary tune
		media     int64
		position  time.Duration // expected job clock relative to the boundary
	}{
		// Viewer is still watching the program's last seconds.
		{"fresh tune within delay of a boundary", 1 * time.Second, -1, 991, -3 * time.Second},
		// First ad attaches when presentation reaches it; its two-second
		// successor is already over on the server clock.
		{"prepare second short ad", PresentationDelay, 2 * time.Second, 993, 2 * time.Second},
		// Prewarming could not fit: the fallback tune follows the viewer.
		{"fallback resumes program at source offset", 4*time.Second + PresentationDelay, -1, 991, 4 * time.Second},
		{"late preparation seeks to presented time", 5*time.Second + PresentationDelay, 4 * time.Second, 991, 5 * time.Second},
		{"normal preparation starts at boundary", -PresentationDelay, 0, 992, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var requested time.Time
			if c.requested >= 0 {
				requested = boundary.Add(c.requested)
			}
			entry, jobClock, err := TuneEntry(ctx, db.DB, at(c.server), 999, requested)
			if err != nil {
				t.Fatal(err)
			}
			if entry.MediaFileID != c.media || !jobClock.Now().Equal(boundary.Add(c.position)) {
				t.Fatalf("got media %d at %v, want %d at %v", entry.MediaFileID, jobClock.Now().Sub(boundary), c.media, c.position)
			}
		})
	}
	// Workers still cannot be started long before their content airs.
	if _, _, err := TuneEntry(ctx, db.DB, at(-PrepareAhead-time.Second), 999, boundary); err == nil {
		t.Fatal("unbounded lookahead accepted")
	}
}

func TestReadBurstCoversContentBehindServerClock(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct{ position, want time.Duration }{
		{-PresentationDelay, PresentationDelay}, // fresh tune at the presented moment
		{-2 * time.Second, 2 * time.Second},     // successor of an entry shorter than the delay
		{4 * time.Second, 0},                    // prepared ahead of its boundary
		{-time.Minute, PresentationDelay},       // never an unbounded burst
	} {
		if got := ReadBurst(now, now.Add(c.position)); got != c.want {
			t.Fatalf("position %v: burst %v, want %v", c.position, got, c.want)
		}
	}
	j := Job{Path: "/media/a.mkv", OutputDir: "/cache/stream-1", Duration: time.Second, Burst: 3 * time.Second, Plan: Plan{Mode: "remux", VideoCopy: true, AudioCopy: true}}
	j.Source.Streams = []library.StreamInfo{{Index: 0, Kind: library.StreamVideo}}
	args, err := Arguments(j)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(args, " "); !strings.Contains(got, "-readrate 1 -readrate_initial_burst 3.000000 -ss") {
		t.Fatalf("burst not applied before input: %s", got)
	}
	j.Burst = 0
	if args, _ = Arguments(j); !strings.Contains(strings.Join(args, " "), "-readrate_initial_burst 0.500000") {
		t.Fatal("prepared-ahead jobs must keep FFmpeg's default half-second burst")
	}
	j.Burst = PresentationDelay + time.Second
	if _, err := Arguments(j); err == nil {
		t.Fatal("unbounded burst accepted")
	}
}

// A stream started at the presented moment must produce its first seconds
// promptly rather than at real-time pace, or converted viewers trail the delay.
func TestReadBurstRealFFmpeg(t *testing.T) {
	base := fixture(t) // four-second source
	for _, burst := range []time.Duration{0, PresentationDelay} {
		j := base
		j.OutputDir, j.Duration, j.Burst = t.TempDir(), 4*time.Second, burst
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		start := time.Now()
		done := make(chan error, 1)
		go func() { done <- (Runner{}).Run(ctx, j) }()
		// Segment 1 holds content from two seconds onward. Paced from zero it
		// cannot close before about 3.5s; with a four-second burst it is early.
		for {
			if _, err := os.Stat(filepath.Join(j.OutputDir, "segment-000000001.ts")); err == nil {
				break
			}
			if time.Since(start) > 10*time.Second {
				t.Fatal("no second segment")
			}
			time.Sleep(20 * time.Millisecond)
		}
		elapsed := time.Since(start)
		cancel()
		<-done
		if burst > 0 && elapsed > 2500*time.Millisecond {
			t.Fatalf("burst %v: second segment after %v", burst, elapsed)
		}
		if burst == 0 && elapsed < 2500*time.Millisecond {
			t.Fatalf("paced job produced second segment after only %v; burst test cannot distinguish", elapsed)
		}
	}
}
