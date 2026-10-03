#!/usr/bin/env bash

set -Eeuo pipefail
IFS=$'\n\t'

readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"
readonly DEFAULT_OUTPUT="$REPO_ROOT/dist/pulse-agent-bundle.tar.gz"

TEMP_DIR=""
OUTPUT_TEMP=""

cleanup() {
    if [[ -n "$OUTPUT_TEMP" && -e "$OUTPUT_TEMP" ]]; then
        rm -f -- "$OUTPUT_TEMP"
    fi
    if [[ -n "$TEMP_DIR" && -d "$TEMP_DIR" ]]; then
        rm -rf -- "$TEMP_DIR"
    fi
}
trap cleanup EXIT HUP INT TERM

die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

usage() {
    cat <<EOF
Usage: ./deploy/build-pulse-agent-bundle.sh [--output PATH]

Builds a Linux pulse-agent production bundle. Default output:
  $DEFAULT_OUTPUT

Set PULSE_AGENT_GOARCH to override the target architecture. The default is the
build host Go architecture.
EOF
}

main() {
    local output="$DEFAULT_OUTPUT" target_arch bundle_dir epoch
    local -a payload=(
        install-pulse-agent.sh
        pulse-agent
        pulse-nft-helper
        pulse-agent.service
        sudoers-pulse-agent
    )

    case ${1:-} in
        '') ;;
        --output)
            [[ $# -eq 2 && -n ${2:-} ]] || { usage >&2; exit 2; }
            output=$2
            ;;
        -h|--help)
            usage
            return
            ;;
        *)
            usage >&2
            exit 2
            ;;
    esac
    [[ $# -le 2 ]] || { usage >&2; exit 2; }

    for command_name in chmod dirname find go install mkdir mktemp mv rm sha256sum sort tar; do
        command -v "$command_name" >/dev/null 2>&1 || die "required build-host command is missing: $command_name"
    done
    [[ -f "$SCRIPT_DIR/install-pulse-agent.sh" ]] || die "installer source is missing"
    [[ -f "$SCRIPT_DIR/systemd/pulse-agent.service" ]] || die "systemd unit source is missing"
    [[ -f "$SCRIPT_DIR/sudoers-pulse-agent" ]] || die "sudoers source is missing"

    if [[ "$output" != /* ]]; then
        output="$(pwd)/$output"
    fi
    mkdir -p -- "$(dirname -- "$output")"
    OUTPUT_TEMP=$(mktemp "${output}.tmp.XXXXXX")
    TEMP_DIR=$(mktemp -d /tmp/pulse-agent-bundle.XXXXXX)
    bundle_dir="$TEMP_DIR/pulse-agent-bundle"
    install -d -m 0755 "$bundle_dir"

    printf 'Running pulse-agent/helper tests...\n'
    (
        cd "$REPO_ROOT"
        go test -count=1 ./cmd/pulse-agent ./cmd/pulse-nft-helper
    )

    target_arch=${PULSE_AGENT_GOARCH:-$(go env GOARCH)}
    [[ "$target_arch" =~ ^[A-Za-z0-9_]+$ ]] || die "invalid PULSE_AGENT_GOARCH: $target_arch"
    printf 'Building Linux/%s binaries...\n' "$target_arch"
    (
        cd "$REPO_ROOT"
        CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go build -trimpath -buildvcs=false -ldflags='-s -w' \
            -o "$bundle_dir/pulse-agent" ./cmd/pulse-agent
        CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go build -trimpath -buildvcs=false -ldflags='-s -w' \
            -o "$bundle_dir/pulse-nft-helper" ./cmd/pulse-nft-helper
    )

    install -m 0755 "$SCRIPT_DIR/install-pulse-agent.sh" "$bundle_dir/install-pulse-agent.sh"
    install -m 0644 "$SCRIPT_DIR/systemd/pulse-agent.service" "$bundle_dir/pulse-agent.service"
    install -m 0440 "$SCRIPT_DIR/sudoers-pulse-agent" "$bundle_dir/sudoers-pulse-agent"
    chmod 0755 "$bundle_dir/install-pulse-agent.sh" "$bundle_dir/pulse-agent" "$bundle_dir/pulse-nft-helper"
    chmod 0644 "$bundle_dir/pulse-agent.service"
    chmod 0440 "$bundle_dir/sudoers-pulse-agent"

    (
        cd "$bundle_dir"
        sha256sum "${payload[@]}" > SHA256SUMS
        chmod 0644 SHA256SUMS
        sha256sum --check --strict SHA256SUMS
        [[ $(find . -mindepth 1 -maxdepth 1 -printf '%f\n' | sort) == \
            $'SHA256SUMS\ninstall-pulse-agent.sh\npulse-agent\npulse-agent.service\npulse-nft-helper\nsudoers-pulse-agent' ]]
    ) || die "bundle staging verification failed"

    epoch=${SOURCE_DATE_EPOCH:-0}
    [[ "$epoch" =~ ^[0-9]+$ ]] || die "SOURCE_DATE_EPOCH must be an integer"
    tar --sort=name --mtime="@$epoch" --owner=0 --group=0 --numeric-owner \
        -C "$TEMP_DIR" -czf "$OUTPUT_TEMP" pulse-agent-bundle
    chmod 0644 "$OUTPUT_TEMP"
    mv -f -- "$OUTPUT_TEMP" "$output"
    OUTPUT_TEMP=""

    printf 'Bundle created: %s\n' "$output"
    sha256sum "$output"
}

main "$@"
