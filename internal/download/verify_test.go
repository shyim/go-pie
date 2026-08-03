package download

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/shyim/go-pie/internal/resolver"
)

func TestParsesGithubDigest(t *testing.T) {
	s := "sha256:ABCd"
	if e := ExpectedFromGithubDigest(&s); e.Algo != "sha256" || e.Hex != "abcd" {
		t.Fatalf("got %+v", e)
	}
	empty := "sha256:"
	if e := ExpectedFromGithubDigest(&empty); e.Algo != "" {
		t.Fatalf("empty hex should be none, got %+v", e)
	}
	if e := ExpectedFromGithubDigest(nil); e.Algo != "" {
		t.Fatalf("nil should be none, got %+v", e)
	}
	md5 := "md5:xx"
	if e := ExpectedFromGithubDigest(&md5); e.Algo != "" {
		t.Fatalf("md5 should be none, got %+v", e)
	}
}

func TestParsesPackagistShasum(t *testing.T) {
	aabb := "AABB"
	if e := ExpectedFromPackagistShasum(&aabb); e.Algo != "sha1" || e.Hex != "aabb" {
		t.Fatalf("got %+v", e)
	}
	empty := ""
	if e := ExpectedFromPackagistShasum(&empty); e.Algo != "" {
		t.Fatalf("empty should be none, got %+v", e)
	}
	spaces := "  "
	if e := ExpectedFromPackagistShasum(&spaces); e.Algo != "" {
		t.Fatalf("spaces should be none, got %+v", e)
	}
	if e := ExpectedFromPackagistShasum(nil); e.Algo != "" {
		t.Fatalf("nil should be none, got %+v", e)
	}
}

func TestVerifiesSha256Roundtrip(t *testing.T) {
	data := []byte("hello gpie")
	sum := sha256.Sum256(data)
	e := Expected{Algo: "sha256", Hex: hex.EncodeToString(sum[:])}
	ok, err := VerifyBytes(data, e)
	if err != nil || !ok {
		t.Fatalf("got (%v, %v)", ok, err)
	}
}

func TestDetectsSha256Mismatch(t *testing.T) {
	_, err := VerifyBytes([]byte("tampered"), Expected{Algo: "sha256", Hex: strings.Repeat("00", 32)})
	if err == nil || !strings.Contains(err.Error(), "sha256 checksum mismatch") {
		t.Fatalf("got %v", err)
	}
}

func TestNoChecksumReturnsFalseNotError(t *testing.T) {
	ok, err := VerifyBytes([]byte("whatever"), Expected{})
	if err != nil || ok {
		t.Fatalf("got (%v, %v)", ok, err)
	}
}

func TestVerifiesSha1Roundtrip(t *testing.T) {
	data := []byte("composer dist bytes")
	sum := sha1.Sum(data)
	e := Expected{Algo: "sha1", Hex: hex.EncodeToString(sum[:])}
	ok, err := VerifyBytes(data, e)
	if err != nil || !ok {
		t.Fatalf("got (%v, %v)", ok, err)
	}
}

func TestExpectedDescribe(t *testing.T) {
	if (Expected{Algo: "sha256"}).Describe() != "sha256" {
		t.Fatal("sha256")
	}
	if (Expected{Algo: "sha1"}).Describe() != "sha1" {
		t.Fatal("sha1")
	}
	if (Expected{}).Describe() != "none" {
		t.Fatal("none")
	}
}

func TestApplyVerificationStagesReleaseAssetForAttestation(t *testing.T) {
	original := verifyGithubAttestationFile
	defer func() { verifyGithubAttestationFile = original }()

	called := false
	verifyGithubAttestationFile = func(_ context.Context, path, repo string) (bool, error) {
		called = true
		if repo != "owner/repo" {
			t.Fatalf("repo = %q", repo)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "release-asset" {
			t.Fatalf("staged bytes = %q", got)
		}
		return true, nil
	}

	if err := applyVerification(t.Context(),
		[]byte("release-asset"),
		Expected{},
		VerifyAttest,
		"https://example.invalid/asset.tar.gz",
		resolver.PrePackagedSource,
		"owner/repo",
	); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("attestation verifier was not called")
	}
}
