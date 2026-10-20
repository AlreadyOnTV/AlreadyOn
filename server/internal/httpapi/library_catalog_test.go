// Validate catalog HTTP responses and reject malformed pagination or bulk edits.
package httpapi

import (
	"encoding/json"
	"fmt"
	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCatalogAPISelectionAndAtomicBulkEdits(t *testing.T) {
	db := openTestDB(t)
	router := NewRouter(t.TempDir(), t.TempDir(), db)
	a := addItem(t, db, "episode", "Pilot", "The Show", 2009, `["scifi"]`)
	addItem(t, db, "episode", "Second", "The Show", 2009, `["scifi"]`)
	movie := addItem(t, db, "movie", "A Film", "", 2010, `["comedy"]`)
	rec := doJSON(t, router, http.MethodGet, "/api/library/catalog?view=shows", "")
	var page library.CatalogPage
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &page) != nil || page.Total != 1 || page.Items[0].Files != 2 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = doJSON(t, router, http.MethodPatch, "/api/library/catalog", fmt.Sprintf(`{"view":"shows","ids":[%d],"audienceOverride":"teen"}`, a))
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = doJSON(t, router, http.MethodGet, fmt.Sprintf("/api/library/catalog?view=episodes&showId=%d&audience=teen", a), "")
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &page) != nil || page.Total != 2 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = doJSON(t, router, http.MethodPatch, "/api/library/catalog", fmt.Sprintf(`{"view":"shows","ids":[%d,%d],"audienceOverride":"adult"}`, a, movie))
	if rec.Code != 400 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	for _, q := range []string{"view=unknown", "limit=0", "offset=-1", "view=episodes", "audience=children"} {
		if rec := doJSON(t, router, http.MethodGet, "/api/library/catalog?"+q, ""); rec.Code != 400 {
			t.Fatal(q, rec.Code, rec.Body.String())
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodPatch} {
		req := httptest.NewRequest(method, "/api/library/catalog", nil)
		if auth.RoleAllows(auth.RoleViewer, req) || !auth.RoleAllows(auth.RoleOwner, req) {
			t.Fatal("catalog authorization", method)
		}
	}
}
