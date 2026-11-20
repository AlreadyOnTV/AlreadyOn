//go:build !e2e

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Production must not expose synthetic calibration controls, even to owners.
func TestProductionExcludesCapacityFixture(t *testing.T) {
	mux := http.NewServeMux()
	registerCapacityFixture(mux, nil, nil, t.TempDir())
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/test/capacity", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("fixture route available: %d", response.Code)
	}
}
