package loudness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/activity"
	"github.com/alreadyonapp/alreadyon/server/internal/config"
	"github.com/alreadyonapp/alreadyon/server/internal/governor"
	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/logging"
)

type tailBuffer struct{ data []byte }

func (b *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	b.data = append(b.data, p...)
	if len(b.data) > 65536 {
		b.data = b.data[len(b.data)-65536:]
	}
	return n, nil
}
func Analyze(ctx context.Context, path string, track int) (*Measurement, string, error) {
	return analyze(ctx, path, track, 0)
}

// AnalyzeSample measures the first tenth; unusable or very short samples retain
// full-file analysis. A finite opening sample cannot detect later loudness changes.
func AnalyzeSample(ctx context.Context, path string, track int, duration float64) (*Measurement, string, error) {
	seconds := duration / 10
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 3 {
		return Analyze(ctx, path, track)
	}
	sampled := context.WithValue(ctx, analysisDurationKey{}, int(math.Ceil(seconds)))
	m, state, err := analyze(sampled, path, track, seconds)
	if ctx.Err() != nil || (err == nil && state == "ready") {
		return m, state, err
	}
	return Analyze(ctx, path, track)
}

func analyze(ctx context.Context, path string, track int, seconds float64) (*Measurement, string, error) {
	args := []string{"-n", "19", "ionice", "-c", "3", "ffmpeg", "-hide_banner", "-nostdin", "-nostats", "-progress", "pipe:1", "-threads", "1", "-filter_threads", "1", "-protocol_whitelist", "file,pipe"}
	if seconds > 0 {
		args = append(args, "-t", strconv.FormatFloat(seconds, 'f', -1, 64))
	}
	args = append(args, "-i", path, "-map", "0:"+strconv.Itoa(track), "-vn", "-sn", "-dn", "-af", Downmix+",loudnorm=I=-16:TP=-1.5:LRA=50:print_format=json", "-threads", "1", "-f", "null", "-")
	cmd := exec.CommandContext(ctx, "nice", args...)
	configure(cmd)
	var stderr tailBuffer
	cmd.Stderr = &stderr
	cmd.Stdout = &analysisProgress{task: activityTask(ctx), total: analysisDuration(ctx)}
	if err := governor.RunCommand(ctx, cmd); err != nil {
		if ctx.Err() != nil {
			return nil, "failed", ctx.Err()
		}
		return nil, "failed", fmt.Errorf("audio analysis failed: %w", err)
	}
	return parse(string(stderr.data))
}

type candidate struct {
	id, size, modified int64
	path               string
}

