// Package keyframes measures how far apart the video keyframes are in each
// library file. Copied video can only be cut into HLS segments at keyframes, so
// the longest gap decides whether a file can be streamed without conversion
// (docs/PLAYBACK.md, Keyframe spacing). The measurement reads packet headers
// only, never decodes, and runs in the background at low priority.
package keyframes

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/activity"
	"github.com/alreadyonapp/alreadyon/server/internal/governor"
)

// Version is bumped when the way gaps are measured changes, so old rows are
// measured again.
const Version = 1

// Window is how much of the file one sample covers. A file whose sample holds
// fewer than two keyframes has a gap of at least this long.
const Window = 60 * time.Second

// InlineBudget bounds the measurement made while a viewer is tuning, so a file
// the background scan has not reached yet cannot hold up the first picture.
const InlineBudget = 1500 * time.Millisecond

// Failed is returned for a file whose gap could not be measured. Callers treat
// it as unknown and convert rather than copy.
const Failed = -time.Nanosecond

// Measure returns the longest gap between video keyframes in samples taken at
// the start and, for files over three windows, the middle. background runs
// ffprobe at the lowest CPU and disk priority.
func Measure(ctx context.Context, path string, durationSeconds float64, background, quick bool) (time.Duration, error) {
	starts := []float64{0}
	if !quick && durationSeconds > 3*Window.Seconds() {
		starts = append(starts, durationSeconds/2)
	}
	task := ""
	if background {
		var finish func()
		task, finish = activity.Default.Start("Keyframe Analysis", "sample windows", len(starts))
		defer finish()
	}
	var longest time.Duration
	for i, start := range starts {
		if task != "" {
			activity.Default.Progress(task, i, len(starts))
		}
		interval := fmt.Sprintf("%.0f%%+%.0f", start, Window.Seconds())
		args := []string{"-v", "error", "-select_streams", "v:0", "-show_entries", "packet=pts_time,dts_time,flags", "-of", "csv=p=0", "-read_intervals", interval, "--", path}
		name := "ffprobe"
		if background {
			args = append([]string{"-n", "19", "ionice", "-c", "3", "ffprobe"}, args...)
			name = "nice"
		}
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.WaitDelay = 500 * time.Millisecond // do not wait on pipes a killed child still holds
		var output bytes.Buffer
		cmd.Stdout = &output
		err := governor.RunCommand(ctx, cmd)
		out := output.Bytes()
		if err != nil {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			return 0, fmt.Errorf("keyframes: ffprobe: %w", err)
		}
		gap, err := maxGap(string(out))
		if err != nil {
			return 0, err
		}
		longest = max(longest, gap)
	}
	return longest, nil
}

// maxGap reads ffprobe's "pts_time,dts_time,flags" rows for one window. Fewer
// than two keyframes means the gap is at least the window.
func maxGap(output string) (time.Duration, error) {
	var times []float64
	rows := 0
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 3 {
			return 0, fmt.Errorf("keyframes: unexpected ffprobe row %q", line)
		}
		rows++
		if !strings.HasPrefix(fields[2], "K") {
			continue
		}
		at, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			if at, err = strconv.ParseFloat(fields[1], 64); err != nil {
				continue
			}
		}
		times = append(times, at)
	}
	if rows == 0 {
		return 0, errors.New("keyframes: no video packets")
	}
	if len(times) < 2 {
		return Window, nil
	}
	sort.Float64s(times)
	var longest float64
	for i := 1; i < len(times); i++ {
		longest = max(longest, times[i]-times[i-1])
	}
	return time.Duration(longest * float64(time.Second)), nil
}

// Lookup returns the stored gap for a file whose fingerprint still matches:
// positive when measured, Failed when the last attempt failed, zero when the
// file has not been measured.
func Lookup(ctx context.Context, db *sql.DB, id int64) (time.Duration, error) {
	var status string
	var ms int64
	err := db.QueryRowContext(ctx, `SELECT km.status,km.max_gap_ms FROM keyframe_measurements km JOIN media_files mf ON mf.id=km.media_file_id WHERE km.media_file_id=? AND km.version=? AND mf.size_bytes=km.size_bytes AND mf.modified_unix=km.modified_unix`, id, Version).Scan(&status, &ms)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if status != "ready" {
		return Failed, nil
	}
	return time.Duration(ms) * time.Millisecond, nil
}

func save(ctx context.Context, db *sql.DB, id, size, modified int64, status string, gap time.Duration, message string) error {
	if len(message) > 512 {
		message = message[:512]
	}
	_, err := db.ExecContext(ctx, `INSERT INTO keyframe_measurements(media_file_id,size_bytes,modified_unix,version,status,max_gap_ms,error) VALUES(?,?,?,?,?,?,?) ON CONFLICT(media_file_id) DO UPDATE SET size_bytes=excluded.size_bytes,modified_unix=excluded.modified_unix,version=excluded.version,status=excluded.status,max_gap_ms=excluded.max_gap_ms,error=excluded.error,analyzed_at=unixepoch()`,
		id, size, modified, Version, status, gap.Milliseconds(), message)
	return err
}

// ForTune returns the gap for the file about to be streamed. A file the
// background scan has not reached is measured now from its first window within
// InlineBudget; running out of time returns Failed and stores nothing, leaving
// the file to the background scan.
func ForTune(ctx context.Context, db *sql.DB, id int64, path string, durationSeconds float64) time.Duration {
	gap, err := Lookup(ctx, db, id)
	if err != nil {
		return Failed
	}
	if gap != 0 {
		return gap
	}
	var size, modified int64
	if err = db.QueryRowContext(ctx, `SELECT size_bytes,modified_unix FROM media_files WHERE id=?`, id).Scan(&size, &modified); err != nil {
		return Failed
	}
	inline, cancel := context.WithTimeout(ctx, InlineBudget)
	defer cancel()
	gap, err = Measure(inline, path, durationSeconds, false, true)
	if err != nil || gap <= 0 {
		return Failed
	}
	_ = save(ctx, db, id, size, modified, "ready", gap, "")
	return gap
}
