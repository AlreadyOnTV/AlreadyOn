package schedule

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/commercial"
	"github.com/alreadyonapp/alreadyon/server/internal/library"
)

// Exercise both manual loops and rotation checkpoints, especially pending ads
// retained after a program started under a different commercial policy.
func TestChannelCommercialOverrides(t *testing.T) {
	for _, rotation := range []bool{false, true} {
		t.Run(fmt.Sprintf("rotation=%v", rotation), func(t *testing.T) {
			db := openTestDB(t)
			ctx := context.Background()
			id := setupChannel(t, db, 1000, 600, 600)
			if rotation {
				_, err := db.Exec(`INSERT INTO identified_items(media_file_id,media_type,title,series_title,season_number,episode_number)
 SELECT media_file_id,'episode','Example','Example',1,position+1 FROM channel_items WHERE channel_id=?`, id)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := channel.SetFilter(ctx, db, id, channel.Filter{MediaType: "tv", AudienceTier: "adult"}); err != nil {
					t.Fatal(err)
				}
			}
			_, err := library.SaveFolderSettings(ctx, db, library.FolderSettings{Commercial: []string{"/ads/a", "/ads/b", "/ads/empty"}})
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"/ads/a/one.mp4", "/ads/b/two.mp4", "/ads/ab/not-a.mp4"} {
				if _, err := db.Exec(`INSERT INTO media_files(root_kind,path,size_bytes,modified_unix,first_seen_unix,last_seen_unix,duration_seconds,status)
 VALUES('commercial',?,1,1,1,1,15,'ok')`, path); err != nil {
					t.Fatal(err)
				}
			}
			global, _ := commercial.GetSettings(ctx, db)
			global.Enabled = true
			if err := commercial.SaveSettings(ctx, db, global); err != nil {
				t.Fatal(err)
			}
			now := testAnchor
			verify := func(wantPath string, wantAds bool) {
				t.Helper()
				if err := Regenerate(ctx, db, id, now); err != nil {
					t.Fatal(err)
				}
				rows, err := db.Query(`SELECT m.path FROM schedule_entries s JOIN media_files m ON m.id=s.media_file_id
 WHERE s.channel_id=? AND s.kind='commercial' AND s.starts_at_unix>?`, id, now.Unix())
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				n := 0
				for rows.Next() {
					var path string
					if err := rows.Scan(&path); err != nil {
						t.Fatal(err)
					}
					n++
					if wantPath != "" && path != wantPath {
						t.Fatalf("unexpected ad %s, want %s", path, wantPath)
					}
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				if (n > 0) != wantAds {
					t.Fatalf("ad count=%d wantAds=%v", n, wantAds)
				}
			}
			verify("", true) // Existing channels inherit global enablement and the pool.
			airing, _, err := Current(ctx, db, FixedClock{Time: now}, id)
			if err != nil {
				t.Fatal(err)
			}
			// Select a root without accidentally matching its prefix-neighbor /ads/ab.
			if err := commercial.SaveChannelSettings(ctx, db, id, commercial.ChannelSettings{Folders: []string{"/ads/a"}}); err != nil {
				t.Fatal(err)
			}
			verify("/ads/a/one.mp4", true)
			after, _, _ := Current(ctx, db, FixedClock{Time: now}, id)
			if airing != after {
				t.Fatal("changed currently airing entry")
			}
			off := false
			if err := commercial.SaveChannelSettings(ctx, db, id, commercial.ChannelSettings{Enabled: &off}); err != nil {
				t.Fatal(err)
			}
			verify("", false)
			// An explicit on overrides globally disabled commercials.
			global.Enabled = false
			if err := commercial.SaveSettings(ctx, db, global); err != nil {
				t.Fatal(err)
			}
			on := true
			if err := commercial.SaveChannelSettings(ctx, db, id, commercial.ChannelSettings{Enabled: &on, Folders: []string{"/ads/b"}}); err != nil {
				t.Fatal(err)
			}
			now = now.Add(time.Second)
			verify("/ads/b/two.mp4", true)
			// Removing a selected root falls back to the remaining configured folders.
			if _, err := library.SaveFolderSettings(ctx, db, library.FolderSettings{Commercial: []string{"/ads/a"}}); err != nil {
				t.Fatal(err)
			}
			p, err := commercial.GetChannelPolicy(ctx, db, id)
			if err != nil || !p.FoldersFallback {
				t.Fatalf("fallback %+v %v", p, err)
			}
			verify("/ads/a/one.mp4", true)
			if _, err := library.SaveFolderSettings(ctx, db, library.FolderSettings{Commercial: []string{"/ads/empty"}}); err != nil {
				t.Fatal(err)
			}
			if err := commercial.SaveChannelSettings(ctx, db, id, commercial.ChannelSettings{Enabled: &on, Folders: []string{"/ads/empty"}}); err != nil {
				t.Fatal(err)
			}
			verify("", false)
			if err := commercial.SaveChannelSettings(ctx, db, id, commercial.ChannelSettings{}); err != nil {
				t.Fatal(err)
			}
			verify("", false) // Reset restores global disabled state.
		})
	}
}
