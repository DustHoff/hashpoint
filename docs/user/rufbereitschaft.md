# Rufbereitschaft

Hashpoint erkennt automatisch Tag-Blöcke, die **außerhalb Ihrer
Arbeitszeit** an einem als „Rufbereitschaft" markierten Tag liegen, und
legt für jeden eine Dokumentations-Zeile in der **Rufbereitschafts-Inbox**
an. Sie füllen das Formular mit Anwendung, Vorgangs-Art und Lösung aus
und übertragen die Doku an Ihr Ticket-/Dokumentationssystem — die
Übertragung selbst läuft über installierte Plugins (z. B. Jira, OTRS,
ServiceNow). Hashpoint pusht nicht selbst.

Der Tab heißt **Rufbereitschaft** und steht zwischen **Zeitachse** und
**Tags** in der Tab-Leiste.

## Wann entsteht eine Doku-Zeile?

Ein Tag-Block landet automatisch in der Inbox, sobald **alle drei**
Bedingungen erfüllt sind:

1. **Der Block ist abgeschlossen** (hat ein Ende).
2. **Er liegt zumindest teilweise außerhalb der Arbeitszeit** —
   entweder auf einem Nicht-Arbeitstag (z. B. Samstag, Sonntag, oder
   einem Feiertag, den ein Plugin signalisiert hat — siehe unten) oder
   außerhalb der konfigurierten Stunden (`work_schedule.start_hour ..
   end_hour`).
3. **Sein Tag ist als Rufbereitschafts-Tag gekennzeichnet** — entweder
   direkt oder über die Tag-Hierarchie (z. B. `#oncall/billing` zählt
   automatisch, wenn `#oncall` als Rufbereitschafts-Tag eingetragen
   ist).

Die Tags pflegen Sie unter **Einstellungen → Rufbereitschaft** über die
Multi-Select-Auswahl „Rufbereitschafts-Tags". Untergeordnete Tags zählen
automatisch mit — es genügt, den Root-Tag auszuwählen. Solange die Liste
leer ist, bleibt die Rufbereitschafts-Funktion **passiv** und es werden
keine Doku-Zeilen erzeugt. Wird ein zuvor markierter Tag gelöscht,
entfernt Hashpoint die zugehörige ID automatisch aus der Konfiguration.

## Aufbau des Tabs

| Bereich | Inhalt |
| --- | --- |
| **Inbox (links)** | Eine Zeile pro erkannter Rufbereitschafts-Phase. Pro Eintrag sehen Sie den Tag, das Zeitfenster und einen Status-Chip. |
| **Detailbereich (rechts)** | Das Formular zum aktuell ausgewählten Eintrag plus die Übertragungs-Buttons und die Liste der Plugin-Antworten. |

