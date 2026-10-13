package httpapi

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/status"
)

type libraryCounts struct {
	FilesFound          int `json:"filesFound"`
	FilesProbed         int `json:"filesProbed"`
	ItemsIdentified     int `json:"itemsIdentified"`
	ItemsClassified     int `json:"itemsClassified"`
	ChannelsWithContent int `json:"channelsWithContent"`
}

type startupStatusResponse struct {
	Phase           status.Phase   `json:"phase"`
	Active          bool           `json:"active"`
	Trigger         status.Trigger `json:"trigger,omitempty"`
	StartupComplete bool           `json:"startupComplete"`
	MediaRoots      int            `json:"mediaRoots"`
	StartedAt       *time.Time     `json:"startedAt"`
	FinishedAt      *time.Time     `json:"finishedAt"`
	LastError       string         `json:"lastError,omitempty"`
	Library         libraryCounts  `json:"library"`
}

// handleStartupStatus serves GET /api/status/startup: what the
// background library work (startup pipeline, refreshes, content polls)
// is doing, plus live library counts read straight from the database so
// they advance while a long scan is still running (#108).
func handleStartupStatus(db *sql.DB, tracker *status.Tracker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		counts, err := loadLibraryCounts(r.Context(), db)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s := tracker.Snapshot()
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, startupStatusResponse{
			Phase:           s.Phase,
			Active:          s.Active,
			Trigger:         s.Trigger,
			StartupComplete: s.StartupComplete,
			MediaRoots:      s.MediaRoots,
			StartedAt:       s.StartedAt,
			FinishedAt:      s.FinishedAt,
			LastError:       s.LastError,
			Library:         counts,
		})
	}
}

// loadLibraryCounts counts media-root files only (commercials aren't
// guide content) and skips files marked missing.
func loadLibraryCounts(ctx context.Context, db *sql.DB) (libraryCounts, error) {
	var c libraryCounts
	err := db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM media_files WHERE root_kind = 'media' AND status != 'missing'),
			(SELECT COUNT(*) FROM media_files WHERE root_kind = 'media' AND status != 'missing' AND duration_seconds > 0),
			(SELECT COUNT(*) FROM identified_items ii JOIN media_files mf ON mf.id = ii.media_file_id
			   WHERE mf.root_kind = 'media' AND mf.status != 'missing'),
			(SELECT COUNT(*) FROM identified_items ii JOIN media_files mf ON mf.id = ii.media_file_id
			   WHERE mf.root_kind = 'media' AND mf.status != 'missing' AND ii.genre_tags IS NOT NULL),
			(SELECT COUNT(DISTINCT channel_id) FROM channel_items)`).
		Scan(&c.FilesFound, &c.FilesProbed, &c.ItemsIdentified, &c.ItemsClassified, &c.ChannelsWithContent)
	return c, err
}
