package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/alreadyonapp/alreadyon/server/internal/logging"
)

// RestartHandler answers 202 and then calls restart, which must return
// promptly (the process shuts down and starts again on its own).
func RestartHandler(restart func()) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "restarting"})
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		logging.Info("restart requested by an administrator")
		restart()
	}
}
