// Filter rules continue to protect channel eligibility after report removal.
package lineup

import (
	"context"
	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/metadata"
	"testing"
)

func TestEnhancedFiltersCombineWithinAndAcrossDimensions(t *testing.T) {
	f := channel.Filter{AudienceTier: "adult", MediaType: "movie", Genres: []string{"western"}, FilterExtra: channel.FilterExtra{
		AgeRestrictionID: "pg", Eras: []string{"classic", "modern"}, TitlePhrases: []string{"wild west", "holiday"}, ExcludedPaths: []string{"/media/private"}},
	}
	catalog := channel.FilterCatalog{
		"age": {{ID: "pg", Ratings: []string{"PG"}}},
		"era": {{ID: "classic", StartYear: 1950, EndYear: 1959}, {ID: "modern", StartYear: 2010}},
	}
	f, err := channel.ResolveFilter(f, catalog)
	if err != nil {
		t.Fatal(err)
	}
	it := classifiedItem{Title: "The WILD WEST Story", MediaType: "movie", Certification: "PG", ReleaseYear: 1958, Path: "/media/private-copy/a.mkv", Tags: []metadata.Tag{metadata.TagWestern}}
	if !filterMatches(f, it) {
		t.Fatal(filterMismatches(f, it))
	}
	for name, change := range map[string]func(*classifiedItem){
		"rating": func(i *classifiedItem) { i.Certification = "" },
		"era":    func(i *classifiedItem) { i.ReleaseYear = 1985 },
		"title":  func(i *classifiedItem) { i.Title = "Ordinary" },
		"path":   func(i *classifiedItem) { i.Path = "/media/private/nested/a.mkv" },
		"genre":  func(i *classifiedItem) { i.Tags = nil },
	} {
		t.Run(name, func(t *testing.T) {
			other := it
			change(&other)
			if filterMatches(f, other) {
				t.Fatal("expected blocked")
			}
		})
	}
	it.Title = "HOLIDAY"
	it.ReleaseYear = 2024
	if !filterMatches(f, it) {
		t.Fatal("OR alternatives did not match")
	}
}
func TestExplicitIncludedPathsEmptyAndDescendantBoundaries(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	insertItem(t, db, item{mediaType: "movie", title: "Film", seconds: 7200})
	f := channel.Filter{AudienceTier: "adult", Era: "both"}
	check := func(want int) {
		t.Helper()
		r, err := PreviewFilter(ctx, db, f)
		if err != nil || r.Items != want {
			t.Fatalf("%+v %v want %d", r, err, want)
		}
	}
	check(1)
	paths := []string{}
	f.IncludedPaths = &paths
	check(0)
	paths = []string{"/medi"}
	check(0)
	paths = []string{"/media"}
	check(1)
}

func TestUnknownRatingsDoNotBypassKnownRatingSelections(t *testing.T) {
	f := channel.Filter{AudienceTier: "adult", MediaType: "movie", AgeOption: &channel.FilterOption{Ratings: []string{"PG"}, AllowUnknown: true}}
	for _, tc := range []struct {
		rating string
		want   bool
	}{{"PG", true}, {"R", false}, {"", true}, {"unrecognized", true}} {
		if got := filterMatches(f, classifiedItem{MediaType: "movie", Certification: tc.rating}); got != tc.want {
			t.Errorf("rating %q: got %v", tc.rating, got)
		}
	}
	f.AgeOption.AllowUnknown = false
	if filterMatches(f, classifiedItem{MediaType: "movie"}) {
		t.Fatal("unknown must be disabled independently")
	}
}
