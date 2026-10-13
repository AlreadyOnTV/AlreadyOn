package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/eligibility"
	"github.com/alreadyonapp/alreadyon/server/internal/guide"
	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
)

// defaultGuideWindowHours is how far ahead the guide looks (the web UI always
// asks for it) when a caller doesn't say.
const defaultGuideWindowHours = 12

type programResponse struct {
	MediaFileID   int64  `json:"mediaFileId"`
	Kind          string `json:"kind"`
	Title         string `json:"title"`
	SeriesTitle   string `json:"seriesTitle,omitempty"`
	SeasonNumber  int    `json:"seasonNumber,omitempty"`
	EpisodeNumber int    `json:"episodeNumber,omitempty"`
	MediaType     string `json:"mediaType,omitempty"`
	// Overview is the movie's own synopsis, or the episode's own
	// description — omitted when not yet fetched from TMDb.
	Overview string `json:"overview,omitempty"`
	// Year is a movie's release year; ContentRating the movie's or show's
	// certification; AirDate an episode's original air date or a movie's release
	// date (YYYY-MM-DD). Each is omitted when unknown.
	Year          int    `json:"year,omitempty"`
	ContentRating string `json:"contentRating,omitempty"`
	AirDate       string `json:"airDate,omitempty"`
	StartsAt      string `json:"startsAt"`
	EndsAt        string `json:"endsAt"`
	// SourceOffsetSeconds is where within this file playback should
	// start — non-zero only for a program a mid-program commercial
	// break split into multiple segments (commercial.ModeInterval).
	SourceOffsetSeconds float64 `json:"sourceOffsetSeconds,omitempty"`
}

func toProgramResponse(p guide.Program) programResponse {
	return programResponse{
		MediaFileID:         p.MediaFileID,
		Kind:                string(p.Kind),
		Title:               p.Title,
		SeriesTitle:         p.SeriesTitle,
		SeasonNumber:        p.SeasonNumber,
		EpisodeNumber:       p.EpisodeNumber,
		MediaType:           p.MediaType,
		Overview:            p.Overview,
		Year:                p.Year,
		ContentRating:       p.ContentRating,
		AirDate:             p.AirDate,
		StartsAt:            p.StartsAt.Format(time.RFC3339),
		EndsAt:              p.EndsAt.Format(time.RFC3339),
		SourceOffsetSeconds: p.SourceOffsetSeconds,
	}
}

type channelGuideResponse struct {
	ChannelID int64  `json:"channelId"`
	Number    int    `json:"number"`
	Name      string `json:"name"`
	// AudienceTier is the audience the channel's filter is set for
	// (young_child, teen or adult); omitted for a channel without a filter.
	AudienceTier string            `json:"audienceTier,omitempty"`
	Programs     []programResponse `json:"programs"`
}

type previewResponse struct {
	Program       programResponse `json:"program"`
	OffsetSeconds float64         `json:"offsetSeconds"`
}

// parseWindowHours reads an optional ?hours= query parameter, falling
// back to defaultGuideWindowHours for a missing or invalid value rather
// than failing the whole request over a cosmetic input mistake.
func parseWindowHours(r *http.Request) time.Duration {
	raw := r.URL.Query().Get("hours")
	if raw == "" {
		return defaultGuideWindowHours * time.Hour
	}
	hours, err := strconv.ParseFloat(raw, 64)
	if err != nil || hours <= 0 {
		return defaultGuideWindowHours * time.Hour
	}
	return time.Duration(hours * float64(time.Hour))
}

// handleGuide serves GET /api/guide?hours=N&audienceTier=T: every
// channel's programs (and any inserted commercial breaks) from now
// through the requested window — what a program-guide grid renders in
// one call. audienceTier (young_child/teen/adult) is optional and, when
// given, drops any channel whose lineup contains content that tier
// isn't eligible for (see channel.MinimumAudienceTier) — a household-
// level content filter asserted by the calling client's active profile,
// not a security boundary (see internal/eligibility's own doc comment;
// there's no real per-viewer auth yet).
func handleGuide(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// The guide is time-sensitive (which program is "current" shifts
		// underneath the same URL every 30 seconds — see the frontend's
		// poll) — never let a browser or intermediary cache a response.
		w.Header().Set("Cache-Control", "no-store")

		channels, err := channel.List(r.Context(), db)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}

		window := parseWindowHours(r)
		filters, err := channel.ListFilters(r.Context(), db)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		audienceTierRaw := r.URL.Query().Get("audienceTier")
		clock := schedule.SystemClock{}
		resp := make([]channelGuideResponse, 0, len(channels))
		for _, c := range channels {
			// c.Hidden is a persisted, admin-controlled flag (also kept in
			// step with content while Hide Empty mode is on). Channel
			// management (which uses channel.List directly, not this
			// endpoint) still sees every channel regardless.
			if c.Hidden {
				continue
			}
			if audienceTierRaw != "" {
				minTier, err := channel.MinimumAudienceTier(r.Context(), db, c.ID)
				if err != nil {
					writeJSONError(w, http.StatusInternalServerError, err.Error())
					return
				}
				// A channel set up for teens or adults is hidden from a lower tier
				// even when its current lineup happens to hold only tame titles.
				if configured := eligibility.Tier(filters[c.ID].AudienceTier); configured != "" {
					minTier = eligibility.Max(minTier, eligibility.ParseTier(string(configured)))
				}
				if !eligibility.Eligible(eligibility.ParseTier(audienceTierRaw), minTier) {
					continue
				}
			}
			programs, err := guide.Window(r.Context(), db, clock, c.ID, window)
			if err != nil && !errors.Is(err, schedule.ErrEmptyLineup) {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			progResp := make([]programResponse, 0, len(programs))
			for _, p := range programs {
				progResp = append(progResp, toProgramResponse(p))
			}
			resp = append(resp, channelGuideResponse{
				ChannelID:    c.ID,
				Number:       c.Number,
				Name:         c.Name,
				AudienceTier: filters[c.ID].AudienceTier,
				Programs:     progResp,
			})
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// handleChannelPreview serves GET /api/channels/{id}/preview: the single
// program currently airing on one channel, for a live preview bar or
// channel-surfing UI that doesn't need the full guide window.
func handleChannelPreview(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")

		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid channel id")
			return
		}
		if _, err := channel.Get(r.Context(), db, id); err != nil {
			writeChannelError(w, err)
			return
		}

		p, offset, err := guide.Preview(r.Context(), db, schedule.SystemClock{}, id)
		if err != nil {
			if errors.Is(err, schedule.ErrEmptyLineup) {
				writeJSONError(w, http.StatusNotFound, "no content scheduled for this channel")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, previewResponse{
			Program:       toProgramResponse(p),
			OffsetSeconds: offset.Seconds(),
		})
	}
}
