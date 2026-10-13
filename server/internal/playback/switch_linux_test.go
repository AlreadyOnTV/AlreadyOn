//go:build linux

package playback

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
)

func TestRapidSwitchReapsRealFFmpeg(t *testing.T) {
	job := fixture(t)
	root := t.TempDir()
	// exec preserves the recorded PID; checking it afterwards detects both live
	// children and unreaped zombies, independently of the manager's counters.
	wrapper := filepath.Join(root, "ffmpeg-record-pid")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nprintf '%s\\n' \"$$\" >> \""+root+"/pids\"\nexec ffmpeg \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(filepath.Join(root, "cache"), schedule.SystemClock{}, Runner{Executable: wrapper}, DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	for i := 0; i < 20; i++ {
		key := fmt.Sprint(i)
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err = manager.Acquire(context.Background(), key, key, job); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal(err)
			}
			time.Sleep(10 * time.Millisecond)
		}
		for {
			data, _ := os.ReadFile(filepath.Join(root, "pids"))
			if len(strings.Fields(string(data))) == i+1 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("FFmpeg did not start")
			}
			time.Sleep(10 * time.Millisecond)
		}
		time.Sleep(30 * time.Millisecond)
		manager.Release(key, key)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	p, r, v := manager.Counts()
	if p+r+v != 0 {
		t.Fatalf("leaked resources %d/%d/%d", p, r, v)
	}
	data, err := os.ReadFile(filepath.Join(root, "pids"))
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range strings.Fields(string(data)) {
		pid, _ := strconv.Atoi(raw)
		if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
			t.Fatalf("FFmpeg PID %d remains: %v", pid, err)
		}
	}
	files, err := filepath.Glob(filepath.Join(root, "cache", "stream-*"))
	if err != nil || len(files) != 0 {
		t.Fatalf("output leaked: %v %v", files, err)
	}
}
