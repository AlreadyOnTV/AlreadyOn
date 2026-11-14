package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/commercial"
	"github.com/alreadyonapp/alreadyon/server/internal/refresh"
)

// Channel overrides return after persistence; the shared worker updates future airings.
func handleChannelCommercials(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeJSONError(w, 400, "invalid channel id")
			return
		}
		if _, err = channel.Get(r.Context(), db, id); err != nil {
			writeChannelError(w, err)
			return
		}
		if r.Method == http.MethodPut {
			var settings commercial.ChannelSettings
			if !decodeStrict(w, r, &settings) {
				return
			}
			err = refresh.MutateSettings(r.Context(), db, func(tx *sql.Tx) (refresh.SettingsWork, error) {
				err := commercial.SaveChannelSettings(r.Context(), tx, id, settings)
				return refresh.SettingsWork{Changed: true, Channels: []int64{id}}, err
			})
			if err != nil {
				if errors.Is(err, commercial.ErrInvalidChannelFolder) {
					writeJSONError(w, 400, err.Error())
				} else {
					writeJSONError(w, 500, "could not save commercial settings")
				}
				return
			}
		}
		policy, err := commercial.GetChannelPolicy(r.Context(), db, id)
		if err != nil {
			writeJSONError(w, 500, "could not load commercial settings")
			return
		}
		writeJSON(w, 200, policy)
	}
}