Über der Inbox liegt der Schalter **Veraltete anzeigen**. Damit blenden
Sie Einträge ein, deren Block sich zwischenzeitlich aus der
Rufbereitschafts-Qualifikation verschoben hat (siehe
[Veraltete Einträge](#veraltete-eintraege)).

## Status-Werte

Eine Doku durchläuft folgende Zustände:

| Status      | Bedeutung |
| --- | --- |
| **Entwurf** | Sie haben die Doku noch nicht übertragen. Die Felder sind editierbar. |
| **In Übertragung** | Mindestens eine Plugin-Antwort steht noch aus. Die Felder sind gesperrt. |
| **Übertragen** | Alle Plugins haben die Doku erfolgreich angenommen. |
| **Teilweise** | Mindestens ein Plugin hat erfolgreich übertragen, ein anderes ist gescheitert. Sie können nur die gescheiterten erneut versuchen — bereits erfolgreich übertragene Doku-Zeilen werden nicht doppelt angelegt. |
| **Fehlgeschlagen** | Kein Plugin konnte die Doku übertragen. Die Felder sind wieder editierbar; **Erneut versuchen** startet einen neuen Versuch. |

## Das Formular

Pro Doku können Sie folgendes erfassen:

| Feld | Beschreibung |
| --- | --- |
| **Betroffene Anwendung** | Welches System, welcher Service war betroffen? Freitext. |
| **Art des Vorgangs** | *Geplante Wartung* (`planned_maintenance`) oder *Servicestörung* (`service_disruption`). Bestimmt, welche Felder das Zielsystem typischerweise erwartet. |
| **Lösung / Bearbeitung** | Was wurde getan, wer war beteiligt, wie wurde der Vorfall geschlossen? Freitext, mehrzeilig. |

Buttons im Detailbereich:

- **Entwurf speichern** — speichert Ihre Änderungen, ohne sie an Plugins
  zu übertragen. Sicher zum Zwischenspeichern.
- **An Dokumentationssystem senden** — speichert den aktuellen Stand und
  schickt ihn parallel an alle laufenden Plugins, die die Capability
  `oncall_documentation` ausspielen. Jedes Plugin antwortet einzeln; die
  Antworten erscheinen unten im Detailbereich als **Plugin-Status**.
- **Erneut versuchen** (statt „Senden", wenn Status = *Fehlgeschlagen*)
  — neuer Übertragungsversuch ausschließlich an die zuvor gescheiterten
  Plugins.

Pro Plugin sehen Sie nach der Übertragung:

- den **Plugin-Namen**,
- ggf. eine **Referenz** auf das angelegte Ticket (mit klickbarem Link,
  wenn das Plugin eine URL zurückliefert),
- den **Plugin-Status** (`pending`, `submitted`, `failed`).

> **Hinweis:** Ohne installiertes Plugin macht der Button **Senden**
> nichts Sichtbares — die Doku bleibt im Status *Entwurf*. Plugins
> installieren Sie über den Tab **Verfügbare Plugins**; eine Übersicht
> der gerade aktiven Plugins finden Sie unter **Plugins**.

## Veraltete Einträge

Wenn Sie einen Block nach der Doku-Erzeugung umtaggen oder seine
Zeitspanne so verändern, dass er sich **nicht mehr** für eine
Rufbereitschafts-Doku qualifiziert, bleibt die ursprüngliche Doku
bestehen — Hashpoint löscht sie nicht still. Stattdessen wird sie als
**veraltet** markiert; ein gelbes Banner erscheint im Detailbereich mit
dem Hinweis *„Dieser Block qualifiziert sich nicht mehr für die
Rufbereitschafts-Dokumentation."* Zwei Optionen:

- **Verwerfen** — löscht die Doku-Zeile endgültig. Wird abgelehnt, wenn
  bereits ein Plugin die Doku erfolgreich übernommen hat (dann müssen
  Sie das Ticket zuerst dort manuell schließen).
- **Beibehalten** — Sie ignorieren das Banner und können die Doku
  trotzdem ausfüllen oder übertragen. Sinnvoll, wenn die Änderung am
  Block ein Tipp-Fehler war oder der Block bald wieder qualifiziert
  wird.

Veraltete Einträge sind standardmäßig **ausgeblendet**. Aktivieren Sie
die Checkbox **Veraltete anzeigen** oben in der Inbox, um sie sichtbar
zu machen.

Verschiebt sich ein als veraltet markierter Block später wieder in die
Qualifikation zurück (z. B. weil Sie ihn erneut umtaggen), wird das
Veraltet-Banner automatisch entfernt — die Doku ist wieder aktiv.

## Plugin-Erweiterung: dynamische Feiertage und Sonderschichten

Standardmäßig ergibt sich „Arbeitszeit" aus zwei Werten in der
`config.toml`:

- `work_schedule.work_days` — die Wochentage, die als Arbeitstage
  zählen (z. B. `["Mon","Tue","Wed","Thu","Fri"]`).
- `work_schedule.start_hour` / `end_hour` — das Stundenfenster an einem
  Arbeitstag (z. B. 8 bis 18 Uhr).

Alles außerhalb dieser Werte ist „Off-Hours" — und genau das löst die
Doku-Erzeugung aus. Hashpoint kennt **keine eingebaute Feiertagsliste**:
Der 25. Dezember auf einem Donnerstag wäre für Hashpoint im
Grundzustand ein normaler Arbeitstag.

**Hier kommen Plugins ins Spiel.** Plugins, die die Capability
`off_hours_provider` ausspielen, können der Off-Hours-Erkennung
beliebige Zeitfenster hinzufügen — typische Anwendungsfälle:

- ein Plugin meldet alle gesetzlichen Feiertage Ihres Bundeslandes,
- ein Plugin meldet betriebsspezifische Brückentage oder Betriebsruhe,
- ein Plugin meldet **umgekehrt**, dass ein bestimmter Samstag *kein*
  Off-Hours-Tag ist, weil eine reguläre Sonderschicht stattfindet
  (dann erzeugt Hashpoint dafür **keine** Rufbereitschafts-Doku).

Welche Plugins solche Off-Hours liefern, hängt davon ab, was Sie unter
**Plugins** installiert haben. Hashpoint liefert kein eigenes
Feiertags-Plugin mit — Sie installieren entweder eines aus einem
verfügbaren Plugin-Katalog oder schreiben ein eigenes
(siehe Entwickler-Doku unter `docs/plugins/`).

**Wichtige Punkte zum Verhalten:**

- Ein neu installiertes Plugin wirkt **ab sofort auf neu mutierte
  Blöcke**. Doku-Zeilen für bereits abgeschlossene historische Blöcke
  werden nicht rückwirkend erzeugt. Wollen Sie eine vergangene
  Rufbereitschaft an einem Feiertag dokumentieren, ändern Sie den
  betroffenen Tag-Block kurz (z. B. Tag wechseln und zurück) — die
  Mutation triggert die Qualifikation neu, und die Doku-Zeile entsteht.
- Wenn ein Off-Hours-Plugin **nicht läuft** (gestoppt, deaktiviert,
  abgestürzt), liefert es auch keine Feiertage. Bestehende Doku-Zeilen
  bleiben dadurch erhalten — sie werden nicht still gelöscht, wenn das
  Plugin später ausfällt.
- Bei mehreren Plugins gewinnt **„working hours" immer**: meldet
  Plugin A einen Tag als Feiertag und Plugin B denselben Tag als
  Sonderschicht, wird der Tag als Arbeitstag behandelt (keine
  Doku-Erzeugung).

## Beziehung zu anderen Funktionen

- **Tag-Hierarchie:** Ein Tag-Block mit Tag `#oncall/billing` qualifiziert
  automatisch, wenn `#oncall` als Rufbereitschafts-Tag eingetragen ist —
  egal wie tief der Sub-Tag verschachtelt ist. Siehe [Tags](tags.md).
- **Personio-Sync:** Eine Rufbereitschafts-Doku ist **unabhängig** vom
  Personio-Sync. Der zugrundeliegende Tag-Block wird genauso synchronisiert
  wie jeder andere; die Doku-Übertragung läuft separat über das Plugin.
- **Verfügbare Plugins:** Die Liste der installierbaren Plugins finden Sie
  im Tab **Verfügbare Plugins**. Welche Plugins gerade aktiv sind, sehen
  Sie unter **Plugins**.
