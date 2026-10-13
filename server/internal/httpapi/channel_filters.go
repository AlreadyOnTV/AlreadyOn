package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/lineup"
	"github.com/alreadyonapp/alreadyon/server/internal/logging"
	"github.com/alreadyonapp/alreadyon/server/internal/metadata"
	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
)

// channelFilterJSON is a custom channel's filter rule on the wire.
type channelFilterJSON struct {
	channel.FilterExtra
	AudienceTier  string   `json:"audienceTier"`
	Genres        []string `json:"genres"`
	MediaType     string   `json:"mediaType"`
	Era           string   `json:"era"`
	TitleContains string   `json:"titleContains"`
}

func (f channelFilterJSON) toFilter() channel.Filter {
	return channel.Filter{FilterExtra: f.FilterExtra, AudienceTier: f.AudienceTier, Genres: f.Genres, MediaType: f.MediaType, Era: f.Era, TitleContains: f.TitleContains}
}

func toFilterJSON(f *channel.Filter) *channelFilterJSON {
	if f == nil {
		return nil
	}
	genres := f.Genres
	if genres == nil {
		genres = []string{}
	}
	return &channelFilterJSON{FilterExtra: f.FilterExtra, AudienceTier: f.AudienceTier, Genres: genres, MediaType: f.MediaType, Era: f.Era, TitleContains: f.TitleContains}
}

// optionalFilter tells an absent "filter" (leave it alone) from an explicit
// null (remove it) from a value (set it).
type optionalFilter struct {
	Set   bool
	Value *channelFilterJSON
}

func (o *optionalFilter) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Value = nil
		return nil
	}
	var v channelFilterJSON
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	o.Value = &v
	return nil
}

// validateFilterChange checks a requested filter change before anything is
// written, so a bad filter can't leave the rest of an update half applied.
func validateFilterChange(db *sql.DB, o optionalFilter) (*channel.Filter, error) {
	if !o.Set || o.Value == nil {
		return nil, nil
	}
	f, err := o.Value.toFilter().Normalize()
	if err != nil {
		return nil, err
	}
	f, err = channel.Resolve(context.Background(), db, f)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// applyFilterChange saves or clears a channel's filter and, when one is
// set, rebuilds its lineup and regenerates its schedule right away.
func applyFilterChange(ctx context.Context, db *sql.DB, channelID int64, o optionalFilter, f *channel.Filter) error {
	if !o.Set {
		return nil
	}
	if o.Value == nil {
		return channel.ClearFilter(ctx, db, channelID)
	}
	if _, err := channel.SetFilter(ctx, db, channelID, *f); err != nil {
		return err
	}
	if _, err := lineup.BuildFilteredChannel(ctx, db, channelID); err != nil {
		return err
	}
	if err := schedule.Regenerate(ctx, db, channelID, time.Now()); err != nil {
		return err
	}
	resyncDemoChannels(ctx, db)
	return nil
}

// resyncDemoChannels adds or removes the fallback demo channels after an
// action that can change which real channels the guide shows, so they
// don't linger until the next refresh. Failures are logged, not returned:
// the change the admin asked for has already been saved.
func resyncDemoChannels(ctx context.Context, db *sql.DB) {
	if _, err := lineup.SyncDemoChannels(ctx, db); err != nil {
		logging.Warn("demo channels: %v", err)
		return
	}
	if _, err := schedule.RegenerateAll(ctx, db, time.Now()); err != nil {
		logging.Warn("demo channels: regenerate schedules: %v", err)
	}
}

// channelResponseFor builds a channel's full response: item count and filter.
func channelResponseFor(ctx context.Context, db *sql.DB, c channel.Channel) (channelResponse, error) {
	count, err := channel.SeriesCount(ctx, db, c.ID)
	if err != nil {
		return channelResponse{}, err
	}
	f, err := channel.GetFilter(ctx, db, c.ID)
	if err != nil {
		return channelResponse{}, err
	}
	resp := toChannelResponse(c, count)
	resp.Filter = toFilterJSON(f)
	return resp, nil
}

func writeFilterError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, channel.ErrInvalidFilterOptions), errors.Is(err, channel.ErrInvalidAudienceTier), errors.Is(err, channel.ErrInvalidGenre),
		errors.Is(err, channel.ErrInvalidMediaType), errors.Is(err, channel.ErrInvalidEra),
		errors.Is(err, channel.ErrInvalidTitleFilter):
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return true
	}
	return false
}

// handleFilterPreview serves POST /api/channels/filter-preview: how much
// library content a filter would match right now, without saving it.
func handleFilterPreview(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body channelFilterJSON
		if !decodeStrict(w, r, &body) {
			return
		}
		f, err := body.toFilter().Normalize()
		if err != nil {
			writeFilterError(w, err)
			return
		}
		f, err = channel.Resolve(r.Context(), db, f)
		if err != nil {
			writeFilterError(w, err)
			return
		}
		res, err := lineup.PreviewFilter(r.Context(), db, f)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"titles": res.Titles, "items": res.Items})
	}
}

// handleListGenres serves GET /api/library/genres: the genre vocabulary
// content is classified with, for pickers.
func handleListGenres(w http.ResponseWriter, r *http.Request) {
	type genre struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	}
	tags := metadata.Tags()
	out := make([]genre, 0, len(tags))
	for _, t := range tags {
		out = append(out, genre{ID: string(t.ID), Label: t.Label})
	}
	writeJSON(w, http.StatusOK, out)
}
