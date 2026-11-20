//go:build !e2e

package main

import (
	"github.com/alreadyonapp/alreadyon/server/internal/playback"
	"net/http"
)

// Production never registers synthetic calibration controls.
func registerCapacityFixture(_ *http.ServeMux, _ *playback.Capacity, _ *playback.Tuner, _ string) {}
