# Public Edge Security (Caddy + SessionGuard EdgeGuard)

SessionGuard EdgeGuard is a small Go service for the public reverse-proxy host.
It is **not** another login and it is not a WAF replacement. It runs locally on
`127.0.0.1:9081` and is called by Caddy using `forward_auth` before traffic is
sent to SessionGuard or a Guacamole worker.

## Threat model

EdgeGuard is intended to reduce the cost of common Internet abuse:

- repeated scanners and exploit probes;
- excessive requests from one source;
- request floods intended to exhaust SessionGuard/Guacamole rather than the
  physical Internet link;
- malformed/oversized URIs and unexpected HTTP methods;
- accidental exposure of backend-only SessionGuard endpoints.

It cannot stop a volumetric attack that saturates the VPS uplink. Provider-side
DDoS filtering/firewalling remains necessary for that class of attack.

## Request flow

```text
Browser
  -> Caddy TLS
      -> EdgeGuard /check (localhost only)
          -> 204: continue
          -> 403/404/405/421/429: stop at edge
      -> SessionGuard Access Auth
      -> Guacamole worker
```

Caddy's `forward_auth` sends a lightweight GET subrequest. EdgeGuard therefore
checks the initial WebSocket handshake, but it is not in the byte path of the
established Guacamole WebSocket/RDP stream.

## Controls

### Static blacklist

`blacklist.txt` accepts one IPv4/IPv6 address or CIDR per line. Comments start
with `#`. Changes are automatically picked up.

```text
203.0.113.44
198.51.100.0/24
2001:db8:1234::/48
```

### Global overload limit

The global token bucket is intentionally high and protects the application
backends when a distributed HTTP flood reaches the VPS. It does not prevent
network saturation because TLS and the incoming packets already reached Caddy.

Default production example:

```json
"global_limit": {
  "rate_per_second": 2500,
  "burst": 5000
}
```

### Per-IP limit

The default is deliberately NAT-friendly:

```json
"per_ip_limit": {
  "rate_per_second": 200,
  "burst": 500
}
```

For environments where many staff share one public NAT IP, do not aggressively
lower this value. If a known source really needs exemption, put its address in
`rate-exempt.txt`. Exempt sources still pass blacklist, scanner, method, host
and global-overload checks.

IPv6 clients are aggregated per prefix (`ipv6_prefix_length`, default `64`)
because one end site usually owns at least a /64 and can rotate source
addresses freely inside it. Per-IP buckets, endpoint buckets, offense points
and temporary bans therefore apply to the whole /64; IPv4 (and IPv4-mapped
IPv6) clients remain keyed per /32. Static blacklist and `rate-exempt.txt`
entries still match the exact address/CIDR you list. Changing
`ipv6_prefix_length` at runtime re-keys active bans and resets the per-client
buckets.

### Endpoint-specific limits

The example uses tighter limits for OIDC login/callback paths: `/_sessionguard/auth/login` and
`/_sessionguard/auth/oidc/callback` on the Guacamole host, and `/login`,
`/auth/login` and `/oidc/` on the SessionGuard administration host. These endpoints
do not contain the PocketID password check itself; the limits are intended to
protect state/session allocation and redirect processing from floods.

### Bounded per-IP memory

`max_tracked_ips` bounds the in-memory table used for per-client token buckets
and offense state. The production example allows 100,000 active client keys
(IPv4 /32 or IPv6 /64). When the table is full, the least-recently-seen entry
is evicted (O(1), LRU) so new clients are still admitted; an attacker rotating
sources cannot lock legitimate users out of the edge. Eviction only resets
that client's buckets and offense points: temporary bans are kept in a
separate table and are never evicted to make room. The ban table is bounded by
the same limit; when it is full, further bans are not recorded
(`sessionguard_edgeguard_bans_dropped_total`) while the offending requests are
still denied. Stale entries are cleaned up automatically.

### Scanner detection and temporary bans

