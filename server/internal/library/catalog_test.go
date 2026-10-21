package library

import (
	"context"
	"errors"
	"testing"
)

func TestCatalogShowInheritanceEpisodeOverrideAndAtomicity(t *testing.T) {
	db := openClassifyTestDB(t)
	ctx := context.Background()
	a := insertUnclassified(t, db, "episode", "Pilot", "Example", 0)
	b := insertUnclassified(t, db, "episode", "Next", "Example", 0)
	movie := insertUnclassified(t, db, "movie", "Other", "", 2010)
	if _, err := UpdateCatalog(ctx, db, []int64{a}, "shows", mustPatch(t, `{"audienceOverride":"teen","year":2009,"genres":["scifi"]}`)); err != nil {
		t.Fatal(err)
	}
	get := func(view string, id int64) CatalogPage {
		t.Helper()
		page, err := Catalog(ctx, db, CatalogQuery{View: view, ShowID: id, Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	shows := get("shows", 0)
	if shows.Total != 1 || shows.Items[0].Files != 2 || shows.Items[0].Year == nil || *shows.Items[0].Year != 2009 || shows.Items[0].Audience != "teen" {
		t.Fatalf("shows %+v", shows)
	}
	if _, err := UpdateCatalog(ctx, db, []int64{b}, "episodes", mustPatch(t, `{"audienceOverride":"adult","genres":["horror"]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateCatalog(ctx, db, []int64{a}, "shows", mustPatch(t, `{"audienceOverride":"young_child","title":"Corrected Show"}`)); err != nil {
		t.Fatal(err)
	}
	eps := get("episodes", a)
	if eps.Total != 2 {
		t.Fatal(eps)
	}
	for _, e := range eps.Items {
		if e.ID == b && (e.Audience != "adult" || len(e.Genres) != 1 || e.Genres[0] != "horror") {
			t.Fatalf("episode override lost: %+v", e)
		}
		if e.ID == a && (e.Audience != "young_child" || e.AudienceSource != "Inherited from show") {
			t.Fatalf("inheritance %+v", e)
		}
	}
	if _, err := UpdateCatalog(ctx, db, []int64{b}, "episodes", mustPatch(t, `{"audienceOverride":null,"title":"Episode correction"}`)); err != nil {
		t.Fatal(err)
	}
	for _, e := range get("episodes", a).Items {
		if e.ID == b && e.Audience != "young_child" {
			t.Fatalf("return to parent %+v", e)
		}
	}
	// A later scan of the same original series attaches to its protected parent.
	c := insertUnclassified(t, db, "episode", "New", "Example", 0)
	var file int64
	db.QueryRow(`SELECT media_file_id FROM identified_items WHERE id=?`, c).Scan(&file)
	if err := SaveIdentification(ctx, db, file, Identification{IsEpisode: true, Show: "Example", Title: "New", Season: 2, Episode: 1}); err != nil {
		t.Fatal(err)
	}
	if page := get("episodes", a); page.Total != 3 {
		t.Fatalf("future episode lost parent: %+v", page)
	}
	if _, err := UpdateCatalog(ctx, db, []int64{a, movie}, "shows", mustPatch(t, `{"audienceOverride":"adult"}`)); !errors.Is(err, ErrInvalidItem) {
		t.Fatal("expected atomic validation", err)
	}
	if page := get("shows", 0); page.Items[0].Audience != "young_child" {
		t.Fatalf("partial bulk update: %+v", page)
	}
	if _, err := UpdateCatalog(ctx, db, []int64{a, 99999}, "shows", mustPatch(t, `{"year":2020}`)); !errors.Is(err, ErrItemNotFound) {
		t.Fatal(err)
	}
	if year := get("shows", 0).Items[0].Year; year == nil || *year != 2009 {
		t.Fatal("partial year update", year)
	}
}

func TestCatalogAllContentFiltersPaginationAndMixedShows(t *testing.T) {
	db := openClassifyTestDB(t)
	ctx := context.Background()
	a := insertUnclassified(t, db, "movie", "Alpha", "", 2009)
	b := insertUnclassified(t, db, "movie", "Beta", "", 2010)
	if _, err := UpdateCatalog(ctx, db, []int64{a}, "movies", mustPatch(t, `{"audienceOverride":"teen","genres":["comedy"],"certification":"PG"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateCatalog(ctx, db, []int64{b}, "movies", mustPatch(t, `{"audienceOverride":"teen","genres":["drama"],"certification":"G"}`)); err != nil {
		t.Fatal(err)
	}
	page, err := Catalog(ctx, db, CatalogQuery{View: "movies", Genre: "comedy,drama", Rating: "PG,G", Limit: 10})
	if err != nil || page.Total != 2 {
		t.Fatalf("multi-select filters %+v %v", page, err)
	}
	page, err = Catalog(ctx, db, CatalogQuery{View: "movies", Search: "alpha", Audience: "teen", Genre: "comedy", Rating: "PG", Source: "manual", Limit: 1})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != a {
		t.Fatalf("combined %+v %v", page, err)
	}
	page, err = Catalog(ctx, db, CatalogQuery{View: "movies", Limit: 1, Offset: 1})
	if err != nil || page.Total != 2 || len(page.Items) != 1 || page.Items[0].Title != "Beta" {
		t.Fatalf("page %+v %v", page, err)
	}
	page, err = Catalog(ctx, db, CatalogQuery{View: "movies", Sort: "title-desc", Limit: 2})
	if err != nil || len(page.Items) != 2 || page.Items[0].Title != "Beta" || page.Items[1].Title != "Alpha" {
		t.Fatalf("descending title sort %+v %v", page, err)
	}
	page, err = Catalog(ctx, db, CatalogQuery{View: "movies", Sort: "rating", Limit: 2})
	if err != nil || len(page.Items) != 2 || page.Items[0].Title != "Beta" || page.Items[1].Title != "Alpha" {
		t.Fatalf("ascending rating sort %+v %v", page, err)
	}
	if _, err := Catalog(ctx, db, CatalogQuery{View: "movies", Limit: 1, Offset: -1}); !errors.Is(err, ErrInvalidItem) {
		t.Fatal(err)
	}
}

func TestCatalogResetAudienceUsesRatingsAndUnrestrictedFallback(t *testing.T) {
	db := openClassifyTestDB(t)
	ctx := context.Background()
	for _, tt := range []struct{ rating, want string }{{"PG", "young_child"}, {"PG-13", "teen"}, {"", "adult"}} {
		id := insertUnclassified(t, db, "movie", "Automatic "+tt.want, "", 2010)
		if _, err := UpdateCatalog(ctx, db, []int64{id}, "movies", mustPatch(t, `{"audienceOverride":"adult","genres":["horror"],"certification":"`+tt.rating+`"}`)); err != nil {
			t.Fatal(err)
		}
		if _, err := UpdateCatalog(ctx, db, []int64{id}, "movies", mustPatch(t, `{"audienceOverride":null}`)); err != nil {
			t.Fatal(err)
		}
		page, err := Catalog(ctx, db, CatalogQuery{View: "movies", Search: "Automatic " + tt.want, Limit: 50})
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("catalog: %+v, %v", page, err)
		}
		e := page.Items[0]
		if e.Audience != tt.want || e.AudienceOverride != nil || e.NeedsReview || len(e.Genres) != 1 || e.Genres[0] != "horror" || e.Year == nil || *e.Year != 2010 {
			t.Fatalf("reset changed metadata or failed automatic classification: %+v", e)
		}
	}
}
