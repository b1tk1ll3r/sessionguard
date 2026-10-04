# Keycloak als Identity Provider (v0.6)

SessionGuard arbeitet mit jedem OIDC-Provider, der Authorization Code + PKCE (S256) beherrscht. Neben PocketID ist Keycloak (ab Version 18, getestet wird gegen das aktuelle Claim-Format) direkt unterstützt. Die folgende Anleitung legt **zwei Clients** an, analog zu PocketID:

| Client | Zweck | Konfiguration |
|---|---|---|
| `sessionguard` | Director/Admin-Konsole (und optional Agent-WebUI) | `oidc` |
| `guacamole-access` | ForwardAuth vor Guacamole | `access_auth` |

Vollständiges Beispiel: [`configs/master.keycloak.example.json`](../configs/master.keycloak.example.json).

## Was sich gegenüber PocketID unterscheidet

| Thema | PocketID | Keycloak | SessionGuard-Einstellung |
|---|---|---|---|
| Scope `groups` | vorhanden | **existiert nicht**: Keycloak bricht mit `invalid_scope` ab | `"scopes": ["openid", "profile", "email"]` |
| Gruppen im Token | Claim `groups` | erst nach einem Mapper; mit „Full group path“ als `/gruppe` | `"groups_claims": ["groups"]`; der führende `/` wird beim Vergleich ignoriert |
| Rollen statt Gruppen | – | `realm_access.roles` / `resource_access.<client>.roles` | `"groups_claims": ["realm_access.roles"]` (Punkt-Pfade) |
| Issuer | `https://auth.example.org` | `https://keycloak.example.org/realms/<realm>` | `issuer` |
| Benutzername änderbar | je nach Einstellung | Standard: aus (Realm → „Edit username“) | Identitätsbindung greift zusätzlich |

## 1. Realm vorbereiten

1. Realm anlegen oder einen bestehenden verwenden, z. B. `example`.
2. **Realm settings → General**: *Frontend URL* bzw. `KC_HOSTNAME` muss die öffentliche URL sein, sonst passt der Issuer in der Discovery nicht und SessionGuard startet nicht (`oidc: issuer did not match`).
3. **Realm settings → Login**: *Edit username* **aus** lassen (Standard). *User registration* aus, wenn Benutzer nur administrativ angelegt werden.
4. **Groups** anlegen, passend zu `rbac.groups`, z. B. `sessionguard-admins`, `sessionguard-helpdesk`, `sessionguard-operators` sowie optional `rds-multimonitor` für Multi-Monitor.

## 2. Client `sessionguard` (Admin-Konsole)

**Clients → Create client**

| Feld | Wert |
|---|---|
| Client type | OpenID Connect |
| Client ID | `sessionguard` |
| Client authentication | **On** (vertraulicher Client → Client Secret) |
| Authentication flow | nur **Standard flow**; Direct access grants, Implicit und Service accounts aus |
| Valid redirect URIs | `https://sessionguard.example.org/oidc/callback` |
| Valid post logout redirect URIs | `https://sessionguard.example.org/` |
| Web origins | leer |

Danach im Client:

- **Advanced → Advanced settings → Proof Key for Code Exchange Code Challenge Method**: `S256`
- **Logout settings**: *Front channel logout* **aus**; *Backchannel logout URL* `https://sessionguard.example.org/oidc/backchannel-logout`; *Backchannel logout session required* **an**
- **Credentials**: Client Secret kopieren → `SESSIONGUARD_OIDC_CLIENT_SECRET`

### Gruppen-Mapper

**Client scopes → `sessionguard-dedicated` → Add mapper → By configuration → Group Membership**

| Feld | Wert |
|---|---|
| Name | `groups` |
| Token Claim Name | `groups` |
| Full group path | an oder aus. Bei „an“ gelten Untergruppen als `it/admins` in der Konfiguration. |
| Add to ID token | **an** (SessionGuard wertet das ID-Token aus) |
| Add to userinfo | an |

> **Rollen statt Gruppen:** Die Standard-Mapper des Client-Scopes `roles` schreiben Rollen nur ins *Access*-Token. Für `groups_claims: ["realm_access.roles"]` bzw. `["resource_access.sessionguard.roles"]` dort bei *realm roles* bzw. *client roles* **Add to ID token** einschalten.

## 3. Client `guacamole-access` (ForwardAuth)

Wie oben, mit diesen Werten:

