# Feedback & Fehler melden

Über den Tab **Feedback** melden Sie Fehler, Feature-Wünsche und Fragen direkt
als **GitHub-Issue** im Projekt-Repository — ohne den Browser zu verlassen oder
sich Logdateien manuell zusammenzusuchen. Der Tab kümmert sich um die Anmeldung,
das Formular und optional das Anhängen der letzten Log-Einträge.

## Mit GitHub verbinden

Bevor Sie etwas absenden können, verbinden Sie Hashpoint einmalig mit Ihrem
GitHub-Konto. Das läuft über den **Device-Flow** — Sie geben also kein Passwort
in Hashpoint ein:

1. Oben im Tab auf **Mit GitHub verbinden** klicken.
2. Hashpoint zeigt einen kurzen **Gerätecode** und öffnet die Seite
   `https://github.com/login/device`.
3. Auf dieser Seite den Code eingeben und die Freigabe bestätigen.
4. Danach zeigt der Tab **„Verbunden mit GitHub als @&lt;login&gt;"**.

> Der Gerätecode und der eigentliche Zugriffstoken verlassen das Backend nicht:
> Der Token wird verschlüsselt abgelegt (Windows Credential Manager), nur der
> anzuzeigende Login-Name wird zwischengespeichert. Über **Trennen** lösen Sie die
> Verbindung wieder und löschen den Token.

## Das Meldeformular

Ist die Verbindung hergestellt, füllen Sie das Formular aus:

| Feld | Pflicht | Bedeutung |
| --- | --- | --- |
| **Titel** | ja | Kurze Zusammenfassung (max. 200 Zeichen). |
| **Kategorie** | ja | `Fehler`, `Feature-Wunsch` oder `Frage`. |
| **Schweregrad** | ja | `Niedrig`, `Mittel`, `Hoch` oder `Kritisch`. |
| **Beschreibung** | ja | Was ist passiert? |
| **Erwartetes Verhalten** | nein | Was hätten Sie erwartet? |
| **Tatsächliches Verhalten** | nein | Was ist stattdessen passiert? |
| **Schritte zur Reproduktion** | nein | Nummerierte Schritte, mit denen sich das Problem nachstellen lässt. |

## Anwendungslog anhängen

Aktivieren Sie **Anwendungslog anhängen**, um die Fehlersuche zu erleichtern.
Dann erscheint zusätzlich ein Zeitfenster:

- **Heute** (Standard)
- **Letzte Stunde**
- **Letzte 24 Stunden**

Angehängt werden die Log-Einträge des gewählten Fensters sowie **erkannte
Abstürze**. Zum Schutz Ihrer Privatsphäre werden dabei **Debug-Einträge und
Fenstertitel vor dem Upload entfernt** — es landen also keine Fenstertitel oder
sensiblen Detail-Logs im öffentlichen Issue.

## Vorschau und Absenden

1. **Preview** klicken — Hashpoint zeigt den vollständigen Markdown-Text, so wie
   das Issue erstellt wird (inklusive des Log-Anhangs, falls aktiviert). Das ist
   Ihre Kontrollmöglichkeit, bevor irgendetwas hochgeladen wird.
2. Im Vorschau-Dialog auf **Absenden** klicken.
3. Nach dem Anlegen erscheint **„Issue #&lt;Nummer&gt; angelegt"** mit einem Link
   **Auf GitHub öffnen**.

Passende **Labels** (aus Kategorie und Schweregrad) werden automatisch gesetzt,
sofern die GitHub-App-Installation im Ziel-Repository die nötigen Rechte hat —
fehlen die Rechte, wird das Issue trotzdem ohne die betreffenden Labels angelegt.

> Der Tab spricht ausschließlich mit GitHub. Es werden keine Zugangsdaten
> übertragen, und die Verbindung lässt sich jederzeit über **Trennen** wieder
> lösen.
