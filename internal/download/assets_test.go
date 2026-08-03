package download

import (
	"strings"
	"testing"

	"github.com/shyim/go-pie/internal/platform"
	"github.com/shyim/go-pie/internal/resolver"
)

func testPkg() *resolver.ResolvedPackage {
	return &resolver.ResolvedPackage{
		Name:          "phpredis/phpredis",
		Version:       "6.1.0",
		ExtensionName: "redis",
		ExtensionType: resolver.PhpModule,
		Metadata:      resolver.MetadataFromValue(nil, "phpredis/phpredis"),
		Priority:      80,
	}
}

func TestSourceNamesFollowPieConventions(t *testing.T) {
	names := sourceAssetNames(testPkg())
	want := []string{
		"php_redis-6.1.0-src.tgz",
		"php_redis-6.1.0-src.zip",
		"redis-6.1.0.tgz",
	}
	if len(names) != len(want) {
		t.Fatalf("got %v", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("index %d: got %q want %q", i, names[i], want[i])
		}
	}
}

func TestBinaryNamesMatchPieFormatForLinuxGlibcNts(t *testing.T) {
	names := binaryAssetNamesInner(
		"example_pie_extension", "2.0.9", "8.4", "x86_64", "linux",
		false, platform.NonThreadSafe, platform.Glibc,
	)
	if !contains(names, "php_example_pie_extension-2.0.9_php8.4-x86_64-linux-glibc.zip") {
		t.Fatalf("missing glibc name: %v", names)
	}
	if !contains(names, "php_example_pie_extension-2.0.9_php8.4-x86_64-linux-glibc-nts.zip") {
		t.Fatalf("missing glibc-nts name: %v", names)
	}
	anylibc := false
	for _, n := range names {
		if strings.Contains(n, "-anylibc") {
			anylibc = true
		}
		if n != strings.ToLower(n) {
			t.Fatalf("not lowercase: %q", n)
		}
	}
	if !anylibc {
		t.Fatalf("no anylibc name: %v", names)
	}
	seen := map[string]struct{}{}
	for _, n := range names {
		if _, ok := seen[n]; ok {
			t.Fatalf("duplicate name %q", n)
		}
		seen[n] = struct{}{}
	}
}

func TestBinaryNamesZtsUsesZtsSuffixAndNoAnylibcOffLinux(t *testing.T) {
	names := binaryAssetNamesInner(
		"redis", "6.1.0", "8.3", "arm64", "darwin",
		false, platform.ThreadSafe, platform.NonLinux,
	)
	if !contains(names, "php_redis-6.1.0_php8.3-arm64-darwin-anylibc-zts.zip") {
		t.Fatalf("missing darwin zts name: %v", names)
	}
	for _, n := range names {
		if strings.Contains(n, "glibc") || strings.Contains(n, "musl") {
			t.Fatalf("unexpected libc token: %q", n)
		}
	}
}

func TestWindowsNamesMatchPieFormat(t *testing.T) {
	names := windowsAssetNamesInner("redis", "6.1.0", "8.4", platform.NonThreadSafe, "vs17", "x86_64")
	if !contains(names, "php_redis-6.1.0-8.4-nts-vs17-x86_64.zip") {
		t.Fatalf("missing name: %v", names)
	}
	if !contains(names, "php_redis-6.1.0-8.4-vs17-nts-x86_64.zip") {
		t.Fatalf("missing name: %v", names)
	}
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %v", names)
	}

	ts := windowsAssetNamesInner("xdebug", "3.4.0", "8.3", platform.ThreadSafe, "vs16", "x86")
	if !contains(ts, "php_xdebug-3.4.0-8.3-ts-vs16-x86.zip") {
		t.Fatalf("missing ts name: %v", ts)
	}
}

//nolint:modernize // Keep this compatible with golangci-lint's current Go stdlib loader.
func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
