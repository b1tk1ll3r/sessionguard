# SessionGuard Access Auth (Guacamole ForwardAuth)

SessionGuard protects Apache Guacamole through the ForwardAuth mechanism of the reverse proxy (Caddy `forward_auth`, Traefik ForwardAuth). The feature lives inside `sessionguard-master`; no separate authentication container is required. Any OpenID Connect provider works. The reference deployment uses **Caddy Sentinel** as reverse proxy *and* OIDC provider – see [SENTINEL.md](SENTINEL.md) for the complete setup.

## Why this exists

Guacamole header authentication trusts the username supplied by the reverse proxy. A stale Guacamole token or a separate proxy login cookie must therefore never be sufficient to bypass the identity provider. SessionGuard Access Auth adds a server-side browser session which the proxy verifies before every HTTP request is sent to Guacamole.

```text
Browser -> reverse proxy -> SessionGuard /auth/verify -> Guacamole
                                  |
                                  +-> OIDC login/logout at the IdP (e.g. Caddy Sentinel)
```

Only a valid SessionGuard access session causes `/auth/verify` to return HTTP 200 and `X-Guacamole-User`. Missing, expired, revoked or no longer revalidated sessions are redirected to the IdP. SessionGuard never invents a fallback username.

## Recommended public URL layout

```text
https://sessionguard.example.org/                  -> SessionGuard admin UI and agent API
https://rds.example.org/                           -> Guacamole
https://rds.example.org/_sessionguard/auth/*       -> SessionGuard Master /auth/*
```

This keeps the access cookie host-only on the Guacamole hostname and avoids cross-domain cookie problems.

## Master configuration

Use a separate OIDC client for Guacamole access. Blank `issuer`, `client_id`, `client_secret`, `scopes` and `groups_claims` are inherited from the main `oidc` block.

```json
"access_auth": {
  "enabled": true,
  "issuer": "https://sso.example.org",
  "client_id": "sessionguard-access",
  "client_secret": "SET-BY-SESSIONGUARD_ACCESS_OIDC_CLIENT_SECRET",
  "redirect_url": "https://rds.example.org/_sessionguard/auth/oidc/callback",
  "logout_redirect_url": "https://rds.example.org/",
  "cookie_name": "sg_access_session",
  "cookie_domain": "",
  "secure_cookie": true,
  "session_hours": 10,
  "revalidate_minutes": 5,
  "scopes": ["openid", "profile", "email", "groups", "offline_access"],
  "username_claim": "preferred_username",
  "allowed_groups": ["RDS-Benutzer"],
  "allowed_hosts": ["rds.example.org"]
}
```

`cookie_domain` should normally remain empty: a host-only cookie keeps unrelated subdomains from receiving the access-session token.

The browser cookie contains only a cryptographically random opaque token; SessionGuard stores its SHA-256 hash as lookup key. Sessions are persisted in the Master control-plane store, so a Master restart does not silently re-authorize a user from a Guacamole token alone.

## OIDC client at the IdP

| Setting | Value |
|---|---|
| Client type | confidential (with secret), PKCE S256 |
| Redirect URI | `https://rds.example.org/_sessionguard/auth/oidc/callback` |
| Post-logout redirect URI | `https://rds.example.org/` |
| Refresh tokens | on, when `revalidate_minutes` is used (scope `offline_access`) |
| Back-channel logout URI (if the IdP supports it) | `https://rds.example.org/_sessionguard/auth/backchannel-logout` |

For browser-initiated logout SessionGuard uses the discovered `end_session_endpoint`, removes the local session first, sends `client_id` plus an `id_token_hint` and returns to `logout_redirect_url`.

## Session revalidation (`revalidate_minutes`)

IdPs without OIDC back-channel logout – Caddy Sentinel among them – cannot tell SessionGuard that a user was signed out or locked. With `revalidate_minutes` (access auth and admin console, `oidc.revalidate_minutes`) the Master renews every session with its refresh token after that interval:

- The IdP re-checks the account, its login policy and the application rules on every refresh. A refused refresh (`invalid_grant`), a different subject or username, or groups that are no longer allowed end the session at once; the next request goes to the login.
- Groups, e-mail and name are taken from the refreshed ID token, so `X-Guacamole-Groups` follows group changes the IdP reports.
- Only one refresh per session runs at a time and the stored refresh token is re-read before each refresh: IdPs with refresh-token rotation (Sentinel) end the whole grant if a used token comes back.
- An unreachable IdP is tolerated for up to twice the interval (retries at most every 30 s), then the session is denied – fail closed, but without deleting it.
- With revalidation enabled, a login whose token response contains no refresh token is refused with a clear log message; sessions from before the switch end at their first revalidation.
- `offline_access` is added to the requested scopes automatically. The refresh token never leaves the Master and is useless without the client secret.

