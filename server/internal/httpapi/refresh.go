package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/logging"
	"github.com/alreadyonapp/alreadyon/server/internal/profile"
	"github.com/alreadyonapp/alreadyon/server/internal/refresh"
	"github.com/alreadyonapp/alreadyon/server/internal/refreshlog"
)

type refreshScheduleResponse struct {
	Classified           bool `json:"classified"`
	Enriched             bool `json:"enriched"`
	ChannelsRefreshed    int  `json:"channelsRefreshed"`
	SchedulesRegenerated int  `json:"schedulesRegenerated"`
}

// handleRefreshSchedule serves POST /api/admin/refresh-schedule: the
// Channels admin tab's "Refresh Schedule" button (also triggered
// automatically after a commercial-settings save, and by the background
// content-polling loop when new content is detected — see
// internal/refresh.Run, which this just reports over HTTP).
func handleRefreshSchedule(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := refresh.Run(r.Context(), db)
		if errors.Is(err, refresh.ErrBusy) {
			writeJSONError(w, http.StatusConflict, "A library refresh is already in progress; try again when it finishes.")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, refreshScheduleResponse{
			Classified:           result.Classified,
			Enriched:             result.Enriched,
			ChannelsRefreshed:    result.ChannelsRefreshed,
			SchedulesRegenerated: result.SchedulesRegenerated,
		})
	}
}

type refreshMediaResponse struct {
	FilesAdded   int `json:"filesAdded"`
	FilesMissing int `json:"filesMissing"`
}

// handleRefreshMedia serves POST /api/admin/refresh-media: the Media admin
// tab's "Refresh Media" button. It rescans the media folders now (new files
// appear, deleted ones leave the guide) instead of waiting for the polling
// interval. The optional profileId only labels the log entry; it is not an
// authorization check. The scan is not tied to the request, so a browser
// that navigates away doesn't leave it half done.
func handleRefreshMedia(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ProfileID *int64 `json:"profileId"`
		}
		if r.Body != http.NoBody && !decodeStrict(w, r, &body) {
			return
		}
		actor := "an API client"
		if body.ProfileID != nil {
			p, err := profile.Get(r.Context(), db, *body.ProfileID)
			if errors.Is(err, profile.ErrNotFound) {
				writeJSONError(w, http.StatusBadRequest, "profile not found")
				return
			}
			if err != nil {
				writeProfileError(w, err)
				return
			}
			actor = fmt.Sprintf("profile %q (id %d)", p.Name, p.ID)
		}

		logging.Info("manual media refresh requested by %s", actor)
		result, err := refresh.TryRescan(context.WithoutCancel(r.Context()), db)
		if errors.Is(err, refresh.ErrBusy) {
			logging.Info("manual media refresh requested by %s was declined: a library refresh is already in progress", actor)
			writeJSONError(w, http.StatusConflict, "A library refresh is already in progress; try again when it finishes.")
			return
		}
		if err != nil {
			logging.Error("manual media refresh requested by %s failed: %v", actor, err)
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		logging.Info("manual media refresh requested by %s finished: %d new file(s), %d missing file(s), %d channel(s) refreshed",
			actor, result.FilesAdded, result.FilesMissing, result.ChannelsRefreshed)
		writeJSON(w, http.StatusOK, refreshMediaResponse{FilesAdded: result.FilesAdded, FilesMissing: result.FilesMissing})
	}
}

// handleRefreshTimes serves GET /api/admin/refresh-times: when the library was
// last scanned and the schedules last regenerated, by any trigger. A time is null
// when it has never happened.
func handleRefreshTimes(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t, err := refreshlog.Get(r.Context(), db)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		resp := map[string]*string{"mediaRefreshedAt": nil, "scheduleRefreshedAt": nil}
		if !t.Media.IsZero() {
			v := t.Media.UTC().Format(time.RFC3339)
			resp["mediaRefreshedAt"] = &v
		}
		if !t.Schedule.IsZero() {
			v := t.Schedule.UTC().Format(time.RFC3339)
			resp["scheduleRefreshedAt"] = &v
		}
		writeJSON(w, http.StatusOK, resp)
	}
}
