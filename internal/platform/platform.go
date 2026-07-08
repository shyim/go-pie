package platform

import (
	"os"
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
	if fileExists("/lib/ld-musl-x86_64.so.1") || fileExists("/lib/ld-musl-aarch64.so.1") {
		return Musl
	}
	return Glibc
}

func (l LibcFlavour) Token() string {
	switch l {
	case Glibc:
		return "glibc"
	case Musl:
		return "musl"
	default:
		return "anylibc"
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
