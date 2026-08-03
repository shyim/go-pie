//go:build !noattest

package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
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

// bundleHTTPClient shares httpClient's timeout but refuses redirects that leave
// the GitHub allowlist, so a hostile Location header cannot redirect a bundle
// fetch to an arbitrary origin.
var bundleHTTPClient = &http.Client{
	Timeout: httpClient.Timeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		if !isAllowedBundleURL(req.URL.String()) {
			return fmt.Errorf("refusing attestation bundle redirect outside GitHub: %s", req.URL)
		}
		return nil
	},
}

var (
	errNoAttestations      = errors.New("no attestations found")
	errNoValidAttestations = errors.New("no valid attestations found")
)

var (
	trustedRootMu    sync.Mutex
	trustedRootValue *root.TrustedRoot
)

// trustedRootOnce caches only a successful TUF bootstrap. sync.OnceValues would
// also memoise a failure, so one transient network or mirror error would make
// every later verification in this process fail until restart.
func trustedRootOnce() (*root.TrustedRoot, error) {
	trustedRootMu.Lock()
	defer trustedRootMu.Unlock()
	if trustedRootValue != nil {
		return trustedRootValue, nil
	}
	opts := tuf.DefaultOptions()
	client, err := tuf.New(opts)
	if err != nil {
		return nil, err
	}
	tr, err := root.GetTrustedRoot(client)
	if err != nil {
		return nil, err
	}
	trustedRootValue = tr
	return trustedRootValue, nil
}

// VerifyGithubAttestation verifies a downloaded asset file against its GitHub
// build attestations. repo is "org/repo".
func VerifyGithubAttestation(ctx context.Context, assetPath, repo string) (bool, error) {
	digestHex, digestBytes, err := fileDigest(assetPath)
	if err != nil {
		return false, err
	}
	return verifyGithubAttestationDigest(ctx, digestHex, digestBytes, repo)
}

// VerifyGithubAttestationDigest verifies a GitHub attestation for an existing
// sha256 digest, such as an OCI manifest digest.
func VerifyGithubAttestationDigest(ctx context.Context, digest, repo string) (bool, error) {
	// The GitHub attestations endpoint only accepts a lowercase hex digest,
	// even though hex.DecodeString happily parses uppercase.
	digestHex := strings.ToLower(strings.TrimPrefix(digest, "sha256:"))
	digestBytes, err := hex.DecodeString(digestHex)
	if err != nil || len(digestBytes) != sha256.Size {
		return false, fmt.Errorf("invalid sha256 attestation digest %q", digest)
	}
	return verifyGithubAttestationDigest(ctx, digestHex, digestBytes, repo)
}

func verifyGithubAttestationDigest(ctx context.Context, digestHex string, digestBytes []byte, repo string) (bool, error) {
	owner, repoName, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || repoName == "" || strings.Contains(repoName, "/") {
		return false, errors.New("attestation repo must be org/repo")
	}

	bundles, err := fetchAttestations(ctx, owner, repoName, digestHex)
	if err != nil {
		return false, fmt.Errorf("attestation verification failed for %s: %w", repo, err)
	}
	if len(bundles) == 0 {
		return false, fmt.Errorf("attestation verification failed for %s: %w", repo, errNoAttestations)
	}

	trusted, err := trustedRootOnce()
	if err != nil {
		return false, fmt.Errorf("attestation verification failed for %s: %w", repo, err)
	}

	verifier, err := verify.NewVerifier(trusted, verify.WithObserverTimestamps(1))
	if err != nil {
		return false, fmt.Errorf("attestation verification failed for %s: %w", repo, err)
	}

	idActions, err := verify.NewShortCertificateIdentity(
		"https://token.actions.githubusercontent.com", "", "",
		"^https://github.com/"+regexp.QuoteMeta(owner+"/"+repoName)+"/")
	if err != nil {
		return false, fmt.Errorf("attestation verification failed for %s: %w", repo, err)
	}
	idReleases, err := verify.NewShortCertificateIdentity(
		"", "^https://", "https://dotcom.releases.github.com", "")
	if err != nil {
		return false, fmt.Errorf("attestation verification failed for %s: %w", repo, err)
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
		firstErr = errNoValidAttestations
	}
	return false, fmt.Errorf("attestation verification failed for %s: %w", repo, firstErr)
}

func fileDigest(path string) (string, []byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", nil, err
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum), sum, nil
}

func fetchAttestations(ctx context.Context, owner, repo, digestHex string) ([][]byte, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/attestations/sha256:%s?per_page=30",
		githubAttestAPI, owner, repo, digestHex)

	body, status, err := attestGet(ctx, url, true)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, errNoAttestations
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
	var firstFetchErr error
	for _, a := range parsed.Attestations {
		if len(a.Bundle) > 0 && string(a.Bundle) != "null" {
			out = append(out, a.Bundle)
			continue
		}
		if a.BundleURL != "" {
			raw, err := fetchBundleURL(ctx, a.BundleURL)
			if err != nil {
				if firstFetchErr == nil {
					firstFetchErr = err
				}
				continue
			}
			out = append(out, raw)
		}
	}
	if len(out) == 0 {
		// Attestations existed but none could be downloaded: report the real
		// transport failure instead of "no attestations found", which under
		// --verify=attest reads as an absent signature rather than an outage.
		if firstFetchErr != nil {
			return nil, firstFetchErr
		}
		return nil, errNoAttestations
	}
	return out, nil
}

func fetchBundleURL(ctx context.Context, rawURL string) ([]byte, error) {
	if !isAllowedBundleURL(rawURL) {
		return nil, fmt.Errorf("refusing attestation bundle_url outside GitHub: %s", rawURL)
	}
	onAPI := isGitHubAPIURL(rawURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "gpie-attestation")
	if onAPI {
		req.Header.Set("X-Github-Api-Version", "2022-11-28")
		if token := githubToken(); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	resp, err := bundleHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("bundle download returned %d", resp.StatusCode)
	}
	body, err := readLimited(resp.Body, maxAPIResponseBytes)
	if err != nil {
		return nil, err
	}
	if resp.Header.Get("Content-Type") == "application/x-snappy" {
		decodedLen, err := snappy.DecodedLen(body)
		if err != nil {
			return nil, err
		}
		if int64(decodedLen) > maxAPIResponseBytes {
			return nil, fmt.Errorf("decoded attestation bundle exceeds the %d-byte safety limit", maxAPIResponseBytes)
		}
		decoded, err := snappy.Decode(nil, body)
		if err != nil {
			return nil, err
		}
		return decoded, nil
	}
	return body, nil
}

func isGitHubAPIURL(rawURL string) bool {
	parsed, err := neturl.Parse(rawURL)
	return err == nil &&
		parsed.Scheme == "https" &&
		strings.EqualFold(parsed.Hostname(), "api.github.com")
}

// isAllowedBundleURL restricts attestation bundle downloads to https on
// GitHub-controlled hosts. bundle_url is attacker-influencable API data, so it
// must not be able to point the client at an arbitrary origin.
func isAllowedBundleURL(rawURL string) bool {
	parsed, err := neturl.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "api.github.com" || host == "github.com" {
		return true
	}
	// GitHub serves bundle blobs from its storage/CDN subdomains.
	for _, suffix := range []string{
		".githubusercontent.com",
		".github.com",
		".githubassets.com",
	} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

func attestGet(ctx context.Context, url string, onAPI bool) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "gpie-attestation")
	if onAPI {
		req.Header.Set("X-Github-Api-Version", "2022-11-28")
		if token := githubToken(); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := readLimited(resp.Body, maxAPIResponseBytes)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}
