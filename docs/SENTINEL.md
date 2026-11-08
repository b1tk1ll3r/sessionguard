# SessionGuard mit Caddy Sentinel

Referenz-Deployment: **Caddy Sentinel** läuft direkt auf dem Host und ist dort Reverse-Proxy (TLS, WAF, IDS, Ratelimits) **und** OIDC-Provider. Der SessionGuard-Stack läuft auf demselben Host in Docker. Benutzer melden sich mit ihrem Active-Directory-Konto (LDAP) an Sentinel an; SessionGuard bekommt die Identität per OpenID Connect.

SessionGuard bleibt dabei ein eigenständiges Projekt: Es spricht nur Standard-OIDC und Standard-ForwardAuth. Sentinel lässt sich durch jeden anderen OIDC-Provider und Reverse-Proxy ersetzen ([ACCESS-AUTH.md](ACCESS-AUTH.md)).

```text
                 Internet / LAN
                       │ 443
        ┌──────────────▼───────────────────────────────────┐
        │ Host: Caddy Sentinel (systemd)                    │
        │  sso.example.org          OIDC-Server ──LDAP──► AD │
        │  sessionguard.example.org ─► 127.0.0.1:8080       │
        │  rds.example.org  forward_auth ─► 127.0.0.1:8080  │
        │                   reverse_proxy ─► 127.0.0.1:8081 │
        └──────────────┬───────────────────────────────────┘
                       │ nur 127.0.0.1
        ┌──────────────▼───────────────────────────────────┐
        │ Docker: deploy/docker-compose.yml                 │
        │  sessionguard-master ─ sessionguard-postgres      │
        │  guacamole ─ guacamole-postgres, guacd ──RDP──►   │
        └───────────────────────────────────────────────────┘
                                          Windows-RDS-Hosts
                                          (SessionGuard-Agent
                                           → sessionguard.example.org)
```

| Weg | Absicherung |
|---|---|
| Browser → Guacamole | Sentinel-Policy `rds` (WAF/IDS/Ratelimits), SessionGuard-ForwardAuth, Proxy-Secret zur Extension |
| Browser → Admin-UI | Sentinel-Policy `sessionguard` (IP-Freigaben), SessionGuard-OIDC mit RBAC |
| Anmeldung | Sentinel-Policy `sso`: Domänenkonten (AD/LDAP), zweiter Faktor, Sperre nach Fehlversuchen |
| Master → Sentinel | OIDC-Discovery, Token, JWKS über `https://sso.example.org` |
| Guacamole → Master | Broker-API über das interne Docker-Netz (`backend`, ohne Weg nach draußen) |
| Agent → Master | `https://sessionguard.example.org/api/v1/agents/*` |

NetBird, Pocket ID, Traefik und der EdgeGuard werden in diesem Aufbau nicht gebraucht.

## LDAP-Anmeldung für OIDC-Anwendungen

Ja: Der OIDC-Server von Sentinel meldet Benutzer mit denselben Konten an wie der Site-Login – also auch mit **Active-Directory-/LDAP-Konten** („Domänenkonten“). Welche Methoden gelten, legt die Policy der Anmelde-Adresse fest (Reiter „Anmeldung“ › „Anmeldemethoden“); pro Anwendung lässt sich das zusätzlich einschränken (`directory`, `local`, `oidc:<anbieter>`). Zweiter Faktor, Sperre nach Fehlversuchen, „Überall abmelden“ und das Anmeldeprotokoll gelten auch für OIDC-Anmeldungen.

Was SessionGuard von einem AD-Konto erhält:

| Claim | Inhalt |
|---|---|
| `sub` | feste Kennung des Sentinel-Kontos (`u-…`) |
| `preferred_username` | Wert von „Attribut Benutzername“ in Kleinbuchstaben, Standard `sAMAccountName` (`mmuster`) – auch bei Anmeldung als `FIRMA\mmuster` oder `mmuster@firma.local` |
| `name`, `email` | `displayName`, `mail` |
| `groups` | CN-Namen der AD-Gruppen (`RDS-Benutzer`), auch verschachtelte, wenn „verschachtelte Gruppen auflösen“ an ist |
| `amr` | `pwd`, mit zweitem Faktor zusätzlich `mfa` |

