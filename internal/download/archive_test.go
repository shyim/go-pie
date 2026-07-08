package download

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
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
	w.Write([]byte("AC_INIT\n"))
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
