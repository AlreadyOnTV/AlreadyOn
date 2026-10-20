package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

func write(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

// procStat renders /proc/stat with the given cumulative busy/idle ticks per core.
func procStat(busy, idle []uint64) string {
	var b strings.Builder
	var totalBusy, totalIdle uint64
	for i := range busy {
		totalBusy += busy[i]
		totalIdle += idle[i]
	}
	fmt.Fprintf(&b, "cpu  %d 0 0 %d 0 0 0 0 0 0\n", totalBusy, totalIdle)
	for i := range busy {
		fmt.Fprintf(&b, "cpu%d %d 0 0 %d 0 0 0 0 0 0\n", i, busy[i], idle[i])
	}
	return b.String() + "intr 1\n"
}

func TestCPUListParsing(t *testing.T) {
	got := parseCPUList("0-2,5,7-8\n")
	if len(got) != 6 || !got[0] || !got[2] || !got[5] || !got[8] || got[3] {
		t.Fatal(got)
	}
	for _, invalid := range []string{"", "x", "3-1"} {
		if parseCPUList(invalid) != nil {
			t.Fatalf("%q should be unknown", invalid)
		}
	}
	root := t.TempDir()
	if allowedCPUs(root) != nil {
		t.Fatal("missing cpuset must include every CPU")
	}
	write(t, root, map[string]string{"cpuset/cpuset.effective_cpus": "1,3"})
	if got := allowedCPUs(root); len(got) != 2 || !got[1] || !got[3] {
		t.Fatal("cgroup v1 cpuset", got)
	}
	write(t, root, map[string]string{"cpuset.cpus.effective": "0-1"})
	if got := allowedCPUs(root); len(got) != 2 || !got[0] || !got[1] {
		t.Fatal("cgroup v2 cpuset takes precedence", got)
	}
}

func TestPerCoreCPUUtilization(t *testing.T) {
	for _, cores := range []int{1, 4, 64} {
		var c cpuCounter
		busy, idle := make([]uint64, cores), make([]uint64, cores)
		if aggregate, got := c.sample(procStat(busy, idle), nil); aggregate != nil || len(got) != cores || got[0].Utilization != nil {
			t.Fatalf("%d cores: first sample must be unavailable: %v %+v", cores, aggregate, got)
		}
		want := 0.0
		for i := range busy {
			// Core i is busy for (i*100/cores)% of 100 ticks.
			busy[i] = uint64(i * 100 / cores)
			idle[i] = 100 - busy[i]
			want += float64(busy[i])
		}
		aggregate, got := c.sample(procStat(busy, idle), nil)
		if len(got) != cores || aggregate == nil || !near(*aggregate, want/float64(cores)) {
			t.Fatalf("%d cores: aggregate %v, want %v", cores, aggregate, want/float64(cores))
		}
		for i, core := range got {
			if core.Core != i || core.Utilization == nil || !near(*core.Utilization, float64(busy[i])) {
				t.Fatalf("%d cores: core %d = %+v", cores, i, core)
			}
		}
	}
}

func TestCPUSetAndAggregateFallback(t *testing.T) {
	var c cpuCounter
	allowed := map[int]bool{1: true, 3: true}
	c.sample(procStat([]uint64{0, 0, 0, 0}, []uint64{0, 0, 0, 0}), allowed)
	aggregate, cores := c.sample(procStat([]uint64{100, 20, 100, 60}, []uint64{0, 80, 0, 40}), allowed)
	if len(cores) != 2 || cores[0].Core != 1 || cores[1].Core != 3 || !near(*aggregate, 40) {
		t.Fatalf("cpuset must exclude host CPUs: %v %+v", *aggregate, cores)
	}
	// A counter reset leaves that core, and so the aggregate, unavailable.
	if aggregate, cores = c.sample(procStat([]uint64{0, 10, 0, 70}, []uint64{0, 0, 0, 50}), allowed); aggregate != nil || cores[0].Utilization != nil || cores[1].Utilization == nil {
		t.Fatalf("reset core: %v %+v", aggregate, cores)
	}
	var fallback cpuCounter
	fallback.sample("cpu  10 0 0 10 0 0 0 0\n", nil)
	aggregate, cores = fallback.sample("cpu  40 0 0 80 0 0 0 0\n", nil)
	if cores != nil || aggregate == nil || !near(*aggregate, 30) {
		t.Fatalf("without per-core lines the kernel total is used: %v %+v", aggregate, cores)
	}
}

func TestLoadAndPressure(t *testing.T) {
	root := t.TempDir()
	if loadAverage(root) != nil || pressure(filepath.Join(root, "pressure", "cpu")) != nil {
		t.Fatal("unsupported kernels must report unavailable, not zero")
	}
	write(t, root, map[string]string{
		"loadavg":      "1.25 0.80 0.50 2/300 1234\n",
		"pressure/cpu": "some avg10=3.50 avg60=1.00 avg300=0.20 total=100\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n",
		"pressure/io":  "some avg10=12.25 avg60=4.00 avg300=1.00 total=100\n",
	})
	if got := loadAverage(root); got == nil || *got != 1.25 {
		t.Fatal("load", got)
	}
	if got := pressure(filepath.Join(root, "pressure", "cpu")); got == nil || *got != 3.5 {
		t.Fatal("cpu pressure", got)
	}
	if got := pressure(filepath.Join(root, "pressure", "io")); got == nil || *got != 12.25 {
		t.Fatal("io pressure", got)
	}
}

func TestMemoryUsage(t *testing.T) {
	proc := t.TempDir()
	write(t, proc, map[string]string{"meminfo": "MemTotal:       16000000 kB\nMemAvailable:    4000000 kB\n"})
	host := int64(16000000 * 1024)
	native, ok := memoryUsage(proc, t.TempDir(), false)
	if !ok || native.limited || native.limit != host || native.used != host-4000000*1024 {
		t.Fatalf("native: %+v", native)
	}
	limited := t.TempDir()
	write(t, limited, map[string]string{"memory.max": "2147483648\n", "memory.current": "1300000000\n", "memory.stat": "anon 1\ninactive_file 300000000\n"})
	if got, ok := memoryUsage(proc, limited, true); !ok || !got.limited || got.limit != 2147483648 || got.used != 1000000000 {
		t.Fatalf("limited v2: %+v", got)
	}
	unlimited := t.TempDir()
	write(t, unlimited, map[string]string{"memory.max": "max\n", "memory.current": "500000000\n"})
	if got, ok := memoryUsage(proc, unlimited, true); !ok || got.limited || got.limit != host || got.used != 500000000 {
		t.Fatalf("unlimited container uses host RAM as its denominator: %+v", got)
	}
	v1 := t.TempDir()
	write(t, v1, map[string]string{"memory/memory.limit_in_bytes": "9223372036854771712\n", "memory/memory.usage_in_bytes": "800\n", "memory/memory.stat": "total_inactive_file 900\n"})
	if got, ok := memoryUsage(proc, v1, true); !ok || got.limited || got.used != 0 {
		t.Fatalf("v1 page-rounded unlimited value and clamped usage: %+v", got)
	}
	if got, ok := memoryUsage(proc, t.TempDir(), true); !ok || got.limited || got.limit != host {
		t.Fatalf("unreadable cgroup falls back to host memory: %+v", got)
	}
	if _, ok := memoryUsage(t.TempDir(), limited, false); ok {
		t.Fatal("missing meminfo must be unavailable")
	}
}

func TestDiskThroughputSources(t *testing.T) {
	var c ioCounter
	now := time.Unix(1800000000, 0)
	if r, w := c.sample(now, "container", 1e6, 2e6); r != nil || w != nil {
		t.Fatal("first reading must be unavailable")
	}
	if r, w := c.sample(now.Add(2*time.Second), "container", 5e6, 4e6); *r != 2 || *w != 1 {
		t.Fatal("MB/s", *r, *w)
	}
	for _, step := range []struct {
		after  time.Duration
		source string
		read   uint64
	}{{3 * time.Second, "container", 1}, {4 * time.Second, "server", 2}, {40 * time.Second, "server", 3}} {
		if r, _ := c.sample(now.Add(step.after), step.source, step.read, 5e6); r != nil {
			t.Fatal("resets, source changes and long gaps must be unavailable", step)
		}
	}
	v2 := t.TempDir()
	write(t, v2, map[string]string{"io.stat": "8:0 rbytes=100 wbytes=200 rios=1 wios=2\n259:0 rbytes=50 wbytes=25 rios=1 wios=1\n"})
	if r, w, ok := cgroupIO(v2); !ok || r != 150 || w != 225 {
		t.Fatal("v2", r, w, ok)
	}
	v1 := t.TempDir()
	write(t, v1, map[string]string{"blkio/blkio.throttle.io_service_bytes": "8:0 Read 10\n8:0 Write 20\n8:0 Total 30\nTotal 30\n"})
	if r, w, ok := cgroupIO(v1); !ok || r != 10 || w != 20 {
		t.Fatal("v1", r, w, ok)
	}
	if _, _, ok := cgroupIO(t.TempDir()); ok {
		t.Fatal("missing cgroup I/O must fall back")
	}
	raw := "8 0 sda 1 0 4 0 2 0 8 0 0 0 0\n8 1 sda1 1 0 100 0 1 0 100 0 0 0 0\n"
	if r, w := physicalIO(raw, map[string]string{"sda": "8:0"}); r != 2048 || w != 4096 {
		t.Fatal("partitions must not double-count", r, w)
	}
}

func TestCapacity(t *testing.T) {
	used, total, _, ok := capacity(t.TempDir())
	if !ok || total <= 0 || used < 0 || used > total {
		t.Fatal(used, total, ok)
	}
	for _, path := range []string{"", filepath.Join(t.TempDir(), "missing")} {
		if _, _, _, ok := capacity(path); ok {
			t.Fatalf("%q must be unavailable", path)
		}
	}
	// Folders on one filesystem are counted once, and missing folders are skipped.
	s := Sampler{storage: t.TempDir(), media: []string{t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "missing")}}
	var out Sample
	s.readCapacity(&out)
	if out.MediaTotalBytes == nil || *out.MediaTotalBytes != total || *out.DiskTotalBytes != total || out.Disk == nil {
		t.Fatalf("shared filesystem double-counted: %+v", out)
	}
	out = Sample{}
	(&Sampler{}).readCapacity(&out)
	if out.MediaTotalBytes != nil || out.DiskTotalBytes != nil || out.Disk != nil {
		t.Fatal("no folders must leave capacity unavailable", out)
	}
}