`0` (default) disables revalidation. Allowed range: 0–1440.

## Reverse proxy

The auth path MUST NOT itself use ForwardAuth, otherwise login/callback/logout create a redirect loop. Before ForwardAuth, strip every client-supplied identity header; after ForwardAuth, copy only SessionGuard's response headers to Guacamole and add the proxy secret (below).

### Caddy / Caddy Sentinel

See `deploy/Caddyfile.sentinel`. Keep `request_header`, `forward_auth` and `reverse_proxy` inside a `route` block – without it Caddy sorts `request_header` after `forward_auth` and deletes the identity it just copied.

### Traefik

Route `Host(rds.example.org) && PathPrefix(/_sessionguard/auth)` with a `stripprefix` middleware (`/_sessionguard`) and a high priority to the Master, without ForwardAuth. On the Guacamole router chain a header scrubber (`customrequestheaders` set to empty for `X-Guacamole-User`, `X-Guacamole-Groups`, `X-SessionGuard-*`) before a `forwardauth` middleware with `address=http://sessionguard-master:8080/auth/verify` and `authResponseHeaders=X-Guacamole-User,X-Guacamole-Groups,X-SessionGuard-User,X-SessionGuard-Email,X-SessionGuard-Groups`, then set `X-SessionGuard-Proxy-Auth` with a headers middleware.

## Guacamole

```yaml
HTTP_AUTH_ENABLED: "true"
HTTP_AUTH_HEADER: X-Guacamole-User
SESSIONGUARD_HEADER_LOGIN: "true"          # extension authenticates from the trusted headers
EXTENSION_PRIORITY: sessionguard-broker   # before guacamole-auth-header (which knows no groups)
SESSIONGUARD_PROXY_SECRET: <openssl rand -hex 32>
POSTGRESQL_AUTO_CREATE_ACCOUNTS: "true"
```

### Proxy secret (v0.7)

With `SESSIONGUARD_PROXY_SECRET` the extension trusts identity headers only on requests that also carry this secret in `X-SessionGuard-Proxy-Auth` (name configurable with `SESSIONGUARD_PROXY_SECRET_HEADER`). The reverse proxy adds it after ForwardAuth and strips any client copy. A process that reaches Guacamole directly – bypassing the proxy, e.g. through the published port on the host – cannot log in as anybody by sending `X-Guacamole-User` itself. Requests with a Guacamole token but without a valid identity now end that Guacamole session (fail closed). Without the secret the extension logs a warning at startup and behaves as before; the proxy must then be the only network path to Guacamole.

The extension also contains a small plain-JavaScript helper:

- every 30 seconds it checks `/_sessionguard/auth/status`;
- if the access session has been revoked, expired or ended by revalidation, it navigates away from Guacamole, closing tunnels/WebSockets;
- an explicit click on a Guacamole logout action redirects to `/_sessionguard/auth/logout`, which also ends the IdP session; generic Guacamole token loss/failover does not trigger IdP logout.

Broker answers are filtered: only `SESSIONGUARD_*` tokens reach the connection, so a broker response can never override Guacamole tokens such as `GUAC_USERNAME`.

## Expected behavior

- No SessionGuard access cookie: Guacamole request redirects to the IdP.
- Valid access session: Guacamole receives exactly the IdP username through `X-Guacamole-User`.
- Session older than `session_hours`: denied even if Guacamole still holds an old auth token.
- Account signed out or locked at the IdP: with back-channel logout immediately, otherwise at the next revalidation; the browser-side poll closes an open Guacamole page within about 30 seconds.
- Guacamole logout button: an explicit user click performs full SessionGuard/IdP logout. If Guacamole merely loses its local token (worker failover/restart), SessionGuard keeps the access session and re-enters Guacamole through header auth.

## Identity protection

The username emitted as `X-Guacamole-User` *is* the Guacamole account. SessionGuard therefore enforces:

| Control | Behavior |
|---|---|
| **Identity binding** | On first login a username is permanently bound to the immutable OIDC `sub` (and issuer). A different `sub` using the same username (case-insensitive) is rejected, and so is a bound `sub` that shows up with a new username. Conflicts are audited as `identity_binding_conflict`. |
| **Reserved usernames** | `access_auth.reserved_usernames` (default `guacadmin`, `administrator`, `root`) can never be used. The check runs on every ForwardAuth request. |
| **Username pattern** | `access_auth.username_pattern` (default `^[A-Za-z0-9][A-Za-z0-9._@-]{0,63}$`) rejects Unicode look-alikes, whitespace and control characters. |
| **Header-only Guacamole logins** | The extension vetoes every login that was not authenticated through the SessionGuard header (and, with a proxy secret, the trusted proxy). JDBC username/password logins (e.g. `guacadmin`/`guacadmin`) are rejected. Break-glass: `SESSIONGUARD_ENFORCE_HEADER_AUTH=false`. |

After a **legitimate rename** at the IdP, or after **switching the IdP** (new issuer, new subjects – e.g. Pocket ID → Caddy Sentinel), an admin releases the bindings in the Master console under *Access-Sessions → Identity bindings* (single user or "reset all"). The next login binds the new subject. Guacamole treats a new username as a new account, so assign permissions to Guacamole groups rather than to individual users.

### Remove `guacadmin`

`initdb.sh` creates `guacadmin` with the password `guacadmin`. Grant a real admin Guacamole administrator rights and delete the default account:

```bash
docker compose exec -T guacamole-postgres psql -U guacamole_user -d guacamole_db \
  -v ON_ERROR_STOP=1 -v admin_user=YOUR_IDP_USERNAME \
  < deploy/guacamole/harden-guacamole-db.sql
```

The script is idempotent and creates the admin account if it has never logged in. For **new** installations `guac-init` runs it automatically with `GUAC_ADMIN_USER` from `.env`.

## Admin console sessions

Director/admin logins are server-side sessions (opaque cookie, only the SHA-256 is kept in memory):

- `oidc.session_hours` (default 8) is the absolute lifetime and `oidc.idle_timeout_minutes` (default 60) the inactivity timeout; `oidc.revalidate_minutes` renews them at the IdP as described above.
- Group membership is re-checked against `admin_groups` on every request (and refreshed from the IdP with revalidation).
- Logout, the *Admin sessions* panel (`GET/DELETE /api/v1/admin/sessions`) and back-channel logout end a session immediately.
- A Master restart ends all admin sessions; users simply log in again through SSO.

## OIDC groups → Guacamole groups

With group sync, membership no longer has to be maintained in Guacamole: a user in IdP group `guac-sage-users` automatically gets the permissions of the Guacamole user group with the mapped name.

1. At login (and at every revalidation) the access session stores the IdP groups (`groups_claims`). Caddy Sentinel sends the CN names of AD groups in `groups`.
2. ForwardAuth emits `X-Guacamole-Groups`: the groups after mapping, percent-encoded and comma-separated.
3. With `SESSIONGUARD_HEADER_LOGIN=true` the extension reports these groups as *effective user groups*; the JDBC extension matches them by name against its user groups.

```json
"access_auth": {
  "guacamole_groups": {
    "enabled": true,
    "prefix": "guac-",
    "strip_prefix": true,
    "map": { "RDS-Admins": "RDS Admins" },
    "exclude": ["guac-legacy"]
  }
}
```

| Field | Meaning |
|---|---|
| `enabled` | Emit `X-Guacamole-Groups` (always, even if empty, so a forged client value is overwritten). |
| `prefix` | Only pass groups starting with this prefix (case-insensitive). Empty = all groups. Recommended so unrelated IdP groups cannot collide with Guacamole group names. |
| `strip_prefix` | `guac-sage-users` → Guacamole group `sage-users`. |
| `map` | Explicit renames IdP → Guacamole; mapped groups bypass `prefix`. |
| `exclude` | Never pass these groups. |

A leading `/` (Keycloak "Full group path") is always removed. The resulting groups per user are visible in the Master console under *Access-Sessions → Guacamole-Gruppen* and at `/_sessionguard/auth/status`.

### Security notes

- `X-Guacamole-Groups` grants permissions. The proxy **must** strip it from client requests and copy it only from the ForwardAuth response; use the proxy secret so Guacamole rejects headers from any other path.
- Whoever controls an IdP group controls the Guacamole permissions of the matching Guacamole group, including `ADMINISTER`. Use `prefix`.
- Without revalidation or back-channel logout, groups stay fixed for the access session (`session_hours`). Caddy Sentinel takes AD groups from the user's last *Sentinel* login; a change in AD reaches SessionGuard after the user signs in at Sentinel again (or after an admin signs the account out there).
- If the identity of a browser changes while Guacamole still holds a token of the previous user, the extension invalidates that Guacamole session.
