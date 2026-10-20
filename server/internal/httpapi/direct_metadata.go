package httpapi

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/metadata"
	"github.com/alreadyonapp/alreadyon/server/internal/refresh"
)

// handleTMDbPreview returns provider metadata for the editor without writing it to the library.
func handleTMDbPreview(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid item ID")
			return
		}
		tmdbID, err := strconv.Atoi(r.URL.Query().Get("tmdbId"))
		if err != nil || tmdbID <= 0 {
			writeJSONError(w, http.StatusBadRequest, "TMDb ID must be positive")
			return
		}
		settings, err := metadata.GetSettings(r.Context(), db)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "could not load metadata settings")
			return
		}
		if settings.TMDbAPIKey == "" {
			writeJSONError(w, http.StatusBadRequest, "Configure a TMDb API token in Server Settings first")
			return
		}
		details, err := library.PreviewTMDb(r.Context(), db, metadata.NewClient(settings.TMDbAPIKey), id, tmdbID)
		if err != nil {
			writeItemError(w, err)
			return
		}
		year := 0
		if len(details.Date) >= 4 {
			year, _ = strconv.Atoi(details.Date[:4])
		}
		genres := make([]string, len(details.Tags))
		for i, genre := range details.Tags {
			genres[i] = string(genre)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"tmdbId": tmdbID, "title": details.Title, "year": year,
			"genres": genres, "certification": details.Certification,
		})
	}
}

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
		})
		if err != nil {
			writeItemError(w, err)
			return
		}
		writeJSON(w, 200, map[string]int{"updated": n})
	}
}
