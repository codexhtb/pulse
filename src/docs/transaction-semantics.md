# DNS transaction semantics

Pulse stores one of three transaction outcomes independently from DNS RCODE:

- `RESPONSE`: a `CLIENT_RESPONSE` was correlated with a pending `CLIENT_QUERY`.
- `NO_RESPONSE`: no client response was observed within the configured correlation window.
- `UNMATCHED_RESPONSE`: a response was observed without a pending query.

The invariants are:

```
total_queries = RESPONSE + NO_RESPONSE
responses = RESPONSE
no_response = NO_RESPONSE
unmatched_responses = UNMATCHED_RESPONSE
```

NXDOMAIN and SERVFAIL rates use `responses` as their denominator. The
NO_RESPONSE rate uses `total_queries`. Latency is recorded only for matched
`RESPONSE` transactions. UNMATCHED_RESPONSE is visible but does not contribute
to query volume, response rates, or latency.

`query_time` and `response_time` are nullable canonical timestamps. For query
outcomes, `event_time` equals `query_time`; for UNMATCHED_RESPONSE it equals
`response_time`. This preserves the legacy sort/partition field without moving
NO_RESPONSE traffic to the later expiration time.

Expiration is append-only. If a response arrives after a query was emitted as
NO_RESPONSE, it is stored as UNMATCHED_RESPONSE. The original NO_RESPONSE is
not rewritten. A short terminal cache suppresses duplicate response frames.

The correlation window is configured with `PULSE_CORRELATION_TIMEOUT_MS`
(default `30000`) and is exposed by `/api/v1/system`.
