package platform

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	phpdiscover "github.com/shyim/go-php-discover"
)

// DiscoveredPhp is a PHP installation found on this machine, before it has been
// introspected into a full PhpBinary.
type DiscoveredPhp struct {
	// Path is the absolute path to the binary with symlinks resolved.
	Path string
	// Version is what `php --version` reported. It is only used for display and
	// selection; a resolved PhpBinary re-reads the version from the binary.
	Version PhpVersion
	// Source names the installation this binary belongs to (PATH, homebrew, ...).
	Source string
	// IsSystem marks the first binary found on $PATH.
	IsSystem bool
	// PhpizePath is the sibling `phpize` of the same installation, when present.
	PhpizePath string
}

// DiscoverPhp returns every PHP installation found on this machine, sorted by
// version (ascending). Discovery is best-effort: binaries that cannot be run
// are skipped, so an empty result means nothing usable was found rather than an
// error.
func DiscoverPhp(ctx context.Context) []DiscoveredPhp {
	found := phpdiscover.Discover(ctx)
	out := make([]DiscoveredPhp, 0, len(found))
	for _, php := range found {
		out = append(out, DiscoveredPhp{
			Path: php.Path,
			Version: PhpVersion{
				Major: clampVersionPart(php.Version.Major),
				Minor: clampVersionPart(php.Version.Minor),
				Patch: clampVersionPart(php.Version.Patch),
			},
			Source:     php.Source,
			IsSystem:   php.IsSystem,
			PhpizePath: php.PHPizePath,
		})
	}
	return out
}

// clampVersionPart narrows a discovered version component to the uint8 used by
// PhpVersion. Real PHP versions are far below 255; a nonsensical value is
// clamped rather than silently wrapping around.
func clampVersionPart(n int) uint8 {
	if n < 0 {
		return 0
	}
	if n > 255 {
		return 255
	}
	return uint8(n)
}

// ErrNoPhpFound is returned when no PHP binary could be located at all.
var ErrNoPhpFound = errors.New("could not find a `php` binary on your PATH or in any known PHP installation; pass --with-php-path")

// PhpBinaryDiscovered locates the PHP binary to target when the user did not
// pass an explicit path: the system PHP if one is on $PATH, otherwise the newest
// discovered installation.
func PhpBinaryDiscovered(ctx context.Context) (*PhpBinary, error) {
	list := DiscoverPhp(ctx)
	if len(list) == 0 {
		return nil, ErrNoPhpFound
	}
	return PhpBinaryFromPath(ctx, DefaultPhp(list).Path)
}

// DefaultPhp picks the installation to use from a discovery result: the system
// PHP (first on $PATH) when present, otherwise the newest one. It returns nil
// for an empty list.
func DefaultPhp(list []DiscoveredPhp) *DiscoveredPhp {
	if len(list) == 0 {
		return nil
	}
	for i := range list {
		if list[i].IsSystem {
			return &list[i]
		}
	}
	// DiscoverPhp is sorted ascending by version, so the last entry is newest.
	return &list[len(list)-1]
}

// PhpBinaryFromSelector resolves a --with-php-path value that may be either a
// path to a binary or a version selector such as "8.3" or "8.3.2", matched
// against the discovered installations.
func PhpBinaryFromSelector(ctx context.Context, selector string) (*PhpBinary, error) {
	if !looksLikeVersionSelector(selector) {
		return PhpBinaryFromPath(ctx, selector)
	}

	list := DiscoverPhp(ctx)
	matches := make([]DiscoveredPhp, 0, len(list))
	for _, php := range list {
		if versionSelectorMatches(selector, php.Version) {
			matches = append(matches, php)
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no installed PHP matches %q; run `gpie php:list` to see what is available", selector)
	}
	// matches keeps DiscoverPhp's ascending order, so the last is the newest
	// patch release of the requested version.
	return PhpBinaryFromPath(ctx, matches[len(matches)-1].Path)
}

// looksLikeVersionSelector reports whether s is a bare version like "8" or
// "8.3.2" rather than a filesystem path.
func looksLikeVersionSelector(s string) bool {
	if s == "" {
		return false
	}
	for part := range strings.SplitSeq(s, ".") {
		if part == "" {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

// versionSelectorMatches reports whether version satisfies a "8", "8.3" or
// "8.3.2" style selector: every component the selector specifies must be equal.
func versionSelectorMatches(selector string, version PhpVersion) bool {
	parts := strings.Split(selector, ".")
	actual := []uint8{version.Major, version.Minor, version.Patch}
	if len(parts) > len(actual) {
		return false
	}
	for i, part := range parts {
		n, err := strconv.ParseUint(part, 10, 8)
		if err != nil {
			return false
		}
		if uint8(n) != actual[i] {
			return false
		}
	}
	return true
}
