package download

import "testing"

func TestParsesGithubRepoForms(t *testing.T) {
	cases := []struct {
		url  string
		want string
		ok   bool
	}{
		{"https://github.com/phpredis/phpredis.git", "phpredis/phpredis", true},
		{"https://github.com/phpredis/phpredis", "phpredis/phpredis", true},
		{"git@github.com:asgrim/example-pie-extension.git", "asgrim/example-pie-extension", true},
		{"https://api.github.com/repos/asgrim/example-pie-extension/zipball/abc123", "asgrim/example-pie-extension", true},
		{"https://gitlab.com/foo/bar", "", false},
	}
	for _, c := range cases {
		got, ok := parseGithubRepo(c.url)
		if ok != c.ok || got != c.want {
			t.Fatalf("parseGithubRepo(%q) = (%q, %v), want (%q, %v)", c.url, got, ok, c.want, c.ok)
		}
	}
}

func TestDebugOptionString(t *testing.T) {
	if got := debugOptionString(nil); got != "None" {
		t.Fatalf("nil: got %q", got)
	}
	s := "https://example.com"
	if got := debugOptionString(&s); got != `Some("https://example.com")` {
		t.Fatalf("some: got %q", got)
	}
}