func candidates(ctx context.Context, db *sql.DB, generation int64, limit int) ([]candidate, error) {
	rows, err := db.QueryContext(ctx, `SELECT mf.id,mf.path,mf.size_bytes,mf.modified_unix FROM media_files mf LEFT JOIN loudness_measurements lm ON lm.media_file_id=mf.id WHERE mf.status='ok' AND (lm.media_file_id IS NULL OR lm.size_bytes!=mf.size_bytes OR lm.modified_unix!=mf.modified_unix OR lm.version NOT IN (?,?) OR (lm.status='failed' AND (lm.generation<? OR lm.analyzed_at<unixepoch()-86400))) ORDER BY mf.id LIMIT ?`, fullFileVersion, Version, generation, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []candidate
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.id, &c.path, &c.size, &c.modified); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func next(ctx context.Context, db *sql.DB, generation int64) (candidate, error) {
	cs, err := candidates(ctx, db, generation, 1)
	if err != nil {
		return candidate{}, err
	}
	if len(cs) == 0 {
		return candidate{}, sql.ErrNoRows
	}
	return cs[0], nil
}

// pool admits jobs up to the host's CPU budget: Acquire waits for a ticket and
// Max is how many workers to start.
type pool interface {
	Acquire(ctx context.Context, priority int) (*governor.Ticket, error)
	Max() int
}

// governed is the pool Run is using, for Get's worker and CPU readings.
var governed atomic.Pointer[governor.Governor]

// inflight holds the files workers are measuring (media file id -> base name),
// so no two workers take the same file and the status row reflects all of them.
var inflight = struct {
	sync.Mutex
	files map[int64]string
}{files: map[int64]string{}}

// publish writes the shared status row; the caller holds inflight's lock.
func publish(ctx context.Context, db *sql.DB) {
	var name string
	low := int64(-1)
	for id, n := range inflight.files {
		if low < 0 || id < low {
			low, name = id, n
		}
	}
	_, _ = db.ExecContext(ctx, `UPDATE loudness_settings SET running=?,current_file=?,heartbeat=unixepoch() WHERE id=1`, len(inflight.files) > 0, name)
}

func release(ctx context.Context, db *sql.DB, id int64) {
	inflight.Lock()
	defer inflight.Unlock()
	delete(inflight.files, id)
	publish(ctx, db)
}

// claim returns the next file needing measurement that no other worker holds.
func claim(ctx context.Context, db *sql.DB, generation int64, limit int) (candidate, error) {
	inflight.Lock()
	defer inflight.Unlock()
	cs, err := candidates(ctx, db, generation, limit)
	if err != nil {
		return candidate{}, err
	}
	for _, c := range cs {
		if _, busy := inflight.files[c.id]; !busy {
			inflight.files[c.id] = filepath.Base(c.path)
			return c, nil
		}
	}
	return candidate{}, sql.ErrNoRows
}
func allowed(ctx context.Context, db *sql.DB, path string) (string, error) {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("source is not a regular file")
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
	return "", errors.New("source is outside the configured media roots")
}
func process(ctx context.Context, db *sql.DB, c candidate, generation int64) {
	task, finish := activity.Default.Start("Loudness Analysis", "seconds of audio", 0)
	defer finish()
	activity.Default.SetCurrentFile(task, filepath.Base(c.path))
	ctx = context.WithValue(ctx, analysisTaskKey{}, task)
	job, cancel := context.WithTimeout(ctx, 24*time.Hour)
	defer cancel()
	done := make(chan struct{})
	defer close(done)
	// Disabling the feature cancels the current process promptly, including probing.
	go func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-job.Done():
				return
			case <-tick.C:
				var enabled bool
				if err := db.QueryRowContext(job, `SELECT enabled FROM loudness_settings WHERE id=1`).Scan(&enabled); err != nil || !enabled {
					cancel()
					return
				}
				_, _ = db.ExecContext(job, `UPDATE loudness_settings SET heartbeat=unixepoch() WHERE id=1`)
			}
		}
	}()
	inflight.Lock()
	inflight.files[c.id] = filepath.Base(c.path)
	publish(ctx, db)
	inflight.Unlock()
	defer release(context.WithoutCancel(ctx), db, c.id)
	real, err := allowed(job, db, c.path)
	var ns int64
	track := -1
	state := "failed"
	var measurement *Measurement
	if err == nil {
		var info os.FileInfo
		info, err = os.Stat(real)
		if err == nil {
			ns = info.ModTime().UnixNano()
			if info.Size() != c.size || info.ModTime().Unix() != c.modified {
				err = errors.New("source changed; waiting for the library scan")
			}
		}
	}
	if err == nil {
		probeCtx, probeCancel := context.WithTimeout(job, 15*time.Second)
		probe, probeErr := library.Probe(probeCtx, real)
		probeCancel()
		err = probeErr
		if err == nil {
			for _, stream := range probe.Streams {
				if stream.Kind == library.StreamAudio {
					track = stream.Index
					break
				}
			}
			if track < 0 {
				state = "no-audio"
			} else {
				activity.Default.Progress(task, 0, int(probe.DurationSeconds))
				analysisCtx := context.WithValue(job, analysisDurationKey{}, int(probe.DurationSeconds))
				measurement, state, err = AnalyzeSample(analysisCtx, real, track, probe.DurationSeconds)
			}
		}
	}
	if job.Err() != nil {
		if ctx.Err() != nil || errors.Is(job.Err(), context.Canceled) {
			return
		}
		err = job.Err()
	}
	if err == nil {
		info, statErr := os.Stat(real)
		if statErr != nil || info.Size() != c.size || info.ModTime().UnixNano() != ns {
			err = errors.New("source changed during analysis")
			state = "failed"
			measurement = nil
		}
	}
	message := ""
	if err != nil {
		message = err.Error()
		if len(message) > 512 {
			message = message[:512]
		}
		state = "failed"
	}
	raw, _ := json.Marshal(measurement)
	_, saveErr := db.ExecContext(ctx, `INSERT INTO loudness_measurements(media_file_id,size_bytes,modified_unix,modified_ns,stream_index,version,status,measurement,error,generation) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(media_file_id) DO UPDATE SET size_bytes=excluded.size_bytes,modified_unix=excluded.modified_unix,modified_ns=excluded.modified_ns,stream_index=excluded.stream_index,version=excluded.version,status=excluded.status,measurement=excluded.measurement,error=excluded.error,generation=excluded.generation,analyzed_at=unixepoch()`, c.id, c.size, c.modified, ns, track, Version, state, string(raw), message, generation)
	if saveErr != nil {
		logging.Warn("loudness: could not store measurement for media %d: %v", c.id, saveErr)
	}
}

