# Prebuilt extensions via GHCR (OCI)

## Goal

Make `gpie install <ext>` inside a Docker image *skip compilation* when we have
already built that exact extension for that exact target. We build a matrix of
extensions nightly in CI, push the compiled `.so`s to GHCR as OCI artifacts, and
at install time do a single registry lookup: if the exact cell exists, download
the `.so` and its runtime dependency list instead of running phpize/make.

This turns a 5–30s source build into a sub-second download, which is the
dominant cost when building PHP Docker images.

## The identity of a build

A compiled `.so` is ABI-locked. Two builds are interchangeable only if they
match on every axis that affects the binary. Per the decision to key on
`distro@version` (matching mlocati/docker-php-extension-installer), the cache
cell is:

```
extension        e.g. redis
version          e.g. 6.3.0                (exact upstream version)
php              e.g. 8.4                  (major.minor; ABI is stable per minor)
distro           e.g. alpine@3.21 | debian@12
arch             e.g. x86_64 | aarch64
thread-safety    nts | zts
debug            0 | 1
config-hash      hash of the configure options used
```

`distro@version` implicitly pins libc (Alpine→musl, Debian→glibc) and the exact
system-library versions the `.so` links against — which is why keying on it (vs
a bare libc flavour) is the safe, simple choice: the runtime packages we install
alongside are guaranteed to exist and match.

### Cell key

The client computes a canonical, stable string and its short hash:

```
redis/6.3.0/php8.4/debian@12/x86_64/nts/nodebug/cfg-<8hex>
```

The `config-hash` is a hash of the sorted configure options (empty options → a
fixed sentinel). This is both the OCI variant selector and the artifact digest
input.

## OCI layout: one image index per `ext@version`

Each extension version is **one OCI image index** (a.k.a. manifest list) pushed
to:

```
ghcr.io/<org>/gpie-ext/<extension>:<version>
# e.g. ghcr.io/shyim/gpie-ext/redis:6.3.0
```

The index's `manifests[]` entries each describe one built cell. We use the
standard `platform` fields plus annotations for the axes OCI platform can't
express:

```jsonc
{
  "schemaVersion": 2,
  "mediaType": "application/vnd.oci.image.index.v1+json",
  "manifests": [
    {
      "mediaType": "application/vnd.oci.image.manifest.v1+json",
      "digest": "sha256:...",
      "platform": { "os": "linux", "architecture": "amd64" },
      "annotations": {
        "sh.gpie.php": "8.4",
        "sh.gpie.distro": "debian@12",
        "sh.gpie.ts": "nts",
        "sh.gpie.debug": "0",
        "sh.gpie.config-hash": "0000000000000000",
        "sh.gpie.cell": "redis/6.3.0/php8.4/debian@12/x86_64/nts/nodebug/cfg-00000000"
      }
    }
    // ... one per (php, distro, arch, ts, debug, config) cell
  ]
}
```

Each referenced **image manifest** is a small artifact:

- **config blob** (`application/vnd.gpie.ext.config.v1+json`) — the custom
  manifest (schema below): runtime deps, checksums, provenance.
- **one layer** (`application/vnd.gpie.ext.layer.v1.tar+gzip`) — a tarball
  containing the `.so` (and `.pdb`/dependency DLLs on Windows).

Why an index-per-version rather than a tag-per-cell: one stable, human-readable
tag (`redis:6.3.0`), atomic publish, native multi-arch, and the client resolves
its cell by scanning `manifests[].annotations` — a single `GET` of the index.

## The custom manifest (config blob)

```jsonc
{
  "gpieManifestVersion": 1,
  "extension": "redis",
  "version": "6.3.0",
  "extensionType": "php-ext",          // or php-ext-zend
  "iniDirective": "extension",          // or zend_extension
  "priority": 60,
  "cell": "redis/6.3.0/php8.4/debian@12/x86_64/nts/nodebug/cfg-00000000",
  "php": "8.4",
  "phpApi": "20240924",                 // Zend Module API; a hard ABI gate
  "distro": "debian@12",
  "arch": "x86_64",
  "threadSafety": "nts",
  "debug": false,
  "configureOptions": [],
  // The runtime system packages the .so links against — installed before use.
  // Distro-family keyed so a manifest stays useful if we later widen matching.
  "runtimePackages": {
    "debian": ["libzstd1"],
    "alpine": ["zstd-libs"]
  },
  "soFile": "redis.so",                 // path inside the layer tarball
  "soSha256": "…",                      // verified after extraction
  "builtAt": "2026-07-02T00:00:00Z",
  "sourceRef": "phpredis/phpredis@6.3.0",
  "builder": "gpie-nightly",
  // GitHub repository whose workflow attested the pushed OCI manifest digest.
  "attestationRepository": "shyim/go-pie"
}
```

