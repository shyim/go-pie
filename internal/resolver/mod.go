// Package resolver resolves an extension request (`vendor/name:^1.0`) to a
// concrete package by querying Packagist and parsing its `php-ext` metadata.
package resolver

import (
	"fmt"

	"github.com/shyim/go-pie/internal/platform"
)

type ResolvedPackage struct {
	Name          string
	Version       string
	ExtensionName string
	ExtensionType ExtensionType
	Metadata      PhpExtMetadata
	DistURL       *string
	DistType      *string
	DistShasum    *string
	SourceURL     *string
	LibRequires   []string
	Requires      map[string]string
	Priority      uint32
}

func Resolve(client PackageSource, req *RequestedPackage, plat *platform.TargetPlatform) (*ResolvedPackage, error) {
	versions, err := client.PackageVersions(req.Name)
	if err != nil {
		return nil, fmt.Errorf("looking up `%s` on Packagist: %w", req.Name, err)
	}

	idx, err := selectVersion(versions, req.Constraint)
	if err != nil {
		return nil, fmt.Errorf("no version of `%s` satisfies the request: %w", req.Name, err)
	}
	chosen := &versions[idx]

	effectiveType := chosen.PackageType
	if effectiveType == "" {
		for i := range versions {
			if versions[i].PackageType != "" {
				effectiveType = versions[i].PackageType
				break
			}
		}
	}

	extType, ok := ExtensionTypeFromComposerType(effectiveType)
	if !ok {
		return nil, fmt.Errorf("`%s:%s` is type `%s`, not a PHP extension (php-ext / php-ext-zend)",
			req.Name, chosen.Version, effectiveType)
	}

	metadata := MetadataFromValue(chosen.PhpExt, req.Name)
	extensionName := metadata.ExtensionName

	if !metadata.SupportsThreadSafety(plat.ThreadSafety) {
		return nil, fmt.Errorf("`%s` does not support the target PHP's thread-safety mode (%s)",
			req.Name, plat.ThreadSafety.String())
	}
	if !metadata.SupportsOsFamily(plat.OSFamily) {
		return nil, fmt.Errorf("`%s` is not compatible with this operating system family (%s)",
			req.Name, plat.OSFamily.Token())
	}

	return &ResolvedPackage{
		Name:          req.Name,
		Version:       chosen.Version,
		ExtensionName: extensionName,
		ExtensionType: extType,
		Priority:      metadata.Priority,
		Metadata:      metadata,
		DistURL:       chosen.DistURL,
		DistType:      chosen.DistType,
		DistShasum:    chosen.DistShasum,
		SourceURL:     chosen.SourceURL,
		LibRequires:   chosen.LibRequires,
		Requires:      chosen.Requires,
	}, nil
}
