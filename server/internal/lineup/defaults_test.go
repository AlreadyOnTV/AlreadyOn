package lineup

import (
	"context"
	"slices"
	"testing"

	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/eligibility"
	"github.com/alreadyonapp/alreadyon/server/internal/metadata"
)

// Default filters are the public age/era contract used by both startup and previews.
func TestDefaultFilterFor_CarriesTheThemeAndBlockAudience(t *testing.T) {
	cases := []struct {
		number int
		want   channel.Filter
	}{
		{101, channel.Filter{AudienceTier: "young_child", MediaType: "tv", Era: "nostalgia", Genres: []string{"animation", "anime"}}},
		{106, channel.Filter{AudienceTier: "young_child", MediaType: "movie", Era: "modern", Genres: []string{}}},
		{109, channel.Filter{AudienceTier: "teen", MediaType: "tv", Era: "both", Genres: []string{"anime"}}},
		{129, channel.Filter{AudienceTier: "adult", MediaType: "tv", Era: "both", Genres: []string{"fantasy", "scifi"}}},
		{136, channel.Filter{AudienceTier: "adult", MediaType: "movie", Era: "both", Genres: []string{"fantasy", "scifi", "horror"}}},
	}
	for _, c := range cases {
		got, ok := defaultFilterFor(c.number)
		if !ok || got.AudienceTier != c.want.AudienceTier || got.MediaType != c.want.MediaType || got.Era != c.want.Era || !slices.Equal(got.Genres, c.want.Genres) {
			t.Errorf("channel %d: filter = %+v (ok=%v), want %+v", c.number, got, ok, c.want)
		}
	}
	if _, ok := defaultFilterFor(200); ok {
		t.Error("a custom channel number has no default filter")
	}
}

func TestEveryDefaultFilterIsValid(t *testing.T) {
	for number := range defaultFilters {
		f, _ := defaultFilterFor(number)
		if _, err := f.Normalize(); err != nil {
			t.Errorf("channel %d: default filter invalid: %v", number, err)
		}
	}
}

func TestSeedDefaultFilters_FillsOnlyBuiltInsWithoutOne(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	scifi := insertChannel(t, db, 129, "Worlds One")
	edited := insertChannel(t, db, 130, "Worlds Two")
	custom := insertChannel(t, db, 200, "My Custom Channel")
	own := channel.Filter{AudienceTier: "teen", Genres: []string{"comedy"}}
	if _, err := channel.SetFilter(ctx, db, edited, own); err != nil {
		t.Fatalf("SetFilter: %v", err)
	}

	n, err := SeedDefaultFilters(ctx, db)
	if err != nil || n != 1 {
		t.Fatalf("SeedDefaultFilters = %d, %v; want 1, nil", n, err)
	}
	if f, _ := channel.GetFilter(ctx, db, scifi); f == nil || f.MediaType != "tv" || !slices.Equal(f.Genres, []string{"fantasy", "scifi"}) {
		t.Errorf("channel 129 filter = %+v, want its scifi TV theme", f)
	}
	if f, _ := channel.GetFilter(ctx, db, edited); f == nil || f.AudienceTier != "teen" || !slices.Equal(f.Genres, []string{"comedy"}) {
		t.Errorf("an admin's own filter was overwritten: %+v", f)
	}
	if f, _ := channel.GetFilter(ctx, db, custom); f != nil {
		t.Errorf("custom channel got a filter: %+v", f)
	}
	if n, _ := SeedDefaultFilters(ctx, db); n != 0 {
		t.Errorf("second run seeded %d filters, want 0", n)
	}
}

func TestRefresh_UsesAnEditedDefaultChannelFilter(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	scifi := insertChannel(t, db, 129, "Worlds One")
	space := insertClassified(t, db, "episode", "Space Show", 1, 1, 0, 600, `["scifi"]`)
	funny := insertClassified(t, db, "episode", "Funny Show", 1, 1, 0, 600, `["comedy"]`)
	if _, err := Refresh(ctx, db); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := channelItemMediaFileIDs(t, db, scifi); len(got) != 1 || got[0] != space {
		t.Fatalf("before edit = %v, want [%d]", got, space)
	}

	if _, err := channel.SetFilter(ctx, db, scifi, channel.Filter{AudienceTier: "adult", Genres: []string{"comedy"}}); err != nil {
		t.Fatalf("SetFilter: %v", err)
	}
	if _, err := Refresh(ctx, db); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := channelItemMediaFileIDs(t, db, scifi); len(got) != 1 || got[0] != funny {
		t.Errorf("after editing the default channel's filter = %v, want [%d]", got, funny)
	}
}