`phpApi` is a belt-and-suspenders ABI check: even within `distro@version` we
refuse to load a `.so` whose Zend Module API differs from the target's.

### Runtime packages & regex resolution

The embedded catalog (`src/docker/system-deps.json`, derived from
docker-php-extension-installer) lists each extension's *persistent* runtime libs.
Some are named with a regex that pins the distro's exact versioned package, e.g.
`^librabbitmq[0-9]*$`. These cannot be `apt-get install`ed literally.

Because `--emit-oci` runs **inside the exact target image**, we resolve those
patterns to concrete names there (`apt-cache search` / `apk search`, filtered by
the anchored regex) — the same moment IPE resolves them — and write the concrete
names (e.g. `librabbitmq4`) into the manifest's `runtimePackages`. The install
path resolves any remaining patterns the same way before `apt-get`/`apk`, so a
source build and a prebuilt install agree on runtime deps.

## Client flow

At install time, before downloading source:

1. Compute the cell key from the resolved package + target platform + distro.
2. `GET ghcr.io/<org>/gpie-ext/<ext>:<version>` (the index). Anonymous pull
   token for public packages; `GITHUB_TOKEN` for private.
3. Find the `manifests[]` entry whose annotations match the cell exactly
   (php, distro, arch, ts, debug, config-hash). Miss → fall back to source build.
4. `GET` that manifest → pull its config blob (the manifest above) and verify:
   - `phpApi` matches the target,
   - checksums,
   - (optional) GitHub attestation on the OCI manifest digest, reusing the existing
     `--verify attest` path.
5. Install `runtimePackages` (via the existing batched apt/apk path), extract
   the layer, copy `soFile` into `extension_dir`, verify `soSha256`, enable INI.

This is a new download method, `PrebuiltOci`, tried **first** on Linux when a
registry is configured, falling through to the existing methods on any miss or
error. It never blocks an install: a cache miss is just a normal source build.

## Trust

- Registry pull over HTTPS; content-addressed blobs (digest-verified by design).
- `soSha256` in the config blob is re-verified after extraction.
- The OCI manifest digest is covered by a GitHub build attestation from the nightly
  workflow; `--verify attest` (already implemented, native, no `gh`) verifies it.
- `--verify enforce` refuses a prebuilt artifact lacking its `.so` checksum;
  `--verify attest` additionally requires valid GitHub provenance.

## Nightly build matrix

Cartesian product, pruned by real support:

```
extensions        × versions (newest N stable, from Packagist)
  × php            (8.2 … 8.5)
  × base           (bookworm, trixie, alpine3.23, alpine3.24)
  × arch           (x86_64, aarch64)
  × ts             (nts, zts)
```

The matrix is not hand-written. `.github/prebuild-targets.yml` declares the
targets and `scripts/gen-prebuild-matrix.py` expands them against Packagist,
so upstream releases land without editing a pinned version.

```sh
python3 scripts/gen-prebuild-matrix.py --check      # per-extension cell counts
python3 scripts/gen-prebuild-matrix.py --shard 0/8  # one shard, as CI runs it
```

### Why the matrix is not a plain cartesian product

**SAPI is not an axis.** `cli`, `fpm` and `apache` images of the same
php+base differ only in `--enable-embed` / `--enable-fpm` / `--with-apxs2` and
phpdbg. They share `PHP_CFLAGS`, Zend API, threading and `extension_dir`, so
one `.so` built in the `cli` image loads in all three. Adding SAPI would
triple the matrix for byte-identical output. `apache` also does not exist for
Alpine at all.

**ZTS is an axis**, and the only SAPI-level one, because `-zts` tags flip
`--enable-zts`, which changes the Zend ABI and `extension_dir`
(`no-debug-zts-<api>`). Official images publish `zts` for every base.

