import { useEffect, useState } from "react";
import { api } from "../api";
import { log } from "../lib/log";
import type { AppConfig, PersonioStatus } from "../types";

const emptyConfig: AppConfig = {
  tracking: {
    poll_interval_sec: 2,
    idle_threshold_min: 5,
    enabled: true,
    tag_block_granularity_min: 0,
  },
  personio: { tenant: "" },
  ui: { autostart: true },
};

// normalize defends against backends that omit (or rename) sub-objects so a
// single missing field doesn't blow up the whole render with a TypeError.
function normalize(c: Partial<AppConfig> | null | undefined): AppConfig {
  return {
    tracking: {
      poll_interval_sec:
        c?.tracking?.poll_interval_sec ?? emptyConfig.tracking.poll_interval_sec,
      idle_threshold_min:
        c?.tracking?.idle_threshold_min ?? emptyConfig.tracking.idle_threshold_min,
      enabled: c?.tracking?.enabled ?? emptyConfig.tracking.enabled,
      tag_block_granularity_min:
        c?.tracking?.tag_block_granularity_min ??
        emptyConfig.tracking.tag_block_granularity_min,
    },
    personio: { tenant: c?.personio?.tenant ?? "" },
    ui: { autostart: c?.ui?.autostart ?? emptyConfig.ui.autostart },
  };
}

