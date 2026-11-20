package playback

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/governor"
	"github.com/alreadyonapp/alreadyon/server/internal/library"
)

type frameProgress struct {
	mu     sync.Mutex
	frames int64
	err    error
	exited bool
}

func (p *frameProgress) snapshot() (int64, error, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.frames, p.err, p.exited
}

// measureStreams observes a shared steady-state wall-clock window after every
// stream has warmed. Frame counters avoid mistaking audio buffering for video
// throughput. Every stream must pass; averaging can hide one starving viewer.
func measureStreams(ctx context.Context, sample string, probe library.ProbeResult, plan Plan, threads, count int, warmup, window time.Duration) ([]float64, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	dir, err := os.MkdirTemp("", "alreadyon-calibration-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	progress := make([]*frameProgress, count)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	for index := range progress {
		out, err := os.MkdirTemp(dir, "stream-")
		if err != nil {
			return nil, err
		}
		job := Job{Path: sample, Source: probe, Plan: plan, Threads: threads, OutputDir: out, Duration: time.Hour}
		if plan.Encoder != "" && plan.Encoder != EncoderNvidiaNVENC {
			job.Device = RenderNode()
		}
		args, err := Arguments(job)
		if err != nil {
			return nil, err
		}
		fast := []string{"-progress", "pipe:1", "-stats_period", "0.2", "-nostats"}
		for i := 0; i < len(args); i++ {
			if args[i] == "-readrate" || args[i] == "-readrate_initial_burst" {
				i++
				continue
			}
			if args[i] == "-i" {
				fast = append(fast, "-stream_loop", "-1")
			}
			fast = append(fast, args[i])
		}
		cmd := lowPriority(ctx, "ffmpeg", fast...)
		// An in-memory pipe also closes when admission is cancelled before exec.
		pipe, writer := io.Pipe()
		cmd.Stdout = writer
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		state := &frameProgress{}
		progress[index] = state
		workers.Add(2)
		go func() {
			defer workers.Done()
			defer pipe.Close()
			scanner := bufio.NewScanner(pipe)
			for scanner.Scan() {
				if value, ok := strings.CutPrefix(scanner.Text(), "frame="); ok {
					frames, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
					if err == nil {
						state.mu.Lock()
						state.frames = frames
						state.mu.Unlock()
					}
				}
			}
		}()
		go func() {
			defer workers.Done()
			defer writer.Close()
			err := governor.BenchmarkCommand(ctx, cmd)
			state.mu.Lock()
			state.exited = true
			if ctx.Err() == nil {
				state.err = fmt.Errorf("calibration process: %v: %.800s", err, stderr.String())
			}
			state.mu.Unlock()
		}()
	}
	readyDeadline := time.NewTimer(8 * time.Second)
	defer readyDeadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		all := true
		for _, p := range progress {
			frames, err, exited := p.snapshot()
			if exited {
				return nil, err
			}
			all = all && frames > 0
		}
		if all {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-readyDeadline.C:
			return nil, fmt.Errorf("calibration pipeline did not warm up")
		case <-tick.C:
		}
	}
	wait := func(duration time.Duration) error {
		timer := time.NewTimer(duration)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}
	if err := wait(warmup); err != nil {
		return nil, err
	}
	initial := make([]int64, count)
	for i, p := range progress {
		initial[i], _, _ = p.snapshot()
	}
	started := time.Now()
	if err := wait(window); err != nil {
		return nil, err
	}
	elapsed := time.Since(started).Seconds()
	speeds := make([]float64, count)
	for i, p := range progress {
		frames, err, exited := p.snapshot()
		if exited {
			return nil, err
		}
		speeds[i] = float64(frames-initial[i]) / plan.FPS / elapsed
	}
	return speeds, nil
}
