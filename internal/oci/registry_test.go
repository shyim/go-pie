package oci

import (
	"encoding/base64"
	"testing"
)

func TestParsesRegistry(t *testing.T) {
	r, err := ParseRegistry("ghcr.io/shyim/rpie-ext")
	if err != nil {
		t.Fatal(err)
	}
	if r.Host != "ghcr.io" {
		t.Errorf("host = %q", r.Host)
	}
	if r.Namespace != "shyim/rpie-ext" {
		t.Errorf("namespace = %q", r.Namespace)
	}
	if got := r.repo("redis"); got != "shyim/rpie-ext/redis" {
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
	if err := verifyDigest([]byte("anything"), "sha512:whatever"); err != nil {
		t.Errorf("unknown algorithm should not be enforced, got %v", err)
	}
}
