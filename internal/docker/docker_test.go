package docker

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func debianFixture() *Distro {
	return &Distro{ID: "debian", VersionID: "12", Family: FamilyDebian}
}

//nolint:modernize // Keep this compatible with golangci-lint's current Go stdlib loader.
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

// Extensions that upstream declares in a SHARED case arm (`pgsql@debian |
// pdo_pgsql@debian | pq@debian)`) were silently dropped by the catalog
// extractor, so `--install-system-deps` never installed their dev packages and
// the build failed with "Cannot find libpq-fe.h".
func TestSharedCaseArmExtensionsArePresent(t *testing.T) {
	for _, ext := range []string{"pgsql", "pdo_pgsql", "odbc", "pdo_odbc", "sodium", "oci8"} {
		for _, family := range []DistroFamily{FamilyDebian, FamilyAlpine} {
			if lookup(ext, family) == nil {
				t.Errorf("%s has no catalog entry for family %v", ext, family)
			}
		}
	}
	if deps := lookup("pgsql", FamilyDebian); deps != nil && !anyContains(deps.BuildOnly, "libpq") {
		t.Errorf("pgsql/debian build deps lack libpq: %v", deps.BuildOnly)
	}
}

// Where upstream picks a different package per distro release, the flat catalog
// must carry an anchored alternation rather than both literal names: apt and apk
// install atomically, so one name that does not exist on this release would fail
// the whole batch and take every other dependency with it.
func TestReleaseConditionalRuntimePackagesAreAPattern(t *testing.T) {
	deps := lookup("odbc", FamilyDebian)
	if deps == nil {
		t.Fatal("expected odbc debian deps")
	}
	var found bool
	for _, p := range deps.Persistent {
		if strings.Contains(p, "libodbc2") && strings.Contains(p, "libodbc1") {
			if !IsPattern(p) {
				t.Errorf("odbc runtime alternatives are not a pattern: %q", p)
			}
			found = true
		}
	}
	if !found {
		t.Errorf("expected a libodbc2/libodbc1 alternation in %v", deps.Persistent)
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
	got := pm.ResolveRuntimePackages(t.Context(), []string{"libzstd1", "icu-libs"})
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
	if err := PMUnsupported.Install(t.Context(), nil); err != nil {
		t.Errorf("empty install: %v", err)
	}
	if err := PMApt.Install(t.Context(), nil); err != nil {
		t.Errorf("empty apt install: %v", err)
	}
}

func TestUnsupportedInstallErrors(t *testing.T) {
	err := PMUnsupported.Install(t.Context(), []string{"foo"})
	if err == nil || err.Error() != "no supported system package manager for this distribution" {
		t.Errorf("unexpected: %v", err)
	}
}

func TestMissingFromInstalledPreservesOnlyNewPackages(t *testing.T) {
	installed := map[string]struct{}{
		"cmake":      {},
		"libzip-dev": {},
	}
	got := missingFromInstalled(
		[]string{"cmake", "autoconf", "libzip-dev", "pkg-config"},
		installed,
	)
	want := []string{"autoconf", "pkg-config"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("missingFromInstalled = %v, want %v", got, want)
	}
}

func TestUnsupportedRemoveIsNoop(t *testing.T) {
	if err := PMUnsupported.Remove(t.Context(), []string{"foo"}); err != nil {
		t.Errorf("unsupported remove should be nil: %v", err)
	}
	if err := PMApt.Remove(t.Context(), nil); err != nil {
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

// Real Debian package names contain '+' (libstdc++, g++). Widening IsPattern's
// metacharacter set to include '+' or '.' would misclassify them as regexes and
// break dependency resolution, so pin the current behaviour.
func TestIsPatternDoesNotMisclassifyPlusNames(t *testing.T) {
	for _, lit := range []string{"libstdc++", "g++", "libstdc++6", "libzip4"} {
		if IsPattern(lit) {
			t.Errorf("%q must be treated as a literal package name", lit)
		}
	}
	for _, pat := range []string{"^libicu[0-9]+$", "^libgeos-c1(v[0-9]*)?$"} {
		if !IsPattern(pat) {
			t.Errorf("%q must be treated as a pattern", pat)
		}
	}
}

func TestPatternMatchesAnchoredCatalogEntries(t *testing.T) {
	cases := []struct {
		pat, pkg string
		want     bool
	}{
		{"^libicu[0-9]+$", "libicu72", true},
		{"^libicu[0-9]+$", "libicu", false},
		{"^libicu[0-9]+$", "xlibicu72", false},
		{"^libgeos-c1(v[0-9]*)?$", "libgeos-c1v5", true},
		{"^libmagickcore-6.q16-[0-9]+-extra$", "libmagickcore-6.q16-6-extra", true},
	}
	for _, c := range cases {
		if got := patternMatches(c.pat, c.pkg); got != c.want {
			t.Errorf("patternMatches(%q, %q) = %v, want %v", c.pat, c.pkg, got, c.want)
		}
	}
}

// Extensions where upstream intentionally keeps the -dev package installed,
// because on that distro it is what ships the runtime shared library. Narrow and
// explicit so a genuine mistake in any other entry still fails the test.
var devPackageIsRuntimeUpstream = map[string]bool{
	"zmq":  true,
	"geos": true,
}

// The catalog is generated data that ships embedded in the binary, so a
// malformed entry cannot be caught at runtime without breaking an install.
func TestEmbeddedCatalogIsWellFormed(t *testing.T) {
	for ext, per := range catalog().Extensions {
		for fam, deps := range map[string]*SystemDeps{"alpine": per.Alpine, "debian": per.Debian} {
			if deps == nil {
				continue
			}
			for _, entry := range deps.Persistent {
				if IsPattern(entry) {
					if _, err := regexp.Compile(entry); err != nil {
						t.Errorf("%s/%s: persistent pattern %q does not compile: %v", ext, fam, entry, err)
					}
					continue
				}
				// A -dev package left in persistent survives
				// --cleanup-build-deps, which is normally a packaging mistake.
				// A few upstream arms do it deliberately (zmq keeps
				// zeromq-dev/libzmq3-dev, geos keeps geos-dev) because that is
				// the package actually carrying the shared library there, and
				// the catalog mirrors upstream rather than second-guessing it.
				if strings.HasSuffix(entry, "-dev") && !devPackageIsRuntimeUpstream[ext] {
					t.Errorf("%s/%s: %q is a development package but is marked persistent", ext, fam, entry)
				}
			}
			// A package in both lists is removed by cleanup despite being needed.
			build := make(map[string]struct{}, len(deps.BuildOnly))
			for _, b := range deps.BuildOnly {
				build[b] = struct{}{}
			}
			for _, p := range deps.Persistent {
				if _, dup := build[p]; dup {
					t.Errorf("%s/%s: %q is both persistent and build-only", ext, fam, p)
				}
			}
		}
	}
}
