package library

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/metadata"
)

func mockKeylessClient(t *testing.T, tvmazeBody string, tvmazeCalls *int32) *metadata.KeylessClient {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(tvmazeCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(tvmazeBody))
	}))
	t.Cleanup(server.Close)

	client := metadata.NewKeylessClient()
	client.HTTPClient = server.Client()
	client.TVmazeBase = server.URL
	client.WikidataEndpoint = server.URL
	client.MinInterval = map[string]time.Duration{}
	return client
}

func TestClassifyGenresKeyless_OneLookupPerSeriesAndMarksSource(t *testing.T) {
	db := openClassifyTestDB(t)
	e1 := insertUnclassified(t, db, "episode", "Show S01E01", "Some Show", 0)
	e2 := insertUnclassified(t, db, "episode", "Show S01E02", "Some Show", 0)
	var calls int32
	client := mockKeylessClient(t, `[{"show":{"name":"Some Show","type":"Scripted","genres":["Comedy"]}}]`, &calls)

	if err := ClassifyGenresKeyless(context.Background(), db, client); err != nil {
		t.Fatalf("ClassifyGenresKeyless: %v", err)
	}
	if calls != 1 {
		t.Errorf("provider calls = %d, want 1 (cached per series)", calls)
	}
	for _, id := range []int64{e1, e2} {
		var tags, source string
		if err := db.QueryRow(`SELECT genre_tags, genre_source FROM identified_items WHERE id = ?`, id).Scan(&tags, &source); err != nil {
			t.Fatalf("query: %v", err)
		}
		if tags != `["comedy"]` || source != "keyless" {
			t.Errorf("row %d: tags=%s source=%s, want [\"comedy\"] keyless", id, tags, source)
		}
	}
}

func TestClassifyGenres_TMDbReplacesKeylessResults(t *testing.T) {
	db := openClassifyTestDB(t)
	id := insertUnclassified(t, db, "movie", "Some Movie", "", 2020)
	if _, err := db.Exec(`UPDATE identified_items SET genre_tags = '[]', genre_source = 'keyless' WHERE id = ?`, id); err != nil {
		t.Fatalf("seed keyless result: %v", err)
	}

	if err := ClassifyGenres(context.Background(), db, mockTMDbClient(t, `{"results":[{"id":1,"genre_ids":[28]}]}`)); err != nil {
		t.Fatalf("ClassifyGenres: %v", err)
	}
	var tags, source string
	if err := db.QueryRow(`SELECT genre_tags, genre_source FROM identified_items WHERE id = ?`, id).Scan(&tags, &source); err != nil {
		t.Fatalf("query: %v", err)
	}
	if tags != `["action"]` || source != "tmdb" {
		t.Errorf("tags=%s source=%s, want [\"action\"] tmdb", tags, source)
	}
}

// Free dates are retained per episode without fabricating a TMDb show identity;
// a later TMDb enrichment remains eligible because its fetch marker stays NULL.
func TestClassifyGenresKeyless_PersistsPremiereSeparately(t *testing.T) {
	db := openClassifyTestDB(t)
	id := insertUnclassified(t, db, "episode", "Pilot", "Some Show", 0)
	var calls int32
	client := mockKeylessClient(t, `[{"show":{"name":"Some Show","type":"Scripted","genres":["Comedy"],"premiered":"2009-12-31"}}]`, &calls)
	if err := ClassifyGenresKeyless(context.Background(), db, client); err != nil {
		t.Fatal(err)
	}
	var date, provider string
	var untouched int
	if err := db.QueryRow(`SELECT keyless_release_date,keyless_provider,metadata_fetched_at_unix IS NULL FROM identified_items WHERE id=?`, id).Scan(&date, &provider, &untouched); err != nil {
		t.Fatal(err)
	}
	if date != "2009-12-31" || provider != "tvmaze" || untouched != 1 {
		t.Fatalf("free metadata=%q %q untouchedTMDb=%d", date, provider, untouched)
	}
	if err := ClassifyGenresKeyless(context.Background(), db, client); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("free enrichment repeated completed lookup: calls=%d", calls)
	}
}

// Free lookups fill date gaps even when TMDb genres exist; manual corrections
// and nonempty TMDb genres remain authoritative across provider combinations.
func TestFreeMetadataFillsGapsWithoutOverwritingProtectedFields(t *testing.T) {
	db := openClassifyTestDB(t)
	ctx := context.Background()
	auto := insertUnclassified(t, db, "episode", "Pilot", "Some Show", 0)
	manual := insertUnclassified(t, db, "episode", "Second", "Some Show", 0)
	db.Exec(`UPDATE identified_items SET genre_tags='["drama"]',genre_source='tmdb' WHERE id=?`, auto)
	if _, err := UpdateCatalog(ctx, db, []int64{manual}, "episodes", mustPatch(t, `{"genres":["fantasy"],"audienceOverride":"teen"}`)); err != nil {
		t.Fatal(err)
	}
	var calls int32
	client := mockKeylessClient(t, `[{"show":{"name":"Some Show","type":"Scripted","genres":["Comedy"],"premiered":"2009-12-31"}}]`, &calls)
	if err := ClassifyGenresKeyless(ctx, db, client); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64]string{auto: `["drama"]`, manual: `["fantasy"]`} {
		var genres, date string
		if err := db.QueryRow(`SELECT genre_tags,keyless_release_date FROM identified_items WHERE id=?`, id).Scan(&genres, &date); err != nil {
			t.Fatal(err)
		}
		if genres != want || date != "2009-12-31" {
			t.Fatalf("id=%d genres=%s date=%s", id, genres, date)
		}
	}
	page, err := Catalog(ctx, db, CatalogQuery{View: "shows", Limit: 50})
	if err != nil || !page.Items[0].Mixed {
		t.Fatalf("mixed summary: %+v %v", page, err)
	}
}
