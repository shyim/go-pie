package install

import "testing"

func TestParsesAManagedBlock(t *testing.T) {
	ini := "\n; gpie automatically added this to enable the phpredis/phpredis extension\n" +
		"; priority=20\n; version=6.1.0\nextension=redis\n"
	parsed := parseMarkers(ini, "/etc/php/conf.d/20-redis.ini")
	if len(parsed) != 1 {
		t.Fatalf("want 1 result, got %d", len(parsed))
	}
	if parsed[0].PackageName != "phpredis/phpredis" {
		t.Errorf("PackageName = %q", parsed[0].PackageName)
	}
	if parsed[0].ExtensionName != "redis" {
		t.Errorf("ExtensionName = %q", parsed[0].ExtensionName)
	}
	if parsed[0].Priority != 20 {
		t.Errorf("Priority = %d", parsed[0].Priority)
	}
	if parsed[0].Version == nil || *parsed[0].Version != "6.1.0" {
		t.Errorf("Version = %v", parsed[0].Version)
	}
}

func TestVersionIsNoneForLegacyMarkers(t *testing.T) {
	ini := "; gpie automatically added this to enable the phpredis/phpredis extension\n" +
		"; priority=20\nextension=redis\n"
	parsed := parseMarkers(ini, "x.ini")
	if len(parsed) != 1 {
		t.Fatalf("want 1 result, got %d", len(parsed))
	}
	if parsed[0].Version != nil {
		t.Errorf("Version = %v, want nil", parsed[0].Version)
	}
}

func TestParsesZendExtensionBlock(t *testing.T) {
	ini := "; gpie automatically added this to enable the xdebug/xdebug extension\n" +
		"; priority=95\nzend_extension=xdebug\n"
	parsed := parseMarkers(ini, "x.ini")
	if len(parsed) != 1 {
		t.Fatalf("want 1 result, got %d", len(parsed))
	}
	if parsed[0].ExtensionName != "xdebug" {
		t.Errorf("ExtensionName = %q", parsed[0].ExtensionName)
	}
	if parsed[0].Priority != 95 {
		t.Errorf("Priority = %d", parsed[0].Priority)
	}
}

func TestIgnoresUnmarkedIni(t *testing.T) {
	ini := "; some hand-written config\nextension=gd\n"
	if got := parseMarkers(ini, "x.ini"); len(got) != 0 {
		t.Errorf("want 0 results, got %d", len(got))
	}
}

func TestDefaultsPriorityWhenAbsent(t *testing.T) {
	ini := "; gpie automatically added this to enable the vendor/ext extension\nextension=ext\n"
	parsed := parseMarkers(ini, "x.ini")
	if len(parsed) != 1 {
		t.Fatalf("want 1 result, got %d", len(parsed))
	}
	if parsed[0].Priority != 80 {
		t.Errorf("Priority = %d, want 80", parsed[0].Priority)
	}
}

func TestDefaultsPriorityWhenUnparsable(t *testing.T) {
	ini := "; gpie automatically added this to enable the vendor/ext extension\n" +
		"; priority=-5\nextension=ext\n"
	parsed := parseMarkers(ini, "x.ini")
	if len(parsed) != 1 {
		t.Fatalf("want 1 result, got %d", len(parsed))
	}
	if parsed[0].Priority != 80 {
		t.Errorf("Priority = %d, want 80", parsed[0].Priority)
	}
}

func TestRemovesOnlyTheTargetedBlock(t *testing.T) {
	ini := "; hand written\nextension=gd\n" +
		"\n; gpie automatically added this to enable the phpredis/phpredis extension\n" +
		"; priority=20\n; version=6.1.0\nextension=redis\n" +
		"\n; gpie automatically added this to enable the xdebug/xdebug extension\n" +
		"; priority=95\n; version=3.4.0\nzend_extension=xdebug\n"
	out := RemoveMarkerBlock(ini, "redis")
	if contains(out, "phpredis/phpredis") {
		t.Errorf("output still contains phpredis/phpredis:\n%s", out)
	}
	if contains(out, "extension=redis") {
		t.Errorf("output still contains extension=redis:\n%s", out)
	}
	if contains(out, "version=6.1.0") {
		t.Errorf("output still contains version=6.1.0:\n%s", out)
	}
	if !contains(out, "zend_extension=xdebug") {
		t.Errorf("output missing zend_extension=xdebug:\n%s", out)
	}
	if !contains(out, "version=3.4.0") {
		t.Errorf("output missing version=3.4.0:\n%s", out)
	}
	if !contains(out, "extension=gd") {
		t.Errorf("output missing extension=gd:\n%s", out)
	}
	remaining := parseMarkers(out, "x.ini")
	if len(remaining) != 1 {
		t.Fatalf("want 1 remaining, got %d", len(remaining))
	}
	if remaining[0].ExtensionName != "xdebug" {
		t.Errorf("remaining ExtensionName = %q", remaining[0].ExtensionName)
	}
	if remaining[0].Version == nil || *remaining[0].Version != "3.4.0" {
		t.Errorf("remaining Version = %v", remaining[0].Version)
	}
}

func TestDedicatedFileBecomesEffectivelyEmptyAfterRemoval(t *testing.T) {
	ini := "\n; gpie automatically added this to enable the phpredis/phpredis extension\n" +
		"; priority=20\nextension=redis\n"
	out := RemoveMarkerBlock(ini, "redis")
	if !IsEffectivelyEmpty(out) {
		t.Errorf("leftover: %q", out)
	}
}

func TestRemovalOfAbsentExtensionIsNoop(t *testing.T) {
	ini := "; gpie automatically added this to enable the vendor/foo extension\nextension=foo\n"
	out := RemoveMarkerBlock(ini, "notpresent")
	if !contains(out, "extension=foo") {
		t.Errorf("output missing extension=foo:\n%s", out)
	}
}

func TestEffectivelyEmptyDetectsContent(t *testing.T) {
	if !IsEffectivelyEmpty("\n; only comments\n\n") {
		t.Error("comments-only should be effectively empty")
	}
	if IsEffectivelyEmpty("; comment\nextension=gd\n") {
		t.Error("file with directive should not be effectively empty")
	}
}

func TestSplitIniDirListPreservesWindowsDriveLetters(t *testing.T) {
	got := splitIniDirList(`C:\php\conf.d;D:\shared\php`, ';')
	if len(got) != 2 || got[0] != `C:\php\conf.d` || got[1] != `D:\shared\php` {
		t.Fatalf("splitIniDirList = %q", got)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// An orphaned marker (no directive of its own) must not absorb the next
// block's directive, and removing the second block must leave the first alone.
func TestOrphanedMarkerDoesNotAbsorbNextBlock(t *testing.T) {
	ini := "; gpie automatically added this to enable the orphan/orphan extension\n" +
		"; gpie automatically added this to enable the xdebug/xdebug extension\n" +
		"; priority=95\nzend_extension=xdebug\n"

	parsed := parseMarkers(ini, "x.ini")
	for _, m := range parsed {
		if m.PackageName == "orphan/orphan" && m.ExtensionName == "xdebug" {
			t.Fatal("orphaned marker absorbed the following block's directive")
		}
	}

	out := RemoveMarkerBlock(ini, "xdebug")
	if contains(out, "zend_extension=xdebug") {
		t.Errorf("xdebug block was not removed:\n%s", out)
	}
	if !contains(out, "orphan/orphan") {
		t.Errorf("removal swallowed the preceding orphaned marker:\n%s", out)
	}
}
