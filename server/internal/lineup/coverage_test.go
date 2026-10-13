package lineup

import (
	"context"
	"fmt"
	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"strings"
	"testing"
)

func TestMoviePoolIncludesAllCandidatesBeyondGuideWindow(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	id := insertChannel(t, db, 200, "Movies")
	for i := 0; i < 90; i++ {
		insertItem(t, db, item{mediaType: "movie", title: fmt.Sprintf("Film %d", i), seconds: 7200})
	}
	if _, err := channel.SetFilter(ctx, db, id, channel.Filter{AudienceTier: "adult", MediaType: "movie"}); err != nil {
		t.Fatal(err)
	}
	result, err := BuildFilteredChannel(ctx, db, id)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(channelItemMediaFileIDs(t, db, id)); got != 90 || result.Items != 90 {
		t.Fatalf("pool %d, preview %d; want 90", got, result.Items)
	}
	page, err := Unmatched(ctx, db, 100, 0)
	if err != nil || page.Total != 0 {
		t.Fatalf("candidates outside guide reported unmatched: %+v %v", page, err)
	}
}

func TestEpisodesUseShowPremiereForEra(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	id := insertItem(t, db, item{mediaType: "episode", title: "Episode", year: 2025, seconds: 1200})
	if _, err := db.Exec(`UPDATE tv_shows SET first_air_date='2009-12-31'`); err != nil {
		t.Fatal(err)
	}
	f := channel.Filter{AudienceTier: "adult", MediaType: "tv", Era: "modern"}
	result, err := PreviewFilter(ctx, db, f)
	if err != nil || result.Items != 0 {
		t.Fatalf("episode release year used: %+v %v", result, err)
	}
	f.Era = "nostalgia"
	result, err = PreviewFilter(ctx, db, f)
	if err != nil || result.Items != 1 {
		t.Fatalf("show premiere ignored: %+v %v", result, err)
	}
	page, err := Unmatched(ctx, db, 10, 0)
	if err != nil || len(page.Items) != 1 || page.Items[0].Year != 2009 || page.Items[0].MediaFileID != id {
		t.Fatalf("diagnostics year %+v %v", page, err)
	}
	if _, err := db.Exec(`UPDATE tv_shows SET first_air_date='2010-01-01'`); err != nil {
		t.Fatal(err)
	}
	result, err = PreviewFilter(ctx, db, f)
	if err != nil || result.Items != 0 {
		t.Fatalf("boundary ignored: %+v %v", result, err)
	}
}

func TestUnmatchedUsesSavedCustomFilters(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	insertItem(t, db, item{mediaType: "movie", title: "Odd Film", year: 1972, seconds: 7200, tags: `["western"]`})
	id := insertChannel(t, db, 201, "My westerns")
	f := channel.Filter{AudienceTier: "adult", MediaType: "movie", Genres: []string{"western"}, Era: "modern", TitleContains: "Something else"}
	if _, err := channel.SetFilter(ctx, db, id, f); err != nil {
		t.Fatal(err)
	}
	page, err := Unmatched(ctx, db, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	reason := page.Items[0].Reason
	for _, s := range []string{"201 My westerns", "1972", "modern", "Something else"} {
		if !strings.Contains(reason, s) {
			t.Fatalf("missing %q in %s", s, reason)
		}
	}
	f.Era = "both"
	f.TitleContains = ""
	if _, err := channel.SetFilter(ctx, db, id, f); err != nil {
		t.Fatal(err)
	}
	page, err = Unmatched(ctx, db, 10, 0)
	if err != nil || !strings.Contains(page.Items[0].Reason, "Eligible but not assigned") {
		t.Fatalf("%+v %v", page, err)
	}
	if _, err := BuildFilteredChannel(ctx, db, id); err != nil {
		t.Fatal(err)
	}
	page, err = Unmatched(ctx, db, 10, 0)
	if err != nil || page.Total != 0 {
		t.Fatalf("still unmatched: %+v %v", page, err)
	}
}
