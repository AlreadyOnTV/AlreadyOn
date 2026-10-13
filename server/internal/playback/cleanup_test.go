package playback

// Manager tests exercise rendition sharing, cleanup, and live worker state.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *testClock) advance(d time.Duration) { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }

type workerFunc func(context.Context, Job) error

func (f workerFunc) Run(ctx context.Context, j Job) error { return f(ctx, j) }

func TestManagerConcurrentAdmissionAndLeaseCleanup(t *testing.T) {
	clock := &testClock{now: time.Now()}
	var active, peak atomic.Int32
	started := make(chan struct{}, 2)
	m, err := NewManager(t.TempDir(), clock, workerFunc(func(ctx context.Context, j Job) error {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}), DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	var wg sync.WaitGroup
	var admitted atomic.Int32
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := m.Acquire(context.Background(), fmt.Sprint(i), fmt.Sprint(i), Job{Plan: Plan{Mode: "transcode"}})
			if err == nil {
				admitted.Add(1)
			} else if !errors.Is(err, ErrBusy) {
				t.Errorf("%v", err)
			}
		}(i)
	}
	wg.Wait()
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("worker did not start")
		}
	}
	if admitted.Load() != 2 || peak.Load() != 2 {
		t.Fatalf("admitted=%d peak=%d", admitted.Load(), peak.Load())
	}
	clock.advance(30 * time.Second)
	m.Sweep()
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	p, r, v := m.Counts()
	if p != 0 || r != 0 || v != 0 || active.Load() != 0 {
		t.Fatalf("leaked %d/%d/%d", p, r, v)
	}
}

func TestManagerSharesAndHoldsSlotUntilReaped(t *testing.T) {
	clock := &testClock{now: time.Now()}
	cancelled := make(chan struct{})
	reap := make(chan struct{})
	s := DefaultSettings()
	s.MaxProcesses = 1
	m, err := NewManager(t.TempDir(), clock, workerFunc(func(ctx context.Context, j Job) error { <-ctx.Done(); close(cancelled); <-reap; return ctx.Err() }), s)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	a, err := m.Acquire(context.Background(), "channel/plan", "viewer1", Job{Plan: Plan{Mode: "transcode"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Acquire(context.Background(), "channel/plan", "viewer2", Job{Plan: Plan{Mode: "transcode"}})
	if err != nil || a.Job.OutputDir != b.Job.OutputDir {
		t.Fatal("did not share")
	}
	m.Release("channel/plan", "viewer1")
	if _, err := m.Touch("channel/plan", "viewer2"); err != nil {
		t.Fatal(err)
	}
	m.Release("channel/plan", "viewer2")
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("release did not cancel")
	}
	if _, err := m.Acquire(context.Background(), "other", "viewer3", Job{Plan: Plan{Mode: "transcode"}}); !errors.Is(err, ErrBusy) {
		t.Fatal("slot released before reap")
	}
	close(reap)
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.Job.OutputDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("output leaked")
	}
}

func TestManagerPublishesWorkerRuntimeStats(t *testing.T) {
	clock := &testClock{now: time.Now()}
	reported := make(chan struct{})
	m, err := NewManager(t.TempDir(), clock, workerFunc(func(ctx context.Context, job Job) error {
		cpu := 37.5
		job.ReportStats(RuntimeStats{CPUUtil: &cpu})
		close(reported)
		<-ctx.Done()
		return ctx.Err()
	}), DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err := m.Acquire(context.Background(), "runtime", "viewer", Job{Plan: Plan{Mode: "transcode"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reported:
	case <-time.After(time.Second):
		t.Fatal("worker did not report runtime stats")
	}
	stream, err := m.Touch("runtime", "viewer")
	if err != nil || stream.Runtime.CPUUtil == nil || *stream.Runtime.CPUUtil != 37.5 {
		t.Fatalf("runtime = %+v, %v", stream.Runtime, err)
	}
}

func TestManagerCacheLockSweepAndDirect(t *testing.T) {
	root := t.TempDir()
	clock := &testClock{now: time.Now()}
	if err := os.Mkdir(filepath.Join(root, "stream-abandoned"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "keep"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	worker := workerFunc(func(context.Context, Job) error { t.Error("direct play spawned worker"); return nil })
	m, err := NewManager(root, clock, worker, DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err := os.Stat(filepath.Join(root, "stream-abandoned")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("abandoned cache retained")
	}
	if _, err := os.Stat(filepath.Join(root, "keep")); err != nil {
		t.Fatal("unrelated file removed")
	}
	if other, err := NewManager(root, clock, worker, DefaultSettings()); err == nil {
		other.Close()
		t.Fatal("second owner accepted")
	}
	if _, err := m.Acquire(context.Background(), "direct", "viewer", Job{Plan: Plan{Mode: "direct"}}); err != nil {
		t.Fatal(err)
	}
	p, _, _ := m.Counts()
	if p != 0 {
		t.Fatal("direct used process slot")
	}
	clock.advance(30 * time.Second)
	if _, err := m.Touch("direct", "viewer"); err == nil {
		t.Fatal("expired lease resurrected")
	}
	m.Sweep()
	_, r, v := m.Counts()
	if r != 0 || v != 0 {
		t.Fatal("direct lease leaked")
	}
}

func TestPlaybackSettingsPersistAndValidate(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := GetSettings(context.Background(), db.DB)
	if err != nil || s != DefaultSettings() {
		t.Fatalf("%+v %v", s, err)
	}
	s.MaxProcesses = 4
	if err := SaveSettings(context.Background(), db.DB, s); err != nil {
		t.Fatal(err)
	}
	got, err := GetSettings(context.Background(), db.DB)
	if err != nil || got != s {
		t.Fatalf("%+v %v", got, err)
	}
	s.MaxProcesses = 0
	if err := SaveSettings(context.Background(), db.DB, s); err == nil {
		t.Fatal("unbounded setting accepted")
	}
}

func TestCloseIsBoundedWhenAWorkerIgnoresCancellation(t *testing.T) {
	old := closeTimeout
	closeTimeout = 150 * time.Millisecond
	t.Cleanup(func() { closeTimeout = old })
	started, release := make(chan struct{}), make(chan struct{})
	m, err := NewManager(t.TempDir(), &testClock{now: time.Now()}, workerFunc(func(ctx context.Context, j Job) error {
		close(started)
		<-release // a hung FFmpeg that never honors the cancel
		return nil
	}), DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Acquire(context.Background(), "k", "v", Job{Plan: Plan{Mode: "transcode"}}); err != nil {
		t.Fatal(err)
	}
	<-started
	done := make(chan error, 1)
	go func() { done <- m.Close() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close waited on a worker that never stopped")
	}
	close(release)
}
