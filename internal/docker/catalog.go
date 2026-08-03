package docker

import (
	_ "embed"
	"encoding/json"
	"sync"
)

// SystemDeps holds one extension's system-dependency needs on one distro family.
type SystemDeps struct {
	Persistent []string `json:"persistent"`
	BuildOnly  []string `json:"build_only"`
}

type perDistro struct {
	Alpine *SystemDeps `json:"alpine"`
	Debian *SystemDeps `json:"debian"`
}

type libraryDef struct {
	Alpine *string `json:"alpine"`
	Debian *string `json:"debian"`
}

type catalogData struct {
	Libraries  map[string]json.RawMessage `json:"libraries"`
	Extensions map[string]perDistro       `json:"extensions"`
}

//go:embed system-deps.json
var rawSystemDeps []byte

var (
	catalogOnce  sync.Once
	catalogValue *catalogData
)

func catalog() *catalogData {
	catalogOnce.Do(func() {
		var c catalogData
		if err := json.Unmarshal(rawSystemDeps, &c); err != nil {
			panic("embedded system-deps.json must be valid")
		}
		catalogValue = &c
	})
	return catalogValue
}

// lookup returns the catalog system deps for extensionName on the given family
// (the fallback when Packagist declares nothing).
func lookup(extensionName string, family DistroFamily) *SystemDeps {
	per, ok := catalog().Extensions[extensionName]
	if !ok {
		return nil
	}
	switch family {
	case FamilyAlpine:
		return cloneDeps(per.Alpine)
	case FamilyDebian:
		return cloneDeps(per.Debian)
	case FamilyOther:
		return nil
	default:
		return nil
	}
}

// lookupLibrary maps a lib-<name> requirement (prefix already stripped) to its
// distro build package, if known.
func lookupLibrary(libName string, family DistroFamily) (string, bool) {
	value, ok := catalog().Libraries[libName]
	if !ok {
		return "", false
	}
	var def libraryDef
	if err := json.Unmarshal(value, &def); err != nil {
		return "", false
	}
	switch family {
	case FamilyAlpine:
		if def.Alpine != nil {
			return *def.Alpine, true
		}
		return "", false
	case FamilyDebian:
		if def.Debian != nil {
			return *def.Debian, true
		}
		return "", false
	case FamilyOther:
		return "", false
	default:
		return "", false
	}
}

func cloneDeps(d *SystemDeps) *SystemDeps {
	if d == nil {
		return nil
	}
	out := SystemDeps{}
	if d.Persistent != nil {
		out.Persistent = append([]string(nil), d.Persistent...)
	}
	if d.BuildOnly != nil {
		out.BuildOnly = append([]string(nil), d.BuildOnly...)
	}
	return &out
}
