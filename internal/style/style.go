// Package style provides TTY-gated ANSI styling, replacing the Rust `console`
// crate. ANSI escapes are emitted only when the destination stream is a
// terminal and NO_COLOR is unset/empty.
package style

import (
	"os"
	"strings"

	"golang.org/x/term"
)

// Styler wraps text in ANSI escape codes when enabled.
type Styler struct {
	enabled bool
}

// ForStdout returns a Styler enabled iff stdout is a TTY and NO_COLOR is unset.
func ForStdout() Styler {
	return Styler{enabled: colorEnabled(os.Stdout)}
}

// ForStderr returns a Styler enabled iff stderr is a TTY and NO_COLOR is unset.
func ForStderr() Styler {
	return Styler{enabled: colorEnabled(os.Stderr)}
}

// New returns a Styler with an explicit enabled state (for tests).
func New(enabled bool) Styler {
	return Styler{enabled: enabled}
}

func colorEnabled(f *os.File) bool {
	if v, ok := os.LookupEnv("NO_COLOR"); ok && v != "" {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

func (s Styler) wrap(codes, text string) string {
	if !s.enabled {
		return text
	}
	var b strings.Builder
	b.WriteString("\x1b[")
	b.WriteString(codes)
	b.WriteString("m")
	b.WriteString(text)
	b.WriteString("\x1b[0m")
	return b.String()
}

// Red wraps text in the red foreground color.
func (s Styler) Red(text string) string { return s.wrap("31", text) }

// RedBold wraps text in bold + red.
func (s Styler) RedBold(text string) string { return s.wrap("1;31", text) }

// Green wraps text in the green foreground color.
func (s Styler) Green(text string) string { return s.wrap("32", text) }

// Yellow wraps text in the yellow foreground color.
func (s Styler) Yellow(text string) string { return s.wrap("33", text) }

// Cyan wraps text in the cyan foreground color.
func (s Styler) Cyan(text string) string { return s.wrap("36", text) }

// BoldCyan wraps text in bold + cyan.
func (s Styler) BoldCyan(text string) string { return s.wrap("1;36", text) }

// Bold wraps text in the bold attribute.
func (s Styler) Bold(text string) string { return s.wrap("1", text) }

// Dim wraps text in the dim attribute.
func (s Styler) Dim(text string) string { return s.wrap("2", text) }

// BoldUnderlined wraps text in bold + underline.
func (s Styler) BoldUnderlined(text string) string { return s.wrap("1;4", text) }
