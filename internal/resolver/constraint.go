package resolver

import (
	"math"
	"strconv"
	"strings"
)

type semVer [3]uint64

func (a semVer) cmp(b semVer) int {
	for i := 0; i < 3; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}

// VersionIsNewer reports whether version a is strictly newer than b, ignoring
// pre-release/build metadata and a leading v.
func VersionIsNewer(a, b string) bool {
	return parseVersion(a).cmp(parseVersion(b)) > 0
}

// ConstraintMatches reports whether version satisfies constraint.
func ConstraintMatches(constraint, version string) bool {
	constraint = strings.TrimSpace(constraint)
	if constraint == "" || constraint == "*" {
		return true
	}
	for _, orPart := range strings.Split(constraint, "||") {
		for _, alt := range strings.Split(orPart, "|") {
			if matchesAndGroup(strings.TrimSpace(alt), version) {
				return true
			}
		}
	}
	return false
}

func matchesAndGroup(group, version string) bool {
	if lo, hi, ok := splitHyphenRange(group); ok {
		v := parseVersion(version)
		return v.cmp(parseVersion(lo)) >= 0 && v.cmp(hyphenUpper(hi)) <= 0
	}
	for _, term := range constraintTerms(group) {
		if !matchesSingle(term, version) {
			return false
		}
	}
	return true
}

func constraintTerms(group string) []string {
	raw := make([]string, 0)
	for _, tok := range strings.FieldsFunc(group, func(r rune) bool {
		return r == ' ' || r == ','
	}) {
		tok = strings.TrimSpace(tok)
		if tok != "" {
			raw = append(raw, tok)
		}
	}

	out := make([]string, 0, len(raw))
	i := 0
	for i < len(raw) {
		t := raw[i]
		if isLoneOperator(t) && i+1 < len(raw) {
			out = append(out, t+raw[i+1])
			i += 2
		} else {
			out = append(out, t)
			i++
		}
	}
	return out
}

func isLoneOperator(t string) bool {
	switch t {
	case ">=", "<=", "!=", ">", "<", "=", "==":
		return true
	}
	return false
}

func matchesSingle(term, version string) bool {
	v := parseVersion(version)

	if rest, ok := strings.CutPrefix(term, ">="); ok {
		return v.cmp(parseVersion(strings.TrimSpace(rest))) >= 0
	}
	if rest, ok := strings.CutPrefix(term, "<="); ok {
		return v.cmp(parseVersion(strings.TrimSpace(rest))) <= 0
	}
	if rest, ok := strings.CutPrefix(term, "!="); ok {
		return v.cmp(parseVersion(strings.TrimSpace(rest))) != 0
	}
	if rest, ok := strings.CutPrefix(term, "=="); ok {
		term = rest
	} else if rest, ok := strings.CutPrefix(term, "="); ok {
		term = rest
	}
	if rest, ok := strings.CutPrefix(term, ">"); ok {
		return v.cmp(parseVersion(strings.TrimSpace(rest))) > 0
	}
	if rest, ok := strings.CutPrefix(term, "<"); ok {
		return v.cmp(parseVersion(strings.TrimSpace(rest))) < 0
	}
	if rest, ok := strings.CutPrefix(term, "^"); ok {
		base := parseVersion(strings.TrimSpace(rest))
		return v.cmp(base) >= 0 && caretUpperOK(base, v)
	}
	if rest, ok := strings.CutPrefix(term, "~"); ok {
		return matchesTilde(strings.TrimSpace(rest), v)
	}

	if prefix, ok := strings.CutSuffix(term, ".*"); ok {
		return versionHasPrefix(version, prefix)
	}
	if term == "*" {
		return true
	}

	normalized := stripBuild(version)
	termCore := stripBuild(strings.TrimLeft(term, "v"))
	return normalized == termCore || versionHasPrefix(version, termCore)
}

func matchesTilde(baseStr string, v semVer) bool {
	base := parseVersion(baseStr)
	if v.cmp(base) < 0 {
		return false
	}
	components := len(strings.Split(stripBuild(strings.TrimLeft(baseStr, "v")), "."))
	if components >= 3 {
		return v[0] == base[0] && v[1] == base[1]
	}
	return v[0] == base[0]
}

func caretUpperOK(base, v semVer) bool {
	if base[0] > 0 {
		return v[0] == base[0]
	}
	if base[1] > 0 {
		return v[0] == 0 && v[1] == base[1]
	}
	return v[0] == 0 && v[1] == 0
}

func versionHasPrefix(version, prefix string) bool {
	core := stripBuild(version)
	return core == prefix || strings.HasPrefix(core, prefix+".")
}

func splitHyphenRange(group string) (string, string, bool) {
	lo, hi, ok := strings.Cut(group, " - ")
	if !ok {
		return "", "", false
	}
	return strings.TrimSpace(lo), strings.TrimSpace(hi), true
}

func hyphenUpper(hi string) semVer {
	components := len(strings.Split(stripBuild(strings.TrimLeft(hi, "v")), "."))
	p := parseVersion(hi)
	switch components {
	case 1:
		return semVer{p[0], math.MaxUint64, math.MaxUint64}
	case 2:
		return semVer{p[0], p[1], math.MaxUint64}
	default:
		return semVer{p[0], p[1], p[2]}
	}
}

func stripBuild(v string) string {
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		return v[:i]
	}
	return v
}

func parseVersion(v string) semVer {
	core := stripBuild(strings.TrimLeft(strings.TrimSpace(v), "v"))
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
