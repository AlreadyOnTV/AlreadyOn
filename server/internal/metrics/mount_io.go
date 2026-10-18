package metrics

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type DiskMountSample struct {
	Mount            string   `json:"mount"`
	ReadMBps         *float64 `json:"readMBps"`
	WriteMBps        *float64 `json:"writeMBps"`
	ReadIOPS         *float64 `json:"readIOPS"`
	WriteIOPS        *float64 `json:"writeIOPS"`
	AverageLatencyMs *float64 `json:"averageLatencyMs"`
}

type deviceIOCounters struct {
	readBytes, writeBytes uint64
	readOps, writeOps     uint64
}

type deviceDiskCounters struct {
	readOps, readTimeMs   uint64
	writeOps, writeTimeMs uint64
}

type mountDeviceCounters struct {
	io        deviceIOCounters
	disk      deviceDiskCounters
	ioValid   bool
	diskValid bool
	at        time.Time
}

type mountIOCounter struct {
	previous map[string]mountDeviceCounters
}

func floatPointer(value float64) *float64 { return &value }

func parseCgroupDeviceIO(raw string) map[string]deviceIOCounters {
	out := map[string]deviceIOCounters{}
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		counter := deviceIOCounters{}
		for _, field := range fields[1:] {
			key, value, ok := strings.Cut(field, "=")
			if !ok {
				continue
			}
			n, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				continue
			}
			switch key {
			case "rbytes":
				counter.readBytes = n
			case "wbytes":
				counter.writeBytes = n
			case "rios":
				counter.readOps = n
			case "wios":
				counter.writeOps = n
			}
		}
		out[fields[0]] = counter
	}
	return out
}

func parseDeviceDiskstats(raw string) map[string]deviceDiskCounters {
	out := map[string]deviceDiskCounters{}
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 11 {
			continue
		}
		readOps, errReadOps := strconv.ParseUint(fields[3], 10, 64)
		readTime, errReadTime := strconv.ParseUint(fields[6], 10, 64)
		writeOps, errWriteOps := strconv.ParseUint(fields[7], 10, 64)
		writeTime, errWriteTime := strconv.ParseUint(fields[10], 10, 64)
		if errReadOps != nil || errReadTime != nil || errWriteOps != nil || errWriteTime != nil {
			continue
		}
		out[fields[0]+":"+fields[1]] = deviceDiskCounters{readOps, readTime, writeOps, writeTime}
	}
	return out
}

func unescapeMountPath(path string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(path)
}

func mountDevices(raw string, paths map[string][]string, sysClassBlock, ioRaw, diskstatsRaw string) map[string][]string {
	type mountInfo struct {
		device string
		path   string
		source string
	}
	mounts := make([]mountInfo, 0)
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		source := ""
		for index, field := range fields {
			if field == "-" && index+2 < len(fields) {
				source = unescapeMountPath(fields[index+2])
				break
			}
		}
		mounts = append(mounts, mountInfo{device: fields[2], path: unescapeMountPath(fields[4]), source: source})
	}
	ioDevices := parseCgroupDeviceIO(ioRaw)
	diskDevices := parseDeviceDiskstats(diskstatsRaw)
	out := make(map[string][]string, len(paths))
	for name, mountPaths := range paths {
		seen := map[string]bool{}
		for _, path := range mountPaths {
			path = strings.TrimSuffix(path, "/")
			bestLength := -1
			var bestMount mountInfo
			for _, mount := range mounts {
				mountPath := strings.TrimSuffix(mount.path, "/")
				matches := path == mountPath || strings.HasPrefix(path, mountPath+"/")
				if matches && len(mount.path) > bestLength {
					bestLength, bestMount = len(mount.path), mount
				}
			}
			if bestLength < 0 {
				continue
			}
			candidates := []string{}
			candidateSet := map[string]bool{}
			addCandidate := func(device string) {
				if device != "" && !strings.HasPrefix(device, "0:") && !candidateSet[device] {
					candidateSet[device] = true
					candidates = append(candidates, device)
				}
			}
			addCandidate(bestMount.device)
			if strings.HasPrefix(bestMount.source, "/dev/") {
				classPath := filepath.Join(sysClassBlock, filepath.Base(bestMount.source))
				if realPath, err := filepath.EvalSymlinks(classPath); err == nil {
					if device, err := os.ReadFile(filepath.Join(realPath, "dev")); err == nil {
						addCandidate(strings.TrimSpace(string(device)))
					}
					if _, err := os.Stat(filepath.Join(realPath, "partition")); err == nil {
						if device, err := os.ReadFile(filepath.Join(filepath.Dir(realPath), "dev")); err == nil {
							addCandidate(strings.TrimSpace(string(device)))
						}
					}
				}
			}
			selected := ""
			for _, candidate := range candidates {
				if _, hasIO := ioDevices[candidate]; hasIO {
					if _, hasDisk := diskDevices[candidate]; hasDisk {
						selected = candidate
						break
					}
				}
			}
			if selected == "" {
				for _, candidate := range candidates {
					if _, hasIO := ioDevices[candidate]; hasIO {
						selected = candidate
						break
					}
					if _, hasDisk := diskDevices[candidate]; hasDisk {
						selected = candidate
						break
					}
				}
			}
			if selected != "" && !seen[selected] {
				seen[selected] = true
				out[name] = append(out[name], selected)
			}
		}
	}
	return out
}

