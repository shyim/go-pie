package docker

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

// DistroFamily classifies a Linux distribution into a package-manager family.
type DistroFamily int

const (
	FamilyDebian DistroFamily = iota
	FamilyAlpine
	FamilyOther
)

// Distro describes the detected Linux distribution.
type Distro struct {
	ID        string
	VersionID string
	Family    DistroFamily
}

// DetectDistro detects the current Linux distribution, or nil off Linux or when
// /etc/os-release is unreadable.
func DetectDistro() *Distro {
	if runtime.GOOS != "linux" {
		return nil
	}
	content, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return nil
	}
	s := string(content)
	id := osReleaseField(s, "ID")
	idLike := osReleaseField(s, "ID_LIKE")
	versionID := osReleaseField(s, "VERSION_ID")
	return &Distro{
		ID:        id,
		VersionID: versionID,
		Family:    classify(id, idLike),
	}
}

// Label returns the "id@version" style label, e.g. "alpine@3.20.3".
func (d *Distro) Label() string {
	return fmt.Sprintf("%s@%s", d.ID, d.VersionID)
}

// FamilyToken returns the distro-family key: "debian", "alpine", or "other".
func (d *Distro) FamilyToken() string {
	switch d.Family {
	case FamilyDebian:
		return "debian"
	case FamilyAlpine:
		return "alpine"
	case FamilyOther:
		return "other"
	default:
		return "other"
	}
}

func classify(id, idLike string) DistroFamily {
	id = strings.ToLower(id)
	idLike = strings.ToLower(idLike)
	if id == "alpine" {
		return FamilyAlpine
	}
	if id == "debian" || id == "ubuntu" ||
		strings.Contains(idLike, "debian") ||
		strings.Contains(idLike, "ubuntu") {
		return FamilyDebian
	}
	return FamilyOther
}

// osReleaseField parses the first KEY=value line from os-release content,
// stripping surrounding whitespace, then double quotes, then single quotes.
// Returns "" when the key is absent.
func osReleaseField(content, key string) string {
	prefix := key + "="
	for line := range strings.SplitSeq(content, "\n") {
		if rest, ok := strings.CutPrefix(line, prefix); ok {
			v := strings.Trim(strings.TrimSpace(rest), "\"")
			v = strings.Trim(v, "'")
			return v
		}
	}
	return ""
}
