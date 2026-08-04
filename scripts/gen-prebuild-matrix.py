#!/usr/bin/env python3
"""Generate the nightly-prebuild build matrix from declarative targets + Packagist.

Usage:
  python3 scripts/gen-prebuild-matrix.py                     # full matrix to stdout
  python3 scripts/gen-prebuild-matrix.py --shard 0/4          # one shard
  python3 scripts/gen-prebuild-matrix.py --check              # validate targets only

Reads `.github/prebuild-targets.yml`, expands the cartesian product of
extension x version x php x base-image x thread-safety x arch, prunes the cells
that cannot exist, and prints a GitHub Actions matrix as JSON.

Pruning rules, each of which reflects a real constraint (see
docs/prebuilt-oci.md, "Why the matrix is not a plain cartesian product"):

* SAPI is not an axis. cli/fpm/apache images of one php+base differ only in
  --enable-embed / --enable-fpm / --with-apxs2 and phpdbg; they share CFLAGS,
  Zend API, extension_dir and threading, so one .so serves all three. Only
  `zts` is a real axis because it flips --enable-zts and the Zend ABI.
* `apache` therefore never appears, and Alpine has no apache image anyway.
* An extension is only built for a PHP minor its Packagist `require.php`
  constraint admits, and that upstream's own version supports.
* `zts: false` targets are skipped on zts bases (extensions that do not build
  or are not supported thread-safe).
* Versions come from Packagist p2 metadata, newest `keep_versions` stable
  releases, so the matrix tracks upstream without hand-editing pins.
"""
import argparse
import json
import re
import sys
import urllib.error
import urllib.request

TARGETS = ".github/prebuild-targets.yml"
PACKAGIST_P2 = "https://repo.packagist.org/p2/{}.json"
# The authoritative patch version behind each `php:<minor>-*` tag. Entries for
# EOL minors are null and prerelease entries carry a non-numeric version
# ("8.6.0alpha3"); php_patch_versions() skips both.
PHP_VERSIONS_JSON = "https://raw.githubusercontent.com/docker-library/php/master/versions.json"

# Arch token -> the GitHub-hosted runner that builds it natively. Native
# runners rather than QEMU: an emulated compile of a large extension blows the
# job timeout.
RUNNERS = {
    "x86_64": "ubuntu-latest",
    "aarch64": "ubuntu-24.04-arm",
}

# Recognised base-image suffixes. Deliberately NOT mapped to a distro label
# here: `gpie` builds the label from /etc/os-release (docker.Distro.Label()),
# and Alpine reports a patch-level VERSION_ID -- `php:8.4-alpine3.24` is
# alpine@3.24.1, not alpine@3.24, and that patch floats as Alpine respins.
# Guessing it would publish cells under a label no client ever computes, i.e. a
# silent 100% cache miss. The workflow reads the real label out of the
# container (out/cell.txt) instead.
BASES = ("bookworm", "trixie", "alpine3.23", "alpine3.24")


def fail(msg):
    sys.exit(f"error: {msg}")


def load_targets(path):
    """Parse the target file.

    Deliberately a tiny hand-rolled parser for the one shape we emit, so the
    generator has no third-party dependency in CI.
    """
    try:
        text = open(path).read()
    except OSError as e:
        fail(f"reading {path}: {e}")

    cfg = {"defaults": {}, "extensions": [], "bundled": []}
    section = None
    current = None
    for raw in text.splitlines():
        line = raw.split("#", 1)[0].rstrip()
        if not line.strip():
            continue
        if not line.startswith(" ") and line.endswith(":"):
            section = line[:-1]
            current = None
            continue
        if section == "defaults":
            k, _, v = line.strip().partition(":")
            cfg["defaults"][k.strip()] = parse_scalar(v.strip())
        elif section in ("extensions", "bundled"):
            stripped = line.strip()
            if stripped.startswith("- "):
                current = {}
                cfg[section].append(current)
                stripped = stripped[2:]
            if current is None:
                fail(f"{path}: key outside a list item: {line!r}")
            k, _, v = stripped.partition(":")
            current[k.strip()] = parse_scalar(v.strip())
    if not cfg["extensions"] and not cfg["bundled"]:
        fail(f"{path}: no extensions declared")
    return cfg


def parse_scalar(v):
    if v in ("true", "false"):
        return v == "true"
    if v.startswith("[") and v.endswith("]"):
        inner = v[1:-1].strip()
        if not inner:
            return []
        return [p.strip().strip("'\"") for p in inner.split(",")]
    if re.fullmatch(r"-?\d+", v):
        return int(v)
    return v.strip("'\"")


