//go:build linux

package governor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The child counter proves a real subprocess stops and resumes, rather than
// merely checking scheduler bookkeeping. It touches only its temporary fixture.
func TestGroupCounterHelper(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "analysis-counter" {
		return
	}
	f, err := os.OpenFile(os.Args[len(os.Args)-1], os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		os.Exit(2)
	}
	defer f.Close()
	for {
		if _, err = f.Write([]byte("x")); err != nil {
			os.Exit(3)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func TestGroupStopsAndResumesAnalysisProcess(t *testing.T) {
	g := Fixed(1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ticket, err := g.Acquire(ctx, PriorityLow)
	if err != nil {
		t.Fatal(err)
	}
	defer ticket.Release()
	file := filepath.Join(t.TempDir(), "counter")
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestGroupCounterHelper$", "--", "analysis-counter", file)
	done := make(chan error, 1)
	go func() { done <- RunCommand(WithTicket(ctx, ticket), cmd) }()
	size := func() int64 {
		st, err := os.Stat(file)
		if err != nil {
			return 0
		}
		return st.Size()
	}
	waitFor(t, "analysis subprocess started", func() bool { return size() > 3 })
	release := g.Reserve(PriorityBenchmark)
	time.Sleep(30 * time.Millisecond)
	paused := size()
	time.Sleep(80 * time.Millisecond)
	if size() != paused {
		release()
		t.Fatal("analysis executed during benchmark")
	}
	release()
	waitFor(t, "analysis resumed", func() bool { return size() > paused+2 })
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("analysis process survived cancellation")
	}
}
