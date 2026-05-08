import { useState } from "react";
import type { ImportResult, SyncPreflight, SyncResult } from "../types";

interface Props {
  preflight: SyncPreflight;
  // override and importDay receive the day (YYYY-MM-DD) the modal was
  // opened for; the caller decides which API to invoke (App.SyncDay or
  // App.ImportPersonioDay) and returns its result so the modal can fold
  // it into a final banner.
  onOverride: (
    day: string,
  ) => Promise<SyncResult>;
  onImport: (day: string) => Promise<ImportResult>;
  onClose: (banner: ResolvedBanner | null) => void;
}

export interface ResolvedBanner {
  level: "success" | "info" | "error";
  text: string;
}

// Convert a local-naive "YYYY-MM-DDTHH:MM:SS" timestamp to "HH:MM" without
// timezone conversion — Personio already returns these in the user's local
// time, so anything else (Date parsing, toLocaleString) would shift them.
function hhmm(localNaiveISO: string): string {
  const t = localNaiveISO.split("T")[1] ?? "";
  return t.slice(0, 5);
}

function fmtHM(sec: number): string {
  if (sec <= 0) return "0m";
  const h = Math.floor(sec / 3600);
  const m = Math.round((sec % 3600) / 60);
  if (h === 0) return `${m}m`;
  if (m === 0) return `${h}h`;
  return `${h}h ${m}m`;
}

