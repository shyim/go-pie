package oci

import (
	"encoding/json"
	"errors"
)

// Media types for the custom OCI artifact.
const (
	ConfigMediaType = "application/vnd.gpie.ext.config.v1+json"
	LayerMediaType  = "application/vnd.gpie.ext.layer.v1.tar+gzip"
)

// ManifestVersion is the current manifest schema version.
const ManifestVersion = uint32(1)

// ExtManifest is the GPIE prebuilt-extension manifest (OCI config blob).
type ExtManifest struct {
	ManifestVersion  uint32              `json:"gpieManifestVersion"`
	Extension        string              `json:"extension"`
	Version          string              `json:"version"`
	ExtensionType    string              `json:"extensionType"`
	IniDirective     string              `json:"iniDirective"`
	Priority         uint32              `json:"priority"`
	Cell             string              `json:"cell"`
	PHP              string              `json:"php"`
	PHPAPI           string              `json:"phpApi"`
	Distro           string              `json:"distro"`
	Arch             string              `json:"arch"`
	ThreadSafety     string              `json:"threadSafety"`
	Debug            bool                `json:"debug"`
	ConfigureOptions []string            `json:"configureOptions"`
	RuntimePackages  map[string][]string `json:"runtimePackages"`
	SoFile           string              `json:"soFile"`
	SoSha256         string              `json:"soSha256"`
	BuiltAt          string              `json:"builtAt"`
	SourceRef        string              `json:"sourceRef"`
	Builder          string              `json:"builder"`
	// AttestationRepository is the GitHub owner/repository whose workflow
	// attested the OCI manifest digest.
	AttestationRepository string `json:"attestationRepository,omitempty"`
}

// ParseExtManifest parses an ExtManifest from JSON bytes (the config blob).
// Lenient (unknown fields ignored) except that an empty "cell" after parse is
// an error, since it drives the authoritative match.
func ParseExtManifest(b []byte) (*ExtManifest, error) {
	var m ExtManifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m.Cell == "" {
		return nil, errors.New("gpie extension manifest missing `cell`")
	}
	return &m, nil
}

// ToJSON serialises to pretty-printed JSON bytes (2-space indent).
func (m *ExtManifest) ToJSON() ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}

// RuntimePackagesFor returns the runtime packages for a distro family
// ("debian"/"alpine"), or an empty slice if the key is absent.
func (m *ExtManifest) RuntimePackagesFor(family string) []string {
	pkgs, ok := m.RuntimePackages[family]
	if !ok {
		return nil
	}
	out := make([]string, len(pkgs))
	copy(out, pkgs)
	return out
}
