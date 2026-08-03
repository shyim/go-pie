package oci

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/shyim/go-pie/internal/version"
)

const acceptManifests = "application/vnd.oci.image.index.v1+json, " +
	"application/vnd.oci.image.manifest.v1+json, " +
	"application/vnd.docker.distribution.manifest.list.v2+json, " +
	"application/vnd.docker.distribution.manifest.v2+json"

const (
	maxRegistryDocumentBytes int64 = 32 << 20
	maxRegistryBlobBytes     int64 = 512 << 20
)

var httpClient = &http.Client{Timeout: 5 * time.Minute}

// Registry is a registry endpoint + repository namespace, e.g.
// `ghcr.io` + `shyim/gpie-ext`.
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
		return nil, errors.New("registry must be `host/namespace`, e.g. ghcr.io/org/gpie-ext")
	}
	insecure := explicitHTTP || isLocalhost(host)
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	if !isGitHubContainerRegistry(host) {
		token = ""
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
func (r *Registry) pullToken(ctx context.Context, repo string) string {
	if r.insecure {
		return ""
	}
	url := fmt.Sprintf(
		"https://%s/token?scope=repository:%s:pull&service=%s",
		tokenHost(r.Host), repo, r.Host,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}
	var body map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxRegistryDocumentBytes)).Decode(&body); err != nil {
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

func (r *Registry) authedRequest(ctx context.Context, url, bearer string, accept bool) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
// the parsed JSON. It returns ErrPrebuiltNotFound on a cache miss (404/401/403).
func (r *Registry) GetIndex(ctx context.Context, ext, tag string) (map[string]any, error) {
	repo := r.repo(ext)
	bearer := r.pullToken(ctx, repo)
	url := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", r.scheme(), r.Host, repo, tag)
	req, err := r.authedRequest(ctx, url, bearer, true)
	if err != nil {
		return nil, fmt.Errorf("fetching index %s: %w", url, err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching index %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusNotFound, http.StatusUnauthorized, http.StatusForbidden:
		return nil, ErrPrebuiltNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetching index %s: %s", url, resp.Status)
	}
	body, err := readRegistryBody(resp.Body, maxRegistryDocumentBytes)
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
func (r *Registry) GetManifest(ctx context.Context, ext, digest string) (map[string]any, error) {
	repo := r.repo(ext)
	bearer := r.pullToken(ctx, repo)
	url := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", r.scheme(), r.Host, repo, digest)
	req, err := r.authedRequest(ctx, url, bearer, true)
	if err != nil {
		return nil, fmt.Errorf("fetching manifest %s: %w", digest, err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching manifest %s: %w", digest, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetching manifest %s: %s", digest, resp.Status)
	}
	body, err := readRegistryBody(resp.Body, maxRegistryDocumentBytes)
	if err != nil {
		return nil, fmt.Errorf("parsing OCI manifest: %w", err)
	}
	if err := verifyDigest(body, digest); err != nil {
		return nil, fmt.Errorf("verifying manifest %s: %w", digest, err)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parsing OCI manifest: %w", err)
	}
	return out, nil
}

// GetBlob fetches a blob by digest, returning its raw bytes, digest-verified.
func (r *Registry) GetBlob(ctx context.Context, ext, digest string) ([]byte, error) {
	repo := r.repo(ext)
	bearer := r.pullToken(ctx, repo)
	url := fmt.Sprintf("%s://%s/v2/%s/blobs/%s", r.scheme(), r.Host, repo, digest)
	req, err := r.authedRequest(ctx, url, bearer, false)
	if err != nil {
		return nil, fmt.Errorf("fetching blob %s: %w", digest, err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching blob %s: %w", digest, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetching blob %s: %s", digest, resp.Status)
	}
	buf, err := readRegistryBody(resp.Body, maxRegistryBlobBytes)
	if err != nil {
		return nil, fmt.Errorf("reading blob %s: %w", digest, err)
	}
	if err := verifyDigest(buf, digest); err != nil {
		return nil, err
	}
	return buf, nil
}

func readRegistryBody(r io.Reader, limit int64) ([]byte, error) {
	buf, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(buf)) > limit {
		return nil, fmt.Errorf("registry response exceeds the %d-byte safety limit", limit)
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
	h := hostWithoutPort(host)
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

func isGitHubContainerRegistry(host string) bool {
	return strings.EqualFold(hostWithoutPort(host), "ghcr.io")
}

func hostWithoutPort(host string) string {
	if strings.HasPrefix(host, "[") {
		if end := strings.Index(host, "]"); end >= 0 {
			return host[1:end]
		}
	}
	if h, port, ok := strings.Cut(host, ":"); ok && port != "" && !strings.Contains(port, ":") {
		return h
	}
	return host
}

// verifyDigest verifies bytes hash to digest (`sha256:<hex>`). Unsupported or
// malformed digests fail closed instead of silently disabling OCI integrity.
func verifyDigest(bytes []byte, digest string) error {
	hexWant, ok := strings.CutPrefix(digest, "sha256:")
	if !ok {
		return fmt.Errorf("unsupported OCI digest %q (only sha256 is supported)", digest)
	}
	if len(hexWant) != sha256.Size*2 {
		return fmt.Errorf("invalid sha256 OCI digest %q", digest)
	}
	sum := sha256.Sum256(bytes)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, hexWant) {
		return fmt.Errorf("blob digest mismatch: expected %s, got sha256:%s", digest, got)
	}
	return nil
}
