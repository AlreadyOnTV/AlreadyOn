package storage

import (
	"github.com/pressly/goose/v3"
	"testing"
)

// Adding address telemetry must preserve legacy sessions, hashes and expiry.
func TestSessionIPMigrationPreservesSessions(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	goose.SetBaseFS(migrationsFS)
	defer goose.SetBaseFS(nil)
	if err := goose.DownTo(db.DB, "migrations", 84); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users(id,username,password_hash,role) VALUES(999,'fixture','unused','viewer')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO browser_sessions(user_id,token_hash,created_at_unix,expires_at_unix,user_agent,last_active_at_unix) VALUES(999,'retained-hash',10,2000000000,'legacy-browser',20)`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(db.DB); err != nil {
		t.Fatal(err)
	}
	var ip, hash string
	var expiry, active int64
	if err := db.QueryRow(`SELECT ip_address,token_hash,expires_at_unix,last_active_at_unix FROM browser_sessions WHERE user_id=999`).Scan(&ip, &hash, &expiry, &active); err != nil {
		t.Fatal(err)
	}
	if ip != "" || hash != "retained-hash" || expiry != 2000000000 || active != 20 {
		t.Fatalf("session altered: %q %q %d %d", ip, hash, expiry, active)
	}
}
