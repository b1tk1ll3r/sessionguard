# SessionGuard mit Caddy Sentinel

Referenz-Deployment über zwei Netzbereiche:

- **DMZ-Host:** Caddy Sentinel direkt auf dem Host (Reverse-Proxy, WAF/IDS, OIDC-Server mit AD-/LDAP-Anmeldung), daneben in Docker der SessionGuard-Master mit seiner Datenbank.
- **Internes Netz:** Guacamole-Worker (Guacamole + guacd) in Docker, dazu die gemeinsame Guacamole-Datenbank.

SessionGuard bleibt dabei ein eigenständiges Projekt: Es spricht nur Standard-OIDC und Standard-ForwardAuth. Sentinel lässt sich durch jeden anderen OIDC-Provider und Reverse-Proxy ersetzen ([ACCESS-AUTH.md](ACCESS-AUTH.md)). NetBird, Pocket ID und der EdgeGuard werden nicht gebraucht.

```text
                         Browser
                            │ 443
 ┌─ DMZ ────────────────────▼──────────────────────────────────────────┐
 │ Caddy Sentinel (Host)                                                │
 │   sso.example.org            Anmeldung ──────────── LDAPS ──────────┼──► Domänencontroller
 │   sessionguard.example.org ─► 127.0.0.1:18080  SessionGuard-Master   │
 │   rds.example.org          ─► forward_auth → Master                  │
 │                             ─► Worker :18080 + Schlüssel ────────────┼──┐
 │ Docker: deploy/dmz  (Master, SessionGuard-DB)                        │  │
 └──────────────────────────────────────────────────────────────────────┘  │
          ▲ 443 (Broker-API, nur Worker-IPs)       ▲ 443 (Agents)         │
 ┌─ Intern ─┼──────────────────────────────────────┼────────────────────▼─┐
 │ guac01, guac02 …  Docker: deploy/worker  (Guacamole + guacd)          │
 │       └──5432──► Docker: deploy/guacamole-db  (z. B. auf guac01)       │
 │       └──3389──► RDS-Hosts (SessionGuard-Agent) ───────────────────────┘
 └───────────────────────────────────────────────────────────────────────┘
```

## Die drei Stacks

| Stack | Wo | Inhalt | Veröffentlichte Ports |
|---|---|---|---|
| `deploy/dmz` | DMZ-Host | SessionGuard-Master, SessionGuard-PostgreSQL | `127.0.0.1:18080` (nur für Sentinel) |
| `deploy/guacamole-db` | intern, z. B. guac01 | Guacamole-PostgreSQL, Ersteinrichtung | `<LAN-IP>:5432` |
| `deploy/worker` | intern, je Worker | Guacamole mit Extension, guacd | `<LAN-IP>:18080` |

Dazu `deploy/Caddyfile.sentinel` für Sentinel auf dem DMZ-Host.

Die Guacamole-Datenbank gehört ins interne Netz zu den Workern: Sie enthält Verbindungen und Rechte und wird nur von den Workern gebraucht. Wer sie vorerst in der DMZ lassen will, startet `deploy/guacamole-db` auf dem DMZ-Host mit dessen DMZ-Adresse als `GUAC_DB_BIND` und gibt in der Firewall 5432 von den Workern frei.

## Firewall

| Von | Nach | Port | Wofür |
|---|---|---|---|
| Browser (Internet/LAN) | DMZ-Host | 443 | Sentinel: Anmeldung, Admin-Konsole, Guacamole |
| DMZ-Host | Worker | 18080 | Sentinel → Guacamole |
| DMZ-Host | Domänencontroller | 636 | Sentinel → AD (LDAPS) |
| Worker | DMZ-Host | 443 | Guacamole → Broker (`https://sessionguard.example.org`) |
| Worker | Guacamole-DB-Host | 5432 | Guacamole → Datenbank |
| Worker | RDS-Hosts | 3389 | guacd → RDP |
| RDS-Hosts | DMZ-Host | 443 | Agents → Master |

