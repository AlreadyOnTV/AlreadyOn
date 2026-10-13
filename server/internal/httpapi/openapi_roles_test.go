package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/alreadyonapp/alreadyon/server/internal/auth"
)

var (
	specPathLine   = regexp.MustCompile(`^  (/\S*):\s*$`)
	specMethodLine = regexp.MustCompile(`^    (get|post|put|patch|delete):\s*$`)
	specRolesLine  = regexp.MustCompile(`^      x-alreadyon-roles: \[(.*)\]\s*$`)
	specParam      = regexp.MustCompile(`\{[^}]+\}`)
)

// specOperation is one documented operation and the roles it declares.
type specOperation struct {
	method, path string
	roles        map[string]bool
}

// readSpecOperations reads every operation and its x-alreadyon-roles from the
// OpenAPI document. The spec's fixed two-space layout makes a line scan
// enough, without adding a YAML dependency.
func readSpecOperations(t *testing.T) []specOperation {
	t.Helper()
	data, err := os.ReadFile("../../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var ops []specOperation
	path := ""
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "components:") {
			break
		}
		if m := specPathLine.FindStringSubmatch(line); m != nil {
			path = m[1]
			continue
		}
		if m := specMethodLine.FindStringSubmatch(line); m != nil && path != "" {
			ops = append(ops, specOperation{method: strings.ToUpper(m[1]), path: path})
			continue
		}
		if m := specRolesLine.FindStringSubmatch(line); m != nil && len(ops) > 0 && ops[len(ops)-1].roles == nil {
			roles := map[string]bool{}
			for _, r := range strings.Split(m[1], ",") {
				roles[strings.TrimSpace(r)] = true
			}
			ops[len(ops)-1].roles = roles
		}
	}
	if len(ops) < 50 {
		t.Fatalf("read only %d operations from the spec; has its layout changed?", len(ops))
	}
	return ops
}

// The Swagger document tells client authors which role may call each
// operation. It must match what the server actually enforces: every
// operation is marked, and an operation is marked viewer exactly when
// auth.RoleAllows lets a viewer make it.
func TestOpenAPIRolesMatchEnforcement(t *testing.T) {
	documented := map[string]bool{}
	for _, op := range readSpecOperations(t) {
		name := op.method + " " + op.path
		documented[name] = true
		if op.roles == nil {
			t.Errorf("%s: missing x-alreadyon-roles", name)
			continue
		}
		for r := range op.roles {
			if r != "owner" && r != "viewer" && r != "public" {
				t.Errorf("%s: unknown role %q", name, r)
			}
		}
		if op.roles["public"] {
			if len(op.roles) != 1 {
				t.Errorf("%s: public must be the only role listed", name)
			}
			continue
		}
		if !op.roles["owner"] {
			t.Errorf("%s: owners can call every signed-in operation; list owner", name)
		}
		// Media is authorized by the playback token tune issues, not by role.
		if strings.HasPrefix(op.path, "/media/") {
			if !op.roles["viewer"] {
				t.Errorf("%s: viewers receive playback tokens; list viewer", name)
			}
			continue
		}
		r := httptest.NewRequest(op.method, specParam.ReplaceAllString(op.path, "1"), nil)
		if allowed := auth.RoleAllows(auth.RoleViewer, r); allowed != op.roles["viewer"] {
			t.Errorf("%s: spec says viewer=%v, server enforces viewer=%v", name, op.roles["viewer"], allowed)
		}
	}
	// Every route a viewer may use is documented, so the allowlist cannot
	// grow silently.
	for _, route := range auth.ViewerRoutes() {
		if !documented[route] {
			t.Errorf("viewer route %q is not documented in api/openapi.yaml", route)
		}
	}
}

// The role marking is backed by a real refusal: a viewer calling an owner-only
// operation from the spec gets 403 forbidden.
func TestViewerIsRefusedOwnerOnlyOperations(t *testing.T) {
	db := openTestDB(t)
	testOwnerID(t, db)
	handler := auth.RequireAuth(db, NewRouter(t.TempDir(), t.TempDir(), db))
	owner := newAuthClientForHandler(t, db, handler)
	if rec := owner.do("POST", "/api/auth/login", `{"username":"owner","password":"synthetic-owner-pass"}`); rec.Code != 200 {
		t.Fatalf("owner login: %d %s", rec.Code, rec.Body.String())
	}
	if rec := owner.do("POST", "/api/users", `{"username":"spec-viewer","password":"viewer-pass-1","role":"viewer"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create viewer: %d %s", rec.Code, rec.Body.String())
	}
	viewer := newAuthClientForHandler(t, db, handler)
	if rec := viewer.do("POST", "/api/auth/login", `{"username":"spec-viewer","password":"viewer-pass-1"}`); rec.Code != 200 {
		t.Fatalf("viewer login: %d %s", rec.Code, rec.Body.String())
	}
	for _, op := range readSpecOperations(t) {
		if op.roles["viewer"] || op.roles["public"] || !strings.HasPrefix(op.path, "/api/") {
			continue
		}
		w := viewer.do(op.method, specParam.ReplaceAllString(op.path, "1"), "{}")
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"forbidden"`) {
			t.Errorf("%s %s as viewer: got %d %s, want 403 forbidden", op.method, op.path, w.Code, w.Body.String())
		}
	}
}
