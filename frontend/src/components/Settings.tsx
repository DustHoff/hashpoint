import { useEffect, useState } from "react";
import { api } from "../api";
import { log } from "../lib/log";
import type {
  AppConfig,
  EntraStatus,
  PersonioStatus,
  WorkDay,
} from "../types";

// Weekday rows for the work-schedule checkbox row. Key is the canonical
// English short name that round-trips through TOML; label is the German
// abbreviation surfaced to the user.
const WORK_DAYS: ReadonlyArray<{ key: WorkDay; label: string }> = [
  { key: "Mon", label: "Mo" },
  { key: "Tue", label: "Di" },
  { key: "Wed", label: "Mi" },
  { key: "Thu", label: "Do" },
  { key: "Fri", label: "Fr" },
  { key: "Sat", label: "Sa" },
  { key: "Sun", label: "So" },
];

const emptyConfig: AppConfig = {
  tracking: {
    poll_interval_sec: 2,
    idle_threshold_min: 5,
    enabled: true,
    tag_block_granularity_min: 0,
  },
  personio: { tenant: "" },
  entra: { client_id: "", tenant_id: "" },
  quick_tag: { enabled: true, hotkey: "Ctrl+Alt+T" },
  communication: { process_names: ["teams.exe"], title_exclude_phrases: [] },
  work_schedule: {
    start_hour: 8,
    end_hour: 18,
    work_days: ["Mon", "Tue", "Wed", "Thu", "Fri"],
  },
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
    entra: {
      client_id: c?.entra?.client_id ?? "",
      tenant_id: c?.entra?.tenant_id ?? "",
    },
    quick_tag: {
      enabled: c?.quick_tag?.enabled ?? emptyConfig.quick_tag.enabled,
      hotkey: c?.quick_tag?.hotkey ?? emptyConfig.quick_tag.hotkey,
    },
    communication: {
      process_names:
        c?.communication?.process_names ??
        emptyConfig.communication.process_names,
      title_exclude_phrases:
        c?.communication?.title_exclude_phrases ??
        emptyConfig.communication.title_exclude_phrases,
    },
    work_schedule: {
      start_hour:
        c?.work_schedule?.start_hour ?? emptyConfig.work_schedule.start_hour,
      end_hour:
        c?.work_schedule?.end_hour ?? emptyConfig.work_schedule.end_hour,
      work_days:
        c?.work_schedule?.work_days ?? emptyConfig.work_schedule.work_days,
    },
  };
}