**`debug=0` only.** Debug builds are rare; they source-build.

**Version/PHP pruning.** A cell is only emitted when the release's Composer
`require.php` admits that PHP minor — including upper bounds, which are real
here (xdebug 3.5.3 is `>=8.0 <8.6`, mongodb 2.3.3 is `>=8.1 <9`). The
generator also rejects any package whose Composer type is not
`php-ext`/`php-ext-zend`, because `gpie`'s resolver refuses those.

**Pick the upstream vendor, not a republisher.** `grpc/grpc` is the pure-PHP
library (type `library`); the extension is `grpc/grpc-php-ext`, the gRPC
project's own repo. Several `php-ext` packages on Packagist are third-party
republishers of an upstream tarball (`pie-extensions/*`, `extport/*`). This
workflow signs and attests what it builds, so building from a republisher
would launder that provenance into something that looks first-party. protobuf
is excluded for exactly this reason: it has no official Packagist package.

### PHP-bundled extensions

Bundled extensions (`gd`, `intl`, `zip`, …) are built by the `docker-php-ext-*`
helpers from the PHP source tree, not resolved from Packagist. They are cached
too, keyed by a **second cell rule**: `oci.NewBundledCell` puts the full PHP
**patch** version in both the version and the PHP axis, e.g.

```
intl/8.4.24/php8.4.24/alpine@3.24.1/aarch64/zts/nodebug/cfg-00000000
```

Most of them carry no version of their own — `intl` and `gd` have no
`PHP_*_VERSION` at all — so their source *is* one exact PHP release. Keying on
major.minor would let an 8.4.24-built `intl.so` serve an 8.4.25 runtime, and
`phpApi` cannot catch it because the Zend Module API only changes per minor.
Third-party extensions keep the major.minor axis (`NewCell`): they have a real
upstream version and are ABI-stable across a minor's patches.

Why cache something that compiles in seconds: natively these are 1–10s, but on
an **emulated** arm64 runner the same builds take 22–105s. Measured on one host,
PHP 8.4, `-j2`:

| ext | native | emulated |
| --- | --- | --- |
| intl | 17s | 105s |
| gd | 7s | 55s |
| bcmath | 2s | 39s |
| soap / sockets | 2s | 28s |
| zip | 3s | 22s |

A `gd intl zip soap sockets bcmath` install is ~4.5 minutes of pure QEMU per
image build. Only extensions *not* enabled by default in the official images are
listed in `bundled:` — caching an already-loaded extension is pointless.

`pdo_firebird` is Debian-only (no Alpine firebird dev package). `odbc` is
excluded: its `config.m4` reads the `--enable-odbc=shared` that
`docker-php-ext-install` always passes as "enable Adabas", then looks for headers
under `/usr/local/incl` and fails; `--without-adabas` does not override it.
`pdo_odbc` builds with an explicit `--with-pdo-odbc=unixODBC,/usr`.

### System dependencies for bundled extensions

`--install-system-deps` resolves bundled extensions through the same embedded
catalog as third-party ones, keyed by extension name. Two things had to be fixed
for that to work:

* The catalog extractor only matched **single-name** case arms, so every
  extension upstream declares in a shared arm — `pgsql@debian | pdo_pgsql@debian
  | pq@debian)` and nine others covering `odbc`, `pdo_odbc`, `oci8`, `pdo_oci`,
  `sodium`, `sqlsrv`, `pdo_sqlsrv` — was silently absent. `pgsql` then failed
  with `Cannot find libpq-fe.h` despite `--install-system-deps`.
* Upstream picks some packages per distro release (`libenchant-2-dev` on
  Debian ≥ 11, `libenchant-dev` below; `enchant2-dev` not `enchant-dev` on
  Alpine). The catalog is flat, so both names land in one list — and because
  apt/apk install atomically, the name that does not exist here fails the whole
  batch and takes every other dependency with it.

The second is handled the way `install-php-extensions` does it: a single dry run
(`apt-get install -s` / `apk add --simulate`) reports which packages cannot be
selected, and those are dropped. One subprocess for the whole list, and it defers
to the package manager's own resolver, so virtual packages (`libxslt-dev` is
provided by `libxslt1-dev`) need no name heuristics. If the dry run cannot single
out the offending names, everything is kept — silently dropping a real build
dependency would turn a clear "package not found" into a confusing compile error.

