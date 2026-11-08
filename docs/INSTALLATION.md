# SessionGuard – Installation und Konfiguration

> Schritt-für-Schritt-Anleitung für den Betrieb von SessionGuard als RDS-Control-Plane mit Apache Guacamole 1.6, PostgreSQL und Windows Remote Desktop Session Hosts. Referenz-Deployment: **Caddy Sentinel** auf dem Host (Reverse-Proxy und OIDC-Provider mit AD-/LDAP-Anmeldung), der SessionGuard-Stack in Docker.

Alle Hostnamen (`sso.example.org`, `sessionguard.example.org`, `rds.example.org`) und Secrets sind Platzhalter.

---

## 1. Zielbild

```text
Internet / LAN
   |
   v
Caddy Sentinel (Host): TLS, WAF/IDS, Ratelimits, OIDC-Server ── AD/LDAP
   |  sso.example.org            → Anmeldung (Domänenkonten + 2. Faktor)
   |  sessionguard.example.org   → 127.0.0.1:8080 SessionGuard Master
   |  rds.example.org            → forward_auth → 127.0.0.1:8081 Guacamole
   v
Docker (deploy/docker-compose.yml)
   Guacamole + SessionGuard-Extension ── guacd ──RDP──► RDS01..RDSn
   SessionGuard Master ── PostgreSQL          ▲
   Guacamole-PostgreSQL                       │ Agent-Heartbeats
                                              └── https://sessionguard.example.org
```

| Komponente | Aufgabe |
|---|---|
| Caddy Sentinel | TLS, Reverse-Proxy, WAF/IDS, OIDC-Server; meldet Benutzer mit AD-Konten an |
| SessionGuard Master | Broker, Director, Farms, Policies, RBAC, Alerts, Audit, ForwardAuth für Guacamole |
| Guacamole + SessionGuard Extension | HTML5/RDP-Gateway; fragt beim Verbindungsaufbau den Broker nach dem Ziel-Host |
| SessionGuard Agent | lokale RDS-/WTS-Steuerung, Profil-Pipeline, Templates und Telemetrie |
| PostgreSQL | persistenter State von SessionGuard bzw. Guacamole |
| Windows RDS | eigentliche Benutzer-Sitzungen |

---

# Teil A – Voraussetzungen

## 2. Voraussetzungen prüfen

### 2.1 Linux-Host

- Caddy Sentinel installiert (systemd), mit Active Directory verbunden (*Site-Login & Konten › Verzeichnis (AD)*)
- Docker Engine mit Compose-Plugin
- DNS: `sso.example.org`, `sessionguard.example.org`, `rds.example.org` zeigen auf den Host
- Netzwerkweg vom Host zu den RDS-Hosts (TCP 3389) für guacd

### 2.2 Windows-Seite

- Windows Server 2019/2022/2025 mit RD Session Host
- Weg zu `https://sessionguard.example.org` (Agent-Heartbeats)
- für SMB-Profilspeicher: gMSA mit Share-/NTFS-Rechten

### 2.3 Build-Voraussetzungen (nur für eigene Builds)

- Go 1.26+, JDK 11+ und Maven für die Guacamole-Extension
- sonst die Images aus GHCR verwenden (`deploy/guacamole/CI-CD.md`)

---

# Teil B – Secrets

## 3. SessionGuard-Secrets erzeugen

Jedes Secret einzeln erzeugen (`openssl rand -hex 32`):

| Secret | Weg |
|---|---|
| `SESSIONGUARD_ENROLLMENT_TOKEN` | Agent → Master (Erstregistrierung) |
| `SESSIONGUARD_BROKER_API_KEY` | Guacamole-Extension → Master |
| `SESSIONGUARD_PROXY_SECRET` | Caddy → Guacamole-Extension (vertrauenswürdige Identitäts-Header) |
| `SESSIONGUARD_DB_PASSWORD`, `GUAC_DB_PASSWORD` | Datenbanken |
| `SESSIONGUARD_OIDC_CLIENT_SECRET` | aus Sentinel, Anwendung `sessionguard-admin` |
| `SESSIONGUARD_ACCESS_OIDC_CLIENT_SECRET` | aus Sentinel, Anwendung `sessionguard-access` |