Sonst nichts aus der DMZ ins interne Netz. Der Master selbst ist nur auf `127.0.0.1` erreichbar; alles von außen geht durch Sentinel.

Verlangt die Firewall NAT zwischen den Zonen, sieht Sentinel statt der Worker-IP die Adresse der Firewall. Dann diese Adresse bei `remote_ip` im Caddyfile eintragen.

## Der Schlüssel (`SESSIONGUARD_PROXY_SECRET`)

Guacamole meldet Benutzer über den Header `X-Guacamole-User` an – ohne Passwort. Im internen Netz erreicht jeder PC, den die Firewall zu Port 18080 auf einem Worker lässt, Guacamole direkt, ohne Sentinel und ohne Anmeldung. Ohne Schlüssel würde dieser Aufruf als `guacadmin` anmelden:

```bash
curl -H 'X-Guacamole-User: guacadmin' -X POST http://guac01:18080/api/tokens
```

Deshalb zählt der Header nur zusammen mit dem Schlüssel, den nur Caddy Sentinel kennt und nach der Prüfung durch SessionGuard mitschickt (`header_up X-SessionGuard-Proxy-Auth`). Derselbe Wert steht:

- auf dem DMZ-Host in `/etc/caddy/sessionguard-proxy.secret` (`0640 root:caddy`),
- auf jedem Worker in `deploy/worker/.env` als `SESSIONGUARD_PROXY_SECRET`.

Sentinels eigenes Handbuch nennt dieselbe Bedingung für Header-Anmeldung: Eine Anwendung darf solchen Headern nur trauen, wenn sie ausschließlich über Sentinel erreichbar ist. Der Schlüssel erzwingt das, auch wenn eine Firewall-Regel einmal zu weit ist.

## LDAP-Anmeldung für OIDC-Anwendungen

Der OIDC-Server von Sentinel meldet Benutzer mit denselben Konten an wie der Site-Login, also auch mit **Active-Directory-/LDAP-Konten** („Domänenkonten“). Welche Methoden gelten, legt die Policy der Anmelde-Adresse fest (Reiter „Anmeldung“ › „Anmeldemethoden“); pro Anwendung lässt sich das zusätzlich einschränken. Zweiter Faktor, Sperre nach Fehlversuchen, „Überall abmelden“ und das Anmeldeprotokoll gelten auch für OIDC-Anmeldungen.

| Claim | Inhalt |
|---|---|
| `sub` | feste Kennung des Sentinel-Kontos (`u-…`) |
| `preferred_username` | „Attribut Benutzername“ in Kleinbuchstaben, Standard `sAMAccountName` (`mmuster`) – auch bei Anmeldung als `FIRMA\mmuster` oder per UPN |
| `name`, `email` | `displayName`, `mail` |
| `groups` | CN-Namen der AD-Gruppen (`RDS-Benutzer`), auch verschachtelte, wenn aktiviert |
| `amr` | `pwd`, mit zweitem Faktor zusätzlich `mfa` |

Grenzen:

- **Gruppen stammen aus der letzten Anmeldung an Sentinel.** Beim Erneuern fragt Sentinel das AD nicht erneut. Eine Gruppenänderung oder Deaktivierung im AD wirkt nach der nächsten Sentinel-Anmeldung – sofort, wenn das Konto zusätzlich in Sentinel abgemeldet oder gesperrt wird.
- Sentinel ist **kein LDAP-Proxy** für Anwendungen, die selbst LDAP sprechen. SessionGuard braucht das nicht.

## 1. Caddy Sentinel (DMZ-Host)

### 1.1 Active Directory verbinden

*Site-Login & Konten › Verzeichnis (AD)*: Server (`ldaps://dc01.firma.local`), Dienstkonto, Base-DN, ggf. CA-Zertifikat der Domänencontroller. Mit „Anmeldung testen“ prüfen – Benutzername und Gruppen erscheinen genau so später bei SessionGuard.

### 1.2 Policies

