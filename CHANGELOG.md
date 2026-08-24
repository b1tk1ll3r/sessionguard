# Changelog

## 0.5.2 — Public EdgeGuard security layer

- Added `sessionguard-edgeguard`, a dependency-free Go edge pre-check for the public Caddy host.
- Static IPv4/IPv6/CIDR blacklist with automatic reload.
- Global and per-IP token-bucket rate limiting plus endpoint-specific OIDC/login limits.
- NAT-safe defaults: ordinary rate-limit hits do not automatically ban a shared public address.
- Scanner/exploit path detection and persistent temporary auto-bans for clearly hostile behavior.
- Bounded per-IP state table prevents rotating-source floods from causing unbounded memory growth; ban persistence is write-debounced to avoid I/O amplification.
- Blocks CONNECT/TRACE/TRACK, validates allowed public hosts and rejects malformed/oversized URIs.
- Local `/healthz` and Prometheus-style `/metrics` endpoints for EdgeGuard.
- Added hardened public Caddy deployment: strict SNI/Host matching, 10s header timeout, 64 KiB header ceiling, HTTP/1.1+HTTP/2 only and conservative response security headers.
- Caddy admin API is disabled on the dedicated edge; access logs use bounded file rotation and high-load sampling to reduce log-amplification risk.
- Public-VPS example pins Caddy 2.11.4 instead of an unqualified major tag.
- Public Caddy now hides SessionGuard `/metrics` and broker endpoints; Guacamole workers continue to use broker APIs directly over NetBird.
- Added a dedicated `Dockerfile.edgeguard`, public-VPS Compose stack and CI image publication.
- No Master/Agent protocol or database migration; Agent protocol remains version 4.

## 0.5.1 — Modal-first responsive Web UI

- Master-WebUI neu strukturiert: Terminalserver öffnen in einem großen responsiven Arbeitsdialog statt in einer langen Inline-Detailspalte.
- Serverdetails sind in Tabs für Übersicht, Sitzungen, Apps/Prozesse, Profile/Ereignisse und Konfiguration gegliedert.
- Farms und Published Resources werden als kompakte Karten dargestellt; Anlegen/Bearbeiten erfolgt in eigenen Modals.
- Resource-Editor behält RemoteApp-/Agent-Managed-Felder und dynamische Sichtbarkeit vollständig bei.
- Agent-WebUI bündelt RemoteApps, Profil-Pipeline und Aktivitätslog in einer tab-basierten Betriebsansicht.
- Lokale Agent-Policy wird in einem responsiven Modal bearbeitet und zeigt einen sichtbaren Dirty-State; die Übersichtsseite zeigt nur eine kompakte Policy-Zusammenfassung.
- Mobile Modals wechseln auf Vollbild, Tabellen bleiben horizontal scrollbar, Formulare reduzieren sich responsiv auf eine Spalte.
- Live-Refresh/Dirty-Guards bleiben erhalten; offene Policy-Eingaben werden weiterhin nicht vom 5-Sekunden-Refresh überschrieben.
- Keine neue Runtime oder Build-Chain: weiterhin ausschließlich eingebettetes HTML/CSS/Vanilla-JavaScript.
- Agent-Protokoll bleibt Version 4; keine Datenbank- oder API-Migration erforderlich.

## 0.5.0 — Integrated Guacamole Access Auth

- SessionGuard Master now provides `/auth/verify` as a Traefik ForwardAuth endpoint for Guacamole.
- Added a dedicated PocketID/OIDC `access_auth` flow, independent from the Master administration login.
- Added opaque server-side access sessions persisted in the existing control-plane store; only the SHA-256 browser-token hash is used as the lookup key.
- Added strict `X-Guacamole-User` emission only for valid access sessions, group restrictions and allowed return-host validation.
- Added RP-initiated OIDC logout using the discovered `end_session_endpoint`.
- Added OIDC Back-Channel Logout with signature/issuer/audience/event/`iat`/`jti` validation and replay protection.
- Added concurrent-safe per-flow OIDC state cookies and external-prefix-aware callback cookie paths.
- Guacamole extension now ships a framework-free JS helper which redirects Guacamole logout into full SessionGuard/PocketID logout and periodically detects revoked/expired access sessions.
- Added Traefik header-scrubbing/ForwardAuth deployment guidance and migration away from `traefik-forward-auth`.
- Agent protocol remains version 4; no Agent data/schema migration is required.

## 0.4.1 — RemoteApp PowerShell/CLIXML robustness

- RemoteApp PowerShell execution now keeps stderr separate from JSON stdout.
- Suppresses PowerShell progress/information/verbose/debug/warning streams for machine-readable RemoteApp calls.
- Forces UTF-8 console output where supported.
- JSON decoder defensively extracts the first valid JSON object/array and tolerates CLIXML/banner noise before or after the payload.
- Added regression tests for the `#< CLIXML` contamination observed on Windows PowerShell 5.1.
- Protocol remains version 4; no Master/Agent schema migration is required.

## 0.4.0 — Agent-managed RemoteApps

- Added a farm-scoped RemoteApp desired-state model to Published Resources.
- Windows Agent discovers RemoteApps through `root\CIMv2\TerminalServices` / `Win32_TSPublishedApplication`.
- Optional Agent-managed publication creates/updates aliases, executable/icon settings and command-line policy through the Terminal Services WMI provider.
- Reconciliation deletes only aliases originally created by SessionGuard; pre-existing/manual RemoteApps remain in place if management is later disabled.
- Agent heartbeat reports per-app executable, publication, ownership, sync and error state.
- Managed RemoteApps are brokered fail-closed per host until the Agent reports `published + path_exists + in_sync`.
- Master WebUI now exposes application path, alias, icon, command-line policy and per-farm readiness.
- Agent WebUI now includes a RemoteApps inventory/status view.
- Protocol version increased from 3 to 4; Master and Agent must be upgraded together.
- No Node.js/npm/frontend framework or build step introduced.

## 0.3.4 — Modern Web UI

- Master- und Agent-WebUI vollständig modernisiert, weiterhin ohne Framework oder Build-Schritt.
- Responsive Sidebar-Navigation mit Mobile-Menü und Scroll-Tracking.
- Dark-/Light-Theme mit lokaler Browser-Präferenz.
- Neue Dashboard-Karten, Panel-Hierarchie, Status-Badges, moderne Formulare/Switches und Tabellen.
- Live-Bereiche und Editoren bleiben weiterhin getrennt; automatische Refreshes überschreiben keine Eingaben.
- Verbesserte Darstellung für Farms, Broker, Published Resources, Director, Alerts, Audit und lokale Agent-Ansichten.
- Keine neue Runtime-Abhängigkeit: HTML/CSS/Vanilla-JavaScript bleiben direkt in den Go-Binaries eingebettet.

## 0.3.3

- Fix: Master-WebUI definiert und aktualisiert `agentCache`, bevor die Farm-Mitglieder berechnet werden.
- Behebt `ReferenceError: agentCache is not defined` in der Farm-/Broker-Ansicht.
- Die Farm-Agent-Anzahl verwendet weiterhin dieselbe Membership-Logik wie der Broker (`farm.agent_ids`, `agent.farm_ids`, `required_tags`).

## 0.3.2

- Fixed Farm member count in the Master WebUI. The table now uses the same membership rules as the broker: explicit `farm.agent_ids`, agent-side `agent.farm_ids`, and `required_tags`.
- Farm member names are available as a tooltip on the member count.

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
