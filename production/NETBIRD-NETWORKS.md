# NetBird Networks / Routing-Peer setup

Use **Networks** (the newer NetBird model), not legacy Network Routes.

The NetBird client runs with `network_mode: host` on each Docker host. Each Docker
host is a routing peer for only the private Docker resource(s) that must be
reachable remotely. None of the application containers publishes a host port.

## Peer groups

Create these peer groups:

- `public-proxy`: PUBLIC01
- `guac-workers`: GUAC01, GUAC02, GUAC03
- `sessionguard-host`: SESSIONGUARD01

## Network: guac01-docker

Routing peer: `GUAC01`

Resource:

- Name: `guac01-web`
- Address: `10.201.1.10`
- Type: IP
- Resource group: `guacamole-web`

Policy:

- Source: `public-proxy`
- Destination/resource: `guac01-web`
- Protocol: TCP
- Port: 8080

Repeat with:

- GUAC02 -> `10.201.2.10:8080`
- GUAC03 -> `10.201.3.10:8080`

Do **not** advertise the guacd addresses (`.11`). guacd has no authentication and
must remain reachable only inside its local Docker bridge.

## Network: sessionguard-docker

Routing peer: `SESSIONGUARD01`

Resources:

1. `sessionguard-master`
   - Address: `10.202.0.10`
   - Type: IP

2. `guacamole-db`
   - Address: `10.202.0.20`
   - Type: IP

Policies:

- `public-proxy` -> `sessionguard-master`, TCP 8080
- `guac-workers` -> `sessionguard-master`, TCP 8080
- `guac-workers` -> `guacamole-db`, TCP 5432

Do **not** create a resource for `10.202.0.11` (SessionGuard PostgreSQL).
That database remains private to the SessionGuard Docker bridge.

## IP forwarding

Linux routing peers normally enable forwarding automatically through NetBird.
If forwarding is blocked by host policy, persist it explicitly:

```bash
sudo sysctl -w net.ipv4.ip_forward=1
printf 'net.ipv4.ip_forward=1\n' | sudo tee /etc/sysctl.d/99-netbird-routing.conf
```

## Verify

On each routing peer:

```bash
netbird status -d
ip route
```

From PUBLIC01, after the Networks are distributed:

```bash
curl -I http://10.201.1.10:8080/
curl -I http://10.201.2.10:8080/
curl -I http://10.201.3.10:8080/
curl -I http://10.202.0.10:8080/healthz
```

From a Guacamole worker container:

```bash
# SessionGuard broker/control plane
wget -S -O- http://10.202.0.10:8080/healthz

# Shared Guacamole PostgreSQL
# use pg_isready/psql from a temporary postgres client container if needed
```

For production, check that PUBLIC01 <-> routing peers are **P2P/direct**, not
permanently relayed, using `netbird status -d`.
