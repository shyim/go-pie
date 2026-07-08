package oci

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/shyim/go-pie/internal/version"
)

const acceptManifests = "application/vnd.oci.image.index.v1+json, " +
	"application/vnd.oci.image.manifest.v1+json, " +
	"application/vnd.docker.distribution.manifest.list.v2+json, " +
	"application/vnd.docker.distribution.manifest.v2+json"

var httpClient = &http.Client{}

// Registry is a registry endpoint + repository namespace, e.g.
// `ghcr.io` + `shyim/rpie-ext`.
type Registry struct {
	Host      string
	Namespace string
	insecure  bool
	token     string
}

// ParseRegistry builds a Registry from a `host/namespace` string. An explicit
// `http://` prefix — or a localhost host — selects plain HTTP. Reads
// GITHUB_TOKEN/GH_TOKEN for authenticated pulls.
func ParseRegistry(s string) (*Registry, error) {
	var explicitHTTP bool
	var rest string
	if after, ok := strings.CutPrefix(s, "http://"); ok {
		explicitHTTP = true
		rest = after
	} else {
		explicitHTTP = false
		rest = strings.TrimPrefix(s, "https://")
	}
	host, namespace, ok := strings.Cut(rest, "/")
	if !ok {
		return nil, fmt.Errorf("registry must be `host/namespace`, e.g. ghcr.io/org/rpie-ext")
	}
	insecure := explicitHTTP || isLocalhost(host)
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	return &Registry{
		Host:      host,
		Namespace: strings.Trim(namespace, "/"),
		insecure:  insecure,
		token:     token,
	}, nil
}

func (r *Registry) scheme() string {
	if r.insecure {
		return "http"
	}
	return "https"
}

func (r *Registry) repo(extension string) string {
	return r.Namespace + "/" + extension
}

// pullToken performs the OCI token-auth dance. Returns the bearer token or "".
// Never errors — any failure returns "" and the caller proceeds unauthenticated.
func (r *Registry) pullToken(repo string) string {
	if r.insecure {
		return ""
	}
	url := fmt.Sprintf(
		"https://%s/token?scope=repository:%s:pull&service=%s",
		tokenHost(r.Host), repo, r.Host,
	)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", version.UserAgent)
	if r.token != "" {
		basic := base64.StdEncoding.EncodeToString([]byte("token:" + r.token))
		req.Header.Set("Authorization", "Basic "+basic)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return ""
	}
	if t, ok := body["token"].(string); ok {
		return t
	}
	if t, ok := body["access_token"].(string); ok {
		return t
	}
	return ""
}

func (r *Registry) authedRequest(url, bearer string, accept bool) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", version.UserAgent)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if accept {
		req.Header.Set("Accept", acceptManifests)
	}
	return req, nil
}

// GetIndex fetches the image index (manifest list) for extension:tag, returning
// the parsed JSON. Returns nil,nil when the tag is a cache miss (404/401/403).
func (r *Registry) GetIndex(ext, tag string) (map[string]any, error) {
	repo := r.repo(ext)
	bearer := r.pullToken(repo)
	url := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", r.scheme(), r.Host, repo, tag)
	req, err := r.authedRequest(url, bearer, true)
	if err != nil {
		return nil, fmt.Errorf("fetching index %s: %w", url, err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching index %s: %w", url, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotFound, http.StatusUnauthorized, http.StatusForbidden:
		return nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetching index %s: %s", url, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parsing OCI index: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parsing OCI index: %w", err)
	}
	return out, nil
}

// GetManifest fetches a manifest by digest, returning parsed JSON.
func (r *Registry) GetManifest(ext, digest string) (map[string]any, error) {
	repo := r.repo(ext)
	bearer := r.pullToken(repo)
	url := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", r.scheme(), r.Host, repo, digest)
	req, err := r.authedRequest(url, bearer, true)
	if err != nil {
		return nil, fmt.Errorf("fetching manifest %s: %w", digest, err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching manifest %s: %w", digest, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetching manifest %s: %s", digest, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parsing OCI manifest: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parsing OCI manifest: %w", err)
	}
	return out, nil
}

// GetBlob fetches a blob by digest, returning its raw bytes, digest-verified.
func (r *Registry) GetBlob(ext, digest string) ([]byte, error) {
	repo := r.repo(ext)
	bearer := r.pullToken(repo)
	url := fmt.Sprintf("%s://%s/v2/%s/blobs/%s", r.scheme(), r.Host, repo, digest)
	req, err := r.authedRequest(url, bearer, false)
	if err != nil {
		return nil, fmt.Errorf("fetching blob %s: %w", digest, err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching blob %s: %w", digest, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetching blob %s: %s", digest, resp.Status)
	}
	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading blob %s: %w", digest, err)
	}
	if err := verifyDigest(buf, digest); err != nil {
		return nil, err
	}
	return buf, nil
}

// tokenHost is the token service host for a registry. GHCR issues tokens from
// ghcr.io itself; the seam lets a future registry with a separate auth host
// slot in.
func tokenHost(host string) string {
	return host
}

// isLocalhost reports whether host is a loopback host (with or without a port).
func isLocalhost(host string) bool {
	h, _, _ := strings.Cut(host, ":")
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// verifyDigest verifies bytes hash to digest (`sha256:<hex>`); no-op for
// unknown algorithms.
func verifyDigest(bytes []byte, digest string) error {
	hexWant, ok := strings.CutPrefix(digest, "sha256:")
	if !ok {
		return nil
	}
	sum := sha256.Sum256(bytes)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, hexWant) {
		return fmt.Errorf("blob digest mismatch: expected %s, got sha256:%s", digest, got)
	}
	return nil
}
