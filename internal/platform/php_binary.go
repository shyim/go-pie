package platform

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/shyim/go-pie/internal/procutil"
)

type PhpVersion struct {
	Major, Minor, Patch uint8
}

func (v PhpVersion) MajorMinor() string {
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

func (v PhpVersion) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

type ExtensionVersion struct {
	Name, Version string
}

type PhpBinary struct {
	Path            string
	Version         PhpVersion
	ThreadSafe      bool
	DebugBuild      bool
	Architecture    Architecture
	APIVersion      string
	ExtensionDir    string
	WindowsCompiler *WindowsCompiler
}

// PhpBinaryFixture builds a synthetic PHP binary for unit tests.
func PhpBinaryFixture(major, minor, patch uint8, arch Architecture) *PhpBinary {
	return &PhpBinary{
		Path:            "/usr/bin/php",
		Version:         PhpVersion{Major: major, Minor: minor, Patch: patch},
		ThreadSafe:      false,
		DebugBuild:      false,
		Architecture:    arch,
		APIVersion:      "20240924",
		ExtensionDir:    "/usr/lib/php/ext",
		WindowsCompiler: nil,
	}
}

// PhpBinaryFromPathEnv locates `php` on $PATH and introspects it.
func PhpBinaryFromPathEnv() (*PhpBinary, error) {
	path, err := exec.LookPath("php")
	if err != nil {
		return nil, fmt.Errorf("could not find a `php` binary on your PATH; pass --with-php-path: %w", err)
	}
	return PhpBinaryFromPath(path)
}

const constScript = "echo PHP_MAJOR_VERSION,'.',PHP_MINOR_VERSION,'.',PHP_RELEASE_VERSION,\"\\n\";" +
	"echo (ZEND_THREAD_SAFE?1:0),\"\\n\";" +
	"echo PHP_INT_SIZE,\"\\n\";" +
	"echo php_uname('m'),\"\\n\";" +
	"echo 'RPIE_OK';"

// PhpBinaryFromPath introspects a specific `php` binary with two subprocess
// invocations run concurrently.
func PhpBinaryFromPath(path string) (*PhpBinary, error) {
	var (
		wg                       sync.WaitGroup
		phpinfo, constants       string
		phpinfoErr, constantsErr error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		phpinfo, phpinfoErr = runPhp(path, []string{"-i"})
	}()
	go func() {
		defer wg.Done()
		constants, constantsErr = runPhp(path, []string{"-n", "-r", constScript})
	}()
	wg.Wait()

	if phpinfoErr != nil {
		return nil, fmt.Errorf("running `php -i` for %s: %w", path, phpinfoErr)
	}
	if constantsErr != nil {
		return nil, fmt.Errorf("failed to execute php binary at %s: %w", path, constantsErr)
	}

	consts, ok := parseConstants(constants)
	if !ok {
		return nil, fmt.Errorf("`%s` does not look like a working PHP binary (unexpected output: %q)", path, constants)
	}

	architecture := ArchitectureFromPhpUname(consts.machine, consts.intSize)
	debugBuild := phpinfoDebugBuild(phpinfo)
	apiVersion, ok := phpinfoPhpAPI(phpinfo)
	if !ok {
		return nil, fmt.Errorf("could not determine PHP API version from `php -i`")
	}
	extensionDir, ok := phpinfoExtensionDir(phpinfo)
	if !ok {
		return nil, fmt.Errorf("could not determine extension_dir for %s", path)
	}
	windowsCompiler := CompilerFromPhpinfo(phpinfo)

	return &PhpBinary{
		Path:            path,
		Version:         consts.version,
		ThreadSafe:      consts.threadSafe,
		DebugBuild:      debugBuild,
		Architecture:    architecture,
		APIVersion:      apiVersion,
		ExtensionDir:    extensionDir,
		WindowsCompiler: windowsCompiler,
	}, nil
}

func (p *PhpBinary) SharedObjectPath(extensionName string) string {
	return filepath.Join(p.ExtensionDir, extensionName+".so")
}

func (p *PhpBinary) DllPath(extensionName string) string {
	return filepath.Join(p.ExtensionDir, "php_"+extensionName+".dll")
}

// PhpDir returns the directory containing the php executable. The bool is false
// only for a bare relative path with no parent.
func (p *PhpBinary) PhpDir() (string, bool) {
	if p.Path == "" {
		return "", false
	}
	return filepath.Dir(p.Path), true
}

func (p *PhpBinary) LoadedExtensions() ([]string, error) {
	raw, err := runPhp(p.Path, []string{
		"-d", "display_errors=stderr",
		"-r", "echo implode(\"\\n\", get_loaded_extensions());",
	})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range rustLines(raw) {
		l := strings.TrimSpace(line)
		if l == "" {
			continue
		}
		out = append(out, strings.ToLower(l))
	}
	return out, nil
}

func (p *PhpBinary) LoadedExtensionsWithVersions() ([]ExtensionVersion, error) {
	raw, err := runPhp(p.Path, []string{
		"-d", "display_errors=stderr",
		"-r", "foreach (get_loaded_extensions() as $e) { echo strtolower($e) . \"\\t\" . (phpversion($e) ?: '') . \"\\n\"; }",
	})
	if err != nil {
		return nil, err
	}
	var out []ExtensionVersion
	for _, line := range rustLines(raw) {
		name, version, _ := strings.Cut(line, "\t")
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		out = append(out, ExtensionVersion{Name: name, Version: strings.TrimSpace(version)})
	}
	return out, nil
}

func (p *PhpBinary) ExtensionIsLoaded(name string) (bool, error) {
	needle := strings.ToLower(name)
	loaded, err := p.LoadedExtensions()
	if err != nil {
		return false, err
	}
	for _, e := range loaded {
		if e == needle {
			return true, nil
		}
	}
	return false, nil
}

// runPhp runs `<path> <args...>` and returns trimmed stdout, erroring on
// non-zero exit. Error text mirrors the Rust helper exactly.
func runPhp(path string, args []string) (string, error) {
	cmd := exec.Command(path, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to spawn %s: %w", path, err)
	}
	err := cmd.Wait()
	if cmd.ProcessState != nil && !cmd.ProcessState.Success() {
		return "", fmt.Errorf("%s %s exited with %s: %s",
			path, procutil.DebugQuotedArgs(args),
			procutil.FormatExitStatus(cmd.ProcessState),
			strings.TrimSpace(stderr.String()))
	}
	if err != nil {
		return "", fmt.Errorf("failed to spawn %s: %w", path, err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

type phpConstants struct {
	version    PhpVersion
	threadSafe bool
	intSize    uint8
	machine    string
}

func parseConstants(raw string) (phpConstants, bool) {
	lines := rustLines(raw)
	if len(lines) < 5 {
		return phpConstants{}, false
	}
	version, err := parseVersion(lines[0])
	if err != nil {
		return phpConstants{}, false
	}
	threadSafe := strings.TrimSpace(lines[1]) == "1"
	intSize, err := strconv.ParseUint(strings.TrimSpace(lines[2]), 10, 8)
	if err != nil {
		return phpConstants{}, false
	}
	machine := strings.TrimSpace(lines[3])
	if strings.TrimSpace(lines[4]) != "RPIE_OK" {
		return phpConstants{}, false
	}
	return phpConstants{
		version:    version,
		threadSafe: threadSafe,
		intSize:    uint8(intSize),
		machine:    machine,
	}, true
}

func parseVersion(raw string) (PhpVersion, error) {
	parts := strings.Split(strings.TrimSpace(raw), ".")
	get := func(idx int, what string) (uint8, error) {
		if idx < len(parts) {
			if n, err := strconv.ParseUint(strings.TrimSpace(parts[idx]), 10, 8); err == nil {
				return uint8(n), nil
			}
		}
		return 0, fmt.Errorf("could not parse PHP %s from version string %q", what, raw)
	}
	major, err := get(0, "major")
	if err != nil {
		return PhpVersion{}, err
	}
	minor, err := get(1, "minor")
	if err != nil {
		return PhpVersion{}, err
	}
	patch, err := get(2, "patch")
	if err != nil {
		return PhpVersion{}, err
	}
	return PhpVersion{Major: major, Minor: minor, Patch: patch}, nil
}

func phpinfoDebugBuild(phpinfo string) bool {
	for _, line := range rustLines(phpinfo) {
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), "Debug Build") {
			return strings.Contains(strings.ToLower(line), "yes")
		}
	}
	return false
}

func phpinfoPhpAPI(phpinfo string) (string, bool) {
	for _, line := range rustLines(phpinfo) {
		trimmed := strings.TrimLeft(line, " \t")
		if rest, ok := strings.CutPrefix(trimmed, "PHP API"); ok {
			value := lastArrowField(rest)
			if value != "" {
				return value, true
			}
		}
	}
	return "", false
}

func phpinfoExtensionDir(phpinfo string) (string, bool) {
	for _, line := range rustLines(phpinfo) {
		trimmed := strings.TrimLeft(line, " \t")
		if rest, ok := strings.CutPrefix(trimmed, "extension_dir"); ok {
			value := lastArrowField(rest)
			if value != "" {
				return value, true
			}
		}
	}
	return "", false
}

// lastArrowField splits on "=>" and returns the last field trimmed. Mirrors
// Rust `rest.split("=>").last().unwrap_or("").trim()`.
func lastArrowField(rest string) string {
	parts := strings.Split(rest, "=>")
	return strings.TrimSpace(parts[len(parts)-1])
}

// rustLines mirrors Rust str::lines(): split on '\n', strip one trailing '\r'
// per line, no trailing empty element for a final newline.
func rustLines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	for i, p := range parts {
		parts[i] = strings.TrimSuffix(p, "\r")
	}
	return parts
}
