package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/refresh"
)

type channelResponse struct {
	ID     int64  `json:"id"`
	Number int    `json:"number"`
	Name   string `json:"name"`
	Hidden bool   `json:"hidden"`
	// Visibility is auto, show or hide; Hidden is what the guide does as a result.
	Visibility string `json:"visibility"`
	// ItemCount is how many distinct shows/movies (not episodes) are in
	// this channel's lineup right now — see channel.SeriesCounts.
	ItemCount int `json:"itemCount"`
	// Filter is the rule the channel's lineup is built from, or null for a
	// channel whose lineup is managed by hand.
	Filter *channelFilterJSON `json:"filter"`
}

func toChannelResponse(c channel.Channel, itemCount int) channelResponse {
	return channelResponse{ID: c.ID, Number: c.Number, Name: c.Name, Hidden: c.Hidden, Visibility: c.Visibility, ItemCount: itemCount}
}

type channelRequestBody struct {
	Number int            `json:"number"`
	Name   string         `json:"name"`
	Filter optionalFilter `json:"filter"`
}

func handleListChannels(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		channels, err := channel.List(r.Context(), db)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		counts, err := channel.SeriesCounts(r.Context(), db)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		resp := make([]channelResponse, 0, len(channels))
		filters, err := channel.ListFilters(r.Context(), db)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, c := range channels {
			response := toChannelResponse(c, counts[c.ID])
			if f, ok := filters[c.ID]; ok {
				response.Filter = toFilterJSON(&f)
			}
			resp = append(resp, response)
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func handleCreateChannel(db *sql.DB) http.HandlerFunc { return handleSaveChannel(db, true) }

// Both edits and newly created custom channels return after desired state and
// dirty work commit, without acquiring the scan/refresh lock.
func handleSaveChannel(db *sql.DB, create bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body channelRequestBody
		if !decodeStrict(w, r, &body) {
			return
		}
		var id int64
		if !create {
			var err error
			id, err = strconv.ParseInt(r.PathValue("id"), 10, 64)
			if err != nil {
				writeJSONError(w, 400, "invalid channel id")
				return
			}
		}
		var c channel.Channel
		err := refresh.MutateSettings(r.Context(), db, func(tx *sql.Tx) (refresh.SettingsWork, error) {
			filter, err := validateFilterChange(r.Context(), tx, body.Filter)
			if err != nil {
				return refresh.SettingsWork{}, err
			}
			draft := channel.Draft{Number: body.Number, Name: body.Name}
			if create {
				c, err = channel.Create(r.Context(), tx, draft)
			} else {
				c, err = channel.Update(r.Context(), tx, id, draft)
			}
			if err != nil {
				return refresh.SettingsWork{}, err
			}
			if err = applyFilterChange(r.Context(), tx, c.ID, body.Filter, filter); err != nil {
				return refresh.SettingsWork{}, err
			}
			return refresh.SettingsWork{Changed: body.Filter.Set, Channels: []int64{c.ID}, Rebuild: true}, nil
		})
		if err != nil {
			if !writeFilterError(w, err) {
				writeChannelError(w, err)
			}
			return
		}
		resp, err := channelResponseFor(r.Context(), db, c)
		if err != nil {
			writeChannelError(w, err)
			return
		}
		code := http.StatusOK
		if create {
			code = http.StatusCreated
		}
		writeJSON(w, code, resp)
	}
}

func handleGetChannel(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid channel id")
			return
		}
		c, err := channel.Get(r.Context(), db, id)
		if err != nil {
			writeChannelError(w, err)
			return
		}
		resp, err := channelResponseFor(r.Context(), db, c)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func handleUpdateChannel(db *sql.DB) http.HandlerFunc { return handleSaveChannel(db, false) }

// handleSetChannelVisibility serves PUT /api/channels/{id}/visibility: the
// per-row Auto/Show/Hide button on the Channels admin tab.
func handleSetChannelVisibility(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid channel id")
			return
		}
		var body struct {
			Visibility string `json:"visibility"`
		}
		if !decodeStrict(w, r, &body) {
			return
		}
		if _, err := channel.Get(r.Context(), db, id); err != nil {
			writeChannelError(w, err)
			return
		}
		if err := saveVisibilityUpdate(r.Context(), db, []int64{id}, body.Visibility); err != nil {
			if errors.Is(err, channel.ErrInvalidVisibility) {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		c, err := channel.Get(r.Context(), db, id)
		if err != nil {
			writeChannelError(w, err)
			return
		}
		resp, err := channelResponseFor(r.Context(), db, c)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// handleBulkSetChannelsVisibility serves POST /api/channels/bulk-visibility: the
// Channels admin tab's Hide Selected, Show Selected and Set to Auto
// buttons, applied to whichever channels the admin has checked.
func handleBulkSetChannelsVisibility(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			IDs        []int64 `json:"ids"`
			Visibility string  `json:"visibility"`
		}
		if !decodeStrict(w, r, &body) {
			return
		}
		if err := saveVisibilityUpdate(r.Context(), db, body.IDs, body.Visibility); err != nil {
			if errors.Is(err, channel.ErrInvalidVisibility) {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"updated": len(body.IDs)})
	}
}

func handleDeleteChannel(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeJSONError(w, 400, "invalid channel id")
			return
		}
		err = refresh.MutateSettings(r.Context(), db, func(tx *sql.Tx) (refresh.SettingsWork, error) {
			err := channel.Delete(r.Context(), tx, id)
			return refresh.SettingsWork{Changed: true, Channels: []int64{}}, err
		})
		if err != nil {
			writeChannelError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func writeChannelError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, channel.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, channel.ErrInvalidName), errors.Is(err, channel.ErrInvalidNumber),
		errors.Is(err, channel.ErrDuplicateNumber), errors.Is(err, channel.ErrReservedNumber):
		writeJSONError(w, http.StatusBadRequest, err.Error())
	default:
		writeJSONError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// writeJSONCode is writeJSONError with a stable machine-readable code that
// clients branch on (for example "stream_limit" or "password_locked").
func writeJSONCode(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": message, "code": code})
}

func saveVisibilityUpdate(ctx context.Context, db *sql.DB, ids []int64, visibility string) error {
	return refresh.MutateSettings(ctx, db, func(tx *sql.Tx) (refresh.SettingsWork, error) {
		err := channel.SaveVisibility(ctx, tx, ids, visibility)
		return refresh.SettingsWork{Changed: true, Channels: []int64{}}, err
	})
}
