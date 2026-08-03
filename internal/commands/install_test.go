package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/shyim/go-pie/internal/download"
	"github.com/shyim/go-pie/internal/oci"
	"github.com/stretchr/testify/assert"
)

func TestDividesCoresAcrossBuilds(t *testing.T) {
	assert.Equal(t, 4, makeJobsPerBuild(16, 4))
	assert.Equal(t, 16, makeJobsPerBuild(16, 1))
	assert.Equal(t, 2, makeJobsPerBuild(8, 3))
}

func TestNeverReturnsZero(t *testing.T) {
	assert.Equal(t, 1, makeJobsPerBuild(2, 8))
	assert.Equal(t, 1, makeJobsPerBuild(1, 4))
	assert.Equal(t, 1, makeJobsPerBuild(0, 0))
}

func TestVerifyPrebuiltPolicyEnforce(t *testing.T) {
	sum := sha256.Sum256([]byte("manifest"))
	prebuilt := &oci.Prebuilt{
		ManifestDigest: "sha256:" + hex.EncodeToString(sum[:]),
		Manifest: oci.ExtManifest{
			SoSha256: "abc123",
		},
	}
	if err := verifyPrebuiltPolicy(t.Context(), prebuilt, download.VerifyEnforce); err != nil {
		t.Fatalf("enforce rejected checksummed prebuilt: %v", err)
	}

	prebuilt.Manifest.SoSha256 = ""
	if err := verifyPrebuiltPolicy(t.Context(), prebuilt, download.VerifyEnforce); err == nil {
		t.Fatal("enforce accepted a prebuilt without an extension checksum")
	}
}

func TestVerifyPrebuiltPolicyAttestRequiresRepository(t *testing.T) {
	prebuilt := &oci.Prebuilt{
		ManifestDigest: "sha256:" + strings.Repeat("0", 64),
	}
	if err := verifyPrebuiltPolicy(t.Context(), prebuilt, download.VerifyAttest); err == nil {
		t.Fatal("attest accepted a prebuilt without an attestation repository")
	}
}

func TestRunInstallRejectsAmbiguousEmitOci(t *testing.T) {
	dir := "/tmp/does-not-matter"
	err := RunInstall(t.Context(), &InstallArgs{
		Packages: []string{"a/one", "b/two"},
		EmitOci:  &dir,
	}, ModeBuildOnly)
	if err == nil || !strings.Contains(err.Error(), "--emit-oci") {
		t.Fatalf("want --emit-oci rejection for multiple packages, got %v", err)
	}
}

func TestRunInstallRejectsNoPackages(t *testing.T) {
	if err := RunInstall(t.Context(), &InstallArgs{}, ModeBuildOnly); err == nil {
		t.Fatal("want error for empty package list")
	}
}
