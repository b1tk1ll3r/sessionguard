# Changelog

## 0.3.1 - Web UI editor stability

- Fixed Master live refresh replacing Farm, Resource and server-control forms every five seconds.
- Split live server metrics from editable server-control fields.
- Farm and Resource editors are mounted once; only their data tables and broker leases refresh.
- Added explicit reset/reload actions instead of implicit form replacement.
- Added Agent policy dirty-state protection and visible "Ungespeicherte Änderungen" state.
- Added explicit Agent policy reload with confirmation before discarding unsaved edits.
- Added `Cache-Control: no-store, max-age=0` for the Web UI HTML and JavaScript.
- Added browser-level regression verification covering a full live-refresh cycle.

## 0.3.0 - Broker & Director production candidate

### Broker and farm control

- Added logical Farms with explicit agent IDs, agent-side farm IDs and required-tag membership.
- Added Published Resources for desktop and RemoteApp mappings.
- Added health-aware broker with existing-session reconnect preference, leases and configurable global single-session behavior.
- Added strict farm isolation and fail-closed unknown/disabled-farm handling.
- Added `online`, `drain`, and `maintenance` server modes.
- Added `restart_when_drained` workflow.
- Added broker-specific API key and `/api/v1/broker/resolve` / `/api/v1/broker/tokens` APIs.
- Added Guacamole 1.6 extension using token injection instead of database rewriting.

### Director

- Added WTS logon/connect/last-input and client-address telemetry where available.
- Added idle-time calculation and persistent disconnect timestamps.
- Added Windows process inventory per user session and process termination command.
- Added CPU, RAM and system-disk telemetry.
- Added RDP-listener/profile-store health checks and composite health score.
- Added indexed session history and observed logon/restore timing.
- Added configurable threshold alerts and generic webhook notifications.
- Extended Master UI with health, session/process control, logon telemetry, farms, resources, leases, alerts and policy rollback.

### Enterprise control plane

- Added PostgreSQL production persistence and single-active-master advisory-lock protection.
- Split append-only audit/session history into indexed tables while keeping small control-plane state in JSONB.
- Added group-to-role RBAC.
- Added policy versioning/rollback for global, farm and agent policies.
- Added environment-secret overrides for database, enrollment, broker, OIDC and webhook credentials.
- Added hardened Docker deployment example.
- Added bounded agent telemetry/process heartbeat payloads.
- Bound queued process-termination commands to the observed RDS session and revalidated PID/session ownership before termination.

### Fixes/hardening

- Farm policies now also apply to centrally selected members (`agent_ids` / required tags), not only agents with explicit local `farm_ids`.
- Broker never reuses an existing session from a different requested farm.
- Drain hosts are reconnect-only; maintenance hosts are excluded from both reconnect and new placement.
- Broker resources cannot silently fall through to a disabled/unknown farm.

## 0.2.0 - Profile & Session Lifecycle

- Added selected-folder profile backup/restore with transactional snapshot activation and history retention.
- Added backup-before-cleanup gating and retries.
- Added disconnected-session timeout with native WTS logoff and the same backup/cleanup pipeline.
- Added master queued session control and result audit.
- Added Windows service session-change wakeups.
- Added Prometheus metrics and profile/session operation documentation.

## 0.1.1

- Fixed Master editor refresh overwriting in-progress form input.
- Added persistent Dry-Run/activity log to local and Master UI.
- Added structured Templates editor and heartbeat transfer of active policy.

## 0.1.0

- Initial Windows Agent / Linux Master MVP with WTS inventory, delayed profile cleanup, templates, PocketID/OIDC and master-agent heartbeats.
