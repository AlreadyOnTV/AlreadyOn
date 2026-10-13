package lineup

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/eligibility"
)

func putOnChannel(t *testing.T, db *sql.DB, channelID, mediaFileID int64) {
	t.Helper()
	var pos int
	db.QueryRow(`SELECT COALESCE(MAX(position) + 1, 0) FROM channel_items WHERE channel_id = ?`, channelID).Scan(&pos)
	if _, err := db.Exec(`INSERT INTO channel_items (channel_id, media_file_id, position) VALUES (?, ?, ?)`, channelID, mediaFileID, pos); err != nil {
		t.Fatalf("insert channel_items: %v", err)
	}
}

func unmatchedByTitle(t *testing.T, db *sql.DB) map[string]UnmatchedItem {
	t.Helper()
	page, err := Unmatched(context.Background(), db, 500, 0)
	if err != nil {
		t.Fatalf("Unmatched: %v", err)
	}
	out := map[string]UnmatchedItem{}
	for _, it := range page.Items {
		out[it.Title] = it
	}
	return out
}

func TestUnmatched_ReasonsAndMatchedItemsAreExcluded(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	insertItem(t, db, item{mediaType: "movie", title: "No Genres Yet", year: 2005, rating: "PG"})                            // never classified
	insertItem(t, db, item{mediaType: "movie", title: "No TMDb Match", year: 2005, rating: "PG", tags: `[]`})                // classified, nothing found
	insertItem(t, db, item{mediaType: "movie", title: "Unused Genre", year: 2005, rating: "PG", tags: `["documentary"]`})    // no rule uses it
	insertItem(t, db, item{mediaType: "movie", title: "Wrong Type", year: 2005, rating: "TV-14", tags: `["reality"]`})       // reality channels are TV only
	insertItem(t, db, item{mediaType: "movie", title: "Too Mature", year: 2005, rating: "R", tags: `["kids"]`})              // kids movie channels take G/PG
	insertItem(t, db, item{mediaType: "movie", title: "Fits But Unplaced", year: 2005, rating: "PG-13", tags: `["comedy"]`}) // rules match, no lineup yet
	onChannel := insertItem(t, db, item{mediaType: "movie", title: "Already Airing", year: 2005, rating: "PG", tags: `["comedy"]`})
	insertItem(t, db, item{mediaType: "movie", title: "Not Probed", year: 2005, rating: "PG", seconds: -1})

	custom, _ := channel.Create(ctx, db, channel.Draft{Number: 200, Name: "Custom"})
	putOnChannel(t, db, custom.ID, onChannel)

	got := unmatchedByTitle(t, db)
	if _, ok := got["Already Airing"]; ok {
		t.Error("an item on a channel was listed as unmatched")
	}
	if _, ok := got["Not Probed"]; ok {
		t.Error("an unprobed file was listed; it can't be scheduled whatever its metadata")
	}
	want := map[string]string{
		"No Genres Yet":     "No configured automatic channel filters",
		"No TMDb Match":     "No configured automatic channel filters",
		"Unused Genre":      "No configured automatic channel filters",
		"Wrong Type":        "No configured automatic channel filters",
		"Too Mature":        "No configured automatic channel filters",
		"Fits But Unplaced": "No configured automatic channel filters",
	}
	if len(got) != len(want) {
		t.Errorf("got %d unmatched items %v, want %d", len(got), keys(got), len(want))
	}
	for title, fragment := range want {
		it, ok := got[title]
		if !ok {
			t.Errorf("%q missing from the unmatched list", title)
			continue
		}
		if !strings.Contains(it.Reason, fragment) {
			t.Errorf("%q reason = %q, want it to mention %q", title, it.Reason, fragment)
		}
	}
	if got["No Genres Yet"].Genres != nil {
		t.Error("an unclassified item should report nil genres, not an empty list")
	}
	if got["No TMDb Match"].Genres == nil || len(got["No TMDb Match"].Genres) != 0 {
		t.Error("an item with no genres found should report an empty (non-nil) list")
	}
}

