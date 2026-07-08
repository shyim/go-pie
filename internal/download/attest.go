//go:build !noattest

package download

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/golang/snappy"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

const githubAttestAPI = "https://api.github.com"

var trustedRootOnce = sync.OnceValues(func() (*root.TrustedRoot, error) {
	opts := tuf.DefaultOptions()
	client, err := tuf.New(opts)
	if err != nil {
		return nil, err
	}
	return root.GetTrustedRoot(client)
})

// VerifyGithubAttestation verifies a downloaded asset file against its GitHub
// build attestations. repo is "org/repo".
func VerifyGithubAttestation(assetPath, repo string) (bool, error) {
	owner, repoName, ok := strings.Cut(repo, "/")
	if !ok {
		return false, fmt.Errorf("attestation repo must be org/repo")
	}

	digestHex, digestBytes, err := fileDigest(assetPath)
	if err != nil {
		return false, err
	}

	bundles, err := fetchAttestations(owner, repoName, digestHex)
	if err != nil {
		return false, fmt.Errorf("attestation verification failed for %s: %w", repo, err)
	}
	if len(bundles) == 0 {
		return false, fmt.Errorf("attestation verification failed for %s: No attestations found", repo)
	}

	trusted, err := trustedRootOnce()
	if err != nil {
		return false, fmt.Errorf("attestation verification failed for %s: %v", repo, err)
	}

	verifier, err := verify.NewVerifier(trusted, verify.WithObserverTimestamps(1))
	if err != nil {
		return false, fmt.Errorf("attestation verification failed for %s: %v", repo, err)
	}

	idActions, err := verify.NewShortCertificateIdentity(
		"https://token.actions.githubusercontent.com", "", "",
		"^https://github.com/"+regexp.QuoteMeta(owner+"/"+repoName)+"/")
	if err != nil {
		return false, fmt.Errorf("attestation verification failed for %s: %v", repo, err)
	}
	idReleases, err := verify.NewShortCertificateIdentity(
		"", "^https://", "https://dotcom.releases.github.com", "")
	if err != nil {
		return false, fmt.Errorf("attestation verification failed for %s: %v", repo, err)
	}

	policy := verify.NewPolicy(
		verify.WithArtifactDigest("sha256", digestBytes),
		verify.WithCertificateIdentity(idActions),
		verify.WithCertificateIdentity(idReleases))

	var firstErr error
	for _, raw := range bundles {
		var b bundle.Bundle
		if err := b.UnmarshalJSON(raw); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if _, err := verifier.Verify(&b, policy); err == nil {
			return true, nil
		} else if firstErr == nil {
			firstErr = err
		}
	}

	if firstErr == nil {
		firstErr = fmt.Errorf("No valid attestations found")
	}
	return false, fmt.Errorf("attestation verification failed for %s: %v", repo, firstErr)
}

func fileDigest(path string) (string, []byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", nil, err
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum), sum, nil
}

func fetchAttestations(owner, repo, digestHex string) ([][]byte, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/attestations/sha256:%s?per_page=30",
		githubAttestAPI, owner, repo, digestHex)

	body, status, err := attestGet(url, true)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, fmt.Errorf("No attestations found")
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("API error: GitHub API returned %d: %s", status, string(body))
	}

	var parsed struct {
		Attestations []struct {
			Bundle    json.RawMessage `json:"bundle"`
			BundleURL string          `json:"bundle_url"`
		} `json:"attestations"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}

	var out [][]byte
	for _, a := range parsed.Attestations {
		if len(a.Bundle) > 0 && string(a.Bundle) != "null" {
			out = append(out, a.Bundle)
			continue
		}
		if a.BundleURL != "" {
			raw, err := fetchBundleURL(a.BundleURL)
			if err != nil {
				continue
			}
			out = append(out, raw)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("No attestations found")
	}
	return out, nil
}

func fetchBundleURL(url string) ([]byte, error) {
	onAPI := strings.HasPrefix(url, githubAttestAPI)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "rpie-attestation")
	if onAPI {
		req.Header.Set("x-github-api-version", "2022-11-28")
		if token := githubToken(); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("bundle download returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.Header.Get("Content-Type") == "application/x-snappy" {
		decoded, err := snappy.Decode(nil, body)
		if err != nil {
			return nil, err
		}
		return decoded, nil
	}
	return body, nil
}

func attestGet(url string, onAPI bool) ([]byte, int, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "rpie-attestation")
	if onAPI {
		req.Header.Set("x-github-api-version", "2022-11-28")
		if token := githubToken(); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}
