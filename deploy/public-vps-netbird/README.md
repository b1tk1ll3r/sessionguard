# SessionGuard Public VPS: Caddy + NetBird + EdgeGuard

This stack keeps public TLS termination and Guacamole load balancing on Caddy,
uses NetBird only as the encrypted backend transport, and inserts SessionGuard
EdgeGuard as a localhost-only request pre-check.

## Data path

```text
Internet
  -> Caddy :443 (TLS, SNI, security headers, sticky load balancing)
      -> EdgeGuard 127.0.0.1:9081 /check
      -> SessionGuard Access Auth over NetBird
      -> Guacamole worker over NetBird
```

EdgeGuard is not in the Guacamole tunnel after the WebSocket connection has
been established. It evaluates normal HTTP requests and the WebSocket handshake.

## What EdgeGuard enforces

- static IPv4/IPv6 IP/CIDR blacklist;
- global request rate limit to protect backends during distributed HTTP floods;
- high per-client request rate limit (NAT-friendly defaults; IPv4 per address,
  IPv6 aggregated per /64 via `ipv6_prefix_length`);
- tighter, configurable limits for OIDC/login/callback paths (`/_sessionguard/auth/login`,
  `/_sessionguard/auth/oidc/callback`, `/login`, `/auth/login`, `/oidc/`);
- scanner-path blocking (`/.env`, `/.git`, WordPress/phpMyAdmin probes, etc.);
- blocks CONNECT/TRACE/TRACK;
- temporary persistent bans for clearly hostile scanner/method behavior;
- host allowlist and URI-length validation;
- localhost-only health and Prometheus-style metrics endpoints;
- automatic config/list reload (15 seconds by default);
- bounded per-client state (100,000 entries by default); when full, the
  least-recently-seen entry is evicted instead of rejecting new clients, and
  active bans are never evicted.

The default `rate_limit_weight` for auto-ban is zero on purpose: legitimate
users behind a shared NAT should receive 429 during extreme bursts, but should
not cause the whole NAT address to be banned. Scanner probes are weighted much
more strongly and are auto-banned after repeated hits.

## Caddy hardening

The supplied Caddyfile additionally enables:

- strict SNI/Host matching;
- 10 second request-header timeout;
- 64 KiB maximum request headers;
- HTTP/1.1 + HTTP/2 only (HTTP/3 disabled to reduce exposed protocol surface);
- HSTS and conservative security headers;
- public blocking of SessionGuard `/metrics` and `/api/v1/broker/*`;
- on the Guacamole host only `/_sessionguard/auth/*` is proxied to the Master
  (prefix stripped); every other `/_sessionguard/*` path returns 404, so the
  admin UI and APIs are reachable only via the SessionGuard host;
- `Permissions-Policy` on the Guacamole host allows `fullscreen` and
  `window-management` (multi-monitor helper, `getScreenDetails()`);
- rotated JSON access logs with sampling during request floods;
- Caddy admin API disabled (`admin off`); configuration changes use a container restart.

## Installation

1. Copy `.env.example` to `.env` and set all values.
2. Adjust `edgeguard.json` host names if needed.
3. Add permanent abusive IPs/CIDRs to `blacklist.txt`.
4. Keep `rate-exempt.txt` empty unless you have a known large NAT that really
   needs exemption from per-IP limits.
5. Start with `docker compose up -d`.
6. Verify:

```bash
curl -fsS http://127.0.0.1:9081/healthz -o /dev/null
curl -fsS http://127.0.0.1:9081/metrics
curl -I https://ts.hilden.info/
```

## Important DDoS boundary

EdgeGuard protects the application/backends against HTTP request floods and
common low-cost scanners. It cannot protect a single VPS if the Internet link
or provider edge is saturated. Keep the VPS provider's network firewall and
DDoS protection enabled. For a volumetric attack, filtering must happen before
traffic reaches the VPS.
