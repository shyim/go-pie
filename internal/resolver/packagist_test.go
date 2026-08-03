package resolver

import (
	"reflect"
	"testing"
)

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
