package library

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alreadyonapp/alreadyon/server/internal/metadata"
	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

func TestDirectTMDbLookupAppliesShowPremiereToAllEpisodes(t *testing.T) {
	store, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, q := range []string{
		`INSERT INTO media_files(id,root_kind,path,size_bytes,modified_unix,first_seen_unix,last_seen_unix) VALUES(1,'media','/a',1,1,1,1),(2,'media','/b',1,1,1,1)`,
		`INSERT INTO identified_items(id,media_file_id,media_type,title,series_title,season_number,episode_number) VALUES(1,1,'episode','Pilot','Wrong name',1,1),(2,2,'episode','Next','Wrong name',1,2)`,
	} {
		if _, err := store.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tv/77":
			fmt.Fprint(w, `{"name":"Correct Show","overview":"Plot","first_air_date":"1999-12-31","genres":[{"id":18}]}`)
		case "/tv/77/content_ratings":
			fmt.Fprint(w, `{"results":[{"iso_3166_1":"US","rating":"TV-PG"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer mock.Close()
	client := metadata.NewClient("test")
	client.BaseURL = mock.URL
	n, err := LookupTMDb(context.Background(), store.DB, client, 1, 77)
	if err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
	var count int
	if err := store.QueryRow(`SELECT count(*) FROM identified_items ii JOIN tv_shows ts ON ts.id=ii.show_id WHERE ii.series_title='Correct Show' AND ts.tmdb_id=77 AND ts.first_air_date='1999-12-31' AND ii.genre_tags='["drama"]'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("metadata %d %v", count, err)
	}
	if _, err := LookupTMDb(context.Background(), store.DB, client, 1, 404); err == nil {
		t.Fatal("accepted missing TMDb ID")
	}
	if err := store.QueryRow(`SELECT count(*) FROM identified_items WHERE series_title='Correct Show'`).Scan(&count); err != nil || count != 2 {
		t.Fatal("failed lookup mutated metadata")
	}
}

func TestPreviewTMDbReturnsDetailsWithoutPersistingThem(t *testing.T) {
	store, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, q := range []string{
		`INSERT INTO media_files(id,root_kind,path,size_bytes,modified_unix,first_seen_unix,last_seen_unix) VALUES(1,'media','/film.mkv',1,1,1,1)`,
		`INSERT INTO identified_items(id,media_file_id,media_type,title,release_year,genre_tags,genre_source) VALUES(1,1,'movie','Original Film',1985,'["comedy"]','manual')`,
	} {
		if _, err := store.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/movie/77":
			fmt.Fprint(w, `{"title":"Preview Film","release_date":"2024-05-01","genres":[{"id":18}]}`)
		case "/movie/77/release_dates":
			fmt.Fprint(w, `{"results":[{"iso_3166_1":"US","release_dates":[{"certification":"PG-13","type":3}]}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer mock.Close()
	client := metadata.NewClient("test")
	client.BaseURL = mock.URL
	preview, err := PreviewTMDb(context.Background(), store.DB, client, 1, 77)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Title != "Preview Film" || preview.Date != "2024-05-01" || preview.Certification != "PG-13" || len(preview.Tags) != 1 || preview.Tags[0] != metadata.TagDrama {
		t.Fatalf("preview=%+v", preview)
	}
	var title string
	var year int
	var genres string
	var tmdbID sql.NullInt64
	if err := store.QueryRow(`SELECT title,release_year,genre_tags,tmdb_id FROM identified_items WHERE id=1`).Scan(&title, &year, &genres, &tmdbID); err != nil {
		t.Fatal(err)
	}
	if title != "Original Film" || year != 1985 || genres != `["comedy"]` || tmdbID.Valid {
		t.Fatalf("preview persisted data: title=%q year=%d genres=%q tmdbId=%v", title, year, genres, tmdbID)
	}
}

// Changing provider identity keeps the user's show and episode corrections.
func TestProviderIdentityCorrectionPreservesManualHierarchy(t *testing.T) {
	db := openClassifyTestDB(t)
	ctx := context.Background()
	a := insertUnclassified(t, db, "episode", "Pilot", "Old Name", 0)
	b := insertUnclassified(t, db, "episode", "Next", "Old Name", 0)
	if _, err := UpdateCatalog(ctx, db, []int64{a}, "shows", mustPatch(t, `{"audienceOverride":"teen","genres":["fantasy"],"certification":"TV-PG"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateCatalog(ctx, db, []int64{b}, "episodes", mustPatch(t, `{"audienceOverride":"adult","genres":["horror"]}`)); err != nil {
		t.Fatal(err)
	}
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tv/77" {
			fmt.Fprint(w, `{"name":"Correct Name","first_air_date":"2001-01-01","genres":[{"id":18}]}`)
		} else {
			fmt.Fprint(w, `{"results":[{"iso_3166_1":"US","rating":"TV-14"}]}`)
		}
	}))
	defer mock.Close()
	client := metadata.NewClient("test")
	client.BaseURL = mock.URL
	if _, err := LookupTMDb(ctx, db, client, a, 77); err != nil {
		t.Fatal(err)
	}
	page, err := Catalog(ctx, db, CatalogQuery{View: "episodes", ShowID: a, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range page.Items {
		if e.ID == a && (e.Audience != "teen" || e.Genres[0] != "fantasy" || *e.Certification != "TV-PG") {
			t.Fatalf("parent lost %+v", e)
		}
		if e.ID == b && (e.Audience != "adult" || e.Genres[0] != "horror") {
			t.Fatalf("episode lost %+v", e)
		}
	}
}
