# Release and Failure-Mode Test Matrix

A production release should pass this matrix on the actual supported Windows Server, Guacamole and storage versions.

## Automated Go tests

Run:

```bash
go mod tidy
go test ./...
go vet ./...
```

Current unit coverage includes:

- policy normalization/validation
- template create/update logic
- profile backup/restore merge behavior
- version retention
- snapshot activation guard when a user becomes active
- broker reconnect to an existing session on a drain host
- drain exclusion for new sessions
- farm isolation
- unknown-farm fail-closed behavior
- farm policy selection through tags

## Guacamole extension build

```bash
cd guacamole-extension
mvn -DskipTests package
```

Then launch Guacamole 1.6 and verify the extension appears in startup logs without version/API errors.

## Broker integration

### New connection

- two online agents in the same farm
- both above minimum health
- verify one is selected and a lease appears
- make selected host significantly more loaded; after lease expiry use a new test user and verify score affects placement

### Reconnect

- disconnect a user's RDS session
- initiate the same Guacamole resource again
- verify broker returns the host containing that session even if that host is `drain`

### Farm isolation

- same username has a stale/disconnected session in Farm B
- connect to a resource in Farm A
- verify Farm B is never selected

### Maintenance

- `drain`: existing session reconnect allowed, new user rejected/placed elsewhere
- `maintenance`: existing-session reconnect is not brokered there
- below-minimum-health host: no new placement
- disabled/missing farm: request fails closed

### Lease race

Issue two near-simultaneous broker requests for the same user and verify they converge on the same valid lease/host in the expected deployment topology.

## Drain / restart-when-empty

1. Set server to drain and `restart_when_drained`.
2. Verify no new users are placed there.
3. Verify existing disconnected user can reconnect.
4. End all user sessions.
5. Verify exactly one restart command is queued/executed.
6. Verify the server returns with agent heartbeat and can be set online.

## Session control

- message
- disconnect
- forced logoff
- bulk disconnected logoff
- process termination
- command expiry if agent remains offline past TTL
- duplicate heartbeat delivery must not re-execute the same command ID
- result appears in audit log

## Profile pipeline

### Normal logoff

- modify selected profile data
- log off
- verify post-logoff delay
- verify `staging` is built
- verify snapshot becomes `current`
- verify cleanup starts only after successful backup

### SMB failure

- make profile store unavailable before logoff
- verify backup enters error/retry
- verify local profile is **not** deleted
- restore SMB and verify retry succeeds before cleanup proceeds

### Reconnect during backup

- use enough data to keep backup running
- reconnect same SID before snapshot activation
- verify new snapshot is not promoted over `current`
- verify no destructive cleanup occurs for the active user

### Restore window

- make SMB unavailable at logon
- keep unavailable beyond `restore_window_seconds`
- verify SessionGuard stops trying to inject the restore into the now-running desktop

### Cleanup race

- let cleanup grace nearly expire
- reconnect user
- verify final WTS recheck cancels `DeleteProfileW`

### Retention

Generate more backups than `keep_versions` and verify `current` plus the configured history count remain valid.

## Director/history

- logon, disconnect, reconnect and logoff should create ordered history rows
- `logon_ready` should contain observed and restore timing when available
- process list should contain user-session processes and remain payload-bounded
- history user/agent filters should work after PostgreSQL restart

## Alerts

Individually cross each configured threshold and verify:

- active alert created
- `last_seen_at` updates
- webhook fires at most at notification minimum interval
- alert resolves after condition clears

Also test profile-failure and offline-agent alerts.

## PostgreSQL

- clean schema bootstrap
- restart Master and verify state/history
- DB unavailable during write: API should surface failure rather than claim success
- restore DB and verify agents resume heartbeats
- backup/restore PostgreSQL into a test instance
- retention with history above configured limit

## OIDC/RBAC

With real PocketID groups:

- viewer cannot mutate
- helpdesk can perform allowed session operations
- operator can use maintenance/process functions
- policy admin can write/rollback policy
- auditor can read audit
- admin can manage farms/resources
- user with no matching group receives `default_role`

## Header-auth / network test

From an unrelated container/network, attempt direct HTTP access to Guacamole with a forged `X-Guacamole-User`. The architecture is acceptable only if that bypass path is unreachable/blocked.

## Canary release gate

Before broad rollout record:

- exact Windows Server builds tested
- exact Guacamole image/extension build
- PocketID version/client configuration
- PostgreSQL version
- profile-store SMB server/version
- successful restore from PostgreSQL backup
- successful restore of at least one user profile snapshot
- successful drain/reconnect/restart workflow
