# rpie Go port — architecture & package contracts

This document is the **hard contract** for the Go port of rpie. Parallel
implementers code against the signatures below without coordinating. If a
signature here disagrees with your reading of a spec, **this document wins**
(escalate rather than silently change a signature). Behavior (output strings,
exit codes, error text) is owned by the per-module specs in
`scratchpad/specs/*.md`; this document owns structure, names, and types.

- Module path: `github.com/shyim/go-pie`
- go.mod lives at the repo root (next to `Cargo.toml`; the Rust tree stays).
- Binary: `cmd/rpie/main.go`. Libraries: `internal/...`.
- Version constant: `0.1.0` (matches `Cargo.toml`; `rpie --version` prints
  `rpie 0.1.0`).

## 1. Package layout

```
cmd/rpie            main() — os.Exit(commands.Execute())
cmd/rpie/cli_test.go  integration tests (port of tests/cli.rs)
internal/version    version + User-Agent constants
internal/style      TTY-gated ANSI styling helper (replaces console crate)
internal/procutil   subprocess helpers (util/process.rs)
internal/sudo       sudo decision logic (util/sudo.rs)
internal/platform   platform introspection (src/platform/*)
internal/resolver   Packagist resolution + constraint matcher (src/resolver/*)
internal/docker     distro / system-deps / bundled (src/docker/*, embeds system-deps.json)
internal/oci        prebuilt-cache OCI client (src/oci/*)
internal/download   artifact download/extract/verify (src/download/*)
internal/buildpkg   phpize/configure/make pipeline (src/build/mod.rs)
internal/install    .so placement + INI markers (src/install/*)
internal/commands   cobra CLI surface + all subcommand runners (src/main.rs, src/commands/*)
```

`buildpkg` is deliberately not named `build` to avoid confusion with CI
`build/` directories and `go/build`; the Go package name is `buildpkg`.

### Dependency graph (arrows = imports)

```
version   <- resolver, oci, download, commands
style     <- download, commands
procutil  <- platform, docker, buildpkg, install
platform  <- sudo, resolver, docker(none), oci, download, buildpkg, install, commands
sudo      <- install
resolver  <- download, buildpkg, install, oci(none), commands
docker    <- oci, commands
oci       <- commands
download  <- buildpkg, commands
buildpkg  <- install, commands
install   <- commands
commands  <- cmd/rpie
```

No cycles: `platform` implements its own private `runPhp` helper (its error
format differs from `procutil.Capture`) but imports `procutil` only for
`FormatExitStatus`/`DebugQuotedArgs`. `docker` does **not** import `platform`.

### Implementation order

1. `internal/version`, `internal/style`, `internal/procutil` (no deps)
2. `internal/platform`, then `internal/sudo`
3. `internal/resolver`, `internal/docker` (independent of each other)
4. `internal/oci`, `internal/download` (independent of each other)
5. `internal/buildpkg`, then `internal/install`
6. `internal/commands`, then `cmd/rpie` + integration tests

Packages at the same step can be built in parallel; every package at step N
only imports packages from steps < N (plus `version`/`style`/`procutil`).

## 2. Shared conventions

### 2.1 Errors

- **Wrapping**: `fmt.Errorf("<context>: %w", err)` for every anyhow
  `.context(...)`. anyhow's `{:#}` alternate rendering (contexts joined by
  `": "`, root cause last) is exactly what `err.Error()` of a chain of
  `%w`-wrapped errors produces. Context strings must be byte-identical to the
  spec.
- **Leaf errors** (`bail!`): `fmt.Errorf(...)` / `errors.New(...)` with the
  exact message. Backticks, em dashes (`—` U+2014), and emoji are literal.
- **No sentinel errors / typed errors** are needed for parity: the Rust code
  never matches on error types, only on strings at print time. Do not invent
  `errors.Is/As` taxonomies. The one behavioral "typed" distinction —
  *miss vs error* in `oci` — is expressed as `(nil, nil)` returns, not error
  values.
- **Exit-status rendering**: Rust `ExitStatus` displays `exit status: 1`
  (colon); Go's `ExitError` prints `exit status 1`. All subprocess error
  strings go through `procutil.FormatExitStatus`, which produces the **Rust**
  form `exit status: N`, and `signal: N (SIGNAME)` for signal deaths
  (approximation; see open questions). Never embed `%v` of an `*exec.ExitError`
  in user-visible text.
- **Debug formatting** of arg slices in errors: `procutil.DebugQuotedArgs`
  produces the Rust form `["-i", "-n"]` (quoted, comma-space joined). For
  single strings use `%q` (equivalent to Rust `{:?}`).
- **CLI error mapping** (owned by `internal/commands`):
  - runtime error → stderr `error: <chain>` (`error:` red+bold via
    `style.ForStderr()`), exit **1**;
  - usage/flag-parse error → cobra's message + usage to stderr, exit **2**;
  - `--help` / `--version` → stdout, exit **0**.

### 2.2 stdout/stderr and styling

- All styling goes through `internal/style`. ANSI is emitted iff the
  *destination stream* is a terminal (`term.IsTerminal`) and `NO_COLOR` is
  unset/empty. Lines written into a per-extension capture buffer are styled
  with the **stdout** styler (they are flushed to stdout).
- Piped output (all parity tests) must contain plain strings, no escapes.

### 2.3 HTTP

- `net/http` everywhere. Each HTTP-using package declares a package-level
  `var httpClient = &http.Client{}` — default client semantics: follows
  redirects (like ureq's ≤5; Go's 10-hop limit is acceptable), **no overall
  timeout** (matches ureq defaults; do not add one).
- Every request sets `User-Agent`. Packagist uses
  `version.PackagistUserAgent`; everything else `version.UserAgent`.
- Non-2xx is an error unless a spec says otherwise (oci `GetIndex` 404/401/403
  miss; github 404 special-case).

