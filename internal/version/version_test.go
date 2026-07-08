package version

import "testing"

func TestConstants(t *testing.T) {
	if Version != "0.1.0" {
		t.Errorf("Version = %q, want %q", Version, "0.1.0")
	}
	if UserAgent != "rpie/0.1.0" {
		t.Errorf("UserAgent = %q, want %q", UserAgent, "rpie/0.1.0")
	}
	if PackagistUserAgent != "rpie/0.1.0 (+https://github.com/shyim/go-pie)" {
		t.Errorf("PackagistUserAgent = %q, want %q", PackagistUserAgent, "rpie/0.1.0 (+https://github.com/shyim/go-pie)")
	}
}
