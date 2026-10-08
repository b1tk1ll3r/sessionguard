# SessionGuard Access Auth (Guacamole ForwardAuth)

SessionGuard 0.5.0 can protect Apache Guacamole directly through Traefik ForwardAuth. The feature lives inside `sessionguard-master`; no separate authentication container is required.

## Why this exists

Guacamole header authentication trusts the username supplied by the reverse proxy. A stale Guacamole token or a separate proxy login cookie must therefore never be sufficient to bypass the identity provider. SessionGuard Access Auth adds a server-side browser session which Traefik verifies before every HTTP request is sent to Guacamole.

The flow is:

```text
Browser -> Traefik -> SessionGuard /auth/verify -> Guacamole
                         |
                         +-> PocketID OIDC login/logout
```

Only a valid SessionGuard access session causes `/auth/verify` to return HTTP 200 and `X-Guacamole-User`. Missing, expired or revoked sessions are redirected to PocketID. SessionGuard never invents a fallback username.

## Recommended public URL layout

Keep the SessionGuard administration UI on its existing hostname, but route a small authentication path on the Guacamole hostname to the same Master container:

```text
https://sessionguard.example.org/                  -> SessionGuard admin UI
https://guacamole.example.org/                     -> Guacamole
https://guacamole.example.org/_sessionguard/auth/* -> SessionGuard Master /auth/*
```

This keeps the access cookie host-only on the Guacamole hostname. It also avoids cross-domain cookie problems.

## Master configuration

Create a separate PocketID client for Guacamole access where practical. The issuer may be inherited from the main `oidc` block, while the client ID/secret can be independent.

```json
"access_auth": {
  "enabled": true,
  "issuer": "https://id.example.org",
  "client_id": "POCKETID-GUACAMOLE-CLIENT-ID",
  "client_secret": "SET-BY-SESSIONGUARD_ACCESS_OIDC_CLIENT_SECRET",
  "redirect_url": "https://guacamole.example.org/_sessionguard/auth/oidc/callback",
  "logout_redirect_url": "https://guacamole.example.org/",
  "cookie_name": "sg_access_session",
  "cookie_domain": "",
  "secure_cookie": true,
  "session_hours": 8,
  "username_claim": "preferred_username",
  "allowed_groups": ["guacamole-users"],
  "allowed_hosts": ["guacamole.example.org"]
}
```

`cookie_domain` should normally remain empty. This creates a host-only cookie and prevents unrelated subdomains from receiving the access-session token.

The browser cookie contains only a cryptographically-random opaque token. SessionGuard stores only its SHA-256 hash as the lookup key. Sessions are persisted in the existing Master control-plane store, so a Master restart does not silently re-authorize a user from a Guacamole token alone.

## PocketID client

Configure the PocketID client with:

```text
Public client: OFF
PKCE: ON
Callback URL:
https://guacamole.example.org/_sessionguard/auth/oidc/callback

Logout Callback URL (post_logout_redirect_uri):
https://guacamole.example.org/
```

The Pocket ID field **Logout Callback URLs** is the allow-list for `post_logout_redirect_uri`; do not put the SessionGuard back-channel endpoint into that field. If the provider exposes a dedicated Back-Channel Logout URI setting, SessionGuard's endpoint is `https://guacamole.example.org/_sessionguard/auth/backchannel-logout`.

For browser-initiated logout SessionGuard uses the `end_session_endpoint` discovered from PocketID, removes the local session first, sends `client_id` plus an `id_token_hint` when available, and then returns to `logout_redirect_url`.

## Traefik

The auth path MUST NOT itself use ForwardAuth, otherwise login/callback/logout create a redirect loop.

Example labels for the SessionGuard Master (adapt router names/network/TLS resolver to your deployment):

```yaml
labels:
  - traefik.enable=true
  - traefik.http.routers.sg-guac-auth.rule=Host(`guacamole.example.org`) && PathPrefix(`/_sessionguard/auth`)
  - traefik.http.routers.sg-guac-auth.entrypoints=websecure
  - traefik.http.routers.sg-guac-auth.tls=true
  - traefik.http.routers.sg-guac-auth.priority=200
  - traefik.http.routers.sg-guac-auth.service=sessionguard-master
  - traefik.http.routers.sg-guac-auth.middlewares=sg-guac-auth-strip
  - traefik.http.middlewares.sg-guac-auth-strip.stripprefix.prefixes=/_sessionguard
  - traefik.http.services.sessionguard-master.loadbalancer.server.port=8080
```

On the normal Guacamole router, replace the old `traefik-forward-auth` middleware with two middlewares in this order:

```yaml
labels:
  # Never trust identity headers supplied by a browser/client.
  - traefik.http.middlewares.guac-id-scrub.headers.customrequestheaders.X-Guacamole-User=
  - traefik.http.middlewares.guac-id-scrub.headers.customrequestheaders.X-SessionGuard-User=
  - traefik.http.middlewares.guac-id-scrub.headers.customrequestheaders.X-SessionGuard-Email=
  - traefik.http.middlewares.guac-id-scrub.headers.customrequestheaders.X-SessionGuard-Groups=
  - traefik.http.middlewares.guac-id-scrub.headers.customrequestheaders.X-Guacamole-Groups=

  - traefik.http.middlewares.guac-sg-auth.forwardauth.address=http://sessionguard-master:8080/auth/verify
  - traefik.http.middlewares.guac-sg-auth.forwardauth.authResponseHeaders=X-Guacamole-User,X-SessionGuard-User,X-SessionGuard-Email,X-SessionGuard-Groups,X-Guacamole-Groups

  # Add these to the existing Guacamole middleware chain, in this order.
  - traefik.http.routers.guacamole.middlewares=guac-id-scrub,guac-sg-auth
```

Traefik copies `authResponseHeaders` from SessionGuard onto the upstream request and replaces conflicting values. The preceding header middleware removes untrusted client-supplied identity headers before authentication.

Both Guacamole and SessionGuard Master must share a Docker network on which the DNS name `sessionguard-master` resolves.

## Guacamole

Keep Guacamole header authentication enabled:

```text
HTTP_AUTH_HEADER=X-Guacamole-User
```

Use the SessionGuard-built Guacamole image from 0.5.0 or later. The extension contains a small plain-JavaScript helper (no npm/Node runtime):

- every 30 seconds it checks `/_sessionguard/auth/status`;
- if the server-side SessionGuard access session has been revoked/expired, it navigates away from Guacamole, closing browser tunnels/WebSockets;
- an explicit click on a Guacamole logout action redirects to `/_sessionguard/auth/logout`, which also ends the SessionGuard/PocketID session; generic Guacamole token loss/failover does not trigger IdP logout.

## Migration from traefik-forward-auth

Do not run both auth middlewares on the Guacamole router. A safe migration is:

1. Deploy SessionGuard 0.5.0 and enable `access_auth`.
2. Add the high-priority `/_sessionguard/auth` router.
3. Test `https://guacamole.example.org/_sessionguard/auth/status` (401 while logged out is correct).
4. Replace the Guacamole router's old ForwardAuth middleware with `guac-id-scrub,guac-sg-auth`.
5. Rebuild/redeploy the SessionGuard Guacamole image so its 0.5.0 extension is loaded.
6. After successful tests, remove the old `traefik-forward-auth` service and its cookies/configuration.

## Expected behavior

- No SessionGuard access cookie: Guacamole request redirects to PocketID.
- Valid access session: Guacamole receives exactly the PocketID username through `X-Guacamole-User`.
- Session older than `session_hours`: denied even if Guacamole still holds an old auth token.
- PocketID back-channel logout: corresponding SessionGuard sessions are revoked immediately; the browser-side poll closes an already-open Guacamole page within about 30 seconds.
- Guacamole logout button: an explicit user click performs full SessionGuard/PocketID OIDC logout. If Guacamole merely loses its local token (for example after worker failover/restart), SessionGuard keeps the upstream access session and re-enters Guacamole through header auth.

## Identity protection (v0.6)

The username emitted as `X-Guacamole-User` *is* the Guacamole account. Because Pocket ID usernames are mutable, SessionGuard additionally enforces:

| Control | Behavior |
|---|---|
| **Identity binding** | On first login a username is permanently bound to the immutable OIDC `sub`. A different `sub` using the same username (case-insensitive) is rejected, and so is a bound `sub` that shows up with a new username (IdP-side rename). Conflicts are audited as `identity_binding_conflict`. On upgrade, bindings are seeded from the existing access sessions (oldest first). |
| **Reserved usernames** | `access_auth.reserved_usernames` (default `guacadmin`, `administrator`, `root`) can never be used. The check also runs on every ForwardAuth request, so existing sessions stop working immediately. |
| **Username pattern** | `access_auth.username_pattern` (default `^[A-Za-z0-9][A-Za-z0-9._@-]{0,63}$`) rejects Unicode look-alikes (e.g. a Cyrillic `а` in `guаcadmin`), whitespace and control characters. |
| **Header-only Guacamole logins** | The SessionGuard Guacamole extension vetoes every login that was not authenticated by the `header` provider with a matching identity header. Username/password logins against the JDBC database (e.g. `guacadmin`/`guacadmin` via `POST /api/tokens`) are therefore rejected even for users who passed ForwardAuth. Disable only for break-glass with `SESSIONGUARD_ENFORCE_HEADER_AUTH=false`. |

After a **legitimate rename** in Pocket ID, an admin releases the old binding in the Master console under *Access-Sessions → Identity bindings* (or `DELETE /api/v1/access/identities/{username}`). This also ends the user's access sessions; the next login binds the new name. Note that Guacamole treats the new name as a new account, so connection permissions must be granted again (or assigned to Guacamole groups in the first place).

### Pocket ID

- In the Pocket ID admin UI under *Application Configuration*, disable the option that lets users **edit their own account details** (wording depends on the Pocket ID version). SessionGuard's binding stops the takeover even if this stays enabled, but a self-rename then locks the user out until an admin releases the binding.
- Register the back-channel logout endpoints if your Pocket ID version supports them:
  - Guacamole access client: `https://<guac-host>/_sessionguard/auth/backchannel-logout`
  - SessionGuard admin client: `https://<sessionguard-host>/oidc/backchannel-logout`