### 2.4 Maps and ordering

Rust `BTreeMap` iteration order is user-visible. Every exported
`map[string]T` in these contracts comes with the rule: **sort keys before any
iteration whose effect is observable** (output lines, first-match lookups,
JSON is fine — `encoding/json` sorts map keys). `install.ManagedKeys` is
provided as the shared sorted-keys helper for the managed map.

### 2.5 Concurrency

Keep the port sequential except the two places the Rust code is concurrent:

1. `platform.PhpBinaryFromPath` runs `php -i` and `php -n -r <script>`
   concurrently (two goroutines + `sync.WaitGroup`).
2. `commands` parallel build pool: buffered `chan int` of indices (filled then
   closed), `min(jobs, N)` worker goroutines, `sync.WaitGroup`,
   mutex-guarded results slice, results **sorted by original index** before
   reporting.

No other goroutines. No contexts/cancellation (the Rust code has none).

### 2.6 Rust `str::lines()` helper

Marker parsing/removal in `install` depends on Rust `lines()` semantics
(split on `\n`, strip one trailing `\r` per line, no trailing empty element
for a final newline). Implement a private helper in `install`:

```go
func rustLines(s string) []string
```

Do not reuse `strings.Split(s, "\n")` directly anywhere those semantics leak
into output.

---

## 3. Package contracts (exported API)

Everything not listed here is unexported. Signatures are the contract;
behavior references are to the per-module specs.

### 3.1 `internal/version`

```go
package version

const Version = "0.1.0"
const UserAgent = "rpie/" + Version                                  // "rpie/0.1.0"
const PackagistUserAgent = UserAgent + " (+https://github.com/shyim/go-pie)"
```

### 3.2 `internal/style`

```go
package style

type Styler struct{ /* enabled bool */ }

func ForStdout() Styler // enabled iff stdout is a TTY and NO_COLOR unset
func ForStderr() Styler // enabled iff stderr is a TTY and NO_COLOR unset
func New(enabled bool) Styler // for tests

// Each returns text wrapped in ANSI codes when enabled, else text unchanged.
func (s Styler) Red(text string) string
func (s Styler) RedBold(text string) string
func (s Styler) Green(text string) string
func (s Styler) Yellow(text string) string
func (s Styler) Cyan(text string) string
func (s Styler) BoldCyan(text string) string
func (s Styler) Bold(text string) string
func (s Styler) Dim(text string) string
func (s Styler) BoldUnderlined(text string) string
```

ANSI codes: red `31`, green `32`, yellow `33`, cyan `36`, bold `1`, dim `2`,
underline `4`; reset `0`. Combined codes as `\x1b[1;36m` etc.

### 3.3 `internal/procutil`

```go
package procutil

// Run spawns program with args in cwd, stdio inherited.
// Spawn failure: fmt.Errorf("failed to spawn `%s` for %s: %w", program, label, err)
// Non-zero exit: fmt.Errorf("%s failed: `%s %s` exited with %s",
//                label, program, strings.Join(args, " "), FormatExitStatus(state))
func Run(program string, args []string, cwd string, label string) error

// RunCaptured captures stdout+stderr into sink (stdout appended first, then
// stderr). On non-zero exit it appends the failure line + "\n" to sink AND
// returns the same message (without trailing newline) as the error.
func RunCaptured(program string, args []string, cwd string, label string, sink *bytes.Buffer) error

// Capture runs program (inherited env, no cwd override), returns trimmed stdout.
// Spawn failure: "failed to spawn `%s`"; non-zero exit:
// fmt.Errorf("`%s %s` exited with %s: %s", program, join(args), status, trimmedStderr)
func Capture(program string, args []string) (string, error)

// FormatExitStatus renders like Rust's ExitStatus Display:
// "exit status: 1", or "signal: 9 (SIGKILL)" for signal deaths.
func FormatExitStatus(state *os.ProcessState) string

// DebugQuotedArgs renders like Rust {:?} on a &[String]: `["-i", "-n"]`.
func DebugQuotedArgs(args []string) string
```

### 3.4 `internal/platform`

