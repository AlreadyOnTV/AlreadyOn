// Package metrics samples server resources and retains twelve hours of history.
// Missing sensors stay NULL; they are never represented as idle hardware.
package metrics

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Sample struct {
	CPUCores         []CoreSample      `json:"cpuCores"`
	CPULoad          *float64          `json:"cpuLoad"`
	CPUPressure      *float64          `json:"cpuPressure"`
	MemoryUsedBytes  *int64            `json:"memoryUsedBytes"`
	MemoryLimitBytes *int64            `json:"memoryLimitBytes"`
	MemoryLimited    bool              `json:"memoryLimited"`
	DiskReadMBps     *float64          `json:"diskReadMBps"`
	DiskWriteMBps    *float64          `json:"diskWriteMBps"`
	IOPressure       *float64          `json:"ioPressure"`
	DiskMounts       []DiskMountSample `json:"diskMounts"`
	Disk             *float64          `json:"disk"`
	DiskUsedBytes    *int64            `json:"diskUsedBytes"`
	DiskTotalBytes   *int64            `json:"diskTotalBytes"`
	DiskPath         string            `json:"diskPath"`
	MediaUsedBytes   *int64            `json:"mediaUsedBytes"`
	MediaTotalBytes  *int64            `json:"mediaTotalBytes"`
	DiskScope        string            `json:"diskScope"`
	IngressMbps      *float64          `json:"ingressMbps"`
	EgressMbps       *float64          `json:"egressMbps"`
	NetworkInterface string            `json:"networkInterface"`
	NetworkScope     string            `json:"networkScope"`
	At               int64             `json:"at"`
	CPU              *float64          `json:"cpu"`
	GPU              *float64          `json:"gpu"`
	RAM              *float64          `json:"ram"`
	GPUSource        string            `json:"gpuSource"`
}
type History struct {
	Interval int      `json:"intervalSeconds"`
	Points   []Sample `json:"points"`
	Current  Sample   `json:"current"`
}

// Sampler keeps the previous counters needed to turn cumulative kernel totals
// into rates. storage is the data directory and media the configured media
// folders whose filesystem capacity the Disk graph reports.
type Sampler struct {
	proc, cgroup, storage string
	media                 []string
	cpu                   cpuCounter
	io                    ioCounter
	mountIO               mountIOCounter
	network               networkCounter
}

func NewSampler(storage string) *Sampler {
	return &Sampler{proc: "/proc", cgroup: "/sys/fs/cgroup", storage: storage}
}
func (s *Sampler) Close()        {}
func percent(v float64) *float64 { v = max(0, min(100, v)); return &v }
func inContainer() bool {
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(marker); err == nil {
			return true
		}
	}
	return false
}

func (s *Sampler) Read(now time.Time) Sample {
	container := inContainer()
	out := Sample{At: now.Unix(), NetworkScope: "server", DiskScope: "server"}
	if container {
		out.NetworkScope = "container"
	}
	if raw, err := os.ReadFile(filepath.Join(s.proc, "stat")); err == nil {
		out.CPU, out.CPUCores = s.cpu.sample(string(raw), allowedCPUs(s.cgroup))
	}
	out.CPULoad = loadAverage(s.proc)
	out.CPUPressure = pressure(filepath.Join(s.proc, "pressure", "cpu"))
	if m, ok := memoryUsage(s.proc, s.cgroup, container); ok && m.limit > 0 {
		out.MemoryUsedBytes, out.MemoryLimitBytes, out.MemoryLimited = &m.used, &m.limit, m.limited
		out.RAM = percent(100 * float64(m.used) / float64(m.limit))
	}
	out.NetworkInterface, out.IngressMbps, out.EgressMbps = s.network.read(now)
	diskstats, _ := os.ReadFile(filepath.Join(s.proc, "diskstats"))
	read, write, ok := uint64(0), uint64(0), false
	if container {
		if read, write, ok = cgroupIO(s.cgroup); ok {
			out.DiskScope = "container"
		}
	}
	if !ok {
		read, write = physicalIO(string(diskstats), physicalDisks("/sys/block"))
	}
	out.DiskReadMBps, out.DiskWriteMBps = s.io.sample(now, out.DiskScope, read, write)
	out.IOPressure = pressure(filepath.Join(s.proc, "pressure", "io"))
	if container {
		mountInfo, _ := os.ReadFile(filepath.Join(s.proc, "self", "mountinfo"))
		cgroupIO, _ := os.ReadFile(filepath.Join(s.cgroup, "io.stat"))
		paths := map[string][]string{"config": {s.storage}, "media": s.media}
		out.DiskMounts = s.mountIO.sample(now, mountDevices(string(mountInfo), paths, filepath.Join("/sys", "class", "block"), string(cgroupIO), string(diskstats)), string(cgroupIO), string(diskstats))
	}
	s.readCapacity(&out)
	return out
}

