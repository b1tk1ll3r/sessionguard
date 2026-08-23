# SessionGuard v0.3 – Installation und Konfiguration

> Schritt-für-Schritt-Anleitung für den Betrieb von SessionGuard als RDS-Control-Plane zusammen mit Apache Guacamole 1.6, PocketID, Traefik, PostgreSQL und Windows Remote Desktop Session Hosts.

Diese Anleitung ist auf den aktuellen SessionGuard-v0.3-Arbeitsstand und die vorhandene Umgebung mit `git.send.nrw`, `ts.hilden.info`, `guacauth.hilden.info` und `auth.hilden.info` zugeschnitten. Alle Secrets sind Platzhalter und müssen durch eigene, zufällige Werte ersetzt werden.

---

## 1. Zielbild

Nach der Installation sieht die Umgebung logisch so aus:

```text
Internet
   |
   v
Traefik
   |
   +----------------------+-----------------------+
   |                      |                       |
   v                      v                       v
Guacamole             PocketID              SessionGuard Master
+ SessionGuard            |                       |
Extension                 |                       +---- PostgreSQL
   |                      |                       |
   +----------------------+                       |
   |                                              |
   | Broker API                                   | Agent Heartbeats
   v                                              v
SessionGuard Master <---------------------- Windows RDS Agents
   |                                              |
   | Brokerentscheidung                           +-- RDS01
   |                                              +-- RDS02
   +----------------------------------------------+-- RDS03
```

Die Verantwortlichkeiten bleiben bewusst getrennt:

| Komponente | Aufgabe |
|---|---|
| PocketID | Benutzeridentität und OIDC-Gruppen |
| Traefik / traefik-forward-auth | TLS, Edge-Proxy und Guacamole-Header-Authentifizierung |
| Guacamole | HTML5/RDP-Gateway |
| SessionGuard Guacamole Extension | fragt beim Verbindungsaufbau den Broker nach dem Ziel-RDS-Host |
| SessionGuard Master | Broker, Director, Farms, Policies, RBAC, Alerts, Audit, Session-Historie |
| SessionGuard Agent | lokale RDS-/WTS-Steuerung, Profil-Pipeline, Templates und Telemetrie |
| PostgreSQL | persistenter SessionGuard-Control-Plane- und Director-State |
| Windows RDS | eigentliche Benutzer-Sitzungen |

---

# Teil A – Voraussetzungen

## 2. Voraussetzungen prüfen

### 2.1 Docker-/Linux-Seite

Benötigt werden:

- Docker Engine
- Docker Compose v2
- bestehender Traefik v3
- Zugriff auf `git.send.nrw`
- DNS für Guacamole, PocketID und SessionGuard
- PostgreSQL 17 wird empfohlen

Vorhanden sind bereits:

```text
Guacamole:       https://ts.hilden.info
Forward Auth:    https://guacauth.hilden.info
PocketID:        https://auth.hilden.info
```

Für SessionGuard wird in dieser Anleitung beispielhaft verwendet:

```text
SessionGuard:    https://sessionguard.hilden.info
```

Dafür einen DNS-Eintrag auf denselben Traefik-Endpunkt erstellen.

### 2.2 Windows-Seite

Pro RDS-Host:

- Windows Server 2019/2022/2025 x64
- RDS Session Host installiert und funktionsfähig
- lokaler Administrator für die Installation
- Netzwerkzugriff zum SessionGuard Master
- bei Profil-Sicherung: Zugriff auf den gewünschten SMB-Store
- empfohlen: gMSA für den SessionGuard-Dienst, wenn auf SMB-Shares geschrieben werden soll

### 2.3 Build-Voraussetzungen

Falls der Windows-Agent lokal gebaut wird:

```text
Go 1.23+
PowerShell 5.1+
Git
```

Für einen lokalen Build der Guacamole-Extension:

```text
JDK 17
Maven 3.9+
```

Im normalen Gitea-CI/CD-Betrieb wird die Guacamole-Extension bereits im Docker-Build erstellt.

---

# Teil B – Secrets und PocketID

## 3. SessionGuard-Secrets erzeugen

Für unterschiedliche Vertrauensbeziehungen **unterschiedliche Secrets** verwenden.

Beispiel unter Linux:

```bash
openssl rand -base64 48
openssl rand -base64 48
openssl rand -base64 48
openssl rand -base64 48
```

Benötigt werden mindestens:

```text
SESSIONGUARD_DB_PASSWORD
SESSIONGUARD_ENROLLMENT_TOKEN
SESSIONGUARD_BROKER_API_KEY
SESSIONGUARD_OIDC_CLIENT_SECRET
```

Die Secrets dürfen nicht wiederverwendet werden.

Insbesondere sind diese Werte voneinander zu trennen:

```text
PocketID OIDC Client Secret       Browser -> SessionGuard
Broker API Key                    Guacamole Extension -> Master
Enrollment Token                  neuer Agent -> Master
Per-Agent Token                   Agent -> Master, wird automatisch erzeugt
PostgreSQL Password               Master -> PostgreSQL
```

---

## 4. PocketID für den SessionGuard Master konfigurieren

In PocketID einen **neuen vertraulichen OIDC-Client nur für SessionGuard** anlegen.

Beispiel:

```text
Name:          SessionGuard Master
Client Type:   Confidential
```

Redirect URI:

```text
https://sessionguard.hilden.info/oidc/callback
```

Scopes:

```text
openid profile email groups
```

Client-ID und Client-Secret notieren.

Das Client-Secret später ausschließlich über:

```text
SESSIONGUARD_OIDC_CLIENT_SECRET
```

an den Master übergeben.

### 4.1 Empfohlene PocketID-Gruppen

Beispielsweise:

```text
sessionguard-admins
sessionguard-helpdesk
sessionguard-operators
sessionguard-policy
sessionguard-profile
sessionguard-auditors
```

Empfohlene SessionGuard-Zuordnung:

| PocketID-Gruppe | SessionGuard-Rolle |
|---|---|
| `sessionguard-admins` | `admin` |
| `sessionguard-helpdesk` | `helpdesk` |
| `sessionguard-operators` | `operator` |
| `sessionguard-policy` | `policy_admin` |
| `sessionguard-profile` | `profile_admin` |
| `sessionguard-auditors` | `auditor` |

Für Produktion sollte `rbac.groups` nicht leer bleiben. Ein leeres Mapping vergibt aus Kompatibilitätsgründen weitreichendere Rechte als für einen finalen Produktionsbetrieb gewünscht.

---

## 5. PocketID für die lokalen Agent-WebUIs

Das lokale Agent-WebUI ist als Fallback vorgesehen, wenn der Master nicht erreichbar ist.

Pro Terminalserver kann ein eigener OIDC-Client verwendet werden, zum Beispiel:

```text
https://rds01-mgmt.hilden.info/oidc/callback
https://rds02-mgmt.hilden.info/oidc/callback
```

Alternativ kann ein zentral verwalteter OIDC-Client mehrere explizit erlaubte Redirect-URIs enthalten, sofern dies in der eigenen PocketID-Konfiguration gewünscht ist.

Für das lokale Agent-UI nur Administratorgruppen freigeben, beispielsweise:

```json
"admin_groups": [
  "sessionguard-admins"
]
```

Das Agent-WebUI selbst lauscht standardmäßig auf Port `9091`. Für einen produktiven OIDC-Login sollte davor HTTPS über einen Reverse Proxy verwendet werden.

---

# Teil C – Docker-Netzwerke

## 6. Netzwerkstruktur härten

Die vorhandene Umgebung verwendet das externe Netzwerk:

```text
aio_proxy
```

Das funktioniert technisch. Bei Guacamole Header Authentication ist aber wichtig, dass kein nicht vertrauenswürdiger Container Guacamole direkt erreichen und selbst einen `X-Guacamole-User`-Header senden kann.