```go
package platform

type Architecture int

const (
	ArchX86 Architecture = iota
	ArchX86_64
	ArchArm64
)

func (a Architecture) Token() string  // "x86" | "x86_64" | "arm64"
func (a Architecture) String() string // == Token()
func ArchitectureFromPhpUname(machine string, intSizeBytes uint8) Architecture

type OperatingSystem int

const (
	OSLinux OperatingSystem = iota
	OSDarwin
	OSWindows
	OSOther
)

type OperatingSystemFamily int

const (
	FamilyLinux OperatingSystemFamily = iota
	FamilyDarwin
	FamilyWindows
	FamilyBsd
	FamilySolaris
	FamilyUnknown
)

func (f OperatingSystemFamily) Token() string // "linux"|"darwin"|"windows"|"bsd"|"solaris"|"unknown"

type ThreadSafety int

const (
	ThreadSafe ThreadSafety = iota
	NonThreadSafe
)

func (t ThreadSafety) Token() string  // "zts" | "nts"
func (t ThreadSafety) String() string // Rust Debug names: "ThreadSafe" | "NonThreadSafe"
                                      // (embedded verbatim in resolver's thread-safety error)

type PhpVersion struct{ Major, Minor, Patch uint8 }

func (v PhpVersion) MajorMinor() string // "8.4"
func (v PhpVersion) String() string     // "8.4.3"

type WindowsCompiler int

const (
	Vc6 WindowsCompiler = iota
	Vc8
	Vc9
	Vc11
	Vc14
	Vc15
	Vs16
	Vs17
)

func WindowsCompilerFromToken(token string) (WindowsCompiler, bool)
func (c WindowsCompiler) Token() string // "vc6".."vc15","vs16","vs17"
func CompilerFromPhpinfo(phpinfo string) *WindowsCompiler // nil when absent/unrecognized

type LibcFlavour int

const (
	Glibc LibcFlavour = iota
	Musl
	NonLinux
)

func DetectLibc() LibcFlavour
func (l LibcFlavour) Token() string // "glibc"|"musl"|"anylibc"

type ExtensionVersion struct{ Name, Version string } // Name pre-lowercased; Version may be ""

type PhpBinary struct {
	Path            string
	Version         PhpVersion
	ThreadSafe      bool
	DebugBuild      bool
	Architecture    Architecture
	APIVersion      string // e.g. "20240924"
	ExtensionDir    string
	WindowsCompiler *WindowsCompiler
}

func PhpBinaryFromPathEnv() (*PhpBinary, error)
func PhpBinaryFromPath(path string) (*PhpBinary, error)
func PhpBinaryFixture(major, minor, patch uint8, arch Architecture) *PhpBinary // test helper (exported: used by resolver/oci tests)

func (p *PhpBinary) SharedObjectPath(extensionName string) string // <ExtensionDir>/<name>.so
func (p *PhpBinary) DllPath(extensionName string) string          // <ExtensionDir>/php_<name>.dll
func (p *PhpBinary) PhpDir() (string, bool)                       // parent dir of Path
func (p *PhpBinary) LoadedExtensions() ([]string, error)          // lowercased names
func (p *PhpBinary) LoadedExtensionsWithVersions() ([]ExtensionVersion, error)
func (p *PhpBinary) ExtensionIsLoaded(name string) (bool, error)

type PhpizePath struct{ Path string }

func GuessPhpize(php *PhpBinary) (*PhpizePath, error)
func ExplicitPhpize(path string) (*PhpizePath, error)

type TargetPlatform struct {
	OS               OperatingSystem
	OSFamily         OperatingSystemFamily
	PHP              PhpBinary
	Architecture     Architecture
	ThreadSafety     ThreadSafety
	MakeParallelJobs int
	Phpize           *PhpizePath // nil = not discovered (error surfaces at build time)
	PhpConfig        string      // "" = not found
}

// makeJobs nil → runtime.NumCPU(). phpize nil → GuessPhpize, error swallowed.
func TargetPlatformFromPhp(php *PhpBinary, makeJobs *int, phpize *PhpizePath) (*TargetPlatform, error)
func TargetPlatformFixture(os OperatingSystem, ts ThreadSafety, php *PhpBinary) *TargetPlatform // test helper

func IsRunningAsRoot() bool // os.Getuid() == 0 (false on Windows)
```

Note: the internal `runPhp` (spawn php, capture, error
`"{path} {args:?} exited with {status}: {stderr}"`) stays unexported;
its error text uses `procutil.DebugQuotedArgs` + `FormatExitStatus`.

### 3.5 `internal/sudo`

```go
package sudo

func IsAvailable() bool          // sudo on PATH AND (root OR stdout is a TTY)
func PathNeedsSudo(path string) bool
```

Unix writability via `golang.org/x/sys/unix.Access(path, unix.W_OK)` behind
`//go:build unix`; non-Unix fallback per spec.

### 3.6 `internal/resolver`

```go
package resolver

type RequestedPackage struct {
	Name       string
	Constraint string // "" = none
}

func ParseRequest(input string) (*RequestedPackage, error)

type PackageVersion struct {
	Version           string
	VersionNormalized string
	PackageType       string
	DistURL           *string
	DistType          *string
	DistShasum        *string // present-but-empty is a non-nil ""
	SourceURL         *string
	LibRequires       []string          // "lib-" stripped, lowercased, sorted-key order
	Requires          map[string]string // iterate sorted
	PhpExt            json.RawMessage   // nil when absent
}

type PackageSource interface {
	PackageVersions(pkg string) ([]PackageVersion, error)
}

type PackagistClient struct{ /* base string = "https://repo.packagist.org/p2" */ }

func NewPackagistClient() *PackagistClient
func (c *PackagistClient) PackageVersions(pkg string) ([]PackageVersion, error)

// ParseVersionsJSON parses a Packagist p2 body (exported for fixture tests).
func ParseVersionsJSON(body []byte, pkg string) ([]PackageVersion, error)

type ExtensionType int

const (
	PhpModule ExtensionType = iota
	ZendExtension
)

func (t ExtensionType) String() string       // "PhpModule" | "ZendExtension" (info output)
func (t ExtensionType) IniDirective() string // "extension" | "zend_extension"
func (t ExtensionType) ComposerType() string // "php-ext" | "php-ext-zend" (emit-oci)
func ExtensionTypeFromComposerType(s string) (ExtensionType, bool)

type DownloadUrlMethod int

const (
	ComposerDefault DownloadUrlMethod = iota
	PrePackagedSource
	PrePackagedBinary
	WindowsBinary
)

func (m DownloadUrlMethod) String() string // Rust Debug names: "ComposerDefault", ...
func (m DownloadUrlMethod) Label() string  // "composer-default", "pre-packaged-source",
                                           // "pre-packaged-binary", "windows-binary"

type ConfigureOption struct {
	Name        string
	NeedsValue  bool
	Description *string
}

type PhpExtMetadata struct {
	ExtensionName      string
	ConfigureOptions   []ConfigureOption
	BuildPath          *string
	SupportZts         bool // default true
	SupportNts         bool // default true
	OsFamilies         []string
	OsFamiliesExclude  []string
	Priority           uint32 // default 80
	DownloadUrlMethods []DownloadUrlMethod // never empty; default [ComposerDefault]
}

func MetadataFromValue(phpExt json.RawMessage, packageName string) PhpExtMetadata
func (m *PhpExtMetadata) SupportsThreadSafety(ts platform.ThreadSafety) bool
func (m *PhpExtMetadata) SupportsOsFamily(f platform.OperatingSystemFamily) bool

type ResolvedPackage struct {
	Name          string // "vendor/name"
	Version       string // display version, e.g. "6.1.0"
	ExtensionName string // no "ext-" prefix
	ExtensionType ExtensionType
	Metadata      PhpExtMetadata
	DistURL       *string
	DistType      *string
	DistShasum    *string
	SourceURL     *string
	LibRequires   []string
	Requires      map[string]string
	Priority      uint32
}

func Resolve(client PackageSource, req *RequestedPackage, plat *platform.TargetPlatform) (*ResolvedPackage, error)

func VersionIsNewer(a, b string) bool
func ConstraintMatches(constraint, version string) bool // hand-ported matcher; NO semver lib

type RequirementKindType int

const (
	KindPhp RequirementKindType = iota
	KindExt
	KindLib
	KindOther
)

type RequirementKind struct {
	Type RequirementKindType
	Name string // Ext/Lib: lowercased suffix; Other: full original name; Php: ""
}

type SatisfactionState int

const (
	Satisfied SatisfactionState = iota
	VersionMismatch
	Missing
	Unknown
)

type Satisfaction struct {
	State     SatisfactionState
	Installed string // set only for VersionMismatch
}

type RequirementStatus struct {
	Name         string
	Constraint   string
	Kind         RequirementKind
	Satisfaction Satisfaction
}

func (r *RequirementStatus) IsBlocking() bool
func CheckRequirements(requires map[string]string, plat *platform.TargetPlatform,
	loaded []platform.ExtensionVersion) []RequirementStatus // sorted-key order of requires
```

