package docker

import (
	"context"
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
	case FamilyOther:
		return PMUnsupported
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
	case PMUnsupported:
		return "Unsupported"
	default:
		return "Unsupported"
	}
}

// RefreshIndex updates the package index so availability queries see the real
// catalogue. Official PHP images ship with apt's lists emptied and no apk index
// cached, so `apt-cache` / `apk search` return nothing at all until this has
// run -- which would make every availability probe report "not available" and
// strip real packages. Idempotent and safe to call more than once.
func (pm PackageManager) RefreshIndex(ctx context.Context) error {
	switch pm {
	case PMApt:
		return procutil.Run(ctx, "apt-get", []string{"update", "-q"}, ".", "apt-get update")
	case PMApk:
		return procutil.Run(ctx, "apk", []string{"update", "-q"}, ".", "apk update")
	case PMUnsupported:
		return nil
	}
	return nil
}

// Install installs the given packages (no-op when the list is empty).
func (pm PackageManager) Install(ctx context.Context, packages []string) error {
	if len(packages) == 0 {
		return nil
	}
	switch pm {
	case PMApt:
		if err := pm.RefreshIndex(ctx); err != nil {
			return err
		}
		args := append([]string{"install", "-qqy", "--no-install-recommends"}, packages...)
		return procutil.Run(ctx, "apt-get", args, ".", "apt-get install")
	case PMApk:
		args := append([]string{"add", "--no-cache"}, packages...)
		return procutil.Run(ctx, "apk", args, ".", "apk add")
	case PMUnsupported:
		return errors.New("no supported system package manager for this distribution")
	default:
		return errors.New("no supported system package manager for this distribution")
	}
}

// Missing reports which requested package names are not currently installed.
// It is used to ensure cleanup only removes packages added by this invocation.
func (pm PackageManager) Missing(ctx context.Context, packages []string) ([]string, error) {
	if len(packages) == 0 {
		return nil, nil
	}

	installed := make(map[string]struct{})
	switch pm {
	case PMApt:
		out, err := procutil.Capture(ctx, "dpkg-query",
			[]string{"-W", "-f=${db:Status-Abbrev}\t${binary:Package}\n"})
		if err != nil {
			return nil, err
		}
		for _, line := range splitLines(out) {
			fields := strings.Fields(line)
			if len(fields) < 2 || fields[0] != "ii" {
				continue
			}
			// Record both "libc6:amd64" and "libc6" so a query in either
			// spelling resolves.
			installed[fields[1]] = struct{}{}
			name, _, _ := strings.Cut(fields[1], ":")
			installed[name] = struct{}{}
		}
	case PMApk:
		out, err := procutil.Capture(ctx, "apk", []string{"info"})
		if err != nil {
			return nil, err
		}
		for _, name := range splitLines(out) {
			installed[strings.TrimSpace(name)] = struct{}{}
		}
	case PMUnsupported:
		return nil, errors.New("cannot inspect installed packages for this distribution")
	default:
		return nil, errors.New("cannot inspect installed packages for this distribution")
	}
	return missingFromInstalled(packages, installed), nil
}

func missingFromInstalled(packages []string, installed map[string]struct{}) []string {
	var missing []string
	for _, pkg := range packages {
		if _, ok := installed[pkg]; !ok {
			missing = append(missing, pkg)
		}
	}
	return missing
}

// Remove purges build-only packages after a successful build.
func (pm PackageManager) Remove(ctx context.Context, packages []string) error {
	if len(packages) == 0 {
		return nil
	}
	switch pm {
	case PMApt:
		args := append([]string{"purge", "-qqy"}, packages...)
		return procutil.Run(ctx, "apt-get", args, ".", "apt-get purge")
	case PMApk:
		args := append([]string{"del", "--purge"}, packages...)
		return procutil.Run(ctx, "apk", args, ".", "apk del")
	// Unlike Install/Missing, removal is best-effort cleanup: nothing was ever
	// installed on a distro without a supported package manager, so this is a
	// deliberate no-op (pinned by TestUnsupportedRemoveIsNoop).
	case PMUnsupported:
		return nil
	default:
		return nil
	}
}

