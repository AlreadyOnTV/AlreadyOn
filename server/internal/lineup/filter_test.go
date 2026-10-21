package lineup

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/metadata"
)

var filterTestCounter int64

// item describes a library file to insert for a filter test.
type item struct {
	mediaType string // "movie" or "episode"
	title     string // movie title, or show name for an episode
	year      int
	rating    string // movie's own certification, or the show's for episodes; "" = unknown
	tags      string // genre_tags JSON; "" = not classified yet (NULL)
	seconds   float64
	episode   int
}

func insertItem(t *testing.T, db *sql.DB, it item) int64 {
	t.Helper()
	ctx := context.Background()
	n := atomic.AddInt64(&filterTestCounter, 1)
	now := time.Now().Unix()
	if it.seconds == 0 {
		it.seconds = 600
	}
	var fileID int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO media_files (root_kind, path, size_bytes, modified_unix, first_seen_unix, last_seen_unix, duration_seconds, status)
		VALUES ('media', ?, 1, ?, ?, ?, ?, 'ok') RETURNING id`,
		fmt.Sprintf("/media/%d.mkv", n), now, now, now, it.seconds).Scan(&fileID); err != nil {
		t.Fatalf("insert media_files: %v", err)
	}
	var tags any
	if it.tags != "" {
		tags = it.tags
	}
	year := sql.NullInt64{Int64: int64(it.year), Valid: it.year != 0}
	if it.mediaType == "movie" {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO identified_items (media_file_id, media_type, title, release_year, genre_tags, content_rating)
			VALUES (?, 'movie', ?, ?, ?, NULLIF(?, ''))`, fileID, it.title, year, tags, it.rating); err != nil {
			t.Fatalf("insert movie: %v", err)
		}
		return fileID
	}
	var showID int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO tv_shows (tmdb_id, title, content_rating) VALUES (?, ?, NULLIF(?, ''))
		ON CONFLICT (tmdb_id) DO UPDATE SET title = excluded.title RETURNING id`,
		int64(len(it.title))*1000+int64(it.year), it.title, it.rating).Scan(&showID); err != nil {
		t.Fatalf("insert tv_shows: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO identified_items (media_file_id, media_type, title, series_title, season_number, episode_number, release_year, genre_tags, show_id)
		VALUES (?, 'episode', ?, ?, 1, ?, ?, ?, ?)`,
		fileID, fmt.Sprintf("Episode %d", it.episode), it.title, it.episode, year, tags, showID); err != nil {
		t.Fatalf("insert episode: %v", err)
	}
	return fileID
}

func mustFilter(t *testing.T, f channel.Filter) channel.Filter {
	t.Helper()
	f, err := f.Normalize()
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	return f
}

func classified(mediaType, title string, year int, rating string, tags ...metadata.Tag) classifiedItem {
	return classifiedItem{MediaType: mediaType, Title: title, ReleaseYear: year, Certification: rating, Tags: tags}
}

