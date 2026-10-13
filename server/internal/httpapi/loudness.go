package httpapi

import (
	"database/sql"
	"github.com/alreadyonapp/alreadyon/server/internal/loudness"
	"net/http"
)

func handleLoudness(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			var body struct {
				Enabled *bool `json:"enabled"`
			}
			if !decodeStrict(w, r, &body) {
				return
			}
			if body.Enabled == nil {
				writeJSONError(w, 400, "enabled is required")
				return
			}
			if err := loudness.SetEnabled(r.Context(), db, *body.Enabled); err != nil {
				writeJSONError(w, 500, "could not save loudness setting")
				return
			}
		}
		if r.Method == http.MethodPost {
			if err := loudness.Request(r.Context(), db); err != nil {
				writeJSONError(w, 409, err.Error())
				return
			}
		}
		s, err := loudness.Get(r.Context(), db)
		if err != nil {
			writeJSONError(w, 500, "could not read loudness status")
			return
		}
		writeJSON(w, 200, s)
	}
}
