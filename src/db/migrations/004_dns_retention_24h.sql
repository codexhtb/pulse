-- Pulse schema migration 004: retain raw DNS events and time-bucketed
-- DNS aggregates for exactly 24 hours.

ALTER TABLE pulse.dns_events MODIFY TTL event_time + toIntervalHour(24);
ALTER TABLE pulse.traffic_minute MODIFY TTL minute + toIntervalHour(24);
ALTER TABLE pulse.client_minute MODIFY TTL minute + toIntervalHour(24);
ALTER TABLE pulse.domain_minute MODIFY TTL minute + toIntervalHour(24);
ALTER TABLE pulse.rcode_minute MODIFY TTL minute + toIntervalHour(24);
ALTER TABLE pulse.latency_minute MODIFY TTL minute + toIntervalHour(24);
ALTER TABLE pulse.client_unique_minute MODIFY TTL minute + toIntervalHour(24);
ALTER TABLE pulse.domain_unique_minute MODIFY TTL minute + toIntervalHour(24);
ALTER TABLE pulse.client_latency_minute MODIFY TTL minute + toIntervalHour(24);
ALTER TABLE pulse.domain_latency_minute MODIFY TTL minute + toIntervalHour(24);
ALTER TABLE pulse.client_domain_hour MODIFY TTL hour + toIntervalHour(24);

-- Evaluate the shorter TTL against parts that already existed before this
-- migration. These mutations are safe to rerun if a previous run stopped
-- before the migration record was written.
ALTER TABLE pulse.dns_events MATERIALIZE TTL;
ALTER TABLE pulse.traffic_minute MATERIALIZE TTL;
ALTER TABLE pulse.client_minute MATERIALIZE TTL;
ALTER TABLE pulse.domain_minute MATERIALIZE TTL;
ALTER TABLE pulse.rcode_minute MATERIALIZE TTL;
ALTER TABLE pulse.latency_minute MATERIALIZE TTL;
ALTER TABLE pulse.client_unique_minute MATERIALIZE TTL;
ALTER TABLE pulse.domain_unique_minute MATERIALIZE TTL;
ALTER TABLE pulse.client_latency_minute MATERIALIZE TTL;
ALTER TABLE pulse.domain_latency_minute MATERIALIZE TTL;
ALTER TABLE pulse.client_domain_hour MATERIALIZE TTL;

INSERT INTO pulse.schema_migrations (version, name)
SELECT 4, '24-hour DNS telemetry retention'
WHERE NOT EXISTS (SELECT 1 FROM pulse.schema_migrations WHERE version = 4);
