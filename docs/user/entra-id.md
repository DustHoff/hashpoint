# Microsoft Entra ID — Anmeldung

Der TimeTracker kann sich optional gegen **Microsoft Entra ID** (vormals
Azure AD) anmelden, um in Ihrem Namen auf Microsoft-365-Daten und auf
Entra-geschützte Drittanwendungen zuzugreifen — z. B. Ihren persönlichen
Outlook-Kalender, SharePoint-Inhalte oder eigene Firmen-APIs.

> **Optional:** Das Feature ist additiv. Wer es nicht braucht, lässt
> Client-ID und Tenant-ID einfach leer — der TimeTracker funktioniert
> unverändert weiter, kein Auth-Code wird ausgeführt, keine Daten an
> Microsoft gesendet.

## Was sich damit erreichen lässt

Mit dem erhaltenen Access-Token sprechen die zugehörigen Module später:

- **Microsoft Graph** — SharePoint-Sites, Dokumente, persönlicher
  Kalender, Profilinformationen.
- **Eigene oder Dritt-APIs**, sofern diese gegen denselben Entra-ID-Tenant
  geschützt sind und die TimeTracker-App-Registrierung dort entsprechende
  Berechtigungen besitzt.

Welche dieser Integrationen aktiv genutzt werden, hängt vom jeweils
ausgerollten Funktionsumfang Ihrer Build ab. Diese Seite beschreibt den
**Anmelde-Mechanismus** — die einzelnen Anwendungen kommen in eigenen
Kapiteln dazu, sobald sie verfügbar sind.

## Wie die Anmeldung funktioniert

Beim erstmaligen Anmelden öffnet der TimeTracker Ihren **Standardbrowser**
und schickt Sie an die Microsoft-Login-Seite (Loopback-Redirect-Flow,
PKCE, kein Client Secret). Auf einem **Entra-ID-joined Gerät** läuft der
Flow dank des Primary Refresh Tokens (PRT) im Browser typischerweise
**ohne Eingabe-Prompt** durch — Browser öffnet sich, Browser schließt
sich, fertig.

Bei jedem weiteren Programmstart wird das Token **still aus einem
verschlüsselten lokalen Cache** geladen — kein Browser, keine
Benutzerinteraktion, keine Netz-Verzögerung im UI.

> Es werden **keine** Anmeldedaten (E-Mail, Passwort, MFA-Code) an den
> TimeTracker übertragen. Microsoft schickt dem Tracker nach erfolgreicher
> Anmeldung lediglich einen kurzlebigen Access-Token sowie einen
> Refresh-Token zurück.

## Voraussetzungen

- **Windows-Gerät**. Die Token-Verschlüsselung nutzt die Windows Data
  Protection API (DPAPI) und ist auf Windows-only ausgelegt.
- **Eine Entra-ID-App-Registrierung** in Ihrem Tenant (siehe Abschnitt
  unten). Anlage erfolgt einmalig durch eine Person mit den nötigen
  Azure-AD-Rechten — meist die IT-Abteilung.
- **Standardbrowser mit Microsoft-Account-Integration** für den
  promptlosen Flow:
  - **Edge** ab Version 87 (Account-Sync aktiv).
  - **Chrome** mit der Erweiterung *„Microsoft Single Sign On"*.
  - **Firefox** wird **nicht** für PRT-SSO unterstützt — der Erst-Login
    läuft, zeigt aber einen normalen Microsoft-Login-Prompt.

## Schritt 1 — App-Registrierung in Entra ID anlegen

Diesen Schritt führt einmalig Ihre IT durch. Die Werte landen am Ende als
Client-ID und Tenant-ID in den TimeTracker-Einstellungen.

1. **Azure Portal → Microsoft Entra ID → App registrations → New registration**.
2. Anzeigename z. B. `Hashpoint TimeTracker (Onesi)`.
3. **Supported account types:** *Accounts in this organizational directory only* (Single-Tenant).
4. **Redirect URI** zunächst leer lassen, **Register** klicken.
5. In der angelegten App auf **Authentication → Add a platform**:
   - Plattform-Typ: **Mobile and desktop applications**.
   - Redirect-URI: **`http://localhost`** (genau so, ohne Port).
   - **Allow public client flows: Yes**.
6. **API permissions → Add a permission → Microsoft Graph → Delegated permissions** — empfohlene Mindest-Scopes:

| Scope | Zweck |
| --- | --- |
| `User.Read` | Eigenes Profil (für die Status-Anzeige im TimeTracker). |
| `openid`, `profile`, `offline_access` | OAuth-Standardumfang; `offline_access` ist für den stillen Token-Refresh **Pflicht**. |
| `Calendars.Read` | Kalender lesen — nur hinzufügen, wenn Kalenderfeatures genutzt werden. |
| `Sites.Read.All` | SharePoint-Sites/Listen lesen — nur hinzufügen, wenn SharePoint-Features genutzt werden. |
| `Files.ReadWrite.All` | SharePoint-Dokumente schreiben — nur hinzufügen, wenn Schreibzugriff nötig ist. |

