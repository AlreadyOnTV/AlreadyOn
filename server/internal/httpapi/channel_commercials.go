package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/commercial"
	"github.com/alreadyonapp/alreadyon/server/internal/refresh"
	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
)

// handleChannelCommercials reads/replaces only this channel's override and
// rebuilds its future schedule while retaining the currently airing entry.
func handleChannelCommercials(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		refresh.Exclusive(func() {
			id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
			if err != nil {
				writeJSONError(w, 400, "invalid channel id")
				return
			}
			if _, err := channel.Get(r.Context(), db, id); err != nil {
				writeChannelError(w, err)
				return
			}
			if r.Method == http.MethodPut {
				var s commercial.ChannelSettings
				if !decodeStrict(w, r, &s) {
					return
				}
				if err := commercial.SaveChannelSettings(r.Context(), db, id, s); err != nil {
					if errors.Is(err, commercial.ErrInvalidChannelFolder) {
						writeJSONError(w, 400, err.Error())
					} else {
						writeJSONError(w, 500, "could not save commercial settings")
					}
					return
				}
				if err := schedule.Regenerate(r.Context(), db, id, time.Now()); err != nil {
					writeJSONError(w, 500, "settings saved, but schedule refresh failed; retry saving")
					return
				}
			}
			p, err := commercial.GetChannelPolicy(r.Context(), db, id)
			if err != nil {
				writeJSONError(w, 500, "could not load commercial settings")
				return
			}
			writeJSON(w, 200, p)
		})
	}
}
