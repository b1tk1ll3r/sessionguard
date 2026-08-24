# SessionGuard + 3x Guacamole + NetBird + public Caddy

Target design for about 200-250 concurrent Guacamole/RDS sessions:

```text
Internet
   |
   v
PUBLIC01 (Caddy + NetBird)
   |  HTTPS termination / SessionGuard ForwardAuth / sticky LB
   |  NetBird/WireGuard
   +--> GUAC01 Docker subnet --> Guacamole01 --> guacd01 --> RDS farm
   +--> GUAC02 Docker subnet --> Guacamole02 --> guacd02 --> RDS farm
   +--> GUAC03 Docker subnet --> Guacamole03 --> guacd03 --> RDS farm
   +--> SESSIONGUARD01 Docker subnet --> SessionGuard Master
                                      --> shared Guacamole PostgreSQL
```

## Important: Guacamole is configured once, not three times

All three workers connect to the **same Guacamole PostgreSQL database** at
`10.202.0.20:5432`.

Therefore these are shared automatically:

- Guacamole users
- connection definitions
- connection groups
- permissions
- user preferences stored in PostgreSQL
- the Guacamole connection IDs used by SessionGuard resources

Every worker must use the same:

- SessionGuard-enabled Guacamole image/version
- PostgreSQL credentials/database
- `SESSIONGUARD_BROKER_API_KEY`
- Header-Auth configuration

There is **no PocketID/OIDC configuration on the individual Guacamole workers**.

## PocketID flow

PocketID is configured once through SessionGuard:

```text
Browser -> https://ts.hilden.info
        -> public Caddy
        -> SessionGuard /auth/verify
        -> PocketID if no valid access session
        -> SessionGuard OIDC callback
        -> Caddy injects X-Guacamole-User
        -> selected Guacamole worker
```

Use one PocketID confidential client for Guacamole access:

- Redirect URI: `https://ts.hilden.info/_sessionguard/auth/oidc/callback`
- Back-channel logout callback: `https://ts.hilden.info/_sessionguard/auth/backchannel-logout`
- Post logout redirect: `https://ts.hilden.info/`
- Scopes: `openid profile email groups`

Use a second PocketID client for the SessionGuard administration UI:

- Redirect URI: `https://sessionguard.hilden.info/oidc/callback`

## DNS

Only the public VPS needs public DNS:

```text
ts.hilden.info            -> PUBLIC01 public IP
sessionguard.hilden.info  -> PUBLIC01 public IP
```

`auth.hilden.info` remains wherever PocketID currently runs. It only needs to be
moved behind PUBLIC01 if you intentionally want that architecture too.

The Guacamole workers and SessionGuard host need no public A records.

## Docker subnet plan

The example intentionally uses unique, fixed subnets:

```text
GUAC01           10.201.1.0/24   Guacamole 10.201.1.10
GUAC02           10.201.2.0/24   Guacamole 10.201.2.10
GUAC03           10.201.3.0/24   Guacamole 10.201.3.10
SESSIONGUARD01   10.202.0.0/24   Master    10.202.0.10
                                    SG DB     10.202.0.11 (NOT routed)
                                    Guac DB   10.202.0.20
```

Change them if they overlap any existing LAN, Docker, VPN or NetBird routes.

## Deployment order

1. Deploy SessionGuard stack.
2. Deploy GUAC01/02/03 using the same `guac-worker/docker-compose.yml` but the
   matching `.env` values.
3. Register all four on-prem hosts as NetBird peers.
4. Configure the NetBird Networks described in `NETBIRD-NETWORKS.md`.
5. Verify PUBLIC01 can reach the three Guacamole IPs and SessionGuard health.
6. Deploy PUBLIC01 Caddy stack.
7. Point public DNS for `ts.hilden.info` and `sessionguard.hilden.info` to PUBLIC01.
8. Configure the PocketID callback URLs.
9. Test `/ _sessionguard/auth/status` (without the space) and then Guacamole.

## Guacamole N+1 behavior

Caddy uses a signed `guac_node` load-balancer cookie. A browser stays on the same
Guacamole webapp while that worker is healthy. If a worker fails, new requests can
move to another worker, but an already active remote desktop tunnel on the failed
worker must reconnect. The RDS session itself can survive and SessionGuard can
broker the reconnect back to its existing RDS host.

For a 250-session peak, three workers imply about 83 sessions/worker normally and
125 sessions/worker with one worker down. Validate that target with your actual
applications and display settings.

## RDP target DNS

Your Guacamole connection currently uses:

```text
${SESSIONGUARD_HOST}.stadt-hilden.de
```

Ensure the Guacamole containers can resolve the internal `stadt-hilden.de` zone.
If NetBird DNS overrides resolver settings on the hosts, configure a NetBird DNS
nameserver/match-domain for your AD/internal DNS zone.

## Updating a worker

Because the three workers are stateless apart from the shared `drive` volume and
shared PostgreSQL state, drain one worker at the Caddy/NetBird level, update it,
then return it to service. Never create three independent Guacamole databases.