| Policy | Host | Einstellungen |
|---|---|---|
| `sso` | `sso.example.org` | Reiter „Anmeldung“: *Login erforderlich*, Anmeldemethode „Domänenkonten (AD/LDAP)“, zweiter Faktor nach Bedarf, Sitzungsdauer z. B. 12 h. |
| `sessionguard` | `sessionguard.example.org` | Reiter „Zugriff“: IP-Freigaben für Admin-Netze, **RDS-Hosts** (Agents) und **Worker** (Broker). WAF auf „Erkennen“. Keine Sentinel-Anmeldung (SessionGuard meldet per OIDC an, Agents und Worker haben kein Browser-Login). |
| `rds` | `rds.example.org` | Keine Sentinel-Anmeldung (macht SessionGuard), Bot-Challenge aus (XHR/WebSockets). WAF zuerst auf „Erkennen“, Fehlalarme für `/api/` und `/websocket-tunnel` über den Fehlalarm-Assistenten ausnehmen. |

Unter *Einstellungen › Netz & Vertrauen* bei „Nie automatisch sperren“ die Worker-IPs und die Gateway-Adresse des Docker-Netzes eintragen (über sie ruft der Master den Token-Endpunkt auf).

### 1.3 Sites

`deploy/Caddyfile.sentinel` als `/etc/caddy/sites/sessionguard.caddy` ablegen oder im Panel einfügen. Anpassen: Hostnamen, Worker-IPs (`10.0.20.11`, `10.0.20.12`) bei `remote_ip` **und** `reverse_proxy`, ggf. Port 18080. Schlüssel anlegen:

```bash
openssl rand -hex 32 > /tmp/sg-secret
install -m 0640 -o root -g caddy /tmp/sg-secret /etc/caddy/sessionguard-proxy.secret
cat /tmp/sg-secret    # für SESSIONGUARD_PROXY_SECRET in worker/.env
shred -u /tmp/sg-secret
```

Was die drei Sites tun:

1. **`sso.example.org`** – nur die Anmeldeseite und der OIDC-Server von Sentinel.
2. **`sessionguard.example.org`** – Admin-Konsole und Agent-API → Master. `/api/v1/broker/*` nur von den Worker-IPs, `/metrics` und `/auth/*` nie.
3. **`rds.example.org`** – Guacamole:
   - `/_sessionguard/auth/*` (Login, Rücksprung, Logout) → Master.
   - Alles andere: Caddy löscht Identitäts-Header vom Browser, fragt den Master (`/auth/verify`), ob der Browser angemeldet ist. Nein → Weiterleitung zur Anmeldung. Ja → Caddy setzt den Benutzer als `X-Guacamole-User`, legt den Schlüssel dazu und leitet an einen Worker weiter (Sticky-Cookie, damit ein Browser bei seinem Worker bleibt).

Das entspricht dem früheren Public-VPS-Caddyfile; neu sind `sentinel …` statt EdgeGuard, `127.0.0.1` für den Master, die Freigabe der Broker-API für die Worker und der Schlüssel. `request_header`, `forward_auth` und `reverse_proxy` stehen in einem `route { … }` – ohne `route` sortiert Caddy `request_header` hinter `forward_auth` und löscht die Identität.

### 1.4 OIDC-Server und Anwendungen

*Site-Login & Konten › OIDC-Server*: einschalten, Anmelde-Adresse `https://sso.example.org`. Zwei Anwendungen (Client-Secret wird **einmal** angezeigt):

| | Admin-Konsole | Guacamole-Zugang |
|---|---|---|
| Client-ID | `sessionguard-admin` | `sessionguard-access` |
| Redirect-URI | `https://sessionguard.example.org/oidc/callback` | `https://rds.example.org/_sessionguard/auth/oidc/callback` |
| Adresse nach dem Abmelden | `https://sessionguard.example.org/` | `https://rds.example.org/` |
| Erlaubte Gruppen | `SessionGuard-Admins` (und weitere RBAC-Gruppen) | `RDS-Benutzer` |
| PKCE verlangen | an | an |
| Refresh-Tokens ausgeben | **an** | **an** (≥ `session_hours`) |

