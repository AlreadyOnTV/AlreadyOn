// Package library discovers media files under configured roots, extracts
// technical metadata via ffprobe, and identifies movies/episodes.
// See docs/PROJECT_BRIEF.md Epic 2.
package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/activity"
	"github.com/alreadyonapp/alreadyon/server/internal/config"
	"github.com/alreadyonapp/alreadyon/server/internal/governor"
	"github.com/alreadyonapp/alreadyon/server/internal/refreshlog"
)

// DefaultVideoExtensions are the file extensions the scanner treats as
// media (movies/episodes/commercials) worth recording and probing.
// Anything else found under a root (posters, .nfo, subtitles, junk) is
// counted as skipped rather than inserted into media_files.
var DefaultVideoExtensions = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true,
	".wmv": true, ".mpg": true, ".mpeg": true, ".ts": true, ".m2ts": true,
	".webm": true, ".flv": true,
}

// ScanResult summarizes what one Scan call found. Per-file problems
// (an unreadable entry, a corrupt probe, a symlink escaping its root)
// are collected in Errors rather than aborting the whole scan — one bad
// file should not stop the rest of the library from being scanned.
type ScanResult struct {
	FilesAdded     int
	FilesUpdated   int
	FilesUnchanged int
	FilesSkipped   int
	FilesMissing   int // previously-seen rows not found on this scan
	Errors         []error

	visited               map[int64]struct{} // media_files ids touched this scan
	activityTask          string
	candidates, inspected int
}

// Scanner walks configured media/commercial roots, records discovered
// files in media_files, and probes new or changed ones via ffprobe.
// media_files.status ("ok", "missing", "unreadable", "corrupt") reflects
// the outcome of the most recent scan per file; a file that becomes
// readable/valid again is simply reported as "ok" on its next scan, same
// as any other status change — no separate "recovery" path needed.
type Scanner struct {
	DB    *sql.DB
	Roots []config.ValidatedRoot

	// Exclude is a set of filepath.Match glob patterns checked against
	// each file/directory's base name (not its full path). A directory
	// match prunes the whole subtree.
	Exclude []string

	// ProbeTimeout bounds each ffprobe subprocess. Defaults to 30s.
	ProbeTimeout time.Duration

	// MinMediaSeconds drops movie/TV files this short or shorter: at that length
	// a file is more likely a stray commercial in the wrong library than
	// programming. Zero keeps everything. Commercial roots are never filtered.
	MinMediaSeconds float64
}

// MinMediaRuntimeSeconds is the runtime (5 minutes) at or below which a file in
// a media root is ignored.
const MinMediaRuntimeSeconds = 300

var backgroundPool atomic.Pointer[governor.Governor]

// SetBackgroundGovernor installs the server's shared analysis pool for library
// scans from startup, polling and admin requests. Foreground Probe calls are
// unaffected. Configure it before starting the server's scan goroutines.
func SetBackgroundGovernor(g *governor.Governor) { backgroundPool.Store(g) }

// Scan counts candidates, then inspects every configured root. Re-running Scan on a library
// where nothing changed on disk makes no writes beyond touching
// last_seen_unix, and re-probes no files.
func (s *Scanner) Scan(ctx context.Context) (ScanResult, error) {
	task, finish := activity.Default.Start("Library Scan", "media candidates", 0)
	defer finish()
	result := ScanResult{visited: make(map[int64]struct{}), activityTask: task}
	// Count directory entries without probing or retaining paths. Discovery is
	// indeterminate; the slower database/probe pass then has a real denominator.
	for _, root := range s.Roots {
		if err := filepath.WalkDir(root.RealPath, func(path string, d fs.DirEntry, err error) error {
			if err := governor.Checkpoint(ctx); err != nil {
				return err
			}
			if err != nil {
				return nil
			}
			if (path != root.RealPath && strings.HasPrefix(d.Name(), ".")) || s.isExcluded(d.Name()) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if scanCandidate(d) {
				result.candidates++
			}
			return nil
		}); err != nil {
			return result, err
		}
	}
	activity.Default.Progress(task, 0, result.candidates)
	for _, root := range s.Roots {
		if err := s.walkRoot(ctx, root, &result); err != nil {
			return result, fmt.Errorf("library: scan %s root %s: %w", root.Kind, root.RealPath, err)
		}
	}

	missing, err := s.markUnvisitedAsMissing(ctx, result.visited)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("mark missing files: %w", err))
	}
	result.FilesMissing = missing

	// Only the "last refreshed" note depends on this; never fail a scan over it.
	_ = refreshlog.Record(ctx, s.DB, refreshlog.Media, time.Now())
	return result, nil
}

