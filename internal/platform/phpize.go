package platform

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type PhpizePath struct {
	Path string
}

// toolProbeTimeout bounds each php-config/phpize probe. Discovery runs on the
// CLI's critical path, so a hung or interactive binary on $PATH must not stall
// the process indefinitely.
const toolProbeTimeout = 10 * time.Second

// GuessPhpize discovers a `phpize` from the target PHP binary, then $PATH,
// validating its reported API version matches the target PHP.
func GuessPhpize(ctx context.Context, php *PhpBinary) (*PhpizePath, error) {
	for _, candidate := range candidateSiblingPaths(php.Path, "phpize") {
		if api, ok := phpizeAPIVersion(ctx, candidate); ok && api == php.APIVersion {
			return &PhpizePath{Path: candidate}, nil
		}
	}
	return nil, fmt.Errorf("could not find a suitable phpize binary (needs PHP API %s); provide one with --with-phpize-path", php.APIVersion)
}

// ExplicitPhpize uses a user-supplied phpize path without API matching, but
// sanity-checks it.
func ExplicitPhpize(ctx context.Context, path string) (*PhpizePath, error) {
	if _, ok := phpizeAPIVersion(ctx, path); !ok {
		return nil, fmt.Errorf("`%s` does not look like a working phpize", path)
	}
	return &PhpizePath{Path: path}, nil
}

// guessPhpConfig finds `php-config` next to the php binary or on $PATH,
// validating it points back at the same php binary.
func guessPhpConfig(ctx context.Context, php *PhpBinary) string {
	probeCtx, cancel := context.WithTimeout(ctx, toolProbeTimeout)
	defer cancel()
	for _, candidate := range candidateSiblingPaths(php.Path, "php-config") {
		out, err := exec.CommandContext(probeCtx, candidate, "--php-binary").Output()
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
func phpizeAPIVersion(ctx context.Context, phpize string) (string, bool) {
	probeCtx, cancel := context.WithTimeout(ctx, toolProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, phpize, "--version").Output()
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
