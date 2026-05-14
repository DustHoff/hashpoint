# Einstellungen

Alle Einstellungen lassen sich direkt im **Hauptfenster** im Tab
**Einstellungen** vornehmen. Geänderte Werte werden anschließend in
`%APPDATA%\TimeTracker\config.toml` persistiert. Ein direktes Editieren der
TOML-Datei ist weiterhin möglich, aber für den Normalbetrieb nicht mehr
nötig.

## Aufbau des Tabs

Der Tab ist in fünf Abschnitte unterteilt:

1. **Erfassung** — globaler Erfassungs-Schalter, Polling-Intervall, Idle-Schwelle und Tag-Block-Granularität.
2. **Quick-Tag-Picker** — globaler Hotkey für die schnelle Tag-Auswahl (siehe [Quick-Tag-Picker](quick-tag.md)).
3. **Kommunikations-Prozesse** — Liste paralleler Erfassungsprozesse (Teams, Zoom, …) für hybride Meetings.
4. **Personio** — Tenant-Subdomain und interaktive Anmeldung.
5. **Microsoft Entra ID** — Client-/Tenant-ID und optionale Anmeldung für Microsoft 365 / SharePoint / Custom-APIs.

> **Autostart:** Der TimeTracker bietet hier keinen Autostart-Schalter mehr. Der MSI-Installer aktiviert den Autostart automatisch für den installierenden Account; eine manuelle Aktivierung beschreibt der Abschnitt [Autostart](installation.md#autostart) im Installationskapitel.

Am unteren Rand befindet sich der Button **Einstellungen speichern**.
Änderungen treten erst nach dem Speichern in Kraft. Erfolge und
Validierungsfehler werden oben im Tab als Banner angezeigt.

## Erfassung

| Feld | Default | Bereich | Bedeutung |
| --- | --- | --- | --- |
| **Erfassung der fokussierten Anwendung aktiv** | an | Checkbox | Globaler Schalter für das automatische Fokus-Tracking. Deaktiviert: keine neuen Programm-Blöcke und keine Auto-Tagging-Regeln greifen mehr — manuelles Tagging über das Tray-Submenü bleibt möglich. Der Wert wird persistiert; wirkt sich sofort und auch über Anwendungs-Neustarts hinweg aus. Identisch mit dem Tray-Eintrag „Pause Tracking". |
| **Poll-Intervall (Sekunden)** | `2` | `1`–`300` | Wie oft prüft der TimeTracker, welches Fenster im Vordergrund ist. Niedriger = präziser, aber höhere CPU-Last. |
| **Idle-Schwelle (Minuten)** | `5` | `1`–`240` | Nach wie vielen Minuten ohne Tastatur-/Maus-Eingabe der laufende Block beendet und als **Idle** markiert wird. |
| **Tag-Block-Granularität (Minuten)** | `0` | `0`–`60` | Legt **Tag-Blöcke** (manuelle Range-Tags und Auto-Tag-Blöcke) auf ein **Slot-Raster** dieser Breite (verankert an lokaler Mitternacht, also z. B. `:00/:15/:30/:45` bei `15`). Beginn wird **abgerundet**, Ende **abgerundet**. Auto-Tag-Blöcke unterhalb der Granularität werden nicht erzeugt (Zero-Length-Suppression). **Process-Tracks** sind von dieser Einstellung **nicht** betroffen — der untere Strip zeigt immer die rohen, sekundengenauen Zeiten. Werteänderungen treten ohne Neustart in Kraft (greifen ab dem nächsten Tag-Block-Boundary). `0` deaktiviert das Raster komplett. |

## Quick-Tag-Picker

| Feld | Default | Bedeutung |
| --- | --- | --- |
| **Globalen Hotkey für den Quick-Tag-Picker registrieren** | an | Aktiviert/deaktiviert die Registrierung eines systemweiten Hotkeys, mit dem aus jedem Fenster heraus eine kompakte Tag-Auswahl unten rechts auf dem Cursor-Monitor geöffnet werden kann. |
| **Hotkey** | `Ctrl+Alt+T` | Tastenkombination im Format `<Mod>+<Mod>+<Taste>`. Modifier: `Ctrl`, `Alt`, `Shift`, `Win`. Tasten: `A`–`Z`, `0`–`9`, `F1`–`F24`. Mindestens ein Modifier ist Pflicht. **Hinweis:** `Win+T` ist von Windows belegt (Taskleisten-Fokus) und sollte vermieden werden. |

Funktionsweise und Bedienung sind detailliert unter [Quick-Tag-Picker](quick-tag.md) beschrieben.

## Kommunikations-Prozesse

Hybrid-Worker, die in Microsoft Teams, Zoom o. ä. an Meetings teilnehmen oder
ihren Bildschirm teilen, sollen diese Zeit auch dann erfasst bekommen, wenn
sie nebenbei in einem anderen Fenster (Browser für Recherche, Editor für
Notizen) arbeiten. Klassisches Fokus-Tracking würde dabei das Tagging des
„falschen" Vordergrund-Programms wählen.

Hashpoint erfasst deshalb **zusätzlich** zu den Fokus-Prozessen alle
hier gelisteten Programme, sobald sie ein sichtbares Top-Level-Fenster
zeigen — unabhängig davon, ob sie gerade im Fokus stehen. Diese parallel
laufenden Tracks erscheinen auf einer **eigenen Zeitachsen-Schiene
„📞 Kommunikation"** unter den regulären Prozessen und sind in der
Prozess-Tabelle mit einem 📞-Symbol markiert.

| Feld | Default | Bedeutung |
| --- | --- | --- |
| **Prozessliste** | `teams.exe` | Liste der Datei-Namen (ohne Pfad). Vergleich erfolgt **case-insensitive**. Beispiel-Einträge: `teams.exe`, `zoom.exe`, `slack.exe`, `webex.exe`. Leere Liste = Feature aus. |
| **Ausschluss-Phrasen (Fenstertitel)** | *(leer)* | Liste von Text-Phrasen, die als Ausschlussfilter auf den Fenstertitel wirken. Enthält der Titel eines Kommunikations-Fensters eine dieser Phrasen (case-insensitive Substring-Vergleich), wird das Fenster **nicht** als Kommunikations-Fenster behandelt, sondern wie jeder andere Prozess. Beispiel-Einträge: `Benachrichtigung`, `Notification`, `Eingehender Anruf`, `Reminder`. Leere/whitespace-Einträge werden beim Speichern verworfen. |

**Erkennungsregel:** Ein Fenster eines konfigurierten Programms gilt als
Kommunikations-Fenster, solange es **sichtbar** ist (das, was Sie in
Alt-Tab sehen) **und sein Titel keine der oben gelisteten Ausschluss-
Phrasen enthält**. Versteckte Hintergrund-Fenster — z. B. das Teams-Tray-
Symbol ohne offenes Hauptfenster — lösen kein Tracking aus. Schließen,
Minimieren-zur-Tray oder Beenden des Programms beendet den Track sofort.
Die Ausschluss-Prüfung läuft in **jedem** Polling-Tick: wechselt der
Fenstertitel zur Laufzeit auf einen ausgeschlossenen Wert, schließt der
Tracker den Kommunikations-Track sofort; ändert sich der Titel später
wieder auf einen zulässigen Wert, beginnt automatisch ein **neuer**
Kommunikations-Track.

### Ausschluss-Phrasen — typische Anwendung

Manche Comm-Programme öffnen sichtbare Fenster, die fachlich **kein**
Meeting/Anruf darstellen — z. B. Teams-Toast-Benachrichtigungen,
Erinnerungs-Fenster oder das Hauptfenster im Stand-by. Ohne Filter würden
diese Fenster auf der Comm-Schiene erscheinen und ggf. eine Comm-getriebene
Auto-Tag-Regel auslösen, obwohl Sie gar nicht in einem Meeting sind. Tragen
Sie passende Substrings in die Ausschluss-Phrasen ein — diese Fenster
werden dann wie ganz normale Anwendungen vom Fokus-Tracker erfasst, die
Comm-Schiene und der Comm-Auto-Tag-Vorrang bleiben aus.

### Auto-Tagging mit Vorrang

Trifft eine [Auto-Tagging-Regel](auto-tagging.md) auf einen
Kommunikations-Prozess (z. B. `process_name contains teams.exe → #meetings`),
erzeugt sie genauso einen Tag-Block wie eine Fokus-getriebene Regel — **mit
einem Unterschied**: Solange das Kommunikations-Fenster offen ist, gewinnt
deren Tag-Block automatisch gegen jeden konkurrierenden Auto-Tag-Block aus
einem fokussierten Programm. Das heißt:

- Ist Teams in einem Meeting offen und matcht die Regel `teams.exe → #meetings`,
  läuft `#meetings` durch, auch wenn Sie währenddessen in den Browser
  (mit eigener Auto-Regel `browser → #web`) wechseln.
- Sobald das Teams-Meeting endet (Fenster geschlossen), übernimmt wieder
  die Fokus-Logik — der gerade fokussierte Prozess bestimmt das nächste
  Auto-Tagging.
- Manuelle Tag-Sitzungen werden während der Kommunikations-Phase
  pausiert (wie bei einer regulären Auto-Tag-Unterbrechung) und nehmen
  ihren Lauf danach wieder auf.

### Tipp

Damit der Vorrang nützlich wird, sollten Sie eine Auto-Tag-Regel anlegen, die
Ihre Kommunikations-Prozesse einem Tag wie `#meetings`, `#meeting` oder einem
Sub-Tag (`#proj1 › #meeting`) zuordnet. Ohne passende Regel wird der
Kommunikations-Track zwar erfasst und auf der Zeitachse dargestellt, aber
**nicht** automatisch getaggt — Sie können in dem Fall jederzeit per
Drag-to-Tag manuell zuordnen.

## Personio

| Feld | Bedeutung |
| --- | --- |
| **Tenant (Subdomain)** | Subdomain Ihrer Personio-Instanz. Beispiel: `acme` → `https://acme.personio.de`. Erlaubt sind Kleinbuchstaben, Ziffern und Bindestriche. |

Im Personio-Abschnitt sehen Sie zusätzlich den aktuellen Login-Status und
können die Anmeldung anstoßen oder zurücksetzen:

- **Bei Personio anmelden / Erneut anmelden** — startet den interaktiven
  Login-Flow. Dabei öffnet sich ein eigenes Chrome-Fenster auf der
  Personio-Login-Seite. Sobald die Anmeldung abgeschlossen ist, übernimmt
  der TimeTracker die Session-Cookies und schließt den Browser.
- **Session löschen** — entfernt die hinterlegten Cookies. Der Sync ist
  danach erst nach erneutem Login wieder möglich.

> Cookies werden verschlüsselt im **Windows Credential Manager** unter
> `TimeTracker.PersonioSession` abgelegt; die `config.toml` enthält keine
> sensiblen Daten.

Details zum Login-Flow, zu Validierung und Fehlerbehandlung siehe
[Personio-Synchronisation](personio.md).

## Microsoft Entra ID

Optionale Anmeldung gegen Ihren Microsoft-Entra-ID-Tenant. Aktiviert nur,
wenn beide GUIDs eingetragen sind — sonst läuft kein Auth-Code.

| Feld | Bedeutung |
| --- | --- |
| **Client ID** | Application (client) ID GUID aus der Entra-ID-App-Registrierung. Format: 8-4-4-4-12 hex. |
| **Tenant ID** | Directory (tenant) ID GUID. Bewusst Single-Tenant — `common`/`organizations` werden abgelehnt. |

Eingaben mit Klammern (`{...}`) und beliebiger Groß-/Kleinschreibung werden
beim Speichern auf das Standard-Format normalisiert.

Im Entra-Abschnitt sehen Sie zusätzlich den aktuellen Anmelde-Status und
können den Login anstoßen oder zurücksetzen:

- **Bei Entra ID anmelden / Erneut anmelden** — öffnet den Standardbrowser
  auf der Microsoft-Login-Seite. Auf Entra-joined Geräten in der Regel
  promptlos via PRT-SSO.
- **Abmelden** — löscht den lokalen, DPAPI-verschlüsselten Token-Cache.
  Der Windows-Login bleibt davon unberührt.

> Tokens werden DPAPI-verschlüsselt unter
> `%LOCALAPPDATA%\TimeTracker\auth\msal_cache.bin` abgelegt; die
> `config.toml` enthält nur die beiden öffentlichen GUIDs.

Details zur App-Registrierung, zum Anmelde-Flow und zur Fehlerbehandlung
siehe [Microsoft Entra ID](entra-id.md).

## `config.toml` — direkter Zugriff (optional)

Wer den Editor lieber direkt verwendet, kann die Datei unter
`%APPDATA%\TimeTracker\config.toml` öffnen. Beispielinhalt:

```toml
[tracking]
enabled                    = true   # globaler Schalter für Fokus-Tracking + Auto-Tagging
poll_interval_sec          = 2
idle_threshold_min         = 5
tag_block_granularity_min  = 0      # 0 = aus; 15 = Tag-Blöcke (manuell+auto) auf
                                    # 15-min-Slots snappen. Process-Tracks bleiben roh.

[personio]
tenant = "acme"

[entra]
client_id = ""   # leer = Entra-ID-Feature aus
tenant_id = ""   # leer = Entra-ID-Feature aus

[quick_tag]
enabled = true
hotkey  = "Ctrl+Alt+T"

[communication]
process_names         = ["teams.exe"]   # parallel erfasst, sobald ein sichtbares
                                        # Fenster offen ist; Auto-Tags daraus
                                        # übersteuern Fokus-Auto-Tags.
title_exclude_phrases = []              # Substrings (case-insensitive). Enthält
                                        # der Fenstertitel einen davon, zählt
                                        # das Fenster nicht als Comm-Fenster
                                        # sondern als normaler Prozess.

[work_schedule]
start_hour = 8                          # inklusiv, lokale Zeit
end_hour   = 18                         # exklusiv, lokale Zeit; 18 = "bis 17:59:59"
work_days  = ["Mon","Tue","Wed","Thu","Fri"]   # Mo–So; bestimmt zusammen mit
                                               # start/end_hour die Off-Hours-
                                               # Definition für die Rufbereitschaft.

[oncall]
tag_ids = []                            # Hier die IDs der Tags eintragen, die als
                                        # Rufbereitschafts-Tags zählen sollen.
                                        # Leer = Rufbereitschaft passiv.
                                        # Sub-Tags zählen über die Hierarchie
                                        # automatisch mit.
```

Nach manuellen Änderungen ist ein **Neustart** der Anwendung erforderlich,
damit die neuen Werte greifen. Beim Speichern aus der UI heraus wird die
Datei direkt aktualisiert; Werte werden zudem zur Laufzeit übernommen.

## Speicherorte (kurz)

- **Konfiguration:** `%APPDATA%\TimeTracker\config.toml`
- **Datenbank:** `%LOCALAPPDATA%\TimeTracker\data.db`
- **Logs:** `%LOCALAPPDATA%\TimeTracker\log\`
- **Personio-Session:** Windows Credential Manager
  (`TimeTracker.PersonioSession`)
- **Entra-ID-Token-Cache:** `%LOCALAPPDATA%\TimeTracker\auth\msal_cache.bin`
  (DPAPI-verschlüsselt, CurrentUser-Scope)
