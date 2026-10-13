//go:build unix

package resetpassword

import (
	"os"
	"syscall"
)

// dropToDataOwner makes a root process continue as the owner of dir.
func dropToDataOwner(dir string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid == 0 {
		return nil
	}
	if err := syscall.Setgroups([]int{int(st.Gid)}); err != nil {
		return err
	}
	if err := syscall.Setgid(int(st.Gid)); err != nil {
		return err
	}
	return syscall.Setuid(int(st.Uid))
}