---

# Teil C – Caddy Sentinel

## 4. Policies, Sites und OIDC-Server

Vollständig beschrieben in [SENTINEL.md](SENTINEL.md), Abschnitt 1:

1. Policies `sso` (Anmeldung mit Domänenkonten, zweiter Faktor), `sessionguard` (IP-Freigaben für Admin- und RDS-Netze) und `rds` (ohne Sentinel-Anmeldung) anlegen.
2. `deploy/Caddyfile.sentinel` als `/etc/caddy/sites/sessionguard.caddy` übernehmen, Hostnamen anpassen.
3. `/etc/caddy/sessionguard-proxy.secret` mit `SESSIONGUARD_PROXY_SECRET` anlegen (`0640 root:caddy`).
4. OIDC-Server mit der Anmelde-Adresse `https://sso.example.org` einschalten und die Anwendungen `sessionguard-admin` und `sessionguard-access` anlegen (PKCE und Refresh-Tokens an).

### 4.1 Empfohlene AD-Gruppen

| AD-Gruppe | Zweck |
|---|---|
| `SessionGuard-Admins` | Rolle `admin` |
| `SessionGuard-Operators` | Rolle `operator` |
| `SessionGuard-Helpdesk` | Rolle `helpdesk` |
| `SessionGuard-Auditors` | Rolle `auditor` |
| `RDS-Benutzer` | darf Guacamole nutzen (`access_auth.allowed_groups`, erlaubte Gruppen der Anwendung) |
| `guac-…` | Guacamole-Benutzergruppen (Gruppen-Sync, Rechte auf Verbindungen) |

Sentinel liefert die **CN-Namen** der Gruppen im Claim `groups`.

## 5. OIDC für die lokalen Agent-WebUIs (optional)

In Sentinel eine weitere Anwendung `sessionguard-agent` anlegen, je Agent eine Redirect-URI `https://<agent-host>/oidc/callback`, erlaubte Gruppen z. B. `SessionGuard-Admins`.

---

# Teil D – CI/CD

## 6. Images

Die GitHub-Actions-Workflows bauen `sessionguard` und `sessionguard-guacamole` und veröffentlichen sie in GHCR. Details: `deploy/guacamole/CI-CD.md`.

---

# Teil E – SessionGuard-Stack

## 7. Stack konfigurieren

```bash
cd deploy
cp .env.example .env
cp master.json.example master.json
chmod 600 .env master.json
```

- `.env`: Images, Secrets aus Teil B, `GUAC_ADMIN_USER` (AD-Benutzername in Kleinbuchstaben, ersetzt `guacadmin`), `SENTINEL_ISSUER_HOST`.
- `master.json`: Hostnamen, Client-IDs, `rbac.groups`, `access_auth.allowed_groups`, `guacamole_groups`.
- Interne CA von Sentinel für das Zertifikat der Anmelde-Adresse: Root-Zertifikat nach `deploy/ca/`.

## 8. Stack starten

```bash
docker compose up -d
docker compose ps
docker compose logs --tail=100 sessionguard-master
```

Erwartung: `master … listening on :8080 (store=postgres)` ohne OIDC-Fehler. Danach:

```bash
curl -s https://sessionguard.example.org/healthz
curl -s https://sso.example.org/.well-known/openid-configuration
```

## 9. RBAC prüfen

Mit je einem Konto pro Rolle an `https://sessionguard.example.org/` anmelden. Ein Konto ohne passende Gruppe sieht bereits in Sentinel „Dein Konto hat keinen Zugriff“.

---

# Teil F – Guacamole

## 10. Guacamole-Anmeldung prüfen

1. `https://rds.example.org/` aufrufen → Sentinel-Anmeldeseite.
2. Mit AD-Konto und zweitem Faktor anmelden → Guacamole mit dem AD-Benutzernamen.
3. `guacadmin` ist gelöscht; der in `GUAC_ADMIN_USER` gesetzte Benutzer ist Guacamole-Administrator.
4. Guacamole-Benutzergruppen mit den Namen der `guac-…`-Gruppen anlegen und Verbindungen den Gruppen zuweisen.

