// Package config loads AlreadyOn's bootstrap configuration: what the
// server needs before it can read its database. Everything an admin can
// change at runtime (content folders, metadata keys, profiles, ...) lives
// in the database instead — see Epic #113.
package config

import (
	"cmp"
	"fmt"
	"os"
	"strconv"
)

// dataDir and webDir are fixed when the binary is built rather than read
// from the environment. The container image sets them at link time (see
// deploy/Dockerfile: -X .../config.dataDir=/config); the values here are
// for running from a checkout, relative to the server/ directory.
var (
	dataDir = "./data"
	webDir  = "../web/dist"
)

// Config holds runtime configuration for the server.
type Config struct {
	// ManagementAddr serves the web UI (built assets), configuration/
	// library/guide/session APIs, admin auth and token-scoped media. This is the port a
	// browser is pointed at. API operations require a bearer API key;
	// full account login remains Epic 5. Deliberately a high,
	// uncommon default (not :8080) to avoid the many other self-hosted
	// homelab apps that claim that port.
	ManagementAddr string

	// WebDir is where the built web UI (web/dist) is served from.
	WebDir string

	// DataDir holds the SQLite database and other persistent local state
	// (uploaded profile icons live in a subfolder).
	DataDir string
}

// Load reads the shared HTTPS port (ALREADYON_WEB_PORT, default 19900).
// ALREADYON_STREAM_PORT is obsolete and ignored; media shares this listener.
func Load() (Config, error) {
	webPort, err := portFromEnv("ALREADYON_WEB_PORT", "19900")
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		ManagementAddr: ":" + webPort,
		WebDir:         webDir,
		DataDir:        dataDir,
	}
	return cfg, nil
}

// portFromEnv returns the named variable (or def when unset) after checking
// it is a valid TCP port number.
func portFromEnv(name, def string) (string, error) {
	v := cmp.Or(os.Getenv(name), def)
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("config: %s must be a port number from 1 to 65535, got %q", name, v)
	}
	return strconv.Itoa(n), nil
}