### 3.7 `internal/docker`

`system-deps.json` is **copied verbatim** from `src/docker/system-deps.json`
into `internal/docker/system-deps.json` and embedded:

```go
//go:embed system-deps.json
var rawSystemDeps []byte
```

Parsed once via `sync.Once`; parse failure panics with
`"embedded system-deps.json must be valid"`.

```go
package docker

type DistroFamily int

const (
	FamilyDebian DistroFamily = iota
	FamilyAlpine
	FamilyOther
)

type Distro struct {
	ID        string
	VersionID string
	Family    DistroFamily
}

func DetectDistro() *Distro // nil off Linux or unreadable /etc/os-release
func (d *Distro) Label() string       // "debian@12"
func (d *Distro) FamilyToken() string // "debian"|"alpine"|"other"

func InOfficialPHPImage() bool // docker-php-ext-install AND docker-php-source on PATH

type SystemDeps struct {
	Persistent []string `json:"persistent"`
	BuildOnly  []string `json:"build_only"`
}

type ResolvedSystemDeps struct {
	ExtensionName string
	Deps          SystemDeps
	FromPackagist bool
}

func ResolveSystemDeps(extensionName string, libRequires []string, distro *Distro) *ResolvedSystemDeps
func MergeSystemDeps(all []SystemDeps) SystemDeps // first-seen-order dedup per list
func InstallSystemDeps(deps *SystemDeps, distro *Distro) error

type PackageManager int

const (
	PMApt PackageManager = iota
	PMApk
	PMUnsupported
)

func PackageManagerForDistro(d *Distro) PackageManager
func (pm PackageManager) String() string // "Apt" | "Apk" | "Unsupported" (info output)
func (pm PackageManager) Install(packages []string) error
func (pm PackageManager) Remove(packages []string) error // Unsupported: silent no-op
func (pm PackageManager) ResolveRuntimePackages(entries []string) []string
func IsPattern(entry string) bool // contains any of ^ [ ] * ( ) ?

// bundled.go
func LooksLikeBundledName(spec string) bool
func HelpersAvailable() bool // all three docker-php-* helpers on PATH
func IsBundled(name string) bool
func InstallBundled(name string, jobs int) (iniPath string, err error) // "" = INI not found
```

### 3.8 `internal/oci`

```go
package oci

const (
	ConfigMediaType = "application/vnd.rpie.ext.config.v1+json"
	LayerMediaType  = "application/vnd.rpie.ext.layer.v1.tar+gzip"
	ManifestVersion = uint32(1)
)

type Cell struct {
	Extension    string
	Version      string
	PHP          string // "8.4"
	Distro       string // "debian@12"
	Arch         string // "x86" | "x86_64" | "aarch64"
	ThreadSafety platform.ThreadSafety
	Debug        bool
	ConfigHash   string // 8 lowercase hex chars or "00000000"
}

func NewCell(extension, ver string, p *platform.TargetPlatform, d *docker.Distro, configureOptions []string) Cell
func (c *Cell) ID() string         // "<ext>/<ver>/php<M.m>/<distro>/<arch>/<nts|zts>/<debug|nodebug>/cfg-<hash>"
func (c *Cell) RepoTag() string    // "<ext>:<ver>"
func (c *Cell) TSToken() string    // "nts"|"zts"
func (c *Cell) DebugToken() string // "debug"|"nodebug"
func OCIArch(arch string) string   // x86_64→amd64, aarch64/arm64→arm64, x86→386, else passthrough

type Registry struct {
	Host      string
	Namespace string
	// insecure bool; token string — unexported
}

func ParseRegistry(s string) (*Registry, error)
func (r *Registry) GetIndex(ext, tag string) (map[string]any, error) // nil,nil on 404/401/403
func (r *Registry) GetManifest(ext, digest string) (map[string]any, error)
func (r *Registry) GetBlob(ext, digest string) ([]byte, error) // sha256-verified

// Declare struct fields in exactly this order (JSON field order parity).
type ExtManifest struct {
	ManifestVersion  uint32              `json:"rpieManifestVersion"`
	Extension        string              `json:"extension"`
	Version          string              `json:"version"`
	ExtensionType    string              `json:"extensionType"` // "php-ext"|"php-ext-zend"
	IniDirective     string              `json:"iniDirective"`  // "extension"|"zend_extension"
	Priority         uint32              `json:"priority"`
	Cell             string              `json:"cell"`
	PHP              string              `json:"php"`
	PHPAPI           string              `json:"phpApi"`
	Distro           string              `json:"distro"`
	Arch             string              `json:"arch"`
	ThreadSafety     string              `json:"threadSafety"` // "nts"|"zts"
	Debug            bool                `json:"debug"`
	ConfigureOptions []string            `json:"configureOptions"`
	RuntimePackages  map[string][]string `json:"runtimePackages"`
	SoFile           string              `json:"soFile"`
	SoSha256         string              `json:"soSha256"`
	BuiltAt          string              `json:"builtAt"`
	SourceRef        string              `json:"sourceRef"`
	Builder          string              `json:"builder"`
}

func ParseExtManifest(b []byte) (*ExtManifest, error) // lenient except: empty "cell" after parse → error
func (m *ExtManifest) ToJSON() ([]byte, error)        // json.MarshalIndent(m, "", "  ")
func (m *ExtManifest) RuntimePackagesFor(family string) []string

type Prebuilt struct {
	Manifest ExtManifest
	SoBytes  []byte
}

func ResolvePrebuilt(r *Registry, c *Cell) (*Prebuilt, error) // nil,nil on ANY miss
```

