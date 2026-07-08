package oci

import (
	"reflect"
	"strings"
	"testing"
)

func sampleManifest() ExtManifest {
	return ExtManifest{
		ManifestVersion:  ManifestVersion,
		Extension:        "redis",
		Version:          "6.3.0",
		ExtensionType:    "php-ext",
		IniDirective:     "extension",
		Priority:         60,
		Cell:             "redis/6.3.0/php8.4/debian@12/x86_64/nts/nodebug/cfg-00000000",
		PHP:              "8.4",
		PHPAPI:           "20240924",
		Distro:           "debian@12",
		Arch:             "x86_64",
		ThreadSafety:     "nts",
		Debug:            false,
		ConfigureOptions: []string{},
		RuntimePackages: map[string][]string{
			"debian": {"libzstd1"},
			"alpine": {"zstd-libs"},
		},
		SoFile:    "redis.so",
		SoSha256:  "abc123",
		BuiltAt:   "2026-07-02T00:00:00Z",
		SourceRef: "phpredis/phpredis@6.3.0",
		Builder:   "rpie-nightly",
	}
}

func TestManifestRoundTripsThroughJSON(t *testing.T) {
	m := sampleManifest()
	bytes, err := m.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseExtManifest(bytes)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m, *back) {
		t.Fatalf("round-trip mismatch:\n%#v\n%#v", m, *back)
	}
}

func TestRuntimePackagesLookup(t *testing.T) {
	m := sampleManifest()
	if got := m.RuntimePackagesFor("debian"); !reflect.DeepEqual(got, []string{"libzstd1"}) {
		t.Errorf("debian = %v", got)
	}
	if got := m.RuntimePackagesFor("alpine"); !reflect.DeepEqual(got, []string{"zstd-libs"}) {
		t.Errorf("alpine = %v", got)
	}
	if got := m.RuntimePackagesFor("windows"); len(got) != 0 {
		t.Errorf("windows = %v, want empty", got)
	}
}

func TestManifestJSONUsesDocumentedFieldNames(t *testing.T) {
	m := sampleManifest()
	b, err := m.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	json := string(b)
	for _, key := range []string{`"rpieManifestVersion"`, `"phpApi"`, `"runtimePackages"`, `"soSha256"`} {
		if !strings.Contains(json, key) {
			t.Errorf("JSON missing key %s", key)
		}
	}
}

func TestParseExtManifestEmptyCellErrors(t *testing.T) {
	if _, err := ParseExtManifest([]byte(`{"extension":"redis"}`)); err == nil {
		t.Fatal("expected error for missing cell")
	}
}
