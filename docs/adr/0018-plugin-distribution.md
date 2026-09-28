# ADR 0018 – Plugin-Distribution über Quell-Plugins und MSI-Seed als Versions-Floor

- **Status:** Akzeptiert (retrospektiv dokumentiert)
- **Datum:** 2026-09-28
- **Bezug:** `internal/plugin/{mgmt,seed}.go`, `AvailablePlugins.tsx`; `build/wix/hashpoint.wxs`; `docs/plugins/README.md`

## Kontext

Anwender sollen Plugins ohne manuelles Kopieren installieren und aktualisieren können. Der Kern soll dabei keine feste Bezugsquelle (Marketplace-URL) kennen.

## Entscheidung

- Die Capability **`plugin_management`** macht ein Plugin zur **Quelle**: es liefert einen Katalog und führt Install/Update/Uninstall auf Dateiebene aus. Der Tab **Verfügbare Plugins** vereinigt die Kataloge aller laufenden Quellen.
- Host-Pflichten: vor Update den Ziel-Subprozess stoppen (Windows-Dateisperre), nach Uninstall `plugin_state`/`plugin_settings` bereinigen; eine Quelle kann sich nicht selbst deinstallieren.
- Das MSI bündelt `hashpoint-plugin-manager` unter `plugins-seed`. Beim Start wird geseedet: fehlend → kopieren, Bundle **strikt neuer** → atomar überschreiben, sonst unverändert (**Floor, nie Cap**).

## Konsequenzen

- ✅ Kern bleibt quellenneutral; Organisationen können eigene Quellen betreiben.
- ✅ MSI-Upgrades heben veraltete Manager an, ohne neuere User-Installationen zurückzurollen.
- ⚠️ Ohne aktive Quelle ist der Katalog leer (nur manuelles Kopieren + Approval).
