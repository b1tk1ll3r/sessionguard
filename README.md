# SessionGuard

SessionGuard is a Go-based **RDS control plane** for Windows Remote Desktop Session Hosts. It is designed to complement Apache Guacamole: Guacamole remains the HTML5/RDP gateway, while SessionGuard provides Citrix-like broker, Director, policy, profile-lifecycle and operations functions.

**Current development version: 0.5.1 (modal-first responsive Web UI + integrated Guacamole Access Auth)**

> SessionGuard is not an ICA/HDX implementation and does not replace the Windows RDS runtime. It deliberately reuses standard RDP/WTS, Guacamole and PocketID/OIDC.

## 0.5.1 Web UI refresh

The Master and local Agent consoles now use a modal-first, responsive Vanilla-JavaScript interface. Server drill-down, Farm creation, Resource editing and local Agent policy editing no longer occupy permanent large inline forms. The Master server workspace uses tabs, Farms/Resources use compact cards, and the Agent groups operational data into a tabbed workspace. No Node.js/npm/frontend build step is required.

## 0.5.0 Integrated Guacamole Access Auth

- `sessionguard-master` can now act directly as Traefik ForwardAuth for Guacamole; no separate forward-auth container is required.
- PocketID OIDC login for Guacamole uses a separate `access_auth` configuration/cookie scope from the SessionGuard admin UI.
- Browser sessions are opaque, server-side and persisted in the existing Master store; expired/revoked sessions are denied even if Guacamole still has an old auth token.
- `X-Guacamole-User` is emitted only after a valid SessionGuard access-session check. There is no fallback identity.
- OIDC RP-initiated logout and PocketID back-channel logout are supported; back-channel tokens are signature/audience/event/time/JTI checked with replay protection.
- The Guacamole extension now includes plain JavaScript which detects Guacamole logout and performs the full SessionGuard/PocketID logout, while also polling access-session status to close stale browser sessions.
- Recommended deployment exposes `/_sessionguard/auth/*` on the Guacamole hostname and routes that prefix to the same Master container.

See `docs/ACCESS-AUTH.md` for the PocketID and Traefik migration.

## 0.4.1 RemoteApp robustness

Windows PowerShell auxiliary streams are now isolated from the JSON protocol used by RemoteApp discovery/reconciliation. CLIXML/progress noise no longer breaks RemoteApp status decoding.

## 0.4.0 RemoteApp control plane

- The Master can define RemoteApp desired state per Published Resource/Farm.
- Windows Agents discover the local `Win32_TSPublishedApplication` inventory through the documented Terminal Services WMI provider.
- For resources with **Agent-managed RemoteApp publication** enabled, the Agent creates/updates the local RemoteApp registration and removes only aliases that the Agent itself originally created. Existing/manual RemoteApps may be adopted for desired-state checks but are never deleted merely because management is later disabled.
- Executable presence, publication state and desired-state convergence are reported back in every Agent snapshot.
- The broker fails closed per managed RemoteApp: a host is eligible only when that application is present, published and in sync. Desktop resources and unmanaged RemoteApps keep their previous placement behavior.
- Master and Agent WebUIs show RemoteApp configuration/readiness without Node.js or a frontend build chain.
- Agent protocol version is now `4`; upgrade Master and Agents together.

> The local Terminal Services WMI provider is used instead of direct registry manipulation. This mode should be canary-tested on the exact Windows Server/RDSH versions in your environment; it does not claim to recreate every Microsoft Connection Broker/Collection management semantic.

See `docs/REMOTEAPP.md` for the canary, ownership/rollback behavior and Guacamole mapping.

## 0.3.4 Modern Web UI

- Master and Agent consoles use a responsive enterprise-style sidebar and dashboard layout.
- Dark/light theme switching is implemented with plain CSS and Vanilla JavaScript.
- Modern status badges, cards, forms, switches and tables without Node.js, npm, Vue, React or a frontend build step.
- The existing refresh-safe editor behavior remains intact: live telemetry can update without replacing forms currently being edited.

## 0.3.3 Farm UI regression fix

The master UI now maintains an explicit `agentCache` from the dashboard response before rendering farm membership. This fixes `ReferenceError: agentCache is not defined` introduced by the v0.3.2 farm-count display correction.


## 0.3.1 UI stability fix

- Live dashboard refresh no longer recreates editable Master forms.
- Server live metrics and server-control inputs are rendered independently.
- Farm and Published Resource editors remain mounted while their live tables refresh.
- Agent policy edits are protected by a dirty-state guard and an explicit reload action.
- HTML and `app.js` are served with `Cache-Control: no-store` to prevent stale UI code after upgrades.

## 0.3.0 at a glance

### Smart broker and farms

- Logical farms with explicit agent membership and/or required tags.
- Published resources for full desktops and RemoteApps.
- Existing-session reconnect affinity before new load balancing.
- Persistent user leases to reduce duplicate sessions across hosts.
- Optional global single-session-per-user behavior.
- Health-aware placement using server health, sessions, CPU and memory pressure.
- `online`, `drain` and `maintenance` server modes.
- `restart_when_drained`: stop new allocations, wait for all user sessions to leave, then queue a controlled restart.
- Farm and global policy inheritance, plus per-agent override.

