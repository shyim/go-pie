#!/usr/bin/env bash
#
# End-to-end integration tests for gpie inside official PHP Docker images.
#
# These are slow (they compile gpie for Linux and run real installs), so they
# are NOT part of `cargo test`. Run them explicitly:
#
#   ./tests/docker/run.sh              # amd64 (glibc, Debian) scenarios
#   PHP_IMAGE=php:8.3-cli ./tests/docker/run.sh
#   ALPINE=1 ./tests/docker/run.sh     # also build a musl binary and run Alpine
#
# Requires Docker. Exits non-zero on the first failed assertion.
set -euo pipefail

cd "$(dirname "$0")/../.."

# Remove the compiled test binaries however we exit: a failed assertion under
# `set -e`, an interrupt, or a clean finish.
trap 'rm -f gpie-glibc gpie-musl 2>/dev/null || true' EXIT

# Default to the host architecture so runs are native (no emulation). Override
# with PLATFORM=linux/amd64 to force a specific arch.
default_platform() {
    case "$(uname -m)" in
        arm64 | aarch64) echo "linux/arm64" ;;
        *) echo "linux/amd64" ;;
    esac
}
PLATFORM="${PLATFORM:-$(default_platform)}"
PHP_IMAGE="${PHP_IMAGE:-php:8.4-cli}"
EXAMPLE="asgrim/example-pie-extension"

pass=0
fail=0

note() { printf '\n\033[1;36m== %s\033[0m\n' "$*"; }
ok()   { printf '  \033[32mok\033[0m   %s\n' "$*"; pass=$((pass + 1)); }
bad()  { printf '  \033[31mFAIL\033[0m %s\n' "$*"; fail=$((fail + 1)); }

# assert_contains <description> <haystack> <needle>
# Uses bash native substring matching (no pipe) so `set -o pipefail` cannot be
# tripped by grep -q closing the pipe early (SIGPIPE) on a match.
assert_contains() {
    if [[ "$2" == *"$3"* ]]; then
        ok "$1"
    else
        bad "$1 (expected to find: $3)"
        printf '    --- last lines of output ---\n' >&2
        printf '%s\n' "$2" | tail -20 | sed 's/^/    /' >&2
    fi
}

# assert_equals <description> <actual> <expected>
# For counts and other exact values, where substring matching would accept
# "10" or "21" as a match for "1".
assert_equals() {
    if [[ "${2//[[:space:]]/}" == "$3" ]]; then
        ok "$1"
    else
        bad "$1 (expected exactly: $3, got: $2)"
    fi
}

# assert_contains_any <description> <haystack> <needle1> <needle2>
# Passes if the haystack contains either needle (used where the artifact path
# differs by architecture — e.g. a prebuilt binary on amd64 vs a source build
# on arm64).
assert_contains_any() {
    if [[ "$2" == *"$3"* || "$2" == *"$4"* ]]; then
        ok "$1"
    else
        bad "$1 (expected: '$3' or '$4')"
        printf '    --- last lines of output ---\n' >&2
        printf '%s\n' "$2" | tail -20 | sed 's/^/    /' >&2
    fi
}

require_docker() {
    if ! command -v docker >/dev/null 2>&1; then
        echo "docker not found; skipping integration tests" >&2
        exit 0
    fi
}

build_linux_binary() {
    local target_image="$1" out="$2"
    note "Building gpie for $PLATFORM ($target_image)"
    docker run --rm --platform "$PLATFORM" -v "$PWD":/src -w /src "$target_image" \
        sh -c "CGO_ENABLED=0 go build -buildvcs=false -ldflags=\"-s -w\" -o /src/$out ."
}

# run_gpie <binary> <args...> — runs gpie in a fresh PHP container, echoes output
run_gpie() {
    local bin="$1"; shift
    # `|| true` so a non-zero exit (e.g. a deliberate `grep` miss, or an
    # expected install refusal) doesn't abort the script under `set -e`; we
    # assert on the captured output, not the exit code.
    docker run --rm --platform "$PLATFORM" -v "$PWD/$bin":/usr/local/bin/gpie "$PHP_IMAGE" \
        sh -c "$*" 2>&1 || true
}

