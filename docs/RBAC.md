# RBAC and PocketID Group Mapping

## Identity

The Master uses standard OIDC and expects the `groups` claim when group-based RBAC is configured. PocketID can provide this claim to the SessionGuard OIDC client.

## Configuration

Example:

```json
"rbac": {
  "default_role": "viewer",
  "groups": {
    "sessionguard-admins": ["admin"],
    "sessionguard-helpdesk": ["helpdesk"],
    "sessionguard-operators": ["operator"],
    "sessionguard-policy": ["policy_admin"],
    "sessionguard-profile": ["profile_admin"],
    "sessionguard-auditors": ["auditor"]
  }
}
```

Group matching is case-insensitive. A user may receive multiple roles and therefore the union of their permissions.

If `rbac.groups` is empty, all authenticated users receive `admin` for backwards compatibility. This is convenient for upgrades but should not be used as the final production configuration.

## Built-in roles

| Role | Permissions | Intended use |
|---|---|---|
| `viewer` | `view` | read-only Director |
| `helpdesk` | `view`, `session` | messages/disconnect operations that do not require forced logoff/process control |
| `operator` | `view`, `session`, `session_logoff`, `process`, `maintenance` | RDS operations staff |
| `profile_admin` | `view`, `session`, `profile` | profile operations/pipeline administration |
| `policy_admin` | `view`, `policy` | policy versions and rollout |
| `auditor` | `view`, `audit` | audit review |
| `admin` | all current permissions | full control |

`manage` covers farm/resource administration. `maintenance` covers server mode/restart-when-drained. `process` covers process termination.

## Separation of identities

Do not reuse credentials across these trust relationships:

- PocketID OIDC client secret: browser/admin authentication
- broker API key: Guacamole extension → Master
- enrollment token: new Agent → Master bootstrap
- per-agent bearer token: established Agent → Master heartbeat
- PostgreSQL password: Master → database

## Audit actor

For authenticated administrative changes, SessionGuard records the OIDC email when present, otherwise name, otherwise subject identifier. System-generated operations use an explicit system actor such as `system:drain`.
