//go:build e2e

package main

import (
	"path/filepath"

	"github.com/alreadyonapp/alreadyon/server/internal/config"
	"github.com/alreadyonapp/alreadyon/server/internal/library"
)

// Synthetic browser fixtures explicitly seed schedules and run real startup
// calibration. Wall-clock jobs must not replace those fixtures mid-scenario.
const periodicJobsEnabled = false

// Startup must not scan mounted personal media before the harness seeds its roots.
func init() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}
	root := filepath.Join(cfg.DataDir, "default-media")
	library.DefaultTVPaths = []string{filepath.Join(root, "tv_shows")}
	library.DefaultMoviePaths = []string{filepath.Join(root, "movies")}
	library.DefaultCommercialPaths = []string{filepath.Join(root, "commercials")}
}
