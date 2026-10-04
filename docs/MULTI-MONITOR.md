# Multi-Monitor (v0.6)

SessionGuard 0.6 bringt Multi-Monitor-Unterstützung für Guacamole-Desktops im **Span-Modus**: Eine Browser-Sitzung wird über mehrere lokale Monitore gelegt, Guacamole passt die RDP-Auflösung über den Display-Update-Kanal an. Das entspricht `mstsc /span`.

## Warum Span und nicht „echtes“ Multi-Monitor?

Apache Guacamole 1.6.0 kann RDP-Multi-Monitor (getrennte Monitore im Windows-Desktop) nicht. Die Upstream-Arbeit dazu ist noch nicht gemergt:

- guacamole-server PR #560 (Draft): Monitor-Layout über den Display-Update-Kanal
- guacamole-client PR #1061 (Draft): ein Browserfenster je Monitor, Abstimmung per `BroadcastChannel`
- Jira GUACAMOLE-288: offen, keine Fix-Version

Der Span-Modus läuft dagegen mit den unveränderten offiziellen Docker-Images `guacamole/guacamole:1.6.0` und `guacamole/guacd:1.6.0`. Sobald Upstream natives Multi-Monitor veröffentlicht, kann die SessionGuard-Policy (`multi_monitor`, `max_monitors`, Gruppen) unverändert weiterverwendet werden.

**Einschränkung des Span-Modus:** Windows sieht *einen* großen Monitor. Ein maximiertes Fenster füllt deshalb alle Monitore, und die Taskleiste läuft über die gesamte Breite. Mit Windows-Snap (`Win + ←/→`) lassen sich Fenster trotzdem bequem je Hälfte anordnen.

## Funktionsweise

```text
Browser (Guacamole-Client-Seite)
  └─ sessionguard-multimonitor.js
       1. erkennt #/client/<id> und liest die Guacamole Connection ID
       2. GET /_sessionguard/auth/display-policy?connection_id=…   (Access-Session-Cookie)
       3. zeigt oben mittig die Leiste „Multi-Monitor (n)“
       4. Klick: getScreenDetails() → Rechteck über benachbarte Monitore
       5. window.open(popup) an dieser Position/Größe, alter Tab lädt neu (Tunnel zu)
  └─ Guacamole resize-method=display-update → RDP-Desktop = Fenstergröße
```

- **Chrome / Edge (ab Version 100):** Die Window Management API (`getScreenDetails`) platziert das Fenster automatisch. Beim ersten Mal fragt der Browser nach der Berechtigung „Fenster auf allen Bildschirmen verwalten“.
- **Firefox / Safari:** Es öffnet sich ein normales Popup-Fenster, das der Benutzer selbst über die Monitore zieht. Die Sitzung passt sich automatisch an.
- Die Leiste erscheint nur, wenn die Resource Multi-Monitor erlaubt **und** der Benutzer zu einer der freigegebenen Gruppen gehört.
- Ausgewählt werden bis zu `max_monitors` nebeneinanderliegende (oder übereinanderliegende) Monitore, die den aktuellen Monitor enthalten. guacd begrenzt Display-Updates auf **8192 px** je Dimension (`GUAC_RDP_DISP_MAX_SIZE`). Der Helper berücksichtigt dafür die Skalierung (`devicePixelRatio`) und lässt notfalls Monitore weg.
- Im Span-Fenster bietet die Leiste „Erneut über Monitore legen“ und „Ein Monitor“ an.

## Einrichtung

### 1. Guacamole-Verbindung

In der Guacamole-Verbindung (RDP) unter **Anzeige**:

| Parameter | Wert |
|---|---|
| Resize method (`resize-method`) | **Display update virtual channel (RDP 8.1+)** / `display-update` |
| Breite/Höhe | leer lassen |

Ohne `display-update` wird das Bild nur skaliert und nicht in höherer Auflösung übertragen.

### 2. SessionGuard Resource

Im Master unter **Apps & Desktops → Resource bearbeiten**:

- **Guacamole Connection ID** setzen. Der Browser-Helper kennt die numerische ID aus der URL. Eine reine Zuordnung über den Namen funktioniert nur, solange der Seitentitel dem Verbindungsnamen entspricht.
- **Multi-Monitor für diese Resource erlauben** aktivieren.
- **Maximale Monitore**: 2–4.
- **Nur für Gruppen**: optional, kommagetrennte PocketID-Gruppen (z. B. `rds-multimonitor`). Leer bedeutet: alle Benutzer mit gültiger Access-Session.

Per API:

```json
PUT /api/v1/resources/{id}
{ "...": "...", "multi_monitor": true, "max_monitors": 2, "multi_monitor_groups": ["rds-multimonitor"] }
```

### 3. Reverse Proxy

Der Endpoint `/_sessionguard/auth/display-policy` liegt unter dem bereits vorhandenen Access-Auth-Präfix. Traefik (`PathPrefix(/_sessionguard/auth)`) und die mitgelieferten Caddyfiles leiten ihn ohne weitere Änderung weiter.

Eine eigene `Permissions-Policy` darf `window-management` nicht sperren. Die mitgelieferten Caddyfiles setzen `window-management=(self)`.

### 4. Guacamole-Image neu bauen

```bash
docker compose build guacamole   # deploy/guacamole/Dockerfile.guacamole
docker compose up -d guacamole
```

Die Extension enthält jetzt zusätzlich `js/sessionguard-multimonitor.js` und `css/sessionguard-multimonitor.css`. Nach dem Update im Browser einmal neu laden (Strg+F5).

### 5. RDS-Host

- GPO *Computer → Administrative Vorlagen → Windows-Komponenten → Remotedesktopdienste → Remotedesktop-Sitzungshost → Remotesitzungsumgebung → „Maximale Anzeigeauflösung begrenzen“*: nicht konfiguriert lassen oder auf mindestens die gewünschte Gesamtbreite setzen (z. B. 3840×1080 bei 2× Full HD).
- Größere Auflösungen brauchen mehr RAM und Bandbreite pro Sitzung. Für die Kapazitätsplanung grob mit dem Faktor der Pixelanzahl rechnen.

## Fehlerbilder

| Symptom | Ursache / Lösung |
|---|---|
| Keine Leiste sichtbar | Resource nicht über die Connection ID zugeordnet, Multi-Monitor deaktiviert oder Benutzer nicht in einer freigegebenen Gruppe. Prüfen: `https://<guac>/_sessionguard/auth/display-policy?connection_id=<id>` im Browser liefert `multi_monitor: true`. |
| „Popup blockiert“ | Popups für den Guacamole-Host erlauben. Beim ersten Mal nach der Berechtigungsabfrage erneut klicken. |
| Fenster bleibt auf einem Monitor | Der Browser hat die Platzierung begrenzt. Fenster manuell aufziehen; die Sitzung folgt. |
| Bild unscharf/skaliert statt höherer Auflösung | `resize-method` der Verbindung steht nicht auf `display-update`. |
| Breite endet vor dem dritten Monitor | 8192-px-Grenze von guacd (bei Skalierung > 100 % schneller erreicht). |