// ResolveRuntimePackages turns a mixed list of concrete names and IPE regex
// patterns into concrete package names available on the current distro.
func (pm PackageManager) ResolveRuntimePackages(ctx context.Context, entries []string) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, entry := range entries {
		if !IsPattern(entry) {
			pushUnique(&out, seen, entry)
			continue
		}
		names, err := pm.matchPattern(ctx, entry)
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

// AvailableBuildPackages filters build-only packages down to those the target
// distro can actually install.
//
// The catalog is flat, but upstream picks some packages per distro release --
// `libenchant-2-dev` on Debian >= 11 and `libenchant-dev` below it, `enchant2-dev`
// rather than `enchant-dev` on Alpine. Both names end up in one list, and because
// apt/apk install atomically the one that does not exist fails the whole batch,
// taking every other build dependency with it.
//
// Selectability is decided by a single DRY RUN (`apt-get install -s` / `apk add
// --simulate`), the same approach docker-php-extension-installer uses. That is
// one subprocess for the whole list instead of one probe per package, and it
// defers to the package manager's own resolver, so virtual packages (`libxslt-dev`
// is provided by `libxslt1-dev`) are handled without any name heuristics.
//
// If the dry run cannot single out the offending names, every package is KEPT:
// silently stripping a real build dependency would turn a clear "package not
// found" into a confusing compile error further along.
func (pm PackageManager) AvailableBuildPackages(ctx context.Context, packages []string) []string {
	if len(packages) == 0 {
		return nil
	}

	var candidates []string
	seen := make(map[string]struct{})
	for _, name := range packages {
		if IsPattern(name) {
			// A pattern cannot be handed to apt/apk literally; resolve it the
			// same way runtime packages are.
			if names, err := pm.matchPattern(ctx, name); err == nil {
				for _, n := range names {
					pushUnique(&candidates, seen, n)
				}
			}
			continue
		}
		pushUnique(&candidates, seen, name)
	}
	if len(candidates) == 0 {
		return nil
	}

	unselectable := pm.unselectable(ctx, candidates)
	if len(unselectable) == 0 {
		return candidates
	}

	var out []string
	for _, name := range candidates {
		if _, bad := unselectable[name]; bad {
			fmt.Fprintf(os.Stderr,
				"  note: build package `%s` is not available on this distro; skipping\n", name)
			continue
		}
		out = append(out, name)
	}
	return out
}

// unselectable returns the subset of packages the package manager cannot select,
// as reported by a dry-run install. An empty result means "install them all" --
// either everything is selectable, or the dry run itself failed and the caller
// should not start dropping packages on a guess.
func (pm PackageManager) unselectable(ctx context.Context, packages []string) map[string]struct{} {
	bad := make(map[string]struct{})

	var args []string
	switch pm {
	case PMApt:
		args = append([]string{"install", "-s", "-y", "--no-install-recommends"}, packages...)
	case PMApk:
		args = append([]string{"add", "--simulate"}, packages...)
	case PMUnsupported:
		return bad
	}

	program := "apt-get"
	if pm == PMApk {
		program = "apk"
	}
	_, err := procutil.Capture(ctx, program, args)
	if err == nil {
		// Every package is selectable.
		return bad
	}
	// apt and apk both report unselectable packages on stderr, which Capture
	// folds into the error message (it discards stdout on a non-zero exit).
	//   apt: `E: Unable to locate package <name>`
	//        `E: Package '<name>' has no installation candidate`
	//   apk: `  <name> (no such package):`
	diagnostic := err.Error()
	for _, name := range packages {
		if strings.Contains(diagnostic, "Unable to locate package "+name) ||
			strings.Contains(diagnostic, "Package '"+name+"' has no installation candidate") ||
			strings.Contains(diagnostic, name+" (no such package)") {
			bad[name] = struct{}{}
		}
	}
	return bad
}

func (pm PackageManager) matchPattern(ctx context.Context, pattern string) ([]string, error) {
	var candidates []string
	switch pm {
	case PMApt:
		out, err := procutil.Capture(ctx, "apt-cache", []string{"search", "--names-only", pattern})
		if err != nil {
			return nil, err
		}
		for _, l := range splitLines(out) {
			if tok := strings.Fields(l); len(tok) > 0 {
				candidates = append(candidates, tok[0])
			}
		}
	case PMApk:
		// Mirror the apt branch: a failed search must not look like "no
		// packages matched", which would silently drop a runtime dependency.
		out, err := procutil.Capture(ctx, "apk", []string{"search", "--quiet"})
		if err != nil {
			return nil, err
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