Empfohlen sind daher getrennte Netze:

```text
guacamole_proxy      Traefik <-> Guacamole / ForwardAuth

guacamole_backend    Guacamole <-> guacd / PostgreSQL

sessionguard_control Guacamole <-> SessionGuard Master

sessionguard_backend SessionGuard Master <-> SessionGuard PostgreSQL
```

### 6.1 Gemeinsames privates Control-Netz erstellen

Wenn Guacamole und SessionGuard in unterschiedlichen Compose-Projekten laufen:

```bash
docker network create --internal sessionguard_control
```

In beiden Compose-Dateien anschließend:

```yaml
networks:
  sessionguard_control:
    external: true
    name: sessionguard_control
```

Damit kann die Guacamole-Extension den Master intern erreichen, ohne dessen Broker-API über das Internet aufrufen zu müssen.

### 6.2 Optional: dediziertes Guacamole-Proxy-Netz

Besonders bei Header-Auth empfohlen:

```bash
docker network create guacamole_proxy
```

Traefik, `traefik-forward-auth` und `guacamole` werden an dieses Netzwerk angeschlossen. Andere Stacks sollten dort nicht Mitglied sein.

Wenn der bestehende `aio_proxy` von sehr vielen Containern gemeinsam verwendet wird, sollte die Umstellung auf ein dediziertes Netzwerk als Security-Hardening eingeplant werden.

---

# Teil D – Gitea CI/CD

## 7. CI/CD-Dateien ins Repository übernehmen

Im SessionGuard-Repository sollten vorhanden sein:

```text
.gitea/workflows/release.yml
deploy/guacamole/Dockerfile.guacamole
guacamole-extension/
```

Die Pipeline erzeugt bei Push auf `main`:

```text
git.send.nrw/sendnrw/<repo>:<version>
git.send.nrw/sendnrw/<repo>:latest

git.send.nrw/sendnrw/<repo>-guacamole:<version>
git.send.nrw/sendnrw/<repo>-guacamole:latest
```

Zusätzlich wird veröffentlicht:

```text
sessionguard-guacamole.jar
```

in der Gitea Generic Package Registry.

### 7.1 Gitea Secrets

Im Repository mindestens hinterlegen:

```text
DOCKER_USERNAME
DOCKER_PASSWORD
```

Der verwendete Account benötigt Schreibrechte auf:

- Container Registry
- Generic Package Registry

Später empfiehlt sich eine Trennung in eigene Package-Credentials.

### 7.2 Release-Versionen

Die CI verwendet:

```bash
git describe --tags --always | sed 's/^v//'
```

Für einen sauberen Release:

```bash
git tag v0.5.0
git push origin v0.5.0
git push origin main
```

Ein Commit exakt auf Tag `v0.5.0` erzeugt dann:

```text
git.send.nrw/sendnrw/sessionguard:0.5.0
git.send.nrw/sendnrw/sessionguard-guacamole:0.5.0
```

`latest` wird ebenfalls aktualisiert.

### 7.3 Release prüfen

```bash
docker pull git.send.nrw/sendnrw/sessionguard:0.5.0
docker pull git.send.nrw/sendnrw/sessionguard-guacamole:0.5.0
```

Für Produktion möglichst einen festen Versions-Tag und nicht ausschließlich `latest` verwenden.

---

# Teil E – SessionGuard Master

## 8. Verzeichnisstruktur anlegen

Auf dem Docker-Host beispielsweise:

```bash
mkdir -p /opt/sessionguard
cd /opt/sessionguard
```

Anlegen:

```text
/opt/sessionguard/
├── docker-compose.yml
├── .env
└── master.json
```

---

## 9. `.env` für SessionGuard erstellen

Beispiel:

```dotenv
SESSIONGUARD_VERSION=0.5.0
POSTGRES_VERSION=17

TRAEFIK_NETWORK=aio_proxy
SESSIONGUARD_HOST=sessionguard.hilden.info
TRAEFIK_ENTRYPOINT=websecure
TRAEFIK_CERTRESOLVER=letsencrypt

SESSIONGUARD_DB_NAME=sessionguard
SESSIONGUARD_DB_USER=sessionguard
SESSIONGUARD_DB_PASSWORD=<SEHR_LANGES_DB_PASSWORT>

SESSIONGUARD_ENROLLMENT_TOKEN=<SEPARATES_ENROLLMENT_TOKEN>
SESSIONGUARD_BROKER_API_KEY=<SEPARATER_BROKER_API_KEY>
SESSIONGUARD_OIDC_CLIENT_SECRET=<POCKETID_SESSIONGUARD_CLIENT_SECRET>

# Optional
SESSIONGUARD_ALERT_WEBHOOK_URL=
```

Datei schützen:

```bash
chmod 600 .env
```

---

## 10. `master.json` erstellen

Beispiel für die vorhandene Domain:

```json
{
  "listen": ":8080",
  "public_url": "https://sessionguard.hilden.info",
  "data_file": "/var/lib/sessionguard/master.json",
  "database_url": "",
  "enrollment_token": "SET-BY-SESSIONGUARD_ENROLLMENT_TOKEN",
  "offline_after_seconds": 30,
  "history_limit": 50000,
  "oidc": {
    "issuer": "https://auth.hilden.info",
    "client_id": "<POCKETID_SESSIONGUARD_CLIENT_ID>",
    "client_secret": "SET-BY-SESSIONGUARD_OIDC_CLIENT_SECRET",
    "redirect_url": "https://sessionguard.hilden.info/oidc/callback",
    "admin_groups": [],
    "secure_cookie": true
  },
  "rbac": {
    "default_role": "viewer",
    "groups": {
      "sessionguard-admins": ["admin"],
      "sessionguard-helpdesk": ["helpdesk"],
      "sessionguard-operators": ["operator"],
      "sessionguard-policy": ["policy_admin"],
      "sessionguard-profile": ["profile_admin"],
      "sessionguard-auditors": ["auditor"]
    }
  },
  "broker": {
    "enabled": true,
    "api_key": "SET-BY-SESSIONGUARD_BROKER_API_KEY",
    "lease_seconds": 900,
    "min_health_score": 60,
    "reconnect_existing": true,
    "single_session_per_user": true,
    "default_farm_id": ""
  },
  "alerts": {
    "enabled": true,
    "webhook_url": "",
    "cpu_percent": 90,
    "memory_percent": 90,
    "disk_free_gb": 10,
    "health_score": 50,
    "offline_seconds": 120,
    "profile_failures": 3,
    "disconnected_sessions": 20,
    "logon_duration_seconds": 30,
    "notification_min_interval_seconds": 900
  }
}
```

Die echten Secrets werden bewusst nicht in `master.json` geschrieben, sondern durch ENV-Variablen überschrieben.

---

## 11. SessionGuard Master Compose erstellen

Für die Registry-basierte Installation:

