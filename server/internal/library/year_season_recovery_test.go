package library

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/alreadyonapp/alreadyon/server/internal/config"
)

// Model an already-probed library with old automatic movie guesses. The files
// intentionally aren't playable: recovery must not invoke ffprobe or rewrite them.
func TestScanRepairsUnchangedYearSeasonMovies(t *testing.T) {
	db := openTestDB(t)
	root := t.TempDir()
	ctx := context.Background()
	var firstFile int64
	for episode := 1; episode <= 2; episode++ {
		path := filepath.Join(root, fmt.Sprintf("Tom and Jerry (1940) - S1940E%02d - Short.mkv", episode))
		if err := os.WriteFile(path, []byte("already-probed synthetic placeholder"), 0600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		file := insertTestMediaFile(t, db, path)
		if episode == 1 {
			firstFile = file
		}
		if _, err = db.Exec(`UPDATE media_files SET size_bytes=?,modified_unix=?,status='ok' WHERE id=?`, info.Size(), info.ModTime().Unix(), file); err != nil {
			t.Fatal(err)
		}
		if err = SaveIdentification(ctx, db, file, Identification{Title: "Tom and Jerry", Year: 1940}); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`UPDATE identified_items SET tmdb_id=1401224,overview='wrong movie',release_date='1940-01-01',episode_air_date='1940-01-01',metadata_fetched_at_unix=100,keyless_release_date='1940-01-01',keyless_provider='old',keyless_fetched_at_unix=100,genre_tags='["horror"]',genre_source='tmdb',content_rating='R' WHERE media_file_id=?`, file); err != nil {
			t.Fatal(err)
		}
	}
	var channel int64
	if err := db.QueryRow(`INSERT INTO channels(number,name) VALUES(999,'Synthetic') RETURNING id`).Scan(&channel); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schedule_entries(channel_id,media_file_id,kind,source_offset_seconds,duration_seconds,starts_at_unix,ends_at_unix) VALUES(?,?,'program',0,10,10,20)`, channel, firstFile); err != nil {
		t.Fatal(err)
	}
	scanner := Scanner{DB: db, Roots: []config.ValidatedRoot{mediaRoot(t, root)}}
	result, err := scanner.Scan(ctx)
	if err != nil || len(result.Errors) != 0 || result.FilesUnchanged != 2 {
		t.Fatalf("rescan: %+v, %v", result, err)
	}
	var pending int
	if err = db.QueryRow(`SELECT count(*) FROM identified_items WHERE media_type='episode' AND season_number=1940 AND tmdb_id IS NULL AND show_id IS NULL AND overview IS NULL AND release_date IS NULL AND episode_air_date IS NULL AND metadata_fetched_at_unix IS NULL AND keyless_release_date IS NULL AND keyless_provider IS NULL AND keyless_fetched_at_unix IS NULL AND genre_tags IS NULL AND genre_source IS NULL AND content_rating IS NULL`).Scan(&pending); err != nil || pending != 2 {
		t.Fatalf("stale metadata remains: %d, %v", pending, err)
	}
	movies, err := Catalog(ctx, db, CatalogQuery{View: "movies", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	shows, err := Catalog(ctx, db, CatalogQuery{View: "shows", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if movies.Total != 0 || shows.Total != 1 || shows.Items[0].Files != 2 {
		t.Fatalf("movies=%+v shows=%+v", movies, shows)
	}
	var scheduleFile int64
	if err = db.QueryRow(`SELECT media_file_id FROM schedule_entries WHERE channel_id=? AND starts_at_unix=10 AND ends_at_unix=20`, channel).Scan(&scheduleFile); err != nil || scheduleFile != firstFile {
		t.Fatalf("published airing changed: %d, %v", scheduleFile, err)
	}
	// Cleared fetch markers must lead back through TV lookup, never movie lookup.
	client, _ := mockTMDbPathRoutedClient(t, map[string]string{
		"/search/tv":                    `{"results":[{"id":123}]}`,
		"/tv/123":                       `{"first_air_date":"1940-02-10","overview":"TV series"}`,
		"/tv/123/content_ratings":       `{"results":[]}`,
		"/tv/123/season/1940/episode/1": `{"overview":"TV episode","air_date":"1940-02-10"}`,
		"/tv/123/season/1940/episode/2": `{"overview":"TV episode","air_date":"1941-07-19"}`,
	})
	if err = EnrichMetadata(ctx, db, client); err != nil {
		t.Fatal(err)
	}
	// A later unchanged scan keeps newly fetched TV data and canonical titles.
	if _, err = db.Exec(`UPDATE identified_items SET title='Provider episode' WHERE media_type='episode'`); err != nil {
		t.Fatal(err)
	}
	result, err = scanner.Scan(ctx)
	if err != nil || len(result.Errors) != 0 {
		t.Fatalf("second scan: %+v %v", result, err)
	}
	var retained int
	if err = db.QueryRow(`SELECT count(*) FROM identified_items i JOIN tv_shows s ON s.id=i.show_id WHERE s.tmdb_id=123 AND i.metadata_fetched_at_unix IS NOT NULL AND i.overview='TV episode' AND i.title='Provider episode'`).Scan(&retained); err != nil || retained != 2 {
		t.Fatalf("TV metadata reset: %d %v", retained, err)
	}
}

func TestYearSeasonTypeRepairPreservesManualMetadata(t *testing.T) {
	for _, manualIdentity := range []bool{false, true} {
		t.Run(fmt.Sprint(manualIdentity), func(t *testing.T) {
			db := openTestDB(t)
			ctx := context.Background()
			file := insertTestMediaFile(t, db, "/synthetic/S1940E01.mkv")
			if err := SaveIdentification(ctx, db, file, Identification{Title: "Old movie", Year: 1940}); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE identified_items SET genre_tags='["comedy"]',genre_source='manual',content_rating='PG',content_rating_manual=1,audience_override='young_child',tmdb_id=999,metadata_fetched_at_unix=100,is_manual_correction=? WHERE media_file_id=?`, manualIdentity, file); err != nil {
				t.Fatal(err)
			}
			if err := SaveIdentification(ctx, db, file, Identification{IsEpisode: true, Show: "Example", Season: 1940, Episode: 1, Title: "Short"}); err != nil {
				t.Fatal(err)
			}
			var kind, genres, source, rating, audience string
			var tmdb, fetched sql.NullInt64
			if err := db.QueryRow(`SELECT media_type,genre_tags,genre_source,content_rating,audience_override,tmdb_id,metadata_fetched_at_unix FROM identified_items WHERE media_file_id=?`, file).Scan(&kind, &genres, &source, &rating, &audience, &tmdb, &fetched); err != nil {
				t.Fatal(err)
			}
			if genres != `["comedy"]` || source != "manual" || rating != "PG" || audience != "young_child" {
				t.Fatal("manual field override lost")
			}
			if manualIdentity {
				if kind != "movie" || !tmdb.Valid || tmdb.Int64 != 999 || !fetched.Valid {
					t.Fatal("manual identity changed")
				}
			} else if kind != "episode" || tmdb.Valid || fetched.Valid {
				t.Fatal("automatic provider identity not reset")
			}
		})
	}
}
