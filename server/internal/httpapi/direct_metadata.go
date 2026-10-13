package httpapi

import (
	"database/sql"
	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/lineup"
	"github.com/alreadyonapp/alreadyon/server/internal/metadata"
	"github.com/alreadyonapp/alreadyon/server/internal/refresh"
	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
	"net/http"
	"strconv"
	"time"
)

func handleTMDbLookup(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeJSONError(w, 400, "invalid item ID")
			return
		}
		var body struct {
			TMDbID int `json:"tmdbId"`
		}
		if !decodeStrict(w, r, &body) {
			return
		}
		settings, err := metadata.GetSettings(r.Context(), db)
		if err != nil {
			writeJSONError(w, 500, "could not load metadata settings")
			return
		}
		if settings.TMDbAPIKey == "" {
			writeJSONError(w, 400, "Configure a TMDb API token in Server Settings first")
			return
		}
		var n int
		refresh.Exclusive(func() {
			n, err = library.LookupTMDb(r.Context(), db, metadata.NewClient(settings.TMDbAPIKey), id, body.TMDbID)
			if err != nil {
				return
			}
			if _, err = lineup.Refresh(r.Context(), db); err != nil {
				return
			}
			_, err = schedule.RegenerateAll(r.Context(), db, time.Now())
		})
		if err != nil {
			writeItemError(w, err)
			return
		}
		writeJSON(w, 200, map[string]int{"updated": n})
	}
}
