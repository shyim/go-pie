package platform

import "strings"

type WindowsCompiler int

const (
	Vc6 WindowsCompiler = iota
	Vc8
	Vc9
	Vc11
	Vc14
	Vc15
	Vs16
	Vs17
)

func WindowsCompilerFromToken(token string) (WindowsCompiler, bool) {
	switch strings.ToUpper(strings.TrimSpace(token)) {
	case "VC6":
		return Vc6, true
	case "VC8":
		return Vc8, true
	case "VC9":
		return Vc9, true
	case "VC11":
		return Vc11, true
	case "VC14":
		return Vc14, true
	case "VC15":
		return Vc15, true
	case "VS16":
		return Vs16, true
	case "VS17":
		return Vs17, true
	default:
		return 0, false
	}
}

func (c WindowsCompiler) Token() string {
	switch c {
	case Vc6:
		return "vc6"
	case Vc8:
		return "vc8"
	case Vc9:
		return "vc9"
	case Vc11:
		return "vc11"
	case Vc14:
		return "vc14"
	case Vc15:
		return "vc15"
	case Vs16:
		return "vs16"
	case Vs17:
		return "vs17"
	default:
		return ""
	}
}

// CompilerFromPhpinfo extracts the Windows compiler from `php -i` output by
// parsing the `PHP Extension Build => API...,TS,VC15` line. Returns nil when
// absent or unrecognized.
func CompilerFromPhpinfo(phpinfo string) *WindowsCompiler {
	for _, line := range strings.Split(phpinfo, "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimLeft(line, " \t")
		if rest, ok := strings.CutPrefix(trimmed, "PHP Extension Build"); ok {
			value := lastArrowField(rest)
			for _, part := range strings.Split(value, ",") {
				if c, ok := WindowsCompilerFromToken(part); ok {
					cc := c
					return &cc
				}
			}
		}
	}
	return nil
}
