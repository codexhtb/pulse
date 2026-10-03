-- Pulse schema migration 003: bounded investigation aggregates.
-- Existing raw and phase-2 aggregate data is not modified.

CREATE TABLE IF NOT EXISTS pulse.client_latency_minute
(
    minute DateTime('UTC'), tenant_id LowCardinality(String), source_id LowCardinality(String), client_ip IPv6,
    latency_state AggregateFunction(quantilesTDigest(0.5, 0.95, 0.99), UInt32)
)
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(minute)
ORDER BY (tenant_id, minute, client_ip, source_id)
TTL minute + toIntervalDay(90) SETTINGS index_granularity = 8192;

CREATE TABLE IF NOT EXISTS pulse.domain_latency_minute
(
    minute DateTime('UTC'), tenant_id LowCardinality(String), source_id LowCardinality(String), qname String,
    latency_state AggregateFunction(quantilesTDigest(0.5, 0.95, 0.99), UInt32),
    INDEX idx_domain qname TYPE bloom_filter(0.01) GRANULARITY 4
)
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(minute)
ORDER BY (tenant_id, minute, qname, source_id)
TTL minute + toIntervalDay(90) SETTINGS index_granularity = 8192;

CREATE TABLE IF NOT EXISTS pulse.client_domain_hour
(
    hour DateTime('UTC'), tenant_id LowCardinality(String), source_id LowCardinality(String),
    client_ip IPv6, qname String,
    total_queries UInt64, responses UInt64, no_response UInt64,
    nxdomain UInt64, servfail UInt64, response_bytes UInt64,
    INDEX idx_domain qname TYPE bloom_filter(0.01) GRANULARITY 4
)
ENGINE = SummingMergeTree PARTITION BY toYYYYMM(hour)
ORDER BY (tenant_id, hour, client_ip, qname, source_id)
TTL hour + toIntervalDay(90) SETTINGS index_granularity = 8192;

CREATE MATERIALIZED VIEW IF NOT EXISTS pulse.mv_client_latency_minute TO pulse.client_latency_minute AS
SELECT toStartOfMinute(event_time) minute, tenant_id, source_id, client_ip,
    quantilesTDigestState(0.5, 0.95, 0.99)(assumeNotNull(latency_us)) latency_state
FROM pulse.dns_events WHERE outcome = 'RESPONSE' AND latency_us IS NOT NULL
GROUP BY minute, tenant_id, source_id, client_ip;

CREATE MATERIALIZED VIEW IF NOT EXISTS pulse.mv_domain_latency_minute TO pulse.domain_latency_minute AS
SELECT toStartOfMinute(event_time) minute, tenant_id, source_id, qname,
    quantilesTDigestState(0.5, 0.95, 0.99)(assumeNotNull(latency_us)) latency_state
FROM pulse.dns_events WHERE outcome = 'RESPONSE' AND latency_us IS NOT NULL
GROUP BY minute, tenant_id, source_id, qname;

CREATE MATERIALIZED VIEW IF NOT EXISTS pulse.mv_client_domain_hour TO pulse.client_domain_hour AS
SELECT toStartOfHour(event_time) hour, tenant_id, source_id, client_ip, qname,
    countIf(outcome IN ('RESPONSE', 'NO_RESPONSE')) total_queries,
    countIf(outcome = 'RESPONSE') responses,
    countIf(outcome = 'NO_RESPONSE') no_response,
    countIf(outcome = 'RESPONSE' AND rcode = 'NXDOMAIN') nxdomain,
    countIf(outcome = 'RESPONSE' AND rcode = 'SERVFAIL') servfail,
    sumIf(toUInt64(response_bytes), outcome = 'RESPONSE') response_bytes
FROM pulse.dns_events WHERE outcome IN ('RESPONSE', 'NO_RESPONSE')
GROUP BY hour, tenant_id, source_id, client_ip, qname;

INSERT INTO pulse.client_latency_minute
SELECT toStartOfMinute(event_time), tenant_id, source_id, client_ip,
    quantilesTDigestState(0.5, 0.95, 0.99)(assumeNotNull(latency_us))
FROM pulse.dns_events WHERE outcome = 'RESPONSE' AND latency_us IS NOT NULL
GROUP BY toStartOfMinute(event_time), tenant_id, source_id, client_ip;

INSERT INTO pulse.domain_latency_minute
SELECT toStartOfMinute(event_time), tenant_id, source_id, qname,
    quantilesTDigestState(0.5, 0.95, 0.99)(assumeNotNull(latency_us))
FROM pulse.dns_events WHERE outcome = 'RESPONSE' AND latency_us IS NOT NULL
GROUP BY toStartOfMinute(event_time), tenant_id, source_id, qname;

INSERT INTO pulse.client_domain_hour
SELECT toStartOfHour(event_time), tenant_id, source_id, client_ip, qname,
    countIf(outcome IN ('RESPONSE', 'NO_RESPONSE')),
    countIf(outcome = 'RESPONSE'), countIf(outcome = 'NO_RESPONSE'),
    countIf(outcome = 'RESPONSE' AND rcode = 'NXDOMAIN'),
    countIf(outcome = 'RESPONSE' AND rcode = 'SERVFAIL'),
    sumIf(toUInt64(response_bytes), outcome = 'RESPONSE')
FROM pulse.dns_events WHERE outcome IN ('RESPONSE', 'NO_RESPONSE')
GROUP BY toStartOfHour(event_time), tenant_id, source_id, client_ip, qname;

INSERT INTO pulse.schema_migrations (version, name)
SELECT 3, 'investigation aggregates'
WHERE NOT EXISTS (SELECT 1 FROM pulse.schema_migrations WHERE version = 3);