require_docker
 
# ---- glibc / Debian scenarios -------------------------------------------------
build_linux_binary golang:1.26-bookworm gpie-glibc
BIN=gpie-glibc

note "info reports the target PHP"
out="$(run_gpie "$BIN" "gpie info")"
assert_contains "info shows Target PHP" "$out" "Target PHP"
assert_contains "info shows extension_dir" "$out" "extension_dir"

note "install $EXAMPLE — prebuilt binary (amd64) or source build (arm64)"
out="$(run_gpie "$BIN" "gpie install $EXAMPLE && php -m | grep -i example_pie")"
# amd64 has a prebuilt Linux binary (sha256-verified); arm64 has none, so it
# falls back to a source build. Accept either path.
assert_contains_any "artifact obtained" "$out" "sha256 checksum verified" "Build complete"
assert_contains "install complete" "$out" "Install complete"
assert_contains "extension loads" "$out" "example_pie_extension"

note "show reports the managed extension and version"
out="$(run_gpie "$BIN" "gpie install $EXAMPLE >/dev/null 2>&1; gpie show")"
assert_contains "show lists managed ext" "$out" "example_pie_extension"
assert_contains "show attributes package" "$out" "$EXAMPLE"

note "uninstall round-trips cleanly"
out="$(run_gpie "$BIN" "gpie install $EXAMPLE >/dev/null 2>&1; gpie uninstall $EXAMPLE; gpie show")"
assert_contains "uninstall removes" "$out" "uninstalled"
assert_contains "show is empty after uninstall" "$out" "(none)"

note "bundled extension (gd) via docker-php-ext-install"
out="$(run_gpie "$BIN" "gpie install gd --install-system-deps && php -m | grep -i '^gd$'")"
assert_contains "gd recognised as bundled" "$out" "bundled"
assert_contains "gd loads" "$out" "gd"

note "batched system deps: two extensions, one apt pass"
out="$(run_gpie "$BIN" "gpie install php-amqp/php-amqp imagick/imagick --install-system-deps 2>&1 | grep -c 'Installed system dependencies' || true")"
assert_equals "single system-deps pass" "$out" "1"

note "parallel source builds (--jobs 2): both build, output grouped, both load"
out="$(run_gpie "$BIN" "gpie install php-amqp/php-amqp phpredis/phpredis --install-system-deps --jobs 2 && php -m | grep -iE '^amqp\$|^redis\$'")"
assert_contains "parallel builds 2 extensions" "$out" "building 2 extension(s)"
assert_contains "parallel: both succeed" "$out" "2/2 extensions succeeded"
assert_contains "parallel: amqp loads" "$out" "amqp"
assert_contains "parallel: redis loads" "$out" "redis"

note "platform requirement check blocks incompatible PHP"
# swoole requires php >=8.2 <8.6; force an old PHP to trigger the block.
out="$(docker run --rm --platform "$PLATFORM" -v "$PWD/$BIN":/usr/local/bin/gpie php:8.1-cli \
    sh -c "gpie install swoole/swoole" 2>&1 || true)"
assert_contains "req mismatch reported" "$out" "requires php"
assert_contains "install refused" "$out" "not compatible"

# ---- musl / Alpine scenarios (opt-in) ----------------------------------------
if [ "${ALPINE:-0}" = "1" ]; then
    build_linux_binary golang:1.26-alpine gpie-musl
    PHP_IMAGE="php:8.4-alpine"
    note "Alpine: bundled intl loads"
    out="$(run_gpie gpie-musl "gpie install intl --install-system-deps && php -m | grep -i '^intl$'")"
    assert_contains "intl loads on alpine" "$out" "intl"
fi

# ---- summary ------------------------------------------------------------------
note "Results: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
