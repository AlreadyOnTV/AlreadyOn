package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/alreadyonapp/alreadyon/server/internal/auth"
)

func TestFolderBrowser(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"TV Shows", "Movies", "commercials"} {
		if err := os.Mkdir(filepath.Join(root, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "movie.mp4"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "Movies"), filepath.Join(root, "linked movies")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "missing"), filepath.Join(root, "broken")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path  string
		code  int
		count int
	}{
		{root, 200, 4}, {filepath.Join(root, "TV Shows"), 200, 0},
		{filepath.Join(root, "missing"), 404, 0}, {filepath.Join(root, "movie.mp4"), 400, 0},
		{"relative/path", 400, 0}, {"/bad\x00path", 400, 0},
	} {
		t.Run(tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handleBrowseFolders(rec, httptest.NewRequest("GET", "/api/settings/folders/browse?path="+url.QueryEscape(tc.path), nil))
			if rec.Code != tc.code {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			if tc.code != 200 {
				return
			}
			var got folderBrowserResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Path != tc.path || got.Parent != filepath.Dir(tc.path) || len(got.Folders) != tc.count {
				t.Fatalf("unexpected listing: %+v", got)
			}
			if got.Folders == nil {
				t.Fatal("empty folder list must be an array")
			}
			if tc.path == root && got.Folders[0].Name != "commercials" {
				t.Fatalf("not sorted: %+v", got.Folders)
			}
		})
	}
}

func TestFolderBrowserRequiresAuthentication(t *testing.T) {
	db := openTestDB(t)
	router := auth.RequireAuth(db, NewRouter(t.TempDir(), t.TempDir(), db))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/settings/folders/browse?path=/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", rec.Code)
	}
}
