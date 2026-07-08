package commands

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/shyim/go-pie/internal/buildpkg"
	"github.com/shyim/go-pie/internal/docker"
	"github.com/shyim/go-pie/internal/download"
	"github.com/shyim/go-pie/internal/install"
	"github.com/shyim/go-pie/internal/oci"
	"github.com/shyim/go-pie/internal/platform"
	"github.com/shyim/go-pie/internal/resolver"
	"github.com/shyim/go-pie/internal/style"
)

type Mode int

const (
	ModeDownloadOnly Mode = iota
	ModeBuildOnly
	ModeInstall
)

type InstallArgs struct {
	Packages           []string
	ConfigureOptions   []string
	SkipEnable         bool
	InstallSystemDeps  bool
	CleanupBuildDeps   bool
	Verify             VerifyArg
	IgnorePlatformReqs bool
	Jobs               int
	PreferPrebuilt     bool
	OciRegistry        *string
	EmitOci            *string
	Php                PhpTargetArgs
}

func (a *InstallArgs) prebuiltRegistry() *string {
	if !a.PreferPrebuilt {
		return nil
	}
	if a.OciRegistry != nil && *a.OciRegistry != "" {
		return a.OciRegistry
	}
	if env := os.Getenv("RPIE_OCI_REGISTRY"); env != "" {
		return &env
	}
	return nil
}

func newInstallCommand(use, short string, mode Mode, phpTarget func(*cobra.Command) PhpTargetArgs) *cobra.Command {
	var (
		skipEnable         bool
		installSystemDeps  bool
		cleanupBuildDeps   bool
		verify             VerifyArg = "warn"
		ignorePlatformReqs bool
		jobs               int
		preferPrebuilt     bool
		ociRegistry        string
		emitOci            string
	)

	cmd := &cobra.Command{
		Use:   use + " PACKAGE...",
		Short: short,
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ranCommand = true

			var packages []string
			var configureOptions []string

			dashDashIdx := -1
			for i, arg := range os.Args {
				if arg == "--" {
					dashDashIdx = i
					break
				}
			}

			if dashDashIdx != -1 {
				numTrailing := len(os.Args) - 1 - dashDashIdx
				if numTrailing > len(args) {
					numTrailing = len(args)
				}
				packages = args[:len(args)-numTrailing]
				configureOptions = args[len(args)-numTrailing:]
			} else {
				packages = args
			}

			var ociRegPtr *string
			if cmd.Flags().Changed("oci-registry") {
				ociRegPtr = &ociRegistry
			}
			var emitOciPtr *string
			if cmd.Flags().Changed("emit-oci") {
				emitOciPtr = &emitOci
			}

			installArgs := &InstallArgs{
				Packages:           packages,
				ConfigureOptions:   configureOptions,
				SkipEnable:         skipEnable,
				InstallSystemDeps:  installSystemDeps,
				CleanupBuildDeps:   cleanupBuildDeps,
				Verify:             verify,
				IgnorePlatformReqs: ignorePlatformReqs,
				Jobs:               jobs,
				PreferPrebuilt:     preferPrebuilt,
				OciRegistry:        ociRegPtr,
				EmitOci:            emitOciPtr,
				Php:                phpTarget(cmd),
			}

			return RunInstall(installArgs, mode)
		},
	}

	flags := cmd.Flags()
	flags.BoolVar(&skipEnable, "skip-enable", false, "Skip writing the php.ini snippet that enables the extension")
	flags.BoolVar(&installSystemDeps, "install-system-deps", false, "Automatically install system build dependencies via apt/apk when inside a Docker image")
	flags.BoolVar(&cleanupBuildDeps, "cleanup-build-deps", false, "Remove build-only system packages after a successful build (Docker)")
	flags.Var(&verify, "verify", "How to verify the integrity of downloaded artifacts (warn, enforce, attest, skip)")
	flags.BoolVar(&ignorePlatformReqs, "ignore-platform-reqs", false, "Do not fail when the target PHP does not satisfy a package's requirement")
	flags.IntVarP(&jobs, "jobs", "J", 1, "Build up to this many extensions concurrently")
	flags.BoolVar(&preferPrebuilt, "prefer-prebuilt", false, "Prefer a prebuilt .so from the OCI cache when one exists")
	flags.StringVar(&ociRegistry, "oci-registry", "", "OCI registry + namespace holding prebuilt extensions")
	flags.StringVar(&emitOci, "emit-oci", "", "After a successful build, emit an OCI artifact into this directory")

	return cmd
}

