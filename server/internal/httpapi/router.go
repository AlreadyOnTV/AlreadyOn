// Package httpapi wires HTTP routes to their handlers.
package httpapi

import (
	"context"
	"database/sql"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"

	httpSwagger "github.com/swaggo/http-swagger/v2"

	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"github.com/alreadyonapp/alreadyon/server/internal/logging"
	"github.com/alreadyonapp/alreadyon/server/internal/status"
)

// NewRouter returns the top-level HTTP handler for the management
// listener: health check, API routes, and the built web UI served as
// static files from webDir. If webDir doesn't exist yet (e.g. `npm run
// build` hasn't been run), the API still works but "/" returns 404 —
// that's expected during frontend-only dev via `npm run dev`, which
// proxies to this server instead.
//
// uploadDir holds profile icons separately from replaceable web build output.
func NewRouter(webDir, uploadDir string, db *sql.DB, playbackHandlers ...*PlaybackHTTP) http.Handler {
	cookieName, err := auth.SessionCookieName(context.Background(), db)
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "session configuration unavailable", http.StatusInternalServerError)
		})
	}
	inner := NewRouterWithSessionCookie(webDir, uploadDir, db, cookieName, playbackHandlers...)
	// Handler tests often call the router without RequireAuth. Those bare
	// requests act as owner account 1; requests that went through RequireAuth
	// keep exactly the identity (or anonymity) it established. Production
	// uses NewRouterWithSessionCookie, which never injects anyone.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !auth.Checked(r.Context()) {
			r = r.WithContext(auth.WithUser(r.Context(), auth.User{ID: 1, Username: "owner", Role: auth.RoleOwner}))
		}
		inner.ServeHTTP(w, r)
	})
}

