package platform

import "testing"

func TestLooksLikeVersionSelector(t *testing.T) {
	for _, s := range []string{"8", "8.3", "8.3.2"} {
		if !looksLikeVersionSelector(s) {
			t.Errorf("%q should be a version selector", s)
		}
	}
	for _, s := range []string{"", "/usr/bin/php", "php8.3", "8.", ".8", "8..3", "C:\\php\\php.exe"} {
		if looksLikeVersionSelector(s) {
			t.Errorf("%q should not be a version selector", s)
		}
	}
}

func TestVersionSelectorMatches(t *testing.T) {
	v := PhpVersion{8, 3, 2}
	for _, s := range []string{"8", "8.3", "8.3.2"} {
		if !versionSelectorMatches(s, v) {
			t.Errorf("%q should match %s", s, v)
		}
	}
	for _, s := range []string{"7", "8.4", "8.3.1", "8.3.2.1"} {
		if versionSelectorMatches(s, v) {
			t.Errorf("%q should not match %s", s, v)
		}
	}
}

func TestVersionSelectorRejectsOutOfRangeComponent(t *testing.T) {
	if versionSelectorMatches("999", PhpVersion{8, 3, 2}) {
		t.Error("out-of-range component should not match")
	}
}

func TestDefaultPhpPrefersSystem(t *testing.T) {
	if got := DefaultPhp(nil); got != nil {
		t.Errorf("empty list should yield nil, got %v", got)
	}

	list := []DiscoveredPhp{
		{Path: "/opt/php81", Version: PhpVersion{8, 1, 0}},
		{Path: "/usr/bin/php", Version: PhpVersion{8, 2, 0}, IsSystem: true},
		{Path: "/opt/php84", Version: PhpVersion{8, 4, 0}},
	}
	if got := DefaultPhp(list); got.Path != "/usr/bin/php" {
		t.Errorf("expected the system PHP, got %s", got.Path)
	}

	// Without a system PHP the newest (last, since the list is sorted
	// ascending) installation wins.
	noSystem := []DiscoveredPhp{
		{Path: "/opt/php81", Version: PhpVersion{8, 1, 0}},
		{Path: "/opt/php84", Version: PhpVersion{8, 4, 0}},
	}
	if got := DefaultPhp(noSystem); got.Path != "/opt/php84" {
		t.Errorf("expected the newest PHP, got %s", got.Path)
	}
}

func TestClampVersionPart(t *testing.T) {
	if got := clampVersionPart(-1); got != 0 {
		t.Errorf("negative should clamp to 0, got %d", got)
	}
	if got := clampVersionPart(8); got != 8 {
		t.Errorf("in-range should pass through, got %d", got)
	}
	if got := clampVersionPart(4096); got != 255 {
		t.Errorf("over-range should clamp to 255, got %d", got)
	}
}
