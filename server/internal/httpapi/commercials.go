package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/alreadyonapp/alreadyon/server/internal/commercial"
	"github.com/alreadyonapp/alreadyon/server/internal/logging"
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
		previous, err := commercial.GetSettings(r.Context(), db)
		if err != nil {
			writeJSONError(w, 500, "could not load commercial settings")
			return
		}
		if err := commercial.SaveSettings(r.Context(), db, s); err != nil {
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

		// How/when breaks get inserted just changed, which changes what
		// should already be scheduled, not just what gets scheduled from
		// here on — regenerate now, for any caller of this endpoint, not
		// only the Channels tab's own explicit "Refresh Schedule" button.
		// The setting itself is already safely saved at this point, so a
		// refresh failure here is only logged, not surfaced as this
		// request having failed.
		previous.VolumeOffsetDB = s.VolumeOffsetDB
		if previous != s {
			if _, err := refresh.Run(r.Context(), db); errors.Is(err, refresh.ErrBusy) {
				logging.Warn("commercial settings: post-save refresh skipped (a refresh is already running); use Refresh Schedule once it finishes")
			} else if err != nil {
				logging.Error("commercial settings: post-save refresh: %v", err)
			}
		}
		writeJSON(w, http.StatusOK, toCommercialSettingsResponse(s))
	}
}
