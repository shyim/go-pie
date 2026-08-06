#!/usr/bin/env python3
"""Report which extensions still have unbuilt cells in GHCR.

Used by `Nightly prebuild` to dispatch a run only for extensions that are
actually missing cells, instead of rebuilding the whole matrix every night.

Usage:
  python3 scripts/check-prebuilt-coverage.py --namespace shyim/gpie-ext
  python3 scripts/check-prebuilt-coverage.py --namespace ... --only redis
  python3 scripts/check-prebuilt-coverage.py --namespace ... --report

Prints a JSON array of ext_names that need a build (empty array = fully
covered). `--report` instead writes a human-readable per-extension table to
stderr and prints nothing to stdout.

## Why this compares tags rather than asking the registry for a cell

The published cell id carries two things the matrix cannot know up front:

* the distro LABEL, which `gpie` derives from /etc/os-release inside the
  container. `php:8.4-alpine3.24` reports `alpine@3.24.1`, and that patch level
  floats as Alpine respins, so `alpine3.24` cannot be turned into an exact
  label here (see docker.Distro.Label and the note in prebuild-targets.yml).
* the config hash, which depends on the configure options the build chose.

So a cell tag is matched on the axes that ARE knowable -- version, php, distro
family + minor, arch, thread-safety -- with the distro patch and config hash
left as wildcards.

## Erring towards "missing"

A false "missing" costs one redundant rebuild, which the per-cell tag overwrite
absorbs harmlessly. A false "present" means a genuinely absent cell is never
built and every client asking for it silently falls back to a source build --
the exact failure this cache exists to prevent. Every ambiguity below therefore
resolves to "missing": an unreachable registry, an unparseable tag, a
truncated tag list, or an unknown base image all count the extension as needing
a build.

That default has one sharp edge, which is why `--fail-on-registry-error`
exists: if GHCR throttles the whole sweep, "assume missing" turns into "rebuild
the entire matrix". Reporting is per-extension, so a handful of throttled repos
degrade gracefully, but a wholesale outage should fail the step loudly instead
of quietly queueing thousands of jobs. The workflow therefore runs with that
flag and a single query pass (`--report --emit-json`), never two.
"""
import argparse
import json
import os
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

GHCR = "https://ghcr.io"
TOKEN_URL = GHCR + "/token?service=ghcr.io&scope=repository:{repo}:pull"

# Base-image suffix -> (distro family, version prefix that the in-container
# label must start with). Alpine's label carries a floating patch
# (alpine@3.24.1), so it is matched on the `major.minor` prefix rather than
# equality; Debian's label is the bare major (debian@12) and matches exactly.
#
# Keys must stay in sync with BASES in gen-prebuild-matrix.py. An unknown base
# is not guessed -- it makes the extension count as missing.
BASE_LABELS = {
    "bookworm": ("debian", "12", True),
    "trixie": ("debian", "13", True),
    "alpine3.23": ("alpine", "3.23", False),
    "alpine3.24": ("alpine", "3.24", False),
}


def log(msg):
    print(msg, file=sys.stderr)


def http_json(url, token=None, retries=3):
    """GET a JSON body, returning (payload, headers).

    Retries transient failures: a registry blip must not be read as "this
    extension is fully covered".
    """
    headers = {
        "User-Agent": "gpie-coverage-check",
        "Accept": "application/json",
    }
    if token:
        headers["Authorization"] = "Bearer " + token
    last = None
    for attempt in range(retries):
        req = urllib.request.Request(url, headers=headers)
        try:
            with urllib.request.urlopen(req, timeout=30) as r:
                return json.load(r), r.headers
        except urllib.error.HTTPError as e:
            # 401/404 on a tag list is the normal "package does not exist yet"
            # answer (GHCR denies the pull token for an absent repo), so it is
            # reported rather than retried.
            if e.code in (401, 403, 404):
                return None, None
            last = f"HTTP {e.code}"
            # 429/503 under a whole-matrix sweep is throttling, not absence.
            # Honour Retry-After and back off harder: treating it as "missing"
            # would queue a full rebuild.
            if e.code in (429, 503) and attempt < retries - 1:
                time.sleep(retry_after(e.headers, 5 * (attempt + 1)))
                continue
        except (urllib.error.URLError, TimeoutError, json.JSONDecodeError) as e:
            last = str(e)
        if attempt < retries - 1:
            time.sleep(2 ** attempt)
    raise RegistryError(last or "unknown error")


def retry_after(headers, default):
    """The Retry-After delay in seconds, clamped so a bad value cannot stall."""
    raw = (headers or {}).get("Retry-After", "")
    try:
        return max(1, min(60, int(str(raw).strip())))
    except (TypeError, ValueError):
        return default


class RegistryError(Exception):
    pass


