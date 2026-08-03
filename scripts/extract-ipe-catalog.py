#!/usr/bin/env python3
"""Regenerate internal/docker/system-deps.json from mlocati/docker-php-extension-installer.

Usage: python3 scripts/extract-ipe-catalog.py path/to/install-php-extensions

Parses the `case "$1@$DISTRO"` block that maps each PHP extension to its
persistent (runtime) and volatile (build-only) system packages per distro.
Regex-style package names (e.g. `^librabbitmq[0-9]*$`) are kept in `persistent`
(resolved to a concrete package on the target distro at emit time) but dropped
from `volatile`, since a pattern cannot be passed to apt-get/apk literally and
the corresponding `-dev` build package pulls the runtime lib in anyway.
"""
import re, json, sys

CATALOG = "internal/docker/system-deps.json"

def main():
    path = sys.argv[1] if len(sys.argv) > 1 else \
        "../docker-php-extension-installer/install-php-extensions"
    src = open(path).read()
    m = re.search(r'case "\$1@\$DISTRO" in\n(.*?)\n\t\tesac', src, re.S)
    if not m:
        sys.exit(f"error: could not find the `case \"$1@$DISTRO\"` block in {path}; "
                 "the upstream format may have changed")
    # A case arm may list SEVERAL extensions for one distro, e.g.
    #   pgsql@alpine | pdo_pgsql@alpine | pq@alpine)
    # so the pattern captures the whole label list and each `ext@distro` pair is
    # expanded below. Matching only single-name arms silently dropped every
    # shared arm (pgsql, pdo_pgsql, odbc, pdo_odbc, oci8, pdo_oci, sodium,
    # sqlsrv, pdo_sqlsrv, ...), which then failed to build with
    # `--install-system-deps` because their dev packages were never installed.
    arms = re.findall(
        r'\n\t\t\t([a-z0-9_]+@(?:alpine|debian)(?:\s*\|\s*[a-z0-9_]+@(?:alpine|debian))*)\)\n(.*?)\n\t\t\t\t;;',
        m.group(1), re.S)
    entries = []
    for labels, body in arms:
        for label in labels.split("|"):
            ext, _, distro = label.strip().partition("@")
            entries.append((ext, distro, body))
    if not entries:
        sys.exit(f"error: parsed 0 extension case-arms from {path}; "
                 "refusing to overwrite the catalog with an empty result")
    cat = {}
    for ext, distro, body in entries:
        persistent, volatile = [], []
        for names, target in extract_lists(body):
            if target == "persistent":
                persistent += names
            else:
                volatile += names

        # IPE shell variables (e.g. `$buildRequiredPackageLists_libssl`) are not
        # package names — drop them. Their contents are distro/PHP conditional
        # and not statically resolvable.
        def is_shell_var(p):
            return p.startswith("$")

        # A regex/glob pattern (e.g. `^librabbitmq[0-9]*$`) — a versioned runtime
        # lib whose exact name we resolve on the target distro at emit time.
        # Must stay in sync with docker.IsPattern, which also treats ( ) ? as
        # metacharacters -- branch-conditional names are emitted as anchored
        # alternations like `^(libodbc2|libodbc1)$`.
        def is_pattern(p):
            return bool(re.search(r'[\^\[\]\*()?]', p))

        def dedup(l):
            return list(dict.fromkeys(l))

        # Persistent = runtime libs that must remain. Keep concrete names and
        # regex patterns (resolved at emit time); drop shell variables.
        persistent = dedup([p for p in persistent if not is_shell_var(p)])
        # Volatile = build-only (*-dev, tools). Drop shell vars and patterns; a
        # pattern can't be apt/apk-installed literally, and the -dev package we
        # keep pulls the runtime lib in anyway.
        volatile = dedup([p for p in volatile if not is_shell_var(p) and not is_pattern(p)])

        # A package cannot be in both lists: --cleanup-build-deps would remove
        # something the .so still needs at runtime. Persistent wins, since
        # keeping a package is always safe and dropping one is not.
        volatile = [p for p in volatile if p not in persistent]

        if persistent or volatile:
            cat.setdefault(ext, {})[distro] = {"persistent": persistent, "build_only": volatile}
    doc = {
        "_comment": "System build dependencies per extension and distro family, extracted from "
                    "mlocati/docker-php-extension-installer (install-php-extensions). 'persistent' "
                    "packages stay in the image; 'build_only' (*-dev, build tools) are removed after "
                    "building when --cleanup-build-deps is used. A persistent entry matching a "
                    "regex (contains ^ [ ] $ *) is a PATTERN resolved to a concrete package on the "
                    "target distro at emit time. Regenerate with scripts/extract-ipe-catalog.py. "
                    "Packagist lib-* requires take precedence over this table when present.",
        # The `libraries` map (composer `lib-<name>` → distro -dev package) is
        # hand-curated (from PIE's SystemDependenciesDefinition), not extracted
        # from IPE — so preserve it across regenerations.
        "libraries": load_existing_libraries(CATALOG),
        "extensions": dict(sorted(cat.items())),
    }
    with open(CATALOG, "w") as f:
        f.write(json.dumps(doc, indent=2) + "\n")
    print(f"wrote {len(cat)} extensions to {CATALOG}")