Zwei Grenzen sollte man kennen:

- **Gruppen stammen aus der letzten Anmeldung an Sentinel.** Beim Erneuern (Refresh) fragt Sentinel das AD nicht erneut. Eine Gruppenänderung oder Deaktivierung im AD erreicht SessionGuard deshalb erst, wenn sich der Benutzer neu an Sentinel anmeldet – oder sofort, wenn ein Administrator das Konto in Sentinel abmeldet oder sperrt (siehe „Abmeldung und Revalidierung“).
- Sentinel ist **kein LDAP-Proxy**: Anwendungen, die selbst LDAP sprechen wollen, bindet man weiterhin direkt ans AD an. Für SessionGuard ist das nicht nötig.

## 1. Caddy Sentinel vorbereiten

### 1.1 Active Directory verbinden

*Site-Login & Konten › Verzeichnis (AD)*: Server (`ldaps://dc01.firma.local`), Dienstkonto, Base-DN, ggf. CA-Zertifikat der Domänencontroller. Mit „Anmeldung testen“ prüfen – die Ausgabe zeigt Benutzername und Gruppen, genau so kommen sie später bei SessionGuard an.

### 1.2 Policies anlegen

| Policy | Host | Einstellungen |
|---|---|---|
| `sso` | `sso.example.org` | Reiter „Anmeldung“: *Login erforderlich*, Anmeldemethoden „Domänenkonten (AD/LDAP)“, zweiter Faktor nach Bedarf, Sitzungsdauer z. B. 12 h. Optional „Erlaubte Gruppen“ für alle Anwendungen. |
| `sessionguard` | `sessionguard.example.org` | Reiter „Zugriff“: IP-Freigabeliste mit Admin-Netzen **und den Netzen der RDS-Hosts** (Agents). WAF auf „Erkennen“ – Policy-JSON sieht für das CRS wie ein Angriff aus. Keine Sentinel-Anmeldung (SessionGuard meldet selbst per OIDC an; Agents haben kein Browser-Login). |
| `rds` | `rds.example.org` | Keine Sentinel-Anmeldung (macht SessionGuard), Bot-Challenge aus (Guacamole nutzt XHR und WebSockets). WAF zuerst auf „Erkennen“ und Fehlalarme für `/api/` und `/websocket-tunnel` über den Fehlalarm-Assistenten ausnehmen. |

Unter *Einstellungen › Netz & Vertrauen* bei „Nie automatisch sperren“ die eigenen Netze eintragen. Ruft der Master den Token-Endpunkt über die Docker-Bridge auf, erscheint er mit der Gateway-Adresse des Docker-Netzes; trage sie dort ebenfalls ein, damit das IDS nie den Master aussperrt.

### 1.3 Sites in Caddy

`deploy/Caddyfile.sentinel` enthält die drei Sites. Als `/etc/caddy/sites/sessionguard.caddy` ablegen (die Sentinel-Caddyfile importiert `sites/*.caddy`) oder im Panel einfügen, Hostnamen anpassen, Konfiguration übernehmen.

Das Proxy-Secret für Guacamole liest Caddy aus einer Datei (Caddy läuft mit `--environ` und würde Umgebungsvariablen ins Journal schreiben):

```bash
openssl rand -hex 32 > /tmp/sg-secret   # derselbe Wert kommt als SESSIONGUARD_PROXY_SECRET in deploy/.env
install -m 0640 -o root -g caddy /tmp/sg-secret /etc/caddy/sessionguard-proxy.secret && shred -u /tmp/sg-secret
```

**Wichtig:** Im Guacamole-Block stehen `request_header`, `forward_auth` und `reverse_proxy` in einem `route { … }`. Ohne `route` sortiert Caddy `request_header` hinter `forward_auth` und löscht die eben gesetzte Identität – Guacamole bekäme dann keinen Benutzer.

### 1.4 OIDC-Server einschalten

