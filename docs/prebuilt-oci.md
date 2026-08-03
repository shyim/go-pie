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
extensions        × versions (latest N)
  × php            (8.1 … 8.4, from data)
  × distro         (alpine@3.20, alpine@3.21, debian@12, …)
  × arch           (x86_64, aarch64)
  × ts             (nts; zts only where an extension needs it)
```

Kept sane by:
- building only extensions/versions users actually request (seed from download
  telemetry / an allowlist), newest few versions each;
- `debug=0` only (debug builds are rare; source-build them);
- matrix generated by a script from `data/` so it is reviewable;
- GitHub Actions native `linux/arm64` runners (or QEMU) for aarch64.

A build cell = spin up the matching official `php:<v>-<distro>` image, run
`gpie build <ext> --emit-oci`, which produces the layer + config blob; a publish
step assembles/updates the per-version OCI index and pushes to GHCR.

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
