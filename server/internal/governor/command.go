package governor

import (
	"context"
	"os/exec"
	"time"
)

// ProcessCPUTime returns CPU time consumed by one process and all of its
// threads. Playback uses the same Linux accounting as the background governor
// so its diagnostics never substitute whole-system CPU utilization.
func ProcessCPUTime(pid int) (time.Duration, error) { return processCPU(pid) }

// RunCommand applies a ticket to a single-process FFmpeg/ffprobe command.
// nice and ionice exec that command in place; shell pipelines are unsupported.
// Wait supplies final CPU accounting so short-lived processes also pay for
// execution that fell between samples.
func RunCommand(ctx context.Context, cmd *exec.Cmd) error {
	t := TicketFrom(ctx)
	if t == nil {
		if err := cmd.Start(); err != nil {
			return err
		}
		observeCommand(ctx, cmd.Process.Pid)
		return cmd.Wait()
	}
	// Probes reached through library analysis need the same scheduling policy
	// as loudness/keyframes. Existing nice/ionice wrappers are not duplicated.
	if cmd.Args[0] != "nice" {
		path, err := exec.LookPath("nice")
		if err != nil {
			return err
		}
		cmd.Args = append([]string{"nice", "-n", "19", "ionice", "-c", "3"}, cmd.Args...)
		cmd.Path = path
	}
	configureCommand(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	observeCommand(ctx, cmd.Process.Pid)
	detach := t.Attach(cmd.Process.Pid)
	defer detach()
	err := cmd.Wait()
	if cmd.ProcessState != nil {
		t.g.mu.Lock()
		cpu := cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime()
		if t.g.budget && cpu > t.cpu {
			t.g.credit -= cpu - t.cpu
			t.g.charged += cpu - t.cpu
			t.cpu = cpu
		}
		t.g.mu.Unlock()
	}
	return err
}

// WithCommandObserver exposes only the PID after successful process creation.
// Benchmark telemetry uses it without racing exec.Cmd.Start or bypassing pacing.
type commandObserverKey struct{}

func WithCommandObserver(ctx context.Context, f func(int)) context.Context {
	return context.WithValue(ctx, commandObserverKey{}, f)
}
func observeCommand(ctx context.Context, pid int) {
	if f, ok := ctx.Value(commandObserverKey{}).(func(int)); ok {
		f(pid)
	}
}
