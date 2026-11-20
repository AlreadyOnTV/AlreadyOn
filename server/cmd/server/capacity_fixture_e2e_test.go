//go:build e2e

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"github.com/alreadyonapp/alreadyon/server/internal/playback"
)

// The test-only fixture requires both marked temporary storage and an owner.
func TestCapacityFixtureGuardsAndRestoration(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "alreadyon-playback-e2e.")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	c := &playback.Capacity{}
	tuner := &playback.Tuner{}
	request := func(mux *http.ServeMux, method string, role auth.Role) int {
		t.Helper()
		req := httptest.NewRequest(method, "/api/test/capacity", nil)
		if role != "" {
			req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: 1, Role: role}))
		}
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, req)
		return response.Code
	}
	unmarked := http.NewServeMux()
	registerCapacityFixture(unmarked, c, tuner, filepath.Join(root, "data"))
	if got := request(unmarked, http.MethodPut, auth.RoleOwner); got != 404 {
		t.Fatalf("unmarked fixture: %d", got)
	}
	if err = os.WriteFile(filepath.Join(root, ".synthetic-test"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerCapacityFixture(mux, c, tuner, filepath.Join(root, "data"))
	for _, role := range []auth.Role{"", auth.RoleViewer} {
		if got := request(mux, http.MethodPut, role); got != 403 {
			t.Fatalf("role %q: %d", role, got)
		}
	}
	if got := request(mux, http.MethodPut, auth.RoleOwner); got != 204 || c.Processes() != 2 {
		t.Fatalf("install: %d, capacity %d", got, c.Processes())
	}
	if got := request(mux, http.MethodPut, auth.RoleOwner); got != 409 {
		t.Fatalf("duplicate: %d", got)
	}
	if got := request(mux, http.MethodDelete, auth.RoleOwner); got != 204 || c.Processes() != 0 {
		t.Fatalf("restore: %d, capacity %d", got, c.Processes())
	}
}
