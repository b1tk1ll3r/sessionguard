# Profile lifecycle

## Scope

SessionGuard synchronizes configured **folders inside an existing Windows profile**. It does not replace Windows profile loading and does not roam registry hives.

Do not configure the entire `C:\Users\username` profile root. In particular, do not use SessionGuard to copy:

- `NTUSER.DAT` / `UsrClass.dat`;
- registry hive transaction logs;
- Windows profile service metadata;
- profile container/VHD(X) files;
- EFS material that depends on user-specific key handling.

Use application/data directories whose consistency model is compatible with file copying after logoff and merging at logon.

## Configuration

```json
"profiles": {
  "enabled": true,
  "store_root": "\\\\fileserver\\SessionGuardProfiles",
  "backup_on_logoff": true,
  "restore_on_logon": true,
  "backup_delay_seconds": 5,
  "retry_seconds": 60,
  "restore_window_seconds": 120,
  "keep_versions": 2,
  "exclude_users": ["Administrator"],
  "exclude_sids": ["S-1-5-18", "S-1-5-19", "S-1-5-20"],
  "folders": [
    {
      "path": "AppData\\Roaming\\Example",
      "exclude_globs": ["Cache/**", "*.tmp"]
    },
    { "path": "Desktop" },
    { "path": "Documents" }
  ]
}
```

### `store_root`

May be a local path or UNC path. In a multi-server RDS farm it should normally be a resilient SMB location.

A user's data is keyed by SID, not username, so renamed accounts do not silently receive a second profile snapshot namespace.

### `backup_delay_seconds`

Delay after the session has disappeared before a backup job starts. Default: 5 seconds. This gives Windows and applications a short period to release profile file handles; locked files are then handled by the normal retry pipeline.

### `retry_seconds`

Backup retry interval and restore retry interval while the restore window is still open.

### `restore_window_seconds`

Maximum period, measured from the first restore job creation for a session, during which restore retries are allowed. Default: 120 seconds.

If the store is still unavailable after this window, SessionGuard marks the restore as abandoned, records the error, marks the session as past its restore phase and applies templates. It will not suddenly perform a late restore into a long-running session.

### `keep_versions`

Number of previous completed `current` snapshots retained under `history`. `0` keeps only `current`.

### `folders[].path`

Must be relative to the Windows user profile. Absolute paths, drive-qualified paths and `..` traversal are rejected.

### `exclude_globs`

Patterns are evaluated relative to that configured folder. Supported behavior:

- standard `path.Match`-style patterns such as `*.tmp`;
- `prefix/**` to exclude a complete subtree such as `Cache/**`.

## Backup semantics

1. Backup is queued after the previous session disappears and waits `backup_delay_seconds`.
2. Before copying, no other session with the same SID may be active.
3. Every configured folder is copied into a new staging tree.
4. Missing configured folders are accepted; different users may not have every application installed/initialized.
5. Symbolic links are not followed.
6. Regular files are copied through a temporary destination file before rename; replacement uses a rollback file when required by the platform.
7. A manifest is written after all configured folders complete.
8. Immediately before activation, the agent performs another WTS session check. If the SID became active while copying, activation is rejected and the previous `current` remains authoritative.
9. The old `current` is moved aside.
10. Staging is renamed to `current`.
11. Historical retention is pruned.
12. Only then is the backup considered successful.

If any required read/write operation fails, the staging tree is removed and the backup job is retried. Cleanup does not proceed.

## Restore semantics

Restore is a **merge** operation. Files present in the snapshot overwrite the corresponding local files. Files that exist locally but are absent from the snapshot are not deleted.

This is intentionally safer than mirroring/deleting at logon. An application may create new machine-specific files between deployments, and deleting them merely because they were absent in an older snapshot would be hazardous.

After restore succeeds (or no previous snapshot exists), templates are evaluated.

## Service startup safety

On service startup, sessions already present are treated as already past the restore phase. This prevents installing/upgrading SessionGuard during working hours from overwriting profile files in sessions that were opened before the service started.

## ACLs and NTFS metadata

The built-in Go copy engine copies regular file data and timestamps and creates destination files/directories under the service identity. It does not claim to reproduce the full NTFS security descriptor, owner, SACL, alternate data streams or every application-specific extended attribute.

For the intended selected-folder use case, destination files normally inherit ACLs from the existing user profile directories. Validate permissions with the applications you actually roam before production rollout.

If your requirement is byte-for-byte/full-metadata profile portability, use a profile-container or specialized profile-management product rather than expanding the selected-folder engine to the entire Windows profile.

## SMB identity

### LocalSystem

For remote SMB resources, a domain-joined server running as LocalSystem commonly authenticates as its machine account:

```text
DOMAIN\RDSHOST01$
```

Grant that account share and NTFS rights if you keep LocalSystem.

### gMSA

For farms, prefer a dedicated group Managed Service Account. Grant it:

- modify/create/delete rights under the SessionGuard profile share;
- local Full Control on `C:\ProgramData\SessionGuard`;
- "Log on as a service" as required by your domain policy.

The installation script supports a gMSA service identity.

## Failure examples

### Profile share unavailable on logoff

- backup job changes to error/retry state;
- local profile remains on disk;
- cleanup is blocked;
- master/local UI shows the job and error;
- when the share returns, backup retries and cleanup can continue.

### Profile share unavailable on logon

- restore retries during `restore_window_seconds`;
- after the window expires, SessionGuard logs the failure and stops retrying for that session;
- templates still run;
- the next real logon gets a fresh restore opportunity.

### User reconnects while backup is pending

If the same SID becomes active before the backup starts, the pending backup and cleanup are cancelled. SessionGuard avoids copying/deleting a profile that has become live again.

## Interaction with the v0.3 broker

Profile correctness is one reason the broker prefers an existing session and maintains a user lease. With `broker.single_session_per_user=true`, two nearly simultaneous Guacamole starts for the same normalized username are steered toward the same leased host whenever possible.

This is an additional coordination layer, not a distributed filesystem lock. The profile pipeline still performs its own WTS rechecks before snapshot activation and cleanup because users may reach RDS through emergency/direct paths outside the broker.

For selected-folder profile synchronization in a multi-host farm, keep the normal Guacamole path brokered and avoid exposing arbitrary direct RDP paths to regular users. Maintain any direct RDP route as an explicitly controlled break-glass path.