export default function Settings() {
  const [config, setConfig] = useState<AppConfig>(emptyConfig);
  const [status, setStatus] = useState<PersonioStatus | null>(null);
  const [saving, setSaving] = useState(false);
  const [loggingIn, setLoggingIn] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function refresh() {
    try {
      const [c, s] = await Promise.all([api.getConfig(), api.personioStatus()]);
      log.debug("settings: loaded", { config: c, status: s });
      setConfig(normalize(c as Partial<AppConfig>));
      setStatus(s);
    } catch (e) {
      log.error("settings: refresh failed", { err: String(e) });
      setError(String(e));
    }
  }

  useEffect(() => {
    refresh();
  }, []);

  async function save() {
    setSaving(true);
    setError(null);
    setMessage(null);
    try {
      await api.saveConfig(config);
      setMessage("Einstellungen gespeichert.");
      await refresh();
    } catch (e) {
      setError(String(e));
    } finally {
      setSaving(false);
    }
  }

  async function login() {
    setLoggingIn(true);
    setError(null);
    setMessage(null);
    try {
      await api.personioLogin();
      setMessage("Personio-Anmeldung erfolgreich erfasst.");
      await refresh();
    } catch (e) {
      setError(String(e));
    } finally {
      setLoggingIn(false);
    }
  }

  async function logout() {
    setError(null);
    setMessage(null);
    try {
      await api.personioLogout();
      setMessage("Personio-Session entfernt.");
      await refresh();
    } catch (e) {
      setError(String(e));
    }
  }

  function update<K extends keyof AppConfig>(section: K, value: AppConfig[K]) {
    setConfig((c) => ({ ...c, [section]: value }));
  }

  return (
    <div className="mx-auto max-w-2xl space-y-6">
      <h2 className="text-xl font-semibold text-slate-100">Einstellungen</h2>

      {message && (
        <div className="rounded bg-emerald-900/40 px-3 py-2 text-sm text-emerald-200">
          {message}
        </div>
      )}
      {error && (
        <div className="rounded bg-red-900/40 px-3 py-2 text-sm text-red-200">
          {error}
        </div>
      )}

      {/* Tracking section ------------------------------------------------ */}
      <section className="space-y-3 rounded bg-surface p-4">
        <h3 className="text-sm font-semibold text-slate-200">Erfassung</h3>
        <label className="flex items-start gap-3 text-sm text-slate-300">
          <input
            type="checkbox"
            checked={config.tracking.enabled}
            onChange={(e) =>
              update("tracking", {
                ...config.tracking,
                enabled: e.target.checked,
              })
            }
            className="mt-0.5 h-4 w-4"
          />
          <span className="flex flex-col">
            <span>Erfassung der fokussierten Anwendung aktiv</span>
            <span className="text-[11px] text-slate-500">
              Wenn deaktiviert, werden keine Fokus-Blöcke mehr automatisch
              erfasst — manuelles Tagging über das Tray-Menü bleibt möglich.
            </span>
          </span>
        </label>
        <Field
          label="Poll-Intervall (Sekunden)"
          help="Wie oft der TimeTracker den fokussierten Prozess prüft. 1–300."
        >
          <input
            type="number"
            min={1}
            max={300}
            value={config.tracking.poll_interval_sec}
            onChange={(e) =>
              update("tracking", {
                ...config.tracking,
                poll_interval_sec: Number(e.target.value),
              })
            }
            className="w-24 rounded bg-slate-900/60 px-2 py-1 text-sm"
          />
        </Field>
        <Field
          label="Idle-Schwelle (Minuten)"
          help="Nach wie vielen Minuten ohne Eingabe ein Block als Idle markiert wird. 1–240."
        >
          <input
            type="number"
            min={1}
            max={240}
            value={config.tracking.idle_threshold_min}
            onChange={(e) =>
              update("tracking", {
                ...config.tracking,
                idle_threshold_min: Number(e.target.value),
              })
            }
            className="w-24 rounded bg-slate-900/60 px-2 py-1 text-sm"
          />
        </Field>
        <Field
          label="Tag-Block-Granularität (Minuten)"
          help="Slot-Raster für Tag-Blöcke und Personio-Sync, verankert an lokaler Mitternacht. Start wird abgerundet, Ende aufgerundet — eine angefangene Periode zählt als voller Slot. 0 = aus, typischer Wert 15. Greift ab dem nächsten Block-Boundary."
        >
          <input
            type="number"
            min={0}
            max={60}
            value={config.tracking.tag_block_granularity_min}
            onChange={(e) =>
              update("tracking", {
                ...config.tracking,
                tag_block_granularity_min: Number(e.target.value),
              })
            }
            className="w-24 rounded bg-slate-900/60 px-2 py-1 text-sm"
          />
        </Field>
      </section>

      {/* UI section ----------------------------------------------------- */}
      <section className="space-y-3 rounded bg-surface p-4">
        <h3 className="text-sm font-semibold text-slate-200">Oberfläche</h3>
        <label className="flex items-center gap-3 text-sm text-slate-300">
          <input
            type="checkbox"
            checked={config.ui.autostart}
            onChange={(e) =>
              update("ui", { ...config.ui, autostart: e.target.checked })
            }
            className="h-4 w-4"
          />
          Mit Windows starten (Autostart)
        </label>
      </section>

      {/* Personio section ----------------------------------------------- */}
      <section className="space-y-3 rounded bg-surface p-4">
        <h3 className="text-sm font-semibold text-slate-200">Personio</h3>
        <Field
          label="Tenant (Subdomain-Slug)"
          help='Nur den Slug eingeben — z. B. "example" für https://example.app.personio.com. Volle URLs werden beim Speichern automatisch auf den Slug gekürzt.'
        >
          <input
            type="text"
            value={config.personio.tenant}
            onChange={(e) =>
              update("personio", { ...config.personio, tenant: e.target.value })
            }
            placeholder="z. B. example"
            className="w-64 rounded bg-slate-900/60 px-2 py-1 text-sm"
          />
        </Field>

        <div className="rounded bg-slate-900/40 px-3 py-2 text-xs text-slate-400">
          {status?.has_session ? (
            <>
              Eingeloggt bei{" "}
              <span className="font-mono text-slate-200">
                {status.tenant}.personio.de
              </span>
              {status.employee_id > 0 && (
                <>
                  {" "}
                  als Mitarbeiter-ID{" "}
                  <span className="font-mono text-slate-200">
                    {status.employee_id}
                  </span>
                </>
              )}
              {status.captured_at && (
                <>
                  {" "}
                  · Sitzung erfasst am{" "}
                  {new Date(status.captured_at).toLocaleString()}
                </>
              )}
            </>
          ) : (
            <>Keine aktive Personio-Session. Bitte anmelden.</>
          )}
        </div>

        <div className="flex flex-wrap gap-2">
          <button
            onClick={login}
            disabled={loggingIn || !config.personio.tenant}
            className="rounded bg-accent px-3 py-1 text-sm text-white disabled:opacity-50"
          >
            {loggingIn
              ? "Browser geöffnet — bitte einloggen…"
              : status?.has_session
                ? "Erneut anmelden"
                : "Bei Personio anmelden"}
          </button>
          {status?.has_session && (
            <button
              onClick={logout}
              className="rounded bg-slate-700 px-3 py-1 text-sm hover:bg-slate-600"
            >
              Session löschen
            </button>
          )}
        </div>
        <p className="text-[11px] text-slate-500">
          Beim Anmelden öffnet sich ein eigener Chrome-Browser auf der
          Personio-Login-Seite. Sobald die Anmeldung (inkl. ggf. 2FA)
          abgeschlossen ist, werden die Session-Cookies erfasst und das Fenster
          wieder geschlossen.
        </p>
      </section>

      <div className="flex justify-end">
        <button
          onClick={save}
          disabled={saving}
          className="rounded bg-accent px-4 py-2 text-sm text-white disabled:opacity-50"
        >
          {saving ? "Speichere…" : "Einstellungen speichern"}
        </button>
      </div>
    </div>
  );
}

function Field({
  label,
  help,
  children,
}: {
  label: string;
  help?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex flex-col gap-1">
      <label className="text-xs text-slate-300">{label}</label>
      {children}
      {help && <span className="text-[11px] text-slate-500">{help}</span>}
    </div>
  );
}
