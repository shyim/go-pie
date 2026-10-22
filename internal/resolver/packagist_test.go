package resolver

import (
	"os"
	"reflect"
	"testing"
)

func TestParsesMinifiedVersionMetadata(t *testing.T) {
	body, err := os.ReadFile("testdata/packagist-minified.json")
	if err != nil {
		t.Fatal(err)
	}
	versions, err := ParseVersionsJSON(body, "xdebug/xdebug")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 5 {
		t.Fatalf("len = %d", len(versions))
	}
	// The stable releases inherit their type and extension metadata from an
	// alpha. Replaced nested objects must not keep the alpha's old fields.
	for _, i := range []int{1, 2} {
		v := versions[i]
		if v.PackageType != "php-ext-zend" || string(v.PhpExt) != `{"priority": 90}` {
			t.Errorf("[%d] lost inherited metadata: %+v", i, v)
		}
		if !reflect.DeepEqual(v.Requires, map[string]string{"php": ">=8.0,<8.6"}) || len(v.LibRequires) != 0 {
			t.Errorf("[%d] requires = %v, libs = %v", i, v.Requires, v.LibRequires)
		}
		if v.DistURL == nil || *v.DistURL != "https://example.com/stable.zip" || v.DistShasum != nil {
			t.Errorf("[%d] dist metadata = %+v", i, v)
		}
		if v.SourceURL == nil || *v.SourceURL != "https://example.com/xdebug.git" {
			t.Errorf("[%d] source = %v", i, v.SourceURL)
		}
	}
	// Clearing a field must persist through subsequent deltas without changing
	// any earlier release's snapshot.
	for _, i := range []int{3, 4} {
		v := versions[i]
		if v.PackageType != "" || len(v.PhpExt) != 0 || len(v.Requires) != 0 || v.DistURL != nil || v.SourceURL != nil {
			t.Errorf("[%d] retained unset metadata: %+v", i, v)
		}
	}
	if versions[0].Requires["lib-zlib"] != "*" || versions[0].DistShasum == nil {
		t.Error("expansion changed the first release")
	}
	if versions[1].Version != "3.5.3" || versions[2].Version != "3.5.2" {
		t.Error("expansion changed release versions")
	}
}

func TestParsesFullVersionMetadata(t *testing.T) {
	body := []byte(`{
		"packages": {
			"phpredis/phpredis": [{
				"version": "6.1.0",
				"version_normalized": "6.1.0.0",
				"type": "php-ext",
				"dist": {"url": "https://d/x.zip", "type": "zip", "shasum": "abc"},
				"source": {"url": "https://github.com/phpredis/phpredis.git"},
				"require": {"php": ">=7.4", "lib-zip": "*", "ext-json": "*"},
				"php-ext": {"extension-name": "redis", "priority": 20}
			}]
		}
	}`)
	versions, err := ParseVersionsJSON(body, "phpredis/phpredis")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 {
		t.Fatalf("len = %d", len(versions))
	}
	v := versions[0]
	if v.Version != "6.1.0" {
		t.Errorf("version = %q", v.Version)
	}
	if v.PackageType != "php-ext" {
		t.Errorf("type = %q", v.PackageType)
	}
	if v.DistURL == nil || *v.DistURL != "https://d/x.zip" {
		t.Errorf("dist url = %v", v.DistURL)
	}
	if v.DistShasum == nil || *v.DistShasum != "abc" {
		t.Errorf("dist shasum = %v", v.DistShasum)
	}
	if !reflect.DeepEqual(v.LibRequires, []string{"zip"}) {
		t.Errorf("lib_requires = %v", v.LibRequires)
	}
	if v.Requires["php"] != ">=7.4" {
		t.Errorf("requires[php] = %q", v.Requires["php"])
	}
	if len(v.PhpExt) == 0 {
		t.Errorf("php_ext should be present")
	}
}

func TestMissingPackageErrors(t *testing.T) {
	body := []byte(`{ "packages": {} }`)
	if _, err := ParseVersionsJSON(body, "vendor/nope"); err == nil {
		t.Error("expected error")
	}
}

func TestOlderVersionsMayOmitType(t *testing.T) {
	body := []byte(`{
		"packages": {
			"a/b": [
				{"version": "2.0.9", "version_normalized": "2.0.9.0", "type": "php-ext"},
				{"version": "2.0.5", "version_normalized": "2.0.5.0"}
			]
		}
	}`)
	versions, err := ParseVersionsJSON(body, "a/b")
	if err != nil {
		t.Fatal(err)
	}
	if versions[0].PackageType != "php-ext" {
		t.Errorf("[0] type = %q", versions[0].PackageType)
	}
	if versions[1].PackageType != "" {
		t.Errorf("[1] type = %q", versions[1].PackageType)
	}
}
