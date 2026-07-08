//go:build !noattest

package download

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// TestNativeAttestationVerifiesRealArtifact is opt-in (network + Sigstore TUF).
// Run with RPIE_ATTEST_NETWORK_TEST=1.
func TestNativeAttestationVerifiesRealArtifact(t *testing.T) {
	if os.Getenv("RPIE_ATTEST_NETWORK_TEST") == "" {
		t.Skip("set RPIE_ATTEST_NETWORK_TEST=1 to run the network attestation test")
	}
	url := "https://github.com/cli/cli/releases/download/v2.95.0/gh_2.95.0_linux_amd64.tar.gz"
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	tmp := filepath.Join(t.TempDir(), "asset")
	f, err := os.Create(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		t.Fatal(err)
	}
	f.Close()

	ok, err := VerifyGithubAttestation(tmp, "cli/cli")
	if err != nil {
		t.Fatalf("verify error: %v", err)
	}
	if !ok {
		t.Fatal("expected attestation to verify")
	}
}