*Site-Login & Konten › OIDC-Server*: „OIDC-Server einschalten“, Anmelde-Adresse `https://sso.example.org`, speichern. Der Kasten „Noch zu tun“ muss leer sein.

Zwei Anwendungen anlegen (Client-Secret wird **einmal** angezeigt):

| | Admin-Konsole | Guacamole-Zugang |
|---|---|---|
| Name / Client-ID | `sessionguard-admin` | `sessionguard-access` |
| Redirect-URI | `https://sessionguard.example.org/oidc/callback` | `https://rds.example.org/_sessionguard/auth/oidc/callback` |
| Adresse nach dem Abmelden | `https://sessionguard.example.org/` | `https://rds.example.org/` |
| Erlaubte Gruppen | `SessionGuard-Admins` (und weitere RBAC-Gruppen) | `RDS-Benutzer` |
| PKCE verlangen | an | an |
| Refresh-Tokens ausgeben | **an** (z. B. 1 Tag) | **an** (≥ `session_hours`) |
| Signaturverfahren | RS256 | RS256 |
| Zweiten Faktor verlangen | empfohlen | nach Bedarf |

Refresh-Tokens sind Pflicht, weil SessionGuard die Sitzungen damit regelmäßig bei Sentinel erneuert (`revalidate_minutes`). Ohne Refresh-Token lehnt SessionGuard die Anmeldung mit einer eindeutigen Log-Meldung ab.

## 2. SessionGuard-Stack starten

```bash
cd deploy
cp .env.example .env               # Secrets: openssl rand -hex 32
cp master.json.example master.json # Hostnamen, Client-IDs, Gruppen
docker compose up -d
docker compose logs -f sessionguard-master
```

`.env`:

- `SESSIONGUARD_OIDC_CLIENT_SECRET`, `SESSIONGUARD_ACCESS_OIDC_CLIENT_SECRET`: die beiden Secrets aus Sentinel.
- `SESSIONGUARD_PROXY_SECRET`: derselbe Wert wie `/etc/caddy/sessionguard-proxy.secret`.
- `GUAC_ADMIN_USER`: `preferred_username` des ersten Guacamole-Admins, also der `sAMAccountName` in Kleinbuchstaben. Er ersetzt bei einer **neuen** Datenbank das Standardkonto `guacadmin`.
- `SENTINEL_ISSUER_HOST`: Hostname der Anmelde-Adresse. Docker löst ihn auf den Host auf, damit der Master Sentinel auch ohne internes DNS erreicht.

