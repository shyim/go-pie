package style

import "testing"

func TestDisabledPassthrough(t *testing.T) {
	s := New(false)
	fns := []struct {
		name string
		fn   func(string) string
	}{
		{"Red", s.Red},
		{"RedBold", s.RedBold},
		{"Green", s.Green},
		{"Yellow", s.Yellow},
		{"Cyan", s.Cyan},
		{"BoldCyan", s.BoldCyan},
		{"Bold", s.Bold},
		{"Dim", s.Dim},
		{"BoldUnderlined", s.BoldUnderlined},
	}
	for _, f := range fns {
		if got := f.fn("hello"); got != "hello" {
			t.Errorf("%s disabled = %q, want %q", f.name, got, "hello")
		}
	}
}

func TestEnabledCodes(t *testing.T) {
	s := New(true)
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"Red", s.Red("x"), "\x1b[31mx\x1b[0m"},
		{"RedBold", s.RedBold("x"), "\x1b[1;31mx\x1b[0m"},
		{"Green", s.Green("x"), "\x1b[32mx\x1b[0m"},
		{"Yellow", s.Yellow("x"), "\x1b[33mx\x1b[0m"},
		{"Cyan", s.Cyan("x"), "\x1b[36mx\x1b[0m"},
		{"BoldCyan", s.BoldCyan("x"), "\x1b[1;36mx\x1b[0m"},
		{"Bold", s.Bold("x"), "\x1b[1mx\x1b[0m"},
		{"Dim", s.Dim("x"), "\x1b[2mx\x1b[0m"},
		{"BoldUnderlined", s.BoldUnderlined("x"), "\x1b[1;4mx\x1b[0m"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s enabled = %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestNoColorDisablesForNonTTY(t *testing.T) {
	// os.Stdout/os.Stderr are not TTYs under `go test`, so both stylers must
	// be disabled regardless of NO_COLOR.
	t.Setenv("NO_COLOR", "1")
	if ForStdout().enabled {
		t.Error("ForStdout should be disabled when stdout is not a TTY")
	}
	if ForStderr().enabled {
		t.Error("ForStderr should be disabled when stderr is not a TTY")
	}
}

func TestNoColorEmptyValueNotHonored(t *testing.T) {
	// An empty NO_COLOR value counts as unset per the spec; still non-TTY here.
	t.Setenv("NO_COLOR", "")
	if ForStdout().enabled {
		t.Error("ForStdout should be disabled when stdout is not a TTY")
	}
}
