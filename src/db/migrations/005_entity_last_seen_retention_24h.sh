#!/usr/bin/env bash
set -euo pipefail

: "${CLICKHOUSE_USER:?CLICKHOUSE_USER is required}"
: "${CLICKHOUSE_PASSWORD:?CLICKHOUSE_PASSWORD is required}"

host="${CLICKHOUSE_HOST:-127.0.0.1}"
port="${CLICKHOUSE_PORT:-9000}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

client=(clickhouse-client --host "$host" --port "$port" --user "$CLICKHOUSE_USER" --password "$CLICKHOUSE_PASSWORD")

if [[ "$("${client[@]}" --query "SELECT count() FROM pulse.schema_migrations WHERE version=5")" != "0" ]]; then
  echo "migration 005 already applied"
  exit 0
fi

"${client[@]}" --multiquery <"$script_dir/005_entity_last_seen_retention_24h.sql"

actual_tables="$("${client[@]}" --query "
SELECT count()
FROM system.tables
WHERE database = 'pulse'
  AND name IN ('client_last_seen', 'domain_last_seen')
  AND position(create_table_query, 'finalizeAggregation(last_seen_state) + toIntervalHour(24)') > 0
")"

if [[ "$actual_tables" != "2" ]]; then
  echo "migration 005 TTL verification failed: expected 2 tables, found $actual_tables" >&2
  exit 1
fi

echo "migration 005 applied"
