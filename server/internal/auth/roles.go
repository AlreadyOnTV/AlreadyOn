package auth

import "net/http"

// viewerRoutes lists every protected route a viewer account may call. The
// list is an allowlist: any route not matched here, including routes added
// later, is owner-only (fail closed). Routes that act on one profile or
// session still check that it belongs to the caller's own account.
//
// The OpenAPI document marks each operation with x-alreadyon-roles; a test
// keeps that marking and this list identical.
var viewerRoutes = []string{
	// Own TV pairing PIN and sign-in settings.
	"GET /api/auth/tv-pin",
	"PUT /api/auth/tv-pin",
	"DELETE /api/auth/tv-pin",
	// Own sign-in, password and browser sessions.
	"GET /api/auth/status",
	"POST /api/auth/logout",
	"POST /api/auth/password",
	"POST /api/auth/username",
	"GET /api/auth/sessions",
	"DELETE /api/auth/sessions/{id}",
	"POST /api/auth/sessions/revoke",
	// Read-only monitoring is visible to every signed-in household profile.
	"GET /api/server/details",
	"GET /api/server/metrics",
	"GET /api/server/activity",
	// Watching.
	"GET /api/guide",
	"GET /api/status/startup",
	"POST /api/channels/{id}/tune",
	"POST /api/playback/sessions",
	"DELETE /api/playback/sessions",
	"POST /api/playback/probe",
	"POST /api/playback/status",
	"POST /api/playback/activity",
	"DELETE /api/playback/activity",
	// The account's own household profiles.
	"GET /api/profiles",
	"POST /api/profiles",
	"GET /api/profiles/{id}",
	"PATCH /api/profiles/{id}",
	"DELETE /api/profiles/{id}",
	"PUT /api/profiles/{id}/pin",
	"DELETE /api/profiles/{id}/pin",
	"POST /api/profiles/{id}/verify-pin",
	"POST /api/profiles/{id}/icon",
	"GET /profile-icons/{file}",
	// API documentation (read-only; operations stay enforced).
	"GET /api/openapi.yaml",
	"GET /api/docs/",
}

// ViewerRoutes returns a copy of the viewer allowlist for documentation tests.
func ViewerRoutes() []string { return append([]string(nil), viewerRoutes...) }

var viewerMux = func() *http.ServeMux {
	m := http.NewServeMux()
	noop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for _, p := range viewerRoutes {
		m.Handle(p, noop)
	}
	return m
}()

// RoleAllows reports whether an account with role may make request r.
func RoleAllows(role Role, r *http.Request) bool {
	switch role {
	case RoleOwner:
		return true
	case RoleViewer:
		// ServeMux matching gives viewers exactly the router's semantics
		// (methods, wildcards, HEAD for GET); no match yields an empty pattern.
		_, pattern := viewerMux.Handler(r)
		return pattern != ""
	}
	return false
}

// passwordChangeRoutes are all an account that must change its password may
// use until it does: learn its state, change the password, or sign out.
var passwordChangeRoutes = map[string]bool{
	"GET /api/auth/status":    true,
	"POST /api/auth/password": true,
	"POST /api/auth/logout":   true,
}
