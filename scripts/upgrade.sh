#!/usr/bin/env bash
set -Eeuo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
# shellcheck source=scripts/lib.sh
source "$ROOT/scripts/lib.sh"
require_env
require_docker

info "running pre-upgrade diagnostics"
"$ROOT/scripts/doctor.sh"
info "creating pre-upgrade backup"
"$ROOT/scripts/backup.sh"

info "validating updated Compose configuration"
compose config --quiet
info "pulling pinned infrastructure images"
compose pull clickhouse state-init pulse-migrate
info "building updated Pulse images"
compose build --pull pulse-api pulse-collector pulse-web

info "stopping ingest before migrations"
compose stop pulse-web pulse-api pulse-collector >/dev/null
compose up -d clickhouse
wait_healthy clickhouse 240
compose run --rm state-init
compose run --rm pulse-migrate

info "recreating Pulse Core"
compose up -d --force-recreate pulse-collector pulse-api pulse-web
wait_healthy pulse-collector 180
wait_healthy pulse-api 180
wait_healthy pulse-web 180
"$ROOT/scripts/doctor.sh"
echo "Upgrade completed successfully"
