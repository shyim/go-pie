package platform

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

type PhpizePath struct {
	Path string
}

// GuessPhpize discovers a `phpize` from the target PHP binary, then $PATH,
// validating its reported API version matches the target PHP.
func GuessPhpize(php *PhpBinary) (*PhpizePath, error) {
	for _, candidate := range candidateSiblingPaths(php.Path, "phpize") {
		if api, ok := phpizeAPIVersion(candidate); ok && api == php.APIVersion {
			return &PhpizePath{Path: candidate}, nil
		}
	}
	return nil, fmt.Errorf("Could not find a suitable phpize binary (needs PHP API %s). Provide one with --with-phpize-path.", php.APIVersion)
}

// ExplicitPhpize uses a user-supplied phpize path without API matching, but
// sanity-checks it.
func ExplicitPhpize(path string) (*PhpizePath, error) {
	if _, ok := phpizeAPIVersion(path); !ok {
		return nil, fmt.Errorf("`%s` does not look like a working phpize", path)
	}
	return &PhpizePath{Path: path}, nil
}

// guessPhpConfig finds `php-config` next to the php binary or on $PATH,
// validating it points back at the same php binary.
func guessPhpConfig(php *PhpBinary) string {
	for _, candidate := range candidateSiblingPaths(php.Path, "php-config") {
		out, err := exec.Command(candidate, "--php-binary").Output()
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(out)) == php.Path {
			return candidate
		}
	}
	if found, err := exec.LookPath("php-config"); err == nil {
		return found
	}
	return ""
}

// candidateSiblingPaths builds the ordered candidate list for a tool, mirroring
// Rust's candidate_sibling_paths incl. consecutive-only dedup.
func candidateSiblingPaths(phpPath, tool string) []string {
	var out []string
	dir := filepath.Dir(phpPath)
	file := filepath.Base(phpPath)
	if dir != "" && file != "" && file != "." && file != string(filepath.Separator) {
		if suffix, ok := strings.CutPrefix(file, "php"); ok {
			out = append(out, filepath.Join(dir, tool+suffix))
		}
		out = append(out, filepath.Join(dir, tool))
	}
	if found, err := exec.LookPath(tool); err == nil {
		out = append(out, found)
	}
	return dedupConsecutive(out)
}

func dedupConsecutive(in []string) []string {
	if len(in) == 0 {
		return in
	}
	out := in[:1]
	for _, v := range in[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

// phpizeAPIVersion runs `phpize --version` and extracts the PHP Api Version.
func phpizeAPIVersion(phpize string) (string, bool) {
	out, err := exec.Command(phpize, "--version").Output()
	if err != nil {
		return "", false
	}
	for _, line := range rustLines(string(out)) {
		if strings.Contains(strings.ToLower(line), "php api version") {
			parts := strings.Split(line, ":")
			if len(parts) > 1 {
				v := strings.TrimSpace(parts[1])
				if v != "" {
					return v, true
				}
			}
		}
	}
	return "", false
}
