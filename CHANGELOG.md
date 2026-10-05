# Changelog

## 0.6.0 — Multi-Monitor (Span) + Security-Hardening

### Multi-Monitor

- Neuer Span-Modus für Guacamole-Desktops: Die Extension (`js/sessionguard-multimonitor.js`) legt die Sitzung in ein Browserfenster über mehrere lokale Monitore; mit `resize-method=display-update` folgt die RDP-Auflösung. In Chrome/Edge läuft das automatisch über die Window Management API, in anderen Browsern zieht der Benutzer das Fenster selbst auf. Berücksichtigt die guacd-Grenze von 8192 px.
- Resource-Felder `multi_monitor`, `max_monitors` (2–4) und `multi_monitor_groups` sowie ein Editor dafür in der Master-WebUI.
- Neuer Endpoint `GET /auth/display-policy` (unter `/_sessionguard/auth`, Access-Session erforderlich).
- Caddy: `Permissions-Policy` auf dem Guacamole-Host erlaubt `window-management=(self)` und `fullscreen=(self)`.
- Siehe `docs/MULTI-MONITOR.md`. Guacamole 1.6 hat kein natives RDP-Multi-Monitor; die Upstream-PRs sind noch Drafts.

### Web-UI (Master & Agent)

- Neu gebaut auf einem gemeinsamen Design-System (`internal/webui`: `sg.css`, `sg.js`, `policy-editor.js`). Das UI liegt jetzt als echte Dateien in `internal/master/web` und `internal/agent/web` (`go:embed`) statt als einzeilige Go-Strings; weiterhin ohne Build-Schritt und ohne npm.
- Navigation mit eigenen Seiten und Deep-Links (`#/servers/<id>/policy`). Jede Seite lädt nur ihre Daten; Historie, Audit und Policies werden alle 30 s statt alle 5 s geladen.
- Alle Tabellen haben Suche, sortierbare Spalten und Trefferzähler; Historie, Audit, Server und Sitzungen lassen sich als CSV exportieren (Excel-kompatibel, Schutz gegen Formel-Injection).
- Eigene Dialoge statt Browser-`prompt`/`confirm`; destruktive Aktionen bestätigt man explizit, das Zurücksetzen der Identitäten nur durch Eingabe von `RESET`.
- Buttons richten sich nach den Rechten des Benutzers. Live-Anzeige mit Pause und Fehler-Banner; Warnung vor dem Verlassen bei ungespeicherten Änderungen.
- Neu: globale **Sitzungsansicht** über alle Server mit Mehrfachauswahl, **CPU/RAM-Verlauf** als Sparklines, **Farm bearbeiten** (inkl. expliziter Mitglieder), **Farm-Policy-** und **globaler Policy-Editor**, Anzeige der **Policy-Quelle** pro Server samt „Override entfernen“, Ressourcen aktivieren/deaktivieren/duplizieren, Anzeige ausstehender Befehle, Policy-Revisionen als JSON, Schnellwahl für den Broker-Modus.
- Agent-UI: Übersicht mit Master-Verbindung und Health-Checks, Sitzungen mit Suche/Filter, die lokale Policy als eigene Seite statt als Modal.
- API: `GET /api/v1/policy/global`, `GET /api/v1/agents/{id}/policy/effective`, `DELETE /api/v1/agents/{id}/policy`, `DELETE /api/v1/farms/{id}/policy`, Agent `GET /api/v1/me`.
- Fix: `PUT /api/v1/farms/{id}` ohne `policy`-Feld hat eine vorhandene Farm-Policy stillschweigend gelöscht.

### GitHub Actions

- New workflows in `.github/workflows/`: `ci.yml` (gofmt, vet, tests on Linux + Windows, Maven, JS syntax), `release.yml` (images to GHCR with provenance/SBOM, extension JAR as artifact or release asset) and `windows-agent-release.yml` (GitHub Release via `gh`). Releases only run after green CI.
- Fixes compared with the Gitea workflows: the Go version now comes from `go.mod` (previously 1.23 instead of 1.26), workflow inputs are passed through `env` instead of being interpolated into the shell, and the duplicate master build from `registry.yml` is gone. Minimal `permissions` per job; the built-in `GITHUB_TOKEN` is enough, no secrets needed.
- Dependabot for actions, Go, Maven and base images (Guacamole versions excluded).
- Dockerfiles also copy `go.sum` before `go mod download`.
- `.gitea/workflows/` is kept for existing Gitea mirrors. See `deploy/guacamole/CI-CD.md`.