def pull_token(repo):
    """A pull token for one GHCR repo, or None if the repo is not accessible.

    A GITHUB_TOKEN in the environment is used when present so the check also
    works against private packages; public repos need no credentials.
    """
    url = TOKEN_URL.format(repo=repo)
    gh_token = os.environ.get("GITHUB_TOKEN", "").strip()
    headers = {"User-Agent": "gpie-coverage-check"}
    if gh_token:
        # GHCR accepts the Actions token as basic-auth password for the token
        # exchange.
        import base64

        basic = base64.b64encode(f"x:{gh_token}".encode()).decode()
        headers["Authorization"] = "Basic " + basic
    req = urllib.request.Request(url, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            return json.load(r).get("token")
    except urllib.error.HTTPError as e:
        if e.code in (401, 403, 404):
            return None
        raise RegistryError(f"token exchange: HTTP {e.code}")
    except (urllib.error.URLError, TimeoutError, json.JSONDecodeError) as e:
        raise RegistryError(f"token exchange: {e}")


def list_tags(repo):
    """Every tag in a repo, following pagination.

    Returns None if the repo does not exist. GHCR caps a page at 1000 tags and
    advertises the next page in a `Link: rel="next"` header -- a single
    unpaginated request silently truncates (verified: gpie-ext/xdebug returns
    exactly 1000 with a next link), which would make cells beyond the cut look
    missing. Harmless in itself, but it would defeat the whole point of the
    check, so the pages are walked to the end.
    """
    token = pull_token(repo)
    if token is None:
        return None

    tags = []
    url = f"{GHCR}/v2/{repo}/tags/list?n=1000"
    seen_urls = set()
    while url:
        if url in seen_urls:  # defensive: a self-referential Link would spin
            raise RegistryError("pagination loop")
        seen_urls.add(url)
        payload, headers = http_json(url, token=token)
        if payload is None:
            return None if not tags else tags
        tags.extend(payload.get("tags") or [])
        url = next_page(headers)
    return tags


def next_page(headers):
    """The absolute URL of the next tag page, from the RFC5988 Link header."""
    if not headers:
        return None
    link = headers.get("Link") or headers.get("link")
    if not link:
        return None
    for part in link.split(","):
        section = part.split(";")
        if len(section) < 2 or 'rel="next"' not in part:
            continue
        target = section[0].strip().strip("<>")
        return urllib.parse.urljoin(GHCR, target)
    return None


# A published cell tag is `<version>-<cell id with / and @ mapped to _>`, e.g.
#   3.5.3-xdebug_3.5.3_php8.2_alpine_3.23.5_aarch64_zts_nodebug_cfg-00000000
# The cell id itself is
#   <ext>/<ver>/php<php>/<distro>@<distrover>/<arch>/<ts>/<debug>/cfg-<hash>
# so after the mapping the fields are positional from the RIGHT, which is what
# this parses: counting from the left is ambiguous because an extension name may
# itself contain `_` (pdo_mysql, sysvshm).
CELL_TAG = re.compile(
    r"^(?P<version>.+?)-"
    r"(?P<rest>.+)_php(?P<php>[0-9.]+)_"
    r"(?P<distro>[a-z]+)_(?P<distrover>[0-9.]+)_"
    r"(?P<arch>x86_64|aarch64|x86)_"
    r"(?P<ts>nts|zts)_(?P<debug>debug|nodebug)_cfg-(?P<cfg>[0-9a-f]+)$"
)


def parse_cell_tag(tag):
    """The (version, php, distro, distrover, arch, ts) a tag covers, or None."""
    if tag.startswith("sha256-"):
        # Attestation / referrers tags, not cells.
        return None
    m = CELL_TAG.match(tag)
    if not m:
        return None
    return (
        m.group("version"),
        m.group("php"),
        m.group("distro"),
        m.group("distrover"),
        m.group("arch"),
        m.group("ts"),
    )


def covered_key(cell):
    """The lookup key for an expected matrix cell, or None if unknowable.

    Bundled extensions key their php axis on the full patch version, matching
    oci.NewBundledCell; third-party ones on major.minor.
    """
    label = BASE_LABELS.get(cell["base"])
    if not label:
        return None
    family, ver_prefix, exact = label
    return (
        cell["version"],
        cell["php"] if cell["bundled"] != "true" else cell["version"],
        family,
        ver_prefix,
        exact,
        cell["arch"],
        cell["ts"],
    )


def tag_matches(key, parsed):
    """Whether a published tag satisfies an expected cell key."""
    version, php, family, ver_prefix, exact, arch, ts = key
    p_version, p_php, p_distro, p_distrover, p_arch, p_ts = parsed
    if (p_version, p_arch, p_ts) != (version, arch, ts):
        return False
    if p_php != php or p_distro != family:
        return False
    if exact:
        return p_distrover == ver_prefix
    # Alpine: the label's patch floats, so match the major.minor prefix and
    # require a component boundary so `3.2` cannot match `3.24`.
    return p_distrover == ver_prefix or p_distrover.startswith(ver_prefix + ".")


def coverage(cells, namespace, ext):
    """(missing, total, note, errored) for one extension's expected cells."""
    repo = f"{namespace}/{ext}"
    try:
        tags = list_tags(repo)
    except RegistryError as e:
        # Unreachable registry: assume nothing is covered rather than skipping
        # a build that may be needed. Flagged as `errored` so the caller can
        # tell a real gap from an unreadable one.
        return len(cells), len(cells), f"registry error ({e}); assuming missing", True

    if tags is None:
        return len(cells), len(cells), "package does not exist yet", False

    parsed = [p for p in (parse_cell_tag(t) for t in tags) if p]
    missing = 0
    unknown_base = False
    for c in cells:
        key = covered_key(c)
        if key is None:
            unknown_base = True
            missing += 1
            continue
        if not any(tag_matches(key, p) for p in parsed):
            missing += 1
    note = ""
    if unknown_base:
        note = "unrecognised base image in matrix; assuming missing"
    return missing, len(cells), note, False


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--namespace", required=True,
                    help="GHCR namespace holding the ext repos, e.g. org/gpie-ext")
    ap.add_argument("--only", help="check just this ext_name")
    ap.add_argument("--report", action="store_true",
                    help="write a per-extension table to stderr")
    ap.add_argument("--emit-json", action="store_true",
                    help="print the JSON array even with --report, so one pass "
                         "produces both (the registry is queried only once)")
    ap.add_argument("--fail-on-registry-error", action="store_true",
                    help="exit non-zero if any extension could not be read, "
                         "instead of reporting its cells as missing")
    ap.add_argument("--matrix", help="read the matrix from this JSON file "
                                     "instead of invoking the generator")
    args = ap.parse_args()

    if not re.fullmatch(r"[A-Za-z0-9._/-]{1,200}", args.namespace):
        sys.exit(f"error: refusing malformed namespace: {args.namespace}")
    if args.only and not re.fullmatch(r"[a-z0-9_]{1,64}", args.only):
        sys.exit(f"error: refusing malformed --only: {args.only}")

    cells = load_matrix(args.matrix, args.only)

    by_ext = {}
    for c in cells:
        by_ext.setdefault(c["ext_name"], []).append(c)

    needed = []
    rows = []
    errored = []
    for ext in sorted(by_ext):
        missing, total, note, err = coverage(by_ext[ext], args.namespace, ext)
        if missing:
            needed.append((ext, missing))
        if err:
            errored.append(ext)
        rows.append((ext, missing, total, note))

    if args.report:
        log(f"{'extension':24} {'missing':>8} {'total':>7}")
        for ext, missing, total, note in rows:
            suffix = f"  {note}" if note else ""
            log(f"{ext:24} {missing:>8} {total:>7}{suffix}")
        covered = sum(1 for _, m, _, _ in rows if not m)
        log(f"\n{covered}/{len(rows)} extension(s) fully covered; "
            f"{len(needed)} need a build")
        if errored:
            log(f"{len(errored)} extension(s) unreadable: {', '.join(errored)}")

    # A sweep that could not read the registry would report every cell missing
    # and queue a full rebuild of the matrix. Fail instead: a skipped night is
    # cheap to retry, thousands of redundant jobs are not.
    if errored and args.fail_on_registry_error:
        sys.exit(
            f"error: {len(errored)} extension(s) could not be read from the "
            f"registry ({', '.join(errored)}); refusing to treat them as "
            "missing. Re-run, or use `force` to rebuild regardless."
        )

    if args.report and not args.emit_json:
        return

    # Biggest gap first, so the longest runs start queueing earliest -- same
    # rationale as --list-extensions in the generator.
    needed.sort(key=lambda kv: (-kv[1], kv[0]))
    print(json.dumps([ext for ext, _ in needed]))


def load_matrix(path, only):
    """The expected cells, from a file or by invoking the generator."""
    if path:
        with open(path) as fh:
            data = json.load(fh)
        cells = data["include"] if isinstance(data, dict) else data
    else:
        import subprocess

        here = os.path.dirname(os.path.abspath(__file__))
        cmd = [sys.executable, os.path.join(here, "gen-prebuild-matrix.py")]
        if only:
            cmd += ["--only", only]
        out = subprocess.run(cmd, capture_output=True, text=True, check=False)
        if out.returncode != 0:
            sys.exit(f"error: matrix generation failed:\n{out.stderr.strip()}")
        cells = json.loads(out.stdout)["include"]
    if only:
        cells = [c for c in cells if c.get("ext_name") == only]
    if not cells:
        sys.exit("error: no cells to check")
    return cells


if __name__ == "__main__":
    main()
