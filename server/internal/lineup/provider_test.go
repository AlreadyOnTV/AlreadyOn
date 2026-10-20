// Provider combinations share one catalog and leave custom channel rules intact.
package lineup

import (
	"context"
	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"testing"
)

func TestAllProvidersUseSameDefaultsAndPreserveCustomChannels(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := channel.SeedDefaults(ctx, db); err != nil {
		t.Fatal(err)
	}
	movie := insertClassified(t, db, "movie", "Unknown comedy", 0, 0, 2010, 7200, `["comedy"]`)
	custom := insertChannel(t, db, 200, "My movies")
	if _, err := channel.SetFilter(ctx, db, custom, channel.Filter{AudienceTier: "adult", MediaType: "movie"}); err != nil {
		t.Fatal(err)
	}
	legacy := insertChannel(t, db, 180, "Free: Library Mix")
	for _, key := range []string{"", "test-key", ""} {
		if _, err := db.Exec(`UPDATE metadata_settings SET tmdb_api_key=?,keyless_enabled=0`, key); err != nil {
			t.Fatal(err)
		}
		if _, err := Refresh(ctx, db); err != nil {
			t.Fatal(err)
		}
		var count, hidden int
		if err := db.QueryRow(`SELECT count(*) FROM channel_items ci JOIN channels c ON c.id=ci.channel_id WHERE c.number=133 AND ci.media_file_id=?`, movie).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatal("unrated movie missing from unrestricted defaults")
		}
		if got := channelItemMediaFileIDs(t, db, custom); len(got) != 1 || got[0] != movie {
			t.Fatal("custom lineup changed", got)
		}
		if err := db.QueryRow(`SELECT hidden FROM channels WHERE id=?`, legacy).Scan(&hidden); err != nil || hidden != 1 {
			t.Fatal("legacy mix visible", err)
		}
	}
}