### Keycloak

- OIDC is no longer PocketID-specific. New fields `oidc.scopes` / `access_auth.scopes`: Keycloak rejects the previously hard-coded `groups` scope with `invalid_scope`. New fields `groups_claims` take claim names or dot paths (`realm_access.roles`, `resource_access.<client>.roles`). `access_auth` inherits both from `oidc`.
- Group comparison ignores a leading `/`, so Keycloak's "Full group path" (`/sessionguard-admins`) matches `rbac.groups`, `admin_groups`, `allowed_groups` and multi-monitor groups.
- Identity bindings store the issuer. After an IdP switch, the audit log shows the cause, and admins can reset all bindings deliberately (`DELETE /api/v1/access/identities?confirm=all`, UI button).
- Guide and example: `docs/KEYCLOAK.md`, `configs/master.keycloak.example.json`.

### Security

- **Guacamole account takeover via PocketID rename closed:** Guacamole usernames are bound to the immutable OIDC `sub` (`identity_bindings`, seeded from existing sessions). A different `sub` with the same name, or a renamed account, is rejected and audited. Reserved names (`guacadmin`, `administrator`, `root`) and usernames outside `username_pattern` (no Unicode look-alikes) are never accepted, and this is checked on every ForwardAuth request too. Admins can release bindings in the UI ("Identitätsbindungen").
- **Guacamole extension accepts header logins only:** Logins not authenticated through the `header` provider with a matching `X-Guacamole-User` are vetoed, so password logins such as `guacadmin/guacadmin` no longer work. Break-glass: `SESSIONGUARD_ENFORCE_HEADER_AUTH=false`.
- **Removing `guacadmin`:** `production/guacamole/harden-guacamole-db.sql` (idempotent) grants a real admin Guacamole administrator rights and deletes `guacadmin`. On new installations `guac-init` runs it automatically using `GUAC_ADMIN_USER`.
- **Admin sessions are server-side:** The opaque cookie replaces the HMAC cookie. Sessions can be revoked (UI "Admin-Sessions", `/api/v1/admin/sessions`), have an absolute lifetime (`oidc.session_hours`, default 8 h) and an idle timeout (`oidc.idle_timeout_minutes`, default 60). The group check runs on every request. New OIDC back-channel logout `POST /oidc/backchannel-logout`.
- **No internal error details to clients:** 500s and broker 503s return only a generic message plus `ref`; the details go to the server log under the same `ref`. Public endpoints (enroll/heartbeat/broker) also no longer reveal JSON parser details, and OIDC callback errors are only logged.
- **Admin-Login fail-closed:** Ist `oidc.admin_groups` leer, wird die Liste aus den `rbac.groups`-Schlüsseln abgeleitet. Ohne Gruppen startet der Master nur mit `oidc.allow_all_authenticated_users: true`. Gleiches gilt für den lokalen Agent-Login. Lese-APIs verlangen jetzt die Permission `view`.
- **Agent-Übernahme verhindert:** Re-Enrollment einer bekannten MachineID und eine Änderung des gemeldeten Hostnamens setzen den Server auf `maintenance`; erst nach Freigabe durch einen Admin wird er wieder gebrokert. Beides wird auditiert.
- **CSRF:** Zentrale Prüfung für alle cookie-authentifizierten POST/PUT/PATCH/DELETE-Anfragen: gleiche Origin über `Origin`/`Sec-Fetch-Site` und `Content-Type: application/json` für `/api/`. `httpx.SameOrigin` parst die Origin jetzt strikt.
- **Secrets:** Ein leeres, zu kurzes (< 24 Zeichen) oder als Platzhalter erkennbares (`SET-BY-…`, `CHANGE-THIS…`) `enrollment_token` bzw. `broker.api_key` verhindert den Start.
- **Login-DoS:** Die Pending-OIDC-States sind begrenzt, und die Session-Cleanup bei Logins wird gedrosselt.
- **Caddy:** Auf dem Guacamole-Host wird nur noch `/_sessionguard/auth/*` an den Master weitergeleitet; der Rest von `/_sessionguard/*` liefert 404. Vorher waren dort Admin-UI, Broker-API, Enrollment und `/metrics` öffentlich erreichbar.
- **EdgeGuard:** IPv6-Clients werden nach `/64` zusammengefasst (`ipv6_prefix_length`). Statt bei voller Tabelle mit 429 abzulehnen, wird der älteste Eintrag verdrängt; Bans werden nie verdrängt. Neue Rate-Limits für `/login` und `/auth/login`. Metrik `denied_capacity_total` ersetzt durch `state_evictions_total`, `bans_dropped_total` und `tracked_clients`.
- **Agent, Profile/Templates (LPE):** Restore, Backup und Templates arbeiten über `os.OpenRoot` und prüfen jede Pfadkomponente auf Symlinks und Junctions. Link-Ziele werden nicht überschrieben.
- **Agent `local_guard`:** Neuer Block in `agent.json` mit `allowed_store_roots`, `allowed_template_source_roots`, `allowed_profile_roots` und `protected_users`. Die Master-Policy kann ihn nicht überschreiben. Template-Quellen aus dem Agent-Config-/Datenverzeichnis, relative Pfade und UNC-Pfade (sofern nicht freigegeben) werden immer abgelehnt.
- **Agent-WebUI:** Nur noch `logoff`, `disconnect` und `message` sind erlaubt. `restart_server` respektiert `control_enabled`.
- **PowerShell-Quoting:** Typografische Anführungszeichen (U+2018–U+201B) werden mit escaped.
- **Agent:** `master_url` muss `https://` sein (Ausnahmen: localhost oder `insecure_master_url: true`).

