# SessionGuard 0.4 Architecture

## Purpose

SessionGuard separates the functions commonly bundled into a Citrix deployment into components that can be independently replaced:

- **PocketID/OIDC**: administrator identity and group claims.
- **Traefik / forward-auth**: trusted ingress for Guacamole.
- **Apache Guacamole**: browser gateway and RDP transport.
- **Windows RDS**: Windows session runtime.
- **SessionGuard Master**: broker, Director, policy control plane, history, alerting and command queue.
- **SessionGuard Agent**: Windows/RDS integration, profile lifecycle, templates, telemetry, local RemoteApp reconciliation and fallback administration.

SessionGuard intentionally does not implement a new remote-display protocol.

## Network model

Agents initiate all master communication:

```text
Agent --HTTPS heartbeat--> Master
      <-- policy + commands--
      -- results + telemetry-->
```

The master does not open SMB/RPC/WMI/WinRM management sessions to RDS hosts. This keeps the Windows hosts behind normal firewall/NAT boundaries and makes master outages less disruptive.

Guacamole reaches only the broker API using a dedicated API key:

```text
Guacamole extension --HTTP(S) + bearer key--> /api/v1/broker/tokens
```

That API key is separate from agent enrollment credentials and PocketID secrets.

## Master persistence

Production mode uses PostgreSQL.

### `sessionguard_state`

Single JSONB control-plane document containing relatively small mutable state:

- agents and last snapshots
- farms
- published resources
- leases
- policy versions/control state
- active alerts

### `sessionguard_audit`

Append-only administrative and command audit events. Indexed by time and actor.

### `sessionguard_session_history`

Append-only Director session history. Indexed by time, lower-cased username and agent ID.

History retention is bounded by `history_limit`. This avoids rewriting a permanently growing history document on every agent heartbeat.

JSON-file storage remains supported when `database_url` is empty. It is intended for development, migration and small single-node installations, not a HA master.

### Single-active-master guard

v0.3 is intentionally single-active-master. A dedicated PostgreSQL connection holds a session-level advisory lock for the lifetime of the Master, and control-plane writes verify that lock is still held. A second Master against the same database refuses to start. This prevents accidental split-brain; it is not a claim of seamless HA/failover.

## Agent state

Persistent agent state under `data_dir` contains:

- enrolled agent ID/token
- last accepted policy
- pending cleanup/profile jobs
- disconnected-session timers
- command deduplication/results
- profile status
- bounded event history
- bounded logon telemetry
- desired/managed RemoteApp state and last observed RemoteApp inventory

The service can therefore continue cleanup/profile/session policy during a master outage using the last accepted policy.

## Session event model

The Windows service subscribes to service session-change notifications and also polls WTS. Immediate notifications wake the worker; polling is the reconciliation mechanism.

The agent records:

- session ID
- user/domain/SID
- state
- client name/address where available
- logon/connect/last-input timestamps
- disconnected-since timestamp
- idle seconds

The master compares consecutive snapshots and emits history events such as `logon`, `reconnect`, `disconnect`, `state_change`, `logoff`, and `logon_ready`.

## Broker model

Broker selection is entirely master-side and never modifies the Guacamole database.

A request contains the authenticated username plus Guacamole connection ID/name or an explicit SessionGuard resource/farm. The master resolves the resource and farm, then follows the placement algorithm documented in `BROKER.md`.

The response contains tokens rather than Guacamole-specific mutable state. The extension injects them into the current user context immediately before the connection parameters are resolved.


## RemoteApp desired-state model

For a Resource with Agent-managed publication enabled, the Master derives a `RemoteAppSpec` for each member of the Resource farm and returns it in the normal outbound heartbeat response. No inbound WMI/WinRM connection from Master to the RDS host is introduced.

```text
Master Resource desired state
        |
        | heartbeat response
        v
Windows Agent
        |
        | local Terminal Services WMI provider
        v
Win32_TSPublishedApplication
        |
        | observed state in next snapshot
        v
Master / Broker readiness
```

The Agent discovers all local RemoteApps but mutates only explicitly desired SessionGuard resources. Deletion is limited to aliases recorded in Agent state as having been created by SessionGuard. For managed RemoteApps, the broker treats missing executable, missing publication, desired-state mismatch, or reconciliation errors as host-specific unavailability for that Resource.

## Policy hierarchy

Policy precedence:

1. explicit agent desired policy
2. policy of a farm explicitly listed in the agent's `farm_ids`, in configured order
3. policy of a centrally matching farm (`agent_ids` or `required_tags`), deterministic farm-ID order
4. global policy
5. agent's local policy if no master desired policy applies

Master policy changes are versioned. Rollback creates a new revision derived from the selected historical version, so history remains append-only rather than being rewritten.

## Command model

Administrative actions are queued on the master and delivered through the next heartbeat:

```text
UI -> Master command queue -> heartbeat response -> Agent -> WTS/Win32 -> result -> heartbeat -> audit
```

Commands have IDs, creation time, expiry time and actor. The agent remembers recently executed IDs to avoid duplicate execution if a heartbeat is retried.

Supported command families include:

- message session
- disconnect session
- logoff session
- terminate process
- restart server after drain

## Maintenance states

- `online`: new and existing connections allowed.
- `drain`: no new broker placements; existing sessions may reconnect.
- `maintenance`: neither new placement nor reconnect is allowed.

With `restart_when_drained`, the master queues a restart only once the agent reports zero user sessions.

## Health model

The agent calculates a 0–100 health score from local checks. Current checks include:

- CPU pressure
- memory pressure
- system-disk free space
- local RDP listener reachability
- profile-store reachability when profile sync is enabled

The broker excludes hosts below `broker.min_health_score` for new sessions. Alerts have independent thresholds.

## Failure behavior

### Master unavailable

- Existing Windows sessions continue.
- Agent keeps its last accepted policy.
- Profile jobs and disconnect timers continue.
- Local agent UI remains usable if its OIDC provider is reachable.
- New Guacamole broker requests fail because authoritative farm placement is unavailable. Do not silently fall back to an arbitrary RDS host if duplicate-profile protection matters.

### PostgreSQL unavailable

Master writes fail rather than pretending state was persisted. Existing master in-memory state may still answer reads, but operators should treat the master as degraded and restore DB connectivity.

### Profile share unavailable

Backup/restore jobs retry within their configured semantics. A failed backup blocks cleanup. Restore stops retrying after the configured restore window rather than injecting files into an already-running desktop later.

### Agent unavailable

It is removed from new broker placement after `offline_after_seconds`. Offline alerts use their own threshold.
