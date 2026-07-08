package download

import (
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
)

// Expected is a checksum expectation attached to a downloaded artifact.
type Expected struct {
	Algo string // "sha256" | "sha1" | "" (none)
	Hex  string // lowercase
}

// ExpectedFromGithubDigest parses a GitHub asset digest value like "sha256:abcd...".
func ExpectedFromGithubDigest(digest *string) Expected {
	if digest != nil {
		if hexPart, ok := strings.CutPrefix(*digest, "sha256:"); ok && hexPart != "" {
			return Expected{Algo: "sha256", Hex: strings.ToLower(hexPart)}
		}
	}
	return Expected{}
}

// ExpectedFromPackagistShasum parses a Packagist dist shasum (SHA-1 hex, may be empty).
func ExpectedFromPackagistShasum(shasum *string) Expected {
	if shasum != nil {
		trimmed := strings.TrimSpace(*shasum)
		if trimmed != "" {
			return Expected{Algo: "sha1", Hex: strings.ToLower(trimmed)}
		}
	}
	return Expected{}
}

// Describe returns a short human description of the guarantee.
func (e Expected) Describe() string {
	switch e.Algo {
	case "sha256":
		return "sha256"
	case "sha1":
		return "sha1"
	default:
		return "none"
	}
}

// VerifyBytes verifies bytes against the expected checksum. It returns (true, nil)
// when a checksum was present and matched, (false, nil) when no checksum was
// available, and an error on a mismatch (always fatal).
func VerifyBytes(b []byte, e Expected) (bool, error) {
	switch e.Algo {
	case "sha256":
		sum := sha256.Sum256(b)
		got := hex.EncodeToString(sum[:])
		if constantTimeEq(got, e.Hex) {
			return true, nil
		}
		return false, fmt.Errorf("sha256 checksum mismatch: expected %s, got %s", e.Hex, got)
	case "sha1":
		sum := sha1.Sum(b)
		got := hex.EncodeToString(sum[:])
		if constantTimeEq(got, e.Hex) {
			return true, nil
		}
		return false, fmt.Errorf("sha1 checksum mismatch: expected %s, got %s", e.Hex, got)
	default:
		return false, nil
	}
}

func constantTimeEq(a, b string) bool {
	ab, bb := []byte(a), []byte(b)
	if len(ab) != len(bb) {
		return false
	}
	return subtle.ConstantTimeCompare(ab, bb) == 1
}
