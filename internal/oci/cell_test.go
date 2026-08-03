package oci

import (
	"testing"

	"github.com/shyim/go-pie/internal/docker"
	"github.com/shyim/go-pie/internal/platform"
)

func TestConfigHashOrderIndependentAndStable(t *testing.T) {
	a := configHash([]string{"--enable-foo", "--with-bar=1"})
	b := configHash([]string{"--with-bar=1", "--enable-foo"})
	if a != b {
		t.Fatalf("hash must not depend on option order: %q vs %q", a, b)
	}
	if len(a) != 8 {
		t.Fatalf("hash length = %d, want 8", len(a))
	}
	if got := configHash(nil); got != "00000000" {
		t.Fatalf("empty config hash = %q, want 00000000", got)
	}
	if a == configHash([]string{"--enable-foo"}) {
		t.Fatalf("two-option hash must differ from single-option hash")
	}
}

func TestOCIArchMapping(t *testing.T) {
	cases := map[string]string{
		"x86_64":  "amd64",
		"aarch64": "arm64",
		"arm64":   "arm64",
		"x86":     "386",
		"other":   "other",
	}
	for in, want := range cases {
		if got := OCIArch(in); got != want {
			t.Errorf("OCIArch(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCellIDFormat(t *testing.T) {
	cell := Cell{
		Extension:    "redis",
		Version:      "6.3.0",
		PHP:          "8.4",
		Distro:       "debian@12",
		Arch:         "x86_64",
		ThreadSafety: platform.NonThreadSafe,
		Debug:        false,
		ConfigHash:   "00000000",
	}
	if got := cell.ID(); got != "redis/6.3.0/php8.4/debian@12/x86_64/nts/nodebug/cfg-00000000" {
		t.Errorf("cell.ID() = %q", got)
	}
	if got := cell.RepoTag(); got != "redis:6.3.0" {
		t.Errorf("cell.RepoTag() = %q", got)
	}
}

// A bundled extension is compiled from the PHP source tree and has no version
// of its own, so both its version and its PHP axis must carry the full patch
// version. Keying on major.minor would let an 8.4.24-built intl.so serve an
// 8.4.25 runtime, which phpApi cannot catch because the Zend Module API only
// changes per minor.
func TestBundledCellUsesFullPatchVersion(t *testing.T) {
	php := &platform.PhpBinary{
		Version:      platform.PhpVersion{Major: 8, Minor: 4, Patch: 24},
		APIVersion:   "20240924",
		Architecture: platform.ArchX86_64,
		ExtensionDir: "/usr/local/lib/php/extensions",
	}
	plat := platform.TargetPlatformFixture(platform.OSLinux, platform.NonThreadSafe, php)
	distro := &docker.Distro{ID: "alpine", VersionID: "3.24.1", Family: docker.FamilyAlpine}

	cell := NewBundledCell("intl", plat, distro, nil)

	want := "intl/8.4.24/php8.4.24/alpine@3.24.1/x86_64/nts/nodebug/cfg-00000000"
	if got := cell.ID(); got != want {
		t.Errorf("NewBundledCell().ID() = %q, want %q", got, want)
	}
	if cell.Version != "8.4.24" || cell.PHP != "8.4.24" {
		t.Errorf("version = %q, php = %q; both must be the full patch version",
			cell.Version, cell.PHP)
	}

	// A third-party cell for the same target keeps the major.minor PHP axis, so
	// the two keying rules must not converge.
	third := NewCell("redis", "6.3.0", plat, distro, nil)
	if third.PHP != "8.4" {
		t.Errorf("NewCell PHP axis = %q, want major.minor 8.4", third.PHP)
	}
}

func TestArchToken(t *testing.T) {
	cases := map[platform.Architecture]string{
		platform.ArchX86:    "x86",
		platform.ArchX86_64: "x86_64",
		platform.ArchArm64:  "aarch64",
	}
	for arch, want := range cases {
		if got := archToken(arch); got != want {
			t.Errorf("archToken(%v) = %q, want %q", arch, got, want)
		}
	}
}