// markUnvisitedAsMissing flags any row this scan didn't touch as
// missing — covering both a file actually deleted from disk and one
// under a root that's no longer configured at all. Comparing against an
// explicit set of visited ids (rather than a last_seen_unix timestamp
// cutoff) avoids a same-second race: two scans of an unchanged, tiny
// library can easily land in the same wall-clock second, which a
// timestamp comparison would misread as "not visited this time".
func (s *Scanner) markUnvisitedAsMissing(ctx context.Context, visited map[int64]struct{}) (int, error) {
	var query string
	args := make([]any, 0, len(visited))
	if len(visited) == 0 {
		query = `UPDATE media_files SET status = 'missing', updated_at_unix = unixepoch() WHERE status != 'missing'`
	} else {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(visited)), ",")
		query = fmt.Sprintf(`
			UPDATE media_files SET status = 'missing', updated_at_unix = unixepoch()
			WHERE status != 'missing' AND id NOT IN (%s)`, placeholders)
		for id := range visited {
			args = append(args, id)
		}
	}

	res, err := s.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(rows), nil
}

func (s *Scanner) walkRoot(ctx context.Context, root config.ValidatedRoot, result *ScanResult) error {
	return filepath.WalkDir(root.RealPath, func(path string, d fs.DirEntry, err error) error {
		if err := governor.Checkpoint(ctx); err != nil {
			return err
		}
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("walk %s: %w", path, err))
			return nil
		}

		name := d.Name()
		if path != root.RealPath && strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			result.FilesSkipped++
			return nil
		}
		if s.isExcluded(name) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			result.FilesSkipped++
			return nil
		}

		if scanCandidate(d) {
			defer func() {
				result.inspected++
				result.candidates = max(result.candidates, result.inspected)
				activity.Default.Progress(result.activityTask, result.inspected, result.candidates)
			}()
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return s.visitSymlink(ctx, root, path, result)
		}
		if d.IsDir() {
			return nil
		}
		if !DefaultVideoExtensions[strings.ToLower(filepath.Ext(path))] {
			result.FilesSkipped++
			return nil
		}

		info, err := d.Info()
		if err != nil {
			// A recognized video file we can't even stat (removed between
			// listing and stat-ing it, or some exotic permission model)
			// still gets tracked — as unreadable — rather than silently
			// vanishing from the library's view of what's there. The much
			// more common "permission denied" case is a file that stats
			// fine but ffprobe can't open — handled in visitFile instead.
			result.Errors = append(result.Errors, fmt.Errorf("stat %s: %w", path, err))
			id, isNew, recordErr := s.recordUnreadable(ctx, root.Kind, path)
			if recordErr != nil {
				result.Errors = append(result.Errors, fmt.Errorf("record unreadable %s: %w", path, recordErr))
				return nil
			}
			result.visited[id] = struct{}{}
			if isNew {
				result.FilesAdded++
			} else {
				result.FilesUpdated++
			}
			return nil
		}
		return s.visitFile(ctx, root, path, info, result)
	})
}

// visitSymlink resolves a symlink found during the walk and rejects it
// if it points outside the root it was found in — the actual
// symlink-escape defense described in issue #13. A symlink that resolves
// to a directory is deliberately not followed at all (simplest safe
// default; avoids needing separate symlink-loop detection), but a
// symlink to a file that resolves safely inside the root is inspected
// like any other file.
func (s *Scanner) visitSymlink(ctx context.Context, root config.ValidatedRoot, path string, result *ScanResult) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("resolve symlink %s: %w", path, err))
		return nil
	}
	if !config.PathWithinRoot(root, resolved) {
		result.FilesSkipped++
		result.Errors = append(result.Errors, fmt.Errorf("skip %s: symlink escapes root %s", path, root.RealPath))
		return nil
	}

	info, err := os.Stat(resolved)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("stat %s: %w", path, err))
		return nil
	}
	if info.IsDir() {
		result.FilesSkipped++
		return nil
	}
	return s.visitFile(ctx, root, path, info, result)
}