Marshal note: `ConfigureOptions`/`RuntimePackages` must serialize as `[]` /
`{}` (not `null`) when empty — initialize to non-nil in constructors and in
`emit-oci` assembly.

### 3.9 `internal/download`

```go
package download

type VerifyPolicy int

const (
	VerifyWarn VerifyPolicy = iota
	VerifyEnforce
	VerifyAttest
	VerifySkip
)

type ArtifactKind int

const (
	ArtifactSource ArtifactKind = iota
	ArtifactBinary
	ArtifactWindowsDll
)

type Artifact struct {
	Kind         ArtifactKind
	Path         string // Source: source dir; Binary: .so path; WindowsDll: dll path
	ExtractedDir string // WindowsDll only
}

type DownloadedPackage struct {
	Artifact Artifact
	// root string; keep bool — unexported temp-dir ownership
}

func (d *DownloadedPackage) SourcePath() (string, bool)
func (d *DownloadedPackage) BinaryPath() (string, bool)
func (d *DownloadedPackage) WindowsDll() (dll, extractedDir string, ok bool)
func (d *DownloadedPackage) Close() error // removes the rpie-dl-* temp dir (no-op after Keep)
func (d *DownloadedPackage) Keep()        // Go equivalent of std::mem::forget

func Download(pkg *resolver.ResolvedPackage, plat *platform.TargetPlatform, policy VerifyPolicy) (*DownloadedPackage, error)

type Expected struct {
	Algo string // "sha256" | "sha1" | "" (none)
	Hex  string // lowercase
}

func ExpectedFromGithubDigest(digest *string) Expected
func ExpectedFromPackagistShasum(shasum *string) Expected
func (e Expected) Describe() string // "sha256"|"sha1"|"none"

func VerifyBytes(b []byte, e Expected) (bool, error) // constant-time compare

// Returns (false, nil) only in the noattest build (see §4 attestation).
func VerifyGithubAttestation(assetPath, repo string) (bool, error)
```

**Ownership rule** (source-verified, note the download.md spec overstates it):
the Rust code calls `std::mem::forget` **only in DownloadOnly mode**. So:
`commands` calls `Keep()` for `rpie download`, and `defer Close()` in all
other modes (yes, `rpie build`'s temp source dir — including the built `.so`
under `modules/` — is deleted on return unless `--emit-oci` copied artifacts
out; this is existing Rust behavior, port it as-is).

Archive extraction (`archive/zip`, `archive/tar`+`compress/gzip`) must
hand-roll what the Rust crates gave for free: reject entries whose cleaned
join escapes the destination, restore Unix mode bits, create symlinks only
when the target stays inside the destination.

### 3.10 `internal/buildpkg`

```go
package buildpkg

type BuiltExtension struct{ BinaryPath string }

func FromPrebuilt(binaryPath string) BuiltExtension

func Build(pkg *resolver.ResolvedPackage, src *download.DownloadedPackage,
	plat *platform.TargetPlatform, configureOptions []string) (BuiltExtension, error)

// sink nil → stream to terminal (procutil.Run); non-nil → procutil.RunCaptured.
func BuildWith(pkg *resolver.ResolvedPackage, src *download.DownloadedPackage,
	plat *platform.TargetPlatform, configureOptions []string,
	makeJobs int, sink *bytes.Buffer) (BuiltExtension, error)
```

`./configure` pitfall: pass `filepath.Join(cwd, "configure")` as the program
(Go does not resolve relative programs against `Cmd.Dir`) but the label and
error text must render `./configure`. Solution: `procutil.Run/RunCaptured`
format errors from the *label* and a caller-provided display command — to keep
procutil generic, buildpkg calls it with program = absolute path but passes
label `./configure` and pre-renders args; the failure line uses
`` `{program} {args}` `` with the **program as given** — therefore buildpkg
must invoke procutil with the literal string `./configure` as program and set
`cmd.Path` correctly. Implementation requirement on procutil: if `program`
contains a `/` and is relative, exec it as `filepath.Join(cwd, program)` while
keeping `program` verbatim in error strings. (This mirrors Unix child-process
semantics the Rust code relies on.)

### 3.11 `internal/install`

