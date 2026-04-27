# Hashpoint TimeTracker

Windows-Zeiterfassung mit Personio-Sync.

## Installation

1. Aktuelle `hashpoint.exe` aus dem [Releases](../../releases)-Tab herunterladen.
2. Beim ersten Start ggf. Defender / SmartScreen entsperren — siehe unten.
3. Beim Klick auf „Weitere Informationen" → „Trotzdem ausführen".

## Defender / SmartScreen-Hinweis

Da Hashpoint nicht code-signiert ist (privates Open-Source-Projekt), kann
Windows Defender beim ersten Start einen heuristischen Treffer melden
(`Trojan:Win32/Wacatac.H!ml`) oder SmartScreen die Ausführung blockieren. Das
ist ein bekannter False Positive bei nicht-signierten Go-/Wails-Binaries mit
Window-Tracking-Funktionalität.

### Wenn Defender die Datei in Quarantäne verschiebt

Bei aktiver Wacatac-Detection wird die `.exe` direkt nach dem Download nach
`C:\ProgramData\Microsoft\Windows Defender\Quarantine\` verschoben — sie
liegt dann nicht mehr im Downloads-Ordner und `Unblock-File` greift ins
Leere. Empfohlene Reihenfolge:

**1. Ordner-Ausnahme setzen, *bevor* erneut heruntergeladen wird** (PowerShell
als Administrator):

```powershell
Add-MpPreference -ExclusionPath "$HOME\Downloads\hashpoint.exe"
```

Alternativ den ganzen Downloads-Ordner: `Add-MpPreference -ExclusionPath
"$HOME\Downloads"`. Defender ignoriert dann diesen Pfad und quarantäniert
nicht mehr.

**2. Datei aus Quarantäne wiederherstellen** (falls Schritt 1 zu spät kam) —
über die Windows-Security-GUI: *Einstellungen → Datenschutz & Sicherheit →
Windows-Sicherheit → Viren- & Bedrohungsschutz → Schutzverlauf* → den
Hashpoint-Eintrag öffnen → *Aktion → Auf Gerät zulassen* (oder
*Wiederherstellen*).

Per CLI (Administrator-Konsole):

```powershell
& "C:\Program Files\Windows Defender\MpCmdRun.exe" -Restore -Name "Trojan:Win32/Wacatac.H!ml" -All
```

**3. Mark-of-the-Web entfernen** (sobald die Datei wieder im Downloads-Ordner
liegt):

```powershell
Unblock-File .\hashpoint.exe
```

`Unblock-File` allein reicht nur, wenn Defender die Datei nicht aktiv
quarantäniert, sondern lediglich SmartScreen-Hinweise zeigt.

### SHA-256 verifizieren

Jedes Release enthält eine `checksums.txt` mit dem SHA-256-Hash der
`hashpoint.exe`. Vor dem Ausführen vergleichen:

```powershell
Get-FileHash -Algorithm SHA256 .\hashpoint.exe
```

Der ausgegebene Hash muss exakt mit dem Wert in `checksums.txt` übereinstimmen.
Stimmt er nicht, wurde die Datei beim Download verändert — **nicht ausführen**.

## Build aus dem Source

Voraussetzungen:
- Go 1.26.2
- Node.js 20.18.1
- [`go-winres`](https://github.com/tc-hib/go-winres) (für eingebettetes Icon
  + Manifest + Versionsinfo): `go install github.com/tc-hib/go-winres@v0.3.3`

```bash
cd frontend && npm ci && npm run build && cd ..
go-winres make --in winres/winres.json --arch amd64 --out cmd/timetracker/hashpoint
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath -tags desktop,production \
    -ldflags "-s -w -buildid= -H windowsgui" \
    -o build/bin/hashpoint.exe ./cmd/timetracker
```

Die fertige `.exe` liegt unter `build/bin/hashpoint.exe`.