func RunInstall(args *InstallArgs, mode Mode) error {
	if len(args.ConfigureOptions) > 0 && len(args.Packages) > 1 {
		return fmt.Errorf("configure options (after `--`) can only be used when installing a single extension, but %d were requested", len(args.Packages))
	}

	plat, err := args.Php.Resolve()
	if err != nil {
		return err
	}

	client := resolver.NewPackagistClient()

	tsStr := "NTS"
	if plat.PHP.ThreadSafe {
		tsStr = "ZTS"
	}
	fmt.Printf("Target PHP: %s %s (%s) from %s\n",
		plat.PHP.Version.String(),
		tsStr,
		plat.OSFamily.Token(),
		plat.PHP.Path,
	)

	total := len(args.Packages)
	multiple := total > 1
	var failures []struct {
		name string
		err  error
	}

	loaded, err := plat.PHP.LoadedExtensionsWithVersions()
	if err != nil {
		loaded = nil
	}

	var targets []*Target
	se := style.ForStderr()
	so := style.ForStdout()

	for _, spec := range args.Packages {
		t, err := classify(spec, client, plat, loaded, args)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s %s: %v\n", se.Red("failed:"), spec, err)
			failures = append(failures, struct {
				name string
				err  error
			}{name: spec, err: err})
		} else {
			switch t.isBundled {
			case false:
				fmt.Printf("%s %s:%s (provides ext-%s)\n",
					so.Green("Found package:"), t.pkg.Name, t.pkg.Version, t.pkg.ExtensionName)
			case true:
				fmt.Printf("%s %s (bundled with PHP, via docker-php-ext-install)\n",
					so.Green("Found bundled extension:"), t.bundled)
			}
			targets = append(targets, t)
		}
	}

	cleanup := provisionSystemDeps(targets, args)

	wantParallel := args.Jobs > 1 && mode == ModeInstall

	var parallelIdx []int
	var sequentialIdx []int
	for i, t := range targets {
		if wantParallel && !t.isBundled {
			parallelIdx = append(parallelIdx, i)
		} else {
			sequentialIdx = append(sequentialIdx, i)
		}
	}

	// Sequential targets first
	for _, idx := range sequentialIdx {
		t := targets[idx]
		if multiple {
			fmt.Printf("\n%s [%d/%d] %s\n",
				so.BoldCyan("==>"), idx+1, len(targets), t.label())
		}
		var err error
		if t.isBundled {
			err = installBundled(t.bundled, plat, mode)
		} else {
			err = buildAndInstallInner(t.pkg, plat, args, mode, plat.MakeParallelJobs, os.Stdout, false)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s %s: %v\n", se.Red("failed:"), t.label(), err)
			failures = append(failures, struct {
				name string
				err  error
			}{name: t.label(), err: err})
		}
	}

	// Parallel targets
	if len(parallelIdx) > 0 {
		results := runTargetsParallel(targets, parallelIdx, plat, args, mode)
		for _, res := range results {
			label := targets[res.idx].label()
			fmt.Printf("\n%s %s\n", so.BoldCyan("==>"), label)
			fmt.Fprint(os.Stdout, res.output)
			if res.err != nil {
				fmt.Fprintf(os.Stderr, "%s %s: %v\n", se.Red("failed:"), label, res.err)
				failures = append(failures, struct {
					name string
					err  error
				}{name: label, err: res.err})
			}
		}
	}

	if cleanup != nil && len(cleanup.buildDeps) > 0 {
		pm := docker.PackageManagerForDistro(cleanup.distro)
		if err := pm.Remove(cleanup.buildDeps); err == nil {
			fmt.Printf("%s build-only packages\n", so.Green("Cleaned up"))
		} else {
			fmt.Fprintf(os.Stderr, "%s could not remove build deps: %v\n",
				se.Yellow("warning:"), err)
		}
	}

	if multiple {
		ok := total - len(failures)
		fmt.Printf("\n%s %d/%d extensions succeeded.\n",
			so.Bold("Finished."), ok, total)
	}

	if len(failures) > 0 {
		var names []string
		for _, f := range failures {
			names = append(names, f.name)
		}
		return fmt.Errorf("%d of %d extension(s) failed: %s",
			len(failures), total, strings.Join(names, ", "))
	}

	return nil
}