func keys(m map[string]UnmatchedItem) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestUnmatched_ShowCountsAsMatchedWhenAnyEpisodeIsOnAChannel(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ep1 := insertItem(t, db, item{mediaType: "episode", title: "On Air Show", year: 2005, rating: "TV-14", tags: `["drama"]`, episode: 1})
	insertItem(t, db, item{mediaType: "episode", title: "On Air Show", year: 2005, rating: "TV-14", tags: `["drama"]`, episode: 2})
	insertItem(t, db, item{mediaType: "episode", title: "Orphan Show", year: 2005, rating: "TV-14", tags: `["drama"]`, episode: 1})

	custom, _ := channel.Create(ctx, db, channel.Draft{Number: 200, Name: "Custom"})
	putOnChannel(t, db, custom.ID, ep1)

	page, err := Unmatched(ctx, db, 50, 0)
	if err != nil {
		t.Fatalf("Unmatched: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].SeriesTitle != "Orphan Show" {
		t.Errorf("unmatched = %+v, want only the show with no episode on a channel", page)
	}
}

func TestUnmatched_DemoChannelsDoNotCount(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	id := insertItem(t, db, item{mediaType: "movie", title: "Only On Demo", year: 2005, rating: "PG", tags: `["comedy"]`})
	var demo int64
	db.QueryRow(`INSERT INTO channels (number, name) VALUES (198, 'Demo: Movie Mix 1') RETURNING id`).Scan(&demo)
	putOnChannel(t, db, demo, id)

	if page, _ := Unmatched(ctx, db, 50, 0); page.Total != 1 {
		t.Errorf("total = %d, want the demo-only item still listed", page.Total)
	}
}

func TestUnmatched_Paging(t *testing.T) {
	db := openTestDB(t)
	for _, title := range []string{"A", "B", "C", "D", "E"} {
		insertItem(t, db, item{mediaType: "movie", title: title, year: 2005, rating: "PG"})
	}
	page, err := Unmatched(context.Background(), db, 2, 2)
	if err != nil {
		t.Fatalf("Unmatched: %v", err)
	}
	if page.Total != 5 || len(page.Items) != 2 {
		t.Errorf("page = total %d, %d items; want total 5 and a 2-item page", page.Total, len(page.Items))
	}
	if page.Items[0].Path >= page.Items[1].Path {
		t.Errorf("items not ordered by path: %q, %q", page.Items[0].Path, page.Items[1].Path)
	}
}

func TestPool_ManualEpisodeCertificationOverridesTheShows(t *testing.T) {
	db := openTestDB(t)
	id := insertItem(t, db, item{mediaType: "episode", title: "Strict Show", year: 2005, rating: "TV-MA", tags: `["drama"]`, episode: 1})
	if _, err := db.Exec(`UPDATE identified_items SET content_rating = 'TV-G' WHERE media_file_id = ?`, id); err != nil {
		t.Fatal(err)
	}
	pool, err := loadPool(context.Background(), db, false)
	if err != nil || len(pool) != 1 || pool[0].Certification != "TV-G" {
		t.Fatalf("pool = %+v, %v; want the episode's own TV-G", pool, err)
	}

	custom, _ := channel.Create(context.Background(), db, channel.Draft{Number: 200, Name: "Custom"})
	putOnChannel(t, db, custom.ID, id)
	tier, err := channel.MinimumAudienceTier(context.Background(), db, custom.ID)
	if err != nil || tier != eligibility.TierYoungChild {
		t.Errorf("MinimumAudienceTier = %q, %v; want young_child from the episode's TV-G, not the show's TV-MA", tier, err)
	}
}

// A full-series pool may contain tens of thousands of channel assignments.
// Counting unmatched media must not hold the shared database connection for
// seconds per page, starving guide/profile requests during navigation.
func TestUnmatchedLargeSharedTVPool(t *testing.T) {
	db := openTestDB(t)
	for _, query := range []string{
		`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<20)
   INSERT INTO channels(id,number,name) SELECT x,200+x,'Channel '||x FROM n`,
		`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<4000)
   INSERT INTO media_files(id,root_kind,path,size_bytes,modified_unix,first_seen_unix,last_seen_unix,duration_seconds,status)
   SELECT x,'media','/media/'||x,1,1,1,1,1200,'ok' FROM n`,
		`INSERT INTO identified_items(media_file_id,media_type,title,series_title,season_number,episode_number)
   SELECT id,'episode','Episode '||id,'Show '||((id-1)/100),1,((id-1)%100)+1 FROM media_files`,
		`INSERT INTO channel_items(channel_id,media_file_id,position)
   SELECT c.id,m.id,m.id FROM channels c CROSS JOIN media_files m WHERE m.id<=3600`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	page, err := Unmatched(ctx, db, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 400 || len(page.Items) != 50 {
		t.Fatalf("unexpected page: total=%d items=%d", page.Total, len(page.Items))
	}
}