// readCapacity reports the data directory and the media folders separately;
// media folders on one filesystem count once. Disk is the combined used
// share of every distinct filesystem involved.
func (s *Sampler) readCapacity(out *Sample) {
	seen := map[uint64]bool{}
	var allUsed, allTotal int64
	if used, total, dev, ok := capacity(s.storage); ok && total > 0 {
		out.DiskUsedBytes, out.DiskTotalBytes, out.DiskPath = &used, &total, s.storage
		seen[dev] = true
		allUsed, allTotal = used, total
	}
	media := map[uint64]bool{}
	var mediaUsed, mediaTotal int64
	for _, path := range s.media {
		used, total, dev, ok := capacity(path)
		if !ok || total <= 0 || media[dev] {
			continue
		}
		media[dev] = true
		mediaUsed, mediaTotal = mediaUsed+used, mediaTotal+total
		if !seen[dev] {
			seen[dev] = true
			allUsed, allTotal = allUsed+used, allTotal+total
		}
	}
	if len(media) > 0 {
		out.MediaUsedBytes, out.MediaTotalBytes = &mediaUsed, &mediaTotal
	}
	if allTotal > 0 {
		out.Disk = percent(100 * float64(allUsed) / float64(allTotal))
	}
}

// Collect runs independently of browser refresh rate. Disk capacity describes
// dataDir, where the database and transcode output live, and the folders
// returned by mediaRoots, refreshed each minute so saved folder changes apply.
// An optional host helper in dataDir supplies host network traffic.
func Collect(ctx context.Context, db *sql.DB, dataDir string, mediaRoots func(context.Context) []string) {
	if abs, err := filepath.Abs(dataDir); err == nil {
		dataDir = abs
	}
	s := NewSampler(dataDir)
	defer s.Close()
	hostFile := filepath.Join(dataDir, "host-metrics.json")
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	lastPrune, lastRoots := time.Time{}, time.Time{}
	collect := func() {
		now := time.Now()
		if mediaRoots != nil && now.Sub(lastRoots) >= time.Minute {
			s.media, lastRoots = mediaRoots(ctx), now
		}
		sample := s.Read(now)
		if raw, err := os.ReadFile(hostFile); err == nil {
			applyHostSample(&sample, raw, now)
		}
		_ = storeSample(ctx, db, sample)
		if now.Sub(lastPrune) >= time.Minute {
			_, _ = db.ExecContext(ctx, `DELETE FROM server_metrics WHERE at_unix<?`, now.Add(-12*time.Hour).Unix())
			lastPrune = now
		}
	}
	collect()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			collect()
		}
	}
}

// Only network traffic comes from the helper: CPU, memory and disk describe
// the container's own limits. A stale helper must not manufacture readings.
func applyHostSample(sample *Sample, raw []byte, now time.Time) {
	var host Sample
	if json.Unmarshal(raw, &host) != nil || host.At > now.Unix()+5 || now.Unix()-host.At > 30 {
		return
	}
	if host.NetworkInterface != "" {
		sample.IngressMbps, sample.EgressMbps = host.IngressMbps, host.EgressMbps
		sample.NetworkInterface, sample.NetworkScope = host.NetworkInterface, "host"
	}
}

