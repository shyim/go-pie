package docker

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/shyim/go-pie/internal/procutil"
)

// PackageManager abstracts over apt-get (Debian) and apk (Alpine).
type PackageManager int

const (
	PMApt PackageManager = iota
	PMApk
	PMUnsupported
)

// PackageManagerForDistro selects the package manager for a distro.
func PackageManagerForDistro(d *Distro) PackageManager {
	switch d.Family {
	case FamilyDebian:
		return PMApt
	case FamilyAlpine:
		return PMApk
	default:
		return PMUnsupported
	}
}

func (pm PackageManager) String() string {
	switch pm {
	case PMApt:
		return "Apt"
	case PMApk:
		return "Apk"
	default:
		return "Unsupported"
	}
}

// Install installs the given packages (no-op when the list is empty).
func (pm PackageManager) Install(packages []string) error {
	if len(packages) == 0 {
		return nil
	}
	switch pm {
	case PMApt:
		if err := procutil.Run("apt-get", []string{"update", "-q"}, ".", "apt-get update"); err != nil {
			return err
		}
		args := append([]string{"install", "-qqy", "--no-install-recommends"}, packages...)
		return procutil.Run("apt-get", args, ".", "apt-get install")
	case PMApk:
		args := append([]string{"add", "--no-cache"}, packages...)
		return procutil.Run("apk", args, ".", "apk add")
	default:
		return errors.New("no supported system package manager for this distribution")
	}
}

// Remove purges build-only packages after a successful build.
func (pm PackageManager) Remove(packages []string) error {
	if len(packages) == 0 {
		return nil
	}
	switch pm {
	case PMApt:
		args := append([]string{"purge", "-qqy"}, packages...)
		return procutil.Run("apt-get", args, ".", "apt-get purge")
	case PMApk:
		args := append([]string{"del", "--purge"}, packages...)
		return procutil.Run("apk", args, ".", "apk del")
	default:
		return nil
	}
}

// ResolveRuntimePackages turns a mixed list of concrete names and IPE regex
// patterns into concrete package names available on the current distro.
func (pm PackageManager) ResolveRuntimePackages(entries []string) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, entry := range entries {
		if !IsPattern(entry) {
			pushUnique(&out, seen, entry)
			continue
		}
		names, err := pm.matchPattern(entry)
		if err == nil && len(names) > 0 {
			for _, n := range names {
				pushUnique(&out, seen, n)
			}
		} else {
			fmt.Fprintf(os.Stderr,
				"  note: runtime package pattern `%s` resolved to nothing; skipping\n", entry)
		}
	}
	return out
}

func (pm PackageManager) matchPattern(pattern string) ([]string, error) {
	var candidates []string
	switch pm {
	case PMApt:
		out, err := procutil.Capture("apt-cache", []string{"search", "--names-only", pattern})
		if err != nil {
			return nil, err
		}
		for _, l := range splitLines(out) {
			if tok := strings.Fields(l); len(tok) > 0 {
				candidates = append(candidates, tok[0])
			}
		}
	case PMApk:
		out, err := procutil.Capture("apk", []string{"search", "--quiet"})
		if err != nil {
			out = ""
		}
		candidates = append(candidates, splitLines(out)...)
	case PMUnsupported:
		candidates = nil
	}
	var matched []string
	for _, name := range candidates {
		if patternMatches(pattern, name) {
			matched = append(matched, name)
		}
	}
	return matched, nil
}

// IsPattern reports whether entry is a regex pattern (rather than a concrete
// package name) by containing any regex metacharacter.
func IsPattern(entry string) bool {
	return strings.ContainsAny(entry, "^[]*()?")
}

func patternMatches(pattern, pkg string) bool {
	anchored := "^" + strings.TrimRight(strings.TrimLeft(pattern, "^"), "$") + "$"
	re, err := regexp.Compile(anchored)
	if err != nil {
		return pattern == pkg
	}
	return re.MatchString(pkg)
}

// splitLines mirrors Rust str::lines(): split on \n, strip one trailing \r per
// line, and produce no trailing empty element for a final newline.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	for i, p := range parts {
		parts[i] = strings.TrimSuffix(p, "\r")
	}
	return parts
}

func pushUnique(out *[]string, seen map[string]struct{}, value string) {
	if _, ok := seen[value]; ok {
		return
	}
	seen[value] = struct{}{}
	*out = append(*out, value)
}
