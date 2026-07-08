# 🐹🥧 RPIE — Go PHP Installer for Extensions

RPIE is a Go port of [PIE](https://github.com/php/pie) (the official PHP
Installer for Extensions) that is also designed to work **inside Docker PHP
images**, borrowing the system-dependency awareness of
[`docker-php-extension-installer`](https://github.com/mlocati/docker-php-extension-installer).

It resolves a PHP extension package from Packagist, downloads its source, builds
it with `phpize` / `configure` / `make`, installs the resulting `.so` into PHP's
`extension_dir`, and enables it with a `conf.d` snippet — all as a single static
binary with no PHP/Composer runtime required.

> **Status: working skeleton.** Installs work end-to-end on Linux and macOS via
> all three source/binary download methods — `composer-default` and
> `pre-packaged-source` (build from source) and `pre-packaged-binary` (install a
> pre-built `.so`, no compiler needed), with automatic fallback in the package's
> declared order. On **Windows** it installs the maintainer's pre-compiled DLL
> (no build toolchain). The full Docker-extension catalog is still growing. See
> [Roadmap](#roadmap).

### Windows

Like PIE, RPIE does not build extensions on Windows — maintainers publish
pre-compiled DLLs as GitHub release assets. RPIE detects the target PHP's build
tag (thread-safety, MSVC toolset such as `vs17`, architecture), downloads the
matching `php_<ext>-<version>-<M.m>-<ts>-<compiler>-<arch>.zip`, copies the
`php_<ext>.dll` into the extension directory (plus its `.pdb` and any dependency
DLLs next to `php.exe`), and enables it with an INI entry.

## Install

```sh
go build -ldflags="-s -w" -o rpie .
# binary at ./rpie
```

## Usage

```sh
# Inspect the target PHP (and optionally a package's metadata)
rpie info
rpie info asgrim/example-pie-extension

# Download, build, install and enable an extension
rpie install phpredis/phpredis:^6.0

# Install several at once (each is resolved up front; a failure in one does
# not abort the rest, and rpie exits non-zero if any failed)
rpie install phpredis/phpredis xdebug/xdebug asgrim/example-pie-extension

# Build several source extensions concurrently (-J/--jobs). Cores are split
# across builds (each make gets -j(cores/jobs)); output is grouped per extension.
rpie install phpredis/phpredis php-amqp/php-amqp --jobs 2

# Just download the source, or just build it
rpie download asgrim/example-pie-extension
rpie build asgrim/example-pie-extension

# List installed extensions (and which RPIE package they came from)
rpie show
rpie show --all            # include extensions RPIE does not manage
rpie show --check-updates  # query Packagist for newer versions

# Control download integrity verification (a checksum mismatch is ALWAYS fatal;
# this only changes what happens when no checksum is published)
rpie install phpredis/phpredis --verify enforce  # refuse unverifiable artifacts
rpie install phpredis/phpredis --verify attest   # also verify GitHub build attestations (native, no `gh`)
rpie install phpredis/phpredis --verify skip      # escape hatch, no verification

# RPIE checks each package's `php` / `ext-*` requirements against the target PHP
# and refuses an incompatible install; override with:
rpie install swoole/swoole --ignore-platform-reqs

# Prefer a prebuilt .so from the OCI cache (GHCR) when one exists for the exact
# target — skips compilation entirely. Misses fall back to a source build.
export RPIE_OCI_REGISTRY=ghcr.io/shyim/rpie-ext
rpie install phpredis/phpredis --prefer-prebuilt --install-system-deps

# Pass configure options after `--`
rpie install asgrim/example-pie-extension -- --enable-example-pie-extension

# Target a specific PHP
rpie install xdebug/xdebug --with-php-path /usr/bin/php8.3
```

### Prebuilt extensions via GHCR

Compiling extensions dominates PHP Docker image build time. RPIE can instead
download a **prebuilt `.so`** we publish nightly to GHCR as OCI artifacts, keyed
on the exact build target (`extension/version/php/distro@ver/arch/ts/debug/cfg`).
`rpie install --prefer-prebuilt` computes that cell, does one registry lookup,
and — on a hit — verifies checksums (and optionally the GitHub build
attestation), installs the runtime system packages the `.so` needs, drops the
`.so` into place and enables it. A miss transparently falls back to building
from source. See [docs/prebuilt-oci.md](docs/prebuilt-oci.md) for the full
design (OCI layout, manifest schema, trust model, build matrix).

### Inside a Docker image

When run inside an official `php:*` image, RPIE detects the distro (Alpine vs
Debian) and can install the system build dependencies an extension needs, then
purge the build-only packages afterwards to keep the layer small:

```dockerfile
FROM php:8.4-fpm-alpine
COPY --from=ghcr.io/shyim/rpie /rpie /usr/local/bin/rpie
RUN rpie install gd intl phpredis/phpredis --install-system-deps --cleanup-build-deps
```

**Bundled vs. Packagist extensions.** A bare name (`gd`, `intl`, `zip`, …) is a
PHP-bundled extension: RPIE installs it with the image's
`docker-php-ext-configure` / `docker-php-ext-install` helpers (no Packagist
lookup). A `vendor/name` spec (`phpredis/phpredis`) is a third-party extension
resolved from Packagist. You can mix both in one command, and system
dependencies for the whole batch are installed in a single `apt`/`apk` pass.

### Verifying downloads

Integrity guarantees differ by download method, following PIE's model:

| Method | Guarantee |
| ------ | --------- |
| `pre-packaged-binary` / `pre-packaged-source` | The GitHub release asset's `sha256` digest is verified against the downloaded bytes. |
| `composer-default` | Packagist's dist `shasum` (SHA-1) is verified when published; GitHub-zipball dists usually leave it empty, so integrity then rests on HTTPS-to-origin plus the pinned commit reference. |

`--verify` chooses the policy. A checksum **mismatch is always fatal**; the flag
only decides what happens when *no* checksum is published:

- `warn` (default) — verify when possible, warn and proceed otherwise.
- `enforce` — refuse to install an artifact that cannot be checksummed.
- `attest` — as `warn`, plus native verification of the artifact's GitHub build
  attestation on binary/source assets. No `gh` CLI is required: RPIE fetches the
  Sigstore bundle GitHub publishes, verifies the Fulcio certificate chain, the
  GitHub-identity claims and the DSSE signature, and confirms the attested
  subject digest matches the download. Like PIE's own OpenSSL fallback it does
  not check Rekor transparency-log inclusion. Honours `GITHUB_TOKEN`/`GH_TOKEN`.
  (Requires the default `attestation` build feature.)
- `skip` — no verification.

## How it maps to PIE

| RPIE package   | PIE namespace                       | Responsibility                                      |
| -------------- | ----------------------------------- | --------------------------------------------------- |
| `platform`     | `Php\Pie\Platform`                  | Introspect target `php`, locate `phpize`/`php-config` |
| `resolver`     | `Php\Pie\DependencyResolver`        | Packagist lookup + `php-ext` metadata parsing       |
| `download`     | `Php\Pie\Downloading`               | Fetch + extract source archive                      |
| `buildpkg`     | `Php\Pie\Building`                  | `phpize` → `./configure` → `make -jN`               |
| `install`      | `Php\Pie\Installing`                | Copy `.so`, write enabling `.ini`                   |
| `docker`       | (new — from docker-php-extension-installer) | distro detection, apt/apk system deps     |

## Roadmap

- [x] Pre-packaged GitHub-release source & binary asset download methods
- [x] Platform requirement checking (`php` / `ext-*` from a package's `require`,
      with `--ignore-platform-reqs`); constraint matcher covers `^ ~ >= <= != *`,
      wildcards, hyphen ranges and `||` groups
- [ ] Full transitive Composer dependency resolution (installing *other* required
      packages; currently checks compatibility but does not pull deps)
- [x] `show` (list installed) — via INI markers
- [x] `uninstall` — removes the `.so` and its enabling INI marker
- [x] Batched system-dependency install (one apt/apk pass per batch) with an
      IPE-derived catalog of ~90 extensions; Packagist `lib-*` requires win when
      present, embedded catalog otherwise
- [x] PHP-bundled extensions (gd, intl, zip, …) via `docker-php-ext-install`
- [x] Record the installed version (in the INI marker) so `show --check-updates`
      reports "up to date" / "upgradable to X"
- [x] Windows pre-compiled DLL install path (`windows-binary` download method,
      compiler/arch detection, DLL + dependency-DLL placement)
- [x] Checksum verification of downloads (sha256 for release assets, sha1 for
      dist); native GitHub build-attestation verification, no `gh` CLI
      (`--verify attest`)
- [x] Parallel source builds (`-J/--jobs`) — cores split across concurrent
      builds, output grouped per extension. Helps most when several extensions
      each build from source (≈35% faster for two on a 16-core host); little
      benefit for prebuilt-binary or tiny extensions where download/configure
      dominate.
- [~] Prebuilt extensions via GHCR OCI (`--prefer-prebuilt`, `--emit-oci`).
      Working: client cell-lookup + install + fallback, `--emit-oci` artifact
      generation, runtime-package regex resolution at emit/install time, a matrix
      nightly workflow that pushes per-cell artifacts, aggregates them into an
      `ext:version` image index, and attests the real pushed digest. Not yet
      exercised against a live registry end-to-end. See
      [docs/prebuilt-oci.md](docs/prebuilt-oci.md).

## Testing

- **Unit + hermetic integration tests** (`go test ./...`) — no network. The tricky
  logic (version-constraint matching, `php-ext` metadata parsing, INI-marker
  round-trips, asset-name generation, checksum verification, requirement
  checking) is unit-tested, and end-to-end **resolution** is tested against
  fixture JSON via a fake `PackageSource` (the Packagist client sits behind an
  interface). `main_test.go` covers argument handling.
- **Docker integration tests** (`./tests/docker/run.sh`) — real installs inside
  official `php:*` images: prebuilt-binary install with checksum verification,
  `show`/`uninstall` round-trip, bundled `gd`/`intl` via `docker-php-ext-install`,
  batched system-deps, and platform-requirement blocking. Slow, so kept out of
  `go test`. `ALPINE=1` adds musl/Alpine scenarios; `PHP_IMAGE=…` overrides
  the base image.
- **CI** (`.github/workflows/ci.yml`) — runs code validation and `go test` on every
  push/PR.

## License

BSD-3-Clause, matching PIE.
