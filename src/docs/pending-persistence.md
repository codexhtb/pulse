# Pending transaction persistence

Pulse collector keeps query/response correlation in memory for the hot path and
mirrors state to a small local write-ahead log under
`PULSE_PENDING_STATE_DIR`. Persistence is optional in code but required by the
production systemd unit.

## Write path and crash consistency

- A query appends one in-memory `PUT` record; a response or timeout appends one
  `DELETE` record with the terminal outcome. There is no per-event filesystem
  call and no per-event ClickHouse query.
- Records accumulated for `PULSE_PENDING_FLUSH_MS` (10 ms in production) are
  JSON encoded as one block, compressed with zstd, framed with lengths and a
  CRC32, and appended to the WAL.
- The WAL is `fsync`ed at most once per `PULSE_PENDING_SYNC_MS` (100 ms in
  production). A power loss can therefore lose at most the unsynced tail; that
  bounded exposure is reported by the dirty-recovery and recovery-error
  metrics rather than hidden.
- Every `PULSE_PENDING_CHECKPOINT_MS` (60 seconds), the WAL is synced and
  rotated, an atomic compressed checkpoint is written and synced, and WAL
  segments covered by that checkpoint are deleted. A crash before or after the
  atomic rename is recoverable because sequence numbers make replay
  idempotent.

## Recovery

Startup loads the last valid checkpoint, then replays rotated segments and the
active WAL in sequence order. Active pending entries are restored with their
original expiration times. Entries already overdue are emitted as
`NO_RESPONSE`; still-current entries continue waiting for a response. Recent
terminal records are also restored for five seconds so a retransmitted response
is not emitted twice.

A truncated or corrupt WAL tail is accepted only up to its last complete,
checksum-valid frame. The active tail is truncated to that boundary and the
event is exposed through `recovery_errors`, `truncated_tail_bytes`, and
`unclean_recovery`. A corrupt checkpoint is fatal because silently guessing the
base state would violate transaction semantics.

## Size and write amplification

With a 30-second correlation window, the upper bound on live pending entries is
approximately 90,000 / 150,000 / 300,000 at 3k / 5k / 10k QPS. The WAL receives
two logical records per completed transaction plus a full checkpoint once per
minute. Compression depends strongly on repeated client/domain/source values;
the planning envelope for checkpoint plus bounded WAL is approximately:

| Query rate | Live pending bound | Expected local state envelope |
| ---: | ---: | ---: |
| 3,000 QPS | 90,000 | 15–30 MiB |
| 5,000 QPS | 150,000 | 25–50 MiB |
| 10,000 QPS | 300,000 | 50–100 MiB |

These are capacity estimates, not load-test measurements. Phase 3 deliberately
does not perform the full production load test.

The collector status endpoint and `/api/v1/system` expose whether persistence is
enabled, WAL/checkpoint bytes, last sync and checkpoint, recovered entries,
recovery/write errors, truncated bytes, dirty recovery, and current in-memory
buffer lag.
