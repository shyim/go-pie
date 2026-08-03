package oci

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

// maxSoBytes bounds a single extracted .so so that a malformed or hostile
// layer cannot exhaust memory during extraction.
const maxSoBytes int64 = 512 << 20

// ErrPrebuiltNotFound indicates that the registry has no compatible prebuilt
// artifact. Callers should fall back to a source build.
var ErrPrebuiltNotFound = errors.New("prebuilt artifact not found")

// readLimited reads at most limit bytes from r, failing if more are available.
func readLimited(r io.Reader, limit int64) ([]byte, error) {
	buf, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(buf)) > limit {
		return nil, fmt.Errorf("layer entry exceeds the %d-byte safety limit", limit)
	}
	return buf, nil
}

// Prebuilt is a resolved prebuilt artifact: its manifest plus the raw .so bytes.
type Prebuilt struct {
	Manifest       ExtManifest
	ManifestDigest string
	SoBytes        []byte
}

// ResolvePrebuilt looks up a prebuilt artifact for cell in the registry.
//
// Returns ErrPrebuiltNotFound on any miss (tag absent, no matching descriptor)
// so the caller can fall back to building. Other errors indicate a hard failure
// during a hit (corrupt manifest, checksum mismatch, network error mid-fetch).
func ResolvePrebuilt(ctx context.Context, r *Registry, c *Cell) (*Prebuilt, error) {
	index, err := r.GetIndex(ctx, c.Extension, c.Version)
	if err != nil {
		if errors.Is(err, ErrPrebuiltNotFound) {
			return nil, ErrPrebuiltNotFound
		}
		return nil, err
	}

	wanted := c.ID()
	descriptors, ok := index["manifests"].([]any)
	if !ok {
		return nil, ErrPrebuiltNotFound
	}

	for _, d := range descriptors {
		descriptor, ok := d.(map[string]any)
		if !ok {
			continue
		}
		digest, ok := descriptor["digest"].(string)
		if !ok {
			continue
		}

		// Fast path: descriptor already carries the cell annotation.
		if cell, ok := descriptorCell(descriptor); ok && cell == wanted {
			return fetchAndBuild(ctx, r, c, digest)
		}

		// Otherwise inspect the child manifest's own annotations.
		imageManifest, err := r.GetManifest(ctx, c.Extension, digest)
		if err != nil {
			return nil, err
		}
		if cell, ok := manifestCell(imageManifest); ok && cell == wanted {
			return buildFromManifest(ctx, r, c, imageManifest, digest)
		}
	}

	return nil, ErrPrebuiltNotFound
}

func fetchAndBuild(ctx context.Context, r *Registry, c *Cell, digest string) (*Prebuilt, error) {
	imageManifest, err := r.GetManifest(ctx, c.Extension, digest)
	if err != nil {
		return nil, err
	}
	return buildFromManifest(ctx, r, c, imageManifest, digest)
}

func buildFromManifest(ctx context.Context, r *Registry, c *Cell, imageManifest map[string]any, manifestDigest string) (*Prebuilt, error) {
	configDigest, ok := digStr(imageManifest, "config", "digest")
	if !ok {
		return nil, errors.New("image manifest missing config digest")
	}
	configBytes, err := r.GetBlob(ctx, c.Extension, configDigest)
	if err != nil {
		return nil, err
	}
	manifest, err := ParseExtManifest(configBytes)
	if err != nil {
		return nil, fmt.Errorf("parsing gpie extension manifest: %w", err)
	}

	if manifest.Cell != c.ID() {
		return nil, fmt.Errorf(
			"registry returned manifest for cell `%s` but we requested `%s`",
			manifest.Cell, c.ID(),
		)
	}

	layerDigest, ok := firstLayerDigest(imageManifest)
	if !ok {
		return nil, errors.New("image manifest has no layer")
	}
	layerBytes, err := r.GetBlob(ctx, c.Extension, layerDigest)
	if err != nil {
		return nil, err
	}
	soBytes, err := extractSo(layerBytes, manifest.SoFile)
	if err != nil {
		return nil, err
	}
	if err := verifySo(soBytes, manifest.SoSha256); err != nil {
		return nil, err
	}

	return &Prebuilt{
		Manifest:       *manifest,
		ManifestDigest: manifestDigest,
		SoBytes:        soBytes,
	}, nil
}

// descriptorCell returns the sh.gpie.cell annotation on an index descriptor.
func descriptorCell(descriptor map[string]any) (string, bool) {
	return annotationCell(descriptor)
}

// manifestCell returns the sh.gpie.cell annotation on an image manifest.
func manifestCell(imageManifest map[string]any) (string, bool) {
	return annotationCell(imageManifest)
}

func annotationCell(v map[string]any) (string, bool) {
	annotations, ok := v["annotations"].(map[string]any)
	if !ok {
		return "", false
	}
	cell, ok := annotations["sh.gpie.cell"].(string)
	return cell, ok
}

func firstLayerDigest(imageManifest map[string]any) (string, bool) {
	layers, ok := imageManifest["layers"].([]any)
	if !ok || len(layers) == 0 {
		return "", false
	}
	first, ok := layers[0].(map[string]any)
	if !ok {
		return "", false
	}
	digest, ok := first["digest"].(string)
	return digest, ok
}

// digStr walks nested map[string]any keys and returns the terminal string value.
func digStr(v map[string]any, keys ...string) (string, bool) {
	cur := v
	for i, k := range keys {
		if i == len(keys)-1 {
			s, ok := cur[k].(string)
			return s, ok
		}
		next, ok := cur[k].(map[string]any)
		if !ok {
			return "", false
		}
		cur = next
	}
	return "", false
}

// extractSo extracts soFile from a gzip'd tar layer.
func extractSo(layerBytes []byte, soFile string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(layerBytes))
	if err != nil {
		return nil, fmt.Errorf("reading layer tar: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading layer tar: %w", err)
		}
		// Match on a full path component so that a request for "redis.so"
		// cannot be satisfied by an entry named "malicious_redis.so".
		trimmed := trimDotSlashPrefix(hdr.Name)
		if trimmed == soFile || strings.HasSuffix(trimmed, "/"+soFile) {
			buf, err := readLimited(tr, maxSoBytes)
			if err != nil {
				return nil, err
			}
			return buf, nil
		}
	}
	return nil, fmt.Errorf("layer did not contain `%s`", soFile)
}

// trimDotSlashPrefix strips repeated leading "./" prefixes, mirroring Rust
// str::trim_start_matches("./").
func trimDotSlashPrefix(s string) string {
	for strings.HasPrefix(s, "./") {
		s = s[2:]
	}
	return s
}

func verifySo(b []byte, expectedSha256 string) error {
	// An absent checksum is not verified here; whether that is acceptable is a
	// policy decision made by verifyPrebuiltPolicy, which rejects empty
	// checksums under VerifyEnforce and otherwise relies on the attested
	// manifest digest covering these bytes.
	if expectedSha256 == "" {
		return nil
	}
	sum := sha256.Sum256(b)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, expectedSha256) {
		return fmt.Errorf("prebuilt .so checksum mismatch: expected %s, got %s", expectedSha256, got)
	}
	return nil
}
