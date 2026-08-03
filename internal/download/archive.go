package download

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxExtractedArchiveBytes int64 = 1 << 30 // 1 GiB

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

// safeJoin joins dest and name, rejecting entries whose cleaned join escapes
// dest. The lexical check is not sufficient on its own: an earlier archive
// entry may have created a symlink to a directory, and a later entry whose
// path traverses that symlink would resolve outside dest at write time. Any
// intermediate component that already exists as a symlink on disk is therefore
// rejected as well.
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
	if err := requireNoSymlinkParents(cleanDest, target); err != nil {
		return "", err
	}
	return target, nil
}

// createFile creates target for writing without following an existing symlink
// at the leaf. O_CREATE|O_TRUNC alone would follow such a link and truncate
// whatever it points at, which an earlier archive entry can aim outside dest.
func createFile(target string, perm os.FileMode) (*os.File, error) {
	if err := removeExistingLeaf(target); err != nil {
		return nil, err
	}
	return os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
}

// removeExistingLeaf deletes target when it exists as anything other than a
// regular file, so a symlink planted by an earlier entry is replaced rather
// than followed.
func removeExistingLeaf(target string) error {
	fi, err := os.Lstat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if fi.Mode().IsRegular() {
		return nil
	}
	if fi.IsDir() {
		return fmt.Errorf("refusing to replace directory `%s` with an archive entry", target)
	}
	return os.Remove(target)
}

// requireNoSymlinkParents walks each path component between dest and target,
// rejecting the entry when an existing component is a symlink. Components that
// do not exist yet are fine; they will be created as real directories.
func requireNoSymlinkParents(cleanDest, target string) error {
	rel, err := filepath.Rel(cleanDest, filepath.Dir(target))
	if err != nil {
		return fmt.Errorf("invalid path `%s`", target)
	}
	if rel == "." {
		return nil
	}
	current := cleanDest
	for part := range strings.SplitSeq(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("invalid path `%s`: traverses symlink `%s`", target, current)
		}
	}
	return nil
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
	remaining := maxExtractedArchiveBytes
	for _, f := range zr.File {
		if err := extractZipEntry(f, dest, &remaining); err != nil {
			return fmt.Errorf("extracting zip archive: %w", err)
		}
	}
	return nil
}

func extractZipEntry(f *zip.File, dest string, remaining *int64) error {
	target, err := safeJoin(dest, f.Name)
	if err != nil {
		return err
	}
	mode := f.Mode()

	if mode&os.ModeSymlink != 0 {
		size, err := consumeZipArchiveBudget(f.UncompressedSize64, remaining)
		if err != nil {
			return err
		}
		if size > 64<<10 {
			return fmt.Errorf("symlink `%s` is too large", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		linkBytes, err := readZipEntry(rc, size)
		_ = rc.Close()
		if err != nil {
			return err
		}
		return writeSymlink(string(linkBytes), target, dest)
	}

	if f.FileInfo().IsDir() {
		return os.MkdirAll(target, 0o755)
	}

	size, err := consumeZipArchiveBudget(f.UncompressedSize64, remaining)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	perm := mode.Perm()
	if perm == 0 {
		perm = 0o644
	}
	out, err := createFile(target, perm)
	if err != nil {
		return err
	}
	if _, err := io.CopyN(out, rc, size); err != nil {
		_ = out.Close()
		return err
	}
	if err := requireZipEOF(rc); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func readZipEntry(rc io.Reader, size int64) ([]byte, error) {
	var buf bytes.Buffer
	if _, err := io.CopyN(&buf, rc, size); err != nil {
		return nil, err
	}
	if err := requireZipEOF(rc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func requireZipEOF(rc io.Reader) error {
	var extra [1]byte
	n, err := rc.Read(extra[:])
	if n != 0 || err != io.EOF {
		if err == nil {
			return errors.New("zip entry contains more data than declared")
		}
		return err
	}
	return nil
}

func extractTarGz(b []byte, dest string) error {
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("extracting tar.gz archive: %w", err)
	}
	// Close validates the gzip CRC/length footer, so a truncated or corrupt
	// payload must not be reported as a successful extraction.
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	remaining := maxExtractedArchiveBytes
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("extracting tar.gz archive: %w", err)
		}
		if err := extractTarEntry(tr, hdr, dest, &remaining); err != nil {
			return fmt.Errorf("extracting tar.gz archive: %w", err)
		}
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("extracting tar.gz archive: %w", err)
	}
	return nil
}

func extractTarEntry(tr *tar.Reader, hdr *tar.Header, dest string, remaining *int64) error {
	name := strings.TrimPrefix(hdr.Name, "/")
	target, err := safeJoin(dest, name)
	if err != nil {
		return err
	}

	switch hdr.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(target, os.FileMode(hdr.Mode&0o777).Perm())
	case tar.TypeSymlink:
		return writeSymlink(hdr.Linkname, target, dest)
	case tar.TypeLink:
		linkTarget, err := safeJoin(dest, strings.TrimPrefix(hdr.Linkname, "/"))
		if err != nil {
			return err
		}
		// Hard-linking a symlink would share an inode that may point outside
		// dest, so require a regular file as the source.
		fi, err := os.Lstat(linkTarget)
		if err != nil {
			return err
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("hard link source `%s` is not a regular file", hdr.Linkname)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := removeExistingLeaf(target); err != nil {
			return err
		}
		return os.Link(linkTarget, target)
	case tar.TypeReg, tar.TypeRegA: //nolint:staticcheck // TypeRegA still appears in legacy archives.
		size, err := consumeArchiveBudget(hdr.Size, remaining)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		perm := os.FileMode(hdr.Mode & 0o777).Perm()
		if perm == 0 {
			perm = 0o644
		}
		out, err := createFile(target, perm)
		if err != nil {
			return err
		}
		if _, err := io.CopyN(out, tr, size); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	default:
		return nil
	}
}

func consumeZipArchiveBudget(size uint64, remaining *int64) (int64, error) {
	if size > uint64(maxExtractedArchiveBytes) {
		return 0, fmt.Errorf("archive expands beyond the %d-byte safety limit", maxExtractedArchiveBytes)
	}
	return consumeArchiveBudget(int64(size), remaining)
}

func consumeArchiveBudget(size int64, remaining *int64) (int64, error) {
	if size < 0 || size > *remaining {
		return 0, fmt.Errorf("archive expands beyond the %d-byte safety limit", maxExtractedArchiveBytes)
	}
	*remaining -= size
	return size, nil
}

// writeSymlink creates a symlink only when its resolved target stays inside
// dest. Absolute link text and any ".." segment are refused outright: a
// lexically-valid ".." can still traverse a symlink planted by an earlier entry
// once the kernel resolves it.
func writeSymlink(linkname, target, dest string) error {
	if linkname == "" || filepath.IsAbs(linkname) {
		return fmt.Errorf("symlink `%s` escapes the extracted tree", linkname)
	}
	for part := range strings.SplitSeq(filepath.ToSlash(linkname), "/") {
		if part == ".." {
			return fmt.Errorf("symlink `%s` escapes the extracted tree", linkname)
		}
	}
	resolved := filepath.Join(filepath.Dir(target), linkname)
	if !insideDest(dest, resolved) {
		return fmt.Errorf("symlink `%s` escapes the extracted tree", linkname)
	}
	if err := removeExistingLeaf(target); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.Symlink(linkname, target)
}
