package oci

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

// Prebuilt is a resolved prebuilt artifact: its manifest plus the raw .so bytes.
type Prebuilt struct {
	Manifest ExtManifest
	SoBytes  []byte
}

// ResolvePrebuilt looks up a prebuilt artifact for cell in the registry.
//
// Returns (nil, nil) on any miss (tag absent, no matching descriptor) so the
// caller can fall back to building. Returns an error only on hard failures
// during a hit (corrupt manifest, checksum mismatch, network error mid-fetch).
func ResolvePrebuilt(r *Registry, c *Cell) (*Prebuilt, error) {
	index, err := r.GetIndex(c.Extension, c.Version)
	if err != nil {
		return nil, err
	}
	if index == nil {
		return nil, nil
	}

	wanted := c.ID()
	descriptors, ok := index["manifests"].([]any)
	if !ok {
		return nil, nil
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
			return fetchAndBuild(r, c, digest)
		}

		// Otherwise inspect the child manifest's own annotations.
		imageManifest, err := r.GetManifest(c.Extension, digest)
		if err != nil {
			return nil, err
		}
		if cell, ok := manifestCell(imageManifest); ok && cell == wanted {
			return buildFromManifest(r, c, imageManifest)
		}
	}

	return nil, nil
}

func fetchAndBuild(r *Registry, c *Cell, digest string) (*Prebuilt, error) {
	imageManifest, err := r.GetManifest(c.Extension, digest)
	if err != nil {
		return nil, err
	}
	return buildFromManifest(r, c, imageManifest)
}

func buildFromManifest(r *Registry, c *Cell, imageManifest map[string]any) (*Prebuilt, error) {
	configDigest, ok := digStr(imageManifest, "config", "digest")
	if !ok {
		return nil, fmt.Errorf("image manifest missing config digest")
	}
	configBytes, err := r.GetBlob(c.Extension, configDigest)
	if err != nil {
		return nil, err
	}
	manifest, err := ParseExtManifest(configBytes)
	if err != nil {
		return nil, fmt.Errorf("parsing rpie extension manifest: %w", err)
	}

	if manifest.Cell != c.ID() {
		return nil, fmt.Errorf(
			"registry returned manifest for cell `%s` but we requested `%s`",
			manifest.Cell, c.ID(),
		)
	}

	layerDigest, ok := firstLayerDigest(imageManifest)
	if !ok {
		return nil, fmt.Errorf("image manifest has no layer")
	}
	layerBytes, err := r.GetBlob(c.Extension, layerDigest)
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

	return &Prebuilt{Manifest: *manifest, SoBytes: soBytes}, nil
}

// descriptorCell returns the sh.go-pie.cell (or legacy sh.rpie.cell) annotation on an index descriptor.
func descriptorCell(descriptor map[string]any) (string, bool) {
	return annotationCell(descriptor)
}

// manifestCell returns the sh.go-pie.cell (or legacy sh.rpie.cell) annotation on an image manifest.
func manifestCell(imageManifest map[string]any) (string, bool) {
	return annotationCell(imageManifest)
}

func annotationCell(v map[string]any) (string, bool) {
	annotations, ok := v["annotations"].(map[string]any)
	if !ok {
		return "", false
	}
	if cell, ok := annotations["sh.go-pie.cell"].(string); ok {
		return cell, true
	}
	cell, ok := annotations["sh.rpie.cell"].(string)
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
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading layer tar: %w", err)
		}
		path := hdr.Name
		if path == soFile || strings.HasSuffix(trimDotSlashPrefix(path), soFile) {
			buf, err := io.ReadAll(tr)
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