func (s *Scanner) visitFile(ctx context.Context, root config.ValidatedRoot, path string, info fs.FileInfo, result *ScanResult) error {
	if !DefaultVideoExtensions[strings.ToLower(filepath.Ext(path))] {
		result.FilesSkipped++
		return nil
	}

	id, isNew, changed, previousStatus, err := s.upsertMediaFile(ctx, root.Kind, path, info)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("record %s: %w", path, err))
		return nil
	}
	result.visited[id] = struct{}{}

	needsProbe := isNew || changed
	switch {
	case isNew:
		result.FilesAdded++
	case changed:
		result.FilesUpdated++
	default:
		result.FilesUnchanged++
		// Content didn't change, so normally skip re-probing (#15). But
		// "unreadable"/"corrupt" are probe-level judgments a mere
		// successful stat can't revise — only a fresh probe can confirm
		// whether that earlier problem is actually fixed, so retry it.
		needsProbe = previousStatus == "unreadable" || previousStatus == "corrupt"
	}
	if !needsProbe {
		// Parser upgrades must also repair already-probed year-season files.
		// This does not re-probe media or reset correctly classified TV metadata.
		if err := s.identifyFile(ctx, root, path, id, true); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("repair identification: %w", err))
		}
		return nil
	}

	if g := backgroundPool.Load(); g != nil {
		ticket, err := g.Acquire(ctx, governor.PriorityHigh)
		if err != nil {
			return err
		}
		defer ticket.Release()
		ticket.SetTask("library probe")
		ctx = governor.WithTicket(ctx, ticket)
	}
	probeCtx, cancel := context.WithTimeout(ctx, s.probeTimeout())
	probe, err := Probe(probeCtx, path)
	cancel()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("probe %s: %w", path, err))
		status := classifyProbeError(err)
		if statusErr := s.setMediaFileStatus(ctx, id, status); statusErr != nil {
			result.Errors = append(result.Errors, fmt.Errorf("mark %s %s: %w", status, path, statusErr))
		}
		return nil
	}
	if root.Kind == config.RootKindMedia && s.MinMediaSeconds > 0 && probe.DurationSeconds > 0 && probe.DurationSeconds <= s.MinMediaSeconds {
		// Forget it (a later scan finds it again and drops it again).
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM media_files WHERE id = ?`, id); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("drop short file %s: %w", path, err))
			return nil
		}
		delete(result.visited, id)
		switch {
		case isNew:
			result.FilesAdded--
		case changed:
			result.FilesUpdated--
		default:
			result.FilesUnchanged--
		}
		result.FilesSkipped++
		return nil
	}
	if err := SaveProbeResult(ctx, s.DB, id, probe); err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("save probe result for %s: %w", path, err))
	}
	// A file that was previously corrupt/unreadable and now probes fine
	// has recovered — no separate "recovery" logic needed, this scan's
	// truth just replaces last scan's.
	if statusErr := s.setMediaFileStatus(ctx, id, "ok"); statusErr != nil {
		result.Errors = append(result.Errors, fmt.Errorf("mark ok %s: %w", path, statusErr))
	}

	if err := s.identifyFile(ctx, root, path, id, false); err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("save identification for %s: %w", path, err))
	}
	return nil
}

// identifyFile shares filename parsing between new probes and narrowly scoped
// recovery of old year-season movie guesses during an otherwise unchanged scan.
func (s *Scanner) identifyFile(ctx context.Context, root config.ValidatedRoot, path string, id int64, repairOnly bool) error {
	// Commercials aren't movies/episodes to identify — running them
	// through Identify() just produced junk identified_items rows (a
	// bare release year from a "2000/2001/2002" library folder, mostly
	// empty titles) that would otherwise pollute genre classification
	// (#79) and channel lineup selection.
	if root.Kind != config.RootKindMedia {
		return nil
	}

	// Identify from the path relative to this file's own root, not the
	// absolute path — otherwise an unrelated ancestor directory name
	// (e.g. "/srv/media") could get mistaken for a show folder.
	relPath, relErr := filepath.Rel(root.RealPath, path)
	if relErr != nil {
		relPath = filepath.Base(path)
	}
	if identification, ok := Identify(relPath); ok {
		if repairOnly {
			if !identification.IsEpisode || identification.Season < 1900 {
				return nil
			}
			var kind string
			err := s.DB.QueryRowContext(ctx, `SELECT media_type FROM identified_items WHERE media_file_id=?`, id).Scan(&kind)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			if err == nil && kind != "movie" {
				return nil
			}
		}
		return SaveIdentification(ctx, s.DB, id, identification)
	}
	return nil
}

// upsertMediaFile records path in media_files, reporting whether it is a
// newly-seen file, a previously-seen file whose size or modification
// time changed, or unchanged, plus its status before this call (used by
// the caller to decide whether an "unchanged" file still needs
// re-probing). last_seen_unix is touched either way, so a missing-file
// check can tell a file was still present as of this scan.
func (s *Scanner) upsertMediaFile(ctx context.Context, kind config.RootKind, path string, info fs.FileInfo) (id int64, isNew, changed bool, previousStatus string, err error) {
	var existingID, existingSize, existingMtime int64
	err = s.DB.QueryRowContext(ctx, `SELECT id, size_bytes, modified_unix, status FROM media_files WHERE path = ?`, path).
		Scan(&existingID, &existingSize, &existingMtime, &previousStatus)

	now := time.Now().Unix()
	size := info.Size()
	mtime := info.ModTime().Unix()

	if errors.Is(err, sql.ErrNoRows) {
		res, insertErr := s.DB.ExecContext(ctx, `
			INSERT INTO media_files (root_kind, path, size_bytes, modified_unix, first_seen_unix, last_seen_unix)
			VALUES (?, ?, ?, ?, ?, ?)`,
			string(kind), path, size, mtime, now, now)
		if insertErr != nil {
			return 0, false, false, "", insertErr
		}
		newID, idErr := res.LastInsertId()
		if idErr != nil {
			return 0, false, false, "", idErr
		}
		return newID, true, true, "", nil
	}
	if err != nil {
		return 0, false, false, "", err
	}

	changed = size != existingSize || mtime != existingMtime
	_, updateErr := s.DB.ExecContext(ctx, `
		UPDATE media_files
		SET size_bytes = ?, modified_unix = ?, last_seen_unix = ?, updated_at_unix = unixepoch(),
			-- Reaching this UPDATE at all means the file exists and is
			-- stat-able right now, so a stale "missing" status no longer
			-- applies. "unreadable"/"corrupt" are left alone here on
			-- purpose — those are probe-level judgments only a fresh
			-- probe can revise (see visitFile's needsProbe), not
			-- something a mere successful stat proves fixed.
			status = CASE WHEN status = 'missing' THEN 'ok' ELSE status END
		WHERE id = ?`,
		size, mtime, now, existingID)
	if updateErr != nil {
		return 0, false, false, "", updateErr
	}
	return existingID, false, changed, previousStatus, nil
}

// recordUnreadable marks path as unreadable — a recognized video file
// that exists (it was listed in its directory) but couldn't be stat-ed
// just now (e.g. a TOCTOU race, or some exotic permission model).
// Reports the row's id and whether this created a new row (the file was
// never successfully seen before) or updated an existing one.
func (s *Scanner) recordUnreadable(ctx context.Context, kind config.RootKind, path string) (id int64, isNew bool, err error) {
	now := time.Now().Unix()
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM media_files WHERE path = ?`, path).Scan(&id); err == nil {
		_, updateErr := s.DB.ExecContext(ctx, `
			UPDATE media_files SET status = 'unreadable', last_seen_unix = ?, updated_at_unix = unixepoch()
			WHERE id = ?`, now, id)
		if updateErr != nil {
			return 0, false, updateErr
		}
		return id, false, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}

	// Never successfully seen before: insert a placeholder row so the
	// file is at least visible as present-but-unreadable, rather than
	// silently absent from the library.
	res, err := s.DB.ExecContext(ctx, `
		INSERT INTO media_files (root_kind, path, size_bytes, modified_unix, first_seen_unix, last_seen_unix, status)
		VALUES (?, ?, 0, 0, ?, ?, 'unreadable')`,
		string(kind), path, now, now)
	if err != nil {
		return 0, false, err
	}
	newID, err := res.LastInsertId()
	if err != nil {
		return 0, false, err
	}
	return newID, true, nil
}

