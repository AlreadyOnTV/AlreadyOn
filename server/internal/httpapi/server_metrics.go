package httpapi

import (
	"database/sql"
	"github.com/alreadyonapp/alreadyon/server/internal/metrics"
	"net/http"
	"strconv"
	"time"
)

func handleServerMetrics(db *sql.DB) http.HandlerFunc {
	var historyReader metrics.HistoryReader
	return func(w http.ResponseWriter, r *http.Request) {
		minutes := 15
		if value := r.URL.Query().Get("minutes"); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil {
				writeJSONError(w, 400, "invalid history window")
				return
			}
			minutes = n
		}
		if _, ok := metrics.BucketSeconds(minutes); !ok {
			writeJSONError(w, 400, "supported windows: 15, 30, 60, 120, 240, 720 minutes")
			return
		}
		history, err := historyReader.Read(r.Context(), db, minutes, time.Now())
		if err != nil {
			writeJSONError(w, 500, "could not read metrics")
			return
		}
		writeJSON(w, 200, history)
	}
}
