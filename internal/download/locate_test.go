package download

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shyim/go-pie/internal/resolver"
)

func mkfile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLocateSourceDirSingleSubdirWithConfig(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "phpredis-6.1.0", "config.m4"), "AC_INIT")
	got, err := locateSourceDir(root, testPkg())
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "phpredis-6.1.0" {
		t.Fatalf("got %q", got)
	}
}

func TestLocateSourceDirNested(t *testing.T) {
	// Rust's find_config_m4 returns the config.m4 file path itself (not its dir);
	// port that faithfully.
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "top", "src", "config.m4"), "AC_INIT")
	got, err := locateSourceDir(root, testPkg())
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "config.m4" || filepath.Base(filepath.Dir(got)) != "src" {
		t.Fatalf("got %q", got)
	}
}

func TestLocateSourceDirMissing(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "top", "readme.txt"), "hi")
	_, err := locateSourceDir(root, testPkg())
	if err == nil || !strings.Contains(err.Error(), "could not find config.m4 under the extracted source of `phpredis/phpredis`") {
		t.Fatalf("got %v", err)
	}
}

func TestLocateSourceDirBuildPath(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "top", "sub-6.1.0", "config.m4"), "AC_INIT")
	bp := "sub-{version}"
	pkg := testPkg()
	pkg.Metadata.BuildPath = &bp
	got, err := locateSourceDir(root, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "sub-6.1.0" {
		t.Fatalf("got %q", got)
	}
}

func TestLocateSourceDirBuildPathEscape(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	// root has a single subdir "top" so top != root.
	mkfile(t, filepath.Join(root, "top", "config.m4"), "AC_INIT")
	// escape target exists as a sibling of root, reachable from top via ../../.
	mkfile(t, filepath.Join(base, "secret", "config.m4"), "AC_INIT")
	bp := "../../secret"
	pkg := testPkg()
	pkg.Metadata.BuildPath = &bp
	_, err := locateSourceDir(root, pkg)
	if err == nil || !strings.Contains(err.Error(), "escapes the extracted source tree") {
		t.Fatalf("got %v", err)
	}
}

func TestLocateSourceDirBuildPathMissing(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "top", "config.m4"), "AC_INIT")
	bp := "nonexistent"
	pkg := testPkg()
	pkg.Metadata.BuildPath = &bp
	_, err := locateSourceDir(root, pkg)
	if err == nil || !strings.Contains(err.Error(), "resolving build-path `nonexistent`") {
		t.Fatalf("got %v", err)
	}
}

func TestLocateSharedObjectExact(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "redis.so"), "so")
	got, err := locateSharedObject(root, "redis")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "redis.so" {
		t.Fatalf("got %q", got)
	}
}

func TestLocateSharedObjectFallbackSingle(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "nested", "weird.so"), "so")
	got, err := locateSharedObject(root, "redis")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "weird.so" {
		t.Fatalf("got %q", got)
	}
}

func TestLocateSharedObjectNone(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "readme.txt"), "hi")
	_, err := locateSharedObject(root, "redis")
	if err == nil || !strings.Contains(err.Error(), "no `.so` found in the pre-packaged binary for `redis`") {
		t.Fatalf("got %v", err)
	}
}

func TestLocateSharedObjectMultiple(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "a.so"), "so")
	mkfile(t, filepath.Join(root, "b.so"), "so")
	_, err := locateSharedObject(root, "redis")
	if err == nil || !strings.Contains(err.Error(), "expected exactly one `.so` in the pre-packaged binary for `redis`, found 2") {
		t.Fatalf("got %v", err)
	}
}

func TestLocateWindowsDllExact(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "php_redis.dll"), "dll")
	got, err := locateWindowsDll(root, "redis")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "php_redis.dll" {
		t.Fatalf("got %q", got)
	}
}

func TestLocateWindowsDllFallbackIgnoresDeps(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "libssl.dll"), "dep")
	mkfile(t, filepath.Join(root, "php_something.dll"), "dll")
	got, err := locateWindowsDll(root, "redis")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "php_something.dll" {
		t.Fatalf("got %q", got)
	}
}

func TestLocateWindowsDllNone(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "libssl.dll"), "dep")
	_, err := locateWindowsDll(root, "redis")
	if err == nil || !strings.Contains(err.Error(), "no `php_redis.dll` found in the Windows package") {
		t.Fatalf("got %v", err)
	}
}

func TestLocateWindowsDllMultiple(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "php_a.dll"), "dll")
	mkfile(t, filepath.Join(root, "php_b.dll"), "dll")
	_, err := locateWindowsDll(root, "redis")
	if err == nil || !strings.Contains(err.Error(), "found multiple `php_*.dll` files in the Windows package") {
		t.Fatalf("got %v", err)
	}
}

func TestDistTypeFromURL(t *testing.T) {
	if distTypeFromURL("https://x/foo.ZIP") != "zip" {
		t.Fatal("zip")
	}
	if distTypeFromURL("https://x/foo.tar.gz") != "tar" {
		t.Fatal("tar")
	}
	if distTypeFromURL("https://x/foo.tgz") != "tar" {
		t.Fatal("tgz")
	}
}

func TestDownloadedPackageAccessors(t *testing.T) {
	root := t.TempDir()
	d := &DownloadedPackage{Artifact: Artifact{Kind: ArtifactSource, Path: "/s"}, root: root}
	if p, ok := d.SourcePath(); !ok || p != "/s" {
		t.Fatal("source")
	}
	if _, ok := d.BinaryPath(); ok {
		t.Fatal("binary should be false")
	}
	d.Keep()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if !isDir(root) {
		t.Fatal("Keep should prevent removal")
	}

	root2 := t.TempDir()
	sub := filepath.Join(root2, "x")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	d2 := &DownloadedPackage{Artifact: Artifact{Kind: ArtifactBinary, Path: "/b"}, root: root2}
	if err := d2.Close(); err != nil {
		t.Fatal(err)
	}
	if isDir(root2) {
		t.Fatal("Close should remove root")
	}
	_ = resolver.PhpModule
}