```yaml
services:
  sessionguard-postgres:
    image: postgres:${POSTGRES_VERSION:-17}
    restart: unless-stopped
    environment:
      POSTGRES_DB: ${SESSIONGUARD_DB_NAME:-sessionguard}
      POSTGRES_USER: ${SESSIONGUARD_DB_USER:-sessionguard}
      POSTGRES_PASSWORD: ${SESSIONGUARD_DB_PASSWORD:?required}
    volumes:
      - sessionguard-postgres:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U $${POSTGRES_USER} -d $${POSTGRES_DB}"]
      interval: 5s
      timeout: 5s
      retries: 20
    networks:
      - sessionguard_backend

  sessionguard-master:
    image: git.send.nrw/sendnrw/sessionguard:${SESSIONGUARD_VERSION:-latest}
    restart: unless-stopped
    depends_on:
      sessionguard-postgres:
        condition: service_healthy
    read_only: true
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    environment:
      SESSIONGUARD_DATABASE_URL: postgres://${SESSIONGUARD_DB_USER:-sessionguard}:${SESSIONGUARD_DB_PASSWORD}@sessionguard-postgres:5432/${SESSIONGUARD_DB_NAME:-sessionguard}?sslmode=disable
      SESSIONGUARD_ENROLLMENT_TOKEN: ${SESSIONGUARD_ENROLLMENT_TOKEN}
      SESSIONGUARD_BROKER_API_KEY: ${SESSIONGUARD_BROKER_API_KEY}
      SESSIONGUARD_OIDC_CLIENT_SECRET: ${SESSIONGUARD_OIDC_CLIENT_SECRET}
      SESSIONGUARD_ALERT_WEBHOOK_URL: ${SESSIONGUARD_ALERT_WEBHOOK_URL:-}
    volumes:
      - ./master.json:/etc/sessionguard/master.json:ro
    tmpfs:
      - /tmp:size=16m,mode=1777
    networks:
      - sessionguard_backend
      - sessionguard_control
      - proxy
    labels:
      - traefik.enable=true
      - traefik.docker.network=${TRAEFIK_NETWORK}
      - traefik.http.routers.sessionguard.rule=Host(`${SESSIONGUARD_HOST}`)
      - traefik.http.routers.sessionguard.entrypoints=${TRAEFIK_ENTRYPOINT}
      - traefik.http.routers.sessionguard.tls=true
      - traefik.http.routers.sessionguard.tls.certresolver=${TRAEFIK_CERTRESOLVER}
      - traefik.http.services.sessionguard.loadbalancer.server.port=8080

networks:
  sessionguard_backend:
    internal: true
  sessionguard_control:
    external: true
    name: sessionguard_control
  proxy:
    external: true
    name: ${TRAEFIK_NETWORK:-aio_proxy}

volumes:
  sessionguard-postgres:
```

Wenn `sessionguard_control` noch nicht existiert:

```bash
docker network create --internal sessionguard_control
```

---

## 12. Master starten

```bash
cd /opt/sessionguard
docker compose pull
docker compose up -d
```

Status:

```bash
docker compose ps
```

Logs:

```bash
docker compose logs -f sessionguard-master
```

Health intern testen:

```bash
docker compose exec sessionguard-master \
  wget -qO- http://127.0.0.1:8080/healthz
```

Extern testen:

```text
https://sessionguard.hilden.info
```

Beim ersten Login sollte PocketID erscheinen.

---

## 13. RBAC prüfen

Mindestens zwei Tests durchführen:

1. Benutzer in `sessionguard-admins` anmelden.
2. Benutzer ohne Admin-Gruppe bzw. mit Viewer-Gruppe anmelden.

Der zweite Benutzer darf keine administrativen Änderungen durchführen können.

Erst danach weitere Agents anbinden.

---

# Teil F – Guacamole mit SessionGuard Extension

## 14. Wichtige Korrektur der vorhandenen PocketID-/ForwardAuth-Konfiguration

In der vorhandenen `.env` steht:

```text
TFA_HOST=guacauth.hilden.info
```

Daher muss die PocketID Redirect URI für den **traefik-forward-auth-Client** dazu passen:

```text
https://guacauth.hilden.info/portals/guacamole/oauth2/callback
```

Ein alter Kommentar mit `guac-sso.hilden.info` sollte entfernt werden, damit es später nicht zu Fehlkonfigurationen kommt.

Der SessionGuard Master benötigt **einen eigenen OIDC-Client**. ForwardAuth- und SessionGuard-Client-Secret dürfen nicht identisch sein.

---

## 15. Guacamole-Image austauschen

Im bestehenden Compose nur beim eigentlichen `guacamole`-Service das Image ändern.

Vorher:

```yaml
image: guacamole/guacamole:${GUACAMOLE_VERSION:-1.6.0}
```

Nachher:

```yaml
image: git.send.nrw/sendnrw/sessionguard-guacamole:${SESSIONGUARD_VERSION:-0.5.0}
```

`guac-init` kann weiterhin das offizielle Guacamole-Image verwenden.

---

## 16. SessionGuard-Variablen zu Guacamole ergänzen

Im `guacamole.environment` ergänzen:

```yaml
SESSIONGUARD_MASTER_URL: http://sessionguard-master:8080
SESSIONGUARD_BROKER_API_KEY: ${SESSIONGUARD_BROKER_API_KEY}
SESSIONGUARD_BROKER_TIMEOUT_MS: "2500"
```

In die Guacamole `.env` zusätzlich:

```dotenv
SESSIONGUARD_VERSION=0.5.0
SESSIONGUARD_BROKER_API_KEY=<EXAKT_DERSELBE_BROKER_API_KEY_WIE_AM_MASTER>
```

Der Broker API Key ist **kein Benutzer-Secret**, sondern ein Service-to-Service-Key zwischen Guacamole-Extension und SessionGuard Master.

---

## 17. Guacamole an `sessionguard_control` anschließen

Beim `guacamole`-Service:

```yaml
networks:
  - aio_proxy
  - sessionguard_control
```

Am Ende der Compose-Datei:

```yaml
networks:
  aio_proxy:
    external: true
  sessionguard_control:
    external: true
    name: sessionguard_control
```

Danach kann Guacamole den Master per Docker DNS erreichen:

```text
http://sessionguard-master:8080
```

Wenn Master und Guacamole in unterschiedlichen Compose-Projekten laufen, funktioniert der DNS-Name nur dann zuverlässig, wenn beide Container im gleichen externen Docker-Netz hängen und dort der Name/Alias verfügbar ist. Falls der tatsächliche Containername anders lautet, einen expliziten Alias setzen oder den tatsächlichen DNS-Namen in `SESSIONGUARD_MASTER_URL` verwenden.

---

## 18. Guacamole neu starten

```bash
docker compose pull guacamole
docker compose up -d guacamole
```

Logs prüfen:

```bash
docker compose logs -f guacamole
```

Die SessionGuard-Extension muss beim Guacamole-Start ohne API-/Classloader-Fehler geladen werden.

---

## 19. Header-Auth absichern

Die vorhandene Konfiguration entfernt vor ForwardAuth bereits eingehende Identitätsheader:

```yaml
- traefik.http.middlewares.guac-strip-auth.headers.customrequestheaders.X-Guacamole-User=
- traefik.http.middlewares.guac-strip-auth.headers.customrequestheaders.X-Forwarded-User=
- traefik.http.middlewares.guac-strip-auth.headers.customrequestheaders.X-Authenticated-User=
```

und verwendet anschließend:

```yaml
- traefik.http.routers.guacamole.middlewares=guac-strip-auth,guac-pocketid
```

Das ist richtig gedacht.

Zusätzlich sicherstellen:

- Guacamole-Port `8080` nicht direkt veröffentlichen.
- Guacamole nicht aus beliebigen Container-Netzen erreichbar machen.
- `aio_proxy` möglichst nicht als allgemeines Netz für unzusammenhängende Stacks verwenden.
- `guacd` und Guacamole-PostgreSQL nur in einem internen Backend-Netz betreiben.

Ein direkter Request eines fremden Containers an Guacamole darf nicht durch einen selbst gesetzten `X-Guacamole-User` authentifiziert werden können.

---

# Teil G – Windows Agent

## 20. Windows-Agent bauen

Der aktuelle CI-Workflow baut die Container-Images und die Guacamole-JAR, veröffentlicht aber noch nicht automatisch den Windows-Agent als Release-Artefakt.

Bis dahin Agent auf einem Buildsystem erzeugen:

```powershell
git clone <SESSIONGUARD_REPOSITORY>
cd sessionguard

go mod tidy
go test ./...
go vet ./...

.\scripts\build.ps1
```

Ergebnis:

```text
.\bin\sessionguard-agent.exe
```

