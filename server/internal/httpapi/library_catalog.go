// Catalog queries validate pagination and show identity before reading library metadata.
package httpapi

import (
	"database/sql"
	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"net/http"
	"strconv"
)

func handleLibraryCatalog(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v := r.URL.Query()
		q := library.CatalogQuery{View: v.Get("view"), Search: v.Get("search"), Match: v.Get("match"), Audience: v.Get("audience"), Genre: v.Get("genre"), Rating: v.Get("rating"), Source: v.Get("source"), Sort: v.Get("sort"), Limit: 50}
		if q.View == "" {
			q.View = "movies"
		}
		for key, dst := range map[string]*int{"limit": &q.Limit, "offset": &q.Offset} {
			if raw := v.Get(key); raw != "" {
				n, err := strconv.Atoi(raw)
				if err != nil {
					writeJSONError(w, 400, "invalid "+key)
					return
				}
				*dst = n
			}
		}
		if q.View == "episodes" {
			id, err := strconv.ParseInt(v.Get("showId"), 10, 64)
			if err != nil || id < 1 {
				writeJSONError(w, 400, "invalid showId")
				return
			}
			q.ShowID = id
		}
		page, err := library.Catalog(r.Context(), db, q)
		if err != nil {
			writeItemError(w, err)
			return
		}
		writeJSON(w, 200, page)
	}
}
func handlePatchCatalog(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			IDs  []int64 `json:"ids"`
			View string  `json:"view"`
			library.ItemPatch
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBulkBodyBytes)
		if !decodeStrict(w, r, &body) {
			return
		}
		n, err := library.UpdateCatalog(r.Context(), db, body.IDs, body.View, body.ItemPatch)
		if err != nil {
			writeItemError(w, err)
			return
		}
		writeJSON(w, 200, map[string]int{"updated": n})
	}
}
