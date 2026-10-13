package lineup

import (
	"context"
	"fmt"
	"testing"

	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/eligibility"
)

// Mode transitions use synthetic metadata only: no provider, live media or
// preview database participates in these regression fixtures.
func TestProviderChannels_ModesCoverageAndUnknownSafety(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := channel.SeedDefaults(ctx, db); err != nil {
		t.Fatal(err)
	}
	db.Exec(`UPDATE metadata_settings SET tmdb_api_key=NULL, keyless_enabled=0`)
	movie := insertClassified(t, db, "movie", "Comedy Movie", 0, 0, 2010, 60000, `["comedy"]`)
	// Each demo movie mix needs a distinct movie; an empty half is omitted.
	oldMovie := insertClassified(t, db, "movie", "Classic Movie", 0, 0, 2009, 1800, `["drama"]`)
	oldTV := insertClassified(t, db, "episode", "Classic", 1, 1, 0, 1800, `["comedy"]`)
	newTV := insertClassified(t, db, "episode", "Current", 1, 1, 0, 1800, `["drama"]`)
	unknownTV := insertUnclassified(t, db, "episode", "Unknown Year", 1, 1)
	db.Exec(`UPDATE identified_items SET keyless_release_date='2009-12-31' WHERE media_file_id=?`, oldTV)
	db.Exec(`UPDATE identified_items SET keyless_release_date='2010-01-01' WHERE media_file_id=?`, newTV)
	for i := 0; i < 100; i++ {
		// Poorly named files have no identification; they must still schedule.
		_, err := db.Exec(`INSERT INTO media_files(root_kind,path,size_bytes,modified_unix,first_seen_unix,last_seen_unix,status,duration_seconds) VALUES('media',?,1,0,0,0,'ok',1800)`, fmt.Sprintf("/media/unknown-%d.mkv", i))
		if err != nil {
			t.Fatal(err)
		}
	}
	db.Exec(`INSERT INTO media_files(root_kind,path,size_bytes,modified_unix,first_seen_unix,last_seen_unix,status,duration_seconds) VALUES('commercial','/ads/a.mkv',1,0,0,0,'ok',30),('media','/media/broken.mkv',1,0,0,0,'corrupt',1800),('media','/media/empty.mkv',1,0,0,0,'ok',0)`)
	if _, err := Refresh(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncDemoChannels(ctx, db); err != nil {
		t.Fatal(err)
	}
	var richItems int
	db.QueryRow(`SELECT COUNT(*) FROM channel_items ci JOIN channels c ON c.id=ci.channel_id WHERE c.number BETWEEN 101 AND 136`).Scan(&richItems)
	if richItems != 0 {
		t.Fatal("cached genres must not activate the full catalog in demo mode")
	}
	if len(demoChannelIDs(t, db)) != 4 {
		t.Fatal("demo mode must retain the four channels")
	}
	db.Exec(`UPDATE metadata_settings SET keyless_enabled=1`)
	if _, err := Refresh(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncDemoChannels(ctx, db); err != nil {
		t.Fatal(err)
	}
	var total, matched, mixID int64
	db.QueryRow(`SELECT COUNT(*) FROM media_files WHERE root_kind='media' AND status='ok' AND duration_seconds>0`).Scan(&total)
	db.QueryRow(`SELECT COUNT(DISTINCT ci.media_file_id) FROM channel_items ci JOIN channels c ON c.id=ci.channel_id WHERE c.number BETWEEN 180 AND 191`).Scan(&matched)
	if matched != total || total != 105 {
		t.Fatalf("usable coverage=%d/%d, want all 105 files including unknown identities and a >14-hour movie", matched, total)
	}
	db.QueryRow(`SELECT id FROM channels WHERE number=180`).Scan(&mixID)
	tier, err := channel.MinimumAudienceTier(ctx, db, mixID)
	if err != nil || tier != eligibility.TierAdult {
		t.Fatalf("unknown identity/rating safety=%s %v", tier, err)
	}
	for number, want := range map[int]int64{183: oldTV, 184: newTV, 185: oldMovie, 186: movie} {
		var id int64
		db.QueryRow(`SELECT id FROM channels WHERE number=?`, number).Scan(&id)
		got := channelItemMediaFileIDs(t, db, id)
		if len(got) != 1 || got[0] != want {
			t.Fatalf("era channel %d = %v, want [%d]", number, got, want)
		}
	}
	var unknownEra int
	db.QueryRow(`SELECT COUNT(*) FROM channel_items ci JOIN channels c ON c.id=ci.channel_id WHERE c.number BETWEEN 183 AND 186 AND ci.media_file_id=?`, unknownTV).Scan(&unknownEra)
	if unknownEra != 0 {
		t.Fatal("unknown date must not match either era")
	}
	var genreUnknown int
	db.QueryRow(`SELECT COUNT(*) FROM channel_items ci JOIN channels c ON c.id=ci.channel_id WHERE c.number BETWEEN 187 AND 191 AND ci.media_file_id=?`, unknownTV).Scan(&genreUnknown)
	if genreUnknown != 0 {
		t.Fatal("missing genres must not match themed channels")
	}
	if len(demoChannelIDs(t, db)) != 0 {
		t.Fatal("free mode replaces demo channels")
	}
	db.Exec(`UPDATE metadata_settings SET tmdb_api_key='fixture',keyless_enabled=0`)
	if _, err := Refresh(ctx, db); err != nil {
		t.Fatal(err)
	}
	var free int
	db.QueryRow(`SELECT COUNT(*) FROM channels WHERE number BETWEEN 180 AND 191 AND hidden=0`).Scan(&free)
	if free != 0 {
		t.Fatal("TMDb mode must retire free channels")
	}
	db.QueryRow(`SELECT COUNT(*) FROM channel_items ci JOIN channels c ON c.id=ci.channel_id WHERE c.number=133 AND ci.media_file_id=?`, movie).Scan(&richItems)
	if richItems != 1 {
		t.Fatal("TMDb mode must restore the full genre catalog")
	}
	db.Exec(`UPDATE metadata_settings SET tmdb_api_key=NULL,keyless_enabled=0`)
	if _, err := Refresh(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncDemoChannels(ctx, db); err != nil {
		t.Fatal(err)
	}
	if len(demoChannelIDs(t, db)) != 4 {
		t.Fatal("removing sources must return deterministically to demo mode")
	}
}

func TestFreeChannels_PreserveCustomChannelsAndEditedFilters(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	db.Exec(`UPDATE metadata_settings SET tmdb_api_key=NULL,keyless_enabled=1`)
	custom := insertChannel(t, db, 200, "Custom")
	if _, err := channel.SetFilter(ctx, db, custom, channel.Filter{AudienceTier: "adult", MediaType: "movie", Era: "both"}); err != nil {
		t.Fatal(err)
	}
	file := insertUnclassified(t, db, "movie", "Unknown", 0, 0)
	if _, err := Refresh(ctx, db); err != nil {
		t.Fatal(err)
	}
	if len(channelItemMediaFileIDs(t, db, custom)) != 1 {
		t.Fatal("custom channel must survive free mode")
	}
	var mix int64
	db.QueryRow(`SELECT id FROM channels WHERE number=180`).Scan(&mix)
	if _, err := channel.SetFilter(ctx, db, mix, channel.Filter{AudienceTier: "young_child", MediaType: "both", Era: "both"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Refresh(ctx, db); err != nil {
		t.Fatal(err)
	}
	if len(channelItemMediaFileIDs(t, db, mix)) != 0 {
		t.Fatal("editing the catch-all must not bypass parental filtering")
	}
	got := channelItemMediaFileIDs(t, db, custom)
	if len(got) != 1 || got[0] != file {
		t.Fatal("editing a managed channel must not alter custom lineups")
	}
}
