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

  - traefik.http.middlewares.guac-sg-auth.forwardauth.address=http://sessionguard-master:8080/auth/verify
  - traefik.http.middlewares.guac-sg-auth.forwardauth.authResponseHeaders=X-Guacamole-User,X-SessionGuard-User,X-SessionGuard-Email,X-SessionGuard-Groups

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
- after Guacamole's own logout reaches its logged-out state, it redirects to `/_sessionguard/auth/logout`, which also ends the SessionGuard/PocketID session.

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
- Guacamole logout button: Guacamole destroys its own token, then the SessionGuard helper performs full OIDC logout.