func float(v float64) *float64 { return &v }
func bytes(v int64) *int64     { return &v }
func near(a, b float64) bool   { return a-b < 1e-9 && b-a < 1e-9 }

func TestHistoryCoresCurrentAndRetention(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Unix(1800000000, 0)
	for _, row := range []struct {
		ago   int64
		core0 *float64
		read  float64
	}{{50000, float(99), 9}, {20, float(20), 1}, {19, float(40), 3}, {0, nil, 5}} {
		s := Sample{At: now.Unix() - row.ago, CPU: float(50), CPUCores: []CoreSample{{0, row.core0}, {1, float(10)}}, DiskReadMBps: float(row.read), DiskWriteMBps: float(1), IOPressure: float(2), CPULoad: float(row.read), CPUPressure: float(row.read / 10), MemoryUsedBytes: bytes(int64(row.read * 1e9)), MemoryLimitBytes: bytes(2e9), DiskMounts: []DiskMountSample{{Mount: "config", ReadMBps: float(row.read), WriteMBps: float(1), ReadIOPS: float(row.read * 10), WriteIOPS: float(5), AverageLatencyMs: float(row.read / 2)}}}
		if row.ago == 0 {
			s.CPULoad, s.CPUPressure, s.MemoryUsedBytes, s.MemoryLimitBytes, s.MemoryLimited = float(1.5), float(0.25), bytes(1e9), bytes(2e9), true
			s.Disk, s.DiskUsedBytes, s.DiskTotalBytes, s.DiskPath, s.DiskScope = float(75), bytes(750e9), bytes(1e12), "/config", "container"
			s.MediaUsedBytes, s.MediaTotalBytes = bytes(12e12), bytes(16e12)
		}
		if err := storeSample(ctx, db.DB, s); err != nil {
			t.Fatal(err)
		}
	}
	h, err := ReadHistory(ctx, db.DB, 15, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Points) != 2 || len(h.Points[0].CPUCores) != 2 || *h.Points[0].CPUCores[0].Utilization != 30 || *h.Points[0].DiskReadMBps != 2 || *h.Points[0].CPULoad != 2 || *h.Points[0].DiskMounts[0].ReadMBps != 2 || *h.Points[0].DiskMounts[0].ReadIOPS != 20 || h.Points[1].CPUCores[0].Utilization != nil {
		t.Fatalf("bucketed cores and disk throughput: %+v", h.Points)
	}
	c := h.Current
	if *c.CPULoad != 1.5 || *c.CPUPressure != 0.25 || *c.MemoryUsedBytes != 1e9 || *c.MemoryLimitBytes != 2e9 || !c.MemoryLimited || *c.Disk != 75 || *c.DiskTotalBytes != 1e12 || c.DiskPath != "/config" || c.DiskScope != "container" || *c.MediaTotalBytes != 16e12 || len(c.CPUCores) != 2 || len(c.DiskMounts) != 1 || *c.DiskMounts[0].AverageLatencyMs != 2.5 {
		t.Fatalf("current reading did not round-trip: %+v", c)
	}
	var reader HistoryReader
	before, err := reader.Read(ctx, db.DB, 720, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := storeSample(ctx, db.DB, Sample{At: now.Unix() + 1, CPUCores: []CoreSample{{0, float(80)}}}); err != nil {
		t.Fatal(err)
	}
	cached, err := reader.Read(ctx, db.DB, 720, now.Add(time.Second))
	if err != nil || *cached.Current.CPUCores[0].Utilization != 80 || len(cached.Points) != len(before.Points) {
		t.Fatal("cache must preserve history but refresh current", cached, err)
	}
	if _, err := db.Exec(`DELETE FROM server_metrics WHERE at_unix<?`, now.Add(-12*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM server_cpu_core_metrics WHERE at_unix<?`, now.Add(-12*time.Hour).Unix()).Scan(&count); err != nil || count != 0 {
		t.Fatal("core retention must follow parent samples", count, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM server_disk_mount_metrics WHERE at_unix<?`, now.Add(-12*time.Hour).Unix()).Scan(&count); err != nil || count != 0 {
		t.Fatal("disk mount retention must follow parent samples", count, err)
	}
	for _, table := range []string{"server_cpu_temperatures", "server_disk_metrics"} {
		if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err == nil {
			t.Fatal(table, "should be removed")
		}
	}
}

func TestHistoryBucketsMissingSensorsAndWindow(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Unix(1800000000, 0)
	for _, row := range []struct {
		ago int
		cpu float64
		gpu any
	}{{19, 20, nil}, {10, 40, 50.0}, {0, 60, nil}, {5000, 99, 99.0}} {
		if _, err := db.Exec("INSERT INTO server_metrics(at_unix,cpu,gpu,ram,gpu_source) VALUES(?,?,?,?,?)", now.Unix()-int64(row.ago), row.cpu, row.gpu, 25, "test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE server_metrics SET ingress_mbps=2,egress_mbps=4 WHERE at_unix=?`, now.Unix()-19); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE server_metrics SET ingress_mbps=4,egress_mbps=8 WHERE at_unix=?`, now.Unix()-10); err != nil {
		t.Fatal(err)
	}
	for minutes, want := range map[int]int{15: 15, 30: 60, 60: 300, 120: 300, 240: 900, 720: 3600} {
		interval, ok := BucketSeconds(minutes)
		if !ok || interval != want {
			t.Fatal(minutes, interval)
		}
		history, err := ReadHistory(context.Background(), db.DB, minutes, now)
		if err != nil {
			t.Fatal(err)
		}
		if history.Interval != want || history.Current.CPU == nil || *history.Current.CPU != 60 || history.Current.CPULoad != nil || history.Current.Disk != nil {
			t.Fatalf("%+v", history)
		}
		if minutes == 15 {
			if len(history.Points) != 3 || history.Points[0].At != now.Unix()-30 || *history.Points[0].CPU != 20 || *history.Points[1].CPU != 40 || *history.Points[0].IngressMbps != 2 || *history.Points[1].EgressMbps != 8 || history.Points[2].IngressMbps != nil {
				t.Fatalf("fifteen-second history lost samples: %+v", history.Points)
			}
		}
		if minutes == 30 {
			if history.Points[0].IngressMbps == nil || *history.Points[0].IngressMbps != 3 || *history.Points[0].EgressMbps != 6 || history.Points[1].IngressMbps != nil {
				t.Fatal("network averages or missing samples incorrect")
			}
			if len(history.Points) != 2 || history.Points[0].CPU == nil || *history.Points[0].CPU != 30 {
				t.Fatalf("buckets: %+v", history.Points)
			}
			if history.Points[0].GPU == nil || *history.Points[0].GPU != 50 || history.Points[1].GPU != nil {
				t.Fatal("missing GPU samples were zero-filled")
			}
		}
	}
	if _, ok := BucketSeconds(1); ok {
		t.Fatal("accepted unsupported history window")
	}
}

func TestHostHelperSuppliesOnlyFreshNetwork(t *testing.T) {
	now := time.Unix(1800000000, 0)
	for _, age := range []int64{0, 30, 31, -6} {
		raw, err := json.Marshal(Sample{At: now.Unix() - age, CPU: float(90), RAM: float(90), Disk: float(90), NetworkInterface: "eno1", IngressMbps: float(5)})
		if err != nil {
			t.Fatal(err)
		}
		sample := Sample{At: now.Unix(), CPU: float(10), RAM: float(20), NetworkScope: "container"}
		applyHostSample(&sample, raw, now)
		valid := age >= -5 && age <= 30
		if valid != (sample.NetworkScope == "host") || (valid && (*sample.IngressMbps != 5 || sample.NetworkInterface != "eno1")) {
			t.Fatalf("age=%d: %+v", age, sample)
		}
		if *sample.CPU != 10 || *sample.RAM != 20 || sample.Disk != nil {
			t.Fatal("host CPU, memory and disk must not replace container readings")
		}
	}
	sample := Sample{NetworkScope: "container"}
	applyHostSample(&sample, []byte(`not json`), now)
	if sample.NetworkScope != "container" {
		t.Fatal("invalid helper output must be ignored")
	}
}
