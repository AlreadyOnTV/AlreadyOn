package httpapi

import (
	"context"
	"database/sql"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/serversettings"
)

// Process uptime resets on restart, independently of the Docker host's uptime.
var processStarted = time.Now()

var nativeBuildVersion string

// Build stamps stay fixed for the life of the binary, including native previews.
func appVersion() string {
	if nativeBuildVersion != "" {
		return nativeBuildVersion
	}
	if data, err := os.ReadFile("/usr/local/share/alreadyon/build-version"); err == nil {
		return strings.TrimSpace(string(data))
	}
	revision := "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				revision = setting.Value
			}
		}
	}
	if len(revision) > 7 {
		revision = revision[:7]
	}
	zone, err := time.LoadLocation(serverTimeZone())
	if err != nil {
		zone = time.Local
	}
	return processStarted.In(zone).Format("06.01.02") + "_" + revision
}

// Report discovered server addresses, including loopback and virtual interfaces.
func hostAddresses() []string {
	addresses := []string{}
	interfaces, _ := net.Interfaces()
	for _, iface := range interfaces {
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err == nil && hostIPv4(ip) {
				addresses = append(addresses, ip.String())
			}
		}
	}
	slices.Sort(addresses)
	return slices.Compact(addresses)
}

// Prefer an IANA name over Go's generic Local label on native installations.
func serverTimeZone() string {
	if zone := os.Getenv("TZ"); zone != "" {
		return strings.TrimPrefix(zone, ":")
	}
	if path, err := os.Readlink("/etc/localtime"); err == nil {
		if _, name, ok := strings.Cut(path, "zoneinfo/"); ok {
			return name
		}
	}
	zone, _ := time.Now().Zone()
	return zone
}

func handleServerDetails(db *sql.DB) http.HandlerFunc {
	// FFmpeg's immutable version is cached; bound the subprocess even if a
	// request disconnects and share it across simultaneous dashboard requests.
	var once sync.Once
	ffmpegVersion := "Unavailable"
	version := appVersion()
	return func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if output, err := exec.CommandContext(ctx, "ffmpeg", "-version").Output(); err == nil {
				fields := strings.Fields(strings.SplitN(string(output), "\n", 2)[0])
				if len(fields) >= 3 && fields[0] == "ffmpeg" && fields[1] == "version" {
					ffmpegVersion = fields[2]
				}
			}
		})
		settings, err := serversettings.Get(r.Context(), db)
		if err != nil {
			writeJSONError(w, 500, "could not read server details")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, struct {
			ServerName    string   `json:"serverName"`
			UptimeSeconds int64    `json:"uptimeSeconds"`
			IPAddresses   []string `json:"ipAddresses"`
			AppVersion    string   `json:"appVersion"`
			TimeZone      string   `json:"timeZone"`
			FFmpegVersion string   `json:"ffmpegVersion"`
		}{settings.ServerName, int64(time.Since(processStarted).Seconds()), hostAddresses(), version, serverTimeZone(), ffmpegVersion})
	}
}

// Only IPv6 is excluded; local and virtual IPv4 addresses remain visible.
func hostIPv4(ip net.IP) bool {
	return ip.To4() != nil
}