### Guacamole 1.6 bridge

`guacamole-extension/` contains a small Java extension using Guacamole's extension API. It does not authenticate users. It decorates the already authenticated Guacamole user context, asks the SessionGuard broker for connection tokens and injects:

- `${SESSIONGUARD_HOST}`
- `${SESSIONGUARD_AGENT_ID}`
- `${SESSIONGUARD_RESOURCE_ID}`
- `${SESSIONGUARD_REMOTE_APP}`
- `${SESSIONGUARD_REMOTE_APP_DIR}`
- `${SESSIONGUARD_REMOTE_APP_ARGS}`

This keeps PocketID / header authentication and the browser gateway separate from broker logic. See `docs/GUACAMOLE.md`.

### Director-style operations

- Consolidated server and session inventory.
- RDS logon/connect/last-input timestamps and calculated idle time.
- Per-session process inventory and remote process termination.
- CPU, memory and system-disk telemetry.
- RDP-listener and profile-store health checks.
- Composite server health score used by both the UI and broker.
- Session history for logon, reconnect, disconnect, logoff and SessionGuard `logon_ready` events.
- Logon telemetry including SessionGuard restore duration and observed time until the post-restore/template stage is ready.
- Active alerts for offline agents, CPU/RAM/disk/health, profile failures, disconnected-session count and slow observed logon.
- Optional generic JSON webhook notifications with notification throttling.
- Prometheus-compatible `/metrics` endpoint.

### Enterprise control plane

- PostgreSQL persistence for production with a database advisory lock preventing accidental active/active masters.
- Append-only audit and session-history tables with retention and indexes.
- JSONB control-plane state for agents, farms, resources, leases, policies and alerts.
- JSON-file persistence remains available as a small single-node fallback / migration path.
- PocketID / generic OIDC authentication.
- Group-to-role RBAC.
- Policy version history and rollback for global, farm and agent policies.
- Agent-initiated HTTPS heartbeats and command delivery only; no inbound management port from master to RDS hosts.
- Expiring, deduplicated command queue with result acknowledgement and audit trail.

### Windows agent and profile lifecycle

The v0.2 functionality remains:

- Windows service with native WTS session inventory and session-change wakeups.
- Send message, disconnect and logoff user sessions.
- Automatic logoff of disconnected sessions after a persistent configurable timeout.
- Backup of selected profile folders after logoff.
- Versioned transactional profile snapshots (`staging -> current`).
- Restore of the last successful snapshot on a newly observed logon.
- A failed backup blocks local profile deletion.
- Cleanup with grace period, exclusions, allowed profile roots, final WTS recheck and `DeleteProfileW`.
- Structured templates for files, folders, `.url` and `.lnk` shortcuts.
- Local PocketID/OIDC-protected fallback UI on every agent.

See `docs/PROFILE-LIFECYCLE.md` for the deliberately limited scope of profile synchronization.

## Target architecture

```text
PocketID
   |
Traefik / trusted header-auth boundary
   |
Apache Guacamole 1.6
 + sessionguard-guacamole extension
   |
   | broker token request
   v
SessionGuard Master  ---------------- PostgreSQL
   |
   | outbound agent heartbeats carry policy/commands/results
   |
   +-------------+-------------+
   |             |             |
 RDS01         RDS02         RDS03
 Agent         Agent         Agent
```

## Quick start: master

Copy the example configuration and environment file:

```bash
cp configs/master.example.json deploy/master.json
cp deploy/.env.example deploy/.env
```

Set at least:

- `SESSIONGUARD_DB_PASSWORD`
- `SESSIONGUARD_ENROLLMENT_TOKEN`
- `SESSIONGUARD_BROKER_API_KEY`
- `SESSIONGUARD_OIDC_CLIENT_SECRET`
- PocketID issuer/client/redirect values in `master.json`
- `TRAEFIK_NETWORK`

Then:

```bash
cd deploy
docker compose up -d --build
```

For production, expose the master only through your existing TLS reverse proxy. The bundled compose publishes port 8080 only on `127.0.0.1` as an additional safety measure.

## Quick start: Windows agent

Build on a machine with Go module access:

```powershell
.\scripts\build.ps1
```

Copy `configs/agent.example.json`, configure PocketID, the master URL and your initial policy, then install from an elevated PowerShell:

```powershell
.\scripts\install-agent.ps1 -BinaryPath .\dist\sessionguard-agent.exe -ConfigPath .\configs\agent.json
```

For SMB profile stores, prefer a gMSA and grant it the required share/NTFS permissions. Do not store SMB passwords in the agent JSON.

## Guacamole integration

Build a Guacamole image containing the extension using `deploy/guacamole/Dockerfile.guacamole`. Add these environment variables to Guacamole:

```yaml
SESSIONGUARD_MASTER_URL: http://sessionguard-master:8080
SESSIONGUARD_BROKER_API_KEY: ${SESSIONGUARD_BROKER_API_KEY}
SESSIONGUARD_BROKER_TIMEOUT_MS: "2500"
```

