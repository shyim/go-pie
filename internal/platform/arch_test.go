package platform

import "testing"

func TestMapsKnownMachineStrings(t *testing.T) {
	cases := []struct {
		machine  string
		intSize  uint8
		expected Architecture
	}{
		{"arm64", 8, ArchArm64},
		{"aarch64", 8, ArchArm64},
		{"x86_64", 8, ArchX86_64},
		{"AMD64", 4, ArchX86_64},
		{"i686", 8, ArchX86},
		{"x86", 8, ArchX86},
	}
	for _, c := range cases {
		if got := ArchitectureFromPhpUname(c.machine, c.intSize); got != c.expected {
			t.Errorf("ArchitectureFromPhpUname(%q, %d) = %v, want %v", c.machine, c.intSize, got, c.expected)
		}
	}
}

func TestArchCaseAndWhitespaceInsensitive(t *testing.T) {
	if got := ArchitectureFromPhpUname("  ARM64\n", 8); got != ArchArm64 {
		t.Errorf("got %v, want Arm64", got)
	}
	if got := ArchitectureFromPhpUname("X86_64", 8); got != ArchX86_64 {
		t.Errorf("got %v, want X86_64", got)
	}
}

func TestArchFallsBackToIntSize(t *testing.T) {
	if got := ArchitectureFromPhpUname("sparc", 8); got != ArchX86_64 {
		t.Errorf("got %v, want X86_64", got)
	}
	if got := ArchitectureFromPhpUname("mystery", 4); got != ArchX86 {
		t.Errorf("got %v, want X86", got)
	}
}

func TestArchTokenAndDisplayAgree(t *testing.T) {
	for _, a := range []Architecture{ArchX86, ArchX86_64, ArchArm64} {
		if a.String() != a.Token() {
			t.Errorf("String()=%q != Token()=%q", a.String(), a.Token())
		}
	}
	if ArchX86_64.Token() != "x86_64" {
		t.Errorf("X86_64 token = %q", ArchX86_64.Token())
	}
	if ArchArm64.Token() != "arm64" {
		t.Errorf("Arm64 token = %q", ArchArm64.Token())
	}
}