export default function SyncConflictModal({
  preflight,
  onOverride,
  onImport,
  onClose,
}: Props) {
  const [busy, setBusy] = useState<"override" | "import" | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function handleOverride() {
    setBusy("override");
    setError(null);
    try {
      const r = await onOverride(preflight.day);
      const errs = r.Errors ?? [];
      if (errs.length > 0) {
        onClose({
          level: "error",
          text: `Sync mit Fehlern: ${errs.join("; ")}`,
        });
        return;
      }
      const banner: ResolvedBanner =
        r.Periods > 0
          ? {
              level: "success",
              text: `Personio überschrieben: ${r.Periods} Periode(n), ${r.BlocksProcessed} Block/Blöcke gebucht${
                r.BlocksSkipped > 0 ? ` (${r.BlocksSkipped} übersprungen)` : ""
              }.`,
            }
          : {
              level: "info",
              text: r.BlocksSkipped > 0
                ? `Nichts an Personio gesendet — alle ${r.BlocksSkipped} Block/Blöcke übersprungen.`
                : "Keine getaggten Blöcke für diesen Tag.",
            };
      onClose(banner);
    } catch (e) {
      setError(`Sync fehlgeschlagen: ${String(e)}`);
      setBusy(null);
    }
  }

  async function handleImport() {
    setBusy("import");
    setError(null);
    try {
      const r = await onImport(preflight.day);
      const errs = r.errors ?? [];
      if (r.blocks_created === 0 && errs.length === 0) {
        onClose({
          level: "info",
          text: "Nichts importiert — alle Personio-Perioden waren bereits durch lokale Tag-Blöcke abgedeckt.",
        });
        return;
      }
      const parts = [
        `${r.blocks_created} Tag-Block/Blöcke aus ${r.periods_considered} Personio-Periode(n) importiert`,
      ];
      if (r.periods_skipped > 0) {
        parts.push(`${r.periods_skipped} übersprungen`);
      }
      if (r.fallback_tag_used) {
        parts.push(`Fallback-Tag "Personio Import" angelegt/genutzt`);
      }
      const banner: ResolvedBanner = {
        level: errs.length > 0 ? "error" : "success",
        text: parts.join(", ") + (errs.length > 0 ? ` — Fehler: ${errs.join("; ")}` : "."),
      };
      onClose(banner);
    } catch (e) {
      setError(`Import fehlgeschlagen: ${String(e)}`);
      setBusy(null);
    }
  }

  const periods = preflight.existing_periods;
  const trackable = preflight.trackable;
  const total = preflight.local_duration_sec;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 px-4">
      <div className="w-full max-w-2xl rounded-lg bg-surface p-5 shadow-xl ring-1 ring-slate-700">
        <h2 className="text-lg font-semibold text-slate-100">
          Personio hat bereits Einträge — wie weiter?
        </h2>
        <p className="mt-1 text-sm text-slate-400">
          Tag <span className="font-mono">{preflight.day}</span>
          {!trackable && (
            <span className="ml-2 rounded bg-amber-900/40 px-2 py-0.5 text-xs text-amber-200">
              Status: {preflight.state || "nicht buchbar"}
            </span>
          )}
        </p>

        <div className="mt-4">
          <h3 className="mb-1 text-xs uppercase tracking-wide text-slate-500">
            Bestehende Personio-Perioden
          </h3>
          <ul className="max-h-64 divide-y divide-slate-700 overflow-y-auto rounded bg-slate-900/60 text-sm">
            {periods.length === 0 && (
              <li className="px-3 py-2 text-slate-500">
                (Keine — der Tag ist auf Personio leer.)
              </li>
            )}
            {periods.map((p) => (
              <li key={p.id} className="flex items-center gap-3 px-3 py-2">
                <span className="w-24 font-mono text-xs text-slate-300">
                  {hhmm(p.start)}–{hhmm(p.end)}
                </span>
                <span className="w-32 truncate text-xs text-slate-400">
                  {p.tag_name ? p.tag_name : p.project_id ? `Projekt ${p.project_id}` : "(kein Projekt)"}
                </span>
                <span className="flex-1 truncate text-xs italic text-slate-400" title={p.comment}>
                  {p.comment || "—"}
                </span>
              </li>
            ))}
          </ul>
        </div>

        <p className="mt-3 text-sm text-slate-400">
          Lokal: {preflight.local_block_count} Tag-Block/Blöcke
          {total > 0 && <> · {fmtHM(total)} getaggt</>}
        </p>

        <div className="mt-3 space-y-1 rounded bg-slate-900/40 px-3 py-2 text-xs text-slate-400">
          <p>
            <span className="font-semibold text-slate-200">Importieren</span> —
            Personio-Perioden werden als Tag-Blöcke nach Hashpoint übernommen.
            Bestehende Hashpoint-Blöcke gewinnen — überlappende Importe werden
            zurechtgeschnitten. Personio bleibt unverändert; nach Review kann
            erneut synchronisiert werden.
          </p>
          <p>
            <span className="font-semibold text-slate-200">Überschreiben</span> —
            Personio wird mit dem aktuellen Hashpoint-Stand ersetzt; manuelle
            Personio-Einträge gehen verloren.
          </p>
        </div>

        {error && (
          <div className="mt-3 rounded bg-red-900/40 px-3 py-2 text-sm text-red-200">
            {error}
          </div>
        )}

        <div className="mt-4 flex flex-wrap items-center justify-end gap-2">
          <button
            onClick={() => onClose(null)}
            disabled={busy != null}
            className="rounded bg-slate-700 px-3 py-1.5 text-sm hover:bg-slate-600 disabled:opacity-50"
          >
            Abbrechen
          </button>
          <button
            onClick={handleImport}
            disabled={busy != null || periods.length === 0}
            className="rounded bg-emerald-700 px-3 py-1.5 text-sm text-white hover:bg-emerald-600 disabled:opacity-50"
            title={periods.length === 0 ? "Keine Personio-Perioden zum Importieren" : undefined}
          >
            {busy === "import" ? "Importiere…" : "Aus Personio importieren"}
          </button>
          <button
            onClick={handleOverride}
            disabled={busy != null || !trackable}
            className="rounded bg-red-700 px-3 py-1.5 text-sm text-white hover:bg-red-600 disabled:opacity-50"
            title={!trackable ? "Tag ist nicht beschreibbar" : undefined}
          >
            {busy === "override" ? "Überschreibe…" : "Trotzdem überschreiben"}
          </button>
        </div>
      </div>
    </div>
  );
}
