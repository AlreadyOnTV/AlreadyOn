package metadata

import (
	"context"
	"database/sql"
	"testing"

	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

func openSettingsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db.DB
}

func TestGetSettings_DefaultsToEmpty(t *testing.T) {
	db := openSettingsTestDB(t)

	s, err := GetSettings(context.Background(), db)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if s != (Settings{}) {
		t.Errorf("s = %+v, want zero value", s)
	}
}

func TestSaveSettings_RoundTrips(t *testing.T) {
	db := openSettingsTestDB(t)

	want := Settings{TMDbAPIKey: "tmdb-key"}
	if err := SaveSettings(context.Background(), db, want); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	got, err := GetSettings(context.Background(), db)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestSaveSettings_EmptyClearsAKey(t *testing.T) {
	db := openSettingsTestDB(t)
	if err := SaveSettings(context.Background(), db, Settings{TMDbAPIKey: "tmdb-key"}); err != nil {
		t.Fatalf("SaveSettings (1st): %v", err)
	}
	if err := SaveSettings(context.Background(), db, Settings{TMDbAPIKey: ""}); err != nil {
		t.Fatalf("SaveSettings (2nd): %v", err)
	}
	got, err := GetSettings(context.Background(), db)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got.TMDbAPIKey != "" {
		t.Errorf("got %+v, want TMDbAPIKey cleared", got)
	}
}

func TestSaveSettings_KeylessFlagRoundTripsAndIsIndependentOfTheKey(t *testing.T) {
	db := openSettingsTestDB(t)
	ctx := context.Background()

	if err := SaveSettings(ctx, db, Settings{KeylessEnabled: true}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	got, _ := GetSettings(ctx, db)
	if !got.KeylessEnabled || got.TMDbAPIKey != "" {
		t.Errorf("got %+v, want keyless on with no key", got)
	}

	if err := SaveSettings(ctx, db, Settings{TMDbAPIKey: "tmdb-key", KeylessEnabled: true}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	got, _ = GetSettings(ctx, db)
	if !got.KeylessEnabled || got.TMDbAPIKey != "tmdb-key" {
		t.Errorf("got %+v, want both set", got)
	}

	if err := SaveSettings(ctx, db, Settings{TMDbAPIKey: "tmdb-key"}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	got, _ = GetSettings(ctx, db)
	if got.KeylessEnabled {
		t.Errorf("got %+v, want keyless turned off", got)
	}
}