```go
package install

const RpieMarkerPrefix = "; rpie automatically added this to enable the "

type IniOutcomeKind int

const (
	IniWritten IniOutcomeKind = iota
	IniAlreadyEnabled
	IniSkipped
	IniNoSuitableLocation
)

type IniOutcome struct {
	Kind IniOutcomeKind
	Path string // set only for IniWritten
}

type InstallOutcome struct {
	InstalledSo string
	Ini         IniOutcome
}

type UninstallOutcome struct {
	RemovedSo         string // "" = no .so existed
	RemovedIniFiles   []string
	RewrittenIniFiles []string
}

type ManagedExtension struct {
	PackageName   string
	ExtensionName string
	Priority      uint32  // default 80
	Version       *string // nil for legacy markers
	IniFile       string
}

func Install(pkg *resolver.ResolvedPackage, built *buildpkg.BuiltExtension,
	plat *platform.TargetPlatform, setupIni bool) (*InstallOutcome, error)

func InstallWindows(pkg *resolver.ResolvedPackage, dll, extractedDir string,
	plat *platform.TargetPlatform, setupIni bool) (*InstallOutcome, error)

func Uninstall(managed *ManagedExtension, plat *platform.TargetPlatform) (*UninstallOutcome, error)

// Keys are ASCII-lowercased extension names; later files win.
func DiscoverManaged(plat *platform.TargetPlatform) (map[string]ManagedExtension, error)

// ManagedKeys returns the map's keys sorted ascending (BTreeMap order).
func ManagedKeys(m map[string]ManagedExtension) []string

// Exported because they are pub in Rust and pinned by unit tests.
func RemoveMarkerBlock(contents, extensionName string) string
func IsEffectivelyEmpty(contents string) bool
```

### 3.12 `internal/commands`

```go
package commands

// Execute parses os.Args[1:], dispatches, prints errors, and returns the
// process exit code: 0 success/help/version, 1 runtime error, 2 usage error.
func Execute() int

type PhpTargetArgs struct {
	PhpPath    *string // --with-php-path
	PhpizePath *string // --with-phpize-path
	MakeJobs   *int    // --make-jobs / -j
}

func (a *PhpTargetArgs) Resolve() (*platform.TargetPlatform, error)

type Mode int

const (
	ModeDownloadOnly Mode = iota
	ModeBuildOnly
	ModeInstall
)

// VerifyArg implements pflag.Value so an invalid value is a PARSE error (exit 2).
type VerifyArg string // "warn" (default) | "enforce" | "attest" | "skip"

func (v *VerifyArg) Set(s string) error
func (v *VerifyArg) String() string
func (v *VerifyArg) Type() string // "WHEN"
func (v VerifyArg) Policy() download.VerifyPolicy

type InstallArgs struct {
	Packages           []string
	ConfigureOptions   []string // args after `--` (cmd.ArgsLenAtDash)
	SkipEnable         bool
	InstallSystemDeps  bool
	CleanupBuildDeps   bool
	Verify             VerifyArg
	IgnorePlatformReqs bool
	Jobs               int // --jobs / -J, default 1
	PreferPrebuilt     bool
	OciRegistry        *string // nil = flag absent; ""/env fallback rules in spec
	EmitOci            *string // nil = flag absent
	Php                PhpTargetArgs
}

func RunInstall(args *InstallArgs, mode Mode) error

type ShowArgs struct {
	All          bool
	CheckUpdates bool
	Php          PhpTargetArgs
}

func RunShow(args *ShowArgs) error

type InfoArgs struct {
	Package *string // optional positional
	Php     PhpTargetArgs
}

func RunInfo(args *InfoArgs) error

type UninstallArgs struct {
	Packages []string
	Php      PhpTargetArgs
}

func RunUninstall(args *UninstallArgs) error
```

Unexported but required helpers (unit-tested in-package):
`makeJobsPerBuild(availableCores, jobs int) int` (floor division, both
operands clamped ≥1).

**Cobra wiring rules** (root command):

- `Use: "rpie"`, `Short: "🦀🥧 RPIE — install PHP extensions (a Rust port of PIE, Docker-aware)"`.
- `Version: version.Version` with
  `SetVersionTemplate("{{.DisplayName}} {{.Version}}\n")` → `rpie 0.1.0`.
- Persistent flags on root: `--with-php-path PATH`, `--with-phpize-path PATH`,
  `--make-jobs N` shorthand `-j` (use `Flags().Changed(...)` to build the
  `*string`/`*int` fields; clap `global = true` ≈ cobra persistent flags).
- Subcommands: `info [PACKAGE]`, `install PACKAGES... [-- CONFIGURE...]`,
  `download` and `build` (same flag set as install), `show`, `uninstall
  PACKAGES...`. One-line `Short` descriptions verbatim from the cli-surface
  spec table.
- `install`/`download`/`build` flags: `--skip-enable`,
  `--install-system-deps`, `--cleanup-build-deps`, `--verify WHEN` (VerifyArg
  pflag.Value, default `warn`), `--ignore-platform-reqs`, `--jobs N` shorthand
  `-J` default 1, `--prefer-prebuilt`, `--oci-registry HOST/NS`,
  `--emit-oci DIR`. Configure options = `args[cmd.ArgsLenAtDash():]` when
  `ArgsLenAtDash() >= 0`.
- `SilenceErrors: true`, `SilenceUsage: true` on root.

**Exit-code mechanism**: a package-level `ranCommand bool` is set at the top
of every subcommand `RunE`. After `root.Execute()`:

- err == nil → 0.
- err != nil && !ranCommand → usage error: print `Error: <err>` plus the
  command's usage string to stderr, return 2. (Covers unknown flags, missing
  required args via `cobra.MinimumNArgs(1)`, bad `--verify` values, unknown
  subcommands, and bare `rpie` — root gets a `RunE` that returns a usage
  error.)
- err != nil && ranCommand → runtime error: print
  `fmt.Fprintf(os.Stderr, "%s %v\n", style.ForStderr().RedBold("error:"), err)`,
  return 1.

---

## 4. Dependency mapping (Cargo → Go)

