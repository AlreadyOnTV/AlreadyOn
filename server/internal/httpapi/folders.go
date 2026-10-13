package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/alreadyonapp/alreadyon/server/internal/config"
	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/refresh"
)

type folderPathStatus struct {
	Count int    `json:"count"`
	Path  string `json:"path"`
	// Status is "ok", or why the path can't be scanned: notAbsolute,
	// notFound, notDirectory, unreadable.
	Status string `json:"status"`
}

type folderList struct {
	Paths   []folderPathStatus   `json:"paths"`
	Default []string             `json:"default"`
	Source  library.FolderSource `json:"source"`
}

type patternList struct {
	Patterns []string             `json:"patterns"`
	Default  []string             `json:"default"`
	Source   library.FolderSource `json:"source"`
}

type folderSettingsResponse struct {
	TV          folderList  `json:"tv"`
	Movies      folderList  `json:"movies"`
	Commercials folderList  `json:"commercials"`
	Exclusions  patternList `json:"exclusions"`
}

func toFolderList(kind config.RootKind, l library.EffectiveList) folderList {
	paths := make([]folderPathStatus, 0, len(l.Values))
	for _, p := range l.Values {
		status := "ok"
		if _, err := config.ValidateRoot(kind, p); err != nil {
			status = "unreadable"
			var re *config.RootError
			if errors.As(err, &re) {
				status = string(re.Problem)
			}
		}
		paths = append(paths, folderPathStatus{Path: p, Status: status})
	}
	return folderList{Paths: paths, Default: l.Default, Source: l.Source}
}

func toFolderSettingsResponse(e library.EffectiveFolders) folderSettingsResponse {
	return folderSettingsResponse{
		TV:          toFolderList(config.RootKindMedia, e.TV),
		Movies:      toFolderList(config.RootKindMedia, e.Movie),
		Commercials: toFolderList(config.RootKindCommercial, e.Commercial),
		Exclusions:  patternList{Patterns: e.Exclusions.Values, Default: e.Exclusions.Default, Source: e.Exclusions.Source},
	}
}

// optionalList distinguishes a JSON field that's absent (leave the
// setting alone) from null (reset to the built-in default) and an array
// (save it, even if empty).
type optionalList struct {
	Set   bool
	Value []string
}

func (o *optionalList) UnmarshalJSON(b []byte) error {
	o.Set = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		o.Value = nil
		return nil
	}
	o.Value = []string{}
	return json.Unmarshal(b, &o.Value)
}

type putFolderSettingsRequest struct {
	TV          optionalList `json:"tv"`
	Movies      optionalList `json:"movies"`
	Commercials optionalList `json:"commercials"`
	Exclusions  optionalList `json:"exclusions"`
}

func handleGetFolderSettings(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s, err := library.GetFolderSettings(r.Context(), db)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		response := toFolderSettingsResponse(s.Effective())
		if err := addFolderCounts(r.Context(), db, &response); err != nil {
			writeJSONError(w, 500, "could not count media")
			return
		}
		writeJSON(w, http.StatusOK, response)
	}
}

// handlePutFolderSettings serves PUT /api/settings/folders. Each field is
// optional: an array saves that list, null resets it to the default, and
// an absent field is left as it is.
func handlePutFolderSettings(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body putFolderSettingsRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		current, err := library.GetFolderSettings(r.Context(), db)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		before := current
		if body.TV.Set {
			current.TV = body.TV.Value
		}
		if body.Movies.Set {
			current.Movie = body.Movies.Value
		}
		if body.Commercials.Set {
			current.Commercial = body.Commercials.Value
		}
		if body.Exclusions.Set {
			current.Exclusions = body.Exclusions.Value
		}
		saved, err := library.SaveFolderSettings(r.Context(), db, current)
		if err != nil {
			if errors.Is(err, library.ErrInvalidFolderPath) || errors.Is(err, library.ErrInvalidPattern) || errors.Is(err, library.ErrTooManyEntries) {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Only a real change (including default <-> saved) triggers a rescan.
		if !reflect.DeepEqual(before, saved) {
			refresh.NotifyFoldersChanged()
		}
		response := toFolderSettingsResponse(saved.Effective())
		if err := addFolderCounts(r.Context(), db, &response); err != nil {
			writeJSONError(w, 500, "could not count media")
			return
		}
		writeJSON(w, http.StatusOK, response)
	}
}

func addFolderCounts(ctx context.Context, db *sql.DB, response *folderSettingsResponse) error {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT mf.path,mf.root_kind FROM media_files mf LEFT JOIN identified_items ii ON ii.media_file_id=mf.id WHERE mf.status='ok' AND ((mf.root_kind='media' AND ii.media_type IN ('movie','episode')) OR mf.root_kind='commercial')`)
	if err != nil {
		return err
	}
	defer rows.Close()
	groups := []*folderList{&response.TV, &response.Movies, &response.Commercials}
	roots := [][]string{}
	for _, group := range groups {
		list := []string{}
		for _, p := range group.Paths {
			root := filepath.Clean(p.Path)
			if r, err := filepath.EvalSymlinks(root); err == nil {
				root = r
			}
			list = append(list, root)
		}
		roots = append(roots, list)
	}
	for rows.Next() {
		var path, kind string
		if err := rows.Scan(&path, &kind); err != nil {
			return err
		}
		for g, group := range groups {
			if (g == 2) != (kind == "commercial") {
				continue
			}
			for i, root := range roots[g] {
				rel, err := filepath.Rel(root, path)
				if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					group.Paths[i].Count++
				}
			}
		}
	}
	return rows.Err()
}
