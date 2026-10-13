package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"github.com/alreadyonapp/alreadyon/server/internal/profile"
)

// TestProfilesAreScopedPerAccount checks that a Viewer only sees and edits
// its own account's profiles, that another account's profile reads as not
// found, and that an Owner can reach other accounts' profiles explicitly.
func TestProfilesAreScopedPerAccount(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ownerID := testOwnerID(t, db)
	viewer, err := auth.CreateUser(ctx, db, auth.NewUser{Username: "viewer", Password: "synthetic-viewer-pass", Role: auth.RoleViewer, MaxStreams: 2})
	if err != nil {
		t.Fatal(err)
	}
	handler := auth.RequireAuth(db, NewRouter(t.TempDir(), t.TempDir(), db))
	ownerProfiles, err := profile.List(ctx, db, ownerID)
	if err != nil || len(ownerProfiles) == 0 {
		t.Fatalf("owner profiles: %v %v", ownerProfiles, err)
	}
	foreign := ownerProfiles[0].ID

	v := newAuthClientForHandler(t, db, handler)
	if rec := v.do("POST", "/api/auth/login", `{"username":"viewer","password":"synthetic-viewer-pass"}`); rec.Code != 200 {
		t.Fatalf("viewer login: %d %s", rec.Code, rec.Body.String())
	}
	rec := v.do("GET", "/api/profiles", "")
	var list []profileResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != 200 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	names := []string{}
	for _, p := range list {
		names = append(names, p.Name)
		if p.ID == foreign {
			t.Fatalf("viewer list contains the owner's profile %d", foreign)
		}
	}
	if fmt.Sprint(names) != "[Admin Teen Kids]" {
		t.Fatalf("viewer profiles = %v, want the default Admin, Teen and Kids", names)
	}

	path := fmt.Sprintf("/api/profiles/%d", foreign)
	for _, attempt := range []struct{ method, path, body string }{
		{"GET", path, ""},
		{"PATCH", path, `{"name":"Taken"}`},
		{"DELETE", path, ""},
		{"PUT", path + "/pin", `{"pin":"1234"}`},
		{"DELETE", path + "/pin", ""},
		{"POST", path + "/verify-pin", `{"pin":"1234"}`},
	} {
		if rec := v.do(attempt.method, attempt.path, attempt.body); rec.Code != 404 {
			t.Fatalf("viewer %s %s = %d %s, want 404", attempt.method, attempt.path, rec.Code, rec.Body.String())
		}
	}
	if rec := v.do("GET", fmt.Sprintf("/api/profiles?userId=%d", ownerID), ""); rec.Code != 403 {
		t.Fatalf("viewer listing another account = %d, want 403", rec.Code)
	}
	// A Viewer can still manage its own profiles, including names an
	// Owner's profile already uses.
	if rec := v.do("POST", "/api/profiles", `{"name":"Guest","role":"viewer","audienceTier":"young_child"}`); rec.Code != 201 {
		t.Fatalf("viewer create: %d %s", rec.Code, rec.Body.String())
	}
	if p, _ := profile.Get(ctx, db, foreign); p.Name == "Taken" {
		t.Fatal("viewer renamed the owner's profile")
	}

	o := newAuthClientForHandler(t, db, handler)
	if rec := o.do("POST", "/api/auth/login", `{"username":"owner","password":"synthetic-owner-pass"}`); rec.Code != 200 {
		t.Fatalf("owner login: %d %s", rec.Code, rec.Body.String())
	}
	var own []profileResponse
	_ = json.Unmarshal(o.do("GET", "/api/profiles", "").Body.Bytes(), &own)
	for _, p := range own {
		if p.Name == "Guest" {
			t.Fatal("owner's own list includes the viewer's profile")
		}
	}
	var theirs []profileResponse
	rec = o.do("GET", fmt.Sprintf("/api/profiles?userId=%d", viewer.ID), "")
	if err := json.Unmarshal(rec.Body.Bytes(), &theirs); err != nil || len(theirs) != 4 {
		t.Fatalf("owner listing viewer profiles: %d %s", rec.Code, rec.Body.String())
	}
	if rec := o.do("GET", fmt.Sprintf("/api/profiles/%d", theirs[0].ID), ""); rec.Code != 200 {
		t.Fatalf("owner get viewer profile = %d", rec.Code)
	}
	if rec := o.do("GET", "/api/profiles?userId=9999", ""); rec.Code != 404 {
		t.Fatalf("owner listing a missing account = %d, want 404", rec.Code)
	}
}

func TestProfileCountIsCappedPerAccount(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ownerID := testOwnerID(t, db)
	existing, err := profile.List(ctx, db, ownerID)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(existing); i < profile.MaxProfilesPerUser; i++ {
		if _, err := profile.Create(ctx, db, ownerID, profile.Draft{Name: fmt.Sprintf("P%d", i), Role: profile.RoleViewer, AudienceTier: "adult"}); err != nil {
			t.Fatal(err)
		}
	}
	router := NewRouter(t.TempDir(), t.TempDir(), db)
	if rec := doJSON(t, router, "POST", "/api/profiles", `{"name":"Overflow","role":"viewer","audienceTier":"adult"}`); rec.Code != 409 {
		t.Fatalf("create past the cap = %d %s, want 409", rec.Code, rec.Body.String())
	}
}