und zusätzlich:

```text
.\bin\sessionguard-master-linux-amd64
```

Vor Produktion sollte genau der Commit gebaut werden, aus dem auch Master und Guacamole-Image erzeugt wurden.

---

## 21. Agent-Konfiguration erstellen

Pro Server eine `agent.json` erstellen.

Beispiel für `RDS01`:

```json
{
  "listen": ":9091",
  "public_url": "https://rds01-mgmt.hilden.info",
  "data_dir": "C:\\ProgramData\\SessionGuard",
  "master_url": "https://sessionguard.hilden.info",
  "enrollment_token": "SET-BY-SESSIONGUARD_ENROLLMENT_TOKEN",
  "heartbeat_seconds": 10,
  "oidc": {
    "issuer": "https://auth.hilden.info",
    "client_id": "<POCKETID_AGENT_CLIENT_ID>",
    "client_secret": "SET-BY-SESSIONGUARD_OIDC_CLIENT_SECRET",
    "redirect_url": "https://rds01-mgmt.hilden.info/oidc/callback",
    "admin_groups": [
      "sessionguard-admins"
    ],
    "secure_cookie": true
  },
  "policy": {
    "cleanup": {
      "enabled": true,
      "grace_seconds": 600,
      "poll_seconds": 10,
      "retry_seconds": 60,
      "dry_run": true,
      "exclude_users": [
        "Administrator",
        "DefaultAccount",
        "WDAGUtilityAccount"
      ],
      "exclude_sids": [
        "S-1-5-18",
        "S-1-5-19",
        "S-1-5-20"
      ],
      "allowed_profile_roots": [
        "C:\\Users"
      ]
    },
    "profiles": {
      "enabled": true,
      "store_root": "\\\\fileserver\\SessionGuardProfiles",
      "backup_on_logoff": true,
      "restore_on_logon": false,
      "backup_delay_seconds": 5,
      "retry_seconds": 60,
      "restore_window_seconds": 120,
      "keep_versions": 2,
      "exclude_users": [
        "Administrator"
      ],
      "exclude_sids": [
        "S-1-5-18",
        "S-1-5-19",
        "S-1-5-20"
      ],
      "folders": [
        {
          "path": "AppData\\Roaming\\Example",
          "exclude_globs": [
            "Cache/**",
            "*.tmp"
          ]
        },
        {
          "path": "Desktop"
        },
        {
          "path": "Documents"
        }
      ]
    },
    "sessions": {
      "control_enabled": true,
      "disconnected_logoff_enabled": false,
      "disconnected_timeout_seconds": 3600,
      "exclude_users": [
        "Administrator"
      ],
      "exclude_sids": [
        "S-1-5-18",
        "S-1-5-19",
        "S-1-5-20"
      ]
    },
    "templates": []
  }
}
```

### Warum für die erste Installation `restore_on_logon=false`?

Der sichere Rollout ist:

1. Backup testen.
2. Restore manuell mit Testbenutzer validieren.
3. Erst danach automatischen Restore aktivieren.
4. Cleanup zunächst im Dry-Run lassen.

So existiert immer eine lokale Rückfallmöglichkeit.

---

## 22. Secrets des Agenten über Environment setzen

Empfohlen ist, sensible Werte nicht im JSON zu hinterlegen.

Der Agent unterstützt insbesondere:

```text
SESSIONGUARD_MASTER_URL
SESSIONGUARD_ENROLLMENT_TOKEN
SESSIONGUARD_OIDC_CLIENT_SECRET
```

Bei einem Windows-Dienst müssen diese Werte so gesetzt werden, dass sie im Dienstkontext verfügbar sind. Alternativ kann die initiale Konfigurationsdatei mit strikten ACLs geschützt werden.

Der Installer schützt standardmäßig:

```text
C:\ProgramData\SessionGuard
```

für `SYSTEM` und lokale Administratoren.

---

## 23. gMSA für Profil-Shares vorbereiten

Empfohlenes Dienstkonto beispielsweise:

```text
DOMAIN\svc-sessionguard$
```

Das gMSA benötigt auf dem Profil-Share die erforderlichen Share- und NTFS-Rechte.

Mindestens erforderlich für Backup/Restore:

- Lesen
- Ordner anlegen
- Dateien schreiben
- Dateien ersetzen/umbenennen
- alte Versionen entsprechend Retention löschen

Keine SMB-Passwörter in `agent.json` speichern.

Wenn LocalSystem verwendet wird, greift der Server gegenüber SMB typischerweise über sein Computerkonto zu, beispielsweise:

```text
DOMAIN\RDS01$
```

Dann müssten entsprechend die Computerkonten auf dem Share berechtigt werden.

---

## 24. Agent installieren

Elevated PowerShell:

```powershell
.\scripts\install-agent.ps1 `
  -Binary .\bin\sessionguard-agent.exe `
  -Config .\configs\agent.json
```

Mit gMSA:

```powershell
.\scripts\install-agent.ps1 `
  -Binary .\bin\sessionguard-agent.exe `
  -Config .\configs\agent.json `
  -GmsaAccount "DOMAIN\svc-sessionguard$"
```

Der Installer:

- kopiert die Binary nach `C:\Program Files\SessionGuard`
- legt den State unter `C:\ProgramData\SessionGuard` ab
- installiert den Windows-Dienst `SessionGuardAgent`
- aktiviert Delayed Auto Start
- konfiguriert automatische Service-Recovery
- startet den Dienst

---

## 25. Agent prüfen

```powershell
Get-Service SessionGuardAgent
```

Erwartet:

```text
Status: Running
```

Falls erforderlich Windows Event Log bzw. Dienst-Logs prüfen.

Anschließend Master öffnen:

```text
https://sessionguard.hilden.info
```

Der Server sollte nach Enrollment/Heartbeat in der Serverliste erscheinen.

Ein Agent, der nach `offline_after_seconds` keinen Heartbeat gesendet hat, wird im Director als offline dargestellt.

---

## 26. Lokales Agent-WebUI veröffentlichen

Das Agent-WebUI lauscht standardmäßig:

```text
http://RDS01:9091
```

Produktiv nicht direkt öffentlich ins Internet stellen.

Empfohlen:

```text
Management-Netz / Reverse Proxy / TLS
          |
          v
http://RDS01:9091
```

Windows Firewall beispielsweise nur für die Reverse-Proxy-IP öffnen:

```powershell
New-NetFirewallRule `
  -DisplayName "SessionGuard Agent UI" `
  -Direction Inbound `
  -Action Allow `
  -Protocol TCP `
  -LocalPort 9091 `
  -RemoteAddress <REVERSE_PROXY_IP>
```

---

# Teil H – Farms, Tags und Broker

## 27. Erste Farm anlegen

Im Master-WebUI unter:

```text
Farms & Broker
```

Beispiel:

```text
Name: Office
ID:   wird erzeugt bzw. im UI angezeigt
```

Optional Required Tags:

```text
role=office
environment=production
```

Farm aktivieren.

---

## 28. Agents taggen und einer Farm zuordnen

Server im Master auswählen.

Unter Serversteuerung beispielsweise:

```text
Maintenance Mode: Online