type Target struct {
	isBundled bool
	pkg       *resolver.ResolvedPackage
	bundled   string
}

func (t *Target) label() string {
	if t.isBundled {
		return t.bundled
	}
	return t.pkg.Name
}

func classify(
	spec string,
	client *resolver.PackagistClient,
	plat *platform.TargetPlatform,
	loaded []platform.ExtensionVersion,
	args *InstallArgs,
) (*Target, error) {
	if docker.LooksLikeBundledName(spec) {
		if docker.IsBundled(spec) {
			return &Target{isBundled: true, bundled: spec}, nil
		}
		return nil, fmt.Errorf("`%s` is not a bundled PHP extension. For a third-party extension, use its Packagist name, e.g. `vendor/%s`.", spec, spec)
	}

	request, err := resolver.ParseRequest(spec)
	if err != nil {
		return nil, err
	}
	resolved, err := resolver.Resolve(client, request, plat)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", request.Name, err)
	}

	err = checkPlatformRequirements(resolved, plat, loaded, args.IgnorePlatformReqs)
	if err != nil {
		return nil, err
	}

	return &Target{isBundled: false, pkg: resolved}, nil
}

func checkPlatformRequirements(
	resolved *resolver.ResolvedPackage,
	plat *platform.TargetPlatform,
	loaded []platform.ExtensionVersion,
	ignore bool,
) error {
	statuses := resolver.CheckRequirements(resolved.Requires, plat, loaded)
	var blocking []resolver.RequirementStatus
	for _, s := range statuses {
		if s.IsBlocking() {
			blocking = append(blocking, s)
		}
	}

	if len(blocking) == 0 {
		return nil
	}

	se := style.ForStderr()
	for _, s := range blocking {
		detail := ""
		switch s.Satisfaction.State {
		case resolver.Missing:
			detail = "not installed"
		case resolver.VersionMismatch:
			detail = fmt.Sprintf("you have %s", s.Satisfaction.Installed)
		}

		fmt.Fprintf(os.Stderr, "  %s requires %s %s (%s)\n",
			se.Red("✗"), s.Name, s.Constraint, detail)
	}

	if ignore {
		fmt.Fprintf(os.Stderr, "  %s proceeding anyway (--ignore-platform-reqs); the build may fail\n",
			se.Yellow("warning:"))
		return nil
	}

	return fmt.Errorf("`%s` is not compatible with the target PHP %s. Pass --ignore-platform-reqs to try anyway.",
		resolved.Name, plat.PHP.Version.String())
}

func writeIniOutcome(out io.Writer, ini *install.IniOutcome, extensionName string) {
	so := style.ForStdout()
	switch ini.Kind {
	case install.IniWritten:
		fmt.Fprintf(out, "%s enabled via %s\n", so.Green("✅"), ini.Path)
	case install.IniAlreadyEnabled:
		fmt.Fprintf(out, "%s already enabled and loaded\n", so.Green("✅"))
	case install.IniSkipped:
		fmt.Fprintln(out, "INI setup skipped (--skip-enable)")
	case install.IniNoSuitableLocation:
		fmt.Fprintf(out, "%s could not find an INI location; enable `%s` manually\n",
			so.Yellow("⚠"), extensionName)
	}
}

