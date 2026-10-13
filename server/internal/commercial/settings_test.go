package commercial

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db.DB
}

func TestGetSettings_DefaultsToEnabled(t *testing.T) {
	db := openTestDB(t)

	s, err := GetSettings(context.Background(), db)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if !s.Enabled {
		t.Errorf("Enabled = false, want true (default, matching migration 0018)")
	}
	if s.Mode != ModeBetweenOnly {
		t.Errorf("Mode = %q, want %q (default, matching migration 0015)", s.Mode, ModeBetweenOnly)
	}
	if s.TVCommercialsPerBreak != 2 {
		t.Errorf("TVCommercialsPerBreak = %d, want 2 (default)", s.TVCommercialsPerBreak)
	}
	if s.MovieCommercialsPerBreak != 5 {
		t.Errorf("MovieCommercialsPerBreak = %d, want 5 (default)", s.MovieCommercialsPerBreak)
	}
}

func TestSaveSettings_RoundTrips(t *testing.T) {
	db := openTestDB(t)

	want := Settings{Enabled: true, Mode: ModeInterval, IntervalMinutes: 30, TVCommercialsPerBreak: 3, MovieCommercialsPerBreak: 4}
	if err := SaveSettings(context.Background(), db, want); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	got, err := GetSettings(context.Background(), db)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got != want {
		t.Errorf("GetSettings after SaveSettings = %+v, want %+v", got, want)
	}
}

func TestSaveSettings_RejectsInvalidMode(t *testing.T) {
	db := openTestDB(t)

	err := SaveSettings(context.Background(), db, Settings{Enabled: true, Mode: "hourly", IntervalMinutes: 30, TVCommercialsPerBreak: 2, MovieCommercialsPerBreak: 5})
	if !errors.Is(err, ErrInvalidMode) {
		t.Errorf("err = %v, want ErrInvalidMode", err)
	}

	// Confirm the rejected save didn't partially apply — the row should
	// still hold its untouched default (enabled).
	got, err := GetSettings(context.Background(), db)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if !got.Enabled {
		t.Errorf("Enabled = false after a rejected save, want the row untouched (true, the default)")
	}
}

func TestSaveSettings_RejectsInvalidIntervalMinutes(t *testing.T) {
	db := openTestDB(t)

	err := SaveSettings(context.Background(), db, Settings{Enabled: true, Mode: ModeInterval, IntervalMinutes: 45, TVCommercialsPerBreak: 2, MovieCommercialsPerBreak: 5})
	if !errors.Is(err, ErrInvalidIntervalMinutes) {
		t.Errorf("err = %v, want ErrInvalidIntervalMinutes", err)
	}
}

func TestSaveSettings_RejectsOutOfRangeCommercialsPerBreak(t *testing.T) {
	db := openTestDB(t)

	err := SaveSettings(context.Background(), db, Settings{Mode: ModeBetweenOnly, IntervalMinutes: 30, TVCommercialsPerBreak: 6, MovieCommercialsPerBreak: 5})
	if !errors.Is(err, ErrInvalidTVCommercialsPerBreak) {
		t.Errorf("err = %v, want ErrInvalidTVCommercialsPerBreak", err)
	}

	err = SaveSettings(context.Background(), db, Settings{Mode: ModeBetweenOnly, IntervalMinutes: 30, TVCommercialsPerBreak: 2, MovieCommercialsPerBreak: 0})
	if !errors.Is(err, ErrInvalidMovieCommercialsPerBreak) {
		t.Errorf("err = %v, want ErrInvalidMovieCommercialsPerBreak", err)
	}
}

func TestSettings_CommercialsPerBreak(t *testing.T) {
	s := Settings{TVCommercialsPerBreak: 2, MovieCommercialsPerBreak: 5}
	if got := s.CommercialsPerBreak("episode"); got != 2 {
		t.Errorf("CommercialsPerBreak(episode) = %d, want 2", got)
	}
	if got := s.CommercialsPerBreak("movie"); got != 5 {
		t.Errorf("CommercialsPerBreak(movie) = %d, want 5", got)
	}
}

func TestCommercialVolumeOffset(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	s, err := GetSettings(ctx, db)
	if err != nil || s.VolumeOffsetDB != 0 {
		t.Fatal("default offset", s, err)
	}
	s.VolumeOffsetDB = -6
	if err := SaveSettings(ctx, db, s); err != nil {
		t.Fatal(err)
	}
	got, err := GetSettings(ctx, db)
	if err != nil || got != s {
		t.Fatal("roundtrip", got, err)
	}
	s.VolumeOffsetDB = -31
	if err := SaveSettings(ctx, db, s); err != ErrInvalidVolumeOffset {
		t.Fatal("accepted invalid offset", err)
	}
}
