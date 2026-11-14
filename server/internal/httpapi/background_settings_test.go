package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/refresh"
)

// Holding the processing lock must never hold up a settings persistence request.
func TestSettingsSavesAcknowledgeWhileBackgroundProcessingIsBusy(t *testing.T) {
	db := openTestDB(t)
	router := NewRouter(t.TempDir(), t.TempDir(), db)
	var id int64
	if err := db.QueryRow(`INSERT INTO channels(number,name) VALUES(250,'Synthetic') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	refresh.Exclusive(func() {
		for _, request := range []struct {
			method, path, body string
			code               int
		}{
			{"PUT", fmt.Sprintf("/api/channels/%d", id), `{"number":250,"name":"Latest Name","filter":` + potterFilter + `}`, 200},
			{"PUT", fmt.Sprintf("/api/channels/%d/commercials", id), `{"enabled":false,"folders":[]}`, 200},
			{"PUT", "/api/channel-filter-options/age/async-fixture", `{"id":"async-fixture","name":"Async Fixture","ratings":["PG"]}`, 200},
			{"POST", "/api/channels", `{"number":251,"name":"New Channel","filter":` + potterFilter + `}`, 201},
			{"PUT", fmt.Sprintf("/api/channels/%d/visibility", id), `{"visibility":"hide"}`, 200},
			{"DELETE", "/api/channel-filter-options/age/async-fixture", "", 200},
			{"PUT", "/api/settings/metadata", `{"tmdbApiKey":""}`, 200},
		} {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			req := httptest.NewRequest(request.method, request.path, strings.NewReader(request.body)).WithContext(ctx)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			cancel()
			if response.Code != request.code {
				t.Fatalf("%s %s blocked/failed: %d %s", request.method, request.path, response.Code, response.Body.String())
			}
		}
	})
	var lineups, schedules int
	if err := db.QueryRow(`SELECT count(*) FROM channel_items`).Scan(&lineups); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM schedule_entries`).Scan(&schedules); err != nil {
		t.Fatal(err)
	}
	if lineups != 0 || schedules != 0 {
		t.Fatalf("request performed derived work: lineups=%d schedules=%d", lineups, schedules)
	}
	state, err := refresh.GetUpdateStatus(t.Context(), db)
	if err != nil || state.State != "queued" {
		t.Fatalf("missing pending work: %+v %v", state, err)
	}
}

func TestChannelCreateQueueFailureRollsBackChannelAndFilter(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`CREATE TRIGGER reject_new_channel_work BEFORE INSERT ON library_update_channels BEGIN SELECT RAISE(ABORT,'synthetic queue failure'); END`); err != nil {
		t.Fatal(err)
	}
	router := NewRouter(t.TempDir(), t.TempDir(), db)
	response := doJSON(t, router, http.MethodPost, "/api/channels", `{"number":250,"name":"Synthetic","filter":`+potterFilter+`}`)
	if response.Code != 500 {
		t.Fatalf("unexpected status %d", response.Code)
	}
	var channels, filters int
	db.QueryRow(`SELECT count(*) FROM channels`).Scan(&channels)
	db.QueryRow(`SELECT count(*) FROM channel_filters`).Scan(&filters)
	if channels != 0 || filters != 0 {
		t.Fatalf("partial save: channels=%d filters=%d", channels, filters)
	}
}
