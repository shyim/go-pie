package resolver

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/shyim/go-pie/internal/platform"
)

type fakeSource struct {
	body []byte
}

func (f fakeSource) PackageVersions(_ context.Context, pkg string) ([]PackageVersion, error) {
	return ParseVersionsJSON(f.body, pkg)
}

func sourceOf(versionsJSON string) fakeSource {
	return fakeSource{body: fmt.Appendf(nil, `{"packages":{"vendor/ext":%s}}`, versionsJSON)}
}

func fixturePlatform(ts platform.ThreadSafety) *platform.TargetPlatform {
	return platform.TargetPlatformFixture(
		platform.OSLinux,
		ts,
		platform.PhpBinaryFixture(8, 4, 3, platform.ArchX86_64),
	)
}

func mustParseRequest(t *testing.T, s string) *RequestedPackage {
	t.Helper()
	r, err := ParseRequest(s)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestResolvesHighestStablePhpExt(t *testing.T) {
	src := sourceOf(`[
		{"version": "2.0.9", "version_normalized": "2.0.9.0", "type": "php-ext",
		 "php-ext": {"extension-name": "ext", "priority": 30}},
		{"version": "2.0.5", "version_normalized": "2.0.5.0", "type": "php-ext"}
	]`)
	req := mustParseRequest(t, "vendor/ext")
	r, err := Resolve(t.Context(), src, req, fixturePlatform(platform.NonThreadSafe))
	if err != nil {
		t.Fatal(err)
	}
	if r.Version != "2.0.9" {
		t.Errorf("version = %q", r.Version)
	}
	if r.ExtensionName != "ext" {
		t.Errorf("extension name = %q", r.ExtensionName)
	}
	if r.Priority != 30 {
		t.Errorf("priority = %d", r.Priority)
	}
}

func TestTypeFallsBackAcrossVersions(t *testing.T) {
	src := sourceOf(`[
		{"version": "2.0.9", "version_normalized": "2.0.9.0", "type": "php-ext"},
		{"version": "2.0.5", "version_normalized": "2.0.5.0"}
	]`)
	req := mustParseRequest(t, "vendor/ext:2.0.5")
	r, err := Resolve(t.Context(), src, req, fixturePlatform(platform.NonThreadSafe))
	if err != nil {
		t.Fatal(err)
	}
	if r.Version != "2.0.5" {
		t.Errorf("version = %q", r.Version)
	}
}

func TestRejectsNonExtensionType(t *testing.T) {
	src := sourceOf(`[
		{"version": "1.0.0", "version_normalized": "1.0.0.0", "type": "library"}
	]`)
	req := mustParseRequest(t, "vendor/ext")
	_, err := Resolve(t.Context(), src, req, fixturePlatform(platform.NonThreadSafe))
	if err == nil || !strings.Contains(err.Error(), "not a PHP extension") {
		t.Errorf("err = %v", err)
	}
}

func TestRejectsUnsafeMetadataExtensionName(t *testing.T) {
	src := sourceOf(`[
		{"version": "1.0.0", "version_normalized": "1.0.0.0", "type": "php-ext",
		 "php-ext": {"extension-name": "../../outside"}}
	]`)
	req := mustParseRequest(t, "vendor/ext")
	_, err := Resolve(t.Context(), src, req, fixturePlatform(platform.NonThreadSafe))
	if err == nil || !strings.Contains(err.Error(), "invalid extension name") {
		t.Fatalf("err = %v", err)
	}
}

func TestRejectsIncompatibleThreadSafety(t *testing.T) {
	src := sourceOf(`[
		{"version": "1.0.0", "version_normalized": "1.0.0.0", "type": "php-ext",
		 "php-ext": {"support-zts": false}}
	]`)
	req := mustParseRequest(t, "vendor/ext")
	_, err := Resolve(t.Context(), src, req, fixturePlatform(platform.ThreadSafe))
	if err == nil || !strings.Contains(err.Error(), "thread-safety") {
		t.Errorf("err = %v", err)
	}
}

func TestRejectsExcludedOsFamily(t *testing.T) {
	src := sourceOf(`[
		{"version": "1.0.0", "version_normalized": "1.0.0.0", "type": "php-ext",
		 "php-ext": {"os-families-exclude": ["linux"]}}
	]`)
	req := mustParseRequest(t, "vendor/ext")
	_, err := Resolve(t.Context(), src, req, fixturePlatform(platform.NonThreadSafe))
	if err == nil || !strings.Contains(err.Error(), "operating system") {
		t.Errorf("err = %v", err)
	}
}