// NewRouterWithSessionCookie builds the router with the cookie name already
// loaded during server startup. NewRouter remains convenient for tests and
// other embedded callers.
func NewRouterWithSessionCookie(webDir, uploadDir string, db *sql.DB, cookieName string, playbackHandlers ...*PlaybackHTTP) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/settings/loudness", handleLoudness(db))
	mux.HandleFunc("PUT /api/settings/loudness", handleLoudness(db))
	mux.HandleFunc("POST /api/settings/loudness/run", handleLoudness(db))
	mux.HandleFunc("GET /api/server/details", handleServerDetails(db))
	mux.HandleFunc("GET /api/server/metrics", handleServerMetrics(db))
	for _, p := range playbackHandlers {
		p.register(mux)
		// Media uses scoped tokens, independently of API/session authentication.
		mux.Handle("/media/", p.MediaHandler())
	}
	mux.HandleFunc("GET /healthz", handleHealth)
	loginLimiter := auth.NewLimiter(time.Now)
	// One shared gate bounds expensive anonymous and authenticated credential
	// operations, including simultaneous guesses before a lockout is recorded.
	credentialSlots := make(chan struct{}, 4)
	credentialHandler := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			select {
			case credentialSlots <- struct{}{}:
				defer func() { <-credentialSlots }()
				next(w, r)
			default:
				w.Header().Set("Retry-After", "1")
				writeJSONError(w, http.StatusTooManyRequests, "authentication is busy; try again shortly")
			}
		}
	}
	mux.HandleFunc("GET /api/tv/server", handleTVServer(db))
	mux.HandleFunc("POST /api/auth/tv-login", credentialHandler(handleTVLogin(db, loginLimiter, cookieName)))
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		handler := handleTVPIN(db)
		if method == "PUT" {
			handler = credentialHandler(handler)
		}
		mux.HandleFunc(method+" /api/auth/tv-pin", handler)
	}
	mux.HandleFunc("GET /api/auth/status", handleAuthStatus(db, cookieName))
	mux.HandleFunc("POST /api/auth/setup", credentialHandler(handleAuthSetup(db, cookieName)))
	mux.HandleFunc("POST /api/auth/login", credentialHandler(handleAuthLogin(db, loginLimiter, cookieName)))
	mux.HandleFunc("POST /api/auth/logout", handleAuthLogout(db, cookieName))
	mux.HandleFunc("GET /api/auth/session-inactivity", handleInactiveSessionPolicy(db))
	mux.HandleFunc("PUT /api/auth/session-inactivity", handleInactiveSessionPolicy(db))
	mux.HandleFunc("GET /api/auth/session-lifetime", handleGetSessionLifetime(db))
	mux.HandleFunc("PUT /api/auth/session-lifetime", handlePutSessionLifetime(db))
	mux.HandleFunc("GET /api/auth/sessions", handleListSessions(db))
	mux.HandleFunc("DELETE /api/auth/sessions/{id}", handleRevokeSession(db, cookieName))
	mux.HandleFunc("POST /api/auth/sessions/revoke", handleRevokeSessions(db, cookieName))
	mux.HandleFunc("POST /api/auth/password", credentialHandler(handleChangePassword(db, loginLimiter)))
	mux.HandleFunc("POST /api/auth/username", credentialHandler(handleChangeUsername(db, loginLimiter)))
	mux.HandleFunc("GET /api/status/startup", handleStartupStatus(db, status.Default))
	mux.HandleFunc("GET /api/logs", handleLogs)
	mux.HandleFunc("GET /api/channels", handleListChannels(db))
	mux.HandleFunc("POST /api/channels", handleCreateChannel(db))
	mux.HandleFunc("GET /api/channels/{id}/commercials", handleChannelCommercials(db))
	mux.HandleFunc("PUT /api/channels/{id}/commercials", handleChannelCommercials(db))
	mux.HandleFunc("GET /api/channels/{id}", handleGetChannel(db))
	mux.HandleFunc("PUT /api/channels/{id}", handleUpdateChannel(db))
	mux.HandleFunc("PUT /api/channels/{id}/visibility", handleSetChannelVisibility(db))
	mux.HandleFunc("DELETE /api/channels/{id}", handleDeleteChannel(db))
	mux.HandleFunc("POST /api/channels/bulk-visibility", handleBulkSetChannelsVisibility(db))
	mux.HandleFunc("POST /api/channel-filter-options/{kind}/{id}/move", handleMoveFilterOption(db))
	mux.HandleFunc("POST /api/channel-filter-options/{kind}/restore-order", handleRestoreFilterOrder(db))
	mux.HandleFunc("GET /api/channel-filter-options", handleFilterOptions(db))
	mux.HandleFunc("PUT /api/channel-filter-options/{kind}/{id}", handleSaveFilterOption(db))
	mux.HandleFunc("DELETE /api/channel-filter-options/{kind}/{id}", handleSaveFilterOption(db))
	mux.HandleFunc("POST /api/channels/filter-preview", handleFilterPreview(db))
	mux.HandleFunc("GET /api/library/genres", handleListGenres)
	mux.HandleFunc("GET /api/library/certifications", handleListCertifications)
	mux.HandleFunc("GET /api/library/catalog", handleLibraryCatalog(db))
	mux.HandleFunc("PATCH /api/library/catalog", handlePatchCatalog(db))
	mux.HandleFunc("GET /api/library/unmatched", handleListUnmatched(db))
	mux.HandleFunc("GET /api/library/items/{id}/tmdb-preview", handleTMDbPreview(db))
	mux.HandleFunc("POST /api/library/items/{id}/tmdb", handleTMDbLookup(db))
	mux.HandleFunc("PATCH /api/library/items/{id}", handlePatchLibraryItem(db))
	mux.HandleFunc("PATCH /api/library/items", handleBulkPatchLibraryItems(db))
	mux.HandleFunc("GET /api/settings/commercials", handleGetCommercialSettings(db))
	mux.HandleFunc("PUT /api/settings/commercials", handlePutCommercialSettings(db))
	mux.HandleFunc("GET /api/settings/metadata", handleGetMetadataSettings(db))
	mux.HandleFunc("PUT /api/settings/metadata", handlePutMetadataSettings(db))
	mux.HandleFunc("GET /api/settings/media", handleGetMediaSettings(db))
	mux.HandleFunc("PUT /api/settings/media", handlePutMediaSettings(db))
	mux.HandleFunc("GET /api/settings/folders/browse", handleBrowseFolders)
	mux.HandleFunc("GET /api/settings/folders", handleGetFolderSettings(db))
	mux.HandleFunc("PUT /api/settings/folders", handlePutFolderSettings(db))
	mux.HandleFunc("GET /api/settings/server", handleGetServerSettings(db))
	mux.HandleFunc("PUT /api/settings/server", handlePutServerSettings(db, func() {
		for _, p := range playbackHandlers {
			p.retune()
		}
	}))
	mux.HandleFunc("GET /api/settings/playback", handleGetPlaybackSettings(db))
	mux.HandleFunc("PUT /api/settings/playback", handlePutPlaybackSettings(db))
	mux.HandleFunc("POST /api/admin/refresh-schedule", handleRefreshSchedule(db))
	mux.HandleFunc("POST /api/admin/refresh-media", handleRefreshMedia(db))
	mux.HandleFunc("GET /api/admin/refresh-times", handleRefreshTimes(db))
	mux.HandleFunc("GET /api/admin/api-keys", handleListAPIKeys(db))
	mux.HandleFunc("POST /api/admin/api-keys", handleCreateAPIKey(db))
	mux.HandleFunc("DELETE /api/admin/api-keys/{id}", handleRevokeAPIKey(db))
	mux.HandleFunc("GET /api/guide", handleGuide(db))
	mux.HandleFunc("GET /api/channels/{id}/preview", handleChannelPreview(db))
	mux.HandleFunc("GET /api/profiles", handleListProfiles(db))
	mux.HandleFunc("POST /api/profiles", handleCreateProfile(db))
	mux.HandleFunc("GET /api/profiles/{id}", handleGetProfile(db))
	mux.HandleFunc("PATCH /api/profiles/{id}", handlePatchProfile(db, uploadDir))
	mux.HandleFunc("DELETE /api/profiles/{id}", handleDeleteProfile(db, uploadDir))
	mux.HandleFunc("PUT /api/profiles/{id}/pin", handleSetProfilePIN(db))
	mux.HandleFunc("DELETE /api/profiles/{id}/pin", handleClearProfilePIN(db))
	mux.HandleFunc("POST /api/profiles/{id}/verify-pin", handleVerifyProfilePIN(db))
	mux.HandleFunc("POST /api/profiles/{id}/icon", handleUploadProfileIcon(db, uploadDir))
	// Account management. auth.RoleAllows keeps these owner-only.
	mux.HandleFunc("GET /api/users", handleListUsers(db))
	mux.HandleFunc("POST /api/users", credentialHandler(handleCreateUser(db)))
	mux.HandleFunc("GET /api/users/{id}", handleGetUser(db))
	mux.HandleFunc("PATCH /api/users/{id}", handlePatchUser(db))
	mux.HandleFunc("DELETE /api/users/{id}", handleDeleteUser(db, uploadDir))
	mux.HandleFunc("PUT /api/users/{id}/password", credentialHandler(handleSetUserPassword(db)))

	// The versioned HTTP API's own spec — every front-end (this bundled
	// web app, and any future Google TV/Apple TV client) is just another
	// client of the same API, so it's worth being able to browse/exercise
	// it directly. Only mounted if the spec file is actually present on
	// disk (see openAPISpecPath), matching webDir's own "warn and skip,
	// don't crash" handling below.
	if specPath := openAPISpecPath; fileExists(specPath) {
		var mediaBase func(*http.Request) (string, error)
		for _, p := range playbackHandlers {
			mediaBase = p.mediaBase
		}
		mux.HandleFunc("GET /api/openapi.yaml", handleOpenAPISpec(specPath, mediaBase))
		// Calls go to the address the docs page was opened from (the spec has no top-level
		// servers) and media calls to that host, so the Servers selector is hidden.
		mux.Handle("/api/docs/", httpSwagger.Handler(
			httpSwagger.URL("/api/openapi.yaml"),
			httpSwagger.AfterScript(`const style = document.createElement('style'); style.textContent = '.swagger-ui .servers { display: none !important; }'; document.head.appendChild(style);`),
		))
	} else {
		logging.Warn("API docs not served: %s not found", specPath)
	}

	// Only profile icons are served from persistent upload storage.
	mux.Handle("/profile-icons/", http.StripPrefix("/profile-icons/", http.FileServer(regularFileFS{http.Dir(uploadDir)})))

	// Unknown API paths must not fall through to the HTML application shell.
	mux.Handle("/api/", http.NotFoundHandler())

	if info, err := os.Stat(webDir); err == nil && info.IsDir() {
		mux.Handle("/", newSPAFileServer(webDir))
	} else {
		logging.Warn("web UI not served: %s not found (run `npm run build` in web/)", webDir)
	}

	return mux
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// newSPAFileServer serves built static assets, but falls back to
// index.html for any request that doesn't match a real file — the web
// app uses client-side routing (react-router), so a bookmark or refresh
// on e.g. /admin/logs has no matching file on disk and must still return
// the app shell, not a 404.
//
// Cache-Control is deliberately different for the two cases: everything
// under /assets/ is a Vite build output with a content hash baked into
// its filename (e.g. index-DLivo7_-.css) — a real content change always
// produces a new URL, so it's safe (and desirable) to let a browser
// cache those forever. index.html (and the SPA-fallback path) has no
// such hash, and it's the one file whose content decides which hashed
// asset URLs get requested next — if a browser caches it, a new deploy
// silently never loads for that browser until a hard refresh. Plain
// http.FileServer sets no Cache-Control at all, leaving that decision to
// each browser's own heuristics, which is exactly the "why is this
// stale" trap this closes.
func newSPAFileServer(webDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if name == "" {
			name = "."
		}
		if !fs.ValidPath(name) || strings.ContainsRune(name, '\\') {
			http.NotFound(w, r)
			return
		}
		// OpenInRoot also prevents symlink escapes and races while resolving
		// the requested file. Do not stat a joined path outside this boundary.
		f, err := os.OpenInRoot(webDir, name)
		if err == nil {
			info, statErr := f.Stat()
			if statErr == nil && info.Mode().IsRegular() {
				defer f.Close()
				w.Header().Set("Cache-Control", "no-cache")
				if strings.HasPrefix(r.URL.Path, "/assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				http.ServeContent(w, r, info.Name(), info.ModTime(), f)
				return
			}
			f.Close()
		}
		f, err = os.OpenInRoot(webDir, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeContent(w, r, "index.html", info.ModTime(), f)
	})
}

// regularFileFS keeps uploaded-icon directories private even after sign-in.
// The generated filenames remain usable, but no directory listing is served.
type regularFileFS struct{ http.FileSystem }

func (fs regularFileFS) Open(name string) (http.File, error) {
	f, err := fs.FileSystem.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, os.ErrNotExist
	}
	return f, nil
}
