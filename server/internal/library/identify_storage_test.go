package library

import (
	"context"
	"database/sql"
	"testing"
)

func TestSaveIdentification_PersistsEpisode(t *testing.T) {
	db := openTestDB(t)
	mediaFileID := insertTestMediaFile(t, db, "/media/tv/show/s01e01.mkv")

	id := Identification{IsEpisode: true, Show: "Example Show", Season: 1, Episode: 1, Title: "Pilot"}
	if err := SaveIdentification(context.Background(), db, mediaFileID, id); err != nil {
		t.Fatalf("SaveIdentification: %v", err)
	}

	var mediaType, title, seriesTitle string
	var season, episode int
	var releaseYear sql.NullInt64
	err := db.QueryRow(`SELECT media_type, title, series_title, season_number, episode_number, release_year
		FROM identified_items WHERE media_file_id = ?`, mediaFileID).
		Scan(&mediaType, &title, &seriesTitle, &season, &episode, &releaseYear)
	if err != nil {
		t.Fatalf("query identified_items: %v", err)
	}
	if mediaType != "episode" || title != "Pilot" || seriesTitle != "Example Show" || season != 1 || episode != 1 {
		t.Errorf("got media_type=%q title=%q series_title=%q season=%d episode=%d",
			mediaType, title, seriesTitle, season, episode)
	}
	if releaseYear.Valid {
		t.Errorf("expected release_year to be NULL for an episode, got %v", releaseYear.Int64)
	}
}

func TestSaveIdentification_PersistsMovie(t *testing.T) {
	db := openTestDB(t)
	mediaFileID := insertTestMediaFile(t, db, "/media/movies/example.mkv")

	id := Identification{IsEpisode: false, Title: "Example Movie", Year: 2020}
	if err := SaveIdentification(context.Background(), db, mediaFileID, id); err != nil {
		t.Fatalf("SaveIdentification: %v", err)
	}

	var mediaType, title string
	var releaseYear int
	var seasonNumber sql.NullInt64
	err := db.QueryRow(`SELECT media_type, title, release_year, season_number FROM identified_items WHERE media_file_id = ?`, mediaFileID).
		Scan(&mediaType, &title, &releaseYear, &seasonNumber)
	if err != nil {
		t.Fatalf("query identified_items: %v", err)
	}
	if mediaType != "movie" || title != "Example Movie" || releaseYear != 2020 {
		t.Errorf("got media_type=%q title=%q release_year=%d", mediaType, title, releaseYear)
	}
	if seasonNumber.Valid {
		t.Errorf("expected season_number to be NULL for a movie, got %v", seasonNumber.Int64)
	}
}