def http_json(url):
    req = urllib.request.Request(url, headers={"User-Agent": "gpie-matrix-generator"})
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            return json.load(r)
    except urllib.error.HTTPError as e:
        fail(f"fetching {url}: HTTP {e.code}")
    except (urllib.error.URLError, TimeoutError) as e:
        fail(f"fetching {url}: {e}")


def is_stable(version):
    """Reject anything that is not a plain release.

    A nightly cache must not serve alphas: the cell key has no stability axis,
    so a prerelease .so would be indistinguishable from a stable one.
    """
    v = version.lstrip("v")
    return re.fullmatch(r"\d+(\.\d+)*", v) is not None


def version_key(version):
    return [int(p) for p in version.lstrip("v").split(".")]


def php_constraint_allows(constraint, php):
    """Evaluate a Composer `require.php` constraint against a PHP minor.

    Supports the subset upstream extensions actually use: comma/space-separated
    AND terms of >=, >, <=, <, ^, ~, ||-separated OR groups. Unparseable input
    returns True: the build itself is the real gate, and excluding a cell on a
    parse failure would silently shrink coverage.
    """
    if not constraint:
        return True
    want = [int(x) for x in php.split(".")]
    for group in constraint.split("||"):
        if _and_group_allows(group, want):
            return True
    return False


def _and_group_allows(group, want):
    terms = [t for t in re.split(r"[,\s]+", group.strip()) if t]
    if not terms:
        return True
    for term in terms:
        m = re.fullmatch(r"(>=|<=|>|<|\^|~|=)?\s*v?(\d+(?:\.\d+){0,2})(?:\.\*)?", term)
        if not m:
            return True
        op = m.group(1) or "="
        bound = [int(x) for x in m.group(2).split(".")]
        if not _term_allows(op, bound, want):
            return False
    return True


def _term_allows(op, bound, want):
    cmp = _cmp(want, bound[: len(want)])
    if op == ">=":
        return cmp >= 0
    if op == ">":
        return cmp > 0
    if op == "<=":
        return cmp <= 0
    if op == "<":
        return cmp < 0
    if op == "=":
        return cmp == 0
    if op == "^":
        # ^8.1 admits >=8.1 within major 8.
        return want[0] == bound[0] and cmp >= 0
    if op == "~":
        # ~8.1 admits >=8.1 <9.0 for a two-part bound.
        return want[0] == bound[0] and cmp >= 0
    return True


def _cmp(a, b):
    for x, y in zip(a, b):
        if x != y:
            return -1 if x < y else 1
    return 0


def php_require(release):
    """The `require.php` constraint of one p2 release entry, or "".

    Packagist's p2 format writes the string `"__unset"` where a release clears
    a field inherited from the previous entry, so `require` is not always an
    object.
    """
    require = release.get("require")
    if not isinstance(require, dict):
        return ""
    constraint = require.get("php", "")
    return constraint if isinstance(constraint, str) else ""


def php_patch_versions():
    """Map each PHP minor to the exact patch version the official images ship.

    A bundled extension's source IS the PHP source tree, so its cell is keyed on
    the full patch version (see oci.NewBundledCell). That patch bumps whenever
    docker-library publishes a new PHP release, which is exactly why the matrix
    reads it live from versions.json instead of pinning it.
    """
    data = http_json(PHP_VERSIONS_JSON)
    out = {}
    for minor, info in data.items():
        version = info.get("version") if isinstance(info, dict) else None
        if isinstance(version, str) and re.fullmatch(r"\d+\.\d+\.\d+", version):
            out[minor] = version
    if not out:
        fail("could not read any PHP patch versions from docker-library versions.json")
    return out


def packagist_versions(package, keep):
    """Newest `keep` stable versions of a package, with their php constraints.

    Returns [(version, php_constraint)], newest first. Also validates the
    Composer type: gpie's resolver rejects anything that is not
    php-ext/php-ext-zend, so a mistyped package must fail here rather than in
    every build cell.
    """
    data = http_json(PACKAGIST_P2.format(package))
    releases = data.get("packages", {}).get(package)
    if not releases:
        fail(f"{package}: not found on Packagist")

    seen = {}
    ext_type = None
    for rel in releases:
        if not isinstance(rel, dict):
            continue
        version = rel.get("version", "")
        if not is_stable(version):
            continue
        if rel.get("type") in ("php-ext", "php-ext-zend"):
            ext_type = rel.get("type")
        # Older releases predate the php-ext spec and carry no `php-ext` block,
        # so gpie cannot learn the extension name and falls back to deriving it
        # from the package name -- phpredis/phpredis 6.2.0 builds redis.so but is
        # looked for as phpredis.so, and the build fails after compiling. Only
        # build versions that actually declare their metadata.
        if not isinstance(rel.get("php-ext"), dict):
            continue
        normalized = version.lstrip("v")
        # p2 lists newest first; keep the first (richest) entry per version.
        seen.setdefault(normalized, php_require(rel))

    if ext_type is None:
        fail(
            f"{package}: no stable release declares composer type php-ext or "
            "php-ext-zend; gpie's resolver would reject it"
        )
    if not seen:
        fail(f"{package}: no stable versions found")

    ordered = sorted(seen.items(), key=lambda kv: version_key(kv[0]), reverse=True)
    return ordered[:keep]


