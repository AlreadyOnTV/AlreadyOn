package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/logging"
)

func TestLogsReportTheServerTimeZone(t *testing.T) {
	saved := time.Local
	t.Cleanup(func() { time.Local = saved })
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no zone data")
	}
	time.Local = loc
	logging.Info("time zone test entry")
	rec := httptest.NewRecorder()
	handleLogs(rec, httptest.NewRequest(http.MethodGet, "/api/logs?limit=5", nil))
	var got struct {
		TimeZone string `json:"timeZone"`
		Entries  []struct{ Time string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.TimeZone != "America/New_York" || len(got.Entries) == 0 {
		t.Fatalf("%d %s %v", rec.Code, rec.Body.String(), err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, got.Entries[len(got.Entries)-1].Time)
	if err != nil {
		t.Fatal(err)
	}
	if _, offset := parsed.Zone(); offset != -4*3600 && offset != -5*3600 {
		t.Fatalf("entry time is not in the server zone: %s", got.Entries[len(got.Entries)-1].Time)
	}
}