| Cargo dep | Go replacement | Notes |
|---|---|---|
| `clap` 4 (derive, wrap_help) | `github.com/spf13/cobra` + `github.com/spf13/pflag` | Persistent flags for the PHP-target trio; `ArgsLenAtDash()` for post-`--` configure options; custom `pflag.Value` for `--verify`; exit 2 on usage errors via the `ranCommand` mechanism. Help layout differs cosmetically; parity tests only assert substrings. |
| `anyhow` | stdlib `fmt.Errorf("...: %w", err)` | `{:#}` chain == wrapped `Error()` text. |
| `thiserror` | — (none needed) | Rust uses it barely; no typed error matching exists. |
| `serde` / `serde_json` | stdlib `encoding/json` | Lenient dynamic parsing: `map[string]any` / `json.RawMessage` with type assertions; never strict struct decoding where Rust used `Value`. `MarshalIndent(m, "", "  ")` == `to_vec_pretty`. Map keys sort on marshal == BTreeMap. |
| `ureq` | stdlib `net/http` | Per-package `http.Client{}`; no overall timeout; manual status checks; redirects default. |
| `flate2` + `tar` | stdlib `compress/gzip` + `archive/tar` | Layer tar: single entry, `tar.FormatGNU`, mode 0644, zero mtime, uid/gid 0. Extraction: hand-rolled traversal guard + mode/symlink handling. |
| `zip` | stdlib `archive/zip` | Same traversal guard; restore Unix mode from `f.Mode()`; symlink entries only inside dest. |
| `tempfile` | `os.MkdirTemp("", "rpie-dl-")`, `os.CreateTemp("", "rpie-prebuilt-*.so")` | Explicit `Close()`/`Keep()` ownership replaces RAII/`mem::forget`. |
| `which` | `os/exec.LookPath` | |
| `num_cpus` | `runtime.NumCPU()` | |
| `console` | `internal/style` (hand-rolled ANSI) + `golang.org/x/term` | Per-stream TTY gating + `NO_COLOR`. |
| `dialoguer` | — (drop) | Declared in Cargo.toml but no interactive prompt exists in any spec'd code path; do not add a dependency. |
| `sha2` / `sha1` / `hex` | `crypto/sha256`, `crypto/sha1`, `encoding/hex` (+ `crypto/subtle.ConstantTimeCompare`) | |
| `regex` | stdlib `regexp` | RE2 ≈ RE2; the IPE ERE subset is compatible. |
| `sigstore-verification` 0.2.8 + `tokio` | `github.com/sigstore/sigstore-go` v1.2.1 (+ `github.com/golang/snappy` for `application/x-snappy` bundle_url bodies) | See §4.1. Synchronous calls; no runtime needed. |
| `libc` FFI (`getuid`, `access`, `isatty`) | `os.Getuid()`, `golang.org/x/sys/unix.Access` (build-tagged), `golang.org/x/term.IsTerminal` | |
| dev-deps `assert_cmd`/`predicates` | `os/exec`-driven Go integration tests | `cmd/rpie/cli_test.go`. |

### 4.1 GitHub attestation verification with sigstore-go (`--verify attest`)

Lives in `internal/download` (files `attest.go` + `attest_stub.go` with build
tags `//go:build !noattest` / `//go:build noattest`; the stub returns
`(false, nil)` so `--verify attest` degrades to the
"attestation support not built in" warning — same as the Rust
`--no-default-features` build).

Fetch protocol (identical to the Rust crate):

1. SHA-256 the asset file (streaming), hex-lowercase → `digest`.
2. `GET https://api.github.com/repos/{owner}/{repo}/attestations/sha256:{digest}?per_page=30`
   with `x-github-api-version: 2022-11-28` and `Authorization: Bearer <token>`
   when `GITHUB_TOKEN`/`GH_TOKEN` is set (token only ever sent to
   `api.github.com`).
3. HTTP 404 → error text `No attestations found`, surfaced as
   `attestation verification failed for {owner}/{repo}: No attestations found`.
   Other non-2xx → `API error: GitHub API returned {status}: {body}`.
4. Response `{"attestations":[{"bundle":{...}}|{"bundle_url":"..."}]}`. For
   `bundle_url` entries, GET the URL (no Authorization off-API-host); if
   `Content-Type: application/x-snappy`, `snappy.Decode` (raw, not framed)
   then JSON-parse; failed downloads silently skipped. Empty final list →
   `No attestations found`.

Verification (sigstore-go v1.x API, confirmed current):

```go
import (
	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// once per process (cache in a sync.OnceValues):
opts := tuf.DefaultOptions()            // public-good Sigstore TUF (tuf-repo-cdn.sigstore.dev)
client, err := tuf.New(opts)
trusted, err := root.GetTrustedRoot(client)

verifier, err := verify.NewVerifier(trusted,
	verify.WithObserverTimestamps(1)) // tlog integrated time OR TSA timestamp;
	                                  // Rekor inclusion is verified when a tlog
	                                  // entry supplies the timestamp — matches
	                                  // "Rekor best-effort" intent without a
	                                  // hard Rekor requirement

idActions, err := verify.NewShortCertificateIdentity(
	"https://token.actions.githubusercontent.com", "", "",
	"^https://github.com/"+regexp.QuoteMeta(owner+"/"+repoName)+"/")
idReleases, err := verify.NewShortCertificateIdentity(
	"", "^https://", "https://dotcom.releases.github.com", "")

policy := verify.NewPolicy(
	verify.WithArtifactDigest("sha256", digestBytes), // raw bytes, not hex
	verify.WithCertificateIdentity(idActions),        // OR semantics
	verify.WithCertificateIdentity(idReleases))

for each fetched bundle JSON `raw`:
	var b bundle.Bundle
	if err := b.UnmarshalJSON(raw); err != nil { record err; continue }
	if _, err := verifier.Verify(&b, policy); err == nil { return true, nil }
	record first error
return false, fmt.Errorf("attestation verification failed for %s: %v", repo, firstErr)
```

