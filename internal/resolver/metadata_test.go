package resolver

import (
	"encoding/json"
	"testing"

	"github.com/shyim/go-pie/internal/platform"
)

func TestDerivesExtensionName(t *testing.T) {
	if got := deriveExtensionNameFromPackage("asgrim/example-pie-extension"); got != "example_pie_extension" {
		t.Errorf("got %q", got)
	}
}

func TestValidatesExtensionNames(t *testing.T) {
	for _, name := range []string{"redis", "pdo_mysql", "Xdebug2"} {
		if err := ValidateExtensionName(name); err != nil {
			t.Errorf("ValidateExtensionName(%q): %v", name, err)
		}
	}
	for _, name := range []string{"", "../evil", "foo/bar", "foo-bar", "foo\nextension=evil"} {
		if err := ValidateExtensionName(name); err == nil {
			t.Errorf("ValidateExtensionName(%q) unexpectedly succeeded", name)
		}
	}
}

func TestMetadataParsesDefaults(t *testing.T) {
	meta := MetadataFromValue(json.RawMessage(`{}`), "vendor/my-ext")
	if meta.ExtensionName != "my_ext" {
		t.Errorf("extension name = %q", meta.ExtensionName)
	}
	if !meta.SupportZts || !meta.SupportNts {
		t.Errorf("support flags = %v %v", meta.SupportZts, meta.SupportNts)
	}
	if meta.Priority != 80 {
		t.Errorf("priority = %d", meta.Priority)
	}
	if len(meta.DownloadUrlMethods) != 1 || meta.DownloadUrlMethods[0] != ComposerDefault {
		t.Errorf("download methods = %v", meta.DownloadUrlMethods)
	}
}

func TestMetadataRejectsOverflowingPriority(t *testing.T) {
	meta := MetadataFromValue(json.RawMessage(`{"priority":4294967296}`), "vendor/my-ext")
	if meta.Priority != 80 {
		t.Fatalf("priority = %d, want default 80", meta.Priority)
	}
}

func TestMetadataParsesFull(t *testing.T) {
	raw := json.RawMessage(`{
		"extension-name": "ext-redis",
		"support-zts": false,
		"priority": 20,
		"os-families-exclude": ["windows"],
		"configure-options": [
			{"name": "enable-foo"},
			{"name": "with-bar", "needs-value": true, "description": "the bar"}
		],
		"download-url-method": ["pre-packaged-binary", "composer-default"]
	}`)
	meta := MetadataFromValue(raw, "phpredis/phpredis")
	if meta.ExtensionName != "redis" {
		t.Errorf("extension name = %q", meta.ExtensionName)
	}
	if meta.SupportZts {
		t.Errorf("support-zts should be false")
	}
	if meta.Priority != 20 {
		t.Errorf("priority = %d", meta.Priority)
	}
	if len(meta.ConfigureOptions) != 2 {
		t.Fatalf("configure options len = %d", len(meta.ConfigureOptions))
	}
	if !meta.ConfigureOptions[1].NeedsValue {
		t.Errorf("configure[1].NeedsValue should be true")
	}
	if meta.SupportsOsFamily(platform.FamilyWindows) {
		t.Errorf("windows should not be supported")
	}
	if !meta.SupportsOsFamily(platform.FamilyLinux) {
		t.Errorf("linux should be supported")
	}
	if meta.DownloadUrlMethods[0] != PrePackagedBinary {
		t.Errorf("download[0] = %v", meta.DownloadUrlMethods[0])
	}
}
