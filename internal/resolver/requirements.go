package resolver

import (
	"sort"
	"strings"

	"github.com/shyim/go-pie/internal/platform"
)

type RequirementKindType int

const (
	KindPhp RequirementKindType = iota
	KindExt
	KindLib
	KindOther
)

type RequirementKind struct {
	Type RequirementKindType
	Name string
}

type SatisfactionState int

const (
	Satisfied SatisfactionState = iota
	VersionMismatch
	Missing
	Unknown
)

type Satisfaction struct {
	State     SatisfactionState
	Installed string
}

type RequirementStatus struct {
	Name         string
	Constraint   string
	Kind         RequirementKind
	Satisfaction Satisfaction
}

func (r *RequirementStatus) IsBlocking() bool {
	if r.Kind.Type != KindPhp && r.Kind.Type != KindExt {
		return false
	}
	return r.Satisfaction.State == Missing || r.Satisfaction.State == VersionMismatch
}

func classify(name string) RequirementKind {
	if name == "php" {
		return RequirementKind{Type: KindPhp}
	}
	if rest, ok := strings.CutPrefix(name, "ext-"); ok {
		return RequirementKind{Type: KindExt, Name: strings.ToLower(rest)}
	}
	if rest, ok := strings.CutPrefix(name, "lib-"); ok {
		return RequirementKind{Type: KindLib, Name: strings.ToLower(rest)}
	}
	return RequirementKind{Type: KindOther, Name: name}
}

func CheckRequirements(requires map[string]string, plat *platform.TargetPlatform,
	loaded []platform.ExtensionVersion) []RequirementStatus {
	phpVersion := plat.PHP.Version.String()

	names := make([]string, 0, len(requires))
	for k := range requires {
		names = append(names, k)
	}
	sort.Strings(names)

	out := make([]RequirementStatus, 0, len(names))
	for _, name := range names {
		constraintStr := requires[name]
		kind := classify(name)
		var sat Satisfaction

		switch kind.Type {
		case KindPhp:
			if ConstraintMatches(constraintStr, phpVersion) {
				sat = Satisfaction{State: Satisfied}
			} else {
				sat = Satisfaction{State: VersionMismatch, Installed: phpVersion}
			}
		case KindExt:
			found := false
			for _, ev := range loaded {
				if ev.Name == kind.Name {
					found = true
					if ev.Version == "" || ConstraintMatches(constraintStr, ev.Version) {
						sat = Satisfaction{State: Satisfied}
					} else {
						sat = Satisfaction{State: VersionMismatch, Installed: ev.Version}
					}
					break
				}
			}
			if !found {
				sat = Satisfaction{State: Missing}
			}
		default:
			sat = Satisfaction{State: Unknown}
		}

		out = append(out, RequirementStatus{
			Name:         name,
			Constraint:   constraintStr,
			Kind:         kind,
			Satisfaction: sat,
		})
	}
	return out
}