def build_matrix(cfg):
    defaults = cfg["defaults"]
    default_php = as_list(defaults.get("php", []))
    default_bases = as_list(defaults.get("bases", []))
    default_arches = as_list(defaults.get("arches", []))
    default_keep = int(defaults.get("keep_versions", 1))

    for base in default_bases:
        if base not in BASES:
            fail(f"unknown base image {base!r}; known: {', '.join(BASES)}")
    for arch in default_arches:
        if arch not in RUNNERS:
            fail(f"unknown arch {arch!r}; known: {', '.join(sorted(RUNNERS))}")

    include = []
    for ext in cfg["extensions"]:
        package = ext.get("package")
        name = ext.get("ext_name")
        if not package or not name:
            fail(f"extension entry needs both `package` and `ext_name`: {ext}")

        phps = as_list(ext.get("php", default_php))
        bases = as_list(ext.get("bases", default_bases))
        arches = as_list(ext.get("arches", default_arches))
        keep = int(ext.get("keep_versions", default_keep))
        allow_zts = ext.get("zts", True)

        # Per-extension overrides bypass the defaults check above, so validate
        # them too: a typo must fail here, not in every job it spawns.
        for base in bases:
            if base not in BASES:
                fail(f"{name}: unknown base image {base!r}; known: {', '.join(BASES)}")
        for arch in arches:
            if arch not in RUNNERS:
                fail(f"{name}: unknown arch {arch!r}; known: {', '.join(sorted(RUNNERS))}")
        if not bases or not arches or not phps:
            fail(f"{name}: php, bases and arches must each be non-empty")
        min_php = ext.get("min_php")

        for version, php_constraint in packagist_versions(package, keep):
            for php in phps:
                if min_php and _cmp(
                    [int(x) for x in php.split(".")],
                    [int(x) for x in str(min_php).split(".")],
                ) < 0:
                    continue
                if not php_constraint_allows(php_constraint, php):
                    continue
                for base in bases:
                    for ts in ("nts", "zts"):
                        if ts == "zts" and not allow_zts:
                            continue
                        for arch in arches:
                            include.append(
                                cell(name, package, version, php, base, ts, arch)
                            )
    include += bundled_cells(cfg, default_php, default_bases, default_arches)

    if not include:
        fail("matrix is empty; every declared cell was pruned")
    return include


def bundled_cells(cfg, default_php, default_bases, default_arches):
    """Expand the `bundled:` section into cells.

    Bundled extensions (gd, intl, zip, …) are compiled from the PHP source tree
    by the `docker-php-ext-*` helpers, so they have no Packagist package and no
    version of their own: the cell's version IS the PHP patch version, read live
    from docker-library's versions.json.
    """
    entries = cfg.get("bundled") or []
    if not entries:
        return []

    patches = php_patch_versions()
    out = []
    for ext in entries:
        name = ext.get("name")
        if not name:
            fail(f"bundled entry needs a `name`: {ext}")
        if not re.fullmatch(r"[a-z0-9_]{1,64}", name):
            fail(f"bundled: invalid extension name {name!r}")

        phps = as_list(ext.get("php", default_php))
        bases = as_list(ext.get("bases", default_bases))
        arches = as_list(ext.get("arches", default_arches))
        allow_zts = ext.get("zts", True)

        for base in bases:
            if base not in BASES:
                fail(f"{name}: unknown base image {base!r}; known: {', '.join(BASES)}")
        for arch in arches:
            if arch not in RUNNERS:
                fail(f"{name}: unknown arch {arch!r}; known: {', '.join(sorted(RUNNERS))}")

        for php in phps:
            patch = patches.get(str(php))
            if not patch:
                # A minor with no published patch version has no image to build
                # in; skip rather than emit a cell that cannot run.
                print(f"note: no published patch version for PHP {php}; "
                      f"skipping bundled {name}", file=sys.stderr)
                continue
            for base in bases:
                for ts in ("nts", "zts"):
                    if ts == "zts" and not allow_zts:
                        continue
                    for arch in arches:
                        c = cell(name, "", patch, php, base, ts, arch)
                        c["bundled"] = "true"
                        out.append(c)
    return out


