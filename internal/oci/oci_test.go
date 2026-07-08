package oci

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

type entry struct {
	name string
	data []byte
}

func makeLayer(t *testing.T, entries []entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{
			Name:   e.name,
			Mode:   0o644,
			Size:   int64(len(e.data)),
			Format: tar.FormatGNU,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(e.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractsNamedSoFromLayer(t *testing.T) {
	layer := makeLayer(t, []entry{
		{"redis.so", []byte("ELF-bytes")},
		{"README", []byte("x")},
	})
	so, err := extractSo(layer, "redis.so")
	if err != nil {
		t.Fatal(err)
	}
	if string(so) != "ELF-bytes" {
		t.Errorf("so = %q", so)
	}
}

func TestExtractsNestedSo(t *testing.T) {
	layer := makeLayer(t, []entry{{"modules/redis.so", []byte("ELF")}})
	so, err := extractSo(layer, "redis.so")
	if err != nil {
		t.Fatal(err)
	}
	if string(so) != "ELF" {
		t.Errorf("so = %q", so)
	}
}

func TestMissingSoErrors(t *testing.T) {
	layer := makeLayer(t, []entry{{"other.so", []byte("x")}})
	if _, err := extractSo(layer, "redis.so"); err == nil {
		t.Fatal("expected error")
	}
}

func TestVerifySoChecksChecksum(t *testing.T) {
	data := []byte("the so bytes")
	sum := sha256.Sum256(data)
	good := hex.EncodeToString(sum[:])
	if err := verifySo(data, good); err != nil {
		t.Errorf("expected ok, got %v", err)
	}
	if err := verifySo(data, "deadbeef"); err == nil {
		t.Error("expected mismatch error")
	}
	if err := verifySo(data, ""); err != nil {
		t.Errorf("empty should skip, got %v", err)
	}
}

func mustJSONMap(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestReadsCellFromDescriptorAnnotation(t *testing.T) {
	// Test legacy sh.rpie.cell
	descLegacy := mustJSONMap(t, `{
		"digest": "sha256:aaa",
		"annotations": {"sh.rpie.cell": "redis/6.3.0/php8.4/debian@12/x86_64/nts/nodebug/cfg-00000000"}
	}`)
	cell, ok := descriptorCell(descLegacy)
	if !ok || cell != "redis/6.3.0/php8.4/debian@12/x86_64/nts/nodebug/cfg-00000000" {
		t.Errorf("legacy descriptorCell = %q, %v", cell, ok)
	}

	// Test new sh.go-pie.cell
	descNew := mustJSONMap(t, `{
		"digest": "sha256:aaa",
		"annotations": {"sh.go-pie.cell": "redis/6.3.0/php8.4/debian@12/x86_64/nts/nodebug/cfg-00000000"}
	}`)
	cell, ok = descriptorCell(descNew)
	if !ok || cell != "redis/6.3.0/php8.4/debian@12/x86_64/nts/nodebug/cfg-00000000" {
		t.Errorf("new descriptorCell = %q, %v", cell, ok)
	}

	bare := mustJSONMap(t, `{"digest": "sha256:bbb"}`)
	if _, ok := descriptorCell(bare); ok {
		t.Error("bare descriptor should have no cell")
	}
}

func TestReadsCellFromManifestAnnotation(t *testing.T) {
	// Test legacy sh.rpie.cell
	manifestLegacy := mustJSONMap(t, `{
		"config": {"digest": "sha256:c"},
		"layers": [{"digest": "sha256:l"}],
		"annotations": {"sh.rpie.cell": "amqp/v2.2.0/php8.4/debian@13/aarch64/nts/nodebug/cfg-00000000"}
	}`)
	cell, ok := manifestCell(manifestLegacy)
	if !ok || cell != "amqp/v2.2.0/php8.4/debian@13/aarch64/nts/nodebug/cfg-00000000" {
		t.Errorf("legacy manifestCell = %q, %v", cell, ok)
	}

	// Test new sh.go-pie.cell
	manifestNew := mustJSONMap(t, `{
		"config": {"digest": "sha256:c"},
		"layers": [{"digest": "sha256:l"}],
		"annotations": {"sh.go-pie.cell": "amqp/v2.2.0/php8.4/debian@13/aarch64/nts/nodebug/cfg-00000000"}
	}`)
	cell, ok = manifestCell(manifestNew)
	if !ok || cell != "amqp/v2.2.0/php8.4/debian@13/aarch64/nts/nodebug/cfg-00000000" {
		t.Errorf("new manifestCell = %q, %v", cell, ok)
	}

	if _, ok := manifestCell(mustJSONMap(t, `{}`)); ok {
		t.Error("empty manifest should have no cell")
	}
}
