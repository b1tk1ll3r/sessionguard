# Agent-managed RemoteApps (v0.4)

SessionGuard v0.4 can maintain the local Windows RemoteApp registration on every RD Session Host that belongs to a Resource farm. The Master carries the desired state; the Agent reconciles it locally through the Terminal Services WMI provider and reports observed readiness back to the broker.

## Safety model

- No inbound WMI/WinRM connection from Master to Windows is introduced.
- The Windows Agent performs all WMI calls locally.
- Existing unrelated RemoteApps are discovered but not modified.
- If a pre-existing alias is explicitly put under SessionGuard management, SessionGuard may update that registration to the configured desired state, but records it as **adopted** rather than owned.
- Automatic removal is limited to aliases that the Agent itself originally created and persisted as owned.
- A managed RemoteApp is brokered fail-closed per host until the Agent reports: executable exists, registration exists, desired state is in sync, and no reconciliation error is present.

## Windows prerequisite / canary check

Run on one RD Session Host in an elevated Windows PowerShell:

```powershell
Get-WmiObject `
  -Namespace 'root\cimv2\TerminalServices' `
  -Class Win32_TSPublishedApplication `
  -Authentication PacketPrivacy |
  Select-Object Alias,Path,VPath,PathExists,CommandLineSetting,RequiredCommandLine,ShowInPortal
```

An empty result is valid. A class/provider error means the RemoteApp WMI provider is not available on this server and Agent-managed publication must not yet be enabled there.

The Agent service must run with administrative/local-system privileges because Windows requires administrative rights to change these WMI objects.

## Create the Resource in Master

Open **Apps & Desktops** and create/edit a Resource:

```text
Name:                          Sage 100
Type:                          RemoteApp
Farm:                          ERP
Guacamole Connection:          Sage 100
RemoteApp Alias:               ||Sage
Agent-managed publication:     enabled
Executable:                    C:\Program Files\Sage\Sage.exe
Icon path:                     optional
Icon index:                    0
Command-line policy:           deny / allow / require
Required arguments:            only for require
Show in RD Web Access:         optional
```

The managed alias must be globally unique across enabled SessionGuard-managed Resources. This prevents collisions when a Windows Agent belongs to multiple farms.

### Command-line policy

- `deny (0)`: Windows disallows RemoteApp command-line arguments and SessionGuard sends an empty `${SESSIONGUARD_REMOTE_APP_ARGS}` token.
- `allow (1)`: Windows permits arguments and SessionGuard sends the Resource's Guacamole arguments.
- `require (2)`: Windows requires the configured arguments and SessionGuard forces those same required arguments into `${SESSIONGUARD_REMOTE_APP_ARGS}`.

## Agent reconciliation

The desired Resource is sent in the normal outbound heartbeat only to members of its farm. The Agent reconciles immediately when desired state changes and then periodically (60 seconds) from persisted local desired state, including during a temporary Master outage.

The local Agent UI contains **RemoteApps**. The Master server detail page also contains the observed inventory.

Typical states:

```text
Ready              published=yes, path_exists=yes, in_sync=yes
Not ready          executable missing / WMI publication failed / mismatch
SessionGuard        currently managed desired state
  (created)         registration was originally created by SessionGuard
  (adopted)         registration existed before SessionGuard took it over
locally discovered  visible only; not modified by SessionGuard
```

The Master Resource table shows `ready/total`, for example `3/3 ready`. The broker excludes only the non-ready hosts for this particular managed application; other desktops/resources can still use those hosts if their own health/rules allow it.

## Guacamole

Create one logical Guacamole RDP connection for the application:

```text
hostname:        ${SESSIONGUARD_HOST}.stadt-hilden.de
remote-app:      ${SESSIONGUARD_REMOTE_APP}
remote-app-dir:  ${SESSIONGUARD_REMOTE_APP_DIR}
remote-app-args: ${SESSIONGUARD_REMOTE_APP_ARGS}
```

Map its Guacamole connection ID (preferred) or connection name to the SessionGuard Resource.

## Upgrade order

v0.4 uses Agent protocol version 4. Upgrade Master and Agents as a coordinated rollout. Until an Agent is upgraded, a v0.4 Master will reject its old protocol heartbeat. For a production farm, update one canary Agent first, verify local RemoteApp status, and then roll out the remaining hosts quickly within the maintenance window.

## Rollback behavior

If you disable Agent-managed publication or delete the Resource:

- SessionGuard-created aliases are removed on the Agent after it receives the new desired state.
- Adopted/pre-existing aliases remain locally published.
- Unrelated manually published RemoteApps are never part of SessionGuard cleanup.

If an Agent loses its local ownership state, SessionGuard errs on the safe side and will not infer ownership merely from the alias; a stale registration may remain and can be removed manually.

## Microsoft provider reference

SessionGuard uses Microsoft's documented `Win32_TSPublishedApplication` / `Win32_TSPublishedApplicationList` provider in `Root\CIMv2\TerminalServices` with packet-privacy authentication. This feature manages the local RDSH RemoteApp provider; it does not attempt to reproduce every RD Connection Broker/Collection management semantic.
