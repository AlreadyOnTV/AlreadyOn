// host-metrics is an optional local-only network sampler for containerized
// servers. It writes one atomic JSON file, opens no network listener, and does
// not change kernel policy. The server uses only its network readings.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/alreadyonapp/alreadyon/server/internal/metrics"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	output := flag.String("output", "/opt/apps/alreadyon/host-metrics.json", "shared sample file")
	once := flag.Bool("once", false, "print one sample after a one-second measurement")
	flag.Parse()
	sampler := metrics.NewSampler("")
	defer sampler.Close()
	sampler.Read(time.Now())
	time.Sleep(time.Second)
	emit := func() error {
		raw, err := json.Marshal(sampler.Read(time.Now()))
		if err != nil {
			return err
		}
		if *once {
			fmt.Println(string(raw))
			return nil
		}
		file, err := os.CreateTemp(filepath.Dir(*output), ".host-metrics-")
		if err != nil {
			return err
		}
		name := file.Name()
		defer os.Remove(name)
		if _, err = file.Write(raw); err != nil {
			file.Close()
			return err
		}
		if err = file.Chmod(0644); err != nil {
			file.Close()
			return err
		}
		if err = file.Close(); err != nil {
			return err
		}
		return os.Rename(name, *output)
	}
	if err := emit(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *once {
		return
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-stop:
			return
		case <-timer.C:
			if err := emit(); err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
		}
	}
}
