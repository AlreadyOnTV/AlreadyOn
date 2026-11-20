// Package status tracks what the server's background library work is
// doing right now — the startup pipeline (#107) and later refreshes —
// so the web UI can show progress instead of an unexplained empty guide
// (#108). State is in memory only and resets on restart.
package status

import (
	"sync"
	"time"
)

// Phase is the step the current (or most recent) run is on.
type Phase string

const (
	// PhasePending: nothing has started yet (only before main begins the
	// startup run).
	PhasePending      Phase = "pending"
	PhaseScanning     Phase = "scanning"
	PhaseBenchmarking Phase = "benchmarking"
	PhaseClassifying  Phase = "classifying"
	PhasePopulating   Phase = "populating"
	PhaseScheduling   Phase = "scheduling"
	// PhaseReady: the last run finished cleanly.
	PhaseReady Phase = "ready"
	// PhaseFailed: the last run recorded at least one error (see
	// Snapshot.LastError); it still ran to the end.
	PhaseFailed Phase = "failed"
)

// Trigger says what started a run.
type Trigger string

const (
	TriggerStartup Trigger = "startup"
	TriggerRefresh Trigger = "refresh"
	TriggerPoll    Trigger = "poll"
)

// Snapshot is a point-in-time copy of the tracker's state.
type Snapshot struct {
	Phase   Phase
	Active  bool
	Trigger Trigger
	// StartupComplete is true once the first startup run has ended
	// (cleanly or not); the guide's first-run placeholder keys off it.
	StartupComplete bool
	// MediaRoots is how many usable media folders are configured.
	MediaRoots int
	StartedAt  *time.Time
	FinishedAt *time.Time
	LastError  string
}

// Tracker records the state of background library runs. It is safe for
// concurrent use. Runs never overlap (see internal/refresh's lock), so a
// Begin while another run is active simply replaces it.
type Tracker struct {
	now func() time.Time

	mu      sync.Mutex
	s       Snapshot
	errored bool
}

// Default is the process-wide tracker shared by main, internal/refresh,
// and the HTTP status endpoint.
var Default = New(time.Now)

// New returns a Tracker in PhasePending that reads time from now.
func New(now func() time.Time) *Tracker {
	return &Tracker{now: now, s: Snapshot{Phase: PhasePending}}
}

// SetMediaRoots records how many usable media folders are configured.
func (t *Tracker) SetMediaRoots(n int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.s.MediaRoots = n
}

// TryBegin is Begin, but does nothing and returns false if a run is
// already active — for work that isn't serialized by the refresh lock
// and mustn't clobber another run's state.
func (t *Tracker) TryBegin(trigger Trigger, phase Phase) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.s.Active {
		return false
	}
	t.begin(trigger, phase)
	return true
}

// Begin starts a run at phase, clearing the previous run's timing and
// error.
func (t *Tracker) Begin(trigger Trigger, phase Phase) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.begin(trigger, phase)
}

func (t *Tracker) begin(trigger Trigger, phase Phase) {
	started := t.now()
	t.s.Active = true
	t.s.Trigger = trigger
	t.s.Phase = phase
	t.s.StartedAt = &started
	t.s.FinishedAt = nil
	t.s.LastError = ""
	t.errored = false
}

// SetPhase moves the active run to phase. No-op when no run is active.
func (t *Tracker) SetPhase(phase Phase) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.s.Active {
		t.s.Phase = phase
	}
}

// RecordError notes a failure in the active run without stopping it; the
// run ends as PhaseFailed. Only the latest message is kept.
func (t *Tracker) RecordError(err error) {
	if err == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.s.Active {
		t.errored = true
		t.s.LastError = err.Error()
	}
}

// End finishes the active run: PhaseFailed if RecordError was called,
// else PhaseReady. Ending a startup run marks StartupComplete.
func (t *Tracker) End() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.s.Active {
		return
	}
	finished := t.now()
	t.s.Active = false
	t.s.FinishedAt = &finished
	if t.errored {
		t.s.Phase = PhaseFailed
	} else {
		t.s.Phase = PhaseReady
	}
	if t.s.Trigger == TriggerStartup {
		t.s.StartupComplete = true
	}
}

// Snapshot returns a copy of the current state.
func (t *Tracker) Snapshot() Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.s
	if s.StartedAt != nil {
		v := *s.StartedAt
		s.StartedAt = &v
	}
	if s.FinishedAt != nil {
		v := *s.FinishedAt
		s.FinishedAt = &v
	}
	return s
}