Stammt das Zertifikat der Anmelde-Adresse aus Sentinels **interner CA**, deren Root-Zertifikat (PEM) nach `deploy/ca/` legen; der Master vertraut dann System-CAs plus dieser CA.

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
  "revalidate_minutes": 5,
  "username_claim": "preferred_username",
  "username_pattern": "^[a-z0-9][a-z0-9._@-]{0,63}$",
  "allowed_groups": ["RDS-Benutzer"],
  "guacamole_groups": { "enabled": true, "prefix": "guac-" }
},
"rbac": { "groups": { "SessionGuard-Admins": ["admin"], "SessionGuard-Helpdesk": ["helpdesk"] } }
```

Gruppennamen sind die **CN-Namen** der AD-Gruppen, wie Sentinel sie im Claim `groups` liefert. `allowed_groups` in SessionGuard und „Erlaubte Gruppen“ der Anwendung in Sentinel ergänzen sich: Sentinel lässt nur passende Konten überhaupt zur Anwendung, SessionGuard prüft bei jeder Revalidierung erneut.

Alle Ports des Stacks sind an `127.0.0.1` gebunden, Datenbanken und guacd haben gar keinen veröffentlichten Port. Weitere Guacamole-Worker: `docker compose -f docker-compose.yml -f docker-compose.workers.yml up -d` und die Worker im Caddyfile mit Sticky-Cookie eintragen.

## 3. Abmeldung und Revalidierung

Sentinel bietet kein OIDC-Back-Channel-Logout. SessionGuard erneuert deshalb jede Sitzung nach `revalidate_minutes` mit ihrem Refresh-Token. Sentinel prüft dabei Konto, Policy der Anmelde-Adresse und die Regeln der Anwendung neu; lehnt es ab, endet die SessionGuard-Sitzung sofort.

| Ereignis | Wirkung in SessionGuard |
|---|---|
| Abmelden in Guacamole oder der Admin-UI | sofort; Sentinel-Sitzung endet mit (`end_session`) |
| Konto in Sentinel „Abmelden“ (überall), „Sperren“, abgelaufen | bei der nächsten Revalidierung (≤ `revalidate_minutes`) |
| Anwendung in Sentinel deaktiviert, Freigabe widerrufen, Gruppen der Anwendung geändert | bei der nächsten Revalidierung |
| Gruppenänderung / Deaktivierung **im AD** | nach der nächsten Sentinel-Anmeldung des Benutzers – für sofortige Wirkung das Konto zusätzlich in Sentinel abmelden oder sperren |
| Sentinel nicht erreichbar | Sitzung gilt bis zum Doppelten des Intervalls weiter, danach Ablehnung |

Die Guacamole-Seite prüft alle 30 Sekunden `/_sessionguard/auth/status` und schließt eine beendete Sitzung im Browser.

## 4. Windows-Agents

```json
"master_url": "https://sessionguard.example.org",
"insecure_master_url": false
```

Die Netze der RDS-Hosts müssen in der Sentinel-Policy `sessionguard` freigegeben sein. Für die lokale Agent-WebUI kann man in Sentinel eine dritte Anwendung (`sessionguard-agent`, Redirect-URI `https://<agent-host>/oidc/callback`) anlegen und in `agent.json` unter `oidc` eintragen.

## 5. Umstieg von Pocket ID / NetBird

1. Sentinel wie oben einrichten, Stack aus `deploy/` starten (die Datenbanken können übernommen werden: Volumes bzw. Dump/Restore).
2. **Identitätsbindungen zurücksetzen:** Die Bindungen von Benutzername an Pocket-ID-`sub` passen nicht zu den neuen Sentinel-Subjects. Admin-Konsole › *Access-Sessions › Identitätsbindungen › Alle zurücksetzen* (beendet auch alle Access-Sessions).
3. Weichen die Benutzernamen ab (Pocket-ID-Name ≠ `sAMAccountName`), legt Guacamole neue Konten an. Rechte deshalb über Guacamole-Gruppen (`guac-…`) vergeben statt pro Benutzer; den ersten Admin mit `deploy/guacamole/harden-guacamole-db.sql` setzen.
4. RBAC-Gruppen in `master.json` auf die AD-Gruppennamen umstellen.
5. NetBird-Routen, Pocket-ID-Clients und die alten Public-VPS-/Worker-Stacks abbauen.

## 6. Prüfliste

- [ ] `https://sso.example.org/.well-known/openid-configuration` liefert `issuer: https://sso.example.org`.
- [ ] `docker compose logs sessionguard-master` zeigt keinen OIDC-Fehler beim Start (Discovery erreicht).
- [ ] `https://rds.example.org/` → Sentinel-Anmeldeseite → nach AD-Login + zweitem Faktor Guacamole mit dem AD-Benutzer.
- [ ] Ein AD-Konto ohne `RDS-Benutzer` sieht in Sentinel „Dein Konto hat keinen Zugriff …“.
- [ ] `https://sessionguard.example.org/` meldet ohne zweites Passwort an (Sentinel-Sitzung), Rolle passt zur AD-Gruppe.
- [ ] `curl -H 'X-Guacamole-User: guacadmin' https://rds.example.org/` landet beim Login, nicht in Guacamole.
- [ ] `curl -H 'X-Guacamole-User: guacadmin' http://127.0.0.1:8081/api/…` auf dem Host meldet niemanden an (Proxy-Secret).
- [ ] `/metrics`, `/api/v1/broker/*`, `/auth/*` auf `sessionguard.example.org` liefern 404.
- [ ] Konto in Sentinel abmelden → nach spätestens `revalidate_minutes` (+30 s) ist Guacamole zu.
