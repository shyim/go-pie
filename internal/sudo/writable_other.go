//go:build !unix

package sudo

import (
	"os"
	"path/filepath"
	"strings"
)

// isWritable is a best-effort check on non-Unix platforms: the path must stat
// successfully with the read-only permission bit unset. Stat failure counts as
// not writable.
func isWritable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode().Perm()&0o200 != 0
}

func parentDir(path string) string {
	if path == "" {
		return ""
	}
	trimmed := strings.TrimRight(path, string(os.PathSeparator))
	if trimmed == "" {
		return ""
	}
	if !strings.ContainsRune(trimmed, os.PathSeparator) {
		return ""
	}
	return filepath.Dir(trimmed)
}