### Remove `guacadmin`

`initdb.sh` creates `guacadmin` with the password `guacadmin`. Grant a real admin Guacamole administrator rights and delete the default account:

```bash
docker exec -i guacamole-postgres psql -U guacamole_user -d guacamole_db \
  -v ON_ERROR_STOP=1 -v admin_user=YOUR_POCKETID_USERNAME \
  < production/guacamole/harden-guacamole-db.sql
```

The script is idempotent, creates the admin account if it has never logged in, and lists the remaining Guacamole administrators. For **new** installations, `guac-init` runs it automatically as `002-sessionguard-hardening.sql` with `GUAC_ADMIN_USER` from `.env`.

## Admin console sessions (v0.6)

Director/admin logins are server-side sessions (opaque cookie, only the SHA-256 is kept in memory):

- `oidc.session_hours` (default 8) is the absolute lifetime and `oidc.idle_timeout_minutes` (default 60) the inactivity timeout.
- Group membership is re-checked against `admin_groups` on every request.
- Logout, the *Admin sessions* panel (`GET/DELETE /api/v1/admin/sessions`) and Pocket ID back-channel logout end a session immediately.
- A Master restart ends all admin sessions; users simply log in again through Pocket ID SSO.

## OIDC groups → Guacamole groups (v0.6)

Guacamole permissions are typically assigned to Guacamole **user groups**. With group sync, membership no longer has to be maintained in Guacamole: a user who is in OIDC group `sage-users` automatically gets the permissions of the Guacamole user group `sage-users` (exact name, case-sensitive by default).

How it works:

1. At login, the access session stores the OIDC groups (`groups_claims`, e.g. `groups` or `realm_access.roles`).
2. ForwardAuth (`/auth/verify`) emits `X-Guacamole-Groups`: the groups after mapping (below), percent-encoded and comma-separated (umlauts and commas survive).
3. With `SESSIONGUARD_HEADER_LOGIN=true` the SessionGuard Guacamole extension authenticates the user from `X-Guacamole-User` (like `guacamole-auth-header`) and reports these groups as *effective user groups*.
4. The Guacamole JDBC extension matches effective groups by name against its user groups and applies their connection and system permissions. Groups that do not exist in Guacamole are ignored.

### Master configuration

```json
"access_auth": {
  "guacamole_groups": {
    "enabled": true,
    "prefix": "guac-",
    "strip_prefix": true,
    "map": { "/IT/RDS-Admins": "RDS Admins" },
    "exclude": ["guac-legacy"]
  }
}
```

| Field | Meaning |
|---|---|
| `enabled` | Emit `X-Guacamole-Groups` (always, even if empty, so a forged client value is overwritten). |
| `prefix` | Only pass groups starting with this prefix (case-insensitive). Empty = all groups. Recommended, e.g. `guac-`, so that only groups intended for Guacamole are considered. |
| `strip_prefix` | `guac-sage-users` → Guacamole group `sage-users`. |
| `map` | Explicit renames OIDC → Guacamole; mapped groups bypass `prefix`. |
| `exclude` | Never pass these groups. |

A leading `/` (Keycloak "Full group path") is always removed. The resulting groups per user are visible in the Master console under *Access-Sessions → Guacamole-Gruppen* and for the user at `/_sessionguard/auth/status`.

### Guacamole configuration

```yaml
SESSIONGUARD_HEADER_LOGIN: "true"          # extension authenticates from the trusted headers
EXTENSION_PRIORITY: sessionguard-broker   # must run before guacamole-auth-header (which knows no groups)
HTTP_AUTH_ENABLED: "true"                 # may stay as fallback
POSTGRESQL_AUTO_CREATE_ACCOUNTS: "true"
```

Create the user groups in Guacamole (*Settings → Groups*) with exactly the mapped names and assign connections/permissions to the groups. Manual memberships are no longer needed; existing manual memberships keep working in addition.

### Security notes

- `X-Guacamole-Groups` grants permissions. The reverse proxy **must** strip it from client requests and copy it only from the ForwardAuth response (the supplied Caddyfiles and the Traefik labels above do this). Never enable `SESSIONGUARD_HEADER_LOGIN` on a Guacamole instance that is reachable without this proxy.
- Whoever controls an IdP group controls the Guacamole permissions of the matching Guacamole group, including `ADMINISTER` if that group has it. Use `prefix` so unrelated IdP groups cannot collide with Guacamole group names.
- Groups are taken from the ID token at the SessionGuard login and stay fixed for the access session (`session_hours`). After a group change in the IdP, revoke the user's access session in the Master console (or wait for its expiry); the next login carries the new groups. Removing a user from a group therefore does not take effect instantly unless the session is revoked.
- If the SessionGuard identity of a browser changes while Guacamole still holds a token of the previous user, the extension invalidates that Guacamole session instead of continuing as the old user.
