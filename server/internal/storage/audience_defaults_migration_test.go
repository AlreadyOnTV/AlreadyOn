// Untouched audience presets advance without overwriting owner-edited definitions.
package storage

import (
	"encoding/json"
	"github.com/pressly/goose/v3"
	"testing"
)

func TestAudienceDefaultsMigrationPreservesEditedDefinitions(t *testing.T) {
	for _, edited := range []bool{false, true} {
		t.Run(map[bool]string{false: "untouched", true: "edited"}[edited], func(t *testing.T) {
			db, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			goose.SetBaseFS(migrationsFS)
			defer goose.SetBaseFS(nil)
			if err := goose.DownTo(db.DB, "migrations", 89); err != nil {
				t.Fatal(err)
			}
			raw := `{"name":"Kids","ratings":["TV-Y","TV-Y7","TV-G","G"]}`
			if edited {
				raw = `{"name":"My Kids","ratings":["G"]}`
			}
			if _, err := db.Exec(`UPDATE channel_filter_options SET definition=? WHERE kind='age' AND id='young_child'`, raw); err != nil {
				t.Fatal(err)
			}
			if err := migrate(db.DB); err != nil {
				t.Fatal(err)
			}
			var got string
			if err := db.QueryRow(`SELECT definition FROM channel_filter_options WHERE kind='age' AND id='young_child'`).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if edited && got != raw {
				t.Fatalf("edited definition changed: %s", got)
			}
			var option struct {
				Ratings []string `json:"ratings"`
			}
			if err := json.Unmarshal([]byte(got), &option); err != nil {
				t.Fatal(err)
			}
			foundPG, foundFV := false, false
			for _, r := range option.Ratings {
				foundPG = foundPG || r == "PG"
				foundFV = foundFV || r == "TV-Y7-FV"
			}
			if !edited && (!foundPG || !foundFV) {
				t.Fatalf("new defaults missing: %s", got)
			}
		})
	}
}