func TestSaveIdentification_ReidentifyOverwritesPreviousResult(t *testing.T) {
	db := openTestDB(t)
	mediaFileID := insertTestMediaFile(t, db, "/media/tv/show/s01e01.mkv")

	first := Identification{IsEpisode: true, Show: "Example Show", Season: 1, Episode: 1, Title: "Old Title"}
	if err := SaveIdentification(context.Background(), db, mediaFileID, first); err != nil {
		t.Fatalf("first SaveIdentification: %v", err)
	}
	second := Identification{IsEpisode: true, Show: "Example Show", Season: 1, Episode: 1, Title: "Corrected Title"}
	if err := SaveIdentification(context.Background(), db, mediaFileID, second); err != nil {
		t.Fatalf("second SaveIdentification: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM identified_items WHERE media_file_id = ?`, mediaFileID).Scan(&count); err != nil {
		t.Fatalf("count identified_items: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 row after re-identifying, got %d", count)
	}

	var title string
	if err := db.QueryRow(`SELECT title FROM identified_items WHERE media_file_id = ?`, mediaFileID).Scan(&title); err != nil {
		t.Fatalf("query title: %v", err)
	}
	if title != "Corrected Title" {
		t.Errorf("title = %q, want %q", title, "Corrected Title")
	}
}

func TestSaveIdentification_DoesNotOverwriteAManualCorrection(t *testing.T) {
	db := openTestDB(t)
	mediaFileID := insertTestMediaFile(t, db, "/media/tv/show/s01e01.mkv")

	correction := Identification{IsEpisode: true, Show: "Corrected Show", Season: 1, Episode: 1, Title: "Corrected Title"}
	if err := SetCorrection(context.Background(), db, mediaFileID, correction); err != nil {
		t.Fatalf("SetCorrection: %v", err)
	}

	automatic := Identification{IsEpisode: true, Show: "Guessed Show", Season: 1, Episode: 1, Title: "Guessed Title"}
	if err := SaveIdentification(context.Background(), db, mediaFileID, automatic); err != nil {
		t.Fatalf("SaveIdentification: %v", err)
	}

	var title, seriesTitle string
	err := db.QueryRow(`SELECT title, series_title FROM identified_items WHERE media_file_id = ?`, mediaFileID).Scan(&title, &seriesTitle)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if title != "Corrected Title" || seriesTitle != "Corrected Show" {
		t.Errorf("expected the manual correction to survive an automatic SaveIdentification, got title=%q series_title=%q", title, seriesTitle)
	}
}

func TestClearCorrection_AllowsSaveIdentificationToOverwriteAgain(t *testing.T) {
	db := openTestDB(t)
	mediaFileID := insertTestMediaFile(t, db, "/media/tv/show/s01e01.mkv")

	correction := Identification{IsEpisode: true, Show: "Corrected Show", Season: 1, Episode: 1, Title: "Corrected Title"}
	if err := SetCorrection(context.Background(), db, mediaFileID, correction); err != nil {
		t.Fatalf("SetCorrection: %v", err)
	}
	if err := ClearCorrection(context.Background(), db, mediaFileID); err != nil {
		t.Fatalf("ClearCorrection: %v", err)
	}

	automatic := Identification{IsEpisode: true, Show: "Guessed Show", Season: 1, Episode: 1, Title: "Guessed Title"}
	if err := SaveIdentification(context.Background(), db, mediaFileID, automatic); err != nil {
		t.Fatalf("SaveIdentification: %v", err)
	}

	var title string
	if err := db.QueryRow(`SELECT title FROM identified_items WHERE media_file_id = ?`, mediaFileID).Scan(&title); err != nil {
		t.Fatalf("query: %v", err)
	}
	if title != "Guessed Title" {
		t.Errorf("title = %q, want %q (clearing the correction should let automatic identification apply again)", title, "Guessed Title")
	}
}

func TestClearCorrection_NoExistingRow_ReturnsError(t *testing.T) {
	db := openTestDB(t)
	mediaFileID := insertTestMediaFile(t, db, "/media/tv/show/never-identified.mkv")

	if err := ClearCorrection(context.Background(), db, mediaFileID); err == nil {
		t.Fatal("expected an error clearing a correction with no identified_items row, got nil")
	}
}

// Same-title provider records must not make automatic parent attachment arbitrary.
func TestSaveIdentification_ReattachesOnlyUniqueManagedSeries(t *testing.T) {
	db := openTestDB(t)
	var firstShowID int64
	if err := db.QueryRow(`INSERT INTO tv_shows (tmdb_id,title,local_series_title) VALUES (900,'Example Show','Example Show') RETURNING id`).Scan(&firstShowID); err != nil {
		t.Fatalf("insert first managed show: %v", err)
	}
	firstFile := insertTestMediaFile(t, db, "/media/tv/Example Show/S01E01.mkv")
	if err := SaveIdentification(context.Background(), db, firstFile, Identification{IsEpisode: true, Show: "Example Show", Season: 1, Episode: 1, Title: "Pilot"}); err != nil {
		t.Fatalf("SaveIdentification for unique parent: %v", err)
	}
	var firstParent sql.NullInt64
	if err := db.QueryRow(`SELECT show_id FROM identified_items WHERE media_file_id=?`, firstFile).Scan(&firstParent); err != nil {
		t.Fatalf("query unique parent: %v", err)
	}
	if !firstParent.Valid || firstParent.Int64 != firstShowID {
		t.Fatalf("unique parent=%v, want %d", firstParent, firstShowID)
	}

	if _, err := db.Exec(`INSERT INTO tv_shows (tmdb_id,title,local_series_title) VALUES (901,'Example Show','Example Show')`); err != nil {
		t.Fatalf("insert second same-title show: %v", err)
	}
	secondFile := insertTestMediaFile(t, db, "/media/tv/Example Show/S01E02.mkv")
	if err := SaveIdentification(context.Background(), db, secondFile, Identification{IsEpisode: true, Show: "Example Show", Season: 1, Episode: 2, Title: "Episode Two"}); err != nil {
		t.Fatalf("SaveIdentification for ambiguous parent: %v", err)
	}
	var secondParent sql.NullInt64
	if err := db.QueryRow(`SELECT show_id FROM identified_items WHERE media_file_id=?`, secondFile).Scan(&secondParent); err != nil {
		t.Fatalf("query ambiguous parent: %v", err)
	}
	if secondParent.Valid {
		t.Errorf("ambiguous episode attached to show %d, want it left unlinked", secondParent.Int64)
	}
}
