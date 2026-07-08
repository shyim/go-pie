//go:build noattest

package download

// VerifyGithubAttestation is the no-attestation build stub: it always reports
// (false, nil), so `--verify attest` degrades to the "attestation support not
// built in" warning.
func VerifyGithubAttestation(assetPath, repo string) (bool, error) {
	return false, nil
}