def extract_lists(body):
    """Yield (package names, "persistent"|"volatile") for one case-arm body.

    Some arms pick a different package per distro release, e.g. odbc uses
    libodbc2 on Debian 13+ and libodbc1 below it:

        if test $DISTRO_VERSION_NUMBER -ge 13; then
            ..._persistent="... libodbc2"
        else
            ..._persistent="... libodbc1"
        fi

    The catalog is flat, so emitting both names produces an install list that
    can never be satisfied -- and because apt/apk installs atomically, the one
    missing name fails the whole batch, taking the -dev packages with it. Names
    that appear only in mutually exclusive branches are therefore collapsed into
    a single anchored alternation pattern (`^(libodbc2|libodbc1)$`), which the
    existing pattern resolver matches against the packages the target distro
    actually has.
    """
    persistent_re = re.compile(r'_persistent="\$buildRequiredPackageLists_persistent (.+?)"')
    volatile_re = re.compile(r'_volatile="\$buildRequiredPackageLists_volatile (.+?)"')

    # Track brace/branch depth so branch-local assignments can be told apart
    # from unconditional ones. IPE only ever nests if/else one level deep here.
    branch_depth = 0
    # {target: {branch_id: [names]}} for conditional, plus unconditional lists.
    unconditional = {"persistent": [], "volatile": []}
    branched = {"persistent": {}, "volatile": {}}
    branch_id = 0

    for line in body.splitlines():
        stripped = line.strip()
        if re.match(r'(if|elif)\b.*;\s*then$', stripped) or stripped == "else":
            if stripped in ("else",) or re.match(r'elif\b', stripped):
                branch_id += 1
            else:
                branch_depth += 1
                branch_id += 1
            continue
        if stripped == "fi":
            branch_depth = max(0, branch_depth - 1)
            continue

        for regex, target in ((persistent_re, "persistent"), (volatile_re, "volatile")):
            m = regex.search(line)
            if not m:
                continue
            names = m.group(1).split()
            if branch_depth > 0:
                branched[target].setdefault(branch_id, []).extend(names)
            else:
                unconditional[target].extend(names)

    for target in ("persistent", "volatile"):
        yield unconditional[target], target

        groups = list(branched[target].values())
        if not groups:
            continue
        # Names common to every branch are unconditional in practice.
        common = [n for n in groups[0] if all(n in g for g in groups[1:])]
        yield common, target

        alternatives = []
        for g in groups:
            for n in g:
                if n not in common and n not in alternatives:
                    alternatives.append(n)
        if not alternatives:
            continue
        if target == "volatile":
            # Build-only packages are *-dev/tools, and installing one that this
            # release does not strictly need is harmless -- they are removed
            # again by --cleanup-build-deps. Collapsing them into a pattern
            # instead would drop them entirely (the volatile filter discards
            # patterns, since apt/apk cannot install one literally), which is
            # how a conditional libzstd-dev or libbrotli-dev would go missing
            # and fail the build. So keep every alternative verbatim.
            yield alternatives, target
            continue

        # A runtime alternative may already BE a pattern (`^libzstd[0-9]*$`);
        # strip its anchors and keep it as-is rather than escaping it into a
        # literal, so the alternation preserves the resolver's semantics.
        def as_branch(name):
            if re.search(r'[\^\[\]\*()?]', name):
                return name.strip("^$")
            return re.escape(name)

        yield [f"^({'|'.join(as_branch(a) for a in alternatives)})$"], target


def load_existing_libraries(path):
    """Carry forward the hand-curated `libraries` map from the current file.

    The map is not extractable from upstream, so losing it would be silent data
    loss: only a genuinely absent file may fall back to an empty map.
    """
    try:
        with open(path) as f:
            return json.load(f).get("libraries", {})
    except FileNotFoundError:
        return {}
    except (OSError, ValueError) as err:
        sys.exit(f"error: refusing to overwrite {path}: "
                 f"cannot read its hand-curated `libraries` map ({err})")

if __name__ == "__main__":
    main()
