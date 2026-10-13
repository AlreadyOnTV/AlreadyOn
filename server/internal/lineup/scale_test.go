package lineup

import (
	"context"
	"fmt"
	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
	"sync"
	"testing"
	"time"
)

// A reproducible isolated large-library fixture; no development files are touched.
func TestLargeLibraryRebuildAndConcurrentReads(t *testing.T) {
	if testing.Short() {
		t.Skip("scale coverage")
	}
	db := openTestDB(t)
	ctx := context.Background()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	file, err := tx.Prepare(`INSERT INTO media_files(id,root_kind,path,size_bytes,modified_unix,first_seen_unix,last_seen_unix,duration_seconds,status) VALUES(?,'media',?,1,1,1,1,1800,'ok')`)
	if err != nil {
		t.Fatal(err)
	}
	item, err := tx.Prepare(`INSERT INTO identified_items(media_file_id,media_type,title,series_title,season_number,episode_number,release_year,genre_tags) VALUES(?,?,?,?,1,?,2005,'["drama"]')`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 10000; i++ {
		if _, err := file.Exec(i, fmt.Sprintf("/synthetic/%d.mkv", i)); err != nil {
			t.Fatal(err)
		}
		kind, title, show := "movie", fmt.Sprintf("Film %d", i), ""
		if i > 2000 {
			kind = "episode"
			show = fmt.Sprintf("Show %d", (i-2001)/100)
			title = show
		}
		if _, err := item.Exec(i, kind, title, show, (i-1)%100+1); err != nil {
			t.Fatal(err)
		}
	}
	file.Close()
	item.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 24; i++ {
		id := insertChannel(t, db, 200+i, fmt.Sprint("Channel ", i))
		if _, err := channel.SetFilter(ctx, db, id, channel.Filter{AudienceTier: "adult", MediaType: "both"}); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	if _, err := Refresh(ctx, db); err != nil {
		t.Fatal(err)
	}
	rebuild := time.Since(start)
	if rebuild > 30*time.Second {
		t.Fatalf("rebuild budget exceeded: %v", rebuild)
	}
	if _, err := schedule.RegenerateAll(ctx, db, time.Now()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := Refresh(ctx, db); err != nil {
			t.Error(err)
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := schedule.RegenerateAll(ctx, db, time.Now()); err != nil {
			t.Error(err)
		}
	}()

	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := schedule.Upcoming(ctx, db, schedule.FixedClock{Time: time.Now()}, 1, 10); err != nil {
				t.Error(err)
			}
			page, err := Unmatched(ctx, db, 50, 0)
			if err != nil || page.Total != 0 {
				t.Errorf("concurrent unmatched: %+v %v", page, err)
			}
		}()
	}
	wg.Wait()
	var pages, size int64
	if err := db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("PRAGMA page_size").Scan(&size); err != nil {
		t.Fatal(err)
	}
	t.Logf("10000 files / 24 overlapping channels: rebuild %v, database %.1f MiB", rebuild, float64(pages*size)/(1<<20))
	if pages*size > 128<<20 {
		t.Fatal("database exceeds 128 MiB fixture budget")
	}
}
