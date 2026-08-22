# Broker, Farms and Published Resources

## Goal

The SessionGuard broker provides the Citrix-like placement functions that Guacamole's generic RDP connections do not know about: reconnect affinity, farm boundaries, drain/maintenance state, health-aware placement and user leases.

## Farms

A farm is a logical group of RDS hosts.

Membership can be expressed in three ways:

1. the agent has the farm ID in `farm_ids`;
2. the farm has the agent ID in `agent_ids`;
3. the agent satisfies all `required_tags` of the farm.

Example:

```json
{
  "id": "office",
  "name": "Office",
  "required_tags": {
    "role": "office",
    "environment": "production"
  },
  "enabled": true
}
```

Tags are exact key/value matches. Explicit membership and tag membership can coexist.

## Published resources

A Resource maps a Guacamole connection to a farm and optionally to a RemoteApp definition.

Desktop example:

```json
{
  "name": "Office Desktop",
  "kind": "desktop",
  "farm_id": "office",
  "guacamole_connection_name": "Office Desktop",
  "enabled": true
}
```

RemoteApp example:

```json
{
  "name": "Sage",
  "kind": "remoteapp",
  "farm_id": "erp",
  "guacamole_connection_name": "Sage",
  "remote_app": "||Sage",
  "remote_app_dir": "C:\\Program Files\\Sage",
  "remote_app_args": "",
  "enabled": true
}
```

Connection ID matching is preferred where stable IDs are known; connection-name matching is case-insensitive and convenient for initial deployment.

## Placement algorithm

Given `username`, optional `resource_id`, `farm_id`, and Guacamole connection identity:

1. Resolve an enabled Resource by explicit resource ID, Guacamole connection ID, or Guacamole connection name.
2. Resolve the farm: Resource farm → explicit request farm → configured default farm.
3. If a non-empty farm does not exist or is disabled, fail closed.
4. If `reconnect_existing` is enabled, search only matching farm members for an existing `Active`, `Connected` or `Disconnected` session for the user.
5. `maintenance` hosts are excluded from reconnect; `drain` hosts are allowed for reconnect.
6. Reuse a non-expired lease if its host is still available and belongs to the farm.
7. For a new session, consider only hosts that are:
   - online,
   - in `online` maintenance mode,
   - members of the farm,
   - at or above `min_health_score`.
8. Rank candidates by broker score and select the highest score.
9. Create or refresh the lease and return connection tokens.

## Broker score

The current score deliberately favors health and penalizes load:

```text
health score * 10
- active sessions * 20
- disconnected sessions * 5
- CPU percent * 2
- memory-used percent
```

This is an intentionally understandable heuristic rather than an opaque ML model. Thresholds and formula can be evolved without changing the Guacamole integration.

## User leases

A lease reduces races between two nearly simultaneous connection attempts.

With:

```json
"single_session_per_user": true
```

the normalized username is the global lease key. The intent is that a user is directed back to one RDS host whenever possible.

With it disabled, the lease key is scoped by:

```text
user | farm | resource
```

This permits independent sessions in separate published resources/farms.

Leases expire after `lease_seconds`; successful broker use refreshes the expiry.

## Username normalization

Broker comparisons are case-insensitive. `DOMAIN\user` and matching WTS domain/user fields are normalized before comparison.

In environments with ambiguous duplicate short usernames across domains, pass the domain-qualified username from the identity/gateway layer.

## Drain and maintenance

### Drain

Use before patching or planned retirement of a server:

- new sessions: blocked
- existing-session reconnect: allowed
- existing sessions: untouched

Optionally set `restart_when_drained=true`. When zero user sessions remain, a restart command is queued.

### Maintenance

Use for a host that must not receive any brokered access:

- new sessions: blocked
- reconnect: blocked

Existing Windows sessions are not forcefully killed merely by changing this state; use bulk session actions if required.

## Farm policy inheritance

Farm policies are independent from broker selection but use the same membership concepts. Explicit `agent.farm_ids` determine precedence when an agent intentionally belongs to multiple farms. Centrally inferred memberships are evaluated deterministically by farm ID.

## Broker API security

The broker endpoints are not user-OIDC endpoints. They are server-to-server APIs protected by `SESSIONGUARD_BROKER_API_KEY`.

Recommendations:

- generate a high-entropy secret;
- keep Guacamole and Master on a private Docker/network path;
- do not reuse the enrollment token, OIDC client secret or PostgreSQL password;
- rotate the key by updating Master and Guacamole together during a controlled maintenance window.
