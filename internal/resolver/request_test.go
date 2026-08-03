package resolver

import (
	"strings"
	"testing"
)

// ver builds a PackageVersion with a crude normalizer, mirroring the Rust test helper.
func ver(v string) PackageVersion {
	return PackageVersion{
		Version:           v,
		VersionNormalized: normalizeTest(v),
		PackageType:       "php-ext",
		Requires:          map[string]string{},
	}
}

func normalizeTest(v string) string {
	core := strings.TrimLeft(v, "v")
	dots := len(strings.Split(core, "."))
	switch dots {
	case 1:
		return core + ".0.0"
	case 2:
		return core + ".0"
	default:
		return core
	}
}

func TestParsesRequest(t *testing.T) {
	r, err := ParseRequest("phpredis/phpredis:^6.0")
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "phpredis/phpredis" {
		t.Errorf("name = %q", r.Name)
	}
	if r.Constraint != "^6.0" {
		t.Errorf("constraint = %q", r.Constraint)
	}

	r2, err := ParseRequest("vendor/ext")
	if err != nil {
		t.Fatal(err)
	}
	if r2.Constraint != "" {
		t.Errorf("constraint should be empty, got %q", r2.Constraint)
	}

	if _, err := ParseRequest("notapackage"); err == nil {
		t.Error("expected error for notapackage")
	}
}

func chosenVersion(t *testing.T, versions []PackageVersion, constraint string) string {
	t.Helper()
	idx, err := selectVersion(versions, constraint)
	if err != nil {
		t.Fatal(err)
	}
	return versions[idx].Version
}

func TestSelectsHighestStable(t *testing.T) {
	versions := []PackageVersion{ver("5.3.7"), ver("6.0.2"), ver("6.1.0"), ver("6.2.0-rc1")}
	if got := chosenVersion(t, versions, ""); got != "6.1.0" {
		t.Errorf("got %q", got)
	}
}

func TestCaretConstraint(t *testing.T) {
	versions := []PackageVersion{ver("5.3.7"), ver("6.0.2"), ver("6.1.0"), ver("7.0.0")}
	if got := chosenVersion(t, versions, "^6.0"); got != "6.1.0" {
		t.Errorf("got %q", got)
	}
}

func TestExactConstraint(t *testing.T) {
	versions := []PackageVersion{ver("6.0.2"), ver("6.1.0")}
	if got := chosenVersion(t, versions, "6.0.2"); got != "6.0.2" {
		t.Errorf("got %q", got)
	}
}

func TestParseRequestRejectsEmptySegments(t *testing.T) {
	for _, spec := range []string{"/", "/name", "vendor/", "/name:1.0"} {
		if _, err := ParseRequest(spec); err == nil {
			t.Errorf("ParseRequest(%q) should reject an empty vendor or package segment", spec)
		}
	}
	if _, err := ParseRequest("vendor/name"); err != nil {
		t.Errorf("ParseRequest(\"vendor/name\") = %v, want nil", err)
	}
}
