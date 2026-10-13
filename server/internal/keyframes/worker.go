package keyframes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/governor"
	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/logging"
	"github.com/alreadyonapp/alreadyon/server/internal/playback"
)

// MaxWorkers caps the scan: keyframe reads are bound by the disk, so more than a
// few at once only add seeking on a spinning array.
const MaxWorkers = 4

// idle is how long a worker waits once every file is measured. New files are
// picked up on the next check and failures are retried after a day, so the scan
// covers both new content and a daily pass.
const idle = time.Minute

type candidate struct {
	id, size, modified int64
	path, probe        string
}

func candidates(ctx context.Context, db *sql.DB, limit int) ([]candidate, error) {
	rows, err := db.QueryContext(ctx, `SELECT mf.id,mf.path,mf.size_bytes,mf.modified_unix,COALESCE(mf.probe_json,'') FROM media_files mf LEFT JOIN keyframe_measurements km ON km.media_file_id=mf.id WHERE mf.status='ok' AND (km.media_file_id IS NULL OR km.size_bytes!=mf.size_bytes OR km.modified_unix!=mf.modified_unix OR km.version!=? OR (km.status='failed' AND km.analyzed_at<unixepoch()-86400)) ORDER BY mf.id LIMIT ?`, Version, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []candidate
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.id, &c.path, &c.size, &c.modified, &c.probe); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

var inflight = struct {
	sync.Mutex
	ids map[int64]bool
}{ids: map[int64]bool{}}

func claim(ctx context.Context, db *sql.DB) (candidate, error) {
	inflight.Lock()
	defer inflight.Unlock()
	cs, err := candidates(ctx, db, MaxWorkers+1)
	if err != nil {
		return candidate{}, err
	}
	for _, c := range cs {
		if !inflight.ids[c.id] {
			inflight.ids[c.id] = true
			return c, nil
		}
	}
	return candidate{}, sql.ErrNoRows
}

func release(id int64) {
	inflight.Lock()
	delete(inflight.ids, id)
	inflight.Unlock()
}

type pool interface {
	Acquire(ctx context.Context, priority int) (*governor.Ticket, error)
	Max() int
}

// Run measures every library file in the background: new and changed files as
// they appear, then a daily retry of failures. It shares the governor with other
// background work and ranks above it, because playback planning needs these
// results.
func Run(ctx context.Context, db *sql.DB, g *governor.Governor) { run(ctx, db, g) }

func run(ctx context.Context, db *sql.DB, p pool) {
	var wg sync.WaitGroup
	var measured atomic.Int64
	for range min(p.Max(), MaxWorkers) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker(ctx, db, p, &measured)
		}()
	}
	wg.Wait()
}

func worker(ctx context.Context, db *sql.DB, p pool, measured *atomic.Int64) {
	for ctx.Err() == nil {
		ticket, err := p.Acquire(ctx, governor.PriorityHigh)
		if err != nil {
			return
		}
		c, claimErr := claim(ctx, db)
		if claimErr == nil {
			ticket.SetTask("keyframe analysis")
			process(governor.WithTicket(ctx, ticket), db, c)
			release(c.id)
			measured.Add(1)
		}
		ticket.Release()
		if claimErr == nil {
			continue
		}
		if n := measured.Swap(0); n > 0 {
			logging.Info("keyframe scan: measured %d file(s)", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(idle):
		}
	}
}

func process(ctx context.Context, db *sql.DB, c candidate) {
	status, gap, message := "ready", time.Duration(0), ""
	real, err := allowed(ctx, db, c.path)
	var source library.ProbeResult
	if err == nil {
		_ = json.Unmarshal([]byte(c.probe), &source)
		if v, _ := playback.Tracks(source); v.Index < 0 {
			status = "no-video"
		} else {
			job, cancel := context.WithTimeout(ctx, 10*time.Minute)
			gap, err = measure(job, real, source.DurationSeconds)
			cancel()
		}
	}
	if err == nil {
		if info, statErr := os.Stat(real); statErr != nil || info.Size() != c.size || info.ModTime().Unix() != c.modified {
			err = errors.New("source changed during measurement")
		}
	}
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		status, message, gap = "failed", err.Error(), 0
	}
	if saveErr := save(ctx, db, c.id, c.size, c.modified, status, gap, message); saveErr != nil {
		logging.Warn("keyframes: could not store measurement for media %d: %v", c.id, saveErr)
	}
}

// measure uses the same metered CPU budget as loudness, including while a probe
// is running. Inline playback measurements do not carry a background ticket.
func measure(ctx context.Context, path string, durationSeconds float64) (time.Duration, error) {
	return Measure(ctx, path, durationSeconds, true, false)
}

func allowed(ctx context.Context, db *sql.DB, path string) (string, error) {
	real, err := playback.AllowedPath(ctx, db, path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(real), nil
}
