package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shyim/go-pie/internal/platform"
	"github.com/shyim/go-pie/internal/procutil"
	"github.com/shyim/go-pie/internal/resolver"
	"github.com/shyim/go-pie/internal/sudo"
)

// IniOutcomeKind enumerates the possible results of INI setup.
type IniOutcomeKind int

const (
	IniWritten IniOutcomeKind = iota
	IniAlreadyEnabled
	IniSkipped
	IniNoSuitableLocation
)

// IniOutcome describes what INI setup did. Path is set only for IniWritten.
type IniOutcome struct {
	Kind IniOutcomeKind
	Path string
}

// setupIni configures the INI so PHP loads the extension, then verifies it
// loads.
func setupIni(pkg *resolver.ResolvedPackage, plat *platform.TargetPlatform) (IniOutcome, error) {
	loaded, err := plat.PHP.ExtensionIsLoaded(pkg.ExtensionName)
	if err != nil {
		return IniOutcome{}, err
	}
	if loaded {
		return IniOutcome{Kind: IniAlreadyEnabled}, nil
	}

	directive := pkg.ExtensionType.IniDirective()
	contents := iniContents(pkg, directive)

	target, err := chooseIniPath(pkg, plat)
	if err != nil {
		return IniOutcome{}, err
	}
	if target == "" {
		return IniOutcome{Kind: IniNoSuitableLocation}, nil
	}

	if err := writeIni(target, contents); err != nil {
		return IniOutcome{}, err
	}

	loaded, err = plat.PHP.ExtensionIsLoaded(pkg.ExtensionName)
	if err != nil {
		return IniOutcome{}, err
	}
	if !loaded {
		return IniOutcome{}, fmt.Errorf(
			"wrote %s but `%s` still does not load — check the build output",
			target, pkg.ExtensionName)
	}

	return IniOutcome{Kind: IniWritten, Path: target}, nil
}

// iniContents renders the snippet RPIE writes.
func iniContents(pkg *resolver.ResolvedPackage, directive string) string {
	return fmt.Sprintf(
		"\n%s%s extension\n; priority=%d\n; version=%s\n%s=%s\n",
		RpieMarkerPrefix,
		pkg.Name,
		pkg.Priority,
		pkg.Version,
		directive,
		pkg.ExtensionName,
	)
}

// chooseIniPath decides where to write the INI: prefer the additional-ini scan
// dir, else the loaded php.ini. Empty return means no suitable location.
func chooseIniPath(pkg *resolver.ResolvedPackage, plat *platform.TargetPlatform) (string, error) {
	dir, err := additionalIniDir(plat)
	if err != nil {
		return "", err
	}
	if dir != "" {
		filename := fmt.Sprintf("%02d-%s.ini", pkg.Priority, pkg.ExtensionName)
		return filepath.Join(dir, filename), nil
	}
	phpIni, err := loadedPhpIni(plat)
	if err != nil {
		return "", err
	}
	if phpIni != "" {
		return phpIni, nil
	}
	return "", nil
}

// additionalIniDir reports PHP's compile-time additional-ini scan dir, returning
// the first existing directory among ':'/';'-separated parts. Empty = none.
func additionalIniDir(plat *platform.TargetPlatform) (string, error) {
	raw, err := procutil.Capture(plat.PHP.Path,
		[]string{"-d", "display_errors=stderr", "-r", "echo PHP_CONFIG_FILE_SCAN_DIR;"})
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(raw)
	for _, part := range strings.FieldsFunc(dir, func(r rune) bool { return r == ':' || r == ';' }) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if info, err := os.Stat(part); err == nil && info.IsDir() {
			return part, nil
		}
	}
	return "", nil
}

// loadedPhpIni reports the single loaded php.ini, empty if none.
func loadedPhpIni(plat *platform.TargetPlatform) (string, error) {
	raw, err := procutil.Capture(plat.PHP.Path,
		[]string{"-d", "display_errors=stderr", "-r", "echo php_ini_loaded_file() ?: '';"})
	if err != nil {
		return "", err
	}
	raw = strings.TrimSpace(raw)
	return raw, nil
}

// writeIni writes (or appends to) the INI file, escalating with sudo if needed.
func writeIni(target, contents string) error {
	if !sudo.PathNeedsSudo(target) {
		f, err := os.OpenFile(target, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("opening %s: %w", target, err)
		}
		defer f.Close()
		if _, err := f.WriteString(contents); err != nil {
			return fmt.Errorf("writing %s: %w", target, err)
		}
		return nil
	}

	if !sudo.IsAvailable() {
		return fmt.Errorf(
			"cannot write %s (needs elevated privileges and sudo is unavailable / non-interactive)",
			target)
	}

	tmp, err := os.CreateTemp("", "")
	if err != nil {
		return fmt.Errorf("creating temp INI file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(contents); err != nil {
		tmp.Close()
		return fmt.Errorf("writing temp INI file: %w", err)
	}
	tmp.Close()

	script := fmt.Sprintf("sudo tee -a %s < %s >/dev/null",
		shellQuote(target), shellQuote(tmpPath))
	return procutil.Run("sh", []string{"-c", script}, ".", "writing INI file (sudo)")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
