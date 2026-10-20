package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func demoNumbers(t *testing.T, router http.Handler) map[int]bool {
	t.Helper()
	got := map[int]bool{}
	for _, c := range listChannelsFor(t, router) {
		if c.Number >= 196 && c.Number <= 199 {
			got[c.Number] = true
		}
	}
	return got
}

// Retired broad demo mixes must not return when real channels are hidden or removed.
func TestDemoChannels_HiddenRealChannelsDoNotCreateFallbackMixes(t *testing.T) {
	db := openTestDB(t)
	router := NewRouter(t.TempDir(), t.TempDir(), db)
	addItem(t, db, "movie", "Movie One", "", 2001, `["comedy"]`)
	addItem(t, db, "movie", "Movie Two", "", 2002, `["comedy"]`)

	// Hide a real channel with content without manufacturing a fallback lineup.
	rec := doJSON(t, router, http.MethodPost, "/api/channels", `{"number":200,"name":"Real","filter":{"audienceTier":"adult","genres":[]}}`)
	id := decodeChannel(t, rec.Body.Bytes()).ID
	doJSON(t, router, http.MethodPut, fmt.Sprintf("/api/channels/%d/visibility", id), `{"visibility":"hide"}`)
	if got := demoNumbers(t, router); len(got) != 0 {
		t.Fatalf("retired demo channels returned while real channels are hidden: %v", got)
	}

	// Showing the real channel must also leave the retired catalog absent.
	doJSON(t, router, http.MethodPut, fmt.Sprintf("/api/channels/%d/visibility", id), `{"visibility":"show"}`)
	if got := demoNumbers(t, router); len(got) != 0 {
		t.Errorf("demo channels = %v after showing a real channel, want none", got)
	}

	// Bulk hide and deletion must obey the same no-fallback policy.
	doJSON(t, router, http.MethodPost, "/api/channels/bulk-visibility", fmt.Sprintf(`{"ids":[%d],"visibility":"hide"}`, id))
	if got := demoNumbers(t, router); len(got) != 0 {
		t.Errorf("retired demo channels returned after bulk hide: %v", got)
	}
	doJSON(t, router, http.MethodDelete, fmt.Sprintf("/api/channels/%d", id), "")
	if got := demoNumbers(t, router); len(got) != 0 {
		t.Errorf("retired demo channels returned after deleting real channel: %v", got)
	}
}

func TestDemoChannels_DeletingADemoChannelDoesNotRecreateIt(t *testing.T) {
	db := openTestDB(t)
	router := NewRouter(t.TempDir(), t.TempDir(), db)
	addItem(t, db, "movie", "Movie One", "", 2001, "")
	addItem(t, db, "movie", "Movie Two", "", 2002, "")
	// Simulate a persisted legacy demo row; synchronization retires it rather
	// than recreating it after explicit deletion.
	if _, err := db.Exec(`INSERT INTO channels(number,name,hidden) VALUES(198,'Legacy Movies',0)`); err != nil {
		t.Fatal(err)
	}
	doJSON(t, router, http.MethodPost, "/api/channels/bulk-visibility", `{"ids":[],"visibility":"auto"}`)
	var hidden bool
	if err := db.QueryRow(`SELECT hidden FROM channels WHERE number=198`).Scan(&hidden); err != nil || !hidden {
		t.Fatalf("legacy demo was not retired: hidden=%v error=%v", hidden, err)
	}
	var demoID int64
	if err := db.QueryRow(`SELECT id FROM channels WHERE number = 198`).Scan(&demoID); err != nil {
		t.Fatalf("demo channel missing: %v", err)
	}
	doJSON(t, router, http.MethodDelete, fmt.Sprintf("/api/channels/%d", demoID), "")
	if demoNumbers(t, router)[198] {
		t.Error("deleting a demo channel recreated it")
	}
}

func listChannelsFor(t *testing.T, router http.Handler) []channelResponse {
	t.Helper()
	rec := doJSON(t, router, http.MethodGet, "/api/channels", "")
	var list []channelResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode channels: %v", err)
	}
	return list
}
