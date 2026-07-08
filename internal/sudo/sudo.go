// Package sudo mirrors PIE's Php\Pie\File\Sudo: it decides whether the sudo
// binary is usable and whether writing a given path requires elevation.
package sudo

import (
	"os"
	"os/exec"

	"golang.org/x/term"

	"github.com/shyim/go-pie/internal/platform"
)

// IsAvailable reports whether sudo is usable: the binary must be on PATH and we
// must either already be root or have an interactive terminal on stdout (PIE
// refuses sudo in non-interactive contexts, where it would hang on the prompt).
func IsAvailable() bool {
	if _, err := exec.LookPath("sudo"); err != nil {
		return false
	}
	return platform.IsRunningAsRoot() || isInteractiveTerminal()
}

// PathNeedsSudo reports whether writing to path (or creating it inside its
// parent directory) requires sudo.
func PathNeedsSudo(path string) bool {
	if platform.IsRunningAsRoot() {
		return false
	}
	if _, err := os.Stat(path); err == nil {
		return !isWritable(path)
	}
	parent := parentDir(path)
	if parent == "" {
		return true
	}
	return !isWritable(parent)
}

func isInteractiveTerminal() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}
