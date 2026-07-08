package docker

import (
	"reflect"
	"testing"
)

func debianFixture() *Distro {
	return &Distro{ID: "debian", VersionID: "12", Family: FamilyDebian}
}

func contains(s []string, v string) bool {
	for _, e := range s {
		if e == v {
			return true
		}
	}
	return false
}

func anyContains(s []string, substr string) bool {
	for _, e := range s {
		if len(substr) == 0 {
			return true
		}
		if containsSub(e, substr) {
			return true
		}
	}
	return false
}

func containsSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// mod.rs tests

func TestPackagistLibRequiresTakePrecedence(t *testing.T) {
	r := ResolveSystemDeps("gd", []string{"zip", "jpeg"}, debianFixture())
	if r == nil {
		t.Fatal("expected non-nil")
	}
	if !r.FromPackagist {
		t.Error("expected FromPackagist true")
	}
	if !contains(r.Deps.BuildOnly, "libzip-dev") {
		t.Errorf("BuildOnly missing libzip-dev: %v", r.Deps.BuildOnly)
	}
	if !contains(r.Deps.BuildOnly, "libjpeg-dev") {
		t.Errorf("BuildOnly missing libjpeg-dev: %v", r.Deps.BuildOnly)
	}
	if len(r.Deps.Persistent) != 0 {
		t.Errorf("expected empty Persistent, got %v", r.Deps.Persistent)
	}
}

func TestFallsBackToCatalogWithoutLibRequires(t *testing.T) {
	r := ResolveSystemDeps("gd", nil, debianFixture())
	if r == nil {
		t.Fatal("expected non-nil")
	}
	if r.FromPackagist {
		t.Error("expected FromPackagist false")
	}
	if !anyContains(r.Deps.BuildOnly, "freetype") {
		t.Errorf("BuildOnly has no freetype: %v", r.Deps.BuildOnly)
	}
}

func TestUnknownExtensionWithoutLibsIsNone(t *testing.T) {
	if r := ResolveSystemDeps("totally-unknown-ext", nil, debianFixture()); r != nil {
		t.Errorf("expected nil, got %+v", r)
	}
}

func TestMergeDedupesAcrossExtensions(t *testing.T) {
	a := SystemDeps{
		Persistent: []string{"libicu72"},
		BuildOnly:  []string{"libicu-dev", "cmake"},
	}
	b := SystemDeps{
		Persistent: []string{"libicu72"},
		BuildOnly:  []string{"cmake", "libzip-dev"},
	}
	merged := MergeSystemDeps([]SystemDeps{a, b})
	if !reflect.DeepEqual(merged.Persistent, []string{"libicu72"}) {
		t.Errorf("Persistent = %v", merged.Persistent)
	}
	if !reflect.DeepEqual(merged.BuildOnly, []string{"libicu-dev", "cmake", "libzip-dev"}) {
		t.Errorf("BuildOnly = %v", merged.BuildOnly)
	}
}

// distro.rs tests

const (
	fixtureAlpine = "NAME=\"Alpine Linux\"\nID=alpine\nVERSION_ID=3.20.3\n"
	fixtureDebian = "PRETTY_NAME=\"Debian GNU/Linux 12\"\nID=debian\nVERSION_ID=\"12\"\n"
	fixtureUbuntu = "ID=ubuntu\nID_LIKE=debian\nVERSION_ID=\"24.04\"\n"
)

func TestParsesAlpine(t *testing.T) {
	if got := osReleaseField(fixtureAlpine, "ID"); got != "alpine" {
		t.Errorf("ID = %q", got)
	}
	if classify("alpine", "") != FamilyAlpine {
		t.Error("classify alpine")
	}
}

func TestParsesDebianAndUbuntu(t *testing.T) {
	if got := osReleaseField(fixtureDebian, "VERSION_ID"); got != "12" {
		t.Errorf("VERSION_ID = %q", got)
	}
	if classify("debian", "") != FamilyDebian {
		t.Error("classify debian")
	}
	if classify("ubuntu", "debian") != FamilyDebian {
		t.Error("classify ubuntu id_like debian")
	}
	if got := osReleaseField(fixtureUbuntu, "VERSION_ID"); got != "24.04" {
		t.Errorf("VERSION_ID = %q", got)
	}
}

func TestDistroLabelAndFamilyToken(t *testing.T) {
	d := &Distro{ID: "alpine", VersionID: "3.20.3", Family: FamilyAlpine}
	if d.Label() != "alpine@3.20.3" {
		t.Errorf("Label = %q", d.Label())
	}
	if d.FamilyToken() != "alpine" {
		t.Errorf("FamilyToken = %q", d.FamilyToken())
	}
	if (&Distro{Family: FamilyDebian}).FamilyToken() != "debian" {
		t.Error("debian token")
	}
	if (&Distro{Family: FamilyOther}).FamilyToken() != "other" {
		t.Error("other token")
	}
}

// catalog.rs tests

func TestEmbeddedCatalogParses(t *testing.T) {
	if catalog() == nil {
		t.Fatal("nil catalog")
	}
}

func TestLooksUpGdAlpine(t *testing.T) {
	deps := lookup("gd", FamilyAlpine)
	if deps == nil {
		t.Fatal("expected gd alpine deps")
	}
	if !anyContains(deps.BuildOnly, "freetype") {
		t.Errorf("no freetype in %v", deps.BuildOnly)
	}
}

func TestUnknownExtensionIsNone(t *testing.T) {
	if lookup("definitely-not-real", FamilyDebian) != nil {
		t.Error("expected nil")
	}
}

