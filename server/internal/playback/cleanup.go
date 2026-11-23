package playback

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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

type AdmissionPolicy func(active []Job, candidate Job) error

type ManagedStream struct {
	Job     Job
	Runtime RuntimeStats
	Done    bool
	Err     error
}

type streamLease struct {
	expires           time.Time
	autoMax           int
	oldKey, oldViewer string
}

type managed struct {
	ManagedStream
	key         string
	cost        int
	leases      map[string]streamLease
	cancel      context.CancelFunc
	closing     bool
	replaces    *managed
	replacement *managed
}

// Manager owns private output, workers and viewer leases. Its clock and worker
// are injectable; Sweep can be called deterministically without timer sleeps.
type Manager struct {
	mu                  sync.Mutex
	root                string
	unlock              func() error
	clock               schedule.Clock
	worker              Worker
	admission           AdmissionPolicy
	settings            Settings
	streams             map[string]*managed
	active              int
	activeCost          int
	autoPressureUntil   time.Time
	autoCeiling         int
	autoPressureViewers int
	closed              bool
	wg                  sync.WaitGroup
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

func (m *Manager) SetAdmissionPolicy(policy AdmissionPolicy) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.admission = policy
}

// Acquire deduplicates concurrent starts using a server-derived rendition key.
// viewer is the opaque session ID, never a user-chosen channel name or path.
// A successful acquisition persists beyond its HTTP request via a lease.
func (m *Manager) Acquire(ctx context.Context, key, viewer string, job Job) (ManagedStream, error) {
	return m.acquire(ctx, key, viewer, "", "", job)
}

// AcquireReplacement substitutes an exclusively leased rendition during
// admission while leaving it running until the client switches over.
func (m *Manager) AcquireReplacement(ctx context.Context, key, viewer, oldKey, oldViewer string, job Job) (ManagedStream, error) {
	return m.acquire(ctx, key, viewer, oldKey, oldViewer, job)
}

