//go:build unix

package libsdl

import (
	"os"
	"strconv"
	"syscall"
)

// ownedByUs reports whether info describes an entry owned by the current
// user that no other user can write to.
func ownedByUs(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return int(st.Uid) == os.Getuid() && info.Mode().Perm()&0o022 == 0
}

// userTag distinguishes per-user fallback directories in a shared temp dir.
func userTag() string { return strconv.Itoa(os.Getuid()) }
