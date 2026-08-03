// Package platform introspects the target PHP binary and build environment,
// mirroring the Rust src/platform/* modules.
package platform

import "strings"

type Architecture int

const (
	ArchX86 Architecture = iota
	ArchX86_64
	ArchArm64
)

func (a Architecture) Token() string {
	switch a {
	case ArchX86:
		return "x86"
	case ArchX86_64:
		return "x86_64"
	case ArchArm64:
		return "arm64"
	default:
		return "x86"
	}
}

func (a Architecture) String() string {
	return a.Token()
}

func ArchitectureFromPhpUname(machine string, intSizeBytes uint8) Architecture {
	switch strings.ToLower(strings.TrimSpace(machine)) {
	case "arm64", "aarch64":
		return ArchArm64
	case "x86_64", "amd64":
		return ArchX86_64
	case "i386", "i486", "i586", "i686", "x86":
		return ArchX86
	default:
		// Unrecognised machine strings fall back to pointer width. This is
		// deliberate Rust parity (TestArchFallsBackToIntSize pins it) and is
		// wrong for genuinely non-x86 hosts such as riscv64 or s390x; changing
		// it is an intentional behaviour change, not a bug fix, because it
		// alters published asset names and OCI cell identities.
		if intSizeBytes >= 8 {
			return ArchX86_64
		}
		return ArchX86
	}
}