func storeSample(ctx context.Context, db *sql.DB, s Sample) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO server_metrics(at_unix,cpu,gpu,ram,gpu_source,ingress_mbps,egress_mbps,network_interface,network_scope,cpu_load,cpu_pressure,memory_used_bytes,memory_limit_bytes,memory_limited,disk_read_mbps,disk_write_mbps,io_pressure,disk_capacity,disk_used_bytes,disk_total_bytes,disk_path,disk_scope,media_used_bytes,media_total_bytes) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		s.At, s.CPU, s.GPU, s.RAM, s.GPUSource, s.IngressMbps, s.EgressMbps, s.NetworkInterface, s.NetworkScope, s.CPULoad, s.CPUPressure, s.MemoryUsedBytes, s.MemoryLimitBytes, s.MemoryLimited, s.DiskReadMBps, s.DiskWriteMBps, s.IOPressure, s.Disk, s.DiskUsedBytes, s.DiskTotalBytes, s.DiskPath, s.DiskScope, s.MediaUsedBytes, s.MediaTotalBytes)
	if err != nil {
		return err
	}
	for _, core := range s.CPUCores {
		if _, err = tx.ExecContext(ctx, `INSERT INTO server_cpu_core_metrics(at_unix,core,utilization) VALUES(?,?,?)`, s.At, core.Core, core.Utilization); err != nil {
			return err
		}
	}
	for _, disk := range s.DiskMounts {
		if _, err = tx.ExecContext(ctx, `INSERT INTO server_disk_mount_metrics(at_unix,mount,read_mbps,write_mbps,read_iops,write_iops,average_latency_ms) VALUES(?,?,?,?,?,?,?)`, s.At, disk.Mount, disk.ReadMBps, disk.WriteMBps, disk.ReadIOPS, disk.WriteIOPS, disk.AverageLatencyMs); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func BucketSeconds(minutes int) (int, bool) {
	switch minutes {
	case 15:
		return 15, true
	case 30:
		return 60, true
	case 60, 120:
		return 300, true
	case 240:
		return 900, true
	case 720:
		return 3600, true
	}
	return 0, false
}
func readPoints(ctx context.Context, db *sql.DB, minutes int, now time.Time) (History, error) {
	interval, _ := BucketSeconds(minutes)
	out := History{Interval: interval, Points: []Sample{}}
	from := now.Add(-time.Duration(minutes) * time.Minute).Unix()
	rows, err := db.QueryContext(ctx, `SELECT (at_unix / ?) * ?,AVG(cpu),AVG(gpu),AVG(ram),AVG(ingress_mbps),AVG(egress_mbps),AVG(disk_read_mbps),AVG(disk_write_mbps),AVG(io_pressure),network_interface,network_scope,AVG(cpu_load),AVG(cpu_pressure),CAST(AVG(memory_used_bytes) AS INTEGER),CAST(AVG(memory_limit_bytes) AS INTEGER),MAX(memory_limited),CAST(AVG(disk_capacity) AS INTEGER),CAST(AVG(disk_used_bytes) AS INTEGER),CAST(AVG(disk_total_bytes) AS INTEGER),MAX(disk_path),MAX(disk_scope),CAST(AVG(media_used_bytes) AS INTEGER),CAST(AVG(media_total_bytes) AS INTEGER) FROM server_metrics WHERE at_unix>=? AND at_unix<=? GROUP BY at_unix / ?,network_interface,network_scope ORDER BY 1,network_interface,network_scope`, interval, interval, from, now.Unix(), interval)
	if err != nil {
		return out, err
	}
	byTime := map[int64][]int{}
	for rows.Next() {
		var p Sample
		if err := rows.Scan(&p.At, &p.CPU, &p.GPU, &p.RAM, &p.IngressMbps, &p.EgressMbps, &p.DiskReadMBps, &p.DiskWriteMBps, &p.IOPressure, &p.NetworkInterface, &p.NetworkScope, &p.CPULoad, &p.CPUPressure, &p.MemoryUsedBytes, &p.MemoryLimitBytes, &p.MemoryLimited, &p.Disk, &p.DiskUsedBytes, &p.DiskTotalBytes, &p.DiskPath, &p.DiskScope, &p.MediaUsedBytes, &p.MediaTotalBytes); err != nil {
			rows.Close()
			return out, err
		}
		byTime[p.At] = append(byTime[p.At], len(out.Points))
		out.Points = append(out.Points, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	diskRows, err := db.QueryContext(ctx, `SELECT (at_unix / ?) * ?,mount,AVG(read_mbps),AVG(write_mbps),AVG(read_iops),AVG(write_iops),AVG(average_latency_ms) FROM server_disk_mount_metrics WHERE at_unix>=? AND at_unix<=? GROUP BY at_unix / ?,mount ORDER BY 1,mount`, interval, interval, from, now.Unix(), interval)
	if err != nil {
		return out, err
	}
	for diskRows.Next() {
		var at int64
		var disk DiskMountSample
		if err := diskRows.Scan(&at, &disk.Mount, &disk.ReadMBps, &disk.WriteMBps, &disk.ReadIOPS, &disk.WriteIOPS, &disk.AverageLatencyMs); err != nil {
			diskRows.Close()
			return out, err
		}
		for _, index := range byTime[at] {
			out.Points[index].DiskMounts = append(out.Points[index].DiskMounts, disk)
		}
	}
	err = diskRows.Err()
	diskRows.Close()
	if err != nil {
		return out, err
	}
	coreRows, err := db.QueryContext(ctx, `SELECT (at_unix / ?) * ?,core,AVG(utilization) FROM server_cpu_core_metrics WHERE at_unix>=? AND at_unix<=? GROUP BY at_unix / ?,core ORDER BY 1,core`, interval, interval, from, now.Unix(), interval)
	if err != nil {
		return out, err
	}
	defer coreRows.Close()
	for coreRows.Next() {
		var at int64
		var core CoreSample
		if err := coreRows.Scan(&at, &core.Core, &core.Utilization); err != nil {
			return out, err
		}
		for _, index := range byTime[at] {
			out.Points[index].CPUCores = append(out.Points[index].CPUCores, core)
		}
	}
	return out, coreRows.Err()
}

func readCurrent(ctx context.Context, db *sql.DB) (Sample, error) {
	var out Sample
	err := db.QueryRowContext(ctx, `SELECT at_unix,cpu,gpu,ram,gpu_source,ingress_mbps,egress_mbps,network_interface,network_scope,cpu_load,cpu_pressure,memory_used_bytes,memory_limit_bytes,memory_limited,disk_read_mbps,disk_write_mbps,io_pressure,disk_capacity,disk_used_bytes,disk_total_bytes,disk_path,disk_scope,media_used_bytes,media_total_bytes FROM server_metrics ORDER BY at_unix DESC LIMIT 1`).Scan(
		&out.At, &out.CPU, &out.GPU, &out.RAM, &out.GPUSource, &out.IngressMbps, &out.EgressMbps, &out.NetworkInterface, &out.NetworkScope, &out.CPULoad, &out.CPUPressure, &out.MemoryUsedBytes, &out.MemoryLimitBytes, &out.MemoryLimited, &out.DiskReadMBps, &out.DiskWriteMBps, &out.IOPressure, &out.Disk, &out.DiskUsedBytes, &out.DiskTotalBytes, &out.DiskPath, &out.DiskScope, &out.MediaUsedBytes, &out.MediaTotalBytes)
	if err == sql.ErrNoRows {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	rows, err := db.QueryContext(ctx, `SELECT core,utilization FROM server_cpu_core_metrics WHERE at_unix=? ORDER BY core`, out.At)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var core CoreSample
		if err := rows.Scan(&core.Core, &core.Utilization); err != nil {
			return out, err
		}
		out.CPUCores = append(out.CPUCores, core)
	}
	diskRows, err := db.QueryContext(ctx, `SELECT mount,read_mbps,write_mbps,read_iops,write_iops,average_latency_ms FROM server_disk_mount_metrics WHERE at_unix=? ORDER BY mount`, out.At)
	if err != nil {
		return out, err
	}
	defer diskRows.Close()
	for diskRows.Next() {
		var disk DiskMountSample
		if err := diskRows.Scan(&disk.Mount, &disk.ReadMBps, &disk.WriteMBps, &disk.ReadIOPS, &disk.WriteIOPS, &disk.AverageLatencyMs); err != nil {
			return out, err
		}
		out.DiskMounts = append(out.DiskMounts, disk)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	return out, diskRows.Err()
}

func ReadHistory(ctx context.Context, db *sql.DB, minutes int, now time.Time) (History, error) {
	out, err := readPoints(ctx, db, minutes, now)
	if err != nil {
		return out, err
	}
	current, err := readCurrent(ctx, db)
	out.Current = current
	return out, err
}

// HistoryReader shares bounded historical aggregates across dashboard viewers.
// Current readings remain fresh on every request; points refresh at most every
// 15 seconds, the smallest averaging bucket, without rescanning 12 hours at 1 Hz.
type HistoryReader struct {
	mu    sync.Mutex
	cache map[int]historyCache
}
type historyCache struct {
	at      time.Time
	history History
}

func (r *HistoryReader) Read(ctx context.Context, db *sql.DB, minutes int, now time.Time) (History, error) {
	if _, ok := BucketSeconds(minutes); !ok {
		return History{}, fmt.Errorf("unsupported history window")
	}
	r.mu.Lock()
	cached, ok := r.cache[minutes]
	if !ok || now.Before(cached.at) || now.Sub(cached.at) >= 15*time.Second {
		points, err := readPoints(ctx, db, minutes, now)
		if err != nil {
			r.mu.Unlock()
			return History{}, err
		}
		cached = historyCache{now, points}
		if r.cache == nil {
			r.cache = map[int]historyCache{}
		}
		r.cache[minutes] = cached
	}
	r.mu.Unlock()
	out := cached.history
	current, err := readCurrent(ctx, db)
	out.Current = current
	return out, err
}
