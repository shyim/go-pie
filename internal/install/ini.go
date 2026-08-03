package install

import (
	"context"
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
func setupIni(ctx context.Context, pkg *resolver.ResolvedPackage, plat *platform.TargetPlatform) (IniOutcome, error) {
	loaded, err := plat.PHP.ExtensionIsLoaded(ctx, pkg.ExtensionName)
	if err != nil {
		return IniOutcome{}, err
	}
	if loaded {
		return IniOutcome{Kind: IniAlreadyEnabled}, nil
	}

	directive := pkg.ExtensionType.IniDirective()
	contents := iniContents(pkg, directive)

	target, dedicated, err := chooseIniPath(ctx, pkg, plat)
	if err != nil {
		return IniOutcome{}, err
	}
	if target == "" {
		return IniOutcome{Kind: IniNoSuitableLocation}, nil
	}

	// A dedicated scan-dir file belongs to this extension alone, so replace it
	// rather than appending: reinstalling while the module is not loaded would
	// otherwise stack duplicate blocks and make PHP warn that it is already
	// loaded. A shared php.ini must keep its existing content, so drop only a
	// previous gpie block before appending the new one.
	if dedicated {
		err = replaceIni(ctx, target, contents)
	} else {
		err = appendIniReplacingBlock(ctx, target, contents, pkg.ExtensionName)
	}
	if err != nil {
		return IniOutcome{}, err
	}

	loaded, err = plat.PHP.ExtensionIsLoaded(ctx, pkg.ExtensionName)
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

// iniContents renders the snippet GPIE writes.
func iniContents(pkg *resolver.ResolvedPackage, directive string) string {
	return fmt.Sprintf(
		"\n%s%s extension\n; priority=%d\n; version=%s\n%s=%s\n",
		GpieMarkerPrefix,
		pkg.Name,
		pkg.Priority,
		pkg.Version,
		directive,
		pkg.ExtensionName,
	)
}

// chooseIniPath decides where to write the INI: prefer the additional-ini scan
// dir, else the loaded php.ini. Empty return means no suitable location.
// chooseIniPath returns the INI to write and whether that file is dedicated to
// this extension (a scan-dir snippet gpie owns outright) rather than a shared
// php.ini that must be edited in place.
func chooseIniPath(ctx context.Context, pkg *resolver.ResolvedPackage, plat *platform.TargetPlatform) (string, bool, error) {
	dir, err := additionalIniDir(ctx, plat)
	if err != nil {
		return "", false, err
	}
	if dir != "" {
		filename := fmt.Sprintf("%02d-%s.ini", pkg.Priority, pkg.ExtensionName)
		return filepath.Join(dir, filename), true, nil
	}
	phpIni, err := loadedPhpIni(ctx, plat)
	if err != nil {
		return "", false, err
	}
	if phpIni != "" {
		return phpIni, false, nil
	}
	return "", false, nil
}

// additionalIniDir reports PHP's compile-time additional-ini scan dir, returning
// the first existing directory in the platform path list. Empty = none.
func additionalIniDir(ctx context.Context, plat *platform.TargetPlatform) (string, error) {
	raw, err := procutil.Capture(ctx, plat.PHP.Path,
		[]string{"-d", "display_errors=stderr", "-r", "echo PHP_CONFIG_FILE_SCAN_DIR;"})
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(raw)
	for _, part := range splitIniDirList(dir, os.PathListSeparator) {
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

func splitIniDirList(raw string, separator rune) []string {
	return strings.Split(raw, string(separator))
}

// loadedPhpIni reports the single loaded php.ini, empty if none.
func loadedPhpIni(ctx context.Context, plat *platform.TargetPlatform) (string, error) {
	raw, err := procutil.Capture(ctx, plat.PHP.Path,
		[]string{"-d", "display_errors=stderr", "-r", "echo php_ini_loaded_file() ?: '';"})
	if err != nil {
		return "", err
	}
	raw = strings.TrimSpace(raw)
	return raw, nil
}

// writeIni writes (or appends to) the INI file, escalating with sudo if needed.
// replaceIni writes contents as the entire file, replacing any previous
// generation of a gpie-owned snippet.
func replaceIni(ctx context.Context, target, contents string) error {
	return writeFile(ctx, target, contents)
}

// appendIniReplacingBlock appends contents to a shared INI, first dropping any
// existing gpie block for the same extension so repeated installs cannot stack
// duplicate directives.
func appendIniReplacingBlock(ctx context.Context, target, contents, extensionName string) error {
	existing, err := os.ReadFile(target)
	if err != nil {
		if os.IsNotExist(err) {
			return writeIni(ctx, target, contents)
		}
		return fmt.Errorf("reading %s: %w", target, err)
	}
	stripped := RemoveMarkerBlock(string(existing), extensionName)
	if stripped == string(existing) {
		// Nothing of ours in there yet; a plain append preserves the file.
		return writeIni(ctx, target, contents)
	}
	return writeFile(ctx, target, stripped+contents)
}

func writeIni(ctx context.Context, target, contents string) error {
	if !sudo.PathNeedsSudo(target) {
		f, err := os.OpenFile(target, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("opening %s: %w", target, err)
		}
		if _, err := f.WriteString(contents); err != nil {
			_ = f.Close()
			return fmt.Errorf("writing %s: %w", target, err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("closing %s: %w", target, err)
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
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.WriteString(contents); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing temp INI file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp INI file: %w", err)
	}

	script := fmt.Sprintf("sudo tee -a %s < %s >/dev/null",
		shellQuote(target), shellQuote(tmpPath))
	return procutil.Run(ctx, "sh", []string{"-c", script}, ".", "writing INI file (sudo)")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