func installBundled(
	name string,
	plat *platform.TargetPlatform,
	mode Mode,
) error {
	if mode != ModeInstall {
		fmt.Printf("%s `%s` is a bundled extension; `download`/`build` do not apply (installs via docker-php-ext-install).\n",
			style.ForStdout().Yellow("note:"), name)
		return nil
	}

	ini, err := docker.InstallBundled(name, plat.MakeParallelJobs)
	if err != nil {
		return fmt.Errorf("installing bundled extension %s: %w", name, err)
	}

	fmt.Printf("%s %s (bundled)\n", style.ForStdout().Green("Install complete:"), name)
	if ini != "" {
		fmt.Printf("%s enabled via %s\n", style.ForStdout().Green("✅"), ini)
	} else {
		fmt.Printf("%s installed; enable it if PHP does not load it automatically\n",
			style.ForStdout().Yellow("⚠"))
	}
	return nil
}

type CleanupDeps struct {
	distro    *docker.Distro
	buildDeps []string
}

func provisionSystemDeps(targets []*Target, args *InstallArgs) *CleanupDeps {
	distro := docker.DetectDistro()
	if distro == nil {
		return nil
	}

	var perExt []docker.ResolvedSystemDeps
	for _, target := range targets {
		var resolved *docker.ResolvedSystemDeps
		if !target.isBundled {
			resolved = docker.ResolveSystemDeps(target.pkg.ExtensionName, target.pkg.LibRequires, distro)
		} else {
			resolved = docker.ResolveSystemDeps(target.bundled, nil, distro)
		}
		if resolved != nil {
			perExt = append(perExt, *resolved)
		}
	}

	if len(perExt) == 0 {
		return nil
	}

	var allDeps []docker.SystemDeps
	for _, r := range perExt {
		allDeps = append(allDeps, r.Deps)
	}
	merged := docker.MergeSystemDeps(allDeps)

	fromPackagist := false
	for _, r := range perExt {
		if r.FromPackagist {
			fromPackagist = true
			break
		}
	}

	source := "embedded catalog"
	if fromPackagist {
		source = "Packagist lib-* requires + catalog"
	}

	se := style.ForStderr()
	so := style.ForStdout()

	if args.InstallSystemDeps {
		err := docker.InstallSystemDeps(&merged, distro)
		if err == nil {
			fmt.Printf("%s system dependencies for %s (%s)\n",
				so.Green("Installed"), distro.Label(), source)
		} else {
			fmt.Fprintf(os.Stderr, "%s installing system dependencies: %v\n",
				se.Yellow("warning:"), err)
		}
	} else {
		var all []string
		all = append(all, merged.Persistent...)
		all = append(all, merged.BuildOnly...)
		fmt.Printf("%s this batch needs system packages (%s): %s. Re-run with --install-system-deps to install them automatically.\n",
			so.Yellow("note:"), source, strings.Join(all, ", "))
	}

	if args.CleanupBuildDeps {
		return &CleanupDeps{
			distro:    distro,
			buildDeps: merged.BuildOnly,
		}
	}
	return nil
}

type parallelResult struct {
	idx    int
	output string
	err    error
}

func runTargetsParallel(
	targets []*Target,
	parallelIdx []int,
	plat *platform.TargetPlatform,
	args *InstallArgs,
	mode Mode,
) []parallelResult {
	jobs := args.Jobs
	if jobs < 1 {
		jobs = 1
	}
	perBuildMakeJobs := makeJobsPerBuild(plat.MakeParallelJobs, jobs)

	fmt.Printf("\n%s building %d extension(s) with up to %d concurrent job(s) (make -j%d each)\n",
		style.ForStdout().BoldCyan("==>"), len(parallelIdx), jobs, perBuildMakeJobs)

	workChan := make(chan int, len(parallelIdx))
	for _, idx := range parallelIdx {
		workChan <- idx
	}
	close(workChan)

	resultsChan := make(chan parallelResult, len(parallelIdx))

	var wg sync.WaitGroup
	numWorkers := jobs
	if len(parallelIdx) < numWorkers {
		numWorkers = len(parallelIdx)
	}

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range workChan {
				target := targets[idx]
				var outBuf bytes.Buffer
				err := buildAndInstallInner(
					target.pkg,
					plat,
					args,
					mode,
					perBuildMakeJobs,
					&outBuf,
					true,
				)
				resultsChan <- parallelResult{
					idx:    idx,
					output: outBuf.String(),
					err:    err,
				}
			}
		}()
	}

	wg.Wait()
	close(resultsChan)

	var results []parallelResult
	for res := range resultsChan {
		results = append(results, res)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].idx < results[j].idx
	})

	return results
}