func TestExpandedCatalogHasManyExtensions(t *testing.T) {
	if len(catalog().Extensions) < 80 {
		t.Fatalf("catalog too small: %d", len(catalog().Extensions))
	}
	if lookup("memcached", FamilyDebian) == nil {
		t.Error("memcached debian nil")
	}
	if lookup("mongodb", FamilyAlpine) == nil {
		t.Error("mongodb alpine nil")
	}
	if lookup("ldap", FamilyDebian) == nil {
		t.Error("ldap debian nil")
	}
}

func TestLooksUpLibraryMapping(t *testing.T) {
	if v, ok := lookupLibrary("zip", FamilyDebian); !ok || v != "libzip-dev" {
		t.Errorf("zip debian = %q,%v", v, ok)
	}
	if v, ok := lookupLibrary("jpeg", FamilyAlpine); !ok || v != "libjpeg-turbo-dev" {
		t.Errorf("jpeg alpine = %q,%v", v, ok)
	}
	if _, ok := lookupLibrary("_comment", FamilyDebian); ok {
		t.Error("_comment must not resolve")
	}
	if _, ok := lookupLibrary("nonexistent-lib", FamilyAlpine); ok {
		t.Error("nonexistent-lib must not resolve")
	}
}

func TestOtherFamilyIsNone(t *testing.T) {
	if lookup("gd", FamilyOther) != nil {
		t.Error("Other family must be nil for lookup")
	}
	if _, ok := lookupLibrary("zip", FamilyOther); ok {
		t.Error("Other family must be nil for lookupLibrary")
	}
}

// packages.rs tests

func TestDetectsPatternsVsConcrete(t *testing.T) {
	if !IsPattern("^librabbitmq[0-9]*$") {
		t.Error("librabbitmq pattern")
	}
	if !IsPattern("^libgeos-c1(v[0-9]*)?$") {
		t.Error("libgeos pattern")
	}
	if IsPattern("libzstd1") {
		t.Error("libzstd1 concrete")
	}
	if IsPattern("icu-libs") {
		t.Error("icu-libs concrete")
	}
}

func TestMatchesIpePatterns(t *testing.T) {
	cases := []struct {
		pattern, pkg string
		want         bool
	}{
		{"^librabbitmq[0-9]*$", "librabbitmq4", true},
		{"^librabbitmq[0-9]*$", "librabbitmq", true},
		{"^librabbitmq[0-9]*$", "librabbitmq-dev", false},
		{"^libpng[0-9]+-[0-9]+$", "libpng16-16", true},
		{"^libpng[0-9]+-[0-9]+$", "libpng-dev", false},
		{"^libgeos-c1(v[0-9]*)?$", "libgeos-c1", true},
		{"^libgeos-c1(v[0-9]*)?$", "libgeos-c1v5", true},
		{"^libgeos-c1(v[0-9]*)?$", "libgeos-dev", false},
	}
	for _, c := range cases {
		if got := patternMatches(c.pattern, c.pkg); got != c.want {
			t.Errorf("patternMatches(%q,%q) = %v, want %v", c.pattern, c.pkg, got, c.want)
		}
	}
}

func TestConcreteNamesPassThroughResolution(t *testing.T) {
	pm := PMUnsupported
	got := pm.ResolveRuntimePackages([]string{"libzstd1", "icu-libs"})
	if !reflect.DeepEqual(got, []string{"libzstd1", "icu-libs"}) {
		t.Errorf("resolved = %v", got)
	}
}

func TestPackageManagerForDistro(t *testing.T) {
	if PackageManagerForDistro(&Distro{Family: FamilyDebian}) != PMApt {
		t.Error("debian -> apt")
	}
	if PackageManagerForDistro(&Distro{Family: FamilyAlpine}) != PMApk {
		t.Error("alpine -> apk")
	}
	if PackageManagerForDistro(&Distro{Family: FamilyOther}) != PMUnsupported {
		t.Error("other -> unsupported")
	}
}

func TestPackageManagerString(t *testing.T) {
	if PMApt.String() != "Apt" || PMApk.String() != "Apk" || PMUnsupported.String() != "Unsupported" {
		t.Error("PackageManager String")
	}
}

func TestInstallEmptyIsNoop(t *testing.T) {
	if err := PMUnsupported.Install(nil); err != nil {
		t.Errorf("empty install: %v", err)
	}
	if err := PMApt.Install(nil); err != nil {
		t.Errorf("empty apt install: %v", err)
	}
}

func TestUnsupportedInstallErrors(t *testing.T) {
	err := PMUnsupported.Install([]string{"foo"})
	if err == nil || err.Error() != "no supported system package manager for this distribution" {
		t.Errorf("unexpected: %v", err)
	}
}

func TestUnsupportedRemoveIsNoop(t *testing.T) {
	if err := PMUnsupported.Remove([]string{"foo"}); err != nil {
		t.Errorf("unsupported remove should be nil: %v", err)
	}
	if err := PMApt.Remove(nil); err != nil {
		t.Errorf("empty remove: %v", err)
	}
}

// bundled.rs tests

func TestRecognisesBundledNames(t *testing.T) {
	yes := []string{"gd", "intl", "pdo_mysql"}
	no := []string{"phpredis/phpredis", "xdebug/xdebug:^3", ""}
	for _, s := range yes {
		if !LooksLikeBundledName(s) {
			t.Errorf("expected bundled: %q", s)
		}
	}
	for _, s := range no {
		if LooksLikeBundledName(s) {
			t.Errorf("expected not bundled: %q", s)
		}
	}
	if LooksLikeBundledName("pdo-mysql") {
		t.Error("hyphen is not allowed")
	}
}

func TestGdGetsImageFlags(t *testing.T) {
	if !contains(configureFlags("gd"), "--with-freetype") {
		t.Error("gd missing --with-freetype")
	}
	if len(configureFlags("intl")) != 0 {
		t.Error("intl should have no flags")
	}
}