Acceptance rules preserved from the Rust crate: subject digest must equal the
file's sha256 (via `WithArtifactDigest`); Fulcio-chained cert with a GitHub
Actions identity for the exact `owner/repo` or the
`dotcom.releases.github.com` release identity; DSSE signature must validate;
404 → fatal `No attestations found`; success prints
`  ✔ GitHub attestation verified for {repo}`.

**Documented differences (strictly stronger, accepted):** sigstore-go does a
real certificate-chain verification and standard DSSE PAE over the *decoded*
payload (the Rust crate's broken PAE silently fell back to a structural
check). Consequence: attestations that only passed the Rust *fallback* path
(e.g. private-repo attestations signed by GitHub's internal Fulcio, which
don't chain to the public-good root) will now **fail** instead of
structurally passing. See open questions.

## 5. go.mod

Written at `/Users/shyim/Downloads/php-extension-installer/rpie/go.mod`
(final; implementers must not edit it):

```
module github.com/shyim/go-pie

go 1.26

require (
	github.com/golang/snappy v1.0.0
	github.com/sigstore/sigstore-go v1.2.1
	github.com/spf13/cobra v1.10.2
	github.com/spf13/pflag v1.0.10
	golang.org/x/sys v0.46.0
	golang.org/x/term v0.44.0
)
```

Versions verified against the module proxy on 2026-07-05 (`go list -m
-versions`). Indirect requirements (sigstore-go pulls protobuf-specs, go-tuf,
etc.) and `go.sum` are produced by a **single integration step** running
`go mod tidy` after the packages compile — that step is the only permitted
go.mod modification, and it must not change the direct require lines above.

## 6. Test plan

### 6.1 Integration tests — `cmd/rpie/cli_test.go`

`TestMain` builds the binary once (`go build -o <t.TempDir>/rpie ./cmd/rpie`
via `os/exec`), then table-driven tests port `tests/cli.rs`:

| Test | Command | Assertions |
|---|---|---|
| shows_help | `rpie --help` | exit 0; stdout contains `install` and `info` |
| info_reports_target_php | `rpie info` | skip when no `php` on PATH; else exit 0; stdout contains `Target PHP` and `extension_dir` |
| rejects_bad_package_name | `rpie install not/a/valid/name` | exit != 0; stderr contains `vendor/name`; no network |
| rejects_unknown_bare_extension_name | `rpie install definitelynotarealext` | exit != 0; stderr contains `Packagist name` |
| rejects_configure_options_with_multiple_packages | `rpie install a/b c/d -- --enable-foo` | exit != 0; stderr contains `single extension`; no network |
| install_requires_at_least_one_package | `rpie install` | exit != 0 (assert exit code **2**) |

Also assert `rpie --version` prints `rpie 0.1.0` and exit 0.

### 6.2 Per-package unit tests (mirror every Rust `#[cfg(test)]` listed in the specs)

- `procutil`: FormatExitStatus / DebugQuotedArgs golden strings.
- `platform`: arch.rs tests 1–4; php_binary parser tests 5–10 (parse_version,
  phpinfo_debug_build, phpinfo_php_api, phpinfo_extension_dir,
  parse_constants incl. missing-RPIE_OK rejection); windows.rs tests 11–12.
- `resolver`: mod.rs resolution tests 1–5 (fake `PackageSource` +
  `platform.TargetPlatformFixture`/`PhpBinaryFixture`); request tests
  (parse, selects_highest_stable, caret, exact); metadata tests 1–3;
  packagist ParseVersionsJSON tests 1–3; the **complete** constraint truth
  table (§7.5 of resolver spec, all ~45 rows) plus is_newer table;
  requirements tests (classify, blocking logic).
- `docker`: mod tests 1–4 (lib-requires precedence, catalog fallback, unknown
  ext, merge order); distro parsing/classification 5–6; catalog 7–11
  (including `_comment` never resolving and `len(extensions) >= 80`);
  packages 12–14 (IsPattern, patternMatches IPE vectors, PMUnsupported
  passthrough); bundled 15–16 (LooksLikeBundledName, gd configure flags).
- `oci`: cell tests 1–3 (config_hash order-independence + sentinel, OCIArch,
  cell id/repo tag); manifest tests 4–6 (round-trip, RuntimePackagesFor,
  exact JSON key presence); registry tests 7–10 (parse, scheme selection,
  base64 vectors via StdEncoding, digest verification incl. sha512
  passthrough); mod tests 11–16 (extract_so exact/nested/missing, verify_so,
  descriptor/manifest annotation extraction) using an in-test gzip-tar
  builder.
- `download`: github parse_github_repo forms; assets tests (source names,
  linux-glibc-nts binary names, darwin-zts no-libc names, windows name
  orderings); verify tests (digest/shasum constructors, sha256/sha1
  round-trips, mismatch, none-is-false); opt-in network test for the real
  `cli/cli` attestation guarded by an env var (skipped by default).
- `install`: managed.rs tests 1–9 (marker parse, legacy version, zend block,
  unmarked ini, default priority, targeted removal, effectively-empty after
  removal, absent-extension noop, IsEffectivelyEmpty) — via the exported
  `RemoveMarkerBlock` / `IsEffectivelyEmpty` and unexported `parseMarkers`.
- `commands`: `makeJobsPerBuild` tests ((16,4)→4, (16,1)→16, (8,3)→2,
  (2,8)→1, (1,4)→1, (0,0)→1); VerifyArg.Set accepts exactly the four values.
- `buildpkg`/`sudo`/`style`/`version`: no Rust tests exist; add smoke tests
  only where cheap (style enable/disable passthrough).

All tests must pass with `go test ./...` offline (network tests skipped).

## 7. Docker

Port `Dockerfile` alongside (not blocking): `FROM golang:1-alpine AS build`,
`CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /rpie ./cmd/rpie`, then
`FROM scratch` + `COPY --from=build /rpie /rpie` +
`ENTRYPOINT ["/rpie"]`. CGO must stay off so the binary runs in any `php:*`
image.
