package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/alreadyonapp/alreadyon/server/internal/eligibility"
	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/lineup"
)

const (
	defaultUnmatchedLimit = 50
	maxUnmatchedLimit     = 200
	maxBulkItems          = 1000
	maxBulkBodyBytes      = 64 << 10
)

type unmatchedItemResponse struct {
	TMDbID        int      `json:"tmdbId"`
	ID            int64    `json:"id"`
	Path          string   `json:"path"`
	MediaType     string   `json:"mediaType"`
	Title         string   `json:"title"`
	SeriesTitle   *string  `json:"seriesTitle"`
	Season        *int     `json:"season"`
	Episode       *int     `json:"episode"`
	Year          *int     `json:"year"`
	Genres        []string `json:"genres"`
	Certification *string  `json:"certification"`
	IsManual      bool     `json:"isManual"`
	Reason        string   `json:"reason"`
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func optInt(n int) *int {
	if n == 0 {
		return nil
	}
	return &n
}

// handleListUnmatched serves GET /api/library/unmatched.
func handleListUnmatched(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, offset := defaultUnmatchedLimit, 0
		if v := r.URL.Query().Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > maxUnmatchedLimit {
				writeJSONError(w, http.StatusBadRequest, "limit must be 1-"+strconv.Itoa(maxUnmatchedLimit))
				return
			}
			limit = n
		}
		if v := r.URL.Query().Get("offset"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				writeJSONError(w, http.StatusBadRequest, "offset must be 0 or more")
				return
			}
			offset = n
		}
		page, err := lineup.Unmatched(r.Context(), db, limit, offset)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		items := make([]unmatchedItemResponse, 0, len(page.Items))
		for _, it := range page.Items {
			items = append(items, unmatchedItemResponse{
				TMDbID: it.TMDbID, ID: it.ID, Path: it.Path, MediaType: it.MediaType, Title: it.Title,
				SeriesTitle: optString(it.SeriesTitle), Season: optInt(it.Season), Episode: optInt(it.Episode),
				Year: optInt(it.Year), Genres: it.Genres, Certification: optString(it.Certification),
				IsManual: it.IsManual, Reason: it.Reason,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"total": page.Total, "items": items})
	}
}

func writeItemError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, library.ErrItemNotFound):
		writeJSONError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, library.ErrInvalidItem):
		writeJSONError(w, http.StatusBadRequest, err.Error())
	default:
		writeJSONError(w, http.StatusInternalServerError, "internal error")
	}
}

// handlePatchLibraryItem serves PATCH /api/library/items/{id}.
func handlePatchLibraryItem(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid item id")
			return
		}
		var patch library.ItemPatch
		if !decodeStrict(w, r, &patch) {
			return
		}
		n, err := library.UpdateItems(r.Context(), db, []int64{id}, patch)
		if err != nil {
			writeItemError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"updated": n})
	}
}

// handleBulkPatchLibraryItems serves PATCH /api/library/items: the same
// field values applied to every listed item, all or nothing.
func handleBulkPatchLibraryItems(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			IDs []int64 `json:"ids"`
			library.ItemPatch
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBulkBodyBytes))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if len(body.IDs) > maxBulkItems {
			writeJSONError(w, http.StatusBadRequest, "too many items: at most "+strconv.Itoa(maxBulkItems)+" per request")
			return
		}
		n, err := library.UpdateItems(r.Context(), db, body.IDs, body.ItemPatch)
		if err != nil {
			writeItemError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"updated": n})
	}
}

// handleListCertifications serves GET /api/library/certifications: the
// ratings that can be assigned by hand, per media type.
func handleListCertifications(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string][]string{
		"movie": eligibility.MovieCertifications,
		"tv":    eligibility.TVCertifications,
	})
}
