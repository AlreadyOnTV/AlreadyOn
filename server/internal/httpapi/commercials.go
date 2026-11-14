package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/alreadyonapp/alreadyon/server/internal/commercial"
	"github.com/alreadyonapp/alreadyon/server/internal/refresh"
)

type commercialSettingsResponse struct {
	VolumeOffsetDB           float64 `json:"volumeOffsetDb"`
	Enabled                  bool    `json:"enabled"`
	Mode                     string  `json:"mode"`
	IntervalMinutes          int     `json:"intervalMinutes"`
	TVCommercialsPerBreak    int     `json:"tvCommercialsPerBreak"`
	MovieCommercialsPerBreak int     `json:"movieCommercialsPerBreak"`
}

func toCommercialSettingsResponse(s commercial.Settings) commercialSettingsResponse {
	return commercialSettingsResponse{
		VolumeOffsetDB:           s.VolumeOffsetDB,
		Enabled:                  s.Enabled,
		Mode:                     string(s.Mode),
		IntervalMinutes:          s.IntervalMinutes,
		TVCommercialsPerBreak:    s.TVCommercialsPerBreak,
		MovieCommercialsPerBreak: s.MovieCommercialsPerBreak,
	}
}

func handleGetCommercialSettings(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s, err := commercial.GetSettings(r.Context(), db)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, toCommercialSettingsResponse(s))
	}
}

// Persist settings and durable work before acknowledging; the worker updates future schedules.
func handlePutCommercialSettings(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body commercialSettingsResponse
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		s := commercial.Settings{
			VolumeOffsetDB:           body.VolumeOffsetDB,
			Enabled:                  body.Enabled,
			Mode:                     commercial.Mode(body.Mode),
			IntervalMinutes:          body.IntervalMinutes,
			TVCommercialsPerBreak:    body.TVCommercialsPerBreak,
			MovieCommercialsPerBreak: body.MovieCommercialsPerBreak,
		}
		if err := refresh.SaveCommercialSettings(r.Context(), db, s); err != nil {
			switch {
			case errors.Is(err, commercial.ErrInvalidVolumeOffset), errors.Is(err, commercial.ErrInvalidMode),
				errors.Is(err, commercial.ErrInvalidIntervalMinutes),
				errors.Is(err, commercial.ErrInvalidTVCommercialsPerBreak),
				errors.Is(err, commercial.ErrInvalidMovieCommercialsPerBreak):
				writeJSONError(w, http.StatusBadRequest, err.Error())
			default:
				writeJSONError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}

		writeJSON(w, http.StatusOK, toCommercialSettingsResponse(s))
	}
}
