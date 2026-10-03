-- Pulse schema migration 002: canonical transaction outcomes.
-- Run with the collector stopped. The migration runner creates a raw backup
-- before this file is applied and rebuilds every derived aggregate from raw.

CREATE TABLE IF NOT EXISTS pulse.schema_migrations
(
    version UInt32,
    name String,
    applied_at DateTime64(6, 'UTC') DEFAULT now64(6)
)
ENGINE = MergeTree ORDER BY version;

DROP TABLE IF EXISTS pulse.dns_events_v2;
CREATE TABLE pulse.dns_events_v2
(
    event_time DateTime64(6, 'UTC') CODEC(Delta(8), ZSTD(1)),
    ingested_at DateTime64(6, 'UTC') DEFAULT now64(6) CODEC(Delta(8), ZSTD(1)),
    query_time Nullable(DateTime64(6, 'UTC')) CODEC(Delta(8), ZSTD(1)),
    response_time Nullable(DateTime64(6, 'UTC')) CODEC(Delta(8), ZSTD(1)),
    outcome LowCardinality(String), tenant_id LowCardinality(String) DEFAULT 'default',
    source_id LowCardinality(String), client_ip IPv6, client_port UInt16, protocol LowCardinality(String),
    qname String CODEC(ZSTD(3)), qtype LowCardinality(String), qclass LowCardinality(String),
    rcode LowCardinality(String), dns_id UInt16, response_bytes UInt32, answer_count UInt16,
    latency_us Nullable(UInt32), matched_query UInt8 DEFAULT 1,
    INDEX idx_qname qname TYPE bloom_filter(0.01) GRANULARITY 4,
    INDEX idx_rcode rcode TYPE set(32) GRANULARITY 4,
    INDEX idx_outcome outcome TYPE set(8) GRANULARITY 4,
    INDEX idx_client_ip client_ip TYPE bloom_filter(0.01) GRANULARITY 4
)
ENGINE = MergeTree PARTITION BY toYYYYMMDD(event_time)
ORDER BY (tenant_id, toStartOfHour(event_time), source_id, client_ip, qname, event_time)
TTL event_time + toIntervalDay(7)
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

INSERT INTO pulse.dns_events_v2
SELECT
    if(matched_query = 1, event_time - toIntervalMicrosecond(ifNull(latency_us, 0)), event_time),
    ingested_at,
    if(matched_query = 1, toNullable(event_time - toIntervalMicrosecond(ifNull(latency_us, 0))), NULL),
    toNullable(event_time),
    if(matched_query = 1, 'RESPONSE', 'UNMATCHED_RESPONSE'),
    tenant_id, source_id, client_ip, client_port, protocol, qname, qtype, qclass, rcode,
    dns_id, response_bytes, answer_count, latency_us, matched_query
FROM pulse.dns_events;

SELECT throwIf(
    (SELECT count() FROM pulse.dns_events_v2) != (SELECT count() FROM pulse.dns_events),
    'raw row count mismatch during migration 002'
);

DROP VIEW IF EXISTS pulse.mv_traffic_minute;
DROP VIEW IF EXISTS pulse.mv_client_minute;
DROP VIEW IF EXISTS pulse.mv_domain_minute;
DROP VIEW IF EXISTS pulse.mv_rcode_minute;
DROP VIEW IF EXISTS pulse.mv_latency_minute;
DROP VIEW IF EXISTS pulse.mv_client_unique_minute;
DROP VIEW IF EXISTS pulse.mv_domain_unique_minute;
DROP VIEW IF EXISTS pulse.mv_client_last_seen;
DROP VIEW IF EXISTS pulse.mv_domain_last_seen;
DROP VIEW IF EXISTS pulse.mv_source_last_seen;

RENAME TABLE pulse.dns_events TO pulse.dns_events_pre002, pulse.dns_events_v2 TO pulse.dns_events;

DROP TABLE IF EXISTS pulse.traffic_minute;
DROP TABLE IF EXISTS pulse.client_minute;
DROP TABLE IF EXISTS pulse.domain_minute;
DROP TABLE IF EXISTS pulse.rcode_minute;
DROP TABLE IF EXISTS pulse.latency_minute;
DROP TABLE IF EXISTS pulse.client_unique_minute;
DROP TABLE IF EXISTS pulse.domain_unique_minute;
DROP TABLE IF EXISTS pulse.client_last_seen;
DROP TABLE IF EXISTS pulse.domain_last_seen;
DROP TABLE IF EXISTS pulse.source_last_seen;
