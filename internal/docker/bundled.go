package docker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/shyim/go-pie/internal/procutil"
)

// LooksLikeBundledName reports whether spec is a simple bundled-extension
// identifier: non-empty, no '/' or ':', and only ASCII alphanumerics or '_'.
func LooksLikeBundledName(spec string) bool {
	if spec == "" {
		return false
	}
	for _, c := range spec {
		if c == '/' || c == ':' {
			return false
		}
		if !isASCIIAlnumUnderscore(c) {
			return false
		}
	}
	return true
}

func isASCIIAlnumUnderscore(c rune) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == '_'
}

// HelpersAvailable reports whether all three docker-php-* helper scripts are on
// $PATH.
func HelpersAvailable() bool {
	return lookPathOK("docker-php-ext-install") &&
		lookPathOK("docker-php-ext-configure") &&
		lookPathOK("docker-php-source")
}

// ensureSourceExtracted extracts the PHP source tree if it is not already
// present. docker-php-source extract fails when /usr/src/php already exists,
// so re-extraction must be skipped rather than retried.
func ensureSourceExtracted(ctx context.Context) error {
	if info, err := os.Stat("/usr/src/php/ext"); err == nil && info.IsDir() {
		return nil
	}
	return procutil.Run(ctx, "docker-php-source", []string{"extract"}, ".", "docker-php-source extract")
}

// IsBundled reports whether name is a bundled extension for the current PHP,
// extracting the PHP source first. Errors are swallowed as false.
func IsBundled(ctx context.Context, name string) bool {
	if !LooksLikeBundledName(name) || !HelpersAvailable() {
		return false
	}
	if ensureSourceExtracted(ctx) != nil {
		return false
	}
	info, err := os.Stat(filepath.Join("/usr/src/php/ext", name))
	return err == nil && info.IsDir()
}

// InstallBundled installs a bundled extension: configure (with any known flags)
// then docker-php-ext-install -jN. Returns the INI path the helper wrote, or ""
// when not found.
func InstallBundled(ctx context.Context, name string, jobs int) (string, error) {
	if !LooksLikeBundledName(name) {
		return "", fmt.Errorf("`%s` is not a valid bundled PHP extension name", name)
	}
	if !HelpersAvailable() {
		return "", errors.New("`docker-php-ext-*` helpers are not available; not in an official PHP image")
	}
	if err := ensureSourceExtracted(ctx); err != nil {
		return "", fmt.Errorf("extracting PHP source: %w", err)
	}

	info, err := os.Stat(filepath.Join("/usr/src/php/ext", name))
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("`%s` is not a bundled PHP extension for this PHP version", name)
	}

	flags := configureFlags(name)
	configureArgs := append([]string{name}, flags...)
	if err := procutil.Run(ctx, "docker-php-ext-configure", configureArgs, ".",
		"docker-php-ext-configure "+name); err != nil {
		return "", fmt.Errorf("configuring bundled extension %s: %w", name, err)
	}

	if err := procutil.Run(ctx, "docker-php-ext-install",
		[]string{fmt.Sprintf("-j%d", jobs), name}, ".",
		"docker-php-ext-install "+name); err != nil {
		return "", fmt.Errorf("installing bundled extension %s: %w", name, err)
	}

	dir, ok := phpIniDir()
	if !ok {
		return "", nil
	}
	ini := filepath.Join(dir, "conf.d", fmt.Sprintf("docker-php-ext-%s.ini", name))
	if _, err := os.Stat(ini); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("checking enabling INI %s: %w", ini, err)
	}
	return ini, nil
}

func configureFlags(name string) []string {
	if name == "gd" {
		return []string{
			"--enable-gd",
			"--with-webp",
			"--with-jpeg",
			"--with-xpm",
			"--with-freetype",
			"--with-avif",
		}
	}
	return nil
}

func phpIniDir() (string, bool) {
	if v, ok := os.LookupEnv("PHP_INI_DIR"); ok {
		if info, err := os.Stat(v); err == nil && info.IsDir() {
			return v, true
		}
	}
	const def = "/usr/local/etc/php"
	if info, err := os.Stat(def); err == nil && info.IsDir() {
		return def, true
	}
	return "", false
}
