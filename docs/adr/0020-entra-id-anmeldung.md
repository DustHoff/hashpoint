# ADR 0020 – Optionale Microsoft-Entra-ID-Anmeldung (Public Client, PKCE, DPAPI-Cache)

- **Status:** Akzeptiert (retrospektiv dokumentiert)
- **Datum:** 2026-09-28
- **Bezug:** `internal/entra`, `EntraBadge.tsx`; `docs/user/entra-id.md`, `docs/technical/entra-api.md`

## Kontext

Plugins und künftige Module sollen auf Microsoft Graph (Kalender, SharePoint) und Entra-geschützte Firmen-APIs zugreifen. Client-Secrets auf Endgeräten sind unzulässig; auf Entra-joined Geräten soll die Anmeldung ohne Prompt laufen.

## Entscheidung

- **Additives Feature**: nur aktiv, wenn Client- und Tenant-ID konfiguriert sind; sonst wird kein Auth-Code ausgeführt.
- **MSAL Public Client** mit **Loopback-Redirect + PKCE** im Standardbrowser (PRT-SSO), **kein Client Secret**; **Single-Tenant** (`common`/`organizations`/`consumers` werden abgelehnt).
- Token-Cache als eine Datei `%LOCALAPPDATA%\TimeTracker\auth\msal_cache.bin`, **DPAPI (CurrentUser)**-verschlüsselt, atomar geschrieben; Abmelden löscht die Datei.
- Folgeaufrufe nur **silent**; Plugins erhalten Tokens über `HostAPI.RequestEntraToken` ohne interaktiven Fallback.
- **Status-Badge** mit stiller Prüfung alle 60 s (nur sichtbar, wenn konfiguriert).

## Konsequenzen

- ✅ Keine Secrets in App-Registrierung oder Config; Tokens geräte-/benutzergebunden.
- ✅ Kein Einfluss auf Anwender ohne Entra-Bedarf.
- ⚠️ App-Registrierung inkl. Admin-Consent durch die IT nötig.
- ⚠️ Firefox ohne PRT-SSO (normaler Login-Prompt).
