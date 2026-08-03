package resolver

import "testing"

func TestVersionIsNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"6.1.0", "6.0.2", true},
		{"6.0.2", "6.0.1", true},
		{"2.0.0", "1.9.9", true},
		{"6.0.2", "6.0.2", false},
		{"6.0.1", "6.1.0", false},
		{"v1.2.3", "1.2.3", false},
	}
	for _, c := range cases {
		if got := VersionIsNewer(c.a, c.b); got != c.want {
			t.Errorf("VersionIsNewer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestConstraintMatches(t *testing.T) {
	cases := []struct {
		constraint, version string
		want                bool
	}{
		{">= 7.4.0", "8.4.22", true},
		{">= 8.5", "8.4.22", false},
		{">= 7.4 < 8.6", "8.4.0", true},
		{">= 7.4 < 8.6", "8.6.0", false},
		{"= 8.4.0", "8.4.0", true},
		{"!= 8.3", "8.4.0", true},
		{"*", "8.4.1", true},
		{"", "8.4.1", true},
		{"8.1.*", "8.1.30", true},
		{"8.1.*", "8.2.0", false},
		{"8.*", "8.4.0", true},
		{"8.*", "9.0.0", false},
		{">=7.4", "8.4.0", true},
		{">=8.5", "8.4.0", false},
		{">7.4", "8.0.0", true},
		{"<=8.4", "8.4.0", true},
		{"<8.5", "8.4.9", true},
		{"<8.4", "8.4.0", false},
		{"!=8.3", "8.4.0", true},
		{"!=8.4.0", "8.4.0", false},
		{"^7.4", "7.9.0", true},
		{"^7.4", "8.0.0", false},
		{"~1.2", "1.9.0", true},
		{"~1.2", "2.0.0", false},
		{"~1.2.3", "1.2.9", true},
		{"~1.2.3", "1.3.0", false},
		{">=7.4 <8.4", "8.3.0", true},
		{">=7.4 <8.4", "8.4.0", false},
		{">=7.4,<9", "8.0.0", true},
		{"8.1.*||8.2.*||8.3.*||8.4.*||8.5.*", "8.4.3", true},
		{"8.1.*||8.2.*||8.3.*||8.4.*||8.5.*", "8.1.0", true},
		{"8.1.*||8.2.*||8.3.*||8.4.*||8.5.*", "8.0.30", false},
		{"8.1.*||8.2.*||8.3.*||8.4.*||8.5.*", "7.4.0", false},
		{"7.4 - 8.3", "8.0.0", true},
		{"7.4 - 8.3", "8.3.99", true},
		{"7.4 - 8.3", "8.4.0", false},
		{"7.4 - 8.3", "7.3.0", false},
		{"8.4.3", "8.4.3", true},
		{"8.1", "8.1.30", true},
		{"8.1", "8.10.0", false},
		{">=7.4,^8.0", "8.2.0", true},
	}
	for _, c := range cases {
		if got := ConstraintMatches(c.constraint, c.version); got != c.want {
			t.Errorf("ConstraintMatches(%q, %q) = %v, want %v", c.constraint, c.version, got, c.want)
		}
	}
}

func TestCaretZeroZeroPinsPatch(t *testing.T) {
	if !ConstraintMatches("^0.0.3", "0.0.3") {
		t.Error("^0.0.3 should match 0.0.3")
	}
	if ConstraintMatches("^0.0.3", "0.0.4") {
		t.Error("^0.0.3 must not match 0.0.4: every 0.0.x release may break")
	}
}

func TestConstraintEdgeCases(t *testing.T) {
	cases := []struct {
		constraint, version string
		want                bool
	}{
		// An empty OR alternative must not turn into "match anything".
		{"8.1.*||", "5.0.0", false},
		{"||8.2.*", "5.0.0", false},
		{"8.1.*||8.2.*", "8.2.7", true},
		// != is the complement of the prefix-based equality form.
		{"=8.3", "8.3.1", true},
		{"!=8.3", "8.3.1", false},
		{"!=8.3", "8.4.0", true},
		// A leading "v" normalises on the equality path, as it does for operators.
		{"1.2.3", "v1.2.3", true},
		{">=1.2.3", "v1.2.3", true},
	}
	for _, c := range cases {
		if got := ConstraintMatches(c.constraint, c.version); got != c.want {
			t.Errorf("ConstraintMatches(%q, %q) = %v, want %v", c.constraint, c.version, got, c.want)
		}
	}
}
