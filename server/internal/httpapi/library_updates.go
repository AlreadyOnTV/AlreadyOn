package httpapi

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/alreadyonapp/alreadyon/server/internal/refresh"
)

// Counts reflect saved path selections even while their background application is pending.
func handleLibraryPathCounts(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		counts, err := refresh.GetPathChannelCounts(r.Context(), db, r.URL.Query().Get("kind"), r.URL.Query().Get("path"))
		if err != nil {
			if errors.Is(err, refresh.ErrInvalidPathSelection) {
				writeJSONError(w, 400, err.Error())
			} else {
				writeJSONError(w, 500, "could not count selected channels")
			}
			return
		}
		writeJSON(w, http.StatusOK, counts)
	}
}

// Bulk preferences save immediately; only the owned worker derives lineups/schedules.
func handleCustomizeLibraryPath(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Kind   string `json:"kind"`
			Path   string `json:"path"`
			Movies *bool  `json:"movies"`
			TV     *bool  `json:"tv"`
			Custom *bool  `json:"custom"`
		}
		if !decodeStrict(w, r, &body) {
			return
		}
		if body.Movies == nil || body.TV == nil || body.Custom == nil {
			writeJSONError(w, 400, "all channel group choices are required")
			return
		}
		state, err := refresh.SavePathChoices(r.Context(), db, refresh.PathChoices{Kind: body.Kind, Path: body.Path, Movies: *body.Movies, TV: *body.TV, Custom: *body.Custom})
		if err != nil {
			if errors.Is(err, refresh.ErrInvalidPathSelection) {
				writeJSONError(w, 400, err.Error())
			} else {
				writeJSONError(w, 500, "could not save path preferences")
			}
			return
		}
		writeJSON(w, http.StatusAccepted, state)
	}
}
func handleLibraryUpdates(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		state, err := refresh.GetUpdateStatus(r.Context(), db)
		if r.Method == http.MethodPost {
			state, err = refresh.RetryUpdates(r.Context(), db)
		}
		if err != nil {
			writeJSONError(w, 500, "could not load library update status")
			return
		}
		code := http.StatusOK
		if r.Method == http.MethodPost {
			code = http.StatusAccepted
		}
		writeJSON(w, code, state)
	}
}
