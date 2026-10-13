package playback

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/config"
	"github.com/alreadyonapp/alreadyon/server/internal/library"
)

// LoadSource uses catalog IDs only. A removed root, escaped symlink, missing
// file or corrupt catalog row cannot be turned into arbitrary file delivery.
func LoadSource(ctx context.Context, db *sql.DB, id int64) (string, library.ProbeResult, error) {
	var path string
	var raw sql.NullString
	var status string
	err := db.QueryRowContext(ctx, `SELECT path,probe_json,status FROM media_files WHERE id=?`, id).Scan(&path, &raw, &status)
	if err != nil {
		return "", library.ProbeResult{}, err
	}
	if status == "missing" {
		return "", library.ProbeResult{}, os.ErrNotExist
	}
	if status != "ok" {
		return "", library.ProbeResult{}, errors.New("playback: source is unreadable or corrupt")
	}
	real, err := AllowedPath(ctx, db, path)
	if err != nil {
		return "", library.ProbeResult{}, err
	}
	var source library.ProbeResult
	if raw.Valid {
		_ = json.Unmarshal([]byte(raw.String), &source)
	}
	v, _ := Tracks(source)
	if (source.MetadataVersion < 2 && (v.Codec == "hevc" || v.Codec == "av1" || IsHDRTransfer(v.ColorTransfer))) || v.FrameRate <= 0 || v.PixelFormat == "" || source.BitRate <= 0 {
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		source, err = library.Probe(probeCtx, real)
		if err != nil {
			return "", source, err
		}
		// Upgrade old cached metadata once, rather than probing each tune.
		if err := library.SaveProbeResult(ctx, db, id, source); err != nil {
			return "", source, err
		}
	}
	return real, source, nil
}

func AllowedPath(ctx context.Context, db *sql.DB, path string) (string, error) {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("playback: source is not a regular file")
	}
	cfg, err := library.LoadScanConfig(ctx, db)
	if err != nil {
		return "", err
	}
	for _, root := range cfg.Roots {
		if config.PathWithinRoot(root, real) {
			return real, nil
		}
	}
	return "", errors.New("playback: source is outside configured media roots")
}

// OpenSource opens a validated catalog path relative to its configured root.
// Root-relative opening prevents a concurrent symlink swap from escaping the
// root between validation and delivery. FFmpeg still uses path-based inputs.
func OpenSource(ctx context.Context, db *sql.DB, path string) (*os.File, error) {
	real, err := AllowedPath(ctx, db, path)
	if err != nil {
		return nil, err
	}
	cfg, err := library.LoadScanConfig(ctx, db)
	if err != nil {
		return nil, err
	}
	for _, root := range cfg.Roots {
		if !config.PathWithinRoot(root, real) {
			continue
		}
		rel, err := filepath.Rel(root.RealPath, real)
		if err != nil {
			return nil, err
		}
		f, err := os.OpenInRoot(root.RealPath, rel)
		if err != nil {
			return nil, err
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			_ = f.Close()
			return nil, errors.New("playback: opened source is not a regular file")
		}
		return f, nil
	}
	return nil, errors.New("playback: source root is no longer configured")
}