// Run measures files with as many workers as the shared governor allows,
// disabled by default. Loudness work has the lowest priority, so it is paused
// first when other work needs the CPU. Completed matching fingerprints are
// reused; changed/new files and explicit retries are picked up.
func Run(ctx context.Context, db *sql.DB, g *governor.Governor) {
	_, _ = db.ExecContext(ctx, `UPDATE loudness_settings SET running=0,current_file='' WHERE id=1`)
	governed.Store(g)
	run(ctx, db, g)
}

func run(ctx context.Context, db *sql.DB, p pool) {
	var wg sync.WaitGroup
	for range p.Max() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker(ctx, db, p)
		}()
	}
	wg.Wait()
}

func worker(ctx context.Context, db *sql.DB, p pool) {
	for ctx.Err() == nil {
		var enabled bool
		var generation int64
		if err := db.QueryRowContext(ctx, `SELECT enabled,generation FROM loudness_settings WHERE id=1`).Scan(&enabled, &generation); err == nil && enabled {
			ticket, err := p.Acquire(ctx, governor.PriorityLow)
			if err != nil {
				return
			}
			jobCtx := governor.WithTicket(ctx, ticket)
			c, claimErr := claim(jobCtx, db, generation, p.Max()+1)
			if claimErr == nil {
				ticket.SetTask("loudness detection")
				process(jobCtx, db, c, generation)
			}
			ticket.Release()
			if claimErr == nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(100 * time.Millisecond):
				}
				continue
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

type analysisTaskKey struct{}
type analysisDurationKey struct{}

func activityTask(ctx context.Context) string {
	v, _ := ctx.Value(analysisTaskKey{}).(string)
	return v
}
func analysisDuration(ctx context.Context) int {
	v, _ := ctx.Value(analysisDurationKey{}).(int)
	return v
}

type analysisProgress struct {
	task    string
	total   int
	pending string
}

func (p *analysisProgress) Write(b []byte) (int, error) {
	p.pending += string(b)
	for {
		i := strings.IndexByte(p.pending, '\n')
		if i < 0 {
			break
		}
		line := p.pending[:i]
		p.pending = p.pending[i+1:]
		if raw, ok := strings.CutPrefix(line, "out_time_us="); ok {
			if us, err := strconv.ParseInt(raw, 10, 64); err == nil {
				activity.Default.Progress(p.task, int(us/1000000), p.total)
			}
		}
	}
	if len(p.pending) > 4096 {
		p.pending = ""
	}
	return len(b), nil
}