func (m *Manager) acquire(ctx context.Context, key, viewer, oldKey, oldViewer string, job Job) (ManagedStream, error) {
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
	lease := streamLease{expires: m.clock.Now().Add(m.settings.Lease()), autoMax: job.AutoQualityMax, oldKey: oldKey, oldViewer: oldViewer}
	if s := m.streams[key]; s != nil {
		if s.closing || s.Err != nil {
			return ManagedStream{}, ErrBusy
		}
		if _, exists := s.leases[viewer]; !exists && s.replacement != nil {
			return ManagedStream{}, ErrBusy
		}
		if _, exists := s.leases[viewer]; !exists && m.viewerCount() >= m.settings.MaxViewers {
			return ManagedStream{}, ErrBusy
		}
		s.leases[viewer] = lease
		return s.ManagedStream, nil
	}
	cost := jobResourceCost(job)
	renditions, viewers, occupiedCost := len(m.streams), m.viewerCount(), m.activeCost
	var replaced *managed
	if old := m.streams[oldKey]; old != nil && oldKey != key && !old.closing && old.Err == nil && old.replacement == nil && len(old.leases) == 1 {
		lease, owned := old.leases[oldViewer]
		if owned && m.clock.Now().Before(lease.expires) {
			replaced = old
			renditions--
			viewers--
			occupiedCost -= old.cost
		}
	}
	if renditions >= m.settings.MaxRenditions || viewers >= m.settings.MaxViewers || (cost > 0 && occupiedCost+cost > m.settings.MaxProcesses) {
		return ManagedStream{}, ErrBusy
	}
	if cost > 0 {
		cpus := min(runtime.NumCPU(), runtime.GOMAXPROCS(0))
		threads := job.Threads
		if threads == 0 {
			threads = DefaultThreads
		}
		if !job.ValidatedThreads {
			job.Threads = FairThreads(threads, cpus, occupiedCost+cost)
		}
	}
	if cost > 0 && m.admission != nil {
		var active []Job
		for _, stream := range m.streams {
			if stream != replaced && stream.cost > 0 && !stream.Done {
				active = append(active, stream.Job)
			}
		}
		if err := m.admission(active, job); err != nil {
			return ManagedStream{}, err
		}
	}
	if job.Plan.Mode != "direct" {
		dir, err := os.MkdirTemp(m.root, "stream-")
		if err != nil {
			return ManagedStream{}, err
		}
		job.OutputDir = dir
	}
	workerCtx, cancel := context.WithCancel(context.Background())
	s := &managed{
		ManagedStream: ManagedStream{Job: job},
		key:           key, cost: cost, cancel: cancel,
		leases: map[string]streamLease{viewer: lease},
	}
	m.streams[key] = s
	if replaced != nil {
		replaced.replacement = s
		s.replaces = replaced
	}
	if job.Plan.Mode == "direct" {
		s.Done = true
		return s.ManagedStream, nil
	}
	m.active++
	m.activeCost += cost
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
		m.activeCost -= s.cost
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

func jobResourceCost(job Job) int {
	if job.Plan.Mode == "direct" || (job.Plan.VideoCopy && job.Plan.AudioCopy) {
		return 0
	}
	if job.Plan.LowHeight > 0 {
		return 2
	}
	return 1
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
	lease, ok := s.leases[viewer]
	if !ok || !m.clock.Now().Before(lease.expires) {
		return ManagedStream{}, os.ErrNotExist
	}
	lease.expires = m.clock.Now().Add(m.settings.Lease())
	s.leases[viewer] = lease
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
		for viewer, lease := range s.leases {
			if !now.Before(lease.expires) {
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
	if s.replaces != nil && s.replaces.replacement == s {
		s.replaces.replacement = nil
	}
	if s.replacement != nil && s.replacement.replaces == s {
		s.replacement.replaces = nil
	}
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

// PauseAll stops every active rendition while keeping the manager available for
// playback after the higher-priority work completes.
func (m *Manager) PauseAll(ctx context.Context) error {
	m.mu.Lock()
	for _, stream := range m.streams {
		m.stop(stream)
	}
	m.mu.Unlock()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, renditions, _ := m.Counts()
		if renditions == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// MarkAutoCapacityPressure asks higher Auto renditions to give up one rung
// when a lower request cannot fit alongside them. The cohort can probe upward
// again after a viewer leaves or the settling window ends.
func (m *Manager) MarkAutoCapacityPressure(requested, floor int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ceiling := m.autoQualityCeilingLocked()
	highest := 0
	for _, stream := range m.streams {
		// A staged replacement temporarily uses resources from both jobs.
		// Clients retry against the settled workload after the old job exits.
		if stream.replaces != nil && !stream.closing && !stream.Done && !stream.replaces.closing && !stream.replaces.Done {
			return
		}
		if stream.closing || stream.Job.Plan.VideoCopy {
			continue
		}
		for _, lease := range stream.leases {
			if lease.autoMax > 0 {
				highest = max(highest, PlanQualityHeight(stream.Job.Plan))
				break
			}
		}
	}
	if ceiling > 0 {
		// Give clients time to poll and stage the current downshift before
		// retries can request another one.
		if m.clock.Now().Before(m.autoPressureUntil.Add(-45 * time.Second)) {
			return
		}
		highest = min(ceiling, highest)
	}
	if highest < requested {
		return // An unsuccessful probe above the cohort needs no downgrade.
	}
	for i := len(QualityHeights) - 1; i >= 0; i-- {
		h := QualityHeights[i]
		if h < highest && h >= floor {
			m.setAutoQualityCeiling(h)
			return
		}
	}
}

// ObserveAutoDownshift shares a player's sustained buffer or decode pressure
// with the other Auto viewers after a lower replacement has been admitted.
func (m *Manager) ObserveAutoDownshift(oldKey, oldViewer, key, viewer string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, next := m.streams[oldKey], m.streams[key]
	if old == nil || next == nil || old.closing || next.closing ||
		old.leases[oldViewer].autoMax == 0 || next.leases[viewer].autoMax == 0 ||
		old.Job.Plan.VideoCopy || next.Job.Plan.VideoCopy ||
		old.Job.Entry.MediaFileID != next.Job.Entry.MediaFileID || !old.Job.Entry.StartsAt.Equal(next.Job.Entry.StartsAt) {
		return
	}
	height := PlanQualityHeight(next.Job.Plan)
	if !ValidAutoHeight(height) || height >= PlanQualityHeight(old.Job.Plan) {
		return
	}
	if ceiling := m.autoQualityCeilingLocked(); ceiling > 0 && height >= ceiling {
		return
	}
	for _, stream := range m.streams {
		if stream.closing || stream.Job.Plan.VideoCopy {
			continue
		}
		for id, lease := range stream.leases {
			if id != oldViewer && id != viewer && lease.autoMax > 0 {
				m.setAutoQualityCeiling(height)
				return
			}
		}
	}
}

func (m *Manager) setAutoQualityCeiling(height int) {
	m.autoCeiling = height
	m.autoPressureViewers = m.settledViewerCount()
	m.autoPressureUntil = m.clock.Now().Add(time.Minute)
}

func (m *Manager) settledViewerCount() int {
	count := m.viewerCount()
	for _, stream := range m.streams {
		for _, lease := range stream.leases {
			if previous := m.streams[lease.oldKey]; previous != nil {
				if _, stillPlaying := previous.leases[lease.oldViewer]; stillPlaying {
					count--
				}
			}
		}
	}
	return count
}

func (m *Manager) autoQualityCeilingLocked() int {
	if m.autoCeiling != 0 && (!m.clock.Now().Before(m.autoPressureUntil) || m.settledViewerCount() < m.autoPressureViewers) {
		m.autoCeiling = 0
	}
	return m.autoCeiling
}

func (m *Manager) AutoQualityCeiling() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	ceiling := m.autoQualityCeilingLocked()
	for _, stream := range m.streams {
		if stream.closing || stream.Job.Plan.VideoCopy {
			continue
		}
		for _, lease := range stream.leases {
			if lease.autoMax > 0 && (ceiling == 0 || lease.autoMax < ceiling) {
				ceiling = lease.autoMax
			}
		}
	}
	return ceiling
}

// TranscodeSlots reports active encode resource slots and the manager's current
// allowed slot limit. Rendition ladders count as two slots; copy-only streams do not.
func (m *Manager) TranscodeSlots() (active, limit int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeCost, m.settings.MaxProcesses
}

// Inspect observes an existing viewer without renewing its lease.
func (m *Manager) Inspect(key, viewer string) (ManagedStream, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.streams[key]
	if s == nil || s.closing || !m.clock.Now().Before(s.leases[viewer].expires) || s.Err != nil {
		return ManagedStream{}, os.ErrNotExist
	}
	return s.ManagedStream, nil
}
