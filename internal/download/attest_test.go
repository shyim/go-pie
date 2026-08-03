//go:build !noattest

package download

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// TestNativeAttestationVerifiesRealArtifact is opt-in (network + Sigstore TUF).
// Run with GPIE_ATTEST_NETWORK_TEST=1.
func TestNativeAttestationVerifiesRealArtifact(t *testing.T) {
	if os.Getenv("GPIE_ATTEST_NETWORK_TEST") == "" {
		t.Skip("set GPIE_ATTEST_NETWORK_TEST=1 to run the network attestation test")
	}
	url := "https://github.com/cli/cli/releases/download/v2.95.0/gh_2.95.0_linux_amd64.tar.gz"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	tmp := filepath.Join(t.TempDir(), "asset")
	f, err := os.Create(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	ok, err := VerifyGithubAttestation(t.Context(), tmp, "cli/cli")
	if err != nil {
		t.Fatalf("verify error: %v", err)
	}
	if !ok {
		t.Fatal("expected attestation to verify")
	}
}

func TestGitHubAPIURLRequiresExactHTTPSHost(t *testing.T) {
	if !isGitHubAPIURL("https://api.github.com/repos/owner/repo/attestations/x") {
		t.Fatal("official GitHub API URL should be recognized")
	}
	for _, rawURL := range []string{
		"http://api.github.com/repos/owner/repo",
		"https://api.github.com.evil.example/repos/owner/repo",
		"https://evil.example/api.github.com",
	} {
		if isGitHubAPIURL(rawURL) {
			t.Fatalf("untrusted URL recognized as GitHub API: %s", rawURL)
		}
	}
}

func TestBundleURLAllowlist(t *testing.T) {
	allowed := []string{
		"https://api.github.com/repos/o/r/attestations/x",
		"https://github.com/o/r",
		"https://objects.githubusercontent.com/blob",
	}
	for _, u := range allowed {
		if !isAllowedBundleURL(u) {
			t.Errorf("expected %q to be allowed", u)
		}
	}
	denied := []string{
		"https://evil.example.com/bundle",
		"http://api.github.com/x",          // plaintext
		"https://api.github.com.evil.test", // suffix confusion
		"file:///etc/passwd",
		"",
	}
	for _, u := range denied {
		if isAllowedBundleURL(u) {
			t.Errorf("expected %q to be refused", u)
		}
	}
}