func makeJobsPerBuild(availableCores, jobs int) int {
	if jobs < 1 {
		jobs = 1
	}
	val := availableCores / jobs
	if val < 1 {
		return 1
	}
	return val
}

func tryPrebuilt(
	resolved *resolver.ResolvedPackage,
	plat *platform.TargetPlatform,
	args *InstallArgs,
	configureOptions []string,
	out io.Writer,
) (bool, error) {
	registryStr := args.prebuiltRegistry()
	if registryStr == nil {
		return false, nil
	}
	distro := docker.DetectDistro()
	if distro == nil {
		return false, nil
	}

	cell := oci.NewCell(
		resolved.ExtensionName,
		resolved.Version,
		plat,
		distro,
		configureOptions,
	)
	registry, err := oci.ParseRegistry(*registryStr)
	if err != nil {
		return false, err
	}

	prebuilt, err := oci.ResolvePrebuilt(registry, &cell)
	if err != nil {
		return false, err
	}
	if prebuilt == nil {
		fmt.Fprintf(out, "%s no prebuilt for %s — building from source\n",
			style.ForStdout().Yellow("cache miss:"), cell.ID())
		return false, nil
	}

	if prebuilt.Manifest.PHPAPI != plat.PHP.APIVersion {
		fmt.Fprintf(out, "%s prebuilt PHP API %s != target %s — building from source\n",
			style.ForStdout().Yellow("cache miss:"), prebuilt.Manifest.PHPAPI, plat.PHP.APIVersion)
		return false, nil
	}

	fmt.Fprintf(out, "%s %s (%d bytes)\n",
		style.ForStdout().Green("Using prebuilt:"), cell.ID(), len(prebuilt.SoBytes))

	runtime := prebuilt.Manifest.RuntimePackagesFor(distro.FamilyToken())
	if len(runtime) > 0 {
		if args.InstallSystemDeps {
			deps := docker.SystemDeps{
				Persistent: runtime,
				BuildOnly:  nil,
			}
			if err := docker.InstallSystemDeps(&deps, distro); err != nil {
				return false, fmt.Errorf("installing prebuilt runtime packages: %w", err)
			}
			fmt.Fprintf(out, "%s runtime packages: %s\n",
				style.ForStdout().Green("Installed"), strings.Join(runtime, ", "))
		} else {
			fmt.Fprintf(out, "%s prebuilt needs runtime packages: %s. Re-run with --install-system-deps.\n",
				style.ForStdout().Yellow("note:"), strings.Join(runtime, ", "))
		}
	}

	tmp, err := os.CreateTemp("", "rpie-prebuilt-*.so")
	if err != nil {
		return false, fmt.Errorf("staging prebuilt .so: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(prebuilt.SoBytes); err != nil {
		tmp.Close()
		return false, fmt.Errorf("writing prebuilt .so: %w", err)
	}
	tmp.Close()

	built := buildpkg.FromPrebuilt(tmpPath)

	outcome, err := install.Install(resolved, &built, plat, !args.SkipEnable)
	if err != nil {
		return false, fmt.Errorf("installing prebuilt extension: %w", err)
	}

	fmt.Fprintf(out, "%s %s (prebuilt, no compilation)\n",
		style.ForStdout().Green("Install complete:"), outcome.InstalledSo)

	writeIniOutcome(out, &outcome.Ini, resolved.ExtensionName)

	return true, nil
}

func emitOciArtifact(
	resolved *resolver.ResolvedPackage,
	plat *platform.TargetPlatform,
	configureOptions []string,
	soPath string,
	dir string,
	out io.Writer,
) error {
	distro := docker.DetectDistro()
	if distro == nil {
		return fmt.Errorf("--emit-oci requires running inside a Linux distro (Docker image)")
	}

	cell := oci.NewCell(
		resolved.ExtensionName,
		resolved.Version,
		plat,
		distro,
		configureOptions,
	)

	soBytes, err := os.ReadFile(soPath)
	if err != nil {
		return fmt.Errorf("reading built .so: %w", err)
	}

	sum := sha256.Sum256(soBytes)
	soSha256 := hex.EncodeToString(sum[:])
	soFile := resolved.ExtensionName + ".so"

	runtimePackages := make(map[string][]string)
	deps := docker.ResolveSystemDeps(resolved.ExtensionName, resolved.LibRequires, distro)
	if deps != nil && len(deps.Deps.Persistent) > 0 {
		pm := docker.PackageManagerForDistro(distro)
		concrete := pm.ResolveRuntimePackages(deps.Deps.Persistent)
		if len(concrete) > 0 {
			runtimePackages[distro.FamilyToken()] = concrete
		}
	}

	manifest := oci.ExtManifest{
		ManifestVersion:  oci.ManifestVersion,
		Extension:        resolved.ExtensionName,
		Version:          resolved.Version,
		ExtensionType:    resolved.ExtensionType.ComposerType(),
		IniDirective:     resolved.ExtensionType.IniDirective(),
		Priority:         resolved.Priority,
		Cell:             cell.ID(),
		PHP:              cell.PHP,
		PHPAPI:           plat.PHP.APIVersion,
		Distro:           cell.Distro,
		Arch:             cell.Arch,
		ThreadSafety:     cell.TSToken(),
		Debug:            cell.Debug,
		ConfigureOptions: configureOptions,
		RuntimePackages:  runtimePackages,
		SoFile:           soFile,
		SoSha256:         soSha256,
		SourceRef:        fmt.Sprintf("%s@%s", resolved.Name, resolved.Version),
		Builder:          "rpie",
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating --emit-oci dir: %w", err)
	}

	configJson, err := manifest.ToJSON()
	if err != nil {
		return fmt.Errorf("serializing manifest to JSON: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), configJson, 0644); err != nil {
		return fmt.Errorf("writing config.json: %w", err)
	}

	layer, err := buildLayerTar(soFile, soBytes)
	if err != nil {
		return fmt.Errorf("building layer tar: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "layer.tar.gz"), layer, 0644); err != nil {
		return fmt.Errorf("writing layer.tar.gz: %w", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "cell.txt"), []byte(cell.ID()), 0644); err != nil {
		return fmt.Errorf("writing cell.txt: %w", err)
	}

	fmt.Fprintf(out, "%s OCI artifact for %s → %s\n",
		style.ForStdout().Green("Emitted"), cell.ID(), dir)

	return nil
}

func buildAndInstallInner(
	resolved *resolver.ResolvedPackage,
	plat *platform.TargetPlatform,
	args *InstallArgs,
	mode Mode,
	makeJobs int,
	out io.Writer,
	capture bool,
) error {
	configureOptions, err := buildConfigureOptions(resolved, args.ConfigureOptions, out)
	if err != nil {
		return err
	}

	if mode == ModeInstall {
		ok, err := tryPrebuilt(resolved, plat, args, configureOptions, out)
		if err != nil {
			fmt.Fprintf(out, "%s prebuilt lookup failed, building from source: %v\n",
				style.ForStdout().Yellow("note:"), err)
		} else if ok {
			return nil
		}
	}

	downloaded, err := download.Download(resolved, plat, args.Verify.Policy())
	if err != nil {
		return fmt.Errorf("downloading artifact: %w", err)
	}
	defer downloaded.Close()

	switch downloaded.Artifact.Kind {
	case download.ArtifactSource:
		fmt.Fprintf(out, "%s %s\n",
			style.ForStdout().Green("Extracted source to:"), downloaded.Artifact.Path)
	case download.ArtifactBinary:
		fmt.Fprintf(out, "%s %s\n",
			style.ForStdout().Green("Downloaded pre-built binary:"), downloaded.Artifact.Path)
	case download.ArtifactWindowsDll:
		fmt.Fprintf(out, "%s %s\n",
			style.ForStdout().Green("Downloaded pre-compiled DLL:"), downloaded.Artifact.Path)
	}

	if mode == ModeDownloadOnly {
		downloaded.Keep()
		path := downloaded.Artifact.Path
		if downloaded.Artifact.Kind == download.ArtifactWindowsDll {
			path = downloaded.Artifact.ExtractedDir
		}
		fmt.Fprintf(out, "Left at: %s\n", path)
		return nil
	}

	if dll, extractedDir, ok := downloaded.WindowsDll(); ok {
		if mode == ModeBuildOnly {
			fmt.Fprintf(out, "%s `%s` ships a pre-compiled DLL; nothing to build.\n",
				style.ForStdout().Yellow("note:"), resolved.Name)
			return nil
		}
		outcome, err := install.InstallWindows(resolved, dll, extractedDir, plat, !args.SkipEnable)
		if err != nil {
			return fmt.Errorf("installing Windows extension: %w", err)
		}
		fmt.Fprintf(out, "%s %s\n",
			style.ForStdout().Green("Install complete:"), outcome.InstalledSo)
		writeIniOutcome(out, &outcome.Ini, resolved.ExtensionName)
		return nil
	}

	var built buildpkg.BuiltExtension
	if so, ok := downloaded.BinaryPath(); ok {
		if len(args.ConfigureOptions) > 0 {
			fmt.Fprintf(out, "%s ignoring configure options — `%s` installs a pre-packaged binary\n",
				style.ForStdout().Yellow("warning:"), resolved.Name)
		}
		built = buildpkg.FromPrebuilt(so)
	} else {
		var sink *bytes.Buffer
		if capture {
			if b, ok := out.(*bytes.Buffer); ok {
				sink = b
			}
		}

		built, err = buildpkg.BuildWith(resolved, downloaded, plat, configureOptions, makeJobs, sink)
		if err != nil {
			return fmt.Errorf("building extension: %w", err)
		}
		fmt.Fprintf(out, "%s %s\n",
			style.ForStdout().Green("Build complete:"), built.BinaryPath)
	}

	if args.EmitOci != nil && *args.EmitOci != "" {
		if err := emitOciArtifact(resolved, plat, configureOptions, built.BinaryPath, *args.EmitOci, out); err != nil {
			return fmt.Errorf("emitting OCI artifact: %w", err)
		}
	}

	if mode == ModeBuildOnly {
		if _, ok := downloaded.BinaryPath(); ok {
			fmt.Fprintf(out, "%s `%s` provides a pre-built binary; nothing to build.\n",
				style.ForStdout().Yellow("note:"), resolved.Name)
		}
		return nil
	}

	outcome, err := install.Install(resolved, &built, plat, !args.SkipEnable)
	if err != nil {
		return fmt.Errorf("installing extension: %w", err)
	}

	fmt.Fprintf(out, "%s %s\n",
		style.ForStdout().Green("Install complete:"), outcome.InstalledSo)
	writeIniOutcome(out, &outcome.Ini, resolved.ExtensionName)

	return nil
}

func buildConfigureOptions(
	resolved *resolver.ResolvedPackage,
	userOptions []string,
	out io.Writer,
) ([]string, error) {
	declared := resolved.Metadata.ConfigureOptions
	for _, opt := range userOptions {
		name := strings.TrimPrefix(opt, "--")
		if parts := strings.SplitN(name, "=", 2); len(parts) > 0 {
			name = parts[0]
		}
		found := false
		for _, d := range declared {
			if d.Name == name {
				found = true
				break
			}
		}
		if !found && len(declared) > 0 {
			fmt.Fprintf(out, "%s `--%s` is not a declared configure option for %s\n",
				style.ForStdout().Yellow("warning:"), name, resolved.Name)
		}
	}
	return userOptions, nil
}

func buildLayerTar(name string, data []byte) ([]byte, error) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	hdr := &tar.Header{
		Name: name,
		Mode: 0644,
		Size: int64(len(data)),
	}

	if err := tw.WriteHeader(hdr); err != nil {
		return nil, fmt.Errorf("writing tar header: %w", err)
	}
	if _, err := tw.Write(data); err != nil {
		return nil, fmt.Errorf("writing tar data: %w", err)
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("closing tar writer: %w", err)
	}
	if err := gw.Close(); err != nil {
		return nil, fmt.Errorf("closing gzip writer: %w", err)
	}

	return buf.Bytes(), nil
}
