//go:build unix

package sudo

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// isWritable checks writability using access(2) with the real uid (W_OK). Using
// the real uid is exactly what "would I need sudo" wants. A path containing a
// NUL byte is treated as not writable (unix.Access rejects it).
func isWritable(path string) bool {
	return unix.Access(path, unix.W_OK) == nil
}

// parentDir mirrors Rust's Path::parent for the branches path_needs_sudo cares
// about. Rust yields None for "/" and "", and Some("") for a bare file name;
// both an empty parent and a not-writable parent make the caller return true,
// so returning "" for all no-real-parent cases preserves behavior.
func parentDir(path string) string {
	if path == "" {
		return ""
	}
	trimmed := strings.TrimRight(path, "/")
	if trimmed == "" {
		return ""
	}
	if !strings.Contains(trimmed, "/") {
		return ""
	}
	return filepath.Dir(trimmed)
}
