#!/usr/bin/env bash
set -Eeuo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
# shellcheck source=scripts/docker-port-check.sh
source "$ROOT/scripts/docker-port-check.sh"

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

# Docker represents EXPOSE without host publication as a null port entry. The
# inspect formatter used by doctor flattens that state to no binding rows.
unpublished=''
if pulse_port_is_published "$unpublished" 8081/tcp; then
  fail 'EXPOSE-only API port was classified as published'
fi
if pulse_port_is_published "$unpublished" 9092/udp; then
  fail 'EXPOSE-only live UDP port was classified as published'
fi

forbidden=$'8081/tcp\t0.0.0.0\t18081'
pulse_port_is_published "$forbidden" 8081/tcp || \
  fail 'real API host binding was not detected'
[[ $(pulse_port_binding_summary "$forbidden" 8081/tcp) == '0.0.0.0:18081' ]] || \
  fail 'real API host binding summary is incorrect'

expected=$'8443/tcp\t0.0.0.0\t8443\n8443/tcp\t::\t8443\n8443/tcp\t\t8443\n6000/tcp\t0.0.0.0\t16000'
pulse_port_uses_only_host_port "$expected" 8443/tcp 8443 || \
  fail 'expected Web host bindings were rejected'
pulse_port_uses_only_host_port "$expected" 6000/tcp 16000 || \
  fail 'expected DNStap host binding was rejected'
[[ $(pulse_port_binding_summary "$expected" 8443/tcp) == '0.0.0.0:8443,[::]:8443,*:8443' ]] || \
  fail 'Docker Desktop/WSL host binding summary is incorrect'
if pulse_port_uses_only_host_port "$expected" 6000/tcp 6000; then
  fail 'wrong configured DNStap host port was accepted'
fi
if pulse_port_is_published "$expected" 9093/tcp; then
  fail 'missing collector status binding was classified as published'
fi

echo 'doctor port regression: PASS'