### Upgrade-Hinweise

1. Master und Agents auf 0.6.0 aktualisieren und das Guacamole-Image neu bauen (`deploy/guacamole/Dockerfile.guacamole`).
2. Nur Benutzer in einer `rbac.groups`-Gruppe (oder in `oidc.admin_groups`) können sich noch an der Master-Console anmelden. Admins vorher prüfen.
3. Enrollment-Token und Broker-Key mindestens 24 Zeichen lang (`openssl rand -hex 32`).
4. Template-Quellen auf SMB-Freigaben in `local_guard.allowed_template_source_roots` eintragen.
5. Nach einer Agent-Neuinstallation (Re-Enrollment) den Server in der Master-UI wieder auf „online“ setzen.
6. Monitoring von `denied_capacity_total` auf `state_evictions_total` umstellen.
7. Für bestehende Datenbanken `guacadmin` entfernen (`production/guacamole/harden-guacamole-db.sql`, siehe `docs/ACCESS-AUTH.md`). In `production/sessionguard/.env` muss `GUAC_ADMIN_USER` gesetzt sein.
8. In PocketID das Bearbeiten des eigenen Kontos durch Benutzer deaktivieren. Die Back-Channel-Logout-URLs eintragen.
9. Nach legitimen Umbenennungen in PocketID die alte Identitätsbindung in der Master-UI freigeben.


## v0.5.2 Guacamole logout/recovery follow-up

- Fixed an over-aggressive Guacamole browser helper which treated every Guacamole `loggedOut` state as an explicit user logout.
- Explicit clicks on Guacamole logout actions still perform full SessionGuard/Pocket ID RP-initiated logout.
- Guacamole-only token loss (for example after a worker restart/failover) now preserves the SessionGuard/Pocket ID session and re-enters Guacamole through header authentication.

## v0.5.2 PKCE / logout hotfix

- Added PKCE S256 (`code_challenge` / `code_verifier`) to both SessionGuard OIDC authorization-code flows.
- Fixed the Go 1.22+ ServeMux root route conflict by registering `GET /{$}`.
- Added RP-initiated logout for the primary Director/Admin OIDC flow using the discovered `end_session_endpoint`.
- Guacamole access logout now sends `client_id` on every end-session request and `id_token_hint` when available.
- Added `oidc.logout_redirect_url` and documented Pocket ID Logout Callback URL requirements.


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
