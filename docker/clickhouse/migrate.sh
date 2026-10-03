#!/usr/bin/env bash
set -Eeuo pipefail

required=(
  CLICKHOUSE_ADMIN_USER CLICKHOUSE_ADMIN_PASSWORD
  CLICKHOUSE_API_USER CLICKHOUSE_API_PASSWORD
  CLICKHOUSE_INGEST_USER CLICKHOUSE_INGEST_PASSWORD
)
for name in "${required[@]}"; do
  [[ -n "${!name:-}" ]] || { echo "$name is required" >&2; exit 1; }
done
for name in CLICKHOUSE_ADMIN_PASSWORD CLICKHOUSE_API_PASSWORD CLICKHOUSE_INGEST_PASSWORD; do
  [[ "${!name}" =~ ^[A-Za-z0-9_-]{32,}$ ]] || { echo "$name has an unsafe or placeholder value" >&2; exit 1; }
done
for name in CLICKHOUSE_ADMIN_USER CLICKHOUSE_API_USER CLICKHOUSE_INGEST_USER; do
  [[ "${!name}" =~ ^[a-z_][a-z0-9_]*$ ]] || { echo "$name is not a safe ClickHouse identifier" >&2; exit 1; }
done

client=(clickhouse-client
  --host "${CLICKHOUSE_HOST:-clickhouse}"
  --port "${CLICKHOUSE_PORT:-9000}"
  --user "$CLICKHOUSE_ADMIN_USER"
  --password "$CLICKHOUSE_ADMIN_PASSWORD")

"${client[@]}" --multiquery --query "
CREATE USER IF NOT EXISTS ${CLICKHOUSE_API_USER} IDENTIFIED WITH sha256_password BY '${CLICKHOUSE_API_PASSWORD}';
ALTER USER ${CLICKHOUSE_API_USER} IDENTIFIED WITH sha256_password BY '${CLICKHOUSE_API_PASSWORD}';
CREATE USER IF NOT EXISTS ${CLICKHOUSE_INGEST_USER} IDENTIFIED WITH sha256_password BY '${CLICKHOUSE_INGEST_PASSWORD}';
ALTER USER ${CLICKHOUSE_INGEST_USER} IDENTIFIED WITH sha256_password BY '${CLICKHOUSE_INGEST_PASSWORD}';
"

schema_exists=$("${client[@]}" --query "EXISTS TABLE pulse.dns_events")
if [[ "$schema_exists" == "0" ]]; then
  echo "initializing current Pulse schema"
  "${client[@]}" --multiquery < /pulse/src/db/schema-current.sql
  "${client[@]}" --multiquery --query "
    INSERT INTO pulse.schema_migrations (version, name) VALUES
      (2, 'transaction outcomes and pipeline observability'),
      (3, 'investigation aggregates'),
      (4, '24-hour DNS telemetry retention'),
      (5, '24-hour client and domain last-seen retention');
  "
else
  migrations_exists=$("${client[@]}" --query "EXISTS TABLE pulse.schema_migrations")
  [[ "$migrations_exists" == "1" ]] || { echo "existing pulse schema has no migration history" >&2; exit 1; }
  export CLICKHOUSE_USER="$CLICKHOUSE_ADMIN_USER" CLICKHOUSE_PASSWORD="$CLICKHOUSE_ADMIN_PASSWORD"
  export CLICKHOUSE_HOST="${CLICKHOUSE_HOST:-clickhouse}" CLICKHOUSE_PORT="${CLICKHOUSE_PORT:-9000}" CLICKHOUSE_DATABASE=pulse
  for migration in /pulse/src/db/migrations/002_transaction_outcomes.sh \
                   /pulse/src/db/migrations/003_investigation_aggregates.sh \
                   /pulse/src/db/migrations/004_dns_retention_24h.sh \
                   /pulse/src/db/migrations/005_entity_last_seen_retention_24h.sh; do
    "$migration"
  done
fi

"${client[@]}" --multiquery --query "
REVOKE ALL ON *.* FROM ${CLICKHOUSE_API_USER};
REVOKE ALL ON *.* FROM ${CLICKHOUSE_INGEST_USER};
GRANT SELECT ON pulse.* TO ${CLICKHOUSE_API_USER};
GRANT INSERT ON pulse.dns_events TO ${CLICKHOUSE_INGEST_USER};
"

"${client[@]}" --query "SELECT throwIf((SELECT count() FROM system.tables WHERE database='pulse' AND name='dns_events') != 1, 'Pulse schema verification failed')"
echo "Pulse schema and ClickHouse users are ready"
