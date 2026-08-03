package platform

import (
	"os"
	"path/filepath"
	"runtime"
)

// IsRunningAsRoot reports whether the current process runs as uid 0. Always
// false on Windows.
func IsRunningAsRoot() bool {
	if runtime.GOOS == "windows" {
		return false
	}
	return os.Getuid() == 0
}

type LibcFlavour int

const (
	Glibc LibcFlavour = iota
	Musl
	NonLinux
)

// DetectLibc detects the C library flavour on Linux (glibc vs musl). macOS and
// other OSes return NonLinux.
func DetectLibc() LibcFlavour {
	if runtime.GOOS != "linux" {
		return NonLinux
	}
	// Glob rather than probing fixed names so musl is detected on every
	// architecture (armhf, riscv64, s390x, ...) and lib dir layout.
	for _, pattern := range []string{
		"/lib/ld-musl-*.so.1",
		"/lib64/ld-musl-*.so.1",
		"/usr/lib/ld-musl-*.so.1",
	} {
		if matches, err := filepath.Glob(pattern); err == nil && len(matches) > 0 {
			return Musl
		}
	}
	return Glibc
}

func (l LibcFlavour) Token() string {
	switch l {
	case Glibc:
		return "glibc"
	case Musl:
		return "musl"
	case NonLinux:
		return "anylibc"
	default:
		return "anylibc"
	}
}
