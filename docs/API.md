# HTTP API (v0.3)

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

Resource fields include desktop/RemoteApp kind, farm ID, Guacamole connection ID/name and RemoteApp parameters.

## Director/history

- `GET /api/v1/history?user=&agent=&limit=`
- `GET /api/v1/alerts`
- `GET /api/v1/leases`
- `GET /api/v1/audit` – `audit`
- `GET /api/v1/me` – current OIDC user, roles and permissions

`history_limit` bounds retained history. Query `limit` is additionally constrained server-side.

## Agent protocol

`model.ProtocolVersion` is `3` for v0.3.

Heartbeat snapshots contain server/health/session/process/telemetry/profile/event state. The response contains effective desired policy and pending commands. Agents acknowledge completed commands in later heartbeats.
