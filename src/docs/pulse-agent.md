# pulse-agent DNS access control

`pulse-agent` runs on the DNS server as the unprivileged `pulse-agent` user. It exposes an authenticated HTTP API to Pulse and persists desired block state in `/var/lib/pulse-agent`.

- `GET /health`
- `POST /block` with `{"ip":"192.0.2.10","ttl":"1h|24h|permanent","reason":"..."}`
- `POST /unblock` with `{"ip":"192.0.2.10"}`
- `GET /list`

Every endpoint requires a bearer token and an allowed caller source address. Each DNS node has a distinct token installed outside git; Pulse refuses to start if two configured nodes share a token file or token value.

Only individual IPv4 addresses are accepted. CIDR prefixes and native IPv6 are rejected; IPv4-mapped IPv6 addresses are normalized with `Unmap()`. Loopback, unspecified, multicast, link-local, local interface, DNS, Pulse, gateway, management, and configured service addresses cannot be blocked.

## Privilege boundary

The agent has no effective or ambient capabilities. It can run only these exact sudo commands:

```text
/usr/local/libexec/pulse-nft-helper reconcile
/usr/local/libexec/pulse-nft-helper status
```

The root-owned helper validates every address again and owns only `table inet pulse`. It atomically reconciles:

```text
set blocked_dns_v4 { type ipv4_addr; flags timeout; }
```

The two input rules drop only TCP/UDP destination port 53 for the configured local DNS destination IP. The helper never modifies UFW, iptables, or another nftables table.

## Persistence and audit

Desired state is atomically stored in `blocks.json`. An append-only JSONL audit records IP, action, reason, duration, timestamp, and result. Startup and periodic reconciliation remove expired records and rebuild `table inet pulse`, including remaining native nftables timeouts.

## Multi-node control plane

Pulse loads non-secret node topology from `/etc/pulse/control-nodes.json`. Each entry has an arbitrary `id`, display name, telemetry `source_identity`, agent URL, token-file path, DNS service IP, and `control_enabled`. Token contents live in separate root-managed files under `/etc/pulse/control-secrets` and are never stored in the topology file or repository.

The central desired state and every per-node application result are atomically stored in `/var/lib/pulse-api/dns-control.json`. A block or unblock is persisted before any agent call. Enabled nodes transition through `pending`, `applied`, or `error`; observe-only nodes are `disabled` and are never sent block/unblock requests. Failed nodes use bounded exponential retry and converge when their agent returns.

`source_identity` is unique and joins agent/control health to the existing DNStap source without changing Search or Live source fields. `pulse-agent -dry-run=true` provides the same authenticated, persistent contract without invoking the nftables helper and is intended only for safe tests.