> **Least privilege:** Geben Sie nur die Scopes frei, die tatsächlich
> verwendet werden. Weitere Scopes lassen sich nachträglich ergänzen, ohne
> dass eine Neuanmeldung notwendig wäre.

7. **Grant admin consent** durch eine berechtigte Person erteilen lassen.
   Das nimmt jedem einzelnen Endanwender den Einwilligungs-Dialog beim
   Erst-Login ab.

8. **Werte für die Konfiguration notieren** — auf der Übersichtsseite der
   App-Registrierung:
   - **Application (client) ID** (eine GUID, 8-4-4-4-12-Format)
   - **Directory (tenant) ID** (ebenfalls GUID)

> **Sind eigene/Dritt-APIs im Spiel?** Diese benötigen eine **eigene**
> App-Registrierung als „Resource"; dort wird unter *Expose an API* eine
> Application ID URI und einer oder mehrere Scopes definiert. Die
> TimeTracker-Client-App-Registrierung erhält dann unter *API permissions
> → My APIs* eine Delegated Permission auf diese Resource. Sprechen Sie
> für diesen Fall mit Ihrer IT, falls noch nicht vorhanden.

## Schritt 2 — Im TimeTracker konfigurieren

1. Im Hauptfenster auf den Tab **Einstellungen** wechseln.
2. Im Abschnitt **Microsoft Entra ID** die beiden GUIDs eintragen:
   - **Client ID** — Application (client) ID aus der App-Registrierung.
   - **Tenant ID** — Directory (tenant) ID aus der App-Registrierung.
3. Auf **Einstellungen speichern** klicken.

> Die Werte landen ausschließlich in `%APPDATA%\TimeTracker\config.toml`.
> Beide IDs sind **keine** Geheimnisse — sie sind in der Cloud öffentlich
> sichtbar — und werden bewusst im Klartext gespeichert. Tokens und
> Cookies hingegen werden DPAPI-verschlüsselt abgelegt (siehe
> *Sicherheit* unten).

Akzeptiert werden Werte mit oder ohne geschweifte Klammern (`{...}`),
beliebige Groß-/Kleinschreibung — beim Speichern werden sie auf das
Standard-Format normalisiert. Die Sonderwerte `common`, `organizations`
und `consumers` werden **abgelehnt**: Der TimeTracker arbeitet bewusst
single-tenant.

## Schritt 3 — Anmelden

1. Nach dem Speichern erscheint im Status-Kasten *„Konfiguration
   vorhanden, aber noch nicht angemeldet"*.
2. Auf **Bei Entra ID anmelden** klicken.
3. Es öffnet sich Ihr Standardbrowser auf der Microsoft-Login-Seite.
4. **Entra-joined Gerät:** in der Regel sehen Sie nur kurz das Login-Logo,
   dann schließt sich der Tab automatisch.
5. **Sonst:** wählen Sie das gewünschte Konto aus, ggf. MFA bestätigen.
   Anschließend leitet Microsoft an die `localhost`-URL weiter, der
   TimeTracker übernimmt das Token.
6. Status wechselt auf *„Eingeloggt als <ihr-name>@<firma>.com im Tenant
   <tenant-id>."*.

## Status verstehen

Im Status-Kasten unter den ID-Feldern stehen drei mögliche Zustände:

| Anzeige | Bedeutung |
| --- | --- |
| *„Feature inaktiv — Entra ID nicht konfiguriert"* | Client- und/oder Tenant-ID sind leer. Der Anmelde-Button ist deaktiviert. |
| *„Konfiguration vorhanden, aber noch nicht angemeldet"* | IDs gespeichert, es liegt aber keine gültige Session vor. Klick auf **Bei Entra ID anmelden**, um den Browser-Flow zu starten. |
| *„Eingeloggt als …"* | Aktive Session vorhanden. Token werden im Hintergrund still erneuert. |

## Erneut anmelden / Abmelden

- **Erneut anmelden:** klickt sich durch denselben Browser-Flow noch
  einmal — nützlich, wenn sich Ihr Account oder Ihr Refresh-Token-Status
  geändert hat (z. B. nach Passwort-Reset, neuer Conditional-Access-
  Policy oder einem Geräte-Wechsel).
- **Abmelden:** löscht den lokalen, verschlüsselten Token-Cache. Ihre
  Microsoft-Sitzung im Browser bleibt davon unberührt — andere
  Anwendungen, die ebenfalls per SSO angemeldet sind, werden **nicht**
  abgemeldet.

## Wann der TimeTracker den Browser erneut öffnet

In den meisten Fällen läuft die Anmeldung dauerhaft still im Hintergrund.
Es gibt aber Situationen, in denen Microsoft eine erneute interaktive
Bestätigung verlangt:

- **Refresh-Token abgelaufen** — passiert bei Standardeinstellung nach
  90 Tagen Inaktivität.
- **Conditional-Access-Policies** — z. B. erzwungenes MFA-Re-Prompt-
  Intervall, neue Compliance-Anforderungen, geblockte Standorte.
- **Passwort-Wechsel oder erzwungene Re-Authentifizierung** durch die IT.
- **Cache zerstört** — z. B. nach Wechsel des Windows-Profils oder einem
  fehlerhaften Backup-Restore. In diesem Fall wird der Cache automatisch
  als „leer" behandelt.

