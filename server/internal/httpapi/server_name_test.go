package httpapi

import (
	"crypto/tls"
	"encoding/json"
	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"net/http/httptest"
	"testing"
)

// A rejected setup must leave first-run setup available; identity updates are visible without restart.
func TestServerNameSetupAndDiscovery(t *testing.T) {
	db := openTestDB(t)
	router := NewRouter(t.TempDir(), t.TempDir(), db)
	client := newAuthClientForHandler(t, db, router)
	if response := client.do("POST", "/api/auth/setup", `{"username":"owner","password":"synthetic-pass","serverName":"  "}`); response.Code != 400 {
		t.Fatalf("invalid setup: %d %s", response.Code, response.Body.String())
	}
	exists, err := auth.AccountExists(t.Context(), db)
	if err != nil || exists {
		t.Fatalf("invalid setup created an owner: %v %v", exists, err)
	}
	if response := client.do("POST", "/api/auth/setup", `{"username":"owner","password":"synthetic-pass","serverName":"  Living Room TV  "}`); response.Code != 201 {
		t.Fatalf("named setup: %d %s", response.Code, response.Body.String())
	}
	discover := func(want string) {
		t.Helper()
		request := httptest.NewRequest("GET", "/api/tv/server", nil)
		request.RemoteAddr = "192.168.1.5:12345"
		request.TLS = &tls.ConnectionState{}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		var metadata struct {
			Name    string `json:"name"`
			Service string `json:"service"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &metadata); err != nil || response.Code != 200 || metadata.Name != want || metadata.Service != "alreadyon" {
			t.Fatalf("discovery: %d %s %v", response.Code, response.Body.String(), err)
		}
	}
	discover("Living Room TV")
	if response := client.do("PUT", "/api/settings/server", `{"serverName":"Family Library"}`); response.Code != 200 {
		t.Fatalf("rename: %d %s", response.Code, response.Body.String())
	}
	discover("Family Library")
	if response := client.do("POST", "/api/auth/setup", `{"username":"other","password":"synthetic-pass","serverName":"Replaced"}`); response.Code != 409 {
		t.Fatalf("second setup: %d", response.Code)
	}
	discover("Family Library")
}
