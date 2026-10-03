-- Pulse schema migration 005: expire client and domain last-seen states
-- 24 hours after their most recent DNS event. Source state is unchanged.

ALTER TABLE pulse.client_last_seen
MODIFY TTL finalizeAggregation(last_seen_state) + toIntervalHour(24);

ALTER TABLE pulse.domain_last_seen
MODIFY TTL finalizeAggregation(last_seen_state) + toIntervalHour(24);

-- Apply the new TTL to aggregate states that already exist.
ALTER TABLE pulse.client_last_seen MATERIALIZE TTL;
ALTER TABLE pulse.domain_last_seen MATERIALIZE TTL;

INSERT INTO pulse.schema_migrations (version, name)
SELECT 5, '24-hour client and domain last-seen retention'
WHERE NOT EXISTS (SELECT 1 FROM pulse.schema_migrations WHERE version = 5);