def cell(name, package, version, php, base, ts, arch):
    # `zts` is the only SAPI-level variant that changes the extension ABI, so
    # it is the only one that becomes an image tag suffix. Everything else
    # builds in the cli image and serves fpm/apache too.
    suffix = "-zts" if ts == "zts" else ""
    image = f"php:{php}{suffix}-{base}"
    return {
        "ext_name": name,
        "package": package,
        "version": version,
        "php": php,
        "base": base,
        "ts": ts,
        "arch": arch,
        "image": image,
        "runner": RUNNERS[arch],
        # Matrix values must be strings for the workflow's `if:` comparisons.
        "bundled": "false",
    }


def as_list(v):
    if isinstance(v, list):
        return v
    if v is None or v == "":
        return []
    return [v]


# GitHub refuses a matrix with more than 256 jobs, so a shard that large would
# fail the whole run rather than degrade.
MAX_MATRIX_JOBS = 256


def shard(items, spec):
    m = re.fullmatch(r"(\d+)/(\d+)", spec)
    if not m:
        fail(f"--shard must be `index/total`, got {spec!r}")
    index, total = int(m.group(1)), int(m.group(2))
    if total < 1 or index >= total:
        fail(f"--shard {spec} is out of range")
    # Check the whole split, not just this shard: every shard runs as its own
    # matrix, so one oversized shard breaks the run even if this one is fine.
    biggest = max(len(items[i::total]) for i in range(total))
    if biggest > MAX_MATRIX_JOBS:
        needed = -(-len(items) // MAX_MATRIX_JOBS)
        fail(
            f"--shard {spec} would put {biggest} cells in one shard, over "
            f"GitHub's {MAX_MATRIX_JOBS}-job matrix cap; use at least "
            f"{needed} shards for {len(items)} cells"
        )
    # Stride rather than block slicing: consecutive cells of one extension
    # differ only by arch/ts, so striding spreads a slow extension's cells over
    # all shards instead of stacking them in one.
    return items[index::total]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--targets", default=TARGETS)
    ap.add_argument("--shard", help="build only shard `index/total`")
    ap.add_argument(
        "--check",
        action="store_true",
        help="validate targets and report the cell count without emitting a matrix",
    )
    ap.add_argument("--group-index", action="store_true",
                    help="emit the deduplicated ext+version index matrix instead")
    ap.add_argument("--only", help="restrict to a single ext_name")
    ap.add_argument(
        "--min-shards",
        action="store_true",
        help="print the fewest shards that keep every shard under the matrix cap",
    )
    ap.add_argument(
        "--list-extensions",
        action="store_true",
        help="emit the extension names as a JSON array, one dispatched run each",
    )
    args = ap.parse_args()

    cfg = load_targets(args.targets)
    if args.only:
        # `only` may name a third-party extension (`ext_name`) or a bundled one
        # (`name`); filter both sections so either works.
        cfg["extensions"] = [
            e for e in cfg["extensions"] if e.get("ext_name") == args.only
        ]
        cfg["bundled"] = [b for b in cfg["bundled"] if b.get("name") == args.only]
        if not cfg["extensions"] and not cfg["bundled"]:
            fail(f"--only {args.only!r} matches no declared extension")
    include = build_matrix(cfg)

    if args.group_index:
        pairs = {}
        for c in include:
            pairs[(c["ext_name"], c["version"])] = {
                "ext_name": c["ext_name"],
                "version": c["version"],
            }
        print(json.dumps({"include": list(pairs.values())}))
        return

    if args.list_extensions:
        # Ordered biggest-first so the longest runs are dispatched (and start
        # queueing) before the short ones.
        counts = {}
        for c in include:
            counts[c["ext_name"]] = counts.get(c["ext_name"], 0) + 1
        names = sorted(counts, key=lambda n: (-counts[n], n))
        print(json.dumps(names))
        return

    if args.min_shards:
        print(max(1, -(-len(include) // MAX_MATRIX_JOBS)))
        return

    if args.shard:
        include = shard(include, args.shard)

    if args.check:
        by_ext = {}
        for c in include:
            by_ext.setdefault(c["ext_name"], 0)
            by_ext[c["ext_name"]] += 1
        for name in sorted(by_ext):
            print(f"{name:24} {by_ext[name]:4} cells", file=sys.stderr)
        print(f"{'TOTAL':24} {len(include):4} cells", file=sys.stderr)
        return

    print(json.dumps({"include": include}))


if __name__ == "__main__":
    main()