Refresh-Tokens sind Pflicht: SessionGuard erneuert die Sitzungen damit regelmäßig bei Sentinel (`revalidate_minutes`, Abschnitt 5).

## 2. SessionGuard-Master (DMZ-Host)

```bash
cd deploy/dmz
cp .env.example .env               # Secrets: openssl rand -hex 32
cp master.json.example master.json # Hostnamen, Client-IDs, AD-Gruppen
docker compose up -d
docker compose logs -f sessionguard-master
```

`.env`: die beiden Client-Secrets aus Sentinel, `SESSIONGUARD_ENROLLMENT_TOKEN`, `SESSIONGUARD_BROKER_API_KEY` (kommt auch auf die Worker), `SENTINEL_ISSUER_HOST`. Stammt das Zertifikat von `sso.example.org` aus Sentinels interner CA, deren Root-Zertifikat nach `deploy/dmz/ca/` legen.

`master.json` (Auszug):

```json
"oidc": {
  "issuer": "https://sso.example.org",
  "client_id": "sessionguard-admin",
  "scopes": ["openid", "profile", "email", "groups", "offline_access"],
  "revalidate_minutes": 5
},
"access_auth": {
  "enabled": true,
  "issuer": "https://sso.example.org",
  "client_id": "sessionguard-access",
  "redirect_url": "https://rds.example.org/_sessionguard/auth/oidc/callback",
  "revalidate_minutes": 5,
  "username_claim": "preferred_username",
  "allowed_groups": ["RDS-Benutzer"],
  "allowed_hosts": ["rds.example.org"],
  "guacamole_groups": { "enabled": true, "prefix": "guac-" }
},
"rbac": { "groups": { "SessionGuard-Admins": ["admin"], "SessionGuard-Helpdesk": ["helpdesk"] } }
```

Gruppennamen sind die **CN-Namen** der AD-Gruppen.

## 3. Guacamole-Datenbank (intern)

```bash
cd deploy/guacamole-db
cp .env.example .env    # GUAC_DB_BIND = LAN-IP dieses Hosts, GUAC_DB_PASSWORD, GUAC_ADMIN_USER
docker compose up -d
```

`GUAC_ADMIN_USER` ist der AD-Benutzername (sAMAccountName, Kleinbuchstaben) des ersten Guacamole-Admins; er ersetzt bei einer **neuen** Datenbank das Standardkonto `guacadmin`.

**Bestehende Datenbank aus der DMZ übernehmen:**

```bash
# DMZ-Host, alter Stack
docker compose exec -T guacamole-postgres pg_dump -U guacamole_user -Fc guacamole_db > guacamole_db.dump
# interner Host: neuen Stack starten, dann
docker compose exec -T guacamole-postgres pg_restore -U guacamole_user -d guacamole_db --clean --if-exists < guacamole_db.dump
docker compose exec -T guacamole-postgres psql -U guacamole_user -d guacamole_db \
  -v ON_ERROR_STOP=1 -v admin_user=DEIN_AD_NAME < harden-guacamole-db.sql
```

## 4. Guacamole-Worker (intern, je Worker)

```bash
cd deploy/worker
cp .env.example .env
docker compose up -d
docker compose logs -f guacamole
```

`.env` je Worker: `GUACAMOLE_BIND` (LAN-IP dieses Workers), `GUAC_DB_HOST`, `GUAC_DB_PASSWORD`, `SESSIONGUARD_BROKER_API_KEY` (wie in `dmz/.env`), `SESSIONGUARD_PROXY_SECRET` (wie `/etc/caddy/sessionguard-proxy.secret`), `SESSIONGUARD_MASTER_URL=https://sessionguard.example.org`.

Das interne DNS muss `sessionguard.example.org` auf den DMZ-Host auflösen. Hat der Name ein Zertifikat von Sentinels interner CA, braucht die Java-Laufzeit im Guacamole-Container diese CA in ihrem Truststore; mit einem öffentlichen Zertifikat ist nichts zu tun.

