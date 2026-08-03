# gpie

> [!WARNING]
> This project is experimental.

`gpie` is a standalone Go implementation of
[PIE](https://github.com/php/pie), the PHP Installer for Extensions. It resolves
PHP extensions from Packagist, builds or downloads the appropriate artifact,
installs it into the target PHP runtime, and manages the enabling INI file.

It is designed for local development and for official `php:*` Docker images:

- No Composer or PHP runtime is needed to run the installer itself.
- Source, release-source, and prebuilt-binary delivery methods are selected
  from package metadata, with fallback between supported methods.
- PHP requirements (`php` and `ext-*`) are checked before installation.
- Debian and Alpine Docker images can receive required system packages in a
  single `apt` or `apk` operation.
- Windows uses maintainer-published DLLs matched to the target PHP build.
- Optional OCI prebuilt artifacts can avoid compiling extensions in Docker.

## Requirements

The installer discovers the target PHP by default, so a normal source build
requires:

- PHP and a matching `phpize` / `php-config` toolchain;
- a C compiler and `make` when building from source;
- network access to Packagist and the artifact host; and
- write access to PHP's `extension_dir` and its scanned INI directory.

Inside an official Docker PHP image, add `--install-system-deps` to let
`gpie` install known build dependencies through `apt` or `apk`.

## Install

Build a local static-friendly binary with Go 1.26 or newer:

```sh
git clone https://github.com/shyim/go-pie.git
cd go-pie
go build -trimpath -ldflags='-s -w' -o gpie .
./gpie --version
```

Move `gpie` onto your `PATH` if you want to use it globally.

## Quick start

```sh
# Inspect the PHP runtime that will receive the extension.
gpie info

# Resolve, build, install, and enable a third-party extension.
gpie install phpredis/phpredis:^6.0

# Inspect extensions installed by gpie.
gpie show

# Remove a gpie-managed extension and its enabling entry.
gpie uninstall phpredis/phpredis
```

Use `--with-php-path` and `--with-phpize-path` when the desired PHP runtime is
not the first one on `PATH`:

```sh
gpie install xdebug/xdebug --with-php-path /usr/bin/php8.3
```

## Common workflows

### Download or build without installing

```sh
gpie download asgrim/example-pie-extension
gpie build asgrim/example-pie-extension
```

Pass package-declared configure options after `--`:

```sh
gpie install asgrim/example-pie-extension -- --enable-example-pie-extension
```

Several extensions can be installed in one invocation. Source builds can run
concurrently with `--jobs`; the available CPU capacity is divided among them.

```sh
gpie install phpredis/phpredis php-amqp/php-amqp --jobs 2
```

### Docker PHP images

In official PHP images, bare names identify PHP-bundled extensions and use the
image's `docker-php-ext-*` helpers. `vendor/name` references are resolved from
Packagist. Both forms can be combined in a single installation.

```dockerfile
FROM php:8.4-fpm-alpine

COPY --from=ghcr.io/shyim/gpie /gpie /usr/local/bin/gpie

RUN gpie install \
      gd intl phpredis/phpredis \
      --install-system-deps \
      --cleanup-build-deps
```

`--cleanup-build-deps` removes build-only packages after a successful build,
keeping the resulting image layer smaller.

### Prebuilt OCI artifacts

For matching targets, `gpie` can download an OCI-hosted prebuilt `.so`
instead of compiling it. A cache miss falls back to the package's normal source
workflow.

```sh
export GPIE_OCI_REGISTRY=ghcr.io/shyim/gpie-ext
gpie install phpredis/phpredis --prefer-prebuilt --install-system-deps
```

Prebuilt artifacts are keyed to the precise PHP, OS, architecture,
thread-safety, debug, and configuration target. See
[Prebuilt OCI artifacts](docs/prebuilt-oci.md) for the artifact format, trust
model, and publishing workflow.

### Download verification

Checksum mismatches are always fatal. The `--verify` flag controls only what
happens when the upstream package does not publish a checksum:

| Policy | Behavior |
| --- | --- |
| `warn` (default) | Verify published checksums; warn when no checksum exists. |
| `enforce` | Refuse artifacts without a published checksum. |
| `attest` | Use `warn`, then require valid GitHub build provenance for supported source and binary assets. |
| `skip` | Skip checksum and attestation verification. |

```sh
gpie install phpredis/phpredis --verify enforce
gpie install phpredis/phpredis --verify attest
```

Release assets use SHA-256. Composer/Packagist distribution metadata can only
provide SHA-1; that published value is verified when present for compatibility
with the upstream protocol. Attestation verification is native and does not
require the GitHub CLI. `GITHUB_TOKEN` or `GH_TOKEN` is used when required for
GitHub-hosted attestations or GHCR access.

## Command reference

| Command | Purpose |
| --- | --- |
| `gpie info [PACKAGE]` | Display target-PHP details and, optionally, resolved package metadata. |
| `gpie install PACKAGE...` | Download, build or retrieve, install, and enable extensions. |
| `gpie download PACKAGE...` | Download extension sources without building. |
| `gpie build PACKAGE...` | Build downloaded source without installing it. |
| `gpie show` | List installed extensions; `--all` includes unmanaged ones. |
| `gpie uninstall PACKAGE...` | Remove gpie-managed extension files and INI markers. |

Run `gpie <command> --help` for all flags and package-specification syntax.

## Development

```sh
# Unit and hermetic integration tests
go test ./...

# Static checks
go vet ./...
golangci-lint run

# Build for the supported cross-platform targets
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...
```

Docker integration tests exercise real installs against official PHP images:

```sh
./tests/docker/run.sh
ALPINE=1 ./tests/docker/run.sh
```

The checked-in [golangci-lint configuration](.golangci.yml) starts from the
complete linter set and documents any project-specific exclusions.

For implementation details and package contracts, read the
[Go-port design](docs/go-port-design.md).

## License

BSD-3-Clause, matching PIE.
