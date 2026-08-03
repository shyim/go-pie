package install

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/shyim/go-pie/internal/platform"
)

// GpieMarkerPrefix is the marker comment GPIE writes above every directive it
// manages. Keep the writer and scanner in sync — this is the single source of
// truth for whether GPIE installed an extension.
const GpieMarkerPrefix = "; gpie automatically added this to enable the "

// ManagedExtension is a single extension GPIE set up, as reconstructed from its
// INI snippet.
type ManagedExtension struct {
	PackageName   string
	ExtensionName string
	Priority      uint32
	Version       *string
	IniFile       string
}

// rustLines mirrors Rust str::lines(): split on '\n', strip one trailing '\r'
// per line, no trailing empty element for a final newline.
func rustLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	parts := strings.Split(s, "\n")
	for i, p := range parts {
		parts[i] = strings.TrimSuffix(p, "\r")
	}
	return parts
}

// DiscoverManaged finds every GPIE-managed extension for the target platform,
// keyed by lowercased extension name (later files win, matching INI scan-dir
// ordering).
func DiscoverManaged(ctx context.Context, plat *platform.TargetPlatform) (map[string]ManagedExtension, error) {
	found := make(map[string]ManagedExtension)

	files, err := candidateIniFiles(ctx, plat)
	if err != nil {
		return nil, err
	}
	for _, path := range files {
		contents, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, managed := range parseMarkers(string(contents), path) {
			found[strings.ToLower(managed.ExtensionName)] = managed
		}
	}

	return found, nil
}

// ManagedKeys returns the map's keys sorted ascending (BTreeMap order).
func ManagedKeys(m map[string]ManagedExtension) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// candidateIniFiles returns every INI file we might have written to: each file
// in the scan dir (sorted), plus the single loaded php.ini.
func candidateIniFiles(ctx context.Context, plat *platform.TargetPlatform) ([]string, error) {
	var files []string

	dir, err := additionalIniDir(ctx, plat)
	if err != nil {
		return nil, err
	}
	if dir != "" {
		entries, readErr := os.ReadDir(dir)
		if readErr == nil {
			var iniFiles []string
			for _, e := range entries {
				p := filepath.Join(dir, e.Name())
				if filepath.Ext(p) == ".ini" {
					iniFiles = append(iniFiles, p)
				}
			}
			sort.Strings(iniFiles)
			files = append(files, iniFiles...)
		}
	}

	phpIni, err := loadedPhpIni(ctx, plat)
	if err != nil {
		return nil, err
	}
	if phpIni != "" {
		files = append(files, phpIni)
	}

	return files, nil
}

// parseMarkers parses GPIE markers in one INI file's contents.
func parseMarkers(contents, iniFile string) []ManagedExtension {
	var out []ManagedExtension
	lines := rustLines(contents)

	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		rest, ok := markerPackage(trimmed)
		if !ok {
			continue
		}
		packageName := strings.TrimSpace(strings.TrimSuffix(rest, " extension"))
		if packageName == "" {
			continue
		}

		priority := uint32(80)
		var version *string
		var extensionName *string
		for _, follow := range lines[idx+1:] {
			t := strings.TrimSpace(follow)
			if t == "" {
				continue
			}
			if p, ok := strings.CutPrefix(t, "; priority="); ok {
				if parsed, err := strconv.ParseUint(strings.TrimSpace(p), 10, 32); err == nil {
					priority = uint32(parsed)
				} else {
					priority = 80
				}
				continue
			}
			if v, ok := strings.CutPrefix(t, "; version="); ok {
				v = strings.TrimSpace(v)
				if v != "" {
					vv := v
					version = &vv
				}
				continue
			}
			// A new marker ends this block; without this an orphaned marker
			// would absorb the next block's directive.
			if _, isMarker := markerPackage(t); isMarker {
				break
			}
			if strings.HasPrefix(t, ";") {
				continue
			}
			if directive, value, found := strings.Cut(t, "="); found {
				directive = strings.TrimSpace(directive)
				if directive == "extension" || directive == "zend_extension" {
					name := strings.Trim(strings.TrimSpace(value), "\"")
					extensionName = &name
				}
			}
			break
		}

		if extensionName != nil && *extensionName != "" {
			out = append(out, ManagedExtension{
				PackageName:   packageName,
				ExtensionName: *extensionName,
				Priority:      priority,
				Version:       version,
				IniFile:       iniFile,
			})
		}
	}

	return out
}

// RemoveMarkerBlock removes the GPIE-managed block for extensionName from
// contents, preserving everything else.
func RemoveMarkerBlock(contents, extensionName string) string {
	target := strings.ToLower(extensionName)
	lines := rustLines(contents)
	keep := make([]string, 0, len(lines))
	i := 0

	for i < len(lines) {
		trimmed := strings.TrimSpace(lines[i])
		if rest, ok := markerPackage(trimmed); ok {
			pkg := strings.TrimSpace(strings.TrimSuffix(rest, " extension"))
			if pkg != "" {
				if end, thisExt, found := blockExtent(lines, i); found {
					if strings.EqualFold(thisExt, target) {
						i = end + 1
						continue
					}
				}
			}
		}
		keep = append(keep, lines[i])
		i++
	}

	result := strings.Join(keep, "\n")
	if result != "" && strings.HasSuffix(contents, "\n") {
		result += "\n"
	}
	return result
}

func markerPackage(line string) (string, bool) {
	return strings.CutPrefix(line, GpieMarkerPrefix)
}

// blockExtent returns (lastLineIndex, extensionName, true) for the block that
// begins at markerIdx, or found=false if it never reaches a directive line.
func blockExtent(lines []string, markerIdx int) (int, string, bool) {
	for offset := markerIdx + 1; offset < len(lines); offset++ {
		t := strings.TrimSpace(lines[offset])
		if t == "" {
			continue
		}
		// Stop at the next marker so removing a block cannot swallow the
		// preceding orphaned marker's line.
		if _, isMarker := markerPackage(t); isMarker {
			return 0, "", false
		}
		if strings.HasPrefix(t, ";") {
			continue
		}
		if directive, value, found := strings.Cut(t, "="); found {
			directive = strings.TrimSpace(directive)
			if directive == "extension" || directive == "zend_extension" {
				return offset, strings.Trim(strings.TrimSpace(value), "\""), true
			}
		}
		return 0, "", false
	}
	return 0, "", false
}

// IsEffectivelyEmpty is true when every line, after trimming whitespace, is
// empty or starts with ';'.
func IsEffectivelyEmpty(contents string) bool {
	for _, line := range rustLines(contents) {
		l := strings.TrimSpace(line)
		if l != "" && !strings.HasPrefix(l, ";") {
			return false
		}
	}
	return true
}
