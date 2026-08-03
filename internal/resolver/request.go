package resolver

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type RequestedPackage struct {
	Name       string
	Constraint string // "" = none
}

func ParseRequest(input string) (*RequestedPackage, error) {
	var name, constraint string
	if n, c, ok := strings.Cut(input, ":"); ok {
		name = strings.TrimSpace(n)
		constraint = strings.TrimSpace(c)
	} else {
		name = strings.TrimSpace(input)
	}

	// strings.Split only counts separators, so "/", "/name", and "vendor/" all
	// yield two elements; require both segments to be non-empty.
	parts := strings.Split(name, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" ||
		strings.ContainsFunc(name, unicode.IsSpace) {
		return nil, fmt.Errorf("`%s` is not a valid vendor/name[:version] package specification", input)
	}

	return &RequestedPackage{Name: name, Constraint: constraint}, nil
}

// selectVersion returns the index into versions of the chosen version.
func selectVersion(versions []PackageVersion, constraint string) (int, error) {
	candidates := make([]int, 0, len(versions))
	for i := range versions {
		if isStable(versions[i].Version) && constraintMatchesOpt(constraint, versions[i].VersionNormalized) {
			candidates = append(candidates, i)
		}
	}

	if len(candidates) == 0 {
		for i := range versions {
			if constraintMatchesOpt(constraint, versions[i].VersionNormalized) {
				candidates = append(candidates, i)
			}
		}
	}

	sort.SliceStable(candidates, func(a, b int) bool {
		return compareVersions(versions[candidates[b]].VersionNormalized, versions[candidates[a]].VersionNormalized) < 0
	})

	if len(candidates) == 0 {
		return -1, errors.New("no matching version found")
	}
	return candidates[0], nil
}

func isStable(version string) bool {
	v := strings.ToLower(version)
	return !strings.HasPrefix(v, "dev-") &&
		!strings.Contains(v, "-dev") &&
		!strings.Contains(v, "alpha") &&
		!strings.Contains(v, "beta") &&
		!strings.Contains(v, "-rc") &&
		!strings.Contains(v, "rc-")
}

func constraintMatchesOpt(constraint, normalized string) bool {
	if constraint == "" {
		return true
	}
	return ConstraintMatches(constraint, normalized)
}

func parseSemver(v string) semVer {
	core := stripBuild(strings.TrimLeft(v, "v"))
	parts := strings.Split(core, ".")
	var out semVer
	for i := 0; i < 3 && i < len(parts); i++ {
		n, err := strconv.ParseUint(parts[i], 10, 64)
		if err != nil {
			n = 0
		}
		out[i] = n
	}
	return out
}

func compareVersions(a, b string) int {
	return parseSemver(a).cmp(parseSemver(b))
}