## 11. Header-Auth absichern

- Guacamole und Master veröffentlichen ihre Ports nur auf `127.0.0.1`.
- Die Extension akzeptiert Identitäts-Header nur zusammen mit `SESSIONGUARD_PROXY_SECRET`. Prüfen auf dem Host:

```bash
curl -s -H 'X-Guacamole-User: guacadmin' -X POST http://127.0.0.1:8081/api/tokens
# erwartet: keine Anmeldung (403/401), im Guacamole-Log
# "SessionGuard ignored identity header from a request without valid proxy secret"
```

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
  "public_url": "https://rds01-mgmt.example.org",
  "data_dir": "C:\\ProgramData\\SessionGuard",
  "master_url": "https://sessionguard.example.org",
  "enrollment_token": "SET-BY-SESSIONGUARD_ENROLLMENT_TOKEN",
  "heartbeat_seconds": 10,
  "oidc": {
    "issuer": "https://sso.example.org",
    "client_id": "sessionguard-agent",
    "client_secret": "SET-BY-SESSIONGUARD_OIDC_CLIENT_SECRET",
    "redirect_url": "https://rds01-mgmt.example.org/oidc/callback",
    "admin_groups": [
      "SessionGuard-Admins"
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
https://sessionguard.example.org
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
- [ ] Caddy Sentinel: Policies, Sites, OIDC-Server
- [ ] AD-Login über Sentinel testen (Admin-UI und Guacamole)
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
- [ ] gefälschter `X-Guacamole-User` – über Caddy und direkt auf `127.0.0.1:8081` – meldet niemanden an
- [ ] Konto in Sentinel abmelden → SessionGuard-Sitzungen enden nach `revalidate_minutes`

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
Test-NetConnection sessionguard.example.org -Port 443  # Netz des Hosts in der Sentinel-Policy "sessionguard" freigegeben?
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
Guacamole und Master im selben Compose-Projekt (Netz "backend")?
Farm aktiviert?
Resource aktiviert?
Resource korrekt mit Guacamole Connection gemappt?
mindestens ein gesunder Online-Host in Farm?
```

Netzwerk prüfen:

```bash
docker compose exec sessionguard-master wget -qO- http://127.0.0.1:8080/healthz
docker compose logs guacamole | grep -i sessionguard
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

Die Redirect URI muss Zeichen für Zeichen zur Anwendung in Sentinel passen (Sentinel zeigt sonst „Die Rücksprungadresse … ist nicht eingetragen“).

Admin-Konsole:

```text
https://sessionguard.example.org/oidc/callback
```

Guacamole-Zugang:

```text
https://rds.example.org/_sessionguard/auth/oidc/callback
```

Agent beispielsweise:

```text
https://rds01-mgmt.example.org/oidc/callback
```

„IdP issued no refresh token“ im Master-Log: bei der Anwendung in Sentinel „Refresh-Tokens ausgeben“ einschalten (Pflicht mit `revalidate_minutes`).

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

Empfohlene Reihenfolge:

```text
1. DNS für sso., sessionguard. und rds. anlegen
2. Sentinel: AD verbinden, Policies, OIDC-Server, Anwendungen
3. RBAC- und Zugangsgruppen im AD anlegen/zuweisen
4. Caddyfile-Sites und Proxy-Secret übernehmen
5. SessionGuard-Stack (deploy/) starten
6. OIDC/RBAC im Master prüfen
7. einen RDS-Canary auswählen
8. Agent dort installieren
9. Director/Health prüfen
10. Office-Canary-Farm erstellen
11. Guacamole-Anmeldung über Sentinel prüfen
12. Guacamole-Gruppen anlegen, Rechte zuweisen
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
deploy/docker-compose.workers.yml
deploy/.env.example
deploy/master.json.example
deploy/Caddyfile.sentinel
deploy/guacamole/Dockerfile.guacamole
deploy/guacamole/harden-guacamole-db.sql
deploy/guacamole/CI-CD.md
scripts/build.ps1
scripts/install-agent.ps1
docs/SENTINEL.md
docs/ACCESS-AUTH.md
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

