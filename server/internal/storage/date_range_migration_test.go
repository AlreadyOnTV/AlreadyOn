// Default cleanup must preserve custom definitions and referenced channel rules.
package storage

import (
	"testing"

	"github.com/pressly/goose/v3"
)

func TestDateRangeMigrationPreservesCustomAndReferencedRanges(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	goose.SetBaseFS(migrationsFS)
	defer goose.SetBaseFS(nil)
	if err := goose.DownTo(db.DB, "migrations", 88); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE channel_filter_options SET definition=json_set(definition,'$.name','My Nineties') WHERE kind='era' AND id='1990s'`,
		`INSERT INTO channel_filter_options(kind,id,definition,position,default_position) VALUES('era','custom','{"name":"Custom","startYear":1940,"endYear":1945}',20,20)`,
		`INSERT INTO channels(id,number,name) VALUES(9000,9000,'Saved custom channel')`,
		`INSERT INTO channel_filters(channel_id,audience_tier,extra) VALUES(9000,'adult','{"eras":["1980s"],"titlePhrases":["Space"]}')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrate(db.DB); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]int{"both": 1, "modern": 1, "nostalgia": 1, "custom": 1, "1990s": 1, "1980s": 1, "1950s": 0, "1960s": 0, "1970s": 0, "2000s": 0, "2010s": 0, "2020s": 0} {
		var count int
		if err := db.QueryRow(`SELECT count(*) FROM channel_filter_options WHERE kind='era' AND id=?`, id).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Errorf("date range %s count = %d, want %d", id, count, want)
		}
	}
	var extra string
	if err := db.QueryRow(`SELECT extra FROM channel_filters WHERE channel_id=9000`).Scan(&extra); err != nil {
		t.Fatal(err)
	}
	if extra != `{"eras":["1980s"],"titlePhrases":["Space"]}` {
		t.Fatalf("saved filter changed: %s", extra)
	}
}