func (c *mountIOCounter) sample(now time.Time, mounts map[string][]string, ioRaw, diskstatsRaw string) []DiskMountSample {
	ioCurrent := parseCgroupDeviceIO(ioRaw)
	diskCurrent := parseDeviceDiskstats(diskstatsRaw)
	devices := make(map[string]bool, len(ioCurrent)+len(diskCurrent))
	for device := range ioCurrent {
		devices[device] = true
	}
	for device := range diskCurrent {
		devices[device] = true
	}
	next := make(map[string]mountDeviceCounters, len(devices))
	for device := range devices {
		io, ioValid := ioCurrent[device]
		disk, diskValid := diskCurrent[device]
		next[device] = mountDeviceCounters{io: io, disk: disk, ioValid: ioValid, diskValid: diskValid, at: now}
	}

	out := make([]DiskMountSample, 0, len(mounts))
	for name, deviceNames := range mounts {
		result := DiskMountSample{Mount: name}
		var readBytes, writeBytes, readOps, writeOps uint64
		var readTimeMs, writeTimeMs, latencyOps uint64
		elapsed := 0.0
		validIO, validDisk := len(deviceNames) > 0, len(deviceNames) > 0
		for _, device := range deviceNames {
			current, currentOK := next[device]
			previous, previousOK := c.previous[device]
			if !currentOK || !previousOK {
				validIO, validDisk = false, false
				continue
			}
			deviceElapsed := now.Sub(previous.at).Seconds()
			if deviceElapsed <= 0 || deviceElapsed > 30 {
				validIO, validDisk = false, false
				continue
			}
			if elapsed == 0 {
				elapsed = deviceElapsed
			} else if elapsed != deviceElapsed {
				validIO, validDisk = false, false
				continue
			}
			if !current.ioValid || !previous.ioValid || current.io.readBytes < previous.io.readBytes || current.io.writeBytes < previous.io.writeBytes || current.io.readOps < previous.io.readOps || current.io.writeOps < previous.io.writeOps {
				validIO = false
			} else {
				readBytes += current.io.readBytes - previous.io.readBytes
				writeBytes += current.io.writeBytes - previous.io.writeBytes
				readOps += current.io.readOps - previous.io.readOps
				writeOps += current.io.writeOps - previous.io.writeOps
			}
			if !current.diskValid || !previous.diskValid || current.disk.readOps < previous.disk.readOps || current.disk.writeOps < previous.disk.writeOps || current.disk.readTimeMs < previous.disk.readTimeMs || current.disk.writeTimeMs < previous.disk.writeTimeMs {
				validDisk = false
			} else {
				latencyOps += current.disk.readOps - previous.disk.readOps + current.disk.writeOps - previous.disk.writeOps
				readTimeMs += current.disk.readTimeMs - previous.disk.readTimeMs
				writeTimeMs += current.disk.writeTimeMs - previous.disk.writeTimeMs
			}
		}
		if validIO && elapsed > 0 {
			result.ReadMBps = floatPointer(float64(readBytes) / elapsed / 1e6)
			result.WriteMBps = floatPointer(float64(writeBytes) / elapsed / 1e6)
			result.ReadIOPS = floatPointer(float64(readOps) / elapsed)
			result.WriteIOPS = floatPointer(float64(writeOps) / elapsed)
		}
		if validDisk && elapsed > 0 && latencyOps > 0 {
			result.AverageLatencyMs = floatPointer(float64(readTimeMs+writeTimeMs) / float64(latencyOps))
		}
		out = append(out, result)
	}
	c.previous = next
	sort.Slice(out, func(i, j int) bool { return out[i].Mount < out[j].Mount })
	return out
}
