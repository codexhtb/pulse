# Installing `pulse-agent` on a DNS node

The production installer creates the unprivileged agent account, installs prebuilt binaries, writes the node-local configuration, installs the narrow sudo policy, validates systemd and sudoers, starts the service, and verifies the authenticated health and nftables paths.

It does not modify the central Pulse topology or `control_enabled`. Enabling control remains a separate, explicit operation on the Pulse server.

## Build the bundle on Pulse/build host

The backend checkout and Go toolchain are needed only on the build host:

```bash
cd <PULSE_SOURCE_DIR>
./deploy/build-pulse-agent-bundle.sh
```

The helper runs the agent/helper Go tests and creates:

```text
<PULSE_SOURCE_DIR>/dist/pulse-agent-bundle.tar.gz
```

To build for another Linux architecture:

```bash
PULSE_AGENT_GOARCH=arm64 ./deploy/build-pulse-agent-bundle.sh
```

The archive contains one `pulse-agent-bundle/` directory with exactly:

```text
install-pulse-agent.sh
pulse-agent
pulse-nft-helper
pulse-agent.service
sudoers-pulse-agent
SHA256SUMS
```

The installer verifies the exact directory layout and every entry in `SHA256SUMS` before install, check, or uninstall. Never add a token or generated configuration to the archive.

## DNS-node prerequisites

- Linux with systemd, nftables, sudo, `ip`, `ss`, curl, and sha256sum installed.
- The extracted six-file production bundle. Git, a backend checkout, and the Go compiler are not required.
- The DNS management and service IPv4 addresses must already be assigned locally.
- The DNS node must have an IPv4 route to the Pulse server.
- A unique bearer token for this DNS node. Do not reuse a token from another agent.

All IP inputs are individual IPv4 addresses. CIDR prefixes and IPv6 are rejected. The installer automatically protects every local IPv4 address and every IPv4 route next-hop/gateway, in addition to the management, DNS service, Pulse, and operator-supplied protected addresses.

## Install or update

Copy the archive to the DNS node through the approved management path and compare its archive hash with the value printed by the build helper. Extract it as root so the executable payload cannot be replaced by an unprivileged local user, then run from the bundle directory:

```bash
sudo tar -xzf pulse-agent-bundle.tar.gz -C /root
cd /root/pulse-agent-bundle
sudo ./install-pulse-agent.sh
```

The installer prompts for:

1. DNS management IPv4;
2. DNS service IPv4;
3. Pulse server IPv4;
4. optional additional protected IPv4 addresses, separated by commas or spaces;
5. the node-specific bearer token, read from the terminal without echo and confirmed a second time.

On a repeated run, installed values are offered as defaults. Pressing Enter at the token prompt preserves the existing token. Existing `blocks.json`, audit history, and desired block state are preserved; they are reconciled after restart. A first installation must finish with an empty `blocked_dns_v4` set.

The resulting files are:

```text
/opt/pulse/bin/pulse-agent
/usr/local/libexec/pulse-nft-helper
/etc/pulse-agent/agent.env
/etc/pulse-agent/api-token
/etc/pulse-agent/helper.json
/etc/systemd/system/pulse-agent.service
/etc/sudoers.d/pulse-agent
/var/lib/pulse-agent/
```

The agent runs as `pulse-agent`, not root. It has zero effective capabilities. Only the root-owned helper changes `table inet pulse`, and sudo permits exactly:

```text
/usr/local/libexec/pulse-nft-helper reconcile
/usr/local/libexec/pulse-nft-helper status
```

The unit deliberately does not set `NoNewPrivileges=true`, because that kernel setting would suppress the setuid transition required by sudo. This does not grant a capability to the agent process: the installer verifies that its runtime effective capability mask is zero, while the existing sudoers file limits privilege acquisition to the two commands above.

### Example for a new DNS node

The delivery flow is:

```bash
# On the Pulse/build host
cd <PULSE_SOURCE_DIR>
./deploy/build-pulse-agent-bundle.sh
sha256sum dist/pulse-agent-bundle.tar.gz

# Copy dist/pulse-agent-bundle.tar.gz through the approved management path and
# compare its SHA-256 with the value printed above.
# On the DNS node, only after deployment approval:
sudo tar -xzf pulse-agent-bundle.tar.gz -C /root
cd /root/pulse-agent-bundle
sudo ./install-pulse-agent.sh
```

Example interactive answers using documentation networks:

```text
DNS management IPv4: 192.0.2.53
DNS service IPv4: 198.51.100.53
Pulse server IPv4: 192.0.2.10
Additional protected IPv4: <blank, unless more management/service IPs are required>
Bearer token: <contents of this node's unique token file, entered without echo>
```

Gateways and route next-hops are discovered from the actual routing table and
need not be entered manually. Verify the discovered set with `--check`.
Installing the agent does not enable the node in the central Pulse topology;
keep `control_enabled=false` until the deployment E2E and rollback procedure
has been approved.

## Check

Run a read-only verification at any time:

```bash
sudo ./install-pulse-agent.sh --check
```

The command prints `PASS` or `FAIL` for:

- system user and group;
- binary, directory, configuration, token, unit, and sudoers ownership/modes;
- individual-IP configuration and automatic protected addresses;
- `visudo` and `systemd-analyze verify`;
- enabled/active service state;
- non-root runtime UID and zero effective capabilities;
- exact management-IP bind on TCP/9094;
- authenticated `/health` response in enforcing mode;
- the narrow helper `status` command;
- the expected `table inet pulse`, `blocked_dns_v4`, and DNS-only TCP/UDP 53 rules.

`--check` does not alter service, firewall, configuration, token, or state. A non-empty set is valid after normal use and is not cleared by the check.

## Uninstall

Run:

```bash
sudo ./install-pulse-agent.sh --uninstall
```

Type `UNINSTALL` at the confirmation prompt. The command:

1. validates that the installed unit, sudoers, and account match the Pulse installer;
2. stops and disables only `pulse-agent.service`;
3. reconciles an empty block set when the helper is available;
4. deletes exactly `table inet pulse`;
5. removes only the Pulse agent binaries, configuration, state, unit, sudoers, user, and group.

It does not flush the nftables ruleset, and it does not modify UFW, iptables, Unbound, DNStap, the Pulse API, or central control topology. Uninstall removes the local agent state and audit log permanently; archive them securely first if retention is required.
