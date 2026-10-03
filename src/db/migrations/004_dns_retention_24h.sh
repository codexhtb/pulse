#!/usr/bin/env bash
set -euo pipefail

: "${CLICKHOUSE_USER:?CLICKHOUSE_USER is required}"
: "${CLICKHOUSE_PASSWORD:?CLICKHOUSE_PASSWORD is required}"

host="${CLICKHOUSE_HOST:-127.0.0.1}"
port="${CLICKHOUSE_PORT:-9000}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

client=(clickhouse-client --host "$host" --port "$port" --user "$CLICKHOUSE_USER" --password "$CLICKHOUSE_PASSWORD")

if [[ "$("${client[@]}" --query "SELECT count() FROM pulse.schema_migrations WHERE version=4")" != "0" ]]; then
  echo "migration 004 already applied"
  exit 0
fi

"${client[@]}" --multiquery <"$script_dir/004_dns_retention_24h.sql"

expected_tables=11
actual_tables="$("${client[@]}" --query "
SELECT count()
FROM system.tables
WHERE database = 'pulse'
  AND name IN (
    'dns_events', 'traffic_minute', 'client_minute', 'domain_minute',
    'rcode_minute', 'latency_minute', 'client_unique_minute',
    'domain_unique_minute', 'client_latency_minute',
    'domain_latency_minute', 'client_domain_hour'
  )
  AND position(create_table_query, 'toIntervalHour(24)') > 0
")"

if [[ "$actual_tables" != "$expected_tables" ]]; then
  echo "migration 004 TTL verification failed: expected $expected_tables tables, found $actual_tables" >&2
  exit 1
fi

echo "migration 004 applied"
