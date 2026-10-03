#!/usr/bin/env bash
set -Eeuo pipefail

PULSE_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
COMPOSE=(docker compose --project-directory "$PULSE_ROOT" --env-file "$PULSE_ROOT/.env" -f "$PULSE_ROOT/compose.yaml")

die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
info() { printf '==> %s\n' "$*"; }

require_env() {
  [[ -f "$PULSE_ROOT/.env" ]] || die "$PULSE_ROOT/.env does not exist; run sudo ./install.sh first"
  set -a
  # shellcheck disable=SC1091
  source "$PULSE_ROOT/.env"
  set +a
}

require_docker() {
  command -v docker >/dev/null 2>&1 || die "Docker Engine is not installed"
  docker info >/dev/null 2>&1 || die "Docker daemon is unavailable"
  docker compose version >/dev/null 2>&1 || die "Docker Compose v2 is unavailable"
}

compose() { "${COMPOSE[@]}" "$@"; }

container_id() { compose ps -q "$1"; }

wait_healthy() {
  local service=$1 timeout=${2:-180} id state deadline
  deadline=$((SECONDS + timeout))
  while (( SECONDS < deadline )); do
    id=$(container_id "$service")
    if [[ -n "$id" ]]; then
      state=$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$id" 2>/dev/null || true)
      [[ "$state" == healthy ]] && return 0
      [[ "$state" == exited || "$state" == dead ]] && break
    fi
    sleep 2
  done
  compose ps "$service" >&2 || true
  compose logs --tail=80 "$service" >&2 || true
  die "$service did not become healthy within ${timeout}s"
}

project_name() {
  printf '%s' "${COMPOSE_PROJECT_NAME:-pulse}"
}

volume_name() {
  printf '%s_%s' "$(project_name)" "$1"
}
