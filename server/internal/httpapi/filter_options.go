// Shared filter endpoints expose only age and era definitions and rebuild affected schedules.
package httpapi

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"

	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/refresh"
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
		err := refresh.MutateSettings(r.Context(), db, func(tx *sql.Tx) (refresh.SettingsWork, error) {
			var err error
			if r.Method == "DELETE" {
				err = channel.DeleteOption(r.Context(), tx, r.PathValue("kind"), option.ID)
			} else {
				err = channel.SaveOption(r.Context(), tx, r.PathValue("kind"), option)
			}
			return refresh.SettingsWork{Changed: true, Rebuild: true}, err
		})
		if err != nil {
			if !writeFilterError(w, err) {
				writeJSONError(w, 500, "could not save filter definition")
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
		err = channel.MoveOption(r.Context(), db, r.PathValue("kind"), r.PathValue("id"), body.Direction, body.Target)
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
		err = channel.RestoreOptionOrder(r.Context(), db, r.PathValue("kind"))
		if err != nil {
			if !writeFilterError(w, err) {
				writeJSONError(w, 500, "could not restore order")
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
