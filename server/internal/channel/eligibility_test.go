package channel

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/eligibility"
)

// insertRatedMovie inserts a movie with certification attached to
// channelID's lineup.
func insertRatedMovie(t *testing.T, db *sql.DB, channelID int64, path, certification string, position int) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().Unix()

	var mediaFileID int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO media_files (root_kind, path, size_bytes, modified_unix, first_seen_unix, last_seen_unix, duration_seconds, status)
		VALUES ('media', ?, 1024, ?, ?, ?, 600, 'ok') RETURNING id`,
		path, now, now, now).Scan(&mediaFileID); err != nil {
		t.Fatalf("insert media_files: %v", err)
	}
	var cert sql.NullString
	if certification != "" {
		cert = sql.NullString{String: certification, Valid: true}
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO identified_items (media_file_id, media_type, title, content_rating) VALUES (?, 'movie', ?, ?)`,
		mediaFileID, path, cert); err != nil {
		t.Fatalf("insert identified_items: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO channel_items (channel_id, media_file_id, position) VALUES (?, ?, ?)`,
		channelID, mediaFileID, position); err != nil {
		t.Fatalf("insert channel_items: %v", err)
	}
}

// insertRatedEpisode inserts an episode of a show certified
// certification (creating the tv_shows row if seriesTitle hasn't been
// used yet in this test) attached to channelID's lineup.
func insertRatedEpisode(t *testing.T, db *sql.DB, channelID int64, path, seriesTitle, certification string, position int) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().Unix()

	var cert sql.NullString
	if certification != "" {
		cert = sql.NullString{String: certification, Valid: true}
	}
	var showID int64
	err := db.QueryRowContext(ctx, `SELECT id FROM tv_shows WHERE title = ?`, seriesTitle).Scan(&showID)
	if err == sql.ErrNoRows {
		if err := db.QueryRowContext(ctx, `
			INSERT INTO tv_shows (tmdb_id, title, content_rating) VALUES (?, ?, ?) RETURNING id`,
			position+1000, seriesTitle, cert).Scan(&showID); err != nil {
			t.Fatalf("insert tv_shows: %v", err)
		}
	} else if err != nil {
		t.Fatalf("look up tv_shows: %v", err)
	}

	var mediaFileID int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO media_files (root_kind, path, size_bytes, modified_unix, first_seen_unix, last_seen_unix, duration_seconds, status)
		VALUES ('media', ?, 1024, ?, ?, ?, 600, 'ok') RETURNING id`,
		path, now, now, now).Scan(&mediaFileID); err != nil {
		t.Fatalf("insert media_files: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO identified_items (media_file_id, media_type, title, series_title, show_id) VALUES (?, 'episode', ?, ?, ?)`,
		mediaFileID, path, seriesTitle, showID); err != nil {
		t.Fatalf("insert identified_items: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO channel_items (channel_id, media_file_id, position) VALUES (?, ?, ?)`,
		channelID, mediaFileID, position); err != nil {
		t.Fatalf("insert channel_items: %v", err)
	}
}

func TestMinimumAudienceTier_EmptyLineup_IsYoungChild(t *testing.T) {
	db := openTestDB(t)
	c, _ := Create(context.Background(), db, Draft{Number: 2000, Name: "Empty"})

	tier, err := MinimumAudienceTier(context.Background(), db, c.ID)
	if err != nil {
		t.Fatalf("MinimumAudienceTier: %v", err)
	}
	if tier != eligibility.TierYoungChild {
		t.Errorf("tier = %q, want young_child for an empty lineup", tier)
	}
}

func TestMinimumAudienceTier_AllYoungChildContent(t *testing.T) {
	db := openTestDB(t)
	c, _ := Create(context.Background(), db, Draft{Number: 2001, Name: "Kids"})
	insertRatedMovie(t, db, c.ID, "/media/kids1.mkv", "G", 1)
	insertRatedEpisode(t, db, c.ID, "/media/kids2.mkv", "Kids Show", "TV-Y7", 2)
	db.Exec(`UPDATE identified_items SET genre_tags='["family","animation"]'`)

	tier, err := MinimumAudienceTier(context.Background(), db, c.ID)
	if err != nil {
		t.Fatalf("MinimumAudienceTier: %v", err)
	}
	if tier != eligibility.TierYoungChild {
		t.Errorf("tier = %q, want young_child", tier)
	}
}

func TestMinimumAudienceTier_OneAdultItem_RaisesTheWholeChannel(t *testing.T) {
	db := openTestDB(t)
	c, _ := Create(context.Background(), db, Draft{Number: 2002, Name: "Mixed"})
	insertRatedMovie(t, db, c.ID, "/media/g.mkv", "G", 1)
	insertRatedMovie(t, db, c.ID, "/media/r.mkv", "R", 2)

	tier, err := MinimumAudienceTier(context.Background(), db, c.ID)
	if err != nil {
		t.Fatalf("MinimumAudienceTier: %v", err)
	}
	if tier != eligibility.TierAdult {
		t.Errorf("tier = %q, want adult (one R movie should raise the whole channel)", tier)
	}
}

func TestMinimumAudienceTier_MissingCertification_RequiresAdult(t *testing.T) {
	db := openTestDB(t)
	c, _ := Create(context.Background(), db, Draft{Number: 2003, Name: "Unrated"})
	insertRatedMovie(t, db, c.ID, "/media/unrated.mkv", "", 1)

	tier, err := MinimumAudienceTier(context.Background(), db, c.ID)
	if err != nil {
		t.Fatalf("MinimumAudienceTier: %v", err)
	}
	if tier != eligibility.TierAdult {
		t.Errorf("tier = %q, want adult (missing certification must default-deny, not default-allow)", tier)
	}
}

func TestMinimumAudienceTier_EpisodeUsesParentShowCertification(t *testing.T) {
	db := openTestDB(t)
	c, _ := Create(context.Background(), db, Draft{Number: 2004, Name: "Teen Show"})
	insertRatedEpisode(t, db, c.ID, "/media/ep1.mkv", "Some Show", "TV-PG", 1)
	insertRatedEpisode(t, db, c.ID, "/media/ep2.mkv", "Some Show", "TV-PG", 2)

	tier, err := MinimumAudienceTier(context.Background(), db, c.ID)
	if err != nil {
		t.Fatalf("MinimumAudienceTier: %v", err)
	}
	if tier != eligibility.TierTeen {
		t.Errorf("tier = %q, want teen", tier)
	}
}
