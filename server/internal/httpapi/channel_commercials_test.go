package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/commercial"
	"github.com/alreadyonapp/alreadyon/server/internal/library"
)

// Validate authorization, strict input, persistence, reset and channel isolation
// through the real HTTP handlers rather than only the storage helpers.
func TestChannelCommercialsAPI(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	c, err := channel.Create(ctx, db, channel.Draft{Number: 1000, Name: "Commercial test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := library.SaveFolderSettings(ctx, db, library.FolderSettings{Commercial: []string{"/ads/one", "/ads/two"}}); err != nil {
		t.Fatal(err)
	}
	router := NewRouter(t.TempDir(), t.TempDir(), db)
	path := fmt.Sprintf("/api/channels/%d/commercials", c.ID)
	call := func(method, body string, status int) commercial.ChannelPolicy {
		t.Helper()
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		if w.Code != status {
			t.Fatalf("%s got %d want %d: %s", method, w.Code, status, w.Body.String())
		}
		var p commercial.ChannelPolicy
		if status == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	if p := call("GET", "", 200); p.Enabled != nil || len(p.Folders) != 0 {
		t.Fatalf("not inherited %+v", p)
	}
	p := call("PUT", `{"enabled":false,"folders":["/ads/two","/ads/two"]}`, 200)
	if p.Enabled == nil || *p.Enabled || p.EffectiveEnabled || len(p.Folders) != 1 {
		t.Fatalf("bad override %+v", p)
	}
	call("PUT", `{"enabled":true,"folders":["/unconfigured"]}`, 400)
	call("PUT", `{"enabled":true,"folders":[],"unexpected":true}`, 400)
	if p := call("GET", "", 200); p.EffectiveEnabled {
		t.Fatal("invalid request changed settings")
	}
	if p := call("PUT", `{"enabled":null,"folders":[]}`, 200); p.Enabled != nil || p.EffectiveEnabled != p.GlobalEnabled {
		t.Fatal("reset did not inherit")
	}
	// Explicit empty selections must persist as no commercials, without
	// switching off the channel or changing legacy empty-list inheritance.
	p = call("PUT", `{"enabled":true,"folders":[],"explicitFolders":true}`, 200)
	if !p.ExplicitFolders || !p.EffectiveEnabled || len(p.EffectiveFolders) != 0 || p.IncludesPath("/ads/one/ad.mp4") {
		t.Fatalf("explicit empty selection was not honored: %+v", p)
	}
	if p := call("GET", "", 200); !p.ExplicitFolders || len(p.EffectiveFolders) != 0 {
		t.Fatalf("explicit empty selection did not persist: %+v", p)
	}
	p = call("PUT", `{"enabled":null,"folders":["/ads/two"],"explicitFolders":true}`, 200)
	if !p.IncludesPath("/ads/two/ad.mp4") || p.IncludesPath("/ads/one/ad.mp4") {
		t.Fatalf("explicit selection did not restrict commercials: %+v", p)
	}
	p = call("PUT", `{"enabled":null,"folders":[]}`, 200)
	if p.ExplicitFolders || len(p.EffectiveFolders) != 2 || !p.IncludesPath("/ads/one/ad.mp4") {
		t.Fatalf("legacy all-folders selection changed: %+v", p)
	}
	secured := auth.RequireAuth(db, router)
	for _, method := range []string{"GET", "PUT"} {
		w := httptest.NewRecorder()
		secured.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(`{"enabled":false}`)))
		if w.Code != 401 {
			t.Fatalf("unauthenticated %s=%d", method, w.Code)
		}
	}
	path = "/api/channels/999999/commercials"
	call("PUT", `{"enabled":false,"folders":[]}`, 404)
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM channel_commercial_settings WHERE channel_id=999999`).Scan(&count); err != nil || count != 0 {
		t.Fatal("unknown channel was written")
	}
	if err := channel.Delete(ctx, db, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM channel_commercial_settings`).Scan(&count); err != nil || count != 0 {
		t.Fatal("orphan settings survived deletion")
	}
}
