# Pocket ID settings for the Hilden deployment

There is one OIDC provider (`https://login.hilden.de`) and two relevant Pocket ID OIDC clients. SessionGuard v0.5.2 with this hotfix uses confidential Authorization Code clients with PKCE S256 and RP-initiated logout.

## Client 1: Guacamole Director

Use these settings in Pocket ID:

- Name: `Guacamole Director`
- Public Client: **OFF**
- PKCE: **ON**
- Client secret: **keep the existing secret**
- Client start URL: `https://director.hilden.de`
- Require re-authentication: **OFF** unless you intentionally want a fresh Pocket ID authentication on every authorization
- Skip consent screen: optional; it does not affect PKCE or logout
- Federated Client Credentials: **not required** for SessionGuard's browser login; remove them unless another workload intentionally uses them

Callback URLs:

- `https://director.hilden.de/oidc/callback`
- `https://gts01.hilden.de:9091/oidc/callback`
- `https://gts02.hilden.de:9091/oidc/callback`
- `https://gts03.hilden.de:9091/oidc/callback`

Logout Callback URLs (Pocket ID allow-list for `post_logout_redirect_uri`):

- `https://director.hilden.de/`
- `https://gts01.hilden.de:9091/`
- `https://gts02.hilden.de:9091/`
- `https://gts03.hilden.de:9091/`

The SessionGuard instance must use the callback/logout pair for the hostname through which that instance is opened. Example for the Director URL:

```json
"oidc": {
  "issuer": "https://login.hilden.de",
  "client_id": "307fde28-7d87-4671-9c7a-201d2332a617",
  "client_secret": "SET-BY-SESSIONGUARD_OIDC_CLIENT_SECRET",
  "redirect_url": "https://director.hilden.de/oidc/callback",
  "logout_redirect_url": "https://director.hilden.de/",
  "admin_groups": [],
  "secure_cookie": true
}
```

For a directly opened agent UI, use its exact pair instead, e.g. `https://gts01.hilden.de:9091/oidc/callback` and `https://gts01.hilden.de:9091/`.

## Client 2: Guacamole (portal)

Use these settings in Pocket ID:

- Name: `Guacamole`
- Public Client: **OFF**
- PKCE: **ON**
- Client secret: **keep the existing secret**
- Client start URL: `https://portal.hilden.de`
- Require re-authentication: **OFF** unless explicitly desired
- Skip consent screen: optional
- Federated Client Credentials: **not required** for SessionGuard's browser login

Callback URL used by SessionGuard:

- `https://portal.hilden.de/_sessionguard/auth/oidc/callback`

Logout Callback URL:

- `https://portal.hilden.de/`

SessionGuard configuration:

```json
"access_auth": {
  "enabled": true,
  "issuer": "https://login.hilden.de",
  "client_id": "170f9724-2d12-45c4-8e6f-e1e559be4f72",
  "client_secret": "SET-BY-SESSIONGUARD_ACCESS_OIDC_CLIENT_SECRET",
  "redirect_url": "https://portal.hilden.de/_sessionguard/auth/oidc/callback",
  "logout_redirect_url": "https://portal.hilden.de/",
  "cookie_name": "sg_access_session",
  "cookie_domain": "",
  "secure_cookie": true,
  "session_hours": 8,
  "username_claim": "preferred_username",
  "allowed_groups": [],
  "allowed_hosts": ["portal.hilden.de"]
}
```

If `https://guacauth.hilden.info/portals/guacamole/oauth2/callback` is no longer used by a separate native Guacamole OIDC integration, remove it from Pocket ID. Keep it only if that second integration is still actually in use.

## Logout behavior in this hotfix

Both SessionGuard flows discover Pocket ID's `end_session_endpoint`. On logout SessionGuard clears its local session and redirects the browser to Pocket ID with:

- `client_id`
- `id_token_hint` when a successful SessionGuard login supplied one
- the configured and registered `post_logout_redirect_uri`

For the Director/Admin/Agent flow the hotfix retains the ID token server-side for the lifetime of the local browser session so it can be supplied as `id_token_hint` without placing the raw ID token in a browser cookie.

The existing SessionGuard `/auth/backchannel-logout` handler is separate from the browser post-logout redirect. Do not enter that endpoint as the normal browser Logout Callback URL unless your Pocket ID version/integration explicitly provides a dedicated back-channel logout setting and calls it as such.
