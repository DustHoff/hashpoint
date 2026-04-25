# Tags verwalten

Tags sind die Kategorien, mit denen Sie Fokus-Blöcke einem Projekt oder einer Aktivität zuordnen. Sie sind die Brücke zur Personio-Synchronisation: Nur Blöcke mit Tag, der ein Personio-Mapping besitzt, werden übertragen.

## Tag-Hierarchie

Tags sind in **zwei** Ebenen organisiert:

- **Eltern-Tag** *(Top-Level, z. B. `#ProjektA`)*
  - Hat einen Namen, eine Farbe und optional eine Personio-Projekt-/Aktivitäts-ID.
  - Mehrere Eltern-Tags sind möglich (typischerweise: ein Tag pro Projekt oder Kostenstelle).

- **Sub-Tag** *(Kind, z. B. `#ProjektA#Recherche`)*
  - Hängt an genau einem Eltern-Tag.
  - Hat einen Namen, eine Beschreibung und kann das Personio-Mapping vom Eltern-Tag erben oder überschreiben.

> Tiefere Ebenen werden nicht unterstützt – die Struktur ist bewusst flach.

## Aufbau des Tabs

Der Tab **Tags** ist zweispaltig:

- **Linke Spalte – Tag-Liste:** Alle Eltern-Tags untereinander, mit eingerückten Sub-Tags darunter.
- **Rechte Spalte – Editor:** Formular zum Anlegen oder Bearbeiten eines Tags.

## Neuen Tag anlegen

1. Tab **Tags** öffnen.
2. Im rechten Editor das Formular ausfüllen.
3. **Speichern** klicken.

### Felder im Editor

| Feld | Pflicht | Bedeutung |
| --- | --- | --- |
| **Name** | ja | Tag-Name. Format: `^#[A-Za-z0-9]+$` (führendes `#`, danach nur Buchstaben/Ziffern). Das `#` wird automatisch ergänzt, wenn Sie es weglassen. |
| **Parent-Tag** | nein | Übergeordneter Tag. Leer = Eltern-Tag, ausgewählt = Sub-Tag. |
| **Beschreibung** | nein | Freitext (nur bei Sub-Tags). Wird im Personio-Kommentar verwendet. |
| **Farbe** | nein | HTML-Color-Picker. Default `#4f8cff`. Wird im Block-Chip in der Zeitachse gezeigt. |
| **Personio Project ID** | für Sync | ID des Projekts in Personio (z. B. `12345`). Sub-Tags erben den Wert vom Eltern-Tag, wenn leer. |
| **Personio Activity ID** | für Sync | ID der Aktivität in Personio (z. B. `67890`). Sub-Tags erben den Wert vom Eltern-Tag, wenn leer. |
| **Zu Personio synchronisieren** | nein | Default an. Bei deaktivierter Checkbox werden Blöcke mit diesem Tag **nicht** übertragen. |

### Namens-Regeln

- Erlaubte Zeichen: `A`–`Z`, `a`–`z`, `0`–`9`
- Das `#` wird automatisch normalisiert (mit oder ohne `#` eingeben)
- Leerzeichen, Sonderzeichen oder Umlaute sind nicht erlaubt
- Beispiele: `#Frontend`, `#OpsBackup`, `#Meeting`

## Tag bearbeiten

1. In der Tag-Liste auf **Bearbeiten** beim gewünschten Tag klicken.
2. Felder im Editor anpassen.
3. **Speichern** klicken.

> Eine Änderung der Personio-IDs wirkt nur auf zukünftige Synchronisationen. Bereits übertragene Blöcke bleiben mit der alten Zuordnung in Personio.

## Tag löschen

1. **Löschen** beim gewünschten Tag klicken.
2. Sicherheitsabfrage bestätigen.

> Das Löschen eines **Eltern-Tags** entfernt automatisch alle Sub-Tags. Blöcke, die mit diesem Tag verknüpft waren, verlieren ihre Zuweisung – die Blöcke selbst bleiben aber erhalten.

## Personio-Mapping – Vererbung

Beim Sync wird für jeden Block die effektive Personio-Zuordnung wie folgt ermittelt:

1. Hat der Tag (Eltern oder Sub) eine `personio_project_id`? → diese verwenden.
2. Sonst: vom Eltern-Tag erben.
3. Analog für `personio_activity_id`.
4. Ist `Zu Personio synchronisieren` deaktiviert oder ist die Project-ID nach Vererbung leer → Block wird **übersprungen**.

## Praxis-Tipp: Strukturieren

Eine bewährte Struktur:

- Pro **Projekt** oder **Kostenstelle** einen Eltern-Tag mit Personio-Projekt-ID
- Sub-Tags für Tätigkeitsarten (z. B. `#Coding`, `#Meeting`, `#Review`) mit einer abweichenden Personio-Aktivitäts-ID
- Pausen/Privates: kein Tag (oder ein Eltern-Tag mit `Zu Personio synchronisieren = aus`)
