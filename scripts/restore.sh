#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
# shellcheck source=scripts/lib.sh
source "$ROOT/scripts/lib.sh"
require_env
require_docker

archive=
assume_yes=false
restore_auth=false
while (($#)); do
  case $1 in
    --backup) shift; archive=${1:?--backup requires an archive} ;;
    --yes) assume_yes=true ;;
    --restore-auth-state) restore_auth=true ;;
    *) die "unknown restore option: $1" ;;
  esac
  shift
done
[[ -n "$archive" && -f "$archive" ]] || die "use --backup /path/to/pulse-YYYYmmddTHHMMSSZ.tar.gz"

if tar -tzf "$archive" | grep -Eq '(^/|(^|/)\.\.(/|$))'; then
  die "backup archive contains unsafe paths"
fi
work=$(mktemp -d /tmp/pulse-restore.XXXXXX)
cleanup() { rm -rf -- "$work"; }
trap cleanup EXIT
tar -C "$work" -xzf "$archive"
manifest="$work/manifest.json"
[[ -f "$manifest" ]] || die "backup manifest is missing"

format=$(sed -n 's/.*"format": *\([0-9][0-9]*\).*/\1/p' "$manifest")
backup_name=$(sed -n 's/.*"clickhouse_backup": *"\([^"]*\)".*/\1/p' "$manifest")
backup_ch_version=$(sed -n 's/.*"clickhouse_version": *"\([^"]*\)".*/\1/p' "$manifest")
[[ "$format" == 1 ]] || die "unsupported backup format: ${format:-missing}"
[[ "$backup_name" =~ ^pulse-[0-9]{8}T[0-9]{6}Z$ ]] || die "invalid ClickHouse backup name"
[[ -d "$work/clickhouse/$backup_name" ]] || die "ClickHouse backup payload is missing"
[[ -f "$work/state/collector-state.tar.gz" ]] || die "collector state payload is missing"

compose up -d clickhouse
wait_healthy clickhouse 240
current_ch_version=$(compose exec -T clickhouse clickhouse-client \
  --user "$CLICKHOUSE_ADMIN_USER" --password "$CLICKHOUSE_ADMIN_PASSWORD" --query 'SELECT version()')
[[ ${backup_ch_version%.*.*} == ${current_ch_version%.*.*} ]] || die "ClickHouse compatibility check failed: backup=$backup_ch_version current=$current_ch_version"

cat >&2 <<EOF
WARNING: this will replace the Pulse database and Docker persistent states.
It only targets Compose project $(project_name); native host services are untouched.
EOF
if ! $assume_yes; then
  [[ -t 0 ]] || die "interactive confirmation required (or use --yes)"
  read -r -p 'Type RESTORE to continue: ' confirmation
  [[ "$confirmation" == RESTORE ]] || die "restore cancelled"
fi

info "stopping Docker Pulse application services"
compose stop pulse-web pulse-api pulse-collector >/dev/null || true

target="/var/lib/clickhouse/pulse-backups/$backup_name"
if compose exec -T clickhouse test -e "$target"; then
  info "using existing ClickHouse backup payload $backup_name"
else
  compose cp "$work/clickhouse/$backup_name" "clickhouse:/var/lib/clickhouse/pulse-backups/" >/dev/null
  compose exec -T -u root clickhouse chown -R clickhouse:clickhouse "$target"
fi

info "restoring ClickHouse database"
compose exec -T clickhouse clickhouse-client \
  --user "$CLICKHOUSE_ADMIN_USER" --password "$CLICKHOUSE_ADMIN_PASSWORD" \
  --multiquery --query "DROP DATABASE IF EXISTS pulse SYNC; RESTORE DATABASE pulse FROM Disk('pulse_backups', '$backup_name');"

alpine=${ALPINE_IMAGE:-alpine:3.21.3}
docker run --rm \
  -v "$(volume_name collector-state):/target" -v "$work/state:/backup:ro" \
  "$alpine" sh -ec 'find /target -mindepth 1 -delete; tar -C /target -xzf /backup/collector-state.tar.gz; chown -R 10001:10001 /target'

if $restore_auth; then
  [[ -f "$work/state/api-state.tar.gz" ]] || die "backup does not contain API auth state"
  printf '%s\n' "WARNING: restoring Admin password hashes and revoking current sessions." >&2
  docker run --rm \
    -v "$(volume_name api-state):/target" -v "$work/state:/backup:ro" \
    "$alpine" sh -ec 'find /target -mindepth 1 -delete; tar -C /target -xzf /backup/api-state.tar.gz; chown -R 10001:10001 /target'
fi

compose run --rm state-init
compose run --rm pulse-migrate
compose up -d
wait_healthy clickhouse 180
wait_healthy pulse-collector 180
wait_healthy pulse-api 180
wait_healthy pulse-web 180
echo "Restore completed successfully"
