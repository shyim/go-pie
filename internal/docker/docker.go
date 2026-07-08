// Package docker provides Docker/Linux-distro awareness for installing PHP
// extensions inside official PHP Docker images, inspired by mlocati's
// docker-php-extension-installer.
package docker

import (
	"os/exec"
)

// InOfficialPHPImage reports whether we appear to be inside an official PHP
// Docker image, i.e. the docker-php-ext-* helper scripts are on $PATH.
func InOfficialPHPImage() bool {
	return lookPathOK("docker-php-ext-install") && lookPathOK("docker-php-source")
}

// ResolvedSystemDeps is one extension's system-dependency needs plus provenance.
type ResolvedSystemDeps struct {
	ExtensionName string
	Deps          SystemDeps
	// FromPackagist is true when the deps came from the package's Packagist
	// lib-* requires rather than the embedded catalog.
	FromPackagist bool
}

// ResolveSystemDeps resolves the system dependencies for one extension.
//
// Packagist wins: if the package declares lib-* requirements we can map to
// distro packages, those are used. Otherwise fall back to the embedded catalog
// keyed by extension name. Returns nil when neither yields anything.
func ResolveSystemDeps(extensionName string, libRequires []string, distro *Distro) *ResolvedSystemDeps {
	var mapped []string
	for _, lib := range libRequires {
		if pkg, ok := lookupLibrary(lib, distro.Family); ok {
			mapped = append(mapped, pkg)
		}
	}
	if len(mapped) > 0 {
		return &ResolvedSystemDeps{
			ExtensionName: extensionName,
			Deps: SystemDeps{
				Persistent: nil,
				BuildOnly:  mapped,
			},
			FromPackagist: true,
		}
	}

	deps := lookup(extensionName, distro.Family)
	if deps == nil {
		return nil
	}
	return &ResolvedSystemDeps{
		ExtensionName: extensionName,
		Deps:          *deps,
		FromPackagist: false,
	}
}

// MergeSystemDeps merges many extensions' dependencies into a single
// deduplicated set (first-seen order preserved per list, no cross-list dedup).
func MergeSystemDeps(all []SystemDeps) SystemDeps {
	var persistent, buildOnly []string
	pSeen := make(map[string]struct{})
	bSeen := make(map[string]struct{})
	for _, d := range all {
		for _, p := range d.Persistent {
			if _, ok := pSeen[p]; !ok {
				pSeen[p] = struct{}{}
				persistent = append(persistent, p)
			}
		}
		for _, b := range d.BuildOnly {
			if _, ok := bSeen[b]; !ok {
				bSeen[b] = struct{}{}
				buildOnly = append(buildOnly, b)
			}
		}
	}
	return SystemDeps{
		Persistent: persistent,
		BuildOnly:  buildOnly,
	}
}

// InstallSystemDeps installs a merged dependency set in one package-manager
// invocation (persistent and build-only together).
func InstallSystemDeps(deps *SystemDeps, distro *Distro) error {
	pm := PackageManagerForDistro(distro)
	all := pm.ResolveRuntimePackages(deps.Persistent)
	all = append(all, deps.BuildOnly...)
	return pm.Install(all)
}

func lookPathOK(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