| Feld | Wert |
|---|---|
| Client ID | `guacamole-access` |
| Valid redirect URIs | `https://guacamole.example.org/_sessionguard/auth/oidc/callback` |
| Valid post logout redirect URIs | `https://guacamole.example.org/` |
| Backchannel logout URL | `https://guacamole.example.org/_sessionguard/auth/backchannel-logout` |

Den Mapper `groups` ebenfalls in `guacamole-access-dedicated` anlegen, wenn `access_auth.allowed_groups` oder Multi-Monitor-Gruppen genutzt werden. Den Client Secret als `SESSIONGUARD_ACCESS_OIDC_CLIENT_SECRET` hinterlegen.

Der Guacamole-Benutzername ist `preferred_username` (Keycloak-Username). Er muss zu `access_auth.username_pattern` passen. Der Standard erlaubt Buchstaben, Ziffern und `._@-`, also auch „E-Mail als Benutzername“.

## 4. SessionGuard konfigurieren

```json
"oidc": {
  "issuer": "https://keycloak.example.org/realms/example",
  "client_id": "sessionguard",
  "client_secret": "SET-BY-SESSIONGUARD_OIDC_CLIENT_SECRET",
  "redirect_url": "https://sessionguard.example.org/oidc/callback",
  "logout_redirect_url": "https://sessionguard.example.org/",
  "scopes": ["openid", "profile", "email"],
  "groups_claims": ["groups"],
  "secure_cookie": true
},
"access_auth": {
  "enabled": true,
  "issuer": "https://keycloak.example.org/realms/example",
  "client_id": "guacamole-access",
  "redirect_url": "https://guacamole.example.org/_sessionguard/auth/oidc/callback",
  "logout_redirect_url": "https://guacamole.example.org/",
  "username_claim": "preferred_username"
}
```

- `access_auth.scopes` und `access_auth.groups_claims` werden aus `oidc` übernommen, wenn sie leer sind.
- `rbac.groups`, `admin_groups`, `allowed_groups` und Multi-Monitor-Gruppen dürfen mit oder ohne führenden `/` angegeben werden.
- Die **Agent-WebUI** nutzt dieselben Felder in ihrem `oidc`-Block (eigener Client oder der Client `sessionguard` mit zusätzlicher Redirect-URI des Agents).

## 5. Migration PocketID → Keycloak

Keycloak vergibt neue `sub`-Werte. Die Identitätsbindungen aus PocketID-Zeiten blockieren deshalb jede Anmeldung (Audit: `identity_binding_conflict … another issuer`). Ablauf:

1. **Benutzernamen in Keycloak identisch** zu PocketID anlegen (oder per LDAP/Import übernehmen). Guacamole kennt Benutzer nur über den Namen; ein anderer Name ergibt ein neues Guacamole-Konto ohne Berechtigungen.
2. Konfiguration umstellen und Master neu starten.
3. Im Master unter **Access-Sessions → Identitätsbindungen → „Alle zurücksetzen (IdP-Wechsel)“** auslösen (`DELETE /api/v1/access/identities?confirm=all`). Das beendet auch alle Access-Sessions.
4. Benutzer melden sich neu an, und die Namen werden an die Keycloak-`sub` gebunden.

Zwischen Schritt 3 und der ersten Anmeldung eines Benutzers kann ein *anderer* Keycloak-Benutzer mit gleichem Namen dessen Guacamole-Konto beanspruchen. Das ist nur dann ausgeschlossen, wenn Benutzernamen in Keycloak ausschließlich administrativ vergeben werden („Edit username“ aus, keine Selbstregistrierung).

## Fehlerbilder

| Symptom | Ursache |
|---|---|
| Keycloak-Fehlerseite „Invalid scopes: openid profile email groups“ | `scopes` ohne `groups` setzen (oder einen Client-Scope `groups` anlegen) |
| Login ok, danach „user is not in an allowed admin group“ | Gruppen-Mapper fehlt oder *Add to ID token* ist aus; Rollen-Mapper schreiben standardmäßig nur ins Access-Token |
| Master startet nicht: `issuer did not match` | `KC_HOSTNAME`/Frontend-URL stimmt nicht mit `issuer` überein |
| „this username is bound to a different account“ | Identitätsbindung aus dem alten IdP; siehe Migration |
| Logout landet auf einer Keycloak-Fehlerseite | *Valid post logout redirect URIs* fehlt |
