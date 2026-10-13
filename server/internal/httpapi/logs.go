package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/logging"
)

const (
	defaultLogsLimit = 100
	maxLogsLimit     = 1000
)

type logEntryResponse struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

type logsResponse struct {
	// TimeZone is the server's IANA zone; the viewer shows entries in it.
	TimeZone string             `json:"timeZone"`
	Entries  []logEntryResponse `json:"entries"`
}

// handleLogs serves the most recent in-memory log entries for the admin
// log viewer: GET /api/logs?levels=warn,error&limit=100. Both query
// params are optional; an invalid or missing levels list means "all
// levels", and limit falls back to defaultLogsLimit.
func handleLogs(w http.ResponseWriter, r *http.Request) {
	levels := parseLevels(r.URL.Query().Get("levels"))
	limit := parseLimit(r.URL.Query().Get("limit"))

	entries := logging.Entries(levels, limit)
	resp := logsResponse{TimeZone: time.Local.String(), Entries: make([]logEntryResponse, 0, len(entries))}
	for _, e := range entries {
		resp.Entries = append(resp.Entries, logEntryResponse{
			Time:    e.Time.In(time.Local).Format(time.RFC3339Nano),
			Level:   string(e.Level),
			Message: e.Message,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func parseLevels(raw string) []logging.Level {
	if raw == "" {
		return nil
	}
	var levels []logging.Level
	for _, part := range strings.Split(raw, ",") {
		l := logging.Level(strings.ToLower(strings.TrimSpace(part)))
		if l.IsValid() {
			levels = append(levels, l)
		}
	}
	return levels
}

func parseLimit(raw string) int {
	if raw == "" {
		return defaultLogsLimit
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultLogsLimit
	}
	if n > maxLogsLimit {
		return maxLogsLimit
	}
	return n
}