Der TimeTracker meldet sich dann mit einem deutlichen Hinweis und bietet
einen erneuten Login-Klick an. Bestehende getaggte Blöcke und der
Personio-Sync sind davon **nicht** betroffen.

## Wo werden Tokens gespeichert?

Der gesamte Microsoft-Authentifizierungs-Cache (Access-Tokens,
Refresh-Tokens, Account-Information) liegt als **einzelne Datei** unter:

```
%LOCALAPPDATA%\TimeTracker\auth\msal_cache.bin
```

Diese Datei ist mit der **Windows Data Protection API** (DPAPI) im
**CurrentUser-Scope** verschlüsselt:

- Nur Ihr **Windows-Benutzerkonto auf genau diesem Gerät** kann den
  Inhalt entschlüsseln.
- Eine Kopie der Datei auf ein anderes Gerät oder unter einem anderen
  Benutzerkonto ist **nicht** verwertbar.
- Auch ein Administrator desselben Geräts kann den Inhalt **nicht**
  ohne Übernahme Ihres Profils auslesen.

Bei jedem Token-Refresh schreibt der TimeTracker die Datei atomar (über
ein Temp-File und Umbenennen) neu — eine Programmunterbrechung mitten im
Schreiben kann den Cache nicht beschädigen.

## Sicherheit & Datenschutz

- Es wird ausschließlich mit `login.microsoftonline.com` und (je nach
  genutztem Modul) `graph.microsoft.com` bzw. den Hosts Ihrer Custom-APIs
  kommuniziert.
- Authentifizierungs-Header (`Authorization: Bearer …`) werden niemals
  geloggt; im Log erscheinen lediglich Hinweise wie *„entra: silent
  acquisition failed — falling back to interactive"*.
- Token-Inhalte (`access_token`, `refresh_token`, `id_token`) erscheinen
  niemals im Log — auch nicht im Debug-Log.
- Beim Klick auf **Abmelden** wird die Datei `msal_cache.bin` vollständig
  entfernt.
- Die App-Registrierung selbst enthält **kein** Client Secret — der
  TimeTracker authentifiziert sich rein als Public Client mit PKCE.

## Fehlerbehandlung

| Fehlermeldung | Ursache | Lösung |
| --- | --- | --- |
| *„Entra ID ist nicht konfiguriert — bitte Client- und Tenant-ID eintragen"* | Mindestens eines der beiden Felder leer. | In den Einstellungen beide GUIDs eintragen, speichern. |
| Validierungsfehler beim Speichern: *„entra.client_id erwartet eine GUID im 8-4-4-4-12-Format"* | Eingabe ist keine GUID (z. B. URL oder Hex-String fehlerhaft). | Den Wert aus dem Azure Portal frisch kopieren. |
| Validierungsfehler: *„entra.tenant_id muss eine konkrete Directory-GUID sein, nicht ‚common'"* | Multi-Tenant-Wert versucht. | Konkrete Tenant-ID Ihres Tenants verwenden. |
| Browser öffnet sich, zeigt Microsoft-Login-Fehlerseite *„AADSTS50011: The reply URL specified in the request does not match …"* | Redirect-URI in der App-Registrierung fehlt oder unpassend. | App-Registrierung → Authentication → Mobile and desktop applications → `http://localhost` ergänzen. |
| Browser öffnet sich, fragt nach Einwilligung — Endanwender kann nicht zustimmen. | Admin Consent fehlt; Tenant erlaubt keinen User-Consent für API-Permissions. | Admin den *Grant admin consent*-Schritt in der App-Registrierung ausführen lassen. |
| Login schlägt fehl mit *„AADSTS7000218: The request body must contain the following parameter: ‚client_assertion'…"* | *Allow public client flows* in der App-Registrierung steht auf **No**. | Auf **Yes** umstellen und erneut anmelden. |
| *„entra: interactive login required"* taucht in einem Folge-Modul auf | Refresh-Token ungültig oder Conditional-Access-Policy verlangt erneut MFA. | Auf **Erneut anmelden** klicken. |

## Browser-Hinweise

- Der TimeTracker nutzt für den Login **immer den Windows-Standardbrowser**
  — also den Browser, der unter *Windows-Einstellungen → Apps →
  Standard-Apps → Webbrowser* hinterlegt ist.
- Für promptloses SSO empfiehlt sich Edge oder Chrome (mit der
  *Microsoft Single Sign On*-Erweiterung). Firefox funktioniert für die
  Anmeldung selbst, zeigt aber jedes Mal einen normalen Account-Picker.
- Manche Anti-Virus-Lösungen blockieren beim ersten Mal den
  Loopback-Listener auf einem zufälligen High-Port. Der Browser zeigt in
  dem Fall *„Diese Seite kann nicht geladen werden"* — die zugrunde
  liegende Anti-Virus-Meldung erscheint im Tray. Einmal zulassen
  reicht; danach läuft jeder weitere Login bis zum nächsten DSGVO-
  Update der Anti-Virus-Software durch.
