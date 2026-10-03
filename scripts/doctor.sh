#!/usr/bin/env bash
set -Eeuo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
failures=0

row() { printf '%-20s %s\n' "$1" "$2"; }
pass() { row "$1" "OK"; }
fail() { row "$1" "FAIL${2:+ — $2}"; failures=$((failures + 1)); }

printf 'Pulse Doctor\n\n'
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then pass Docker; else fail Docker "daemon unavailable"; fi
if docker compose version >/dev/null 2>&1; then pass Compose; else fail Compose "v2 unavailable"; fi
if [[ -r "$ROOT/.env" ]]; then pass Configuration; elif [[ -e "$ROOT/.env" ]]; then fail Configuration ".env is not readable by $(id -un)"; else fail Configuration ".env missing"; fi

if (( failures > 0 )); then exit 1; fi
# shellcheck source=scripts/lib.sh
source "$ROOT/scripts/lib.sh"
# shellcheck source=scripts/docker-port-check.sh
source "$ROOT/scripts/docker-port-check.sh"
require_env

if compose config --quiet >/dev/null 2>&1; then pass "Compose config"; else fail "Compose config" "invalid"; fi

network="$(project_name)_pulse-core"
if docker network inspect "$network" >/dev/null 2>&1; then pass "Pulse network"; else fail "Pulse network" "missing"; fi

volume_fail=0
for short in clickhouse-data collector-state api-state; do
  docker volume inspect "$(volume_name "$short")" >/dev/null 2>&1 || volume_fail=1
done
if (( volume_fail == 0 )); then pass "Data volumes"; else fail "Data volumes" "one or more missing"; fi

for service in clickhouse pulse-collector pulse-api pulse-web; do
  id=$(container_id "$service")
  if [[ -z "$id" ]]; then
    fail "$service" "not created"
    continue
  fi
  health=$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$id" 2>/dev/null || true)
  if [[ "$health" == healthy ]]; then pass "$service"; else fail "$service" "$health"; fi
done

if compose exec -T clickhouse clickhouse-client --user "$CLICKHOUSE_ADMIN_USER" --password "$CLICKHOUSE_ADMIN_PASSWORD" --query 'SELECT 1' >/dev/null 2>&1; then
  pass "ClickHouse query"
else
  fail "ClickHouse query"
fi
if compose exec -T pulse-api wget -q -O /dev/null http://127.0.0.1:8081/internal/health >/dev/null 2>&1; then pass "API health"; else fail "API health"; fi
if compose exec -T pulse-collector wget -q -O /dev/null http://127.0.0.1:9093/internal/status >/dev/null 2>&1; then pass "Collector status"; else fail "Collector status"; fi
if compose exec -T pulse-web wget --no-check-certificate -q -O /dev/null https://127.0.0.1:8443/healthz >/dev/null 2>&1; then pass "Web health"; else fail "Web health"; fi

clickhouse_bindings=$(pulse_container_host_bindings "$(container_id clickhouse)" 2>/dev/null || true)
collector_bindings=$(pulse_container_host_bindings "$(container_id pulse-collector)" 2>/dev/null || true)
api_bindings=$(pulse_container_host_bindings "$(container_id pulse-api)" 2>/dev/null || true)
web_bindings=$(pulse_container_host_bindings "$(container_id pulse-web)" 2>/dev/null || true)
dnstap_host_port=${PULSE_DNSTAP_PORT:-6000}
web_host_port=${PULSE_WEB_PORT:-443}

if pulse_port_uses_only_host_port "$collector_bindings" 6000/tcp "$dnstap_host_port"; then
  row "DNStap" "LISTENING $(pulse_port_binding_summary "$collector_bindings" 6000/tcp)"
else
  fail "DNStap" "expected host port $dnstap_host_port"
fi
if pulse_port_uses_only_host_port "$web_bindings" 8443/tcp "$web_host_port"; then
  row "Web endpoint" "LISTENING $(pulse_port_binding_summary "$web_bindings" 8443/tcp)"
else
  fail "Web endpoint" "expected host port $web_host_port"
fi

exposure_fail=0
exposure_details=()
for target in \
  'clickhouse|8123/tcp' \
  'clickhouse|9000/tcp' \
  'clickhouse|9009/tcp' \
  'pulse-api|8081/tcp' \
  'pulse-api|9092/udp' \
  'pulse-collector|9093/tcp'; do
  IFS='|' read -r service port <<<"$target"
  case $service in
    clickhouse) bindings=$clickhouse_bindings ;;
    pulse-api) bindings=$api_bindings ;;
    pulse-collector) bindings=$collector_bindings ;;
  esac
  if pulse_port_is_published "$bindings" "$port"; then
    exposure_fail=1
    exposure_details+=("$service $port=>$(pulse_port_binding_summary "$bindings" "$port")")
  fi
done
if (( exposure_fail == 0 )); then
  pass "Internal exposure"
else
  fail "Internal exposure" "unexpected host binding: ${exposure_details[*]}"
fi

usage=$(df -P "$ROOT" | awk 'NR==2 {print $5}')
row "Host disk usage" "$usage"
docker system df --format 'Docker images={{.Size}} reclaimable={{.Reclaimable}}' 2>/dev/null | head -n 1 || true

printf '\n'
if (( failures == 0 )); then
  echo "Pulse Doctor: all checks passed"
else
  echo "Pulse Doctor: $failures check(s) failed" >&2
  exit 1
fi
