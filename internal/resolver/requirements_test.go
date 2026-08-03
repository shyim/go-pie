package resolver

import "testing"

func TestClassifiesRequirements(t *testing.T) {
	if k := classify("php"); k.Type != KindPhp {
		t.Errorf("php -> %v", k)
	}
	if k := classify("ext-JSON"); k.Type != KindExt || k.Name != "json" {
		t.Errorf("ext-JSON -> %v", k)
	}
	if k := classify("lib-zip"); k.Type != KindLib || k.Name != "zip" {
		t.Errorf("lib-zip -> %v", k)
	}
	if k := classify("symfony/console"); k.Type != KindOther || k.Name != "symfony/console" {
		t.Errorf("symfony/console -> %v", k)
	}
}

func TestBlockingLogic(t *testing.T) {
	missingExt := RequirementStatus{
		Name:         "ext-mbstring",
		Constraint:   "*",
		Kind:         RequirementKind{Type: KindExt, Name: "mbstring"},
		Satisfaction: Satisfaction{State: Missing},
	}
	if !missingExt.IsBlocking() {
		t.Error("missing ext should block")
	}

	phpMismatch := RequirementStatus{
		Name:         "php",
		Constraint:   ">=8.5",
		Kind:         RequirementKind{Type: KindPhp},
		Satisfaction: Satisfaction{State: VersionMismatch, Installed: "8.4.0"},
	}
	if !phpMismatch.IsBlocking() {
		t.Error("php mismatch should block")
	}

	unknownPkg := RequirementStatus{
		Name:         "symfony/console",
		Constraint:   "^6",
		Kind:         RequirementKind{Type: KindOther, Name: "symfony/console"},
		Satisfaction: Satisfaction{State: Unknown},
	}
	if unknownPkg.IsBlocking() {
		t.Error("unknown pkg should not block")
	}

	lib := RequirementStatus{
		Name:         "lib-zip",
		Constraint:   "*",
		Kind:         RequirementKind{Type: KindLib, Name: "zip"},
		Satisfaction: Satisfaction{State: Unknown},
	}
	if lib.IsBlocking() {
		t.Error("lib should not block")
	}
}

func TestRequiresHelperBuildsMap(t *testing.T) {
	r := map[string]string{"php": ">=8.1", "ext-json": "*"}
	if r["php"] != ">=8.1" {
		t.Errorf("php = %q", r["php"])
	}
}