func TestApprovedDefaultsAgeEraAndGenreBoundaries(t *testing.T) {
	if len(defaultFilters) != 38 {
		t.Fatalf("defaults=%d, want 38", len(defaultFilters))
	}
	for number, f := range defaultFilters {
		if number < 101 || number > 138 {
			t.Fatal(number)
		}
		if f.RequireKnownYear != (f.Era == "nostalgia") {
			t.Fatalf("known-year rule on %d", number)
		}
		if number >= 121 && number <= 136 && f.Era != "both" {
			t.Fatalf("unrestricted era on %d", number)
		}
	}
	makeItem := func(year int, tags ...metadata.Tag) classifiedItem {
		return classifiedItem{MediaType: "episode", ReleaseYear: year, Certification: "TV-Y7", Tags: tags}
	}
	classic, _ := defaultFilterFor(101)
	modern, _ := defaultFilterFor(102)
	if !filterMatches(classic, makeItem(2009, metadata.TagAnimation)) || filterMatches(classic, makeItem(2010, metadata.TagAnimation)) || !filterMatches(modern, makeItem(2010, metadata.TagAnimation)) {
		t.Fatal("2010 boundary")
	}
	if filterMatches(classic, makeItem(0, metadata.TagAnimation)) || !filterMatches(modern, makeItem(0, metadata.TagAnimation)) {
		t.Fatal("unknown year should use Modern, never Classics")
	}
	live, _ := defaultFilterFor(103)
	if filterMatches(live, makeItem(2009, metadata.TagAnimation)) {
		t.Fatal("live action")
	}
	toon, _ := defaultFilterFor(107)
	if filterMatches(toon, makeItem(2009, metadata.TagAnimation, metadata.TagAnime)) {
		t.Fatal("anime leaked into cartoons")
	}
	anime, _ := defaultFilterFor(109)
	validatedAnime := func(year int) classifiedItem {
		it := makeItem(year, metadata.TagAnime)
		it.AudienceOverride = "teen"
		return it
	}
	if !filterMatches(anime, validatedAnime(1990)) || !filterMatches(anime, validatedAnime(2025)) {
		t.Fatal("anime era restriction")
	}
	anime.AudienceTier = "teen"
	tooOld := makeItem(2020, metadata.TagAnime)
	tooOld.Certification = "TV-MA"
	if filterMatches(anime, tooOld) {
		t.Fatal("mature anime on teen channel")
	}
	unrestricted, _ := defaultFilterFor(123)
	unknown := makeItem(0, metadata.TagAnime)
	unknown.Certification = ""
	if !filterMatches(unrestricted, unknown) {
		t.Fatal("unknown date missing from unrestricted")
	}
}

func TestValidatedAudienceDoesNotBypassCustomRatingFilter(t *testing.T) {
	item := classifiedItem{MediaType: "movie", Certification: "PG", AudienceOverride: "young_child", ReleaseYear: 2010, Tags: []metadata.Tag{metadata.TagAnime}}
	kids, _ := defaultFilterFor(106)
	kids.AgeOption = &channel.FilterOption{Ratings: []string{"G"}}
	if !filterMatches(kids, item) {
		t.Fatal("positive kids movie exception excluded from default")
	}
	custom := kids
	custom.MatchAudience = false
	if filterMatches(custom, item) {
		t.Fatal("manual audience bypassed custom exact rating filter")
	}
}

// Every identified title has a default destination even without genres or dates.
// This also guards the second eligibility gate (PG must be allowed on Kids).
func TestDefaultsCoverAutomaticRatingsWithoutManualMatching(t *testing.T) {
	db := openTestDB(t)
	resolved := make(map[int]channel.Filter)
	for n, f := range defaultFilters {
		f, err := channel.Resolve(context.Background(), db, f)
		if err != nil {
			t.Fatal(err)
		}
		resolved[n] = f
	}
	for _, kind := range []string{"movie", "episode"} {
		certs := append([]string{"", "NR", "invalid"}, eligibility.TVCertifications...)
		if kind == "movie" {
			certs = append([]string{"", "invalid"}, eligibility.MovieCertifications...)
		}
		for _, cert := range certs {
			for _, year := range []int{0, 2009, 2010} {
				for _, tags := range [][]metadata.Tag{nil, {metadata.TagAnime}, {metadata.TagHorror, metadata.TagWar}, {metadata.TagAnimation}} {
					it := classifiedItem{MediaType: kind, Certification: cert, ReleaseYear: year, Tags: tags}
					count := 0
					for _, f := range resolved {
						if filterMatches(f, it) {
							count++
							if f.AudienceTier != string(eligibility.RequiredTier(kind, cert)) {
								t.Fatalf("%+v crossed audience: %+v", it, f)
							}
						}
					}
					if count == 0 {
						t.Errorf("no default destination for %+v", it)
					}
				}
			}
		}
	}
}