A brokered desktop connection uses:

```text
hostname = ${SESSIONGUARD_HOST}
```

A RemoteApp can additionally use the `SESSIONGUARD_REMOTE_APP*` tokens. Map the Guacamole connection ID or name to a SessionGuard Resource in the master UI.

**Important:** with Guacamole header authentication, untrusted containers or networks must not be able to reach Guacamole directly and forge the authentication header. See `docs/GUACAMOLE.md` and `docs/SECURITY.md`.

## Master environment overrides

Secrets do not need to be stored in JSON:

| Variable | Purpose |
|---|---|
| `SESSIONGUARD_DATABASE_URL` | PostgreSQL DSN |
| `SESSIONGUARD_ENROLLMENT_TOKEN` | one-time agent enrollment shared secret |
| `SESSIONGUARD_BROKER_API_KEY` | Guacamole-extension → broker credential |
| `SESSIONGUARD_OIDC_CLIENT_SECRET` | master PocketID/OIDC client secret |
| `SESSIONGUARD_ALERT_WEBHOOK_URL` | optional alert webhook |

Agent overrides:

| Variable | Purpose |
|---|---|
| `SESSIONGUARD_MASTER_URL` | master URL |
| `SESSIONGUARD_ENROLLMENT_TOKEN` | enrollment secret |
| `SESSIONGUARD_OIDC_CLIENT_SECRET` | local agent UI OIDC secret |

## Broker placement order

1. Resolve an enabled Published Resource if one matches the requested Guacamole connection ID/name.
2. Determine the requested/default farm.
3. If enabled, reconnect a matching existing Active/Connected/Disconnected session in that farm. A `drain` host may be used for this; `maintenance` may not.
4. Reuse a non-expired matching user lease if possible.
5. Select only online, healthy, `online`-mode members of the farm.
6. Score candidates using health, active/disconnected sessions, CPU and memory pressure.
7. Create/refresh the user lease and return connection tokens.

A farm boundary is fail-closed: a session on another farm is not reused for a request for the selected farm.

## Policy precedence

```text
agent override
    > explicit farm membership (order in agent.farm_ids)
    > centrally selected farm (agent_ids / required_tags, deterministic farm-ID order)
    > global policy
```

All policy writes are normalized and validated before activation. Every global/farm/agent policy write creates a version that can be rolled back from the UI/API.

## RBAC roles

Built-in roles are:

- `viewer`
- `helpdesk`
- `operator`
- `profile_admin`
- `policy_admin`
- `auditor`
- `admin`

PocketID `groups` claims are mapped in `master.json`. If the RBAC group mapping is intentionally empty, SessionGuard preserves backwards compatibility and grants authenticated users the `admin` role; do not leave it empty in a multi-admin production environment.

See `docs/RBAC.md`.

## Profile scope and safety

SessionGuard synchronizes selected **profile folders**, not a complete Windows profile. Do not configure `NTUSER.DAT`, registry hives, the whole profile root, EFS material or Windows-owned profile internals as roaming content.

Good candidates include:

- `AppData\Roaming\Vendor\Product`
- `Desktop`
- `Documents`
- application-owned configuration folders safe to merge after logon

If the profile backup fails, cleanup is not scheduled. If the user reconnects before snapshot activation or immediately before cleanup, the dangerous operation is cancelled.

## Build and test

Requirements:

- Go 1.23+
- Windows Server 2019/2022/2025 x64 for agent integration testing
- Maven/JDK 11+ for the Guacamole extension
- PostgreSQL 17 recommended for production

Go:

```bash
go mod tidy
go test ./...
go vet ./...
```

Guacamole extension:

```bash
cd guacamole-extension
mvn -DskipTests package
```

The current source tree contains unit tests for configuration, templates, profile snapshot behavior and broker placement invariants. A real release should additionally pass the Windows/RDS/SMB/PostgreSQL integration matrix in `docs/TESTING.md`.

## Documentation

- `docs/ARCHITECTURE.md` – components, state and trust boundaries
- `docs/BROKER.md` – farms, resources, leases, scoring and maintenance states
- `docs/DIRECTOR.md` – telemetry, history, processes, alerts and health scoring
- `docs/GUACAMOLE.md` – extension and integration with Guacamole/PocketID
- `docs/PROFILE-LIFECYCLE.md` – profile backup/restore/cleanup pipeline
- `docs/RBAC.md` – roles and PocketID group mapping
- `docs/API.md` – HTTP API
- `docs/OPERATIONS.md` – deployment, upgrade, backup and recovery
- `docs/SECURITY.md` – security model and hardening
- `docs/TESTING.md` – release and failure-mode test matrix
- `CHANGELOG.md` – release changes

## Production-candidate status

The design intentionally fails closed around destructive profile operations and broker farm boundaries. Nevertheless, v0.5.0 should be introduced as a canary before broad production rollout. In particular, validate native WTS behavior, SMB failure/recovery, PostgreSQL backup/restore, Guacamole extension loading and your exact PocketID group claims in your environment.
