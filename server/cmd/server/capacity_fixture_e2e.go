//go:build e2e

package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"github.com/alreadyonapp/alreadyon/server/internal/playback"
)

// Only the marked disposable browser harness can install a scoped result. This
// keeps ladder rendering assertions independent of the CI runner's measured
// capacity without relaxing production admission or faking FFmpeg playback.
func registerCapacityFixture(mux *http.ServeMux, capacity *playback.Capacity, tuner *playback.Tuner, dataDir string) {
	root := filepath.Dir(dataDir)
	if filepath.Dir(root) != "/tmp" || !strings.HasPrefix(filepath.Base(root), "alreadyon-playback-e2e.") {
		return
	}
	if info, err := os.Lstat(filepath.Join(root, ".synthetic-test")); err != nil || !info.Mode().IsRegular() {
		return
	}
	var mu sync.Mutex
	var restore func()
	handler := func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok || !user.IsOwner() {
			http.Error(w, "owner required", http.StatusForbidden)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPut {
			if restore != nil {
				http.Error(w, "fixture already active", http.StatusConflict)
				return
			}
			restore = capacity.FixtureCapacity(tuner.Threads())
		} else if restore != nil {
			restore()
			restore = nil
		}
		w.WriteHeader(http.StatusNoContent)
	}
	mux.HandleFunc("PUT /api/test/capacity", handler)
	mux.HandleFunc("DELETE /api/test/capacity", handler)
}
