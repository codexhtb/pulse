# Investigation API contracts

All endpoints are under `/api/v1`, use the configured tenant, and accept the
existing `range` values (`15m`, `1h`, `3h`, `6h`, `12h`, `24h`). Client and
domain summary/history requests read bounded aggregate tables. Raw transaction
search is limited to the 24-hour retention window and remains cursor-only.

## Entity inspectors

- `GET /clients/{ip}` returns query/response/outcome counters, percentages,
  unique domains, response bytes, average and p50/p95/p99 latency, last-seen
  state, and sources.
- `GET /clients/{ip}/traffic` returns the existing bounded timeline contract.
- `GET /clients/{ip}/domains?limit=N` returns top domain behavior from
  `client_domain_hour`.
- `GET /domains/{domain}` mirrors the client summary with unique clients and
  QTYPE distribution.
- `GET /domains/{domain}/traffic` returns the domain timeline.
- `GET /domains/{domain}/clients?limit=N` returns top clients from
  `client_domain_hour`.
- `GET /sources/{source}` combines process-local connection/counter state with
  source-aware historical aggregates.

Entity state is `active` when its last observation is within 60 seconds and
`silent` otherwise. Source state additionally distinguishes `active`, `silent`,
`disconnected`, `connected_no_events`, and `never_seen_since_start`, with
independent `expected` and `unexpected` flags.

## Search and live filters

`GET /search` and `GET /live` accept the existing source, client, domain, QTYPE,
RCODE, and protocol filters plus:

- `outcome=RESPONSE|NO_RESPONSE|UNMATCHED_RESPONSE`;
- `failures=true`;
- `slow_us=N` (matched responses at or above the threshold).

`failures=true` means `NO_RESPONSE`, `SERVFAIL`, `REFUSED`, or `FORMERR`.
`NXDOMAIN` is intentionally not treated as a failure. Search pagination is the
existing opaque `next_cursor`; OFFSET pagination is not supported.

Live delivery remains best-effort SSE. Filters are applied server-side, the
subscriber queue is bounded, and the browser batches UI updates every 200 ms
into a 1,000-event ring.

Host filesystem capacity is always collected with local `statfs`. Optional
`system.parts` and `system.merges` queries are disabled unless
`PULSE_CLICKHOUSE_SYSTEM_METRICS=true`; production leaves this false while the
API user has intentionally narrow grants, avoiding repeated access-denied
queries and their error-log noise.

## Aggregate cost model

Migration 003 adds two per-minute TDigest latency states and one hourly
client/domain relation. Rows per day are bounded by distinct
source-client-minute, source-domain-minute, and source-client-domain-hour
tuples, respectively. The absolute transaction-rate ceiling for any one of
these is 259.2M / 432M / 864M rows per day at 3k / 5k / 10k QPS, but the design
depends on normal DNS repetition and merge aggregation; the relation table is
the highest-cardinality risk.

For capacity planning, if distinct client/domain/hour tuples are 1% of queries,
`client_domain_hour` receives about 2.59M / 4.32M / 8.64M pre-merge rows per day.
At an estimated compressed 40–100 bytes per resulting row, this is roughly
0.10–0.26 / 0.17–0.43 / 0.35–0.86 GB per day before replicas. Actual deployment
cardinality and compression must be measured with a representative load test.
The 24-hour TTL bounds DNS event and aggregate retention; no investigation
query performs an unbounded raw scan or uses OFFSET.
