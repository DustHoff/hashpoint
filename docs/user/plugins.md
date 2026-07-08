# Plugins

Plugins erweitern Hashpoint um Funktionen, die nicht fest eingebaut sind. Ein
Plugin ist ein **eigenständiges Programm**, das Hashpoint als Unterprozess
startet und über eine definierte Schnittstelle anspricht. Zwei Tabs im
Hauptfenster gehören dazu: **Verfügbare Plugins** (Katalog zum Installieren) und
**Plugins** (Verwaltung der installierten Plugins).

> Für Entwickler, die eigene Plugins bauen wollen, gibt es eine separate
> technische Dokumentation unter `docs/plugins/`.

## Was ein Plugin beisteuern kann

Jedes Plugin meldet eine oder mehrere **Fähigkeiten** (Capabilities) an. Für Sie
als Anwender relevant:

- **Auto-Tag-Provider** — liefert automatische Tag-Zuordnungen, wenn keine Ihrer
  eigenen [Auto-Tagging-Regeln](auto-tagging.md) greift.
- **Tag-Provider** — liefert einen Tag- bzw. **Auftrags-Katalog**; daraus speist
  sich u. a. das Feld **Auftrag** im [Tag-Editor](tags.md).
- **Off-Hours-Provider** — liefert dynamische Off-Hours-Zeiten (z. B. Feiertage,
  Brückentage) für die [Rufbereitschaft](rufbereitschaft.md).
- **On-Call-Dokumentation** — überträgt Rufbereitschafts-Dokumentationen an ein
  externes System (z. B. Jira, OTRS, ServiceNow).
- **Plugin-Verwaltung** — stellt einen Katalog installierbarer Plugins bereit,
  der im Tab **Verfügbare Plugins** erscheint.

## Tab „Verfügbare Plugins"

Hier erscheinen die Kataloge aller aktiven **Plugin-Quellen** (Plugins mit der
Fähigkeit *Plugin-Verwaltung*). Pro Eintrag sehen Sie Name, Version, eine kurze
Beschreibung und einen Status:

- **installiert** — bereits installiert und aktuell.
- **Update verfügbar** — eine neuere Version steht bereit.

Die Aktionen **Installieren**, **Aktualisieren** und **Entfernen** werden jeweils
**durch die Quelle** ausgeführt. Ist keine Quelle aktiv, bleibt der Katalog leer —
dann können Sie ein Plugin nur manuell in den Plugin-Ordner legen (siehe unten).

## Tab „Plugins" (installierte verwalten)

Die linke Spalte listet alle installierten Plugins mit einem **Status-Badge**:

| Status | Bedeutung |
| --- | --- |
| **Aktiv** | Läuft und ist einsatzbereit. |
| **Konfiguration fehlt** | Pflichtfelder sind noch nicht ausgefüllt; das Plugin wurde noch nicht gestartet. |
| **Fehler** | Der Unterprozess ist abgestürzt oder die Konfiguration ist ungültig (Fehlermeldung in den Details). |
| **Deaktiviert** | Über den Schalter ausgeschaltet. |
| **Genehmigung ausstehend** | Neu gefunden und noch nicht zur Ausführung freigegeben (siehe [Genehmigung](#genehmigung--sicherheit)). |

Über den **Schalter** rechts aktivieren oder deaktivieren Sie ein Plugin. In der
rechten Detailspalte finden Sie:

- **Konfigurationsfelder** je nach Plugin: Text, Passwort oder An/Aus. Passwörter
  werden **verschlüsselt** gespeichert und in der Oberfläche nur als „gespeichert"
  angezeigt, nie im Klartext.
- **Speichern** — übernimmt die Konfiguration.
- **Neu starten** — startet den Plugin-Unterprozess neu (z. B. nach einem Fehler).
- **Tags neu laden** — nur bei *Tag-Provider*-Plugins: importiert deren
  Tag-Katalog erneut (bestehende Tags werden nicht überschrieben).

## Genehmigung & Sicherheit

Weil ein Plugin ein eigenständiges Programm mit Zugriff auf sensible Funktionen
ist, gibt es ein **Freigabe-Gate**:

- Plugins, die über den **Installer** oder den Tab **Verfügbare Plugins** kommen,
  sind automatisch genehmigt.
- Plugins, die Sie **manuell** in den Plugin-Ordner kopieren, stehen zunächst auf
  **Genehmigung ausstehend**. Erst nach Klick auf **Plugin genehmigen und starten**
  werden sie ausgeführt.

**Geben Sie ein Plugin nur frei, wenn Sie der Quelle vertrauen.** Ein laufendes
Plugin kann über den Host:

- Ihre **Personio-Session** anfordern (Cookies und CSRF-Token — **nicht** Ihr
  Passwort) und damit im Personio-UI-API in Ihrem Namen agieren,
- ein **Entra-ID-Token** für angeforderte Scopes anfordern,
- Tags lesen und veröffentlichen.

## Wo Plugins liegen

Hashpoint sucht Plugins unter `%APPDATA%\TimeTracker\plugins\<plugin-name>\` —
jeweils eine `manifest.toml` plus die Plugin-Binärdatei. Der Ordner wird
regelmäßig neu eingelesen, ein frisch abgelegtes Plugin taucht also nach kurzer
Zeit als **Genehmigung ausstehend** auf.
