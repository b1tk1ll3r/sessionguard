# SessionGuard architecture

## Components

- **Agent (Windows service):** watches RDS/WTS sessions, applies user-profile templates, schedules profile cleanup, exposes a local management UI, and sends outbound heartbeats to the master.
- **Master (Linux/Docker):** receives enrollments and heartbeats, stores the latest server snapshots, provides a consolidated dashboard, and distributes per-agent or bulk policies.
- **Pocket ID:** authenticates administrators through OIDC. The master and each independently usable agent UI have their own callback URL.

## Connection model

Agents initiate HTTPS calls to the master. There is no requirement for the master to open an inbound management connection to a terminal server. Enrollment uses a bootstrap secret once; the master then returns an agent-specific bearer token and stores only its SHA-256 hash.

## Profile cleanup state machine

1. The agent polls WTS sessions.
2. A session that was present in the previous persisted snapshot and disappears is treated as logged off.
3. If no other session with the same SID exists, the profile is scheduled for cleanup after `grace_seconds`.
4. If the SID appears again before the deadline, cleanup is cancelled.
5. Immediately before deletion, the allowed-root rule and active-session rule are checked again.
6. Deletion uses the Windows `DeleteProfileW` API. Failures are retried.

The agent persists the previous session set and pending cleanup jobs so a service restart does not normally lose a logout transition.

## Template engine

Targets are always relative to the resolved user profile path. Supported types:

- `directory`: ensure a directory exists.
- `file`: write inline content/base64 content or copy a source file (including a UNC path).
- `url`: create an Internet Shortcut (`.url`).
- `shortcut`: create or update a Windows Shell Link (`.lnk`) and compare its key properties before changing it.

Templates are evaluated on a newly observed user session and again when a policy revision changes.

## Policy precedence

- The agent starts with its local configured/persisted policy.
- A master policy for an agent becomes authoritative once received.
- If the master is unavailable, the last policy remains active and can be edited locally.
- When the master reconnects and still has a different desired policy, the master's policy wins.

## Deliberate non-goal in v0.1: full roaming-profile replacement

A complete restore of a Windows user profile from a share is not implemented. Restoring `NTUSER.DAT`, registry state, and profile files after the Windows profile has already been loaded is race-prone and can corrupt state. Citrix Profile Management operates much deeper in the logon/logoff lifecycle than a normal post-logon service loop.

A future profile provider should therefore either:

1. synchronize only explicitly selected user-data directories, or
2. integrate with a supported pre-profile-load mechanism / profile-container technology.

The current design keeps this concern separate from cleanup and template enforcement rather than pretending that copying a profile directory after logon is equivalent.
