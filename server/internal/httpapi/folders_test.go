package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alreadyonapp/alreadyon/server/internal/refresh"
)

func foldersRequest(t *testing.T, router http.Handler, method, body string) (int, folderSettingsResponse) {
	t.Helper()
	req := httptest.NewRequest(method, "/api/settings/folders", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var resp folderSettingsResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v (%s)", err, rec.Body.String())
		}
	}
	return rec.Code, resp
}

func TestFolderSettingsAPI_DefaultsThenSetPartialAndReset(t *testing.T) {
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))
	real, missing := t.TempDir(), "/definitely/not/here"

	code, got := foldersRequest(t, router, http.MethodGet, "")
	if code != http.StatusOK || got.TV.Source != "default" || len(got.TV.Paths) != 1 || got.TV.Paths[0].Path != "/data/media/tv_shows" {
		t.Fatalf("defaults: code=%d tv=%+v", code, got.TV)
	}
	if got.Exclusions.Source != "default" || len(got.Exclusions.Patterns) == 0 {
		t.Errorf("exclusions default = %+v", got.Exclusions)
	}

	code, got = foldersRequest(t, router, http.MethodPut, `{"tv":["`+real+`","`+missing+`"],"movies":[]}`)
	if code != http.StatusOK {
		t.Fatalf("PUT status = %d", code)
	}
	if got.TV.Source != "admin" || len(got.TV.Paths) != 2 {
		t.Fatalf("tv after PUT = %+v", got.TV)
	}
	if got.TV.Paths[0].Status != "ok" || got.TV.Paths[1].Status != "notFound" {
		t.Errorf("path statuses = %+v, want ok then notFound", got.TV.Paths)
	}
	if got.Movies.Source != "admin" || len(got.Movies.Paths) != 0 {
		t.Errorf("empty movies list = %+v, want admin-set and empty", got.Movies)
	}
	if got.Commercials.Source != "default" {
		t.Errorf("commercials untouched but source = %s", got.Commercials.Source)
	}

	// Absent fields are unchanged; null resets just that field.
	code, got = foldersRequest(t, router, http.MethodPut, `{"tv":null}`)
	if code != http.StatusOK || got.TV.Source != "default" {
		t.Errorf("after null: code=%d tv source=%s, want default", code, got.TV.Source)
	}
	if got.Movies.Source != "admin" {
		t.Errorf("movies changed by a PUT that didn't mention it: %+v", got.Movies)
	}

	// Persisted, not just echoed.
	_, got = foldersRequest(t, router, http.MethodGet, "")
	if got.Movies.Source != "admin" || got.TV.Source != "default" {
		t.Errorf("GET after PUTs = movies %s / tv %s", got.Movies.Source, got.TV.Source)
	}
}

func signalPending() bool {
	select {
	case <-refresh.FoldersChanged():
		return true
	default:
		return false
	}
}

func TestFolderSettingsAPI_OnlyRealChangesSignalTheWatcher(t *testing.T) {
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))
	signalPending() // drain anything left by other tests

	foldersRequest(t, router, http.MethodPut, `{"tv":["/a/tv"]}`)
	if !signalPending() {
		t.Error("saving different folders didn't signal a rescan")
	}
	foldersRequest(t, router, http.MethodPut, `{"tv":["/a/tv"]}`)
	if signalPending() {
		t.Error("saving identical folders signalled a rescan")
	}
	foldersRequest(t, router, http.MethodPut, `{"tv":null}`)
	if !signalPending() {
		t.Error("resetting to the default didn't signal a rescan")
	}
	foldersRequest(t, router, http.MethodPut, `{"tv":["relative"]}`)
	if signalPending() {
		t.Error("a rejected save signalled a rescan")
	}
}

func TestFolderSettingsAPI_RejectsInvalidInput(t *testing.T) {
	router := NewRouter(t.TempDir(), t.TempDir(), openTestDB(t))
	for name, body := range map[string]string{
		"relative path": `{"tv":["relative/dir"]}`,
		"bad glob":      `{"exclusions":["[oops"]}`,
		"not json":      `nope`,
		"wrong type":    `{"tv":"/data/media/tv_shows"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if code, _ := foldersRequest(t, router, http.MethodPut, body); code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", code)
			}
		})
	}
}

func TestFolderCountsRespectDirectoryBoundaries(t *testing.T) {
	db := openTestDB(t)
	for _, q := range []string{
		`INSERT INTO media_files(id,root_kind,path,size_bytes,modified_unix,first_seen_unix,last_seen_unix,status) VALUES(1,'media','/media/films/a.mkv',1,1,1,1,'ok'),(2,'media','/media/films/nested/b.mkv',1,1,1,1,'ok'),(3,'media','/media/films2/c.mkv',1,1,1,1,'ok'),(4,'commercial','/media/films/ad.mkv',1,1,1,1,'ok'),(5,'commercial','/media/films/missing.mkv',1,1,1,1,'missing')`,
		`INSERT INTO identified_items(media_file_id,media_type,title) VALUES(1,'movie','A'),(2,'episode','B'),(3,'movie','C')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	response := folderSettingsResponse{TV: folderList{Paths: []folderPathStatus{{Path: "/media/films"}}}, Commercials: folderList{Paths: []folderPathStatus{{Path: "/media/films"}}}}
	if err := addFolderCounts(t.Context(), db, &response); err != nil {
		t.Fatal(err)
	}
	if response.Commercials.Paths[0].Count != 1 {
		t.Fatal(response.Commercials.Paths)
	}
	if response.TV.Paths[0].Count != 2 {
		t.Fatal(response.TV.Paths)
	}
}