Runtime packages that differ per release become an anchored alternation
(`^(libodbc2|libodbc1)$`) resolved against the target distro, the same mechanism
already used for versioned library names.

### Extensions whose configure flags depend on the build environment

`redis` and `memcached` decide `--enable-*-igbinary` / `--enable-*-msgpack`
from whether those extensions are *loaded in the build image*, and `swoole`
gates `--enable-iouring` on the distro release and `--enable-swoole-thread` on
ZTS. Because `cfg-<hash>` in the cell id hashes the configure options, a cell
built with a different flag set than the client computes is simply a miss, not
a wrong binary — safe, but useless. Anything added here whose flags vary that
way needs its flag set pinned explicitly, or its cells will never be hit.

### Two traps worth knowing

**The distro label is read, never guessed.** `gpie` builds it from
`/etc/os-release`, and Alpine reports a *patch-level* `VERSION_ID`:
`php:8.4-alpine3.24` is `alpine@3.24.1`, and `php:8.4-alpine3.23` is
`alpine@3.23.5`. Those patch levels float as Alpine respins. Hard-coding
`alpine@3.24` would publish cells under a label no client ever computes — a
silent 100% cache miss. The workflow therefore takes the label from
`out/cell.txt` and derives the annotation from it.

**Alpine images have no build toolchain.** `--install-system-deps` installs an
extension's own dependencies, not `autoconf`/`gcc`/`make`. Debian php images
ship those preinstalled; Alpine images ship none, so `phpize` fails with
"Cannot find autoconf". The workflow installs `$PHPIZE_DEPS` (published by the
official images) before building on Alpine.

### Sharding

The full product exceeds GitHub's 256-job matrix cap, and `strategy.matrix` is
evaluated before any job runs, so a single job cannot both compute and shard
its matrix. `nightly-prebuild.yml` fans out over shard ids and calls the
reusable `prebuild-shard.yml`, which expands its own slice. Shards stride
rather than block-slice, so one slow extension's cells spread across all
shards instead of stacking in one.

A build cell = spin up the matching official `php:<v>-<base>` image, run
`gpie build <ext> --emit-oci`, which produces the layer + config blob; a publish
step assembles/updates the per-version OCI index and pushes to GHCR.

### Index annotations matter at this scale

`oras manifest index create` does not copy a child manifest's annotations onto
the index descriptor, and `ResolvePrebuilt` falls back to fetching each child
manifest when a descriptor lacks `sh.gpie.cell`. With hundreds of cells per
`ext@version` that turns one lookup into hundreds of serial round-trips, on
every miss.

`--annotation` does **not** fix this: it writes *index-level* annotations, and
oras exposes no per-descriptor annotation flag (verified against oras 1.3.0). So
the index job builds the index with `-o -`, injects `sh.gpie.cell` into each
entry of `manifests[]` keyed by digest, and pushes the result with
`oras manifest push` — keeping both a hit and a miss to a single `GET`.

Reading the cell id back out of a child also needs care: `oras manifest fetch
--format go-template` renders the *descriptor*, which has no annotations, so
`index .annotations` aborts the command with "index of untyped nil" rather than
returning empty. The job parses the manifest body as JSON instead.

## Failure modes & guarantees

- **Miss** → transparent source build (today's behaviour).
- **ABI drift** (php patch bumps Zend API) → `phpApi` mismatch → miss → source
  build. Nightly rebuilds refresh the cell.
- **Registry down / rate-limited** → warn, source build.
- **Runtime package missing on the target** → the batched dep install surfaces
  it exactly as for a source build.
- **Tampering** → digest + optional attestation; `enforce` mode hard-fails.

## Scope of the initial vertical slice

- Client: compute the cell key; `oci` module that resolves the index, matches
  the cell, pulls config + layer, verifies, and installs. Wired as the
  `PrebuiltOci` method, off by default behind `--prefer-prebuilt` +
  `GPIE_OCI_REGISTRY`.
- Build/emit: `gpie build --emit-oci <dir>` writes the layer tarball + config
  blob for one cell (the unit CI publishes).
- CI: a nightly workflow that builds ONE extension for ONE cell and pushes an
  OCI index, proving the round-trip.
