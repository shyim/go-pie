package download

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// extractArchive extracts bytes of the given dist type into dest.
func extractArchive(b []byte, distType string, dest string) error {
	switch distType {
	case "zip":
		return extractZip(b, dest)
	case "tar":
		return extractTarGz(b, dest)
	default:
		return fmt.Errorf("unsupported dist type `%s`", distType)
	}
}

// safeJoin joins dest and name, rejecting entries whose cleaned join escapes dest.
func safeJoin(dest, name string) (string, error) {
	target := filepath.Join(dest, name)
	cleanDest := filepath.Clean(dest)
	rel, err := filepath.Rel(cleanDest, target)
	if err != nil {
		return "", fmt.Errorf("invalid path `%s`", name)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid path `%s`", name)
	}
	return target, nil
}

// insideDest reports whether an absolute or resolved link target stays inside dest.
func insideDest(dest, target string) bool {
	cleanDest := filepath.Clean(dest)
	rel, err := filepath.Rel(cleanDest, filepath.Clean(target))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func extractZip(b []byte, dest string) error {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return fmt.Errorf("opening zip archive: %w", err)
	}
	for _, f := range zr.File {
		if err := extractZipEntry(f, dest); err != nil {
			return fmt.Errorf("extracting zip archive: %w", err)
		}
	}
	return nil
}

func extractZipEntry(f *zip.File, dest string) error {
	target, err := safeJoin(dest, f.Name)
	if err != nil {
		return err
	}
	mode := f.Mode()

	if mode&os.ModeSymlink != 0 {
		rc, err := f.Open()
		if err != nil {
			return err
		}
		linkBytes, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return err
		}
		return writeSymlink(string(linkBytes), target, dest)
	}

	if f.FileInfo().IsDir() {
		return os.MkdirAll(target, 0o755)
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	perm := mode.Perm()
	if perm == 0 {
		perm = 0o644
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func extractTarGz(b []byte, dest string) error {
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("extracting tar.gz archive: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("extracting tar.gz archive: %w", err)
		}
		if err := extractTarEntry(tr, hdr, dest); err != nil {
			return fmt.Errorf("extracting tar.gz archive: %w", err)
		}
	}
	return nil
}

func extractTarEntry(tr *tar.Reader, hdr *tar.Header, dest string) error {
	name := strings.TrimPrefix(hdr.Name, "/")
	target, err := safeJoin(dest, name)
	if err != nil {
		return err
	}

	switch hdr.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(target, os.FileMode(hdr.Mode).Perm())
	case tar.TypeSymlink:
		return writeSymlink(hdr.Linkname, target, dest)
	case tar.TypeLink:
		linkTarget, err := safeJoin(dest, strings.TrimPrefix(hdr.Linkname, "/"))
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.Link(linkTarget, target)
	case tar.TypeReg, tar.TypeRegA:
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		perm := os.FileMode(hdr.Mode).Perm()
		if perm == 0 {
			perm = 0o644
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	default:
		return nil
	}
}

// writeSymlink creates a symlink only when its resolved target stays inside dest.
func writeSymlink(linkname, target, dest string) error {
	var resolved string
	if filepath.IsAbs(linkname) {
		resolved = linkname
	} else {
		resolved = filepath.Join(filepath.Dir(target), linkname)
	}
	if !insideDest(dest, resolved) {
		return fmt.Errorf("symlink `%s` escapes the extracted tree", linkname)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.Symlink(linkname, target)
}
