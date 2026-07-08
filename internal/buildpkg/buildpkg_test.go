package buildpkg

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/shyim/go-pie/internal/download"
	"github.com/shyim/go-pie/internal/platform"
	"github.com/shyim/go-pie/internal/resolver"
)

func fixturePlatform() *platform.TargetPlatform {
	php := platform.PhpBinaryFixture(8, 4, 3, platform.ArchX86_64)
	return platform.TargetPlatformFixture(platform.OSLinux, platform.NonThreadSafe, php)
}

func sourcePackage(dir string) *download.DownloadedPackage {
	return &download.DownloadedPackage{
		Artifact: download.Artifact{Kind: download.ArtifactSource, Path: dir},
	}
}

func binaryPackage(path string) *download.DownloadedPackage {
	return &download.DownloadedPackage{
		Artifact: download.Artifact{Kind: download.ArtifactBinary, Path: path},
	}
}

func TestFromPrebuilt(t *testing.T) {
	b := FromPrebuilt("/some/path/redis.so")
	if b.BinaryPath != "/some/path/redis.so" {
		t.Fatalf("BinaryPath = %q", b.BinaryPath)
	}
}

func TestBuildRejectsPrePackagedBinary(t *testing.T) {
	pkg := &resolver.ResolvedPackage{Name: "phpredis/redis", ExtensionName: "redis"}
	src := binaryPackage("/tmp/redis.so")
	plat := fixturePlatform()

	_, err := Build(pkg, src, plat, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	want := "cannot build `phpredis/redis`: the download is a pre-packaged binary, not source"
	if err.Error() != want {
		t.Fatalf("got %q, want %q", err.Error(), want)
	}
}

func TestBuildRejectsMissingPhpize(t *testing.T) {
	pkg := &resolver.ResolvedPackage{Name: "phpredis/redis", ExtensionName: "redis"}
	src := sourcePackage(t.TempDir())
	plat := fixturePlatform() // fixture leaves Phpize nil

	_, err := Build(pkg, src, plat, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	want := "no usable phpize found for the target PHP (API 20240924); pass --with-phpize-path"
	if err.Error() != want {
		t.Fatalf("got %q, want %q", err.Error(), want)
	}
}

// fakeToolPlatform wires a phpize/configure/make backed by the given scripts so
// the build pipeline can be exercised without real PHP tooling.
func fakeToolPlatform(t *testing.T, dir, phpizeScript string) *platform.TargetPlatform {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("sh scripts unavailable on windows")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	phpize := filepath.Join(dir, "phpize")
	if err := os.WriteFile(phpize, []byte(phpizeScript), 0o755); err != nil {
		t.Fatal(err)
	}
	plat := fixturePlatform()
	plat.Phpize = &platform.PhpizePath{Path: phpize}
	return plat
}

// writeConfigure drops a ./configure script into the source dir.
func writeConfigure(t *testing.T, srcDir, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(srcDir, "configure"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestBuildMissingBinaryAfterSteps(t *testing.T) {
	toolDir := t.TempDir()
	srcDir := t.TempDir()

	plat := fakeToolPlatform(t, toolDir, "#!/bin/sh\nexit 0\n")
	writeConfigure(t, srcDir, "#!/bin/sh\nexit 0\n")

	// A `make` that succeeds but produces no .so.
	makeStub := filepath.Join(toolDir, "make")
	if err := os.WriteFile(makeStub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", toolDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	pkg := &resolver.ResolvedPackage{Name: "phpredis/redis", ExtensionName: "redis"}
	src := sourcePackage(srcDir)

	_, err := Build(pkg, src, plat, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	wantSuffix := filepath.Join(srcDir, "modules", "redis.so")
	if !strings.Contains(err.Error(), "build completed but expected binary was not found at "+wantSuffix) {
		t.Fatalf("got %q", err.Error())
	}
}

func TestBuildSucceedsEndToEnd(t *testing.T) {
	toolDir := t.TempDir()
	srcDir := t.TempDir()

	plat := fakeToolPlatform(t, toolDir, "#!/bin/sh\nexit 0\n")
	writeConfigure(t, srcDir, "#!/bin/sh\nexit 0\n")

	// make creates modules/<ext>.so in the current directory (cwd == srcDir).
	makeStub := filepath.Join(toolDir, "make")
	makeBody := "#!/bin/sh\nmkdir -p modules && : > modules/redis.so\nexit 0\n"
	if err := os.WriteFile(makeStub, []byte(makeBody), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", toolDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	pkg := &resolver.ResolvedPackage{Name: "phpredis/redis", ExtensionName: "redis"}
	src := sourcePackage(srcDir)

	built, err := Build(pkg, src, plat, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(srcDir, "modules", "redis.so")
	if built.BinaryPath != want {
		t.Fatalf("BinaryPath = %q, want %q", built.BinaryPath, want)
	}
}

func TestBuildWithSinkCapturesOutput(t *testing.T) {
	toolDir := t.TempDir()
	srcDir := t.TempDir()

	// phpize echoes a marker; configure and make succeed and produce the .so.
	plat := fakeToolPlatform(t, toolDir, "#!/bin/sh\necho PHPIZE_RAN\nexit 0\n")
	writeConfigure(t, srcDir, "#!/bin/sh\necho CONFIGURE_RAN\nexit 0\n")

	makeStub := filepath.Join(toolDir, "make")
	makeBody := "#!/bin/sh\necho MAKE_RAN\nmkdir -p modules && : > modules/redis.so\nexit 0\n"
	if err := os.WriteFile(makeStub, []byte(makeBody), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", toolDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	pkg := &resolver.ResolvedPackage{Name: "phpredis/redis", ExtensionName: "redis"}
	src := sourcePackage(srcDir)

	var sink bytes.Buffer
	_, err := BuildWith(pkg, src, plat, nil, 1, &sink)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := sink.String()
	for _, marker := range []string{"PHPIZE_RAN", "CONFIGURE_RAN", "MAKE_RAN"} {
		if !strings.Contains(out, marker) {
			t.Fatalf("sink missing %q; got:\n%s", marker, out)
		}
	}
}

// TestBuildConfigurePassesPhpConfig verifies the --with-php-config arg and the
// post-`--` configure options are forwarded verbatim, in order.
func TestBuildConfigurePassesPhpConfig(t *testing.T) {
	toolDir := t.TempDir()
	srcDir := t.TempDir()

	plat := fakeToolPlatform(t, toolDir, "#!/bin/sh\nexit 0\n")
	plat.PhpConfig = "/usr/bin/php-config"

	// configure records its argv into a file we can inspect.
	argsFile := filepath.Join(srcDir, "configure_args")
	writeConfigure(t, srcDir, "#!/bin/sh\nprintf '%s\\n' \"$@\" > "+argsFile+"\nexit 0\n")

	makeStub := filepath.Join(toolDir, "make")
	makeBody := "#!/bin/sh\nmkdir -p modules && : > modules/redis.so\nexit 0\n"
	if err := os.WriteFile(makeStub, []byte(makeBody), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", toolDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	pkg := &resolver.ResolvedPackage{Name: "phpredis/redis", ExtensionName: "redis"}
	src := sourcePackage(srcDir)

	_, err := Build(pkg, src, plat, []string{"--enable-redis-igbinary", "--with-foo"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	want := []string{"--with-php-config=/usr/bin/php-config", "--enable-redis-igbinary", "--with-foo"}
	if len(got) != len(want) {
		t.Fatalf("configure args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("configure arg[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestBuildConfigureFailureError verifies the ./configure error is rendered with
// the relative program string verbatim (parity with the Rust message shape).
func TestBuildConfigureFailureError(t *testing.T) {
	toolDir := t.TempDir()
	srcDir := t.TempDir()

	plat := fakeToolPlatform(t, toolDir, "#!/bin/sh\nexit 0\n")
	writeConfigure(t, srcDir, "#!/bin/sh\nexit 1\n")

	pkg := &resolver.ResolvedPackage{Name: "phpredis/redis", ExtensionName: "redis"}
	src := sourcePackage(srcDir)

	_, err := Build(pkg, src, plat, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "./configure failed: `./configure") {
		t.Fatalf("error should reference relative ./configure: %q", msg)
	}
	if !strings.Contains(msg, "exited with exit status: 1") {
		t.Fatalf("error should render exit status: 1: %q", msg)
	}
}
