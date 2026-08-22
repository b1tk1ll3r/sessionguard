# Security Model

## Trust boundaries

SessionGuard has five distinct credential classes:

1. OIDC/PocketID browser administrator authentication.
2. Agent enrollment token used only to bootstrap a new agent.
3. Per-agent bearer tokens for heartbeats.
4. Guacamole broker API key.
5. PostgreSQL credentials.

Use independent high-entropy values and rotate them independently.

## Guacamole header authentication

If Guacamole trusts `X-Guacamole-User` or another header, the reverse proxy/header-auth component is part of the authentication boundary. An attacker who can directly reach Guacamole and set the trusted header may bypass the upstream authentication flow.

Therefore:

- do not publish Guacamole's backend port directly;
- strip user-supplied identity headers at the trusted edge;
- prefer a dedicated network between Traefik/forward-auth and Guacamole;
- do not attach unrelated/untrusted containers to a network that can directly reach Guacamole's trusted backend listener.

## Master split-brain protection

v0.3 is a single-active-master design. PostgreSQL mode acquires a dedicated advisory lock and verifies it on control-plane writes. This prevents an accidentally duplicated Master from independently assigning leases/policies against the same database. It does not provide automatic HA; operational failover is still explicit.

## Broker fail-closed behavior

The broker does not fall back from a missing/disabled farm to an arbitrary server. `maintenance` blocks reconnect and new placement; `drain` blocks new placement. This prevents availability logic from silently violating profile/session isolation.

## Profile deletion

SessionGuard does not recursively remove arbitrary configured paths. Cleanup resolves the Windows profile path for the SID, verifies it is beneath an allowed root, verifies exclusions, performs a fresh session recheck and calls Windows `DeleteProfileW`.

When profile backup is enabled, a failed backup blocks cleanup.

## Profile synchronization

Only explicitly configured relative folders are copied. Configuration rejects paths that are absolute or escape the profile root. The copy engine does not intentionally follow symlinks/reparse-like entries into unrelated filesystem locations.

Do not include:

- `NTUSER.DAT`
- `UsrClass.dat`
- registry hives
- the entire profile root
- EFS/private-key material without a separately reviewed design

## Reconnect races

Before activating a completed backup as `current`, the agent rechecks whether the SID became active. If so, activation is abandoned. Before cleanup the agent also performs a fresh WTS recheck.

These checks reduce profile corruption/data-loss risk when a user reconnects during backup or cleanup grace periods.

## Command execution

Master commands are short-lived and authenticated through the per-agent heartbeat. Agent command IDs are deduplicated. Results are returned and audited.

High-impact actions such as process termination, forced logoff and maintenance changes are protected by RBAC on the Master. Process termination is additionally bound to the observed RDS session and revalidated on the agent immediately before termination to reduce PID-reuse risk.

## Local agent UI

The local UI is an intentional break-glass/control-plane fallback. Protect it with OIDC and network ACLs. Do not expose it broadly to the Internet merely because it supports PocketID.

## Container hardening

The supplied Master compose demonstrates:

- read-only root filesystem
- `no-new-privileges`
- dropped Linux capabilities
- private backend network
- a small tmpfs

Review the Guacamole stack separately. Avoid forcing `user: 0:0` on long-running containers unless the specific image/runtime actually requires it.

## Database data sensitivity

PostgreSQL includes usernames, SIDs, client metadata, session history, audit actors, policies and operational state. Treat DB backups accordingly. SessionGuard does not store user passwords.

## Webhook security

Alert webhooks may contain hostnames and operational details. Send them only to trusted HTTPS destinations. v0.3 uses a generic webhook and does not currently implement request signing; if the receiver requires authenticated provenance, place a trusted relay in front of it or add a signing layer before exposing it externally.

## Limitations

- The RDS agent runs with high Windows privileges to enumerate/control sessions and delete profiles; compromise of the agent binary/service account is high impact.
- The current WTS extended-session parsing is targeted at supported x64 Windows Server deployment. Validate on your exact Windows Server versions.
- SessionGuard is not an EDR, PAM product, or complete Windows profile provider.
