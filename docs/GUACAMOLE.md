# Guacamole 1.6 Integration

## Design

The SessionGuard Guacamole extension is deliberately **not** an authentication provider in the identity sense. PocketID/header authentication stays responsible for user identity.

The extension subclasses Guacamole's authentication-provider extension point only to decorate the authenticated `UserContext` with a `TokenInjectingUserContext`. At connection time it sends the current Guacamole username plus connection identity to the SessionGuard broker and injects returned tokens.

This avoids:

- writing dynamic hostnames directly into the Guacamole PostgreSQL database;
- reimplementing PocketID authentication in SessionGuard;
- opening a management connection from Master to RDS hosts.

## Build

```bash
cd guacamole-extension
mvn -DskipTests package
```

Or use `deploy/guacamole/Dockerfile.guacamole`, which builds the JAR and copies it to the stock Guacamole extension directory.

## Required Guacamole environment

```yaml
SESSIONGUARD_MASTER_URL: http://sessionguard-master:8080
SESSIONGUARD_BROKER_API_KEY: ${SESSIONGUARD_BROKER_API_KEY}
SESSIONGUARD_BROKER_TIMEOUT_MS: "2500"
```

Optional (v0.6):

```yaml
# Veto every Guacamole login not authenticated via the header provider
# with a matching identity header (default true; false = break-glass only).
SESSIONGUARD_ENFORCE_HEADER_AUTH: "true"
# Defaults to HTTP_AUTH_HEADER, then X-Guacamole-User.
SESSIONGUARD_IDENTITY_HEADER: X-Guacamole-User
# Comma-separated auth provider identifiers allowed to authenticate users.
SESSIONGUARD_ALLOWED_AUTH_PROVIDERS: header,sessionguard-broker
# OIDC group sync (see docs/ACCESS-AUTH.md): authenticate from the trusted
# headers and pass X-Guacamole-Groups as effective Guacamole user groups.
SESSIONGUARD_HEADER_LOGIN: "true"
SESSIONGUARD_GROUPS_HEADER: X-Guacamole-Groups
EXTENSION_PRIORITY: sessionguard-broker
```

Use the same broker API key configured on the SessionGuard Master. The URL should preferably be an internal network URL, not the public Internet endpoint.

## Desktop connection

Configure the Guacamole RDP hostname as:

```text
${SESSIONGUARD_HOST}
```

The extension resolves the target host at connection time.

## RemoteApp

Use:

```text
hostname:        ${SESSIONGUARD_HOST}
remote-app:      ${SESSIONGUARD_REMOTE_APP}
remote-app-dir:  ${SESSIONGUARD_REMOTE_APP_DIR}
remote-app-args: ${SESSIONGUARD_REMOTE_APP_ARGS}
```

If your Agents report short Windows computer names and your DNS requires a suffix, a Guacamole hostname such as `${SESSIONGUARD_HOST}.example.org` is valid.

Create a matching Published Resource in SessionGuard by Guacamole connection ID or name. In v0.4 the Resource can optionally enable **Agent-managed RemoteApp publication** and specify the executable path. The Master then distributes the desired alias/path to every Agent in the farm. A managed RemoteApp is not brokered to a host until the Agent reports that it is present and in sync. The broker also aligns `${SESSIONGUARD_REMOTE_APP_ARGS}` with the Windows command-line policy (deny/allow/require).

## Multi-monitor (span)

The extension also ships `js/sessionguard-multimonitor.js`. For Resources with `multi_monitor` enabled it offers a small top-center bar in the Guacamole client view which opens the session in one window spanning several local screens; set the connection's `resize-method` to `display-update` so the RDP desktop follows the window size. Guacamole 1.6 has no native RDP multi-monitor support. See `docs/MULTI-MONITOR.md`.

## Header-auth security boundary

Your Guacamole configuration uses a trusted header such as `X-Guacamole-User`. That is safe only if untrusted requests cannot reach Guacamole directly with an attacker-controlled copy of that header.

Recommended topology:

```text
Internet
   |
Traefik + forward-auth/header scrubber
   |
dedicated trusted Guacamole proxy network
   |
Guacamole
   +--- private backend ---> guacd
   +--- private backend ---> PostgreSQL
   +--- private control ---> SessionGuard Master
```

Do not rely solely on the fact that the public Traefik router strips inbound identity headers if arbitrary unrelated containers can directly connect to Guacamole's port on a shared Docker network.

## Integration with the supplied compose

Your existing PocketID → traefik-forward-auth → Guacamole chain can remain unchanged. The required additions are:

1. build Guacamole using `deploy/guacamole/Dockerfile.guacamole`;
2. add the three `SESSIONGUARD_*` variables above;
3. give Guacamole private reachability to the SessionGuard Master;
4. add SessionGuard Published Resources matching your Guacamole connections;
5. replace fixed RDP hostnames in brokered connections with `${SESSIONGUARD_HOST}`.

`deploy/guacamole/docker-compose.sessionguard.example.yml` provides a focused example rather than replacing your complete existing stack.

## Failure behavior

If the SessionGuard broker is unavailable, the extension fails the brokered connection instead of choosing an arbitrary backend. This is intentional where reconnect affinity/profile concurrency matters. Existing already-established Guacamole/RDP sessions are unaffected.
