// Package buildpkg implements the Unix source-build pipeline:
// phpize → ./configure → make -jN, producing modules/<ext>.so.
// It mirrors the Rust src/build/mod.rs module.
package buildpkg

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/shyim/go-pie/internal/download"
	"github.com/shyim/go-pie/internal/platform"
	"github.com/shyim/go-pie/internal/procutil"
	"github.com/shyim/go-pie/internal/resolver"
)

// BuiltExtension is the result of a successful build: the compiled shared object.
type BuiltExtension struct {
	BinaryPath string
}

// FromPrebuilt wraps an already-built .so (e.g. a downloaded pre-packaged
// binary) so it can flow through the same install path as a freshly compiled
// one. No validation.
func FromPrebuilt(binaryPath string) BuiltExtension {
	return BuiltExtension{BinaryPath: binaryPath}
}

// Build builds package from its extracted source, streaming output to the
// terminal and using the platform's full make -jN. This is the sequential path.
func Build(pkg *resolver.ResolvedPackage, src *download.DownloadedPackage,
	plat *platform.TargetPlatform, configureOptions []string) (BuiltExtension, error) {
	return BuildWith(pkg, src, plat, configureOptions, plat.MakeParallelJobs, nil)
}

// BuildWith builds package, letting the caller control the make job count and
// whether output is captured. When sink is non-nil, all build output is
// buffered into it (for parallel builds); when nil, output streams to the
// terminal.
func BuildWith(pkg *resolver.ResolvedPackage, src *download.DownloadedPackage,
	plat *platform.TargetPlatform, configureOptions []string,
	makeJobs int, sink *bytes.Buffer) (BuiltExtension, error) {

	cwd, ok := src.SourcePath()
	if !ok {
		return BuiltExtension{}, fmt.Errorf(
			"cannot build `%s`: the download is a pre-packaged binary, not source",
			pkg.Name)
	}

	if plat.Phpize == nil {
		return BuiltExtension{}, fmt.Errorf(
			"no usable phpize found for the target PHP (API %s); pass --with-phpize-path",
			plat.PHP.APIVersion)
	}
	phpizePath := plat.Phpize.Path

	step := func(program string, args []string, label string) error {
		if sink != nil {
			return procutil.RunCaptured(program, args, cwd, label, sink)
		}
		return procutil.Run(program, args, cwd, label)
	}

	// 1. phpize --clean (best-effort), then phpize.
	_ = step(phpizePath, []string{"--clean"}, "phpize --clean")
	if err := step(phpizePath, nil, "phpize"); err != nil {
		return BuiltExtension{}, err
	}

	// 2. ./configure --with-php-config=<...> <options>
	var configureArgs []string
	if plat.PhpConfig != "" {
		configureArgs = append(configureArgs, "--with-php-config="+plat.PhpConfig)
	}
	configureArgs = append(configureArgs, configureOptions...)
	if err := step("./configure", configureArgs, "./configure"); err != nil {
		return BuiltExtension{}, err
	}

	// 3. make -jN
	var makeArgs []string
	if makeJobs > 1 {
		makeArgs = []string{fmt.Sprintf("-j%d", makeJobs)}
	}
	if err := step("make", makeArgs, "make"); err != nil {
		return BuiltExtension{}, err
	}

	binaryPath := filepath.Join(cwd, "modules", pkg.ExtensionName+".so")
	if _, err := os.Stat(binaryPath); err != nil {
		return BuiltExtension{}, fmt.Errorf(
			"build completed but expected binary was not found at %s", binaryPath)
	}

	return BuiltExtension{BinaryPath: binaryPath}, nil
}