Tags:
role=office
environment=production
location=hilden
```

Optional Farm-ID explizit hinterlegen.

Die explizite Zuordnung hat gegenüber automatisch aus Tags ermittelten Farm-Zuordnungen eine höhere Policy-Relevanz.

---

## 29. Server-Modi verstehen

### Online

```text
Neue Sitzungen:       erlaubt
Reconnect:            erlaubt
```

### Drain

```text
Neue Sitzungen:       nicht auf diesem Host
Reconnect bestehend:  erlaubt
```

Verwenden vor Updates/Wartung.

### Maintenance

```text
Neue Sitzungen:       blockiert
Reconnect:            blockiert
```

Bestehende Windows-Sitzungen werden durch das Umschalten allein nicht automatisch beendet.

### Restart when drained

Aktivieren:

```text
Neu starten sobald leer
```

Dann:

1. Host auf Drain.
2. Keine neuen Sessions mehr.
3. Bestehende Sessions laufen aus.
4. Bei 0 Benutzersitzungen wird ein Restart-Kommando erzeugt.
5. Nach Neustart Agent-Health prüfen.
6. Host wieder auf `online` setzen.

---

## 30. Broker-Grundeinstellungen

In `master.json`:

```json
"broker": {
  "enabled": true,
  "api_key": "SET-BY-SESSIONGUARD_BROKER_API_KEY",
  "lease_seconds": 900,
  "min_health_score": 60,
  "reconnect_existing": true,
  "single_session_per_user": true,
  "default_farm_id": ""
}
```

Empfohlen für das vorhandene Profilmodell:

```text
reconnect_existing = true
single_session_per_user = true
```

Damit versucht SessionGuard zuerst, einen Benutzer auf seine vorhandene aktive/getrennte Sitzung zurückzuführen.

Das reduziert gleichzeitige Profilzustände desselben Benutzers auf mehreren Hosts.

---

# Teil I – Guacamole Published Resources

## 31. Brokered Desktop in Guacamole anlegen

In Guacamole eine normale RDP-Verbindung anlegen.

Beispielname:

```text
Office Desktop
```

Beim RDP Hostname **keinen festen RDS-Host** eintragen, sondern:

```text
${SESSIONGUARD_HOST}
```

RDP-Port bleibt normalerweise:

```text
3389
```

Die bisherige RDP-Credential-Konfiguration bleibt unverändert. SessionGuard entscheidet über den Zielhost, ersetzt aber nicht automatisch die Windows-Anmeldeinformationen.

---

## 32. Passende Resource in SessionGuard anlegen

Im Master-WebUI unter:

```text
Published Resources
```

Beispiel:

```text
Name:                       Office Desktop
Typ:                        desktop
Farm:                       Office
Guacamole Connection Name:  Office Desktop
Enabled:                    ja
```

Wenn möglich langfristig die Guacamole Connection-ID verwenden, da sie gegenüber Umbenennungen stabiler ist. Connection-Namen müssen eindeutig bleiben.

SessionGuard priorisiert beim Matching:

```text
Resource-ID
-> Guacamole Connection-ID
-> Guacamole Connection-Name
```

Doppelte aktive Zuordnungen werden abgelehnt.

---

## 33. RemoteApp veröffentlichen

Guacamole-Verbindung:

```text
hostname:        ${SESSIONGUARD_HOST}
remote-app:      ${SESSIONGUARD_REMOTE_APP}
remote-app-dir:  ${SESSIONGUARD_REMOTE_APP_DIR}
remote-app-args: ${SESSIONGUARD_REMOTE_APP_ARGS}
```

SessionGuard Resource zum Beispiel:

```text
Name:             Sage
Typ:              remoteapp
Farm:             ERP
Remote App:       ||Sage
Remote App Dir:   C:\Program Files\Sage
Remote App Args:  <optional>
```

Die RemoteApp muss auf allen Hosts der betreffenden Farm konsistent bereitgestellt sein.

---

## 34. Broker-Funktion testen

### Test 1 – neue Sitzung

1. Zwei RDS-Hosts auf `online`.
2. Beide in derselben Farm.
3. Beide Health Score >= `min_health_score`.
4. Testbenutzer verbindet sich über die brokered Guacamole-Verbindung.
5. Im Master prüfen, welcher Host ausgewählt wurde.
6. Broker-Lease prüfen.

### Test 2 – Reconnect

1. Benutzer trennt die RDS-Sitzung.
2. Benutzer öffnet dieselbe Guacamole Resource erneut.
3. SessionGuard muss denselben RDS-Host zurückgeben.

### Test 3 – Drain

1. RDS01 auf `drain` setzen.
2. Neuer Benutzer darf nicht neu auf RDS01 platziert werden.
3. Benutzer mit vorhandener Sitzung auf RDS01 darf dort reconnecten.

### Test 4 – Maintenance

1. RDS01 auf `maintenance` setzen.
2. Keine neuen Broker-Verbindungen nach RDS01.
3. Auch vorhandener Reconnect darf nicht dorthin gebrokert werden.

---

# Teil J – Profil-Pipeline

## 35. Profil-Store vorbereiten

Beispiel:

```text
\\fileserver\SessionGuardProfiles
```

SessionGuard synchronisiert bewusst nur ausgewählte Profil-Unterordner.

Geeignete Beispiele:

```text
Desktop
Documents
AppData\Roaming\Hersteller\Produkt
```

Nicht konfigurieren:

```text
NTUSER.DAT
UsrClass.dat
komplettes C:\Users\<user>
Registry-Hives
EFS-Schlüsselmaterial
Windows-interne Profilstrukturen
```

---

## 36. Profilordner konfigurieren

Im Policy-Editor des Master-WebUIs:

```text
Profil-Sicherung & Wiederherstellung
```

Beispiel:

```text
Store Root:                \\fileserver\SessionGuardProfiles
Backup nach Logoff:        ja
Restore bei Logon:         zunächst nein
Backup-Verzögerung:        5 Sekunden
Retry:                     60 Sekunden
Restore-Fenster:           120 Sekunden
Historische Versionen:     2
```

Profilordner beispielsweise:

```text
Desktop
Documents
AppData\Roaming\Example
```

Exclude-Globs für einen Ordner:

```text
Cache/**
*.tmp
```

---

## 37. Backup zuerst ohne Cleanup-Risiko testen

Zunächst:

```text
Backup:               aktiviert
Restore:              deaktiviert
Cleanup:              aktiviert
Cleanup Dry-Run:      aktiviert
Auto-Logoff:          deaktiviert
```

Testablauf:

1. Testbenutzer anmelden.
2. Testdatei in einen synchronisierten Ordner schreiben.
3. Benutzer sauber abmelden.
4. Master öffnen.
5. Aktivitäts-/Profiljob-Log prüfen.
6. SMB-Store prüfen.
7. Sicherstellen, dass ein `current`-Snapshot vorhanden ist.
8. Dry-Run-Eintrag für geplanten Cleanup prüfen.

Wichtig:

> Schlägt das Backup fehl, darf SessionGuard das lokale Profil nicht löschen.

---

## 38. Restore aktivieren

Erst nach erfolgreichem Backup-Test:

```text
Restore bei Logon = aktiviert
```

Test:

1. Testbenutzer abmelden.
2. Gesicherte Datei lokal gezielt verändern/entfernen, ohne den Backup-Store zu verändern.
3. Benutzer erneut anmelden.
4. Prüfen, dass der letzte `current`-Snapshot innerhalb des Restore-Fensters eingespielt wurde.
5. Master-Aktivitätslog prüfen.

Der Restore arbeitet als Merge und nicht als vollständiger Profil-Mirror.

---

## 39. Cleanup produktiv aktivieren

Erst wenn Backup und Restore mehrfach erfolgreich getestet wurden:

```text
Cleanup Dry-Run = aus
```

Empfohlener Startwert:

```text
Grace: 600 Sekunden
```

Pipeline:

```text
Logoff
  |
  +-- Backup Delay
  |
  +-- Backup nach staging
  |
  +-- Benutzer erneut aktiv? -> Abbruch
  |
  +-- staging -> current
  |
  +-- Cleanup Grace
  |
  +-- finaler WTS-Recheck
  |
  +-- DeleteProfileW
```

Ein Reconnect blockiert gefährliche Operationen.

---

# Teil K – Disconnect Auto-Logoff

## 40. Getrennte Sitzungen zunächst beobachten

Im Director prüfen:

- welche Anwendungen häufig getrennt bleiben
- typische Disconnect-Dauer
- ob bestimmte Service-/Admin-Benutzer ausgeschlossen werden müssen

Dann Timeout definieren, zum Beispiel:

```text
3600 Sekunden = 1 Stunde
```

---

## 41. Auto-Logoff aktivieren

Policy:

```text
Sitzungssteuerung erlauben:                 ja
Getrennte Sitzungen automatisch abmelden:   ja
Disconnect Timeout:                         3600
```

Wichtig:

Der automatische Logoff verwendet dieselbe Nachbearbeitung wie ein manueller Logoff:

```text
Disconnected
   |
Timeout erreicht
   |
WTSLogoffSession
   |
Profil-Backup
   |
Cleanup Grace
   |
Profil-Cleanup
```

Damit wird ein Benutzerprofil nicht einfach gelöscht, bevor die Sicherung abgeschlossen wurde.

---

# Teil L – Templates

## 42. Templates konfigurieren

Im Policy-Editor können folgende Typen angelegt werden:

```text
Datei
Ordner
URL-Verknüpfung
Windows .lnk
```

### URL-Beispiel

```text
ID:       support-url
Typ:      URL
Ziel:     Desktop\Support.url
URL:      https://support.example.org
Overwrite: ja
```

### Datei von SMB

```text
ID:       settings-file
Typ:      Datei
Ziel:     AppData\Roaming\Example\defaults.json
Quelle:   \\fileserver\sessionguard-templates\defaults.json
Overwrite: ja
```

### Windows Shortcut

```text
ID:          company-shortcut
Typ:         Shortcut
Ziel:        Desktop\Fachanwendung.lnk
Target:      C:\Program Files\Example\app.exe
Description: Fachanwendung
Overwrite:   ja
```

Templates werden geprüft und nur bei Bedarf erzeugt/aktualisiert.

---

# Teil M – Director und Session-Steuerung

## 43. Director prüfen

Der Master zeigt pro Server unter anderem:

```text
Online/Offline
Maintenance Mode
Health Score
CPU
RAM
Systemdisk
RDP Listener
Profile Store
aktive Sitzungen
getrennte Sitzungen
Profiljobs
Cleanupjobs
Prozesse
```

Pro Session:

```text
Session-ID
Benutzer
Status
Logon-Zeit
Idle-Zeit
Clientname
Clientadresse
```

---

## 44. Sitzung steuern

Je nach RBAC-Rolle sind verfügbar:

```text
Nachricht senden
Trennen
Abmelden
Prozess beenden
```

Prozessbeendigung wird zusätzlich gegen PID-Wiederverwendung abgesichert, indem der Agent PID und Session-ID vor `TerminateProcess` erneut verifiziert.

Administrative Aktionen werden im Audit-Log protokolliert.

---

# Teil N – Policies und Rollback

## 45. Policy-Ebenen

Die Policy-Priorität lautet:

```text
Agent Override
    > explizite Farm-Zuordnung
    > zentral/tagbasiert ermittelte Farm
    > globale Policy
```

Empfehlung:

- globale Baseline für alle RDS-Hosts
- Farm-Policy für Office/ERP/etc.
- Agent-Override nur für begründete Ausnahmen

---

## 46. Policy-Änderungen ausrollen

Für Änderungen zunächst einen Canary-Host verwenden.

Beispiel:

1. Policy nur auf RDS03 anwenden.
2. mindestens mehrere echte Logons/Logoffs beobachten.
3. Director, Profiljobs und Alerts prüfen.
4. danach auf Farm oder alle Server ausrollen.

Das WebUI bietet:

```text
Für diesen Server speichern
Auf alle anwenden
Editor neu laden
```

---

## 47. Policy-Rollback

Unter:

```text
Policy-Historie & Rollback
```

alte Revision auswählen und:

```text
Rollback
```

verwenden.

Der Rollback löscht die Historie nicht, sondern erzeugt eine neue aktuelle Revision auf Basis der gewählten älteren Version.

---

# Teil O – Alerts und Monitoring

## 48. Alert-Grenzwerte konfigurieren

Beispiel:

```json
"alerts": {
  "enabled": true,
  "cpu_percent": 90,
  "memory_percent": 90,
  "disk_free_gb": 10,
  "health_score": 50,
  "offline_seconds": 120,
  "profile_failures": 3,
  "disconnected_sessions": 20,
  "logon_duration_seconds": 30,
  "notification_min_interval_seconds": 900
}
```

Damit werden unter anderem überwacht:

```text
Agent offline
CPU hoch
RAM hoch
Disk knapp
Health Score zu niedrig
Profilfehler
zu viele getrennte Sessions
langsame beobachtete Logons
```

---

## 49. Webhook konfigurieren

Optional:

```dotenv
SESSIONGUARD_ALERT_WEBHOOK_URL=https://example.org/sessionguard-webhook
```

SessionGuard sendet JSON-Webhooks.

Die Webhook-Zustellung ist Best Effort und ersetzt kein unabhängiges Infrastrukturmonitoring.

---

## 50. Prometheus-Metriken

Der Master stellt bereit:

```text
/metrics
```

und Health:

```text
/healthz
```

Beide Endpunkte in das bestehende Monitoring integrieren.

---

# Teil P – PostgreSQL und Backup

## 51. PostgreSQL sichern

SessionGuard speichert dort unter anderem:

```text
Agents
Farms
Resources
Broker-Leases
Policy-State
Policy-Historie
Audit
Session-Historie
Alerts
```

Regelmäßiges Backup beispielsweise mit `pg_dump`:

```bash
docker compose exec -T sessionguard-postgres \
  pg_dump -U sessionguard -d sessionguard -Fc \
  > sessionguard-$(date +%F).dump
```

Das Profil-Share wird **nicht** in PostgreSQL gespeichert und muss separat gesichert werden.

---

## 52. Master-HA beachten

v0.3 ist als **Single-Active-Master** ausgelegt.

Der Master hält einen PostgreSQL Advisory Lock. Ein zweiter aktiver Master gegen dieselbe DB soll dadurch nicht gleichzeitig als Control Plane arbeiten.

Für manuellen Failover:

1. alten Master stoppen bzw. dessen Ausfall sicher feststellen
2. PostgreSQL-Verfügbarkeit prüfen
3. Ersatz-Master gegen dieselbe DB starten
4. `/healthz` prüfen
5. Agent-Heartbeats prüfen
6. Broker-Test durchführen

Kein Active/Active-Deployment konfigurieren.

---

# Teil Q – Produktions-Rollout

## 53. Empfohlene Rollout-Reihenfolge

### Phase 1 – Infrastruktur

- [ ] SessionGuard PostgreSQL bereitstellen
- [ ] SessionGuard Master bereitstellen
- [ ] HTTPS/Traefik konfigurieren
- [ ] PocketID OIDC testen
- [ ] RBAC testen
- [ ] PostgreSQL-Backup testen

### Phase 2 – erster Agent

- [ ] einen Canary-RDS-Host wählen
- [ ] Agent installieren
- [ ] Heartbeat prüfen
- [ ] Director-Telemetrie prüfen
- [ ] Cleanup Dry-Run aktiv lassen
- [ ] Auto-Logoff deaktiviert lassen

### Phase 3 – Guacamole Broker

- [ ] SessionGuard-Guacamole-Image deployen
- [ ] Extension-Startup prüfen
- [ ] Canary-Farm erstellen
- [ ] eine Published Resource erstellen
- [ ] `${SESSIONGUARD_HOST}` in Guacamole verwenden
- [ ] neue Sitzung testen
- [ ] Reconnect testen

### Phase 4 – zweite RDS-Node

- [ ] zweiten Agent hinzufügen
- [ ] gleiche Farm
- [ ] Broker-Verteilung testen
- [ ] Drain testen
- [ ] Maintenance testen
- [ ] Restart when drained testen

### Phase 5 – Profile

- [ ] Profil-Backup aktivieren
- [ ] Backup mehrfach testen
- [ ] SMB-Ausfall simulieren
- [ ] sicherstellen, dass Cleanup bei Backupfehler blockiert bleibt
- [ ] Restore aktivieren
- [ ] Restore mehrfach testen
- [ ] Reconnect während Backup testen
- [ ] erst danach Cleanup Dry-Run deaktivieren

### Phase 6 – Session Lifecycle

- [ ] Disconnect-Dauer beobachten
- [ ] sinnvollen Timeout festlegen
- [ ] Ausschlussbenutzer festlegen
- [ ] Auto-Logoff aktivieren
- [ ] Backup-Pipeline nach Auto-Logoff prüfen

### Phase 7 – breite Einführung

- [ ] weitere Farms anlegen
- [ ] weitere RDS-Hosts aufnehmen
- [ ] Policies zentralisieren
- [ ] Alerts anbinden
- [ ] Monitoring anbinden
- [ ] Betriebsdokumentation/Notfallzugang dokumentieren

---

# Teil R – Funktionstests vor Produktivfreigabe

## 54. Mindest-Testmatrix

### Broker

- [ ] neue Verbindung wird auf gesunden Online-Host gelegt
- [ ] Benutzer reconnectet auf seinen vorhandenen Host
- [ ] Drain blockiert neue Sessions
- [ ] Drain erlaubt Reconnect
- [ ] Maintenance blockiert Brokerzugriff
- [ ] Farm-Isolation funktioniert
- [ ] Lease-Race bei zwei fast gleichzeitigen Verbindungsversuchen funktioniert

### Session Control

- [ ] Nachricht
- [ ] Disconnect
- [ ] Logoff
- [ ] getrennte Sessions gesammelt abmelden
- [ ] Prozess beenden
- [ ] Audit-Eintrag vorhanden

### Profile

- [ ] normaler Backup-Job
- [ ] Restore
- [ ] Snapshot-History
- [ ] SMB-Ausfall
- [ ] Reconnect während Backup
- [ ] Reconnect unmittelbar vor Cleanup
- [ ] Cleanup Dry-Run
- [ ] echter Cleanup erst nach erfolgreichem Backup

### Director

- [ ] Logon erscheint
- [ ] Disconnect erscheint
- [ ] Reconnect erscheint
- [ ] Logoff erscheint
- [ ] Prozesse sichtbar
- [ ] CPU/RAM/Disk sichtbar
- [ ] Agent Offline Alert funktioniert

### Security

- [ ] Viewer kann nicht administrieren
- [ ] Helpdesk-Rechte sind begrenzt
- [ ] Operator kann Maintenance/Sessions steuern
- [ ] Policy Admin kann Policy ändern/rollbacken
- [ ] fremder Container kann Guacamole nicht per gefälschtem `X-Guacamole-User` umgehen

---

# Teil S – Updates

## 55. Master aktualisieren

Vorher PostgreSQL sichern.

Dann neuen Tag setzen, beispielsweise:

```dotenv
SESSIONGUARD_VERSION=0.5.0
```

Update:

```bash
docker compose pull sessionguard-master
docker compose up -d sessionguard-master
```

Prüfen:

```bash
docker compose logs --tail=200 sessionguard-master
```

und:

```text
/healthz
OIDC Login
Dashboard
Agent Heartbeats
Broker
```

---

## 56. Guacamole Extension aktualisieren

Gleichen SessionGuard-Release-Tag verwenden:

```dotenv
SESSIONGUARD_VERSION=0.5.0
```

Dann:

```bash
docker compose pull guacamole
docker compose up -d guacamole
```

Guacamole-Logs unmittelbar prüfen.

Master und Guacamole-Extension sollten möglichst aus demselben SessionGuard-Release stammen.

---

## 57. Windows Agents aktualisieren

Pro Farm in Wellen:

1. Host auf `drain` setzen.
2. Sitzungen auslaufen lassen.
3. optional `restart_when_drained` verwenden.
4. neuen Agent-Build kopieren/installieren.
5. State unter `C:\ProgramData\SessionGuard` erhalten.
6. Dienst prüfen.
7. Heartbeat/Health im Master prüfen.
8. Host wieder `online` setzen.
9. nächster Host.

Installer erneut ausführen:

```powershell
.\scripts\install-agent.ps1 `
  -Binary .\bin\sessionguard-agent.exe `
  -Config .\configs\agent.json `
  -GmsaAccount "DOMAIN\svc-sessionguard$"
```

---

# Teil T – Troubleshooting

## 58. Agent erscheint nicht im Master

Prüfen:

```text
Master URL korrekt?
DNS vom RDS-Host?
HTTPS-Zertifikat vertrauenswürdig?
Enrollment Token identisch?
Firewall/Proxy?
SessionGuardAgent läuft?
```

PowerShell:

```powershell
Get-Service SessionGuardAgent
Test-NetConnection sessionguard.hilden.info -Port 443
```

Master-Logs:

```bash
docker compose logs -f sessionguard-master
```

---

## 59. Guacamole meldet Brokerfehler

Prüfen:

```text
SessionGuard Master erreichbar?
SESSIONGUARD_BROKER_API_KEY identisch?
Guacamole und Master im sessionguard_control Netzwerk?
Farm aktiviert?
Resource aktiviert?
Resource korrekt mit Guacamole Connection gemappt?
mindestens ein gesunder Online-Host in Farm?
```

Netzwerk prüfen:

```bash
docker network inspect sessionguard_control
```

Guacamole-Logs:

```bash
docker compose logs -f guacamole
```

---

## 60. Broker findet keinen Server

Prüfen:

```text
Farm enabled?
Agent online?
Agent in Farm?
Required Tags erfüllt?
Maintenance Mode = online?
Health Score >= min_health_score?
Resource zeigt auf richtige Farm?
```

Ein Host auf `drain` ist nur für Reconnects geeignet, nicht für neue Benutzer.

Ein Host auf `maintenance` wird auch für Reconnect nicht verwendet.

---

## 61. Profil-Backup schlägt fehl

Prüfen:

- Dienstkonto des Agenten
- Share-Rechte
- NTFS-Rechte
- DNS zum Fileserver
- SMB-Erreichbarkeit
- freier Speicher
- gesperrte Dateien
- Pfad in `store_root`

Mit gMSA besonders wichtig: Tests müssen im Kontext des Dienstkontos betrachtet werden, nicht nur mit dem eigenen Admin-Account.

Bei Backupfehler darf der Cleanup nicht fortgesetzt werden.

---

## 62. Restore wird nicht ausgeführt

Prüfen:

```text
restore_on_logon=true?
current Snapshot vorhanden?
SMB erreichbar?
Benutzer/SID ausgeschlossen?
Restore-Fenster überschritten?
Agent während Logon aktiv?
```

Ein Restore, der das konfigurierte Restore-Fenster überschritten hat, wird bewusst nicht verspätet in eine bereits laufende Sitzung injiziert.

---

## 63. OIDC Redirect-Fehler

Die Redirect URI muss Zeichen für Zeichen zum PocketID-Client passen.

Master:

```text
https://sessionguard.hilden.info/oidc/callback
```

ForwardAuth entsprechend der vorhandenen ENV:

```text
https://guacauth.hilden.info/portals/guacamole/oauth2/callback
```

Agent beispielsweise:

```text
https://rds01-mgmt.hilden.info/oidc/callback
```

Außerdem `public_url`, `redirect_url`, DNS und TLS prüfen.

---

## 64. PostgreSQL startet nicht

```bash
docker compose logs sessionguard-postgres
```

Prüfen:

```text
SESSIONGUARD_DB_PASSWORD gesetzt?
Volume beschreibbar?
Disk voll?
PostgreSQL-Volume aus inkompatibler Major-Version?
```

PostgreSQL Major Upgrades nicht durch simples Austauschen von `17` auf eine spätere Major-Version durchführen; dafür das normale PostgreSQL-Upgradeverfahren verwenden.

---

# Teil U – Empfohlene Produktionswerte

## 65. Startwerte

Diese Werte sind ein sinnvoller Ausgangspunkt, müssen aber zur eigenen Umgebung passen:

| Einstellung | Startwert |
|---|---:|
| Agent Heartbeat | 10 s |
| Agent offline nach | 30 s |
| Broker Lease | 900 s |
| Broker min. Health | 60 |
| Profil Backup Delay | 5 s |
| Profil Retry | 60 s |
| Restore Window | 120 s |
| Snapshot History | 2 |
| Cleanup Grace | 600 s |
| Cleanup Poll | 10 s |
| Cleanup Retry | 60 s |
| Disconnect Auto-Logoff | zunächst aus |
| Disconnect Timeout | später z. B. 3600 s |
| CPU Alert | 90 % |
| RAM Alert | 90 % |
| Disk Alert | < 10 GB |
| Alert Repeat Minimum | 900 s |

---

# Teil V – Go-Live-Checkliste

## 66. Go-Live

Vor dem breiten Rollout müssen alle Punkte erfüllt sein:

- [ ] SessionGuard Master nur per HTTPS administrierbar
- [ ] getrennte Secrets für DB/OIDC/Broker/Enrollment
- [ ] RBAC-Gruppen eingerichtet
- [ ] PostgreSQL-Backup erfolgreich getestet
- [ ] SessionGuard Master Restore-Verfahren dokumentiert
- [ ] Guacamole Header-Auth kann nicht direkt aus fremden Netzen umgangen werden
- [ ] Guacamole Extension lädt fehlerfrei
- [ ] Brokered Desktop funktioniert
- [ ] Reconnect Affinity funktioniert
- [ ] Drain funktioniert
- [ ] Maintenance funktioniert
- [ ] mindestens zwei RDS-Hosts in einer Canary-Farm getestet
- [ ] Agent-Dienst Recovery getestet
- [ ] Profil-Backup mehrfach erfolgreich
- [ ] Profil-Restore mehrfach erfolgreich
- [ ] SMB-Ausfall getestet
- [ ] Reconnect während Profil-Backup getestet
- [ ] Cleanup Dry-Run überprüft
- [ ] echter Profil-Cleanup nur nach erfolgreichem Backup aktiviert
- [ ] Auto-Logoff separat getestet
- [ ] Audit-Log geprüft
- [ ] Alerts geprüft
- [ ] `/healthz` überwacht
- [ ] `/metrics` überwacht
- [ ] Break-Glass-RDP-Zugang dokumentiert
- [ ] Upgrade- und Rollback-Verfahren dokumentiert

---

# 67. Empfohlene Reihenfolge für diese konkrete Umgebung

Für die vorhandene Guacamole/PocketID-Installation würde ich genau so vorgehen:

```text
1. sessionguard.hilden.info in DNS anlegen
2. separaten PocketID OIDC Client für SessionGuard erstellen
3. RBAC-Gruppen in PocketID erstellen/zuweisen
4. sessionguard_control Docker-Netz erstellen
5. SessionGuard PostgreSQL + Master deployen
6. OIDC/RBAC im Master prüfen
7. einen RDS-Canary auswählen
8. Agent dort installieren
9. Director/Health prüfen
10. Office-Canary-Farm erstellen
11. Guacamole auf sessionguard-guacamole:<version> umstellen
12. Guacamole mit sessionguard_control verbinden
13. eine neue Guacamole-Testconnection mit ${SESSIONGUARD_HOST} erstellen
14. passende Published Resource in SessionGuard erstellen
15. Broker-Verbindung testen
16. Disconnect/Reconnect testen
17. zweiten Canary-RDS-Host hinzufügen
18. Load Placement testen
19. Drain/Maintenance testen
20. Profil-Backup testen
21. Restore testen
22. Cleanup Dry-Run beobachten
23. Cleanup aktivieren
24. Disconnect Auto-Logoff aktivieren
25. danach schrittweise die restlichen RDS-Hosts aufnehmen
```

Damit wird nicht gleichzeitig Gateway, Broker, Profilmanagement und Session-Lifecycle produktiv umgestellt. Jede Schicht kann einzeln validiert und bei Problemen zurückgenommen werden.

---

# 68. Wichtige Betriebsgrenzen

SessionGuard ersetzt in diesem Stand nicht:

- ICA/HDX
- den Windows RDS Session Host
- einen vollständigen Windows Profile Provider wie FSLogix/UPM
- Active/Active-Master-HA
- die eigentliche RDP-Credential-Authentifizierung

SessionGuard übernimmt stattdessen gezielt:

```text
Broker
Reconnect Affinity
Farms
Drain / Maintenance
Director
Session Control
Profile Folder Lifecycle
Templates
Policies
RBAC
Audit
Alerts
```

Guacamole bleibt das Gateway und Windows bleibt die Session Runtime.

---

## 69. Relevante Projektdateien

Im Repository:

```text
README.md
configs/master.example.json
configs/agent.example.json
deploy/docker-compose.yml
deploy/.env.example
deploy/guacamole/Dockerfile.guacamole
deploy/guacamole/docker-compose.sessionguard.example.yml
deploy/guacamole/CI-CD.md
scripts/build.ps1
scripts/install-agent.ps1
docs/ARCHITECTURE.md
docs/BROKER.md
docs/DIRECTOR.md
docs/GUACAMOLE.md
docs/OPERATIONS.md
docs/PROFILE-LIFECYCLE.md
docs/RBAC.md
docs/SECURITY.md
docs/TESTING.md
```

Diese Anleitung sollte zusammen mit `docs/TESTING.md` als Go-Live-Grundlage verwendet werden.

## SessionGuard-managed RemoteApps (v0.4)

This mode is intended for RD Session Hosts where you want SessionGuard Agents to maintain the local RemoteApp allow-list instead of manually publishing the same alias on every farm member. It uses the documented Terminal Services WMI provider in `root\CIMv2\TerminalServices`. Administrator rights are required for changes; the normal SessionGuard Windows service account must therefore retain its existing local system/administrative privileges.

Before enabling it for a production farm, verify the provider on a canary RDS host:

```powershell
Get-WmiObject `
  -Namespace 'root\cimv2\TerminalServices' `
  -Class Win32_TSPublishedApplication `
  -Authentication PacketPrivacy |
  Select-Object Alias,Path,PathExists,CommandLineSetting
```

An empty result is valid when no RemoteApps are published. A class/provider error means this SessionGuard mode should not be enabled on that host until the Windows RDS installation is corrected.

In **Master → Apps & Desktops**, create/edit a Resource:

```text
Type:                         RemoteApp
Farm:                         <target farm>
RemoteApp Alias:              ||Sage
Agent-managed publication:    enabled
Executable:                   C:\Program Files\Sage\Sage.exe
Command-line policy:          deny / allow / require
```

The executable must exist at the configured path on each eligible farm member. The Agent reconciles on desired-state changes and periodically thereafter. The Master UI shows per-host readiness. A managed RemoteApp remains unavailable on any host that has not yet reported a healthy synchronized registration.

SessionGuard only removes aliases that the same Agent originally created and recorded as owned. Existing manually published RemoteApps are discovered and can be adopted for desired-state checks, but are not deleted when management is later disabled. Canary-test this behavior on the exact Windows Server version used in your environment.

Because v0.4 changes the heartbeat protocol to version 4, upgrade Master and Agents as one coordinated rollout. A v0.3 Agent will be rejected by a v0.4 Master with a protocol-version mismatch until upgraded.