// setMediaFileStatus sets mediaFileID's status directly — used once a
// probe attempt has definitively confirmed whether the file is valid.
func (s *Scanner) setMediaFileStatus(ctx context.Context, mediaFileID int64, status string) error {
	_, err := s.DB.ExecContext(ctx, `
		UPDATE media_files SET status = ?, updated_at_unix = unixepoch() WHERE id = ?`, status, mediaFileID)
	return err
}

// classifyProbeError distinguishes a permission problem (ffprobe's own
// stderr says so verbatim, e.g. "Permission denied") from genuinely
// invalid/unsupported content — the former is "unreadable", the latter
// "corrupt".
func classifyProbeError(err error) string {
	if strings.Contains(strings.ToLower(err.Error()), "permission denied") {
		return "unreadable"
	}
	return "corrupt"
}

func (s *Scanner) isExcluded(name string) bool {
	for _, pattern := range s.Exclude {
		if ok, _ := filepath.Match(pattern, name); ok {
			return true
		}
	}
	return false
}

func (s *Scanner) probeTimeout() time.Duration {
	if s.ProbeTimeout > 0 {
		return s.ProbeTimeout
	}
	return 30 * time.Second
}

// Symlinks are candidates too: inspecting and rejecting an unsafe link is work.
func scanCandidate(d fs.DirEntry) bool {
	return !d.IsDir() && (d.Type()&fs.ModeSymlink != 0 || DefaultVideoExtensions[strings.ToLower(filepath.Ext(d.Name()))])
}
