package download

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func buildTarGz(t *testing.T, entries func(tw *tar.Writer)) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	entries(tw)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeTarFile(t *testing.T, tw *tar.Writer, name string, mode int64, body string) {
	t.Helper()
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
}

func TestExtractTarGzRestoresModeBits(t *testing.T) {
	data := buildTarGz(t, func(tw *tar.Writer) {
		writeTarFile(t, tw, "ext/config.m4", 0o644, "AC_INIT\n")
		writeTarFile(t, tw, "ext/build.sh", 0o755, "#!/bin/sh\n")
	})
	dest := t.TempDir()
	if err := extractArchive(data, "tar", dest); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dest, "ext", "config.m4"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o644 {
		t.Fatalf("config.m4 perm = %o", info.Mode().Perm())
	}
	sh, err := os.Stat(filepath.Join(dest, "ext", "build.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && sh.Mode().Perm() != 0o755 {
		t.Fatalf("build.sh perm = %o", sh.Mode().Perm())
	}
}

func TestExtractTarGzRejectsTraversal(t *testing.T) {
	data := buildTarGz(t, func(tw *tar.Writer) {
		writeTarFile(t, tw, "../escape.txt", 0o644, "nope")
	})
	dest := t.TempDir()
	if err := extractArchive(data, "tar", dest); err == nil {
		t.Fatal("expected traversal rejection")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "escape.txt")); err == nil {
		t.Fatal("traversal file was written")
	}
}

func TestExtractTarGzSymlinkInside(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	data := buildTarGz(t, func(tw *tar.Writer) {
		writeTarFile(t, tw, "dir/real.txt", 0o644, "hi")
		if err := tw.WriteHeader(&tar.Header{Name: "dir/link.txt", Linkname: "real.txt", Typeflag: tar.TypeSymlink}); err != nil {
			t.Fatal(err)
		}
	})
	dest := t.TempDir()
	if err := extractArchive(data, "tar", dest); err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(filepath.Join(dest, "dir", "link.txt"))
	if err != nil || target != "real.txt" {
		t.Fatalf("readlink got (%q, %v)", target, err)
	}
}

func TestExtractTarGzRejectsEscapingSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	data := buildTarGz(t, func(tw *tar.Writer) {
		if err := tw.WriteHeader(&tar.Header{Name: "evil", Linkname: "../../etc/passwd", Typeflag: tar.TypeSymlink}); err != nil {
			t.Fatal(err)
		}
	})
	dest := t.TempDir()
	if err := extractArchive(data, "tar", dest); err == nil {
		t.Fatal("expected escaping symlink rejection")
	}
}

func TestExtractZipRoundtrip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("top/config.m4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("AC_INIT\n")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := extractArchive(buf.Bytes(), "zip", dest); err != nil {
		t.Fatal(err)
	}
	if !fileExists(filepath.Join(dest, "top", "config.m4")) {
		t.Fatal("config.m4 not extracted")
	}
}

func TestExtractUnsupportedType(t *testing.T) {
	if err := extractArchive([]byte("x"), "rar", t.TempDir()); err == nil ||
		err.Error() != "unsupported dist type `rar`" {
		t.Fatalf("got %v", err)
	}
}

func TestConsumeArchiveBudget(t *testing.T) {
	remaining := int64(10)
	size, err := consumeArchiveBudget(6, &remaining)
	if err != nil {
		t.Fatal(err)
	}
	if size != 6 || remaining != 4 {
		t.Fatalf("size = %d, remaining = %d", size, remaining)
	}

	if _, err := consumeArchiveBudget(5, &remaining); err == nil {
		t.Fatal("expected extraction budget rejection")
	}
	if remaining != 4 {
		t.Fatalf("failed extraction changed remaining budget to %d", remaining)
	}
}

func TestReadLimited(t *testing.T) {
	got, err := readLimited(strings.NewReader("1234"), 4)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "1234" {
		t.Fatalf("body = %q", got)
	}

	if _, err := readLimited(strings.NewReader("12345"), 4); err == nil {
		t.Fatal("expected response size rejection")
	}
}

// A two-hop symlink chain that stays lexically inside dest at every step, but
// resolves outside it on disk. Later entries must not write through it.
func TestExtractTarGzRejectsWriteThroughSymlinkedDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	dest := t.TempDir()
	// "sub" is a real dir; "sub/up" points to dest's parent via a relative
	// path that insideDest cannot see through once "sub" is followed.
	data := buildTarGz(t, func(tw *tar.Writer) {
		if err := tw.WriteHeader(&tar.Header{
			Name: "sub/", Mode: 0o755, Typeflag: tar.TypeDir,
		}); err != nil {
			t.Fatal(err)
		}
		if err := tw.WriteHeader(&tar.Header{
			Name: "sub/up", Linkname: "..", Typeflag: tar.TypeSymlink,
		}); err != nil {
			t.Fatal(err)
		}
		writeTarFile(t, tw, "sub/up/pwned", 0o644, "owned")
	})

	err := extractArchive(data, "tar", dest)
	escaped := filepath.Join(dest, "pwned")
	if _, statErr := os.Lstat(escaped); statErr == nil {
		t.Fatalf("entry wrote through the symlink chain (err=%v)", err)
	}
}

// A file entry whose leaf path is an existing symlink must not be written
// through: O_CREATE|O_TRUNC follows the link and truncates its destination.
func TestExtractTarGzDoesNotWriteThroughLeafSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	dest := t.TempDir()
	victim := filepath.Join(dest, "victim.txt")
	if err := os.WriteFile(victim, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	data := buildTarGz(t, func(tw *tar.Writer) {
		// "link" -> victim.txt: lexically inside dest, so writeSymlink allows it.
		if err := tw.WriteHeader(&tar.Header{
			Name: "link", Linkname: "victim.txt", Typeflag: tar.TypeSymlink,
		}); err != nil {
			t.Fatal(err)
		}
		writeTarFile(t, tw, "link", 0o644, "OVERWRITTEN")
	})

	err := extractArchive(data, "tar", dest)
	got, readErr := os.ReadFile(victim)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "original" {
		t.Fatalf("write followed the leaf symlink and clobbered the target (err=%v)", err)
	}
}
