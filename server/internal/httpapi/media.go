package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/alreadyonapp/alreadyon/server/internal/library"
)

type mediaSettingsResponse struct {
	PollIntervalMinutes int `json:"pollIntervalMinutes"`
}

func toMediaSettingsResponse(s library.PollSettings) mediaSettingsResponse {
	return mediaSettingsResponse{PollIntervalMinutes: s.IntervalMinutes}
}

func handleGetMediaSettings(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s, err := library.GetPollSettings(r.Context(), db)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, toMediaSettingsResponse(s))
	}
}

func handlePutMediaSettings(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body mediaSettingsResponse
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		s := library.PollSettings{IntervalMinutes: body.PollIntervalMinutes}
		if err := library.SavePollSettings(r.Context(), db, s); err != nil {
			if errors.Is(err, library.ErrInvalidPollInterval) {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, toMediaSettingsResponse(s))
	}
}
