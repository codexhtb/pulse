#!/usr/bin/env bash
set -euo pipefail

: "${CLICKHOUSE_USER:?CLICKHOUSE_USER is required}"
: "${CLICKHOUSE_PASSWORD:?CLICKHOUSE_PASSWORD is required}"

host="${CLICKHOUSE_HOST:-127.0.0.1}"
port="${CLICKHOUSE_PORT:-9000}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

client=(clickhouse-client --host "$host" --port "$port" --user "$CLICKHOUSE_USER" --password "$CLICKHOUSE_PASSWORD")

if [[ "$("${client[@]}" --query "SELECT count() FROM pulse.schema_migrations WHERE version=3")" != "0" ]]; then
  echo "migration 003 already applied"
  exit 0
fi

# Run this migration with pulse-collector stopped. Materialized views become
# active before the historical backfill, so concurrent ingestion would count
# rows once through the view and once through the backfill.
if systemctl is-active --quiet pulse-collector; then
  echo "pulse-collector must be stopped while migration 003 backfills data" >&2
  exit 1
fi

cleanup_phase3() {
  "${client[@]}" --multiquery <<'SQL'
DROP VIEW IF EXISTS pulse.mv_client_latency_minute;
DROP VIEW IF EXISTS pulse.mv_domain_latency_minute;
DROP VIEW IF EXISTS pulse.mv_client_domain_hour;
DROP TABLE IF EXISTS pulse.client_latency_minute;
DROP TABLE IF EXISTS pulse.domain_latency_minute;
DROP TABLE IF EXISTS pulse.client_domain_hour;
SQL
}

# A failed ClickHouse multiquery is not transactional. Since these objects are
# new in phase 3, recreating them is the bounded, idempotent recovery path.
cleanup_phase3
trap cleanup_phase3 ERR
"${client[@]}" --multiquery <"$script_dir/003_investigation_aggregates.sql"
"${client[@]}" --query "SELECT throwIf((SELECT count() FROM pulse.client_domain_hour) = 0 AND (SELECT count() FROM pulse.dns_events) > 0, 'client_domain_hour backfill is empty')"
trap - ERR
echo "migration 003 applied"
