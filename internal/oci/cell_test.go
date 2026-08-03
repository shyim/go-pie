package oci

import (
	"testing"

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
