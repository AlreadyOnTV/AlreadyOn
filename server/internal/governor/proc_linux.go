//go:build linux

package governor

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/metrics"
)

var resourceReader struct {
	sync.Mutex
	sampler metrics.ResourceSampler
}

// ReadProc combines host CPU/memory/swap counters with physical-disk busy time
// and network-namespace throughput. Run subtracts I/O wait from CPU execution.
func ReadProc() (Sample, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return Sample{}, err
	}
	defer f.Close()
	line, err := bufio.NewReader(f).ReadString('\n')
	if err != nil {
		return Sample{}, err
	}
	s, err := parseProcStat(line)
	if err != nil {
		return s, err
	}
	s.MemAvailable, s.MemTotal = readMemory()
	if s.MemTotal == 0 {
		return s, fmt.Errorf("host memory metrics unavailable")
	}
	raw, err := os.ReadFile("/proc/vmstat")
	if err != nil {
		return s, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		n, _ := strconv.ParseUint(f[1], 10, 64)
		if f[0] == "pswpin" {
			s.SwapIn = n
		}
		if f[0] == "pswpout" {
			s.SwapOut = n
		}
	}
	// Do not interpret missing pressure counters as an idle machine.
	for _, path := range []string{"/proc/diskstats", "/proc/net/dev"} {
		if _, err := os.ReadFile(path); err != nil {
			return s, err
		}
	}
	resourceReader.Lock()
	s.DiskUtil, s.NetworkMbps = resourceReader.sampler.Read(time.Now())
	resourceReader.Unlock()
	return s, nil
}

// readMemory returns MemAvailable and MemTotal in kilobytes, or zeros when
// /proc/meminfo is unreadable (ReadProc then holds work for missing metrics).
func readMemory() (available, total uint64) {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "MemAvailable:":
			fmt.Sscanf(fields[1], "%d", &available)
		case "MemTotal:":
			fmt.Sscanf(fields[1], "%d", &total)
		}
	}
	return available, total
}

func parseProcStat(line string) (Sample, error) {
	fields := strings.Fields(line)
	if len(fields) < 8 || fields[0] != "cpu" {
		return Sample{}, fmt.Errorf("governor: unexpected /proc/stat line %q", line)
	}
	var v [8]uint64 // user nice system idle iowait irq softirq steal
	for i := range v {
		if _, err := fmt.Sscanf(fields[i+1], "%d", &v[i]); err != nil {
			return Sample{}, fmt.Errorf("governor: bad /proc/stat field %q", fields[i+1])
		}
	}
	busy := v[0] + v[1] + v[2] + v[4] + v[5] + v[6] + v[7]
	return Sample{Busy: busy, Total: busy + v[3], IOWait: v[4]}, nil
}

func pauseGroup(pgid int)  { _ = syscall.Kill(-pgid, syscall.SIGSTOP) }
func resumeGroup(pgid int) { _ = syscall.Kill(-pgid, syscall.SIGCONT) }
