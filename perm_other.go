//go:build !unix

package libsdl

import "os"

// ownedByUs is a no-op on platforms without Unix ownership semantics; the
// per-user cache directory is protected by its ACL there.
func ownedByUs(os.FileInfo) bool { return true }

func userTag() string { return "user" }
