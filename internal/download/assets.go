package download

import (
	"fmt"
	"strings"

	"github.com/shyim/go-pie/internal/platform"
	"github.com/shyim/go-pie/internal/resolver"
)

// sourceAssetNames returns candidate filenames for a pre-packaged source
// archive, most-preferred first.
func sourceAssetNames(pkg *resolver.ResolvedPackage) []string {
	name := strings.ToLower(pkg.ExtensionName)
	version := strings.ToLower(pkg.Version)
	return []string{
		fmt.Sprintf("php_%s-%s-src.tgz", name, version),
		fmt.Sprintf("php_%s-%s-src.zip", name, version),
		fmt.Sprintf("%s-%s.tgz", name, version),
	}
}

// binaryAssetNames returns candidate filenames for a pre-packaged binary asset,
// most-preferred first.
func binaryAssetNames(pkg *resolver.ResolvedPackage, plat *platform.TargetPlatform) []string {
	return binaryAssetNamesInner(
		pkg.ExtensionName,
		pkg.Version,
		plat.PHP.Version.MajorMinor(),
		plat.Architecture.Token(),
		plat.OSFamily.Token(),
		plat.PHP.DebugBuild,
		plat.ThreadSafety,
		platform.DetectLibc(),
	)
}

func binaryAssetNamesInner(
	name, version, phpVer, arch, os string,
	debugBuild bool,
	threadSafety platform.ThreadSafety,
	libc platform.LibcFlavour,
) []string {
	name = strings.ToLower(name)
	version = strings.ToLower(version)
	debug := ""
	if debugBuild {
		debug = "-debug"
	}

	tsNoSuffix := ""
	if threadSafety == platform.ThreadSafe {
		tsNoSuffix = "-zts"
	}
	tsWithSuffix := "-" + threadSafety.Token()

	var names []string
	pushVariant := func(libcToken, ts string) {
		names = append(names,
			fmt.Sprintf("php_%s-%s_php%s-%s-%s-%s%s%s.zip", name, version, phpVer, arch, os, libcToken, debug, ts),
			fmt.Sprintf("php_%s-%s_php%s-%s-%s-%s%s%s.tgz", name, version, phpVer, arch, os, libcToken, debug, ts),
		)
	}

	pushVariant(libc.Token(), tsNoSuffix)
	pushVariant(libc.Token(), tsWithSuffix)

	if libc == platform.Glibc || libc == platform.Musl {
		pushVariant("anylibc", tsNoSuffix)
		pushVariant("anylibc", tsWithSuffix)
	}

	return dedupPreservingOrder(names)
}

func dedupPreservingOrder(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

// windowsAssetNames returns candidate filenames for a Windows pre-compiled DLL
// zip. Returns an empty slice when the compiler could not be determined.
func windowsAssetNames(pkg *resolver.ResolvedPackage, plat *platform.TargetPlatform) []string {
	if plat.PHP.WindowsCompiler == nil {
		return nil
	}
	return windowsAssetNamesInner(
		pkg.ExtensionName,
		pkg.Version,
		plat.PHP.Version.MajorMinor(),
		plat.ThreadSafety,
		plat.PHP.WindowsCompiler.Token(),
		plat.Architecture.Token(),
	)
}

func windowsAssetNamesInner(
	name, version, phpVer string,
	threadSafety platform.ThreadSafety,
	vc, arch string,
) []string {
	name = strings.ToLower(name)
	version = strings.ToLower(version)
	ts := "nts"
	if threadSafety == platform.ThreadSafe {
		ts = "ts"
	}
	return dedupPreservingOrder([]string{
		fmt.Sprintf("php_%s-%s-%s-%s-%s-%s.zip", name, version, phpVer, ts, vc, arch),
		fmt.Sprintf("php_%s-%s-%s-%s-%s-%s.zip", name, version, phpVer, vc, ts, arch),
	})
}
