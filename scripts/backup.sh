#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
# shellcheck source=scripts/lib.sh
source "$ROOT/scripts/lib.sh"
require_env
require_docker

include_secrets=false
destination="$ROOT/backups"
while (($#)); do
  case $1 in
    --include-secrets) include_secrets=true ;;
    --output) shift; destination=${1:?--output requires a directory} ;;
    *) die "unknown backup option: $1" ;;
  esac
  shift
done

if $include_secrets; then
  printf '%s\n' "WARNING: this backup will contain .env and password hashes." >&2
  printf '%s\n' "Store and transport the resulting archive as a secret." >&2
fi

timestamp=$(date -u +%Y%m%dT%H%M%SZ)
backup_id="pulse-${timestamp}"
mkdir -p "$destination"
chmod 0700 "$destination"
work=$(mktemp -d "$destination/.${backup_id}.XXXXXX")
mkdir -p "$work/config" "$work/state" "$work/clickhouse"
cleanup() { rm -rf -- "$work"; }
trap cleanup EXIT

collector_was_running=false
api_was_running=false
[[ $(docker inspect --format '{{.State.Running}}' "$(container_id pulse-collector)" 2>/dev/null || true) == true ]] && collector_was_running=true
[[ $(docker inspect --format '{{.State.Running}}' "$(container_id pulse-api)" 2>/dev/null || true) == true ]] && api_was_running=true
resume_services() {
  $collector_was_running && compose up -d pulse-collector >/dev/null
  $api_was_running && compose up -d pulse-api >/dev/null
}
trap 'resume_services; cleanup' EXIT

info "stopping Docker collector/API for a consistent state snapshot"
compose stop pulse-collector pulse-api >/dev/null

info "creating an online ClickHouse BACKUP"
compose exec -T clickhouse clickhouse-client \
  --user "$CLICKHOUSE_ADMIN_USER" --password "$CLICKHOUSE_ADMIN_PASSWORD" \
  --query "BACKUP DATABASE pulse TO Disk('pulse_backups', '${backup_id}')"
compose cp "clickhouse:/var/lib/clickhouse/pulse-backups/${backup_id}" "$work/clickhouse/" >/dev/null

alpine=${ALPINE_IMAGE:-alpine:3.21.3}
docker run --rm --read-only \
  -v "$(volume_name collector-state):/source:ro" -v "$work/state:/backup" \
  "$alpine" tar -C /source -czf /backup/collector-state.tar.gz .

cp "$ROOT/compose.yaml" "$ROOT/.env.example" "$work/config/"
if $include_secrets; then
  cp "$ROOT/.env" "$work/config/.env"
  chmod 0600 "$work/config/.env"
  docker run --rm --read-only \
    -v "$(volume_name api-state):/source:ro" -v "$work/state:/backup" \
    "$alpine" tar -C /source -czf /backup/api-state.tar.gz .
fi

commit=$(git -C "$ROOT" rev-parse --verify HEAD 2>/dev/null || printf unknown)
clickhouse_version=$(compose exec -T clickhouse clickhouse-client \
  --user "$CLICKHOUSE_ADMIN_USER" --password "$CLICKHOUSE_ADMIN_PASSWORD" \
  --query 'SELECT version()')
cat > "$work/manifest.json" <<EOF
{
  "format": 1,
  "timestamp": "${timestamp}",
  "pulse_version": "${PULSE_VERSION:-dev}",
  "git_commit": "${commit}",
  "clickhouse_version": "${clickhouse_version}",
  "clickhouse_backup": "${backup_id}",
  "includes_secrets": ${include_secrets}
}
EOF

archive="$destination/${backup_id}.tar.gz"
tar -C "$work" -czf "$archive" .
chmod 0600 "$archive"
resume_services
collector_was_running=false
api_was_running=false
printf 'Backup created: %s\n' "$archive"
