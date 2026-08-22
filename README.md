# SessionGuard

SessionGuard is a Go-based management layer for Windows Remote Desktop Session Hosts. It is intended for environments that use Guacamole or another access gateway and want a small subset of the operational features commonly provided by Citrix management/profile components.

## Implemented MVP

- Windows service agent
- RDS/WTS session inventory
- delayed profile deletion after a real session disappears
- safety exclusions, allowed profile roots, retries, and dry-run mode
- per-user template enforcement for files, directories, `.url` links and `.lnk` shortcuts
- server basics: hostname, Windows version/build, uptime, RAM
- local agent dashboard
- Linux/Docker master dashboard for all agents
- outbound agent-to-master heartbeats
- bootstrap enrollment followed by per-agent bearer credentials
- per-agent policies and "apply to all agents"
- Pocket ID / generic OIDC authentication for master and local agent UI
- last-known policy continues to work if the master is unavailable

## Important safety note

Profile deletion is destructive. Start with `dry_run: true`, verify exclusions and `allowed_profile_roots`, test on a non-production RDS host, and only then disable dry-run. SessionGuard calls the Windows user-profile deletion API; it does not recursively delete arbitrary profile paths itself.

## Build

Requirements: Go 1.23+ and Internet access for the Go modules on the first build.

```powershell
.\scripts\build.ps1
```

Or:

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o sessionguard-agent.exe ./cmd/agent
go build -o sessionguard-master ./cmd/master
```

## Master deployment

1. Copy `configs/master.example.json` to `deploy/master.json` and edit it.
2. In Pocket ID create an OIDC client whose callback URL is `https://sessionguard.example.org/oidc/callback`.
3. Restrict the Pocket ID client to the intended admin group and configure the same group in `admin_groups`.
4. Put a TLS reverse proxy in front of the master.
5. Start with `docker compose -f deploy/docker-compose.yml up -d --build`.

The container binds the example host port only to `127.0.0.1`; publish it through your reverse proxy rather than exposing plain HTTP.

## Agent deployment

1. Copy `configs/agent.example.json` to `configs/agent.json` and edit the master URL, enrollment token and OIDC settings.
2. Create a Pocket ID OIDC client for the agent's management URL, for example `https://ts01-mgmt.example.org/oidc/callback`.
3. Build the Windows agent.
4. Run `scripts/install-agent.ps1` from an elevated PowerShell prompt.
5. Keep `dry_run: true` until profile cleanup has been observed successfully.

The service is installed as LocalSystem by default. If a template `source` points to a UNC share, grant read access to the server computer account (`DOMAIN\SERVER$`) or change the service identity to an appropriate gMSA/service account. Do not put share passwords in the SessionGuard JSON configuration.

## Example templates

```json
[
  {
    "id": "support",
    "kind": "url",
    "target": "Desktop\\Support.url",
    "url": "https://support.example.org",
    "overwrite": true
  },
  {
    "id": "erp",
    "kind": "shortcut",
    "target": "Desktop\\ERP.lnk",
    "overwrite": true,
    "shortcut": {
      "target": "C:\\Program Files\\ERP\\erp.exe",
      "arguments": "--terminal"
    }
  },
  {
    "id": "defaults",
    "kind": "file",
    "target": "AppData\\Roaming\\Example\\defaults.json",
    "source": "\\\\fileserver\\templates\\defaults.json",
    "overwrite": true
  }
]
```

All `target` values are relative to the user's profile. Attempts to escape the profile root are rejected.

## Local management during a master outage

The Windows agent continues cleanup and template work using its persisted policy. Its local web UI remains available independently of the master, provided Pocket ID is reachable. A locally saved emergency policy remains in effect until the master reconnects; if the master already has a different desired policy for that agent, the master policy is then reapplied.

## Pocket ID notes

SessionGuard requests the `openid`, `profile`, `email` and `groups` scopes. Use `admin_groups` as a second authorization check in addition to Pocket ID's client-side allowed-group restriction. The master and agent should be served over HTTPS and `secure_cookie` should remain enabled.

## What is intentionally not implemented yet

Full Citrix-style profile roaming/restoration is not part of v0.1. Copying an entire profile, especially `NTUSER.DAT` and registry-backed settings, after Windows has loaded that profile is unsafe. See `docs/ARCHITECTURE.md` for the recommended extension path.

## Repository layout

```text
cmd/master                 Linux/Docker master binary
cmd/agent                  Windows service binary
internal/agent             agent lifecycle, cleanup, heartbeat, local UI
internal/master            enrollment, dashboard, policy distribution
internal/windowsx          WTS, profile and Windows server APIs
internal/templates         template comparison/application
internal/auth              Pocket ID / OIDC login
configs                    example JSON configurations
deploy                     Docker Compose example
scripts                    build/install helpers
docs                       architecture notes
```
