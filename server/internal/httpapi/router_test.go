package httpapi

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db.DB
}

func writeTestWebDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>app shell</html>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log('hi')"), 0o644); err != nil {
		t.Fatalf("write app.js: %v", err)
	}
	return dir
}

func TestNewRouter_ServesRealStaticFile(t *testing.T) {
	router := NewRouter(writeTestWebDir(t), t.TempDir(), openTestDB(t))

	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "console.log('hi')" {
		t.Errorf("body = %q, want the real file contents", rec.Body.String())
	}
}

func TestNewRouter_UnknownClientRoute_FallsBackToIndexHTML(t *testing.T) {
	router := NewRouter(writeTestWebDir(t), t.TempDir(), openTestDB(t))

	// A client-side route with no matching file on disk — e.g. a
	// bookmark or refresh on /admin/logs — must still return the app
	// shell, not a 404, so react-router can take over and render it.
	req := httptest.NewRequest(http.MethodGet, "/admin/logs", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "<html>app shell</html>" {
		t.Errorf("body = %q, want the index.html fallback", rec.Body.String())
	}
}

func TestNewRouter_ServesOpenAPISpecAndSwaggerUI(t *testing.T) {
	// openAPISpecPath's default assumes a CWD of server/ (true for the
	// real binary), not go test's own package-directory CWD — so this
	// points explicitly at the real repo-root api/openapi.yaml (three
	// levels up from this package) rather than faking one with a temp
	// file, so a missing or renamed spec would fail this test too.
	orig := openAPISpecPath
	openAPISpecPath = filepath.Join("..", "..", "..", "api", "openapi.yaml")
	t.Cleanup(func() { openAPISpecPath = orig })
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))

	req := httptest.NewRequest(http.MethodGet, "/api/openapi.yaml", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/openapi.yaml status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("AlreadyOn API")) {
		t.Errorf("GET /api/openapi.yaml body doesn't look like the real spec: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/docs/index.html", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/docs/index.html status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestNewRouter_APIRoutesTakePriorityOverSPAFallback(t *testing.T) {
	router := NewRouter(writeTestWebDir(t), t.TempDir(), openTestDB(t))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var body healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("expected the real health handler's response, got %q", body.Status)
	}
}

// Knowing an upload URL permits file delivery after authentication, but must
// never disclose the directory's filenames or return an index listing.
func TestProfileIconFilesystemRefusesDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "icon.png"), []byte("icon"), 0600); err != nil {
		t.Fatal(err)
	}
	h := http.FileServer(regularFileFS{http.Dir(dir)})
	for _, tc := range []struct {
		path   string
		status int
	}{{"/", 404}, {"/icon.png", 200}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != tc.status {
			t.Fatalf("%s: %d", tc.path, w.Code)
		}
	}
}

// Held request bodies verify that the admission limit spans credential routes
// before hashing. All state belongs to this isolated synthetic router.
type heldCredentialBody struct {
	entered chan struct{}
	release <-chan struct{}
}

func (b *heldCredentialBody) Read([]byte) (int, error) {
	select {
	case b.entered <- struct{}{}:
	default:
	}
	<-b.release
	return 0, io.EOF
}
func TestCredentialWorkAdmissionAndRelease(t *testing.T) {
	router := NewRouter(writeTestWebDir(t), t.TempDir(), openTestDB(t))
	release := make(chan struct{})
	defer close(release)
	entered := make(chan struct{}, 4)
	done := make(chan struct{}, 4)
	for i := 0; i < 4; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			r := httptest.NewRequest("POST", "/api/auth/login", &heldCredentialBody{entered, release})
			router.ServeHTTP(httptest.NewRecorder(), r)
		}()
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("credential request did not enter")
		}
	}
	for _, path := range []string{"/api/auth/setup", "/api/auth/login", "/api/auth/password"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("POST", path, nil))
		if w.Code != 429 || w.Header().Get("Retry-After") == "" {
			t.Fatalf("%s admitted when busy: %d", path, w.Code)
		}
	}
	for i := 0; i < 4; i++ {
		release <- struct{}{}
	}
	for i := 0; i < 4; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("credential request did not release")
		}
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("POST", "/api/auth/login", nil))
	if w.Code != 400 {
		t.Fatalf("released request should reach JSON validation: %d", w.Code)
	}
}
