# SessionGuard + Guacamole 1.6 integration

The `sessionguard-guacamole` extension does **not** authenticate users. It decorates the `UserContext` produced by Guacamole's existing providers and asks the SessionGuard broker for connection parameter tokens at connect time.

## 1. Build Guacamole with the extension

Use `Dockerfile.guacamole` instead of the stock image directly. It builds `guacamole-extension/` with Maven and copies the JAR to `/etc/guacamole/extensions/`.

The Guacamole container needs these additional environment variables:

```yaml
SESSIONGUARD_MASTER_URL: http://sessionguard-master:8080
SESSIONGUARD_BROKER_API_KEY: ${SESSIONGUARD_BROKER_API_KEY}
SESSIONGUARD_BROKER_TIMEOUT_MS: "2500"
```

Use a private Docker network between Guacamole and SessionGuard Master where possible. The broker API key is independent from the agent enrollment token and PocketID client secret.

## 2. Configure Guacamole RDP connections

For a brokered desktop connection set the RDP hostname to:

```text
${SESSIONGUARD_HOST}
```

For a brokered RemoteApp resource, these SessionGuard tokens can be used as connection parameters:

```text
hostname:        ${SESSIONGUARD_HOST}
remote-app:      ${SESSIONGUARD_REMOTE_APP}
remote-app-dir:  ${SESSIONGUARD_REMOTE_APP_DIR}
remote-app-args: ${SESSIONGUARD_REMOTE_APP_ARGS}
```

The extension identifies a SessionGuard resource by Guacamole connection ID or connection name. Create the corresponding mapping in SessionGuard under **Published Resources**. Unmapped Guacamole connections receive no SessionGuard tokens and remain untouched; this lets brokered and static/admin connections coexist.

## 3. Broker behavior

1. Existing matching RDS session in the requested farm wins (if reconnect is enabled).
2. Otherwise a valid SessionGuard user lease is reused.
3. Otherwise only online, `online`-mode farm members above `min_health_score` are considered.
4. Candidates are scored by health, active/disconnected sessions, CPU and memory pressure.
5. `drain` blocks new allocations but allows reconnects; `maintenance` blocks both.

With `single_session_per_user=true`, the lease key is global per username. With it disabled, leases are scoped by user + farm + resource.

## 4. Header-auth boundary

If Guacamole uses `HTTP_AUTH_HEADER=X-Guacamole-User`, untrusted traffic must not be able to reach Guacamole directly and supply that header. Keep the reverse proxy/header scrubber as the only trusted ingress path. Prefer a dedicated proxy network instead of sharing Guacamole's port with unrelated containers.
