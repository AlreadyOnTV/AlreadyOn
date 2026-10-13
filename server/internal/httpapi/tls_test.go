package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"github.com/alreadyonapp/alreadyon/server/internal/instancetls"
)

// TLS management shares the existing authentication and CSRF boundary; public
// trust material means non-secret, not an unauthenticated bootstrap endpoint.
func TestTLSManagementTrustAndNames(t *testing.T) {
	db := openTestDB(t)
	manager, err := instancetls.Open(t.Context(), db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	RegisterTLS(mux, manager)
	handler := auth.RequireAuth(db, mux)
	key, _, err := auth.GenerateAPIKey(t.Context(), db, testOwnerID(t, db), "isolated TLS test")
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body, bearer, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://localhost"+path, strings.NewReader(body))
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/api/settings/tls", "/api/settings/tls/ca.pem"} {
		if w := request("GET", path, "", "", ""); w.Code != 401 {
			t.Fatalf("unauthenticated %s: %d", path, w.Code)
		}
	}
	before := manager.Status()
	if w := request("POST", "/api/settings/tls/upload", `{}`, "", ""); w.Code != 401 {
		t.Fatal("unauthenticated upload accepted")
	}
	if w := request("POST", "/api/settings/tls/self-signed", `{}`, "", "https://other.example"); w.Code != 403 {
		t.Fatal("cross-site restore accepted")
	}
	if w := request("POST", "/api/settings/tls/upload", `{"certificate":"invalid","privateKey":"invalid","ca":"invalid"}`, key, ""); w.Code != 400 {
		t.Fatal("malformed upload accepted")
	}
	if w := request("POST", "/api/settings/tls/upload", `{} {}`, key, ""); w.Code != 400 {
		t.Fatal("multiple upload objects accepted")
	}
	if w := request("POST", "/api/settings/tls/upload", `{"unknown":"field"}`, key, ""); w.Code != 400 {
		t.Fatal("unknown upload field accepted")
	}
	ca := request("GET", "/api/settings/tls/ca.pem", "", key, "")
	if ca.Code != 200 || !bytes.Equal(ca.Body.Bytes(), manager.TrustPEM()) || strings.Contains(ca.Body.String(), "PRIVATE KEY") {
		t.Fatal("invalid public trust export")
	}
	if w := request("PUT", "/api/settings/tls", `{"names":["https://bad.example"]}`, key, ""); w.Code != 400 {
		t.Fatal("invalid names accepted")
	}
	if w := request("PUT", "/api/settings/tls", `{"names":["tv.example"]}`, "", "https://other.example"); w.Code != 403 {
		t.Fatal("cross-site mutation accepted")
	}
	w := request("PUT", "/api/settings/tls", `{"names":["tv.example"]}`, key, "")
	if w.Code != 200 {
		t.Fatalf("name update: %d", w.Code)
	}
	var after instancetls.Status
	if err := json.Unmarshal(w.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if before.Fingerprint != after.Fingerprint || after.Mode != "self-signed" {
		t.Fatal("name update changed CA identity")
	}
}
