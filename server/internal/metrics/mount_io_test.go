package metrics

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func diskstatsDevice(major, minor string, readOps, readTimeMs, writeOps, writeTimeMs uint64) string {
	return fmt.Sprintf("%s %s test %d 0 0 %d %d 0 0 %d 0 0 0\n", major, minor, readOps, readTimeMs, writeOps, writeTimeMs)
}

func TestMountDevicesSelectMostSpecificAndDeduplicate(t *testing.T) {
	mountInfo := "36 25 8:0 / / rw - ext4 /dev/sda rw\n" +
		"37 36 8:1 / /config rw - ext4 /dev/sda1 rw\n" +
		"38 36 8:16 / /data/media rw - ext4 /dev/sdb rw\n" +
		"39 38 8:17 / /data/media/Films\\040And\\040TV rw - ext4 /dev/sdb1 rw\n"
	paths := map[string][]string{
		"config": {"/config/database"},
		"media":  {"/data/media/Films And TV", "/data/media/Music"},
	}
	ioStats := "8:1 rbytes=10 wbytes=20 rios=1 wios=2\n8:17 rbytes=30 wbytes=40 rios=3 wios=4\n8:16 rbytes=50 wbytes=60 rios=5 wios=6\n"
	diskstats := diskstatsDevice("8", "1", 1, 10, 2, 20) + diskstatsDevice("8", "16", 5, 50, 6, 60) + diskstatsDevice("8", "17", 3, 30, 4, 40)
	got := mountDevices(mountInfo, paths, t.TempDir(), ioStats, diskstats)
	if len(got["config"]) != 1 || got["config"][0] != "8:1" {
		t.Fatalf("config mount resolution: %#v", got)
	}
	if len(got["media"]) != 2 || got["media"][0] != "8:17" || got["media"][1] != "8:16" {
		t.Fatalf("media mounts should resolve independently: %#v", got)
	}
}

func TestMountDevicesResolveBtrfsSourceToBackingDevice(t *testing.T) {
	sysClass := t.TempDir()
	disk := filepath.Join(sysClass, "devices", "nvme0n1")
	partition := filepath.Join(disk, "nvme0n1p3")
	write(t, sysClass, map[string]string{
		"devices/nvme0n1/dev":                 "259:0\n",
		"devices/nvme0n1/nvme0n1p3/dev":       "259:3\n",
		"devices/nvme0n1/nvme0n1p3/partition": "3\n",
	})
	if err := os.Symlink(disk, filepath.Join(sysClass, "nvme0n1")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(partition, filepath.Join(sysClass, "nvme0n1p3")); err != nil {
		t.Fatal(err)
	}
	mountInfo := "36 25 0:36 / /config rw - btrfs /dev/nvme0n1p3 rw\n"
	ioStats := "259:0 rbytes=100 wbytes=200 rios=3 wios=4\n"
	diskstats := diskstatsDevice("259", "0", 10, 30, 20, 60)
	got := mountDevices(mountInfo, map[string][]string{"config": {"/config"}}, sysClass, ioStats, diskstats)
	if len(got["config"]) != 1 || got["config"][0] != "259:0" {
		t.Fatalf("Btrfs mount source should resolve to its parent physical device: %#v", got)
	}
}

func TestMountIOBandwidthIOPSAndAverageLatency(t *testing.T) {
	var counter mountIOCounter
	mounts := map[string][]string{"config": {"8:0"}, "media": {"8:16"}}
	now := time.Unix(1800000000, 0)
	firstIO := "8:0 rbytes=100 wbytes=200 rios=10 wios=20\n8:16 rbytes=50 wbytes=75 rios=5 wios=7\n"
	firstDisk := diskstatsDevice("8", "0", 100, 500, 200, 1000) + diskstatsDevice("8", "16", 50, 100, 70, 200)
	counter.sample(now, mounts, firstIO, firstDisk)
	secondIO := "8:0 rbytes=2000100 wbytes=4000200 rios=20 wios=26\n8:16 rbytes=1000050 wbytes=2000075 rios=9 wios=11\n"
	secondDisk := diskstatsDevice("8", "0", 104, 520, 202, 1060) + diskstatsDevice("8", "16", 54, 120, 72, 260)
	got := counter.sample(now.Add(2*time.Second), mounts, secondIO, secondDisk)
	if len(got) != 2 || got[0].Mount != "config" || !near(*got[0].ReadMBps, 1) || !near(*got[0].WriteMBps, 2) || !near(*got[0].ReadIOPS, 5) || !near(*got[0].WriteIOPS, 3) || !near(*got[0].AverageLatencyMs, 80.0/6) {
		t.Fatalf("config metrics: %+v", got)
	}
	if got[1].Mount != "media" || !near(*got[1].ReadMBps, .5) || !near(*got[1].WriteMBps, 1) || !near(*got[1].ReadIOPS, 2) || !near(*got[1].WriteIOPS, 2) || !near(*got[1].AverageLatencyMs, 80.0/6) {
		t.Fatalf("media metrics: %+v", got)
	}
}

func TestMountIOSharedDeviceAndIndependentAvailability(t *testing.T) {
	var counter mountIOCounter
	mounts := map[string][]string{"config": {"8:0"}, "media": {"8:0"}}
	now := time.Unix(1800000000, 0)
	firstDisk := diskstatsDevice("8", "0", 10, 30, 20, 60)
	secondDisk := diskstatsDevice("8", "0", 12, 34, 22, 68)
	counter.sample(now, mounts, "", firstDisk)
	got := counter.sample(now.Add(time.Second), mounts, "", secondDisk)
	if len(got) != 2 || got[0].AverageLatencyMs == nil || *got[0].AverageLatencyMs != 3 || *got[1].AverageLatencyMs != 3 {
		t.Fatalf("shared device latency should match and remain independently labeled: %+v", got)
	}
	if got[0].ReadMBps != nil || got[0].ReadIOPS != nil {
		t.Fatalf("missing cgroup counters must not be presented as zero: %+v", got[0])
	}
}
