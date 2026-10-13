package library

import (
	"context"
	"fmt"
	"github.com/alreadyonapp/alreadyon/server/internal/metadata"
	"github.com/alreadyonapp/alreadyon/server/internal/storage"
	"net/http"
	"net/http/httptest"
	"testing"
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
