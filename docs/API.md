# HTTP API (v0.4)

All JSON APIs return an error object with an `error` field on failure unless otherwise stated.

## Authentication classes

### Public/internal service endpoints

- `GET /healthz`
- `GET /metrics`
- `POST /api/v1/agents/enroll` – enrollment-token authenticated in request body
- `POST /api/v1/agents/heartbeat` – per-agent bearer authentication
- `POST /api/v1/broker/resolve` – broker bearer API key
- `POST /api/v1/broker/tokens` – broker bearer API key, form-encoded for Guacamole extension

### OIDC-protected admin endpoints

All remaining `/api/v1/...` endpoints require an authenticated Master OIDC session; write operations additionally require the documented RBAC permission.

## Broker

### `POST /api/v1/broker/resolve`

Header:

```text
Authorization: Bearer <SESSIONGUARD_BROKER_API_KEY>
```

JSON request:

```json
{
  "username": "DOMAIN\\user",
  "connection_id": "42",
  "connection_name": "Office Desktop",
  "resource_id": "optional",
  "farm_id": "optional"
}
```

Response includes selected agent/hostname, farm/resource, reconnect reason, health score, lease expiry and token map.

### `POST /api/v1/broker/tokens`

Same broker credential. Accepts form fields `username`, `connection_id`, `connection_name`, `resource_id`, `farm_id`. Returns URL-encoded token pairs for the Guacamole extension.

## Agents and dashboard

- `GET /api/v1/dashboard`
- `GET /api/v1/agents/{id}`
- `PATCH /api/v1/agents/{id}/control` – `maintenance`

Control body:

```json
{
  "mode": "online|drain|maintenance",
  "restart_when_drained": false,
  "tags": {"role":"office"},
  "farm_ids": ["office"]
}
```

## Sessions

- `POST /api/v1/agents/{id}/sessions/{session}/action` – `session`
- `POST /api/v1/agents/{id}/sessions/bulk` – `session`

Actions: `message`, `disconnect`, `logoff`. Forced logoff additionally requires the server-side role permission checks applicable to the operation.

Bulk request example:

```json
{
  "action": "logoff",
  "scope": "disconnected"
}
```

Scopes include `all`, `active`, and `disconnected`.

## Processes

- `POST /api/v1/agents/{id}/processes/{pid}/kill` – `process`

The Master queues an expiring command; it is not a synchronous remote Win32 call.

## Policies

- `PUT /api/v1/agents/{id}/policy` – `policy`
- `PUT /api/v1/policy/all` – `policy`
- `PUT /api/v1/farms/{id}/policy` – `policy`
- `GET /api/v1/policy/history` – `policy`
- `POST /api/v1/agents/{id}/policy/rollback/{revision}` – `policy`
- `POST /api/v1/farms/{id}/policy/rollback/{revision}` – `policy`
- `POST /api/v1/policy/global/rollback/{revision}` – `policy`

Policy writes are normalized, validated, assigned a fresh revision and appended to policy history.

## Farms

- `GET /api/v1/farms`
- `POST /api/v1/farms` – `manage`
- `PUT /api/v1/farms/{id}` – `manage`
- `DELETE /api/v1/farms/{id}` – `manage`

Farm fields include `name`, `description`, `agent_ids`, `required_tags`, optional `policy`, and `enabled`.

## Published resources

- `GET /api/v1/resources`
- `POST /api/v1/resources` – `manage`
- `PUT /api/v1/resources/{id}` – `manage`
- `DELETE /api/v1/resources/{id}` – `manage`

Resource fields include desktop/RemoteApp kind, farm ID, Guacamole connection ID/name and RemoteApp parameters. RemoteApp resources additionally support Agent-managed local publication:

```json
{
  "name": "Sage",
  "kind": "remoteapp",
  "farm_id": "erp",
  "remote_app": "||Sage",
  "manage_remote_app": true,
  "remote_app_path": "C:\\Program Files\\Sage\\Sage.exe",
  "remote_app_icon_path": "",
  "remote_app_icon_index": 0,
  "remote_app_command_line_setting": 0,
  "remote_app_required_command_line": "",
  "remote_app_show_in_portal": false,
  "enabled": true
}
```

`remote_app_command_line_setting` is `0` (deny client-provided arguments), `1` (allow), or `2` (require the configured command line). For Agent-managed RemoteApps the broker mirrors this policy into Guacamole tokens: setting `0` emits no RemoteApp arguments, setting `1` uses `remote_app_args`, and setting `2` forces `remote_app_required_command_line`.

## Director/history

- `GET /api/v1/history?user=&agent=&limit=`
- `GET /api/v1/alerts`
- `GET /api/v1/leases`
- `GET /api/v1/audit` – `audit`
- `GET /api/v1/me` – current OIDC user, roles and permissions

`history_limit` bounds retained history. Query `limit` is additionally constrained server-side.

## Agent protocol

`model.ProtocolVersion` is `4` for v0.4. Master and Agent must use the same protocol version.

Heartbeat snapshots contain server/health/session/process/telemetry/profile/event state plus `remote_apps`. Each RemoteApp status can report `resource_id`, `alias`, `path`, `path_exists`, `published`, `managed`, `in_sync` and `error`.

Heartbeat responses contain effective desired policy, pending commands and `desired_remote_apps`. The latter is intentionally always present (including an empty array) so an Agent can safely remove SessionGuard-owned registrations that are no longer desired. Agents acknowledge completed commands in later heartbeats.
