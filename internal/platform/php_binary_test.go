package platform

import "testing"

func TestParsesVersion(t *testing.T) {
	v, err := parseVersion("8.4.3")
	if err != nil {
		t.Fatal(err)
	}
	if v != (PhpVersion{8, 4, 3}) {
		t.Errorf("got %v", v)
	}
	if v.MajorMinor() != "8.4" {
		t.Errorf("MajorMinor = %q", v.MajorMinor())
	}
}

func TestDetectsDebugBuild(t *testing.T) {
	if !phpinfoDebugBuild("Debug Build => yes") {
		t.Error("expected true for yes")
	}
	if phpinfoDebugBuild("Debug Build => no") {
		t.Error("expected false for no")
	}
	if phpinfoDebugBuild("Some Line => yes") {
		t.Error("expected false for non-matching prefix")
	}
}

func TestParsesPhpAPI(t *testing.T) {
	got, ok := phpinfoPhpAPI("PHP API => 20240924\nfoo => bar")
	if !ok || got != "20240924" {
		t.Errorf("got %q ok=%v", got, ok)
	}
	if _, ok := phpinfoPhpAPI("nothing here"); ok {
		t.Error("expected no match")
	}
}

func TestParsesExtensionDirLocalValue(t *testing.T) {
	line := "extension_dir => /usr/lib/php/global => /usr/lib/php/20240924"
	got, ok := phpinfoExtensionDir(line)
	if !ok || got != "/usr/lib/php/20240924" {
		t.Errorf("got %q ok=%v", got, ok)
	}
	got, ok = phpinfoExtensionDir("extension_dir => /usr/lib/php/ext")
	if !ok || got != "/usr/lib/php/ext" {
		t.Errorf("got %q ok=%v", got, ok)
	}
	if _, ok := phpinfoExtensionDir("no such line"); ok {
		t.Error("expected no match")
	}
}

func TestParsesCombinedConstants(t *testing.T) {
	c, ok := parseConstants("8.4.3\n0\n8\narm64\nGPIE_OK")
	if !ok {
		t.Fatal("should parse")
	}
	if c.version != (PhpVersion{8, 4, 3}) {
		t.Errorf("version %v", c.version)
	}
	if c.threadSafe {
		t.Error("expected not thread safe")
	}
	if c.intSize != 8 {
		t.Errorf("intSize %d", c.intSize)
	}
	if c.machine != "arm64" {
		t.Errorf("machine %q", c.machine)
	}
}

func TestRejectsConstantsWithoutMarker(t *testing.T) {
	if _, ok := parseConstants("8.4.3\n1\n8\nx86_64"); ok {
		t.Error("expected rejection without marker")
	}
	if _, ok := parseConstants("garbage"); ok {
		t.Error("expected rejection for garbage")
	}
}

func TestPhpVersionString(t *testing.T) {
	v := PhpVersion{8, 4, 3}
	if v.String() != "8.4.3" {
		t.Errorf("String = %q", v.String())
	}
}
