// Shared age and era definitions preserve edits, ordering and channel reference protection.
package channel

import (
	"context"
	"testing"
)

func TestSharedOptionsRoundTripAndReferenceProtection(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	c, err := Create(ctx, db, Draft{Number: 250, Name: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveOption(ctx, db, "age", FilterOption{ID: "custom", Name: "Custom", Ratings: []string{"PG", "TV-PG"}}); err != nil {
		t.Fatal(err)
	}
	if err := SaveOption(ctx, db, "era", FilterOption{ID: "era", Name: "Range", StartYear: 1980, EndYear: 1999}); err != nil {
		t.Fatal(err)
	}
	f := Filter{AudienceTier: "adult", FilterExtra: FilterExtra{AgeRestrictionID: "custom", Eras: []string{"era"}, TitlePhrases: []string{" Some Phrase "}, ExcludedPaths: []string{"/media/kids/../adults"}}}
	if _, err := SetFilter(ctx, db, c.ID, f); err != nil {
		t.Fatal(err)
	}
	got, err := GetFilter(ctx, db, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AgeOption.Name != "Custom" || got.EraOptions[0].StartYear != 1980 || got.TitlePhrases[0] != "Some Phrase" || got.ExcludedPaths[0] != "/media/adults" {
		t.Fatalf("%+v", got)
	}
	if err := DeleteOption(ctx, db, "age", "custom"); err == nil {
		t.Fatal("deleted a referenced restriction")
	}
	if err := SaveOption(ctx, db, "age", FilterOption{ID: "custom", Name: "Updated", Ratings: []string{"G"}}); err != nil {
		t.Fatal(err)
	}
	got, err = GetFilter(ctx, db, c.ID)
	if err != nil || got.AgeOption.Ratings[0] != "G" {
		t.Fatal("shared edits did not resolve")
	}
	if err := ClearFilter(ctx, db, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := DeleteOption(ctx, db, "age", "custom"); err != nil {
		t.Fatal(err)
	}
}
func TestOptionValidation(t *testing.T) {
	for _, c := range []struct {
		kind  string
		value FilterOption
	}{
		{"age", FilterOption{ID: "x", Name: "x", Ratings: []string{"not-a-rating"}}},
		{"era", FilterOption{ID: "x", Name: "x", StartYear: 2020, EndYear: 1990}},
	} {
		if err := validateOption(c.kind, c.value); err == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
}

func TestDisplayOrderDefaultsMovesAndPersistsOnEdit(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	catalog, err := ListOptions(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog["era"]) != 3 || catalog["era"][0].ID != "both" || catalog["era"][1].ID != "modern" || catalog["era"][2].Name != "Classics" {
		t.Fatal(catalog["era"])
	}
	if err := SaveOption(ctx, db, "era", FilterOption{ID: "custom-range", Name: "Custom", StartYear: 2020, EndYear: 2029}); err != nil {
		t.Fatal(err)
	}
	catalog, err = ListOptions(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := MoveOption(ctx, db, "era", "custom-range", "up"); err != nil {
		t.Fatal(err)
	}
	option := catalog["era"][3]
	option.Name = "Recent"
	if err := SaveOption(ctx, db, "era", option); err != nil {
		t.Fatal(err)
	}
	catalog, err = ListOptions(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if catalog["era"][2].Name != "Recent" {
		t.Fatal("edit reset order")
	}
}

func TestDragOrderMovesAcrossRowsAtomically(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec(`DELETE FROM channel_filter_options WHERE kind='age'`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c", "d"} {
		if err := SaveOption(ctx, db, "age", FilterOption{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := MoveOption(ctx, db, "age", "a", "target", "d"); err != nil {
		t.Fatal(err)
	}
	got, err := ListOptions(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"b", "c", "d", "a"} {
		if got["age"][i].ID != id {
			t.Fatal(got)
		}
	}
	if err := MoveOption(ctx, db, "age", "a", "target", "b"); err != nil {
		t.Fatal(err)
	}
	got, _ = ListOptions(ctx, db)
	if got["age"][0].ID != "a" {
		t.Fatal(got)
	}
	if err := MoveOption(ctx, db, "age", "a", "target", "missing"); err == nil {
		t.Fatal("missing target accepted")
	}
}

func TestRestoreDefaultOrderPreservesDefinitions(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for _, kind := range []string{"age", "era"} {
		for _, id := range []string{"custom-first", "custom-second"} {
			if err := SaveOption(ctx, db, kind, FilterOption{ID: id, Name: id}); err != nil {
				t.Fatal(err)
			}
		}
		original, err := ListOptions(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		first := original[kind][0].ID
		if err := MoveOption(ctx, db, kind, "custom-second", "target", first); err != nil {
			t.Fatal(err)
		}
		if err := SaveOption(ctx, db, kind, FilterOption{ID: "custom-first", Name: "Renamed"}); err != nil {
			t.Fatal(err)
		}
		if err := RestoreOptionOrder(ctx, db, kind); err != nil {
			t.Fatal(err)
		}
		restored, err := ListOptions(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		for i, o := range original[kind] {
			if restored[kind][i].ID != o.ID {
				t.Fatalf("%s order: %+v", kind, restored[kind])
			}
		}
		if restored[kind][len(restored[kind])-2].Name != "Renamed" {
			t.Fatal("restore changed definition")
		}
	}
	if err := RestoreOptionOrder(ctx, db, "invalid"); err == nil {
		t.Fatal("invalid kind accepted")
	}
}