Gegenüber dem bisherigen Worker-Stack neu: `SESSIONGUARD_PROXY_SECRET`, `SESSIONGUARD_HEADER_LOGIN` und `EXTENSION_PRIORITY` (Gruppen-Sync); `SESSIONGUARD_MASTER_URL` zeigt auf HTTPS über Sentinel statt auf die NetBird-IP. NetBird, feste Docker-IPs und `user: 0:0` entfallen.

## 5. Abmeldung und Revalidierung

Sentinel bietet kein OIDC-Back-Channel-Logout. SessionGuard erneuert deshalb jede Sitzung nach `revalidate_minutes` mit ihrem Refresh-Token; lehnt Sentinel ab, endet die Sitzung.

| Ereignis | Wirkung in SessionGuard |
|---|---|
| Abmelden in Guacamole oder der Admin-UI | sofort; Sentinel-Sitzung endet mit |
| Konto in Sentinel abgemeldet (überall), gesperrt, abgelaufen | bei der nächsten Revalidierung (≤ `revalidate_minutes`) |
| Anwendung deaktiviert, Freigabe widerrufen, Gruppen der Anwendung geändert | bei der nächsten Revalidierung |
| Gruppenänderung / Deaktivierung **im AD** | nach der nächsten Sentinel-Anmeldung – sofort, wenn zusätzlich in Sentinel abgemeldet |
| Sentinel nicht erreichbar | Sitzung gilt bis zum Doppelten des Intervalls, danach Ablehnung |

## 6. Windows-Agents

```json
"master_url": "https://sessionguard.example.org",
"insecure_master_url": false
```

Die Netze der RDS-Hosts müssen in der Sentinel-Policy `sessionguard` freigegeben sein.

## 7. Umstieg von Pocket ID / NetBird

1. Sentinel wie oben einrichten; DMZ-Stack starten (SessionGuard-Datenbank per Volume oder Dump übernehmen).
2. Guacamole-Datenbank ins interne Netz umziehen (Abschnitt 3) oder vorerst in der DMZ lassen.
3. Worker auf `deploy/worker` umstellen, Schlüssel eintragen.
4. **Identitätsbindungen zurücksetzen:** Admin-Konsole › *Access-Sessions › Identitätsbindungen › Alle zurücksetzen* – die Bindungen an Pocket-ID-Subjects passen nicht zu Sentinel.
5. Weichen die Benutzernamen ab (Pocket-ID-Name ≠ `sAMAccountName`), legt Guacamole neue Konten an. Rechte über Guacamole-Gruppen (`guac-…`) vergeben; ersten Admin mit `harden-guacamole-db.sql` setzen.
6. RBAC-Gruppen in `master.json` auf AD-Gruppennamen umstellen.
7. NetBird-Peers, Pocket-ID-Clients und den Public-VPS abbauen.

## 8. Prüfliste

- [ ] `https://sso.example.org/.well-known/openid-configuration` liefert `issuer: https://sso.example.org`.
- [ ] `docker compose logs sessionguard-master` (DMZ) ohne OIDC-Fehler.
- [ ] `https://rds.example.org/` → Sentinel-Anmeldung → nach AD-Login Guacamole mit dem AD-Benutzer.
- [ ] Ein AD-Konto ohne `RDS-Benutzer` sieht in Sentinel „Dein Konto hat keinen Zugriff …“.
- [ ] `https://sessionguard.example.org/` meldet ohne zweites Passwort an, Rolle passt zur AD-Gruppe.
- [ ] Vom internen PC: `curl -H 'X-Guacamole-User: guacadmin' -X POST http://guac01:18080/api/tokens` meldet niemanden an (Schlüssel), im Guacamole-Log „ignored identity header … without valid proxy secret“.
- [ ] Von einem Nicht-Worker: `https://sessionguard.example.org/api/v1/broker/tokens` → 404.
- [ ] Verbindung in Guacamole öffnen: Broker wählt einen RDS-Host (Worker-Log ohne „broker is unavailable“).
- [ ] Konto in Sentinel abmelden → nach spätestens `revalidate_minutes` (+30 s) ist Guacamole zu.
