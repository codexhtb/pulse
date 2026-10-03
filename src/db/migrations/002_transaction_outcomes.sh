#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
clickhouse() {
    command clickhouse-client --host "${CLICKHOUSE_HOST:-127.0.0.1}" --port "${CLICKHOUSE_PORT:-9000}" \
        --database "${CLICKHOUSE_DATABASE:-pulse}" --user "${CLICKHOUSE_USER:?CLICKHOUSE_USER is required}" \
        --password "${CLICKHOUSE_PASSWORD:?CLICKHOUSE_PASSWORD is required}" "$@"
}

applied=$(clickhouse --query "SELECT count() FROM pulse.schema_migrations WHERE version = 2" 2>/dev/null || true)
if [ "${applied:-0}" != "0" ]; then
    echo "migration 002 already applied"
    exit 0
fi

clickhouse --multiquery < "$repo_root/db/migrations/002_transaction_outcomes.sql"

# Recreate all derived tables and materialized views from the canonical schema.
sed -n '/^CREATE TABLE pulse.traffic_minute/,$p' "$repo_root/db/schema-current.sql" | clickhouse --multiquery

# Materialized views only process future inserts. Rebuild existing derived data
# explicitly while ingestion is stopped.
clickhouse --multiquery <<'SQL'
INSERT INTO pulse.traffic_minute SELECT toStartOfMinute(event_time), tenant_id, source_id,
 countIf(outcome IN ('RESPONSE','NO_RESPONSE')), countIf(outcome='RESPONSE'), countIf(outcome='NO_RESPONSE'), countIf(outcome='UNMATCHED_RESPONSE'),
 countIf(outcome='RESPONSE' AND rcode='NXDOMAIN'), countIf(outcome='RESPONSE' AND rcode='SERVFAIL'),
 sumIf(toUInt64(response_bytes),outcome IN ('RESPONSE','UNMATCHED_RESPONSE')), sumIf(toUInt64(ifNull(latency_us,0)),outcome='RESPONSE'), countIf(outcome='RESPONSE' AND latency_us IS NOT NULL)
 FROM pulse.dns_events GROUP BY toStartOfMinute(event_time),tenant_id,source_id;

INSERT INTO pulse.client_minute SELECT toStartOfMinute(event_time), tenant_id, source_id, client_ip,
 countIf(outcome IN ('RESPONSE','NO_RESPONSE')), countIf(outcome='RESPONSE'), countIf(outcome='NO_RESPONSE'), countIf(outcome='UNMATCHED_RESPONSE'),
 countIf(outcome='RESPONSE' AND rcode='NXDOMAIN'), countIf(outcome='RESPONSE' AND rcode='SERVFAIL'),
 sumIf(toUInt64(response_bytes),outcome IN ('RESPONSE','UNMATCHED_RESPONSE')), sumIf(toUInt64(ifNull(latency_us,0)),outcome='RESPONSE'), countIf(outcome='RESPONSE' AND latency_us IS NOT NULL)
 FROM pulse.dns_events GROUP BY toStartOfMinute(event_time),tenant_id,source_id,client_ip;

INSERT INTO pulse.domain_minute SELECT toStartOfMinute(event_time), tenant_id, source_id, qname, qtype,
 countIf(outcome IN ('RESPONSE','NO_RESPONSE')), countIf(outcome='RESPONSE'), countIf(outcome='NO_RESPONSE'), countIf(outcome='UNMATCHED_RESPONSE'),
 countIf(outcome='RESPONSE' AND rcode='NXDOMAIN'), countIf(outcome='RESPONSE' AND rcode='SERVFAIL'),
 sumIf(toUInt64(response_bytes),outcome IN ('RESPONSE','UNMATCHED_RESPONSE')), sumIf(toUInt64(ifNull(latency_us,0)),outcome='RESPONSE'), countIf(outcome='RESPONSE' AND latency_us IS NOT NULL)
 FROM pulse.dns_events GROUP BY toStartOfMinute(event_time),tenant_id,source_id,qname,qtype;

INSERT INTO pulse.rcode_minute SELECT toStartOfMinute(event_time),tenant_id,source_id,rcode,count()
 FROM pulse.dns_events WHERE outcome='RESPONSE' GROUP BY toStartOfMinute(event_time),tenant_id,source_id,rcode;
INSERT INTO pulse.latency_minute SELECT toStartOfMinute(event_time),tenant_id,source_id,quantilesTDigestState(0.5,0.95,0.99)(assumeNotNull(latency_us))
 FROM pulse.dns_events WHERE outcome='RESPONSE' AND latency_us IS NOT NULL GROUP BY toStartOfMinute(event_time),tenant_id,source_id;
INSERT INTO pulse.client_unique_minute SELECT toStartOfMinute(event_time),tenant_id,source_id,client_ip,uniqCombined64State(qname)
 FROM pulse.dns_events WHERE outcome IN ('RESPONSE','NO_RESPONSE') GROUP BY toStartOfMinute(event_time),tenant_id,source_id,client_ip;
INSERT INTO pulse.domain_unique_minute SELECT toStartOfMinute(event_time),tenant_id,source_id,qname,uniqCombined64State(client_ip)
 FROM pulse.dns_events WHERE outcome IN ('RESPONSE','NO_RESPONSE') GROUP BY toStartOfMinute(event_time),tenant_id,source_id,qname;
INSERT INTO pulse.client_last_seen SELECT tenant_id,source_id,client_ip,maxState(event_time) FROM pulse.dns_events WHERE outcome IN ('RESPONSE','NO_RESPONSE') GROUP BY tenant_id,source_id,client_ip;
INSERT INTO pulse.domain_last_seen SELECT tenant_id,source_id,qname,maxState(event_time) FROM pulse.dns_events WHERE outcome IN ('RESPONSE','NO_RESPONSE') GROUP BY tenant_id,source_id,qname;
INSERT INTO pulse.source_last_seen SELECT tenant_id,source_id,maxState(event_time) FROM pulse.dns_events GROUP BY tenant_id,source_id;
INSERT INTO pulse.schema_migrations (version,name) VALUES (2,'transaction outcomes and pipeline observability');
DROP TABLE pulse.dns_events_pre002;
SQL

echo "migration 002 applied"
