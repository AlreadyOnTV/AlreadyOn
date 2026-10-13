package httpapi

import (
	"database/sql"
	"net/http"

	"github.com/alreadyonapp/alreadyon/server/internal/playback"
)

func handleGetPlaybackSettings(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s, err := playback.GetSettings(r.Context(), db)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "could not load playback settings")
			return
		}
		writeJSON(w, http.StatusOK, s)
	}
}
func handlePutPlaybackSettings(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var s playback.Settings
		if !decodeStrict(w, r, &s) {
			return
		}
		if err := s.Validate(); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := playback.SaveSettings(r.Context(), db, s); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "could not save playback settings")
			return
		}
		writeJSON(w, http.StatusOK, s)
	}
}
