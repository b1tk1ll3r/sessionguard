# Director Functions

SessionGuard 0.3 turns the master UI into an RDS-oriented operational console rather than merely a configuration page.

## Server telemetry

Each heartbeat can include:

- hostname and Windows version/build
- uptime
- CPU utilization
- physical memory total/available
- system disk total/free
- composite health score and health-check results
- agent version and heartbeat time

## Session telemetry

For each RDS session, the agent reports the available WTS data:

- session ID
- state
- domain/user/SID
- client name/address where available
- logon timestamp
- connect timestamp
- last-input timestamp
- persistent disconnected-since timestamp
- calculated idle seconds

Not every WTS field is guaranteed to be populated by every Windows version/client combination. The UI treats missing values as unavailable rather than inventing them.

## Session history

The master diffs consecutive snapshots and records history events. Typical timeline:

```text
08:01:12 logon
08:01:20 logon_ready  logon=8124ms restore=1380ms
10:44:03 disconnect
10:55:11 reconnect
16:31:52 disconnect
17:31:55 logoff      (possibly automatic timeout)
```

History can be filtered by user and agent through the API. Production PostgreSQL stores history in its own indexed append-only table.

## Logon telemetry

SessionGuard can observe part of the logon path that it directly participates in:

- first observed new session
- native WTS logon time if available
- profile restore start/completion
- time until restore/template stage is ready

`ObservedLogonMS` is therefore an **RDS/SessionGuard observed duration**, not a claim to reproduce all Citrix Director logon phases. It is useful for trend detection and alerting, especially when restore/profile storage is responsible for slow logons.

## Process inventory

The Windows agent enumerates processes and associates them with RDS session IDs. The heartbeat includes only user-session processes and caps the list to protect payload size.

From the Director UI an authorized operator can request termination of a process. The action is queued, expires, is executed locally by the agent and is audited. The command carries the session ID observed with the PID; immediately before termination the agent re-enumerates processes and refuses the action if that PID no longer belongs to the expected RDS session. This reduces PID-reuse risk for delayed commands.

Process termination is intentionally an elevated operation and requires the `process` permission.

## Health score

Current local checks start at 100 and subtract penalties when a check fails. The UI exposes both score and check details. The same score feeds new-session brokering.

Current checks:

- CPU below the critical local threshold
- memory below the critical local threshold
- minimum system-disk free space
- TCP reachability of the local RDP listener
- configured profile store reachable

This health score is a placement signal, not a substitute for Windows monitoring/EDR.

## Alerts

When enabled, the master maintains active alerts for conditions including:

- agent offline
- CPU above configured threshold
- memory above configured threshold
- system disk below configured free GiB
- health score below configured threshold
- profile failures above configured count
- disconnected sessions above configured count
- observed logon duration above configured threshold

Alerts have first-seen, last-seen, active/resolved state and notification throttling. A generic JSON webhook can receive alert changes.

## Prometheus

`GET /metrics` exposes aggregate master metrics without usernames/SIDs:

- `sessionguard_master_up`
- `sessionguard_agents_total`
- `sessionguard_agents_online`
- `sessionguard_sessions_active`
- `sessionguard_sessions_disconnected`
- `sessionguard_profile_jobs`
- `sessionguard_cleanup_jobs`
- `sessionguard_health_score_sum`

The endpoint is intentionally small in v0.3. Place it behind network ACLs or your metrics network even though it contains no per-user data.

## Retention

`history_limit` bounds session history and audit retention. PostgreSQL retention is applied to the append-only tables; it is not necessary to rewrite an ever-growing control-plane blob on every heartbeat.