export default function Settings() {
  const [config, setConfig] = useState<AppConfig>(emptyConfig);
  const [status, setStatus] = useState<PersonioStatus | null>(null);
  const [entraStatus, setEntraStatus] = useState<EntraStatus | null>(null);
  const [saving, setSaving] = useState(false);
  const [loggingIn, setLoggingIn] = useState(false);
  const [entraLoggingIn, setEntraLoggingIn] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function refresh() {
    try {
      const [c, s, es] = await Promise.all([
        api.getConfig(),
        api.personioStatus(),
        api.entraStatus(),
      ]);
      log.debug("settings: loaded", { config: c, status: s, entra: es });
      setConfig(normalize(c as Partial<AppConfig>));
      setStatus(s);
      setEntraStatus(es);
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

  async function entraLogin() {
    setEntraLoggingIn(true);
    setError(null);
    setMessage(null);
    try {
      await api.entraLogin();
      setMessage("Entra ID-Anmeldung erfolgreich.");
      await refresh();
    } catch (e) {
      setError(String(e));
    } finally {
      setEntraLoggingIn(false);
    }
  }

  async function entraLogout() {
    setError(null);
    setMessage(null);
    try {
      await api.entraLogout();
      setMessage("Entra ID-Session entfernt.");
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

      {/* Work-schedule section ------------------------------------------ */}
      <section className="space-y-3 rounded bg-surface p-4">
        <h3 className="text-sm font-semibold text-slate-200">Arbeitszeit</h3>
        <p className="text-[11px] text-slate-500">
          Nominale tägliche Arbeitszeit und Arbeitstage. Wird aktuell zur
          Hervorhebung im Monatskalender genutzt (Nicht-Arbeitstage werden
          gedämpft dargestellt). Die Werte beeinflussen die Erfassung selbst
          nicht — die läuft weiterhin durchgehend, solange sie aktiv ist.
        </p>
        <div className="flex flex-wrap items-end gap-4">
          <Field
            label="Arbeitsbeginn"
            help="Volle Stunde (0–23), inklusiv."
          >
            <select
              value={config.work_schedule.start_hour}
              onChange={(e) =>
                update("work_schedule", {
                  ...config.work_schedule,
                  start_hour: Number(e.target.value),
                })
              }
              className="w-24 rounded bg-slate-900/60 px-2 py-1 text-sm"
            >
              {Array.from({ length: 24 }, (_, h) => (
                <option key={h} value={h}>
                  {String(h).padStart(2, "0")}:00
                </option>
              ))}
            </select>
          </Field>
          <Field
            label="Arbeitsende"
            help="Volle Stunde (1–24), exklusiv — d. h. 18:00 = bis 17:59."
          >
            <select
              value={config.work_schedule.end_hour}
              onChange={(e) =>
                update("work_schedule", {
                  ...config.work_schedule,
                  end_hour: Number(e.target.value),
                })
              }
              className="w-24 rounded bg-slate-900/60 px-2 py-1 text-sm"
            >
              {Array.from({ length: 24 }, (_, i) => {
                const h = i + 1;
                return (
                  <option key={h} value={h}>
                    {h === 24 ? "24:00" : `${String(h).padStart(2, "0")}:00`}
                  </option>
                );
              })}
            </select>
          </Field>
        </div>
        <Field
          label="Arbeitstage"
          help="Aktive Tage werden im Kalender normal dargestellt, inaktive gedämpft."
        >
          <div className="flex flex-wrap gap-1">
            {WORK_DAYS.map(({ key, label }) => {
              const checked = config.work_schedule.work_days.includes(key);
              return (
                <label
                  key={key}
                  className={`flex cursor-pointer items-center gap-1.5 rounded px-2 py-1 text-xs ${
                    checked
                      ? "bg-accent/80 text-white"
                      : "bg-slate-900/60 text-slate-400"
                  }`}
                >
                  <input
                    type="checkbox"
                    checked={checked}
                    onChange={(e) => {
                      const next = e.target.checked
                        ? [...config.work_schedule.work_days, key]
                        : config.work_schedule.work_days.filter(
                            (d) => d !== key,
                          );
                      update("work_schedule", {
                        ...config.work_schedule,
                        // Re-sort to canonical Mo→So order so the on-disk
                        // TOML stays stable across saves regardless of
                        // click sequence.
                        work_days: WORK_DAYS.map((w) => w.key).filter((k) =>
                          next.includes(k),
                        ),
                      });
                    }}
                    className="h-3.5 w-3.5"
                  />
                  <span>{label}</span>
                </label>
              );
            })}
          </div>
        </Field>
      </section>

      {/* Quick-Tag section ---------------------------------------------- */}
      <section className="space-y-3 rounded bg-surface p-4">
        <h3 className="text-sm font-semibold text-slate-200">Quick-Tag-Picker</h3>
        <label className="flex items-start gap-3 text-sm text-slate-300">
          <input
            type="checkbox"
            checked={config.quick_tag.enabled}
            onChange={(e) =>
              update("quick_tag", {
                ...config.quick_tag,
                enabled: e.target.checked,
              })
            }
            className="mt-0.5 h-4 w-4"
          />
          <span className="flex flex-col">
            <span>Globalen Hotkey für den Quick-Tag-Picker registrieren</span>
            <span className="text-[11px] text-slate-500">
              Öffnet beim Drücken eine kleine Auswahl unten rechts auf dem
              aktuellen Monitor mit den 10 zuletzt verwendeten Tags
              (nummeriert 0–9, Auswahl per Zifferntaste oder Mausklick).
            </span>
          </span>
        </label>
        <Field
          label="Hotkey"
          help={
            'Format: "<Mod>+<Mod>+<Taste>", z. B. "Ctrl+Alt+T", "Win+Y" oder "Shift+Ctrl+5". ' +
            "Modifier: Ctrl, Alt, Shift, Win. Tasten: A–Z, 0–9, F1–F24. Mindestens ein Modifier ist Pflicht. " +
            "Hinweis: Win+T ist von Windows belegt (Taskleisten-Fokus) und sollte vermieden werden."
          }
        >
          <input
            type="text"
            value={config.quick_tag.hotkey}
            onChange={(e) =>
              update("quick_tag", {
                ...config.quick_tag,
                hotkey: e.target.value,
              })
            }
            disabled={!config.quick_tag.enabled}
            className="w-48 rounded bg-slate-900/60 px-2 py-1 font-mono text-sm disabled:opacity-50"
            placeholder="Ctrl+Alt+T"
          />
        </Field>
      </section>

      {/* Communication section ------------------------------------------ */}
      <section className="space-y-3 rounded bg-surface p-4">
        <h3 className="text-sm font-semibold text-slate-200">
          Kommunikations-Prozesse
        </h3>
        <p className="text-[11px] text-slate-500">
          Sobald eines der hier gelisteten Programme ein sichtbares Fenster
          (Meeting, Anruf, Bildschirmfreigabe) hat, wird parallel zur normalen
          Fokus-Erfassung ein <span className="text-slate-300">📞 Kommunikations-Track</span>
          {" "}geführt — auch wenn das Fenster gerade nicht im Fokus ist. Trifft
          eine Auto-Tag-Regel auf das Kommunikations-Fenster zu, übersteuert
          deren Tag-Block jeden anderen Auto-Tag im selben Zeitraum.
          Vergleich erfolgt case-insensitive auf den Datei-Namen ohne Pfad.
        </p>
        <ul className="space-y-2">
          {config.communication.process_names.map((name, idx) => (
            <li key={idx} className="flex items-center gap-2">
              <input
                type="text"
                value={name}
                onChange={(e) => {
                  const next = [...config.communication.process_names];
                  next[idx] = e.target.value;
                  update("communication", {
                    ...config.communication,
                    process_names: next,
                  });
                }}
                placeholder="z. B. teams.exe"
                className="w-64 rounded bg-slate-900/60 px-2 py-1 font-mono text-sm"
              />
              <button
                onClick={() => {
                  const next = config.communication.process_names.filter(
                    (_, i) => i !== idx,
                  );
                  update("communication", {
                    ...config.communication,
                    process_names: next,
                  });
                }}
                className="rounded bg-slate-700 px-2 py-1 text-xs hover:bg-slate-600"
              >
                Entfernen
              </button>
            </li>
          ))}
        </ul>
        <button
          onClick={() =>
            update("communication", {
              ...config.communication,
              process_names: [...config.communication.process_names, ""],
            })
          }
          className="rounded bg-accent/80 px-3 py-1 text-xs text-white hover:bg-accent"
        >
          + Prozess hinzufügen
        </button>

        <div className="mt-4 border-t border-slate-700/50 pt-4">
          <h4 className="text-xs font-semibold uppercase tracking-wide text-slate-400">
            Ausschluss-Phrasen (Fenstertitel)
          </h4>
          <p className="mt-1 text-[11px] text-slate-500">
            Enthält der Fenstertitel eines oben gelisteten Programms eine
            dieser Phrasen (case-insensitive Substring-Vergleich), wird das
            Fenster <span className="text-slate-300">nicht</span> als
            Kommunikations-Fenster behandelt — kein Comm-Track, kein
            Comm-Auto-Tag-Override. Beispiel: <code className="font-mono">Benachrichtigung</code>,
            {" "}<code className="font-mono">Reminder</code>. Leer = keine Ausschlüsse.
          </p>
          <ul className="mt-2 space-y-2">
            {config.communication.title_exclude_phrases.map((phrase, idx) => (
              <li key={idx} className="flex items-center gap-2">
                <input
                  type="text"
                  value={phrase}
                  onChange={(e) => {
                    const next = [
                      ...config.communication.title_exclude_phrases,
                    ];
                    next[idx] = e.target.value;
                    update("communication", {
                      ...config.communication,
                      title_exclude_phrases: next,
                    });
                  }}
                  placeholder="z. B. Benachrichtigung"
                  className="w-64 rounded bg-slate-900/60 px-2 py-1 font-mono text-sm"
                />
                <button
                  onClick={() => {
                    const next =
                      config.communication.title_exclude_phrases.filter(
                        (_, i) => i !== idx,
                      );
                    update("communication", {
                      ...config.communication,
                      title_exclude_phrases: next,
                    });
                  }}
                  className="rounded bg-slate-700 px-2 py-1 text-xs hover:bg-slate-600"
                >
                  Entfernen
                </button>
              </li>
            ))}
          </ul>
          <button
            onClick={() =>
              update("communication", {
                ...config.communication,
                title_exclude_phrases: [
                  ...config.communication.title_exclude_phrases,
                  "",
                ],
              })
            }
            className="mt-2 rounded bg-accent/80 px-3 py-1 text-xs text-white hover:bg-accent"
          >
            + Phrase hinzufügen
          </button>
        </div>
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

      {/* Entra ID section ----------------------------------------------- */}
      <section className="space-y-3 rounded bg-surface p-4">
        <h3 className="text-sm font-semibold text-slate-200">
          Microsoft Entra ID
        </h3>
        <p className="text-[11px] text-slate-500">
          Optionale Anmeldung gegen einen Entra-ID-Tenant für Microsoft Graph
          (SharePoint, Kalender) und Entra-geschützte Drittanwendungen. Die
          App-Registrierung muss als „Mobile and desktop applications“ mit
          Loopback-Redirect <code className="font-mono">http://localhost</code> und
          aktivierten Public Client Flows angelegt sein.
        </p>
        <Field
          label="Client ID"
          help='Application (client) ID GUID aus der Entra-ID-App-Registrierung. Format: 8-4-4-4-12 hex, z. B. "11111111-2222-3333-4444-555555555555".'
        >
          <input
            type="text"
            value={config.entra.client_id}
            onChange={(e) =>
              update("entra", { ...config.entra, client_id: e.target.value })
            }
            placeholder="00000000-0000-0000-0000-000000000000"
            className="w-96 rounded bg-slate-900/60 px-2 py-1 font-mono text-sm"
          />
        </Field>
        <Field
          label="Tenant ID"
          help="Directory (tenant) ID GUID. „common“ / „organizations“ werden bewusst abgelehnt — die App ist Single-Tenant."
        >
          <input
            type="text"
            value={config.entra.tenant_id}
            onChange={(e) =>
              update("entra", { ...config.entra, tenant_id: e.target.value })
            }
            placeholder="00000000-0000-0000-0000-000000000000"
            className="w-96 rounded bg-slate-900/60 px-2 py-1 font-mono text-sm"
          />
        </Field>

        <div className="rounded bg-slate-900/40 px-3 py-2 text-xs text-slate-400">
          {!entraStatus?.configured ? (
            <>
              Feature inaktiv —{" "}
              {entraStatus?.reason || "Client-ID und Tenant-ID eintragen und speichern."}
            </>
          ) : entraStatus.has_account ? (
            <>
              Eingeloggt
              {entraStatus.username && (
                <>
                  {" "}als{" "}
                  <span className="font-mono text-slate-200">
                    {entraStatus.username}
                  </span>
                </>
              )}
              {entraStatus.tenant_id && (
                <>
                  {" "}im Tenant{" "}
                  <span className="font-mono text-slate-200">
                    {entraStatus.tenant_id}
                  </span>
                </>
              )}
              .
            </>
          ) : (
            <>Konfiguration vorhanden, aber noch nicht angemeldet.</>
          )}
        </div>

        <div className="flex flex-wrap gap-2">
          <button
            onClick={entraLogin}
            disabled={
              entraLoggingIn ||
              !entraStatus?.configured ||
              !config.entra.client_id ||
              !config.entra.tenant_id
            }
            className="rounded bg-accent px-3 py-1 text-sm text-white disabled:opacity-50"
          >
            {entraLoggingIn
              ? "Browser geöffnet — bitte einloggen…"
              : entraStatus?.has_account
                ? "Erneut anmelden"
                : "Bei Entra ID anmelden"}
          </button>
          {entraStatus?.has_account && (
            <button
              onClick={entraLogout}
              className="rounded bg-slate-700 px-3 py-1 text-sm hover:bg-slate-600"
            >
              Abmelden
            </button>
          )}
        </div>
        <p className="text-[11px] text-slate-500">
          Beim ersten Login öffnet sich der Standardbrowser auf der
          Microsoft-Login-Seite. Auf Entra-ID-joined Geräten greift das
          PRT-SSO und der Flow läuft promptlos durch. Folgestarts holen das
          Token still aus dem DPAPI-verschlüsselten lokalen Cache.
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
