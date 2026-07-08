#!/usr/bin/env python3
"""Regenerate src/docker/system-deps.json from mlocati/docker-php-extension-installer.

Usage: python3 scripts/extract-ipe-catalog.py path/to/install-php-extensions

Parses the `case "$1@$DISTRO"` block that maps each PHP extension to its
persistent (runtime) and volatile (build-only) system packages per distro.
Regex-style package names (e.g. `^librabbitmq[0-9]*$`) are dropped, since they
cannot be passed directly to apt-get/apk; the corresponding `-dev` build package
pulls the runtime lib in as a dependency anyway.
"""
import re, json, sys

def main():
    path = sys.argv[1] if len(sys.argv) > 1 else \
        "../docker-php-extension-installer/install-php-extensions"
    src = open(path).read()
    m = re.search(r'case "\$1@\$DISTRO" in\n(.*?)\n\t\tesac', src, re.S)
    entries = re.findall(
        r'\n\t\t\t([a-z0-9_]+)@(alpine|debian)\)\n(.*?)\n\t\t\t\t;;', m.group(1), re.S)
    cat = {}
    for ext, distro, body in entries:
        persistent, volatile = [], []
        for line in body.splitlines():
            mp = re.search(r'_persistent="\$buildRequiredPackageLists_persistent (.+?)"', line)
            mv = re.search(r'_volatile="\$buildRequiredPackageLists_volatile (.+?)"', line)
            if mp: persistent += mp.group(1).split()
            if mv: volatile += mv.group(1).split()

        # IPE shell variables (e.g. `$buildRequiredPackageLists_libssl`) are not
        # package names — drop them. Their contents are distro/PHP conditional
        # and not statically resolvable.
        def is_shell_var(p):
            return p.startswith("$")

        # A regex/glob pattern (e.g. `^librabbitmq[0-9]*$`) — a versioned runtime
        # lib whose exact name we resolve on the target distro at emit time.
        def is_pattern(p):
            return bool(re.search(r'[\^\[\]\*]', p))

        def dedup(l):
            return list(dict.fromkeys(l))

        # Persistent = runtime libs that must remain. Keep concrete names and
        # regex patterns (resolved at emit time); drop shell variables.
        persistent = dedup([p for p in persistent if not is_shell_var(p)])
        # Volatile = build-only (*-dev, tools). Drop shell vars and patterns; a
        # pattern can't be apt/apk-installed literally, and the -dev package we
        # keep pulls the runtime lib in anyway.
        volatile = dedup([p for p in volatile if not is_shell_var(p) and not is_pattern(p)])

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
        "libraries": load_existing_libraries("src/docker/system-deps.json"),
        "extensions": dict(sorted(cat.items())),
    }
    out = "src/docker/system-deps.json"
    open(out, "w").write(json.dumps(doc, indent=2) + "\n")
    print(f"wrote {len(cat)} extensions to {out}")


def load_existing_libraries(path):
    """Carry forward the hand-curated `libraries` map from the current file."""
    try:
        with open(path) as f:
            return json.load(f).get("libraries", {})
    except (OSError, ValueError):
        return {}

if __name__ == "__main__":
    main()
