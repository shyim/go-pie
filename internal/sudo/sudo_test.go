package sudo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathNeedsSudoWritableDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root: path_needs_sudo always returns false")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "new.so")
	if PathNeedsSudo(target) {
		t.Fatalf("PathNeedsSudo(%q) = true, want false (parent is writable, path absent)", target)
	}
}

func TestPathNeedsSudoExistingWritableFile(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root: path_needs_sudo always returns false")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "existing.ini")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if PathNeedsSudo(target) {
		t.Fatalf("PathNeedsSudo(%q) = true, want false for a writable existing file", target)
	}
}

func TestPathNeedsSudoRootWritableTarget(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("not root")
	}
	dir := t.TempDir()
	if PathNeedsSudo(filepath.Join(dir, "x")) {
		t.Fatal("as root PathNeedsSudo must always be false")
	}
}

func TestPathNeedsSudoUnwritableParent(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root: writability checks are bypassed")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "ro")
	if err := os.Mkdir(sub, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0o700) })
	target := filepath.Join(sub, "new.so")
	if !PathNeedsSudo(target) {
		t.Fatalf("PathNeedsSudo(%q) = false, want true (parent is read-only)", target)
	}
}

func TestParentDir(t *testing.T) {
	cases := map[string]string{
		"":     "",
		"/":    "",
		"///":  "",
		"foo":  "",
		"/foo": "/",
	}
	for in, want := range cases {
		if got := parentDir(in); got != want {
			t.Errorf("parentDir(%q) = %q, want %q", in, got, want)
		}
	}
	if got := parentDir("/a/b/c"); got != filepath.Dir("/a/b/c") {
		t.Errorf("parentDir(/a/b/c) = %q, want %q", got, filepath.Dir("/a/b/c"))
	}
}

func TestIsAvailableDoesNotPanic(t *testing.T) {
	_ = IsAvailable()
}
