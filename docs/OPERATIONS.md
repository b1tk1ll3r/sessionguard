# Production Operations

## Recommended rollout order

1. Deploy PostgreSQL and Master behind TLS/reverse proxy.
2. Configure PocketID group RBAC and verify a read-only user and an administrator.
3. Upgrade one RDS host/agent and keep profile cleanup in Dry-Run.
4. Verify heartbeat telemetry, processes, health and session history.
5. Create a canary Farm containing only that host.
6. Load the Guacamole extension and create one brokered canary connection.
7. Verify existing-session reconnect and lease behavior.
8. Add a second canary RDS host; test load balancing, drain and maintenance.
9. Enable selected-folder profile restore/backup if not already validated.
10. Enable automatic disconnected-session logoff.
11. Enable destructive cleanup only after profile backup/recovery has been exercised.
12. Expand farm membership gradually.

## PostgreSQL

Production should set `SESSIONGUARD_DATABASE_URL`.

Example:

```text
postgres://sessionguard:SECRET@sessionguard-postgres:5432/sessionguard?sslmode=disable
```

Use TLS (`sslmode=require`/verify modes) when PostgreSQL crosses an untrusted network. The provided compose uses an internal Docker network and therefore demonstrates `sslmode=disable` only for that local link.

Back up the SessionGuard database like any other control-plane database. It contains operational metadata, session usernames/SIDs, audit history and policies, but not profile files themselves.

## Master backup and restore

Back up:

- PostgreSQL database
- `master.json` configuration (without assuming env-injected secrets are present)
- external secret-management values
- custom Guacamole extension image/tag

Restore database and configuration together. After Master starts, Agents will reconnect with their existing per-agent credentials.

## Profile store backup

The profile store is independent from PostgreSQL and must be protected separately. Snapshot/backup the underlying SMB storage according to your recovery objectives.

SessionGuard maintains per-user `current` plus optional historical snapshots, but these are operational generations, not a replacement for storage backup.

## Drain workflow

For planned patching:

1. Set host to `drain`.
2. Optionally send a message to users.
3. Existing users can reconnect to the same host; new users go elsewhere.
4. Wait for sessions to finish or use policy/bulk logoff as appropriate.
5. Set `restart_when_drained` if automatic restart is desired.
6. After reboot/health verification, set host to `online`.

Use `maintenance` when reconnects must also be blocked.

## Upgrades

### Master

- back up PostgreSQL;
- deploy the new binary/image;
- start one master against the DB and verify `/healthz` reports the expected version/store;
- verify OIDC and dashboard;
- then upgrade the Guacamole extension if required.

The database migration code is designed to be forward-applied by the Master. v0.3 also holds a PostgreSQL advisory lock and intentionally refuses a second active Master against the same database. Do not attempt active/active deployment; for manual failover stop/lose the old Master first, then start the replacement against the same restored/reachable database.

### Agents

Upgrade in farm batches using drain mode. Agent state in `C:\ProgramData\SessionGuard` should be preserved across binary replacement.

## Observability

Monitor at minimum:

- `/healthz`
- `/metrics`
- active alerts in Director
- PostgreSQL health
- profile-store availability
- master heartbeat freshness

Alert webhook delivery is best-effort and should complement, not replace, your infrastructure monitoring.

## Master outage

Existing sessions and agent-side policy continue. Brokered **new connections** cannot be authoritatively placed while the Master is unavailable. This fail-closed behavior protects farm affinity and profile concurrency.

If emergency bypass access is required, maintain a separately controlled break-glass RDP path rather than silently making the normal broker choose an arbitrary host.

## JSON fallback migration

If upgrading from JSON storage:

1. stop the old Master;
2. preserve `master.json`;
3. start v0.3 once with the existing file and verify state;
4. configure PostgreSQL and perform an explicit controlled migration/import workflow before retiring the file.

The code can read legacy control-plane JSON state and PostgreSQL migrations can normalize embedded history, but operationally treat backend migration as a change window and verify counts/history.
