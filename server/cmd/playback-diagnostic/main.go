// Command playback-diagnostic compares real media pipelines without a database,
// account changes, or a server deployment. All generated segments are disposable.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/playback"
)

func main() {
	defer func() {
		if err := recover(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}()
	run()
}

func run() {
	file := flag.String("file", "", "absolute read-only source path")
	encoder := flag.String("encoder", "", "intel-qsv, intel-vaapi, or empty for software")
	threads := flag.Int("threads", 2, "CPU decode/filter budget (1 to 32)")
	height := flag.Int("height", 1080, "output height (720 or 1080)")
	tone := flag.String("tone-map", "cpu", "cpu or gpu")
	seconds := flag.Int("seconds", 20, "bounded excerpt duration (2 to 60 seconds)")
	offset := flag.Int("offset", 0, "source offset seconds")
	capacity := flag.Bool("capacity", false, "measure synthetic HDR capacity instead of a file")
	flag.Parse()
	if *threads < 1 || *threads > 32 || (*encoder != "" && !playback.IsHardwareEncoder(*encoder)) || (*tone != "cpu" && *tone != "gpu") || (*height != 720 && *height != 1080) || *seconds < 2 || *seconds > 60 || *offset < 0 {
		panic("invalid diagnostic options")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if *capacity {
		c := &playback.Capacity{Context: ctx, Hardware: &playback.Hardware{}, Encoder: func(context.Context) string { return *encoder }, HardwareDecoding: func(context.Context) bool { return *encoder != "" }, Threads: func() int { return *threads }}
		c.Start(true)
		for c.Status().State == "running" && ctx.Err() == nil {
			time.Sleep(time.Second)
		}
		json.NewEncoder(os.Stdout).Encode(c.Status())
		if len(c.Status().HDR) == 0 {
			os.Exit(1)
		}
		return
	}
	probe, err := library.Probe(ctx, *file)
	must(err)
	caps := playback.Capabilities{HLS: true, Mbps: 100, Encoder: *encoder, HWDecode: *encoder != "", HWToneMap: *tone == "gpu", MaxHeight: *height,
		Video: []playback.VideoCapability{{Codec: "h264", Profile: "High", Level: 42, Width: 1920, Height: 1080, FPS: 60, Bitrate: 20000000, PixelFormat: "yuv420p"}},
		Audio: []playback.AudioCapability{{Codec: "aac", Channels: 2, SampleRate: 48000, Bitrate: 320000}}}
	plan, err := playback.Choose(probe, caps, false, true)
	must(err)
	dir, err := os.MkdirTemp("", "alreadyon-hdr-diagnostic-")
	must(err)
	defer os.RemoveAll(dir)
	job := playback.Job{Path: *file, Source: probe, Plan: plan, Offset: time.Duration(*offset) * time.Second, Duration: time.Duration(*seconds) * time.Second, OutputDir: dir, Threads: *threads, Device: playback.RenderNode()}
	args, err := playback.Arguments(job)
	must(err)
	var fast []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-readrate" || args[i] == "-readrate_initial_burst" {
			i++
			continue
		}
		fast = append(fast, args[i])
	}
	runCtx, stop := context.WithTimeout(ctx, 2*time.Minute)
	defer stop()
	cmd := exec.CommandContext(runCtx, "ffmpeg", fast...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	start := time.Now()
	must(cmd.Start())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	first := 0.0
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			if err != nil {
				fmt.Fprintln(os.Stderr, stderr.String())
				must(err)
			}
			elapsed := time.Since(start).Seconds()
			files, _ := filepath.Glob(filepath.Join(dir, "*.ts"))
			if len(files) == 0 {
				panic("no HLS segments")
			}
			// Decode the produced video/audio to verify usable output, not only a zero exit.
			check := exec.CommandContext(runCtx, "ffmpeg", "-v", "error", "-i", filepath.Join(dir, "index.m3u8"), "-f", "null", "-")
			if out, err := check.CombinedOutput(); err != nil {
				fmt.Fprintln(os.Stderr, string(out))
				must(err)
			}
			json.NewEncoder(os.Stdout).Encode(map[string]any{"pipeline": playback.DescribePipeline(plan), "height": plan.Height, "fps": plan.FPS, "firstSegmentSeconds": first, "elapsedSeconds": elapsed, "wallRate": float64(*seconds) / elapsed, "segments": len(files), "decoded": true})
			return
		case <-ticker.C:
			if first == 0 {
				files, _ := filepath.Glob(filepath.Join(dir, "*.ts"))
				if len(files) > 0 {
					first = time.Since(start).Seconds()
				}
			}
		}
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
