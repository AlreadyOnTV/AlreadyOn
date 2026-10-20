package storage

import (
	"fmt"
	"github.com/pressly/goose/v3"
	"testing"
)

// Upgrading replaces generated defaults while retaining custom schedules and media.
func TestApprovedLineupMigrationPreservesCustomCatalog(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	goose.SetBaseFS(migrationsFS)
	defer goose.SetBaseFS(nil)
	if err = goose.DownTo(db.DB, "migrations", 79); err != nil {
		t.Fatal(err)
	}
	for n := 100; n <= 152; n++ {
		if _, err = db.Exec(`INSERT INTO channels(number,name) VALUES(?,?)`, n, fmt.Sprintf("Legacy %d", n)); err != nil {
			t.Fatal(err)
		}
	}
	queries := []string{
		`INSERT INTO channels(id,number,name) VALUES(1000,200,'My Custom')`,
		`INSERT INTO media_files(id,root_kind,path,size_bytes,modified_unix,first_seen_unix,last_seen_unix) VALUES(1000,'media','/fixture',1,1,1,1)`,
		`INSERT INTO channel_items(channel_id,media_file_id,position) SELECT id,1000,0 FROM channels`,
		`INSERT INTO schedule_entries(channel_id,media_file_id,kind,duration_seconds,starts_at_unix,ends_at_unix) SELECT id,1000,'program',600,1,601 FROM channels`,
		`INSERT INTO channel_filters(channel_id,audience_tier,genres,extra) VALUES(1000,'adult','["western"]','{}')`,
	}
	for _, q := range queries {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err = goose.UpTo(db.DB, "migrations", 80); err != nil {
		t.Fatal(err)
	}
	checks := map[string]int{
		`SELECT count(*) FROM channels WHERE number BETWEEN 101 AND 136 AND length(name)<=20`: 36,
		`SELECT count(*) FROM channels WHERE number=100 OR number BETWEEN 137 AND 152`:        0,
		`SELECT count(*) FROM channels`:                                                       37,
		`SELECT count(*) FROM media_files WHERE id=1000`:                                      1,
		`SELECT count(*) FROM channel_items`:                                                  1,
		`SELECT count(*) FROM schedule_entries`:                                               1,
		`SELECT count(*) FROM channel_filters WHERE channel_id=1000 AND genres='["western"]'`: 1,
		`SELECT count(*) FROM channel_filters f JOIN channels c ON c.id=f.channel_id WHERE c.number BETWEEN 101 AND 120 AND json_extract(extra,'$.requireKnownYear')=1`: 20,
		`SELECT count(*) FROM channel_filters f JOIN channels c ON c.id=f.channel_id WHERE c.number BETWEEN 121 AND 136 AND era='both'`:                                 16,
	}
	for q, want := range checks {
		var got int
		if err = db.QueryRow(q).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s: got %d want %d", q, got, want)
		}
	}
	if err = goose.UpTo(db.DB, "migrations", 80); err != nil {
		t.Fatal(err)
	}
}