Known irrelevant exploit/scanner paths are denied before they reach the
backends. The defaults include `/.env`, `/.git`, WordPress, phpMyAdmin, CGI,
Spring Actuator and several common automated exploit probes.

The example auto-ban weights are:

- scanner path: 5 points;
- blocked method: 3 points;
- malformed URI: 3 points;
- ordinary rate-limit violation: 0 points.

At 10 points within 120 seconds, the IP is banned for 900 seconds. Bans are
persisted in `/var/lib/sessionguard-edgeguard/state.json` with a short write
debounce, so a container restart does not normally remove them while scanner
floods cannot force one synchronous disk write per request.

Rate-limit violations deliberately have weight 0 by default to avoid banning a
whole corporate NAT during a legitimate burst.

### Blocked HTTP methods

`CONNECT`, `TRACE` and `TRACK` are rejected. SessionGuard/Guacamole continue to
use their normal GET/POST/PUT/PATCH/DELETE/OPTIONS behavior.

### Host allowlist

Only configured public host names are accepted by EdgeGuard. Caddy additionally
uses `strict_sni_host on`, requiring the TLS SNI host and HTTP Host header to
match.

## Caddy hardening

The supplied public Caddyfile also configures:

```caddyfile
servers {
    protocols h1 h2
    strict_sni_host on
    max_header_size 64KB
    timeouts {
        read_header 10s
    }
}
```

The short header timeout and smaller header ceiling reduce slow-header/resource
exhaustion risk. HTTP/3 is intentionally disabled in the example to reduce the
public protocol surface; Guacamole works with HTTP/1.1/WebSocket and HTTP/2.

Access logs are written to size-limited rotating files. Sampling keeps all normal
traffic but reduces log amplification once a single logger exceeds 200 entries
per second.

The public SessionGuard host denies `/metrics` and `/api/v1/broker/*` at Caddy.
Guacamole workers call broker APIs directly over NetBird instead.

On the Guacamole host only `/_sessionguard/auth/*` is forwarded to the Master
(with `/_sessionguard` stripped, so the Master receives `/auth/login`,
`/auth/oidc/callback`, `/auth/logout`, `/auth/backchannel-logout`,
`/auth/status`, `/auth/verify` and `/auth/display-policy`). Every other path
below `/_sessionguard/` returns 404, so the admin UI, `/api/v1/*` and
`/metrics` are not reachable through the Guacamole hostname. Agent
enrollment/heartbeat (`/api/v1/agents/*`) stays reachable only on the
SessionGuard host, which is the documented agent `master_url`.

The Guacamole host sends
`Permissions-Policy: ... fullscreen=(self), window-management=(self)` because
the multi-monitor helper uses the Window Management API
(`getScreenDetails()`); the SessionGuard host keeps the stricter default.

The Caddy admin API is disabled (`admin off`) on this dedicated public edge;
configuration changes are applied by restarting the Caddy container.

## Metrics

EdgeGuard exposes Prometheus text metrics only on localhost:

```text
http://127.0.0.1:9081/metrics
```

Counters include total checks, allows, static-blacklist denies, temporary-ban
denies, rate-limit denies, scanner denies, auto-bans, LRU state evictions
(`sessionguard_edgeguard_state_evictions_total`), dropped bans and the current
number of tracked clients. The former
`sessionguard_edgeguard_denied_capacity_total` counter was removed because a
full table no longer denies new clients.

## Configuration reload

`edgeguard.json`, `blacklist.txt` and `rate-exempt.txt` are re-read on the
configured interval (15 seconds in the example). An invalid replacement config
is logged and the previous working configuration remains active.

## Operational recommendations

1. Keep provider/network DDoS protection enabled.
2. Expose only TCP 80/443 publicly; keep EdgeGuard on localhost.
3. Keep NetBird peer/backend ports private.
4. Pin Caddy to a currently patched release rather than an old major-only image
   during controlled production rollouts.
5. Monitor HTTP 429 and EdgeGuard auto-ban counters before tightening limits.
6. Do not put broad office/country CIDRs on the static blacklist without first
   checking whether legitimate remote users may originate there.