func TestFilterMatches(t *testing.T) {
	cases := []struct {
		name string
		f    channel.Filter
		it   classifiedItem
		want bool
	}{
		// Audience tier is a hard boundary.
		{"kids sees G movie", channel.Filter{AudienceTier: "young_child"}, classified("movie", "A", 2000, "G"), true},
		{"kids sees PG movie", channel.Filter{AudienceTier: "young_child"}, classified("movie", "A", 2000, "PG"), true},
		{"teen sees PG-13", channel.Filter{AudienceTier: "teen"}, classified("movie", "A", 2000, "PG-13"), true},
		{"teen blocked from R", channel.Filter{AudienceTier: "teen"}, classified("movie", "A", 2000, "R"), false},
		{"teen sees TV-14 episode", channel.Filter{AudienceTier: "teen"}, classified("episode", "A", 2000, "TV-14"), true},
		{"teen blocked from TV-MA", channel.Filter{AudienceTier: "teen"}, classified("episode", "A", 2000, "TV-MA"), false},
		{"adult sees R", channel.Filter{AudienceTier: "adult"}, classified("movie", "A", 2000, "R"), true},
		{"unknown rating is adult-only", channel.Filter{AudienceTier: "teen"}, classified("movie", "A", 2000, ""), false},
		{"unknown rating passes for adult", channel.Filter{AudienceTier: "adult"}, classified("movie", "A", 2000, ""), true},

		// Media type.
		{"tv only rejects movie", channel.Filter{AudienceTier: "adult", MediaType: "tv"}, classified("movie", "A", 2000, "R"), false},
		{"tv only accepts episode", channel.Filter{AudienceTier: "adult", MediaType: "tv"}, classified("episode", "A", 2000, "TV-MA"), true},
		{"movie only rejects episode", channel.Filter{AudienceTier: "adult", MediaType: "movie"}, classified("episode", "A", 2000, "TV-MA"), false},
		{"both accepts either", channel.Filter{AudienceTier: "adult", MediaType: "both"}, classified("episode", "A", 2000, "TV-MA"), true},

		// Era: nostalgia is up to 2009, modern from 2010; unknown year passes.
		{"modern accepts 2010", channel.Filter{AudienceTier: "adult", Era: "modern"}, classified("movie", "A", 2010, "R"), true},
		{"modern rejects 2009", channel.Filter{AudienceTier: "adult", Era: "modern"}, classified("movie", "A", 2009, "R"), false},
		{"nostalgia accepts 2009", channel.Filter{AudienceTier: "adult", Era: "nostalgia"}, classified("movie", "A", 2009, "R"), true},
		{"nostalgia rejects 2010", channel.Filter{AudienceTier: "adult", Era: "nostalgia"}, classified("movie", "A", 2010, "R"), false},
		{"unknown year passes era", channel.Filter{AudienceTier: "adult", Era: "modern"}, classified("movie", "A", 0, "R"), true},
		{"both ignores year", channel.Filter{AudienceTier: "adult", Era: "both"}, classified("movie", "A", 1975, "R"), true},

		// Genres: any-of, empty means any (including unclassified).
		{"genre any-of hit", channel.Filter{AudienceTier: "adult", Genres: []string{"comedy", "drama"}}, classified("movie", "A", 2000, "R", metadata.TagDrama), true},
		{"genre miss", channel.Filter{AudienceTier: "adult", Genres: []string{"comedy"}}, classified("movie", "A", 2000, "R", metadata.TagDrama), false},
		{"genre filter rejects untagged", channel.Filter{AudienceTier: "adult", Genres: []string{"comedy"}}, classified("movie", "A", 2000, "R"), false},
		{"no genre filter accepts untagged", channel.Filter{AudienceTier: "adult"}, classified("movie", "A", 2000, "R"), true},

		// Title: case-insensitive substring.
		{"title exact case", channel.Filter{AudienceTier: "adult", TitleContains: "Harry Potter"}, classified("movie", "Harry Potter and the Goblet of Fire", 2005, "PG-13"), true},
		{"title other case", channel.Filter{AudienceTier: "adult", TitleContains: "hArRy pOtTeR"}, classified("movie", "Harry Potter and the Goblet of Fire", 2005, "PG-13"), true},
		{"title partial word", channel.Filter{AudienceTier: "adult", TitleContains: "otte"}, classified("movie", "Harry Potter", 2005, "PG-13"), true},
		{"title miss", channel.Filter{AudienceTier: "adult", TitleContains: "Potter"}, classified("movie", "Star Wars", 1977, "PG"), false},
		{"title non-ASCII", channel.Filter{AudienceTier: "adult", TitleContains: "am\u00e9lie"}, classified("movie", "AM\u00c9LIE", 2001, "R"), true},
		{"title matches show name", channel.Filter{AudienceTier: "adult", TitleContains: "office"}, classified("episode", "The Office", 2005, "TV-14"), true},
	}
	for _, c := range cases {
		f, err := c.f.Normalize()
		if err != nil {
			t.Fatalf("%s: Normalize: %v", c.name, err)
		}
		if got := filterMatches(f, c.it); got != c.want {
			t.Errorf("%s: filterMatches = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestBuildFilteredChannel_TitleFilterAirsOnlyMatchingMovies(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	hp1 := insertItem(t, db, item{mediaType: "movie", title: "Harry Potter and the Sorcerer's Stone", year: 2001, rating: "PG", tags: `["fantasy"]`})
	hp2 := insertItem(t, db, item{mediaType: "movie", title: "HARRY POTTER and the Chamber of Secrets", year: 2002, rating: "PG", tags: `["fantasy"]`})
	insertItem(t, db, item{mediaType: "movie", title: "Star Wars", year: 1977, rating: "PG", tags: `["scifi"]`})
	insertItem(t, db, item{mediaType: "episode", title: "Harry Potter Reunion", year: 2022, rating: "TV-PG", tags: `["reality"]`, episode: 1})

	ch, err := channel.Create(ctx, db, channel.Draft{Number: 200, Name: "Wizards"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := channel.SetFilter(ctx, db, ch.ID, channel.Filter{AudienceTier: "teen", MediaType: "movie", TitleContains: "harry potter"}); err != nil {
		t.Fatalf("SetFilter: %v", err)
	}
	res, err := BuildFilteredChannel(ctx, db, ch.ID)
	if err != nil {
		t.Fatalf("BuildFilteredChannel: %v", err)
	}
	if res.Titles != 2 || res.Items != 2 {
		t.Errorf("result = %+v, want 2 titles / 2 items", res)
	}
	got := channelItemMediaFileIDs(t, db, ch.ID)
	if len(got) != 2 || !((got[0] == hp1 && got[1] == hp2) || (got[0] == hp2 && got[1] == hp1)) {
		t.Errorf("channel_items = %v, want the two Harry Potter movies (%d, %d)", got, hp1, hp2)
	}
}

func TestBuildFilteredChannel_ReplacesLineupWhenFilterChanges(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	comedy := insertItem(t, db, item{mediaType: "movie", title: "Funny", year: 2001, rating: "PG", tags: `["comedy"]`})
	drama := insertItem(t, db, item{mediaType: "movie", title: "Sad", year: 2001, rating: "PG", tags: `["drama"]`})
	ch, _ := channel.Create(ctx, db, channel.Draft{Number: 200, Name: "Mixed"})

	channel.SetFilter(ctx, db, ch.ID, channel.Filter{AudienceTier: "adult", Genres: []string{"comedy"}})
	BuildFilteredChannel(ctx, db, ch.ID)
	if got := channelItemMediaFileIDs(t, db, ch.ID); len(got) != 1 || got[0] != comedy {
		t.Fatalf("first build = %v, want just the comedy", got)
	}
	channel.SetFilter(ctx, db, ch.ID, channel.Filter{AudienceTier: "adult", Genres: []string{"drama"}})
	BuildFilteredChannel(ctx, db, ch.ID)
	if got := channelItemMediaFileIDs(t, db, ch.ID); len(got) != 1 || got[0] != drama {
		t.Errorf("after changing the filter = %v, want just the drama (old lineup replaced)", got)
	}
}

func TestBuildFilteredChannel_NoMatchesLeavesAnEmptyChannel(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	insertItem(t, db, item{mediaType: "movie", title: "Funny", year: 2001, rating: "PG", tags: `["comedy"]`})
	ch, _ := channel.Create(ctx, db, channel.Draft{Number: 200, Name: "None"})
	channel.SetFilter(ctx, db, ch.ID, channel.Filter{AudienceTier: "adult", TitleContains: "zzz"})

	res, err := BuildFilteredChannel(ctx, db, ch.ID)
	if err != nil || res.Items != 0 {
		t.Fatalf("result = %+v, err = %v, want no matches", res, err)
	}
	if got := channelItemMediaFileIDs(t, db, ch.ID); len(got) != 0 {
		t.Errorf("channel_items = %v, want none", got)
	}
	c, _ := channel.Get(ctx, db, ch.ID)
	if !c.Hidden {
		t.Error("an empty filtered channel is hidden by default (Auto)")
	}
	if err := channel.SetVisibility(ctx, db, []int64{ch.ID}, channel.VisibilityShow); err != nil {
		t.Fatalf("SetVisibility: %v", err)
	}
	if _, err := BuildFilteredChannel(ctx, db, ch.ID); err != nil {
		t.Fatalf("BuildFilteredChannel: %v", err)
	}
	c, _ = channel.Get(ctx, db, ch.ID)
	if c.Hidden {
		t.Error("an empty filtered channel set to Show should stay visible")
	}
}

func TestBuildFilteredChannel_IncludesUnclassifiedWhenNoGenreFilter(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	id := insertItem(t, db, item{mediaType: "movie", title: "Not Yet Classified", year: 2001, rating: "PG"})
	ch, _ := channel.Create(ctx, db, channel.Draft{Number: 200, Name: "All"})
	channel.SetFilter(ctx, db, ch.ID, channel.Filter{AudienceTier: "adult"})
	BuildFilteredChannel(ctx, db, ch.ID)
	if got := channelItemMediaFileIDs(t, db, ch.ID); len(got) != 1 || got[0] != id {
		t.Errorf("channel_items = %v, want the unclassified movie", got)
	}
}

func TestBuildFilteredChannel_WithoutAFilter(t *testing.T) {
	db := openTestDB(t)
	ch, _ := channel.Create(context.Background(), db, channel.Draft{Number: 200, Name: "Manual"})
	if _, err := BuildFilteredChannel(context.Background(), db, ch.ID); err != ErrNoFilter {
		t.Errorf("err = %v, want ErrNoFilter", err)
	}
}

func TestPreviewFilter_CountsTitlesAndItems(t *testing.T) {
	db := openTestDB(t)
	for e := 1; e <= 3; e++ {
		insertItem(t, db, item{mediaType: "episode", title: "Some Show", year: 2005, rating: "TV-14", tags: `["drama"]`, episode: e})
	}
	insertItem(t, db, item{mediaType: "movie", title: "Some Movie", year: 2005, rating: "PG-13", tags: `["drama"]`})

	res, err := PreviewFilter(context.Background(), db, mustFilter(t, channel.Filter{AudienceTier: "teen", Genres: []string{"drama"}}))
	if err != nil {
		t.Fatalf("PreviewFilter: %v", err)
	}
	if res.Titles != 2 || res.Items != 4 {
		t.Errorf("preview = %+v, want 2 titles / 4 items", res)
	}
}

func TestRefreshAndPopulate_HandleFilteredChannels(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	manual, _ := channel.Create(ctx, db, channel.Draft{Number: 201, Name: "Manual"})
	filtered, _ := channel.Create(ctx, db, channel.Draft{Number: 200, Name: "Filtered"})
	channel.SetFilter(ctx, db, filtered.ID, channel.Filter{AudienceTier: "adult", Genres: []string{"comedy"}})
	first := insertItem(t, db, item{mediaType: "movie", title: "First", year: 2001, rating: "PG", tags: `["comedy"]`})

	// Populate fills an empty filtered channel and ignores a manual one.
	if _, err := Populate(ctx, db); err != nil {
		t.Fatalf("Populate: %v", err)
	}
	if got := channelItemMediaFileIDs(t, db, filtered.ID); len(got) != 1 || got[0] != first {
		t.Fatalf("Populate lineup = %v, want [%d]", got, first)
	}
	if got := channelItemMediaFileIDs(t, db, manual.ID); len(got) != 0 {
		t.Errorf("manual channel got items %v, want none", got)
	}

	// Populate leaves a filtered channel that already has content alone;
	// Refresh rebuilds it from the current library.
	second := insertItem(t, db, item{mediaType: "movie", title: "Second", year: 2002, rating: "PG", tags: `["comedy"]`})
	Populate(ctx, db)
	if got := channelItemMediaFileIDs(t, db, filtered.ID); len(got) != 1 {
		t.Errorf("Populate changed a filled channel: %v", got)
	}
	if _, err := Refresh(ctx, db); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	got := channelItemMediaFileIDs(t, db, filtered.ID)
	if len(got) != 2 {
		t.Errorf("Refresh lineup = %v, want both comedies (%d, %d)", got, first, second)
	}
	if got := channelItemMediaFileIDs(t, db, manual.ID); len(got) != 0 {
		t.Errorf("Refresh touched the manual channel: %v", got)
	}
}

func TestTVFilteredPoolIncludesEpisodesBeyondFourteenHours(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	var finale int64
	for episode := 1; episode <= 40; episode++ {
		finale = insertItem(t, db, item{mediaType: "episode", title: "Long running show", episode: episode, seconds: 3600})
	}
	ch, err := channel.Create(ctx, db, channel.Draft{Number: 200, Name: "Full series"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = channel.SetFilter(ctx, db, ch.ID, channel.Filter{AudienceTier: "adult", MediaType: "tv"}); err != nil {
		t.Fatal(err)
	}
	if _, err = BuildFilteredChannel(ctx, db, ch.ID); err != nil {
		t.Fatal(err)
	}
	ids := channelItemMediaFileIDs(t, db, ch.ID)
	if len(ids) != 40 || ids[len(ids)-1] != finale {
		t.Fatalf("series truncated: %v", ids)
	}
}
