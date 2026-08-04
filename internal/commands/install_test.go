package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/shyim/go-pie/internal/download"
	"github.com/shyim/go-pie/internal/oci"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// `--prefer-prebuilt` used to require --oci-registry or GPIE_OCI_REGISTRY as
// well; with neither it resolved to nil and the flag became a silent no-op that
// looked exactly like a cache which never hits.
func TestPrebuiltRegistryDefaultsToTheOfficialCache(t *testing.T) {
	// Setenv first so the original value is restored after the test, then
	// remove it: "unset" and "set but empty" mean different things here.
	t.Setenv("GPIE_OCI_REGISTRY", "")
	require.NoError(t, os.Unsetenv("GPIE_OCI_REGISTRY"))

	args := &InstallArgs{PreferPrebuilt: true}
	got := args.prebuiltRegistry()
	if assert.NotNil(t, got, "--prefer-prebuilt must resolve a registry without extra configuration") {
		assert.Equal(t, DefaultOciRegistry, *got)
	}

	// Without the flag there is no lookup at all, default or otherwise.
	assert.Nil(t, (&InstallArgs{}).prebuiltRegistry())
}

func TestPrebuiltRegistryPrecedence(t *testing.T) {
	explicit := "registry.example/ns"
	t.Setenv("GPIE_OCI_REGISTRY", "env.example/ns")

	// --oci-registry beats the environment.
	got := (&InstallArgs{PreferPrebuilt: true, OciRegistry: &explicit}).prebuiltRegistry()
	if assert.NotNil(t, got) {
		assert.Equal(t, explicit, *got)
	}

	// The environment beats the built-in default.
	got = (&InstallArgs{PreferPrebuilt: true}).prebuiltRegistry()
	if assert.NotNil(t, got) {
		assert.Equal(t, "env.example/ns", *got)
	}
}

// An empty GPIE_OCI_REGISTRY is an explicit opt-out, distinct from leaving it
// unset -- otherwise there would be no way to turn the lookup off.
func TestPrebuiltRegistryEmptyEnvOptsOut(t *testing.T) {
	t.Setenv("GPIE_OCI_REGISTRY", "")
	assert.Nil(t, (&InstallArgs{PreferPrebuilt: true}).prebuiltRegistry())
}
