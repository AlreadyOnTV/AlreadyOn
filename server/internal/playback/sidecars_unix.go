//go:build linux || darwin

package playback

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

const sidecarsSupported = true

func openSidecar(root *os.Root, name string) (*os.File, error) {
	// Refuse symlinks and avoid blocking if a listed file was replaced by a FIFO.
	return root.OpenFile(name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
}

func sidecarFileID(info os.FileInfo) string {
	stat := info.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino)
}
