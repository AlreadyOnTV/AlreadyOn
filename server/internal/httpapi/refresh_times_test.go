package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
)

func TestRefreshTimes_FillInWhenTheLibraryIsScannedAndSchedulesRegenerated(t *testing.T) {
	db := openTestDB(t)
	router := NewRouter(t.TempDir(), t.TempDir(), db)
	times := func() map[string]*string {
		var got map[string]*string
		rec := doJSON(t, router, http.MethodGet, "/api/admin/refresh-times", "")
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("%d %s %v", rec.Code, rec.Body.String(), err)
		}
		return got
	}
	if got := times(); got["mediaRefreshedAt"] != nil || got["scheduleRefreshedAt"] != nil {
		t.Fatalf("nothing has run yet: %v", got)
	}

	if _, err := (&library.Scanner{DB: db}).Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := times()
	if got["mediaRefreshedAt"] == nil || got["scheduleRefreshedAt"] != nil {
		t.Fatalf("a scan sets only the media time: %v", got)
	}
	if at, err := time.Parse(time.RFC3339, *got["mediaRefreshedAt"]); err != nil || time.Since(at) > time.Minute {
		t.Errorf("media time %v %v", *got["mediaRefreshedAt"], err)
	}

	channelID := insertTestChannelWithContent(t, db, 1000, "Channel")
	if err := schedule.Regenerate(context.Background(), db, channelID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := times(); got["scheduleRefreshedAt"] == nil {
		t.Errorf("a regeneration sets the schedule time: %v", got)
	}
}
