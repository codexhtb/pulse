#!/usr/bin/env bash

set -Eeuo pipefail

readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"
readonly BUNDLE=${1:-"$REPO_ROOT/dist/pulse-agent-bundle.tar.gz"}
readonly TEST_LOCALE=${PULSE_AGENT_TEST_LOCALE:-en_US.UTF-8}

TEMP_DIR=""

cleanup() {
    if [[ -n "$TEMP_DIR" && -d "$TEMP_DIR" ]]; then
        rm -rf -- "$TEMP_DIR"
    fi
}
trap cleanup EXIT HUP INT TERM

die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

[[ ${EUID:-$(id -u)} -eq 0 ]] || die "run as root so extracted bundle ownership can be verified"
[[ -f "$BUNDLE" ]] || die "bundle not found: $BUNDLE"
LC_ALL="$TEST_LOCALE" locale charmap >/dev/null 2>&1 || die "test locale is unavailable: $TEST_LOCALE"

TEMP_DIR=$(mktemp -d /tmp/pulse-agent-bundle-locale.XXXXXX)
tar -xzf "$BUNDLE" -C "$TEMP_DIR"

LC_ALL="$TEST_LOCALE" bash -c '
    source "$1/install-pulse-agent.sh"
    verify_bundle
' _ "$TEMP_DIR/pulse-agent-bundle"

printf 'PASS: bundle preflight is locale-independent under %s\n' "$TEST_LOCALE"
