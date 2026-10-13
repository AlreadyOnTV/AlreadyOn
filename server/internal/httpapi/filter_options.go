// Shared filter endpoints expose only age and era definitions and rebuild affected schedules.
package httpapi

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/lineup"
	"github.com/alreadyonapp/alreadyon/server/internal/refresh"
	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
)

func handleFilterOptions(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		options, err := channel.ListOptions(r.Context(), db)
		if err != nil {
			writeJSONError(w, 500, "could not load filter options")
			return
		}
		writeJSON(w, 200, options)
	}
}
func handleSaveFilterOption(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var option channel.FilterOption
		if r.Method != "DELETE" {
			dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&option); err != nil {
				writeJSONError(w, 400, "invalid filter definition")
				return
			}
			if err := dec.Decode(&struct{}{}); err != io.EOF {
				writeJSONError(w, 400, "invalid filter definition")
				return
			}
		}
		option.ID = r.PathValue("id")
		var err error
		refresh.Exclusive(func() {
			if r.Method == "DELETE" {
				err = channel.DeleteOption(r.Context(), db, r.PathValue("kind"), option.ID)
			} else {
				err = channel.SaveOption(r.Context(), db, r.PathValue("kind"), option)
			}
			if err != nil {
				return
			}
			if _, err = lineup.Refresh(r.Context(), db); err != nil {
				return
			}
			_, err = schedule.RegenerateAll(r.Context(), db, time.Now())
		})
		if err != nil {
			if !writeFilterError(w, err) {
				writeJSONError(w, 500, "filter saved but refresh failed; retry Refresh Schedule")
			}
			return
		}
		writeJSON(w, 200, option)
	}
}
func handleMoveFilterOption(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Direction string `json:"direction"`
			Target    string `json:"target"`
		}
		if !decodeStrict(w, r, &body) {
			return
		}
		var err error
		refresh.Exclusive(func() {
			err = channel.MoveOption(r.Context(), db, r.PathValue("kind"), r.PathValue("id"), body.Direction, body.Target)
		})
		if err != nil {
			if !writeFilterError(w, err) {
				writeJSONError(w, 500, "could not reorder filters")
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleRestoreFilterOrder(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var err error
		refresh.Exclusive(func() { err = channel.RestoreOptionOrder(r.Context(), db, r.PathValue("kind")) })
		if err != nil {
			if !writeFilterError(w, err) {
				writeJSONError(w, 500, "could not restore order")
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
