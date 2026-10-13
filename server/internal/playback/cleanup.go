package playback

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/logging"
	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
)

var ErrBusy = errors.New("playback: stream capacity reached; retry shortly")
var ErrClosed = errors.New("playback: stream manager is closed")

type Worker interface {
	Run(context.Context, Job) error
}

type ManagedStream struct {
	Job     Job
	Runtime RuntimeStats
	Done    bool
	Err     error
}

type managed struct {
	ManagedStream
	key     string
	leases  map[string]time.Time
	cancel  context.CancelFunc
	closing bool
}

// Manager owns private output, workers and viewer leases. Its clock and worker
// are injectable; Sweep can be called deterministically without timer sleeps.
type Manager struct {
	mu       sync.Mutex
	root     string
	unlock   func() error
	clock    schedule.Clock
	worker   Worker
	settings Settings
	streams  map[string]*managed
	active   int
	closed   bool
	wg       sync.WaitGroup
}

func NewManager(root string, clock schedule.Clock, worker Worker, settings Settings) (*Manager, error) {
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	if clock == nil || worker == nil {
		return nil, errors.New("playback: missing clock/worker")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("playback: cache must be a private directory")
	}
	unlock, err := lockCache(abs)
	if err != nil {
		return nil, err
	}
	// A lock excludes another live manager. Only remove our generated names;
	// unrelated files in the parent directory are never swept.
	entries, err := os.ReadDir(abs)
	if err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "stream-") {
				if err = os.RemoveAll(filepath.Join(abs, e.Name())); err != nil {
					break
				}
			}
		}
	}
	if err != nil {
		_ = unlock()
		return nil, err
	}
	return &Manager{root: abs, unlock: unlock, clock: clock, worker: worker, settings: settings, streams: map[string]*managed{}}, nil
}

func (m *Manager) Configure(s Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings = s // Lowering a limit prevents admission; existing viewers finish.
	return nil
}

// Acquire deduplicates concurrent starts using a server-derived rendition key.
// viewer is the opaque session ID, never a user-chosen channel name or path.
// A successful acquisition persists beyond its HTTP request via a lease.
func (m *Manager) Acquire(ctx context.Context, key, viewer string, job Job) (ManagedStream, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ManagedStream{}, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return ManagedStream{}, err
	}
	if key == "" || viewer == "" {
		return ManagedStream{}, errors.New("playback: empty stream/session key")
	}
	if s := m.streams[key]; s != nil {
		if s.closing || s.Err != nil {
			return ManagedStream{}, ErrBusy
		}
		if _, exists := s.leases[viewer]; !exists && m.viewerCount() >= m.settings.MaxViewers {
			return ManagedStream{}, ErrBusy
		}
		s.leases[viewer] = m.clock.Now().Add(m.settings.Lease())
		return s.ManagedStream, nil
	}
	if len(m.streams) >= m.settings.MaxRenditions || m.viewerCount() >= m.settings.MaxViewers || (job.Plan.Mode != "direct" && m.active >= m.settings.MaxProcesses) {
		return ManagedStream{}, ErrBusy
	}
	if job.Plan.Mode != "direct" {
		dir, err := os.MkdirTemp(m.root, "stream-")
		if err != nil {
			return ManagedStream{}, err
		}
		job.OutputDir = dir
	}
	workerCtx, cancel := context.WithCancel(context.Background())
	s := &managed{ManagedStream: ManagedStream{Job: job}, key: key, leases: map[string]time.Time{viewer: m.clock.Now().Add(m.settings.Lease())}, cancel: cancel}
	m.streams[key] = s
	if job.Plan.Mode == "direct" {
		s.Done = true
		return s.ManagedStream, nil
	}
	m.active++
	m.wg.Add(1)
	// A fallback inside the runner changes how the stream is made; keep the plan and details true.
	job.Report = func(p Plan, pipeline Pipeline) {
		m.mu.Lock()
		defer m.mu.Unlock()
		s.Job.Plan = p
		s.Job.Details.Pipeline = pipeline
	}
	job.ReportStats = func(stats RuntimeStats) {
		m.mu.Lock()
		defer m.mu.Unlock()
		s.Runtime = stats
	}
	s.Job.Report = job.Report
	s.Job.ReportStats = job.ReportStats
	go func() {
		defer m.wg.Done()
		err := m.worker.Run(workerCtx, job)
		m.mu.Lock()
		defer m.mu.Unlock()
		m.active-- // includes cancelling processes until Run has reaped them
		s.Done, s.Err = true, err
		if err != nil && !s.closing {
			logging.Error("stream failed: %s (%s): %v", filepath.Base(job.Path), job.Plan.Mode, err)
		}
		if s.closing || err != nil {
			m.remove(s)
		}
	}()
	return s.ManagedStream, nil
}

func (m *Manager) viewerCount() int {
	n := 0
	for _, s := range m.streams {
		n += len(s.leases)
	}
	return n
}

func (m *Manager) Touch(key, viewer string) (ManagedStream, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.streams[key]
	if s == nil || s.closing {
		return ManagedStream{}, os.ErrNotExist
	}
	expires, ok := s.leases[viewer]
	if !ok || !m.clock.Now().Before(expires) {
		return ManagedStream{}, os.ErrNotExist
	}
	s.leases[viewer] = m.clock.Now().Add(m.settings.Lease())
	return s.ManagedStream, nil
}

func (m *Manager) Release(key, viewer string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.streams[key]; s != nil {
		delete(s.leases, viewer)
		if len(s.leases) == 0 {
			m.stop(s)
		}
	}
}

func (m *Manager) Sweep() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock.Now()
	for _, s := range m.streams {
		for viewer, expires := range s.leases {
			if !now.Before(expires) {
				delete(s.leases, viewer)
				logging.Info("stream stopped: viewer lease expired for %s", filepath.Base(s.Job.Path))
			}
		}
		if len(s.leases) == 0 {
			m.stop(s)
		}
	}
}

// Serve sweeps at most five seconds after expiry; the runner's bounded kill
// and reap follows. Explicit Release starts teardown immediately.
func (m *Manager) Serve(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.Sweep()
		}
	}
}

func (m *Manager) stop(s *managed) {
	s.closing = true
	s.cancel()
	if s.Done {
		m.remove(s)
	}
}
func (m *Manager) remove(s *managed) {
	if s.Job.Plan.Mode != "direct" {
		_ = os.RemoveAll(s.Job.OutputDir)
	}
	delete(m.streams, s.key)
}

func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	for _, s := range m.streams {
		m.stop(s)
	}
	m.mu.Unlock()
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(closeTimeout):
		m.mu.Lock()
		abandoned := m.active
		m.mu.Unlock()
		logging.Warn("playback: %d FFmpeg process(es) did not stop within %s at shutdown and were abandoned", abandoned, closeTimeout)
	}
	return m.unlock()
}

// closeTimeout bounds how long shutdown waits for stream workers to stop.
var closeTimeout = 10 * time.Second

func (m *Manager) Counts() (processes, renditions, viewers int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active, len(m.streams), m.viewerCount()
}

// Inspect observes an existing viewer without renewing its lease.
func (m *Manager) Inspect(key, viewer string) (ManagedStream, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.streams[key]
	if s == nil || s.closing || !m.clock.Now().Before(s.leases[viewer]) || s.Err != nil {
		return ManagedStream{}, os.ErrNotExist
	}
	return s.ManagedStream, nil
}
