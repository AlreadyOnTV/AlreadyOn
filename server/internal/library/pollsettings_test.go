package library

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

func openPollSettingsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db.DB
}

func TestGetPollSettings_DefaultsToOneHour(t *testing.T) {
	db := openPollSettingsTestDB(t)

	s, err := GetPollSettings(context.Background(), db)
	if err != nil {
		t.Fatalf("GetPollSettings: %v", err)
	}
	if s.IntervalMinutes != 60 {
		t.Errorf("IntervalMinutes = %d, want 60 (default, matching migration 0026)", s.IntervalMinutes)
	}
}

func TestSavePollSettings_RoundTrips(t *testing.T) {
	db := openPollSettingsTestDB(t)

	want := PollSettings{IntervalMinutes: 240}
	if err := SavePollSettings(context.Background(), db, want); err != nil {
		t.Fatalf("SavePollSettings: %v", err)
	}
	got, err := GetPollSettings(context.Background(), db)
	if err != nil {
		t.Fatalf("GetPollSettings: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestSavePollSettings_RejectsUnlistedInterval(t *testing.T) {
	db := openPollSettingsTestDB(t)

	err := SavePollSettings(context.Background(), db, PollSettings{IntervalMinutes: 45})
	if !errors.Is(err, ErrInvalidPollInterval) {
		t.Errorf("err = %v, want ErrInvalidPollInterval", err)
	}
}
