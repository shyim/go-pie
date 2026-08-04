package oci

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestGetIndexReturnsNotFoundSentinelForCacheMiss(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	r := &Registry{
		Host:      server.Listener.Addr().String(),
		Namespace: "example/extensions",
		insecure:  true,
	}
	_, err := r.GetIndex(t.Context(), "redis", "1.0.0")
	if !errors.Is(err, ErrPrebuiltNotFound) {
		t.Fatalf("GetIndex error = %v, want ErrPrebuiltNotFound", err)
	}
}

func TestParsesRegistry(t *testing.T) {
	r, err := ParseRegistry("ghcr.io/shyim/gpie-ext")
	if err != nil {
		t.Fatal(err)
	}
	if r.Host != "ghcr.io" {
		t.Errorf("host = %q", r.Host)
	}
	if r.Namespace != "shyim/gpie-ext" {
		t.Errorf("namespace = %q", r.Namespace)
	}
	if got := r.repo("redis"); got != "shyim/gpie-ext/redis" {
		t.Errorf("repo = %q", got)
	}
	if got := r.scheme(); got != "https" {
		t.Errorf("scheme = %q", got)
	}
	if _, err := ParseRegistry("noSlash"); err == nil {
		t.Fatal("expected error for noSlash")
	}
}

func TestSelectsScheme(t *testing.T) {
	cases := map[string]string{
		"http://reg.local/ns":     "http",
		"localhost:5000/ns":       "http",
		"127.0.0.1:5000/ns":       "http",
		"[::1]:5000/ns":           "http",
		"https://ghcr.io/ns":      "https",
		"registry.example.com/ns": "https",
	}
	for in, want := range cases {
		r, err := ParseRegistry(in)
		if err != nil {
			t.Fatalf("ParseRegistry(%q): %v", in, err)
		}
		if got := r.scheme(); got != want {
			t.Errorf("scheme(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScopesGitHubTokenToGhcr(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "top-secret")
	t.Setenv("GH_TOKEN", "")

	ghcr, err := ParseRegistry("ghcr.io/acme/extensions")
	if err != nil {
		t.Fatal(err)
	}
	if ghcr.token != "top-secret" {
		t.Fatal("GHCR should receive the configured GitHub token")
	}

	custom, err := ParseRegistry("registry.example.com/acme/extensions")
	if err != nil {
		t.Fatal(err)
	}
	if custom.token != "" {
		t.Fatal("custom registry must not receive a GitHub token")
	}

	lookalike, err := ParseRegistry("ghcr.io.example.com/acme/extensions")
	if err != nil {
		t.Fatal(err)
	}
	if lookalike.token != "" {
		t.Fatal("GHCR lookalike host must not receive a GitHub token")
	}
}

func TestBase64MatchesKnownVectors(t *testing.T) {
	cases := map[string]string{
		"":             "",
		"f":            "Zg==",
		"fo":           "Zm8=",
		"foo":          "Zm9v",
		"token:secret": "dG9rZW46c2VjcmV0",
	}
	for in, want := range cases {
		if got := base64.StdEncoding.EncodeToString([]byte(in)); got != want {
			t.Errorf("base64(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVerifyDigestDetectsMismatch(t *testing.T) {
	if err := verifyDigest([]byte("hello"), "sha256:deadbeef"); err == nil {
		t.Error("expected mismatch error")
	}
	ok := "sha256:2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if err := verifyDigest([]byte("hello"), ok); err != nil {
		t.Errorf("expected ok, got %v", err)
	}
	if err := verifyDigest([]byte("anything"), "sha512:whatever"); err == nil {
		t.Error("unknown digest algorithms must fail closed")
	}
}

func TestReadRegistryBodyRejectsOversizedResponse(t *testing.T) {
	got, err := readRegistryBody(strings.NewReader("1234"), 4)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "1234" {
		t.Fatalf("body = %q", got)
	}

	if _, err := readRegistryBody(strings.NewReader("12345"), 4); err == nil {
		t.Fatal("expected registry response size rejection")
	}
}

// A resolve issues four requests (index, manifest, config blob, layer blob) and
// each used to redo the token dance -- measured at ~730ms of redundant auth per
// cache hit, against ~815ms of actual content transfer.
func TestPullTokenIsFetchedOncePerRepo(t *testing.T) {
	var tokenRequests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/token") {
			tokenRequests.Add(1)
			_, _ = w.Write([]byte(`{"token":"t"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	// `insecure` skips the token dance entirely, and a loopback host would set
	// it, so drive the caching path directly instead.
	r := &Registry{
		Host:       strings.TrimPrefix(srv.URL, "http://"),
		Namespace:  "ns",
		insecure:   false,
		pullTokens: make(map[string]string),
	}
	// tokenHost() builds an https:// URL; point the client at the test server.
	restore := httpClient
	httpClient = srv.Client()
	httpClient.Transport = rewriteToTestServer{srv.URL}
	defer func() { httpClient = restore }()

	for range 4 {
		r.pullToken(context.Background(), "ns/redis")
	}
	if got := tokenRequests.Load(); got != 1 {
		t.Errorf("token fetched %d times for one repo, want 1", got)
	}

	// A different repository needs its own scoped token.
	r.pullToken(context.Background(), "ns/xdebug")
	if got := tokenRequests.Load(); got != 2 {
		t.Errorf("token fetched %d times for two repos, want 2", got)
	}
}

// rewriteToTestServer sends every request to the httptest server regardless of
// the scheme/host the code under test built, so the https-only token URL can be
// exercised without TLS.
type rewriteToTestServer struct{ base string }

func (t rewriteToTestServer) RoundTrip(req *http.Request) (*http.Response, error) {
	u, err := url.Parse(t.base)
	if err != nil {
		return nil, err
	}
	req = req.Clone(req.Context())
	req.URL.Scheme = u.Scheme
	req.URL.Host = u.Host
	return http.DefaultTransport.RoundTrip(req)
}
