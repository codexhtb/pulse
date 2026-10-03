CREATE DATABASE IF NOT EXISTS pulse;

CREATE TABLE IF NOT EXISTS pulse.schema_migrations
(
    version UInt32, name String, applied_at DateTime64(6, 'UTC') DEFAULT now64(6)
)
ENGINE = MergeTree ORDER BY version;

CREATE TABLE pulse.dns_events
(
    event_time DateTime64(6, 'UTC') CODEC(Delta(8), ZSTD(1)),
    ingested_at DateTime64(6, 'UTC') DEFAULT now64(6) CODEC(Delta(8), ZSTD(1)),
    query_time Nullable(DateTime64(6, 'UTC')) CODEC(Delta(8), ZSTD(1)),
    response_time Nullable(DateTime64(6, 'UTC')) CODEC(Delta(8), ZSTD(1)),
    outcome LowCardinality(String),
    tenant_id LowCardinality(String) DEFAULT 'default', source_id LowCardinality(String),
    client_ip IPv6, client_port UInt16, protocol LowCardinality(String),
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
TTL event_time + toIntervalHour(24)
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

CREATE TABLE pulse.traffic_minute
(
    minute DateTime('UTC'), tenant_id LowCardinality(String), source_id LowCardinality(String),
    total_queries UInt64, responses UInt64, no_response UInt64, unmatched_responses UInt64,
    nxdomain UInt64, servfail UInt64, response_bytes UInt64, latency_us_sum UInt64, latency_samples UInt64
)
ENGINE = SummingMergeTree PARTITION BY toYYYYMM(minute) ORDER BY (tenant_id, source_id, minute)
TTL minute + toIntervalHour(24) SETTINGS index_granularity = 8192;

CREATE TABLE pulse.client_minute
(
    minute DateTime('UTC'), tenant_id LowCardinality(String), source_id LowCardinality(String), client_ip IPv6,
    total_queries UInt64, responses UInt64, no_response UInt64, unmatched_responses UInt64,
    nxdomain UInt64, servfail UInt64, response_bytes UInt64, latency_us_sum UInt64, latency_samples UInt64
)
ENGINE = SummingMergeTree PARTITION BY toYYYYMM(minute) ORDER BY (tenant_id, minute, client_ip, source_id)
TTL minute + toIntervalHour(24) SETTINGS index_granularity = 8192;

CREATE TABLE pulse.domain_minute
(
    minute DateTime('UTC'), tenant_id LowCardinality(String), source_id LowCardinality(String),
    qname String, qtype LowCardinality(String),
    total_queries UInt64, responses UInt64, no_response UInt64, unmatched_responses UInt64,
    nxdomain UInt64, servfail UInt64, response_bytes UInt64, latency_us_sum UInt64, latency_samples UInt64,
    INDEX idx_domain qname TYPE bloom_filter(0.01) GRANULARITY 4
)
ENGINE = SummingMergeTree PARTITION BY toYYYYMM(minute) ORDER BY (tenant_id, minute, qname, source_id, qtype)
TTL minute + toIntervalHour(24) SETTINGS index_granularity = 8192;

CREATE TABLE pulse.rcode_minute
(
    minute DateTime('UTC'), tenant_id LowCardinality(String), source_id LowCardinality(String),
    rcode LowCardinality(String), responses UInt64
)
ENGINE = SummingMergeTree PARTITION BY toYYYYMM(minute) ORDER BY (tenant_id, minute, source_id, rcode)
TTL minute + toIntervalHour(24) SETTINGS index_granularity = 8192;

CREATE TABLE pulse.latency_minute
(
    minute DateTime('UTC'), tenant_id LowCardinality(String), source_id LowCardinality(String),
    latency_state AggregateFunction(quantilesTDigest(0.5, 0.95, 0.99), UInt32)
)
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(minute) ORDER BY (tenant_id, source_id, minute)
TTL minute + toIntervalHour(24) SETTINGS index_granularity = 8192;

CREATE TABLE pulse.client_unique_minute
(
    minute DateTime('UTC'), tenant_id LowCardinality(String), source_id LowCardinality(String), client_ip IPv6,
    domains_state AggregateFunction(uniqCombined64, String)
)
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(minute) ORDER BY (tenant_id, minute, client_ip, source_id)
TTL minute + toIntervalHour(24) SETTINGS index_granularity = 8192;

CREATE TABLE pulse.domain_unique_minute
(
    minute DateTime('UTC'), tenant_id LowCardinality(String), source_id LowCardinality(String), qname String,
    clients_state AggregateFunction(uniqCombined64, IPv6)
)
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(minute) ORDER BY (tenant_id, minute, qname, source_id)
TTL minute + toIntervalHour(24) SETTINGS index_granularity = 8192;

CREATE TABLE pulse.client_last_seen
(
    tenant_id LowCardinality(String), source_id LowCardinality(String), client_ip IPv6,
    last_seen_state AggregateFunction(max, DateTime64(6, 'UTC'))
)
ENGINE = AggregatingMergeTree ORDER BY (tenant_id, client_ip, source_id)
TTL finalizeAggregation(last_seen_state) + toIntervalHour(24) SETTINGS index_granularity = 8192;

CREATE TABLE pulse.domain_last_seen
(
    tenant_id LowCardinality(String), source_id LowCardinality(String), qname String,
    last_seen_state AggregateFunction(max, DateTime64(6, 'UTC'))
)
ENGINE = AggregatingMergeTree ORDER BY (tenant_id, qname, source_id)
TTL finalizeAggregation(last_seen_state) + toIntervalHour(24) SETTINGS index_granularity = 8192;

CREATE TABLE pulse.source_last_seen
(
    tenant_id LowCardinality(String), source_id LowCardinality(String),
    last_seen_state AggregateFunction(max, DateTime64(6, 'UTC'))
)
ENGINE = AggregatingMergeTree ORDER BY (tenant_id, source_id) SETTINGS index_granularity = 8192;

CREATE TABLE pulse.client_latency_minute
(
    minute DateTime('UTC'), tenant_id LowCardinality(String), source_id LowCardinality(String), client_ip IPv6,
    latency_state AggregateFunction(quantilesTDigest(0.5, 0.95, 0.99), UInt32)
)
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(minute) ORDER BY (tenant_id, minute, client_ip, source_id)
TTL minute + toIntervalHour(24) SETTINGS index_granularity = 8192;

CREATE TABLE pulse.domain_latency_minute
(
    minute DateTime('UTC'), tenant_id LowCardinality(String), source_id LowCardinality(String), qname String,
    latency_state AggregateFunction(quantilesTDigest(0.5, 0.95, 0.99), UInt32),
    INDEX idx_domain qname TYPE bloom_filter(0.01) GRANULARITY 4
)
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(minute) ORDER BY (tenant_id, minute, qname, source_id)
TTL minute + toIntervalHour(24) SETTINGS index_granularity = 8192;

CREATE TABLE pulse.client_domain_hour
(
    hour DateTime('UTC'), tenant_id LowCardinality(String), source_id LowCardinality(String),
    client_ip IPv6, qname String,
    total_queries UInt64, responses UInt64, no_response UInt64,
    nxdomain UInt64, servfail UInt64, response_bytes UInt64,
    INDEX idx_domain qname TYPE bloom_filter(0.01) GRANULARITY 4
)
ENGINE = SummingMergeTree PARTITION BY toYYYYMM(hour) ORDER BY (tenant_id, hour, client_ip, qname, source_id)
TTL hour + toIntervalHour(24) SETTINGS index_granularity = 8192;

CREATE MATERIALIZED VIEW pulse.mv_traffic_minute TO pulse.traffic_minute AS
SELECT toStartOfMinute(event_time) minute, tenant_id, source_id,
    countIf(outcome IN ('RESPONSE', 'NO_RESPONSE')) total_queries, countIf(outcome = 'RESPONSE') responses,
    countIf(outcome = 'NO_RESPONSE') no_response, countIf(outcome = 'UNMATCHED_RESPONSE') unmatched_responses,
    countIf(outcome = 'RESPONSE' AND rcode = 'NXDOMAIN') nxdomain,
    countIf(outcome = 'RESPONSE' AND rcode = 'SERVFAIL') servfail,
    sumIf(toUInt64(response_bytes), outcome IN ('RESPONSE', 'UNMATCHED_RESPONSE')) response_bytes,
    sumIf(toUInt64(ifNull(latency_us, 0)), outcome = 'RESPONSE') latency_us_sum,
    countIf(outcome = 'RESPONSE' AND latency_us IS NOT NULL) latency_samples
FROM pulse.dns_events GROUP BY minute, tenant_id, source_id;

CREATE MATERIALIZED VIEW pulse.mv_client_minute TO pulse.client_minute AS
SELECT toStartOfMinute(event_time) minute, tenant_id, source_id, client_ip,
    countIf(outcome IN ('RESPONSE', 'NO_RESPONSE')) total_queries, countIf(outcome = 'RESPONSE') responses,
    countIf(outcome = 'NO_RESPONSE') no_response, countIf(outcome = 'UNMATCHED_RESPONSE') unmatched_responses,
    countIf(outcome = 'RESPONSE' AND rcode = 'NXDOMAIN') nxdomain,
    countIf(outcome = 'RESPONSE' AND rcode = 'SERVFAIL') servfail,
    sumIf(toUInt64(response_bytes), outcome IN ('RESPONSE', 'UNMATCHED_RESPONSE')) response_bytes,
    sumIf(toUInt64(ifNull(latency_us, 0)), outcome = 'RESPONSE') latency_us_sum,
    countIf(outcome = 'RESPONSE' AND latency_us IS NOT NULL) latency_samples
FROM pulse.dns_events GROUP BY minute, tenant_id, source_id, client_ip;

CREATE MATERIALIZED VIEW pulse.mv_domain_minute TO pulse.domain_minute AS
SELECT toStartOfMinute(event_time) minute, tenant_id, source_id, qname, qtype,
    countIf(outcome IN ('RESPONSE', 'NO_RESPONSE')) total_queries, countIf(outcome = 'RESPONSE') responses,
    countIf(outcome = 'NO_RESPONSE') no_response, countIf(outcome = 'UNMATCHED_RESPONSE') unmatched_responses,
    countIf(outcome = 'RESPONSE' AND rcode = 'NXDOMAIN') nxdomain,
    countIf(outcome = 'RESPONSE' AND rcode = 'SERVFAIL') servfail,
    sumIf(toUInt64(response_bytes), outcome IN ('RESPONSE', 'UNMATCHED_RESPONSE')) response_bytes,
    sumIf(toUInt64(ifNull(latency_us, 0)), outcome = 'RESPONSE') latency_us_sum,
    countIf(outcome = 'RESPONSE' AND latency_us IS NOT NULL) latency_samples
FROM pulse.dns_events GROUP BY minute, tenant_id, source_id, qname, qtype;

CREATE MATERIALIZED VIEW pulse.mv_rcode_minute TO pulse.rcode_minute AS
SELECT toStartOfMinute(event_time) minute, tenant_id, source_id, rcode, count() responses
FROM pulse.dns_events WHERE outcome = 'RESPONSE' GROUP BY minute, tenant_id, source_id, rcode;

CREATE MATERIALIZED VIEW pulse.mv_latency_minute TO pulse.latency_minute AS
SELECT toStartOfMinute(event_time) minute, tenant_id, source_id,
    quantilesTDigestState(0.5, 0.95, 0.99)(assumeNotNull(latency_us)) latency_state
FROM pulse.dns_events WHERE outcome = 'RESPONSE' AND latency_us IS NOT NULL GROUP BY minute, tenant_id, source_id;

CREATE MATERIALIZED VIEW pulse.mv_client_unique_minute TO pulse.client_unique_minute AS
SELECT toStartOfMinute(event_time) minute, tenant_id, source_id, client_ip, uniqCombined64State(qname) domains_state
FROM pulse.dns_events WHERE outcome IN ('RESPONSE', 'NO_RESPONSE') GROUP BY minute, tenant_id, source_id, client_ip;

CREATE MATERIALIZED VIEW pulse.mv_domain_unique_minute TO pulse.domain_unique_minute AS
SELECT toStartOfMinute(event_time) minute, tenant_id, source_id, qname, uniqCombined64State(client_ip) clients_state
FROM pulse.dns_events WHERE outcome IN ('RESPONSE', 'NO_RESPONSE') GROUP BY minute, tenant_id, source_id, qname;

CREATE MATERIALIZED VIEW pulse.mv_client_last_seen TO pulse.client_last_seen AS
SELECT tenant_id, source_id, client_ip, maxState(event_time) last_seen_state FROM pulse.dns_events
WHERE outcome IN ('RESPONSE', 'NO_RESPONSE') GROUP BY tenant_id, source_id, client_ip;

CREATE MATERIALIZED VIEW pulse.mv_domain_last_seen TO pulse.domain_last_seen AS
SELECT tenant_id, source_id, qname, maxState(event_time) last_seen_state FROM pulse.dns_events
WHERE outcome IN ('RESPONSE', 'NO_RESPONSE') GROUP BY tenant_id, source_id, qname;

CREATE MATERIALIZED VIEW pulse.mv_source_last_seen TO pulse.source_last_seen AS
SELECT tenant_id, source_id, maxState(event_time) last_seen_state FROM pulse.dns_events GROUP BY tenant_id, source_id;

CREATE MATERIALIZED VIEW pulse.mv_client_latency_minute TO pulse.client_latency_minute AS
SELECT toStartOfMinute(event_time) minute, tenant_id, source_id, client_ip,
    quantilesTDigestState(0.5, 0.95, 0.99)(assumeNotNull(latency_us)) latency_state
FROM pulse.dns_events WHERE outcome = 'RESPONSE' AND latency_us IS NOT NULL
GROUP BY minute, tenant_id, source_id, client_ip;

CREATE MATERIALIZED VIEW pulse.mv_domain_latency_minute TO pulse.domain_latency_minute AS
SELECT toStartOfMinute(event_time) minute, tenant_id, source_id, qname,
    quantilesTDigestState(0.5, 0.95, 0.99)(assumeNotNull(latency_us)) latency_state
FROM pulse.dns_events WHERE outcome = 'RESPONSE' AND latency_us IS NOT NULL
GROUP BY minute, tenant_id, source_id, qname;

CREATE MATERIALIZED VIEW pulse.mv_client_domain_hour TO pulse.client_domain_hour AS
SELECT toStartOfHour(event_time) hour, tenant_id, source_id, client_ip, qname,
    countIf(outcome IN ('RESPONSE', 'NO_RESPONSE')) total_queries,
    countIf(outcome = 'RESPONSE') responses, countIf(outcome = 'NO_RESPONSE') no_response,
    countIf(outcome = 'RESPONSE' AND rcode = 'NXDOMAIN') nxdomain,
    countIf(outcome = 'RESPONSE' AND rcode = 'SERVFAIL') servfail,
    sumIf(toUInt64(response_bytes), outcome = 'RESPONSE') response_bytes
FROM pulse.dns_events WHERE outcome IN ('RESPONSE', 'NO_RESPONSE')
GROUP BY hour, tenant_id, source_id, client_ip, qname;
