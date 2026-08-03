//go:build noattest

package download

import "context"

// VerifyGithubAttestation is the no-attestation build stub: it always reports
// (false, nil), so `--verify attest` degrades to the "attestation support not
// built in" warning.
func VerifyGithubAttestation(_ context.Context, assetPath, repo string) (bool, error) {
	return false, nil
}

// VerifyGithubAttestationDigest is the no-attestation build stub.
func VerifyGithubAttestationDigest(_ context.Context, digest, repo string) (bool, error) {
	return false, nil
}
