// Package oci is the read-side client for the prebuilt-extension cache backed
// by an OCI registry (GHCR), mirroring the Rust src/oci/* modules.
package oci

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/shyim/go-pie/internal/docker"
	"github.com/shyim/go-pie/internal/platform"
)

// Cell is the full identity of one prebuilt build.
type Cell struct {
	Extension    string
	Version      string
	PHP          string
	Distro       string
	Arch         string
	ThreadSafety platform.ThreadSafety
	Debug        bool
	ConfigHash   string
}

// NewCell builds a cell for extension@version on the given platform + distro,
// with the (already-formatted) configure options that will be used.
func NewCell(extension, ver string, p *platform.TargetPlatform, d *docker.Distro, configureOptions []string) Cell {
	return Cell{
		Extension:    strings.ToLower(extension),
		Version:      strings.ToLower(ver),
		PHP:          p.PHP.Version.MajorMinor(),
		Distro:       d.Label(),
		Arch:         archToken(p.Architecture),
		ThreadSafety: p.ThreadSafety,
		Debug:        p.PHP.DebugBuild,
		ConfigHash:   configHash(configureOptions),
	}
}

// NewBundledCell builds a cell for a PHP-bundled extension (gd, intl, zip, …)
// built by the `docker-php-ext-*` helpers.
//
// Bundled extensions are compiled from the PHP source tree, so most of them
// carry no version of their own: `intl` and `gd` have no PHP_*_VERSION at all,
// and the sources ship with (and are only valid for) one exact PHP release.
// Both the version and the PHP axis therefore use the full patch version, not
// major.minor: an `intl.so` built against 8.4.24 is not interchangeable with
// one built against 8.4.25, and `phpApi` cannot tell them apart because the
// Zend Module API only changes per minor.
//
// Third-party extensions keep the major.minor PHP axis (see NewCell): they have
// a real upstream version and are ABI-stable across a minor's patches.
func NewBundledCell(extension string, p *platform.TargetPlatform, d *docker.Distro, configureOptions []string) Cell {
	full := p.PHP.Version.String()
	return Cell{
		Extension:    strings.ToLower(extension),
		Version:      full,
		PHP:          full,
		Distro:       d.Label(),
		Arch:         archToken(p.Architecture),
		ThreadSafety: p.ThreadSafety,
		Debug:        p.PHP.DebugBuild,
		ConfigHash:   configHash(configureOptions),
	}
}

// TSToken returns "nts" / "zts".
func (c *Cell) TSToken() string {
	return c.ThreadSafety.Token()
}

// DebugToken returns "debug" / "nodebug".
func (c *Cell) DebugToken() string {
	if c.Debug {
		return "debug"
	}
	return "nodebug"
}

// ID is the canonical cell id used in the OCI annotation and matching.
func (c *Cell) ID() string {
	return strings.Join([]string{
		c.Extension,
		c.Version,
		"php" + c.PHP,
		c.Distro,
		c.Arch,
		c.TSToken(),
		c.DebugToken(),
		"cfg-" + c.ConfigHash,
	}, "/")
}

// RepoTag returns the OCI repository tag for this extension version
// ("<ext>:<version>").
func (c *Cell) RepoTag() string {
	return c.Extension + ":" + c.Version
}

// archToken maps the platform Architecture to the cell arch token. Distinct
// from OCI's amd64/arm64 platform names.
func archToken(arch platform.Architecture) string {
	switch arch {
	case platform.ArchX86:
		return "x86"
	case platform.ArchX86_64:
		return "x86_64"
	case platform.ArchArm64:
		return "aarch64"
	default:
		return "x86"
	}
}

// configHash hashes the configure options into a short, stable,
// order-independent token. No options → the all-zero sentinel.
func configHash(options []string) string {
	if len(options) == 0 {
		return strings.Repeat("0", 8)
	}
	sorted := make([]string, len(options))
	copy(sorted, options)
	sort.Strings(sorted)
	// Length-prefix each option instead of space-joining: a bare join is not
	// injective, so ["--with-foo=a b"] and ["--with-foo=a", "b"] would hash to
	// the same cell and serve each other's prebuilt binary. The digest stays 8
	// hex chars because published cell IDs (and the cfg-00000000 sentinel)
	// already encode that width.
	h := sha256.New()
	var lenBuf [8]byte
	for _, opt := range sorted {
		binary.BigEndian.PutUint64(lenBuf[:], uint64(len(opt)))
		_, _ = h.Write(lenBuf[:])
		_, _ = h.Write([]byte(opt))
	}
	return hex.EncodeToString(h.Sum(nil))[:8]
}

// OCIArch maps our arch token to the OCI platform.architecture value.
func OCIArch(arch string) string {
	switch arch {
	case "x86_64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	case "x86":
		return "386"
	default:
		return arch
	}
}
