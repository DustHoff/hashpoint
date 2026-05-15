import { useEffect, useMemo, useState } from "react";
import { api } from "../api";
import type {
  ManifestField,
  PluginConfigView,
  PluginInfo,
  PluginState,
} from "../types";

// stateBadgeStyles maps backend PluginState values to a Tailwind class
// trio used for the per-row pill. Keeping the mapping centralised here
// means a new state (e.g. "upgrading") only needs a row added without
// touching markup further down.
const stateBadgeStyles: Record<PluginState, string> = {
  running: "bg-emerald-700/30 text-emerald-200 border border-emerald-700/50",
  needs_config: "bg-amber-700/30 text-amber-200 border border-amber-700/50",
  failed: "bg-rose-700/30 text-rose-200 border border-rose-700/50",
  disabled: "bg-slate-700/40 text-slate-300 border border-slate-700/50",
};

const stateLabels: Record<PluginState, string> = {
  running: "Aktiv",
  needs_config: "Konfiguration fehlt",
  failed: "Fehler",
  disabled: "Deaktiviert",
};

// Pseudo-secret value the password input renders while the user hasn't
// touched the field. Submitting it unchanged is interpreted as "leave
// the stored secret alone" (PluginSetSecret skips empty values).
const SECRET_PLACEHOLDER = "";

export default function Plugins() {
  const [plugins, setPlugins] = useState<PluginInfo[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function refresh() {
    try {
      const list = (await api.pluginList()) ?? [];
      setPlugins(list);
      setError(null);
      // Auto-select the first plugin on initial load so the right pane
      // is never blank when at least one plugin is installed.
      if (list.length > 0 && selected === null) {
        setSelected(list[0].name);
      }
    } catch (e) {
      setError(String(e));
    }
  }

  useEffect(() => {
    refresh();
    // The host emits plugins:discovered when its periodic scan picks
    // up a new plugin directory. We refresh the installed list so the
    // newcomer shows up without forcing the user to press ↻.
    const offDiscovered = api.onEvent("plugins:discovered", () => {
      void refresh();
    });
    // plugins:state-changed fires when the host's exit watcher demotes
    // a previously running plugin (subprocess crash). Refresh so the
    // badge flips to "Fehler" in real time.
    const offStateChanged = api.onEvent("plugins:state-changed", () => {
      void refresh();
    });
    return () => {
      offDiscovered();
      offStateChanged();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const current = useMemo(
    () => plugins.find((p) => p.name === selected) ?? null,
    [plugins, selected],
  );

  async function toggleEnabled(name: string, enabled: boolean) {
    try {
      await api.pluginSetEnabled(name, enabled);
      await refresh();
    } catch (e) {
      setError(String(e));
    }
  }

  return (
    <div className="grid h-full gap-6 lg:grid-cols-[320px,1fr]">
      <aside className="flex flex-col">
        <div className="mb-3 flex items-baseline justify-between">
          <h2 className="font-semibold">Installierte Plugins</h2>
          <button
            onClick={refresh}
            className="text-xs text-slate-400 hover:text-slate-200"
            title="Liste neu laden"
          >
            ↻
          </button>
        </div>
        {plugins.length === 0 ? (
          <div className="rounded bg-surface p-4 text-sm text-slate-400">
            Keine Plugins gefunden. Lege ein Verzeichnis unter{" "}
            <code className="text-slate-300">PluginsDir</code> mit einer
            <code className="text-slate-300"> manifest.toml</code> und der
            Plugin-Binary an.
          </div>
        ) : (
          <ul className="divide-y divide-slate-800 overflow-hidden rounded bg-surface">
            {plugins.map((p) => {
              const isSelected = p.name === selected;
              return (
                <li key={p.name}>
                  <button
                    onClick={() => setSelected(p.name)}
                    className={`flex w-full flex-col gap-1 px-3 py-2 text-left transition ${
                      isSelected
                        ? "bg-slate-800/60"
                        : "hover:bg-slate-800/30"
                    }`}
                  >
                    <div className="flex items-center justify-between gap-2">
                      <span className="truncate font-medium text-slate-100">
                        {p.name}
                      </span>
                      <span className="shrink-0 text-xs text-slate-400">
                        v{p.version || "?"}
                      </span>
                    </div>
                    <div className="flex items-center justify-between gap-2">
                      <span
                        className={`rounded px-1.5 py-0.5 text-[10px] uppercase tracking-wide ${stateBadgeStyles[p.state]}`}
                      >
                        {stateLabels[p.state]}
                      </span>
                      <ToggleSwitch
                        checked={p.enabled}
                        onChange={(next) => {
                          // The button itself sits inside the list-row
                          // button — clicking it would also bubble up
                          // and re-select the row. Stop the event so
                          // the toggle and the select are independent.
                          void toggleEnabled(p.name, next);
                        }}
                      />
                    </div>
                  </button>
                </li>
              );
            })}
          </ul>
        )}
        {error && (
          <div className="mt-3 rounded border border-rose-700 bg-rose-900/30 p-2 text-sm text-rose-200">
            {error}
          </div>
        )}
      </aside>

      <section className="rounded bg-surface p-4">
        {current === null ? (
          <p className="text-sm text-slate-400">
            Wähle ein Plugin aus der Liste, um die Konfiguration anzuzeigen.
          </p>
        ) : (
          <PluginDetail
            plugin={current}
            onSaved={refresh}
            onError={setError}
          />
        )}
      </section>
    </div>
  );
}

// ToggleSwitch is a minimal CSS-only switch. Kept inline because the
// rest of the app doesn't need a generic one (yet) — moving it to a
// shared file the moment a second consumer shows up.
function ToggleSwitch({
  checked,
  onChange,
}: {
  checked: boolean;
  onChange: (next: boolean) => void;
}) {
  return (
    <span
      role="switch"
      aria-checked={checked}
      tabIndex={0}
      onClick={(e) => {
        e.stopPropagation();
        onChange(!checked);
      }}
      onKeyDown={(e) => {
        if (e.key === " " || e.key === "Enter") {
          e.preventDefault();
          e.stopPropagation();
          onChange(!checked);
        }
      }}
      className={`relative inline-flex h-4 w-7 shrink-0 cursor-pointer items-center rounded-full transition ${
        checked ? "bg-emerald-600" : "bg-slate-600"
      }`}
    >
      <span
        className={`inline-block h-3 w-3 transform rounded-full bg-white transition ${
          checked ? "translate-x-3.5" : "translate-x-0.5"
        }`}
      />
    </span>
  );
}

interface PluginDetailProps {
  plugin: PluginInfo;
  onSaved: () => void;
  onError: (msg: string | null) => void;
}

// PluginDetail owns the per-plugin form state. Re-mounts on plugin
// change via the `key` prop so the inputs reset cleanly when the user
// switches selection — avoids stale `dirty` flags carrying over.
function PluginDetail({ plugin, onSaved, onError }: PluginDetailProps) {
  return (
    <PluginDetailInner
      key={plugin.name}
      plugin={plugin}
      onSaved={onSaved}
      onError={onError}
    />
  );
}

function PluginDetailInner({ plugin, onSaved, onError }: PluginDetailProps) {
  const [view, setView] = useState<PluginConfigView | null>(null);
  const [draft, setDraft] = useState<Record<string, string>>({});
  // pwTouched[key] === true means the user typed something into the
  // password field, so we should submit the new value (otherwise we
  // leave the stored secret untouched).
  const [pwTouched, setPwTouched] = useState<Record<string, boolean>>({});
  const [saving, setSaving] = useState(false);
  // info is the local "success" toast for actions whose result the user
  // wants to see (e.g. "5 Tags neu importiert"). Cleared on the next
  // save / restart action; not shared with onError which uses the
  // parent's banner.
  const [info, setInfo] = useState<string | null>(null);
  const [refreshing, setRefreshing] = useState(false);

  const isTagProvider = plugin.capabilities.includes("tag_provider");

  useEffect(() => {
    let cancelled = false;
    api
      .pluginGetConfig(plugin.name)
      .then((v) => {
        if (cancelled) return;
        setView(v);
        setDraft({ ...v.fields });
        setPwTouched({});
      })
      .catch((e) => onError(String(e)));
    return () => {
      cancelled = true;
    };
  }, [plugin.name, onError]);

  // Field keys sorted alphabetically — manifest.toml's `[fields.*]`
  // tables are unordered in TOML and Go maps don't preserve order, so
  // we impose a stable display order here.
  const fieldEntries = useMemo(() => {
    const keys = Object.keys(plugin.config_schema?.fields ?? {});
    keys.sort();
    return keys.map((k) => [k, plugin.config_schema.fields[k]] as const);
  }, [plugin]);

  async function save() {
    if (view === null) return;
    setSaving(true);
    onError(null);
    try {
      const plainFields: Record<string, string> = {};
      for (const [key, field] of fieldEntries) {
        if (field.type === "password") continue;
        plainFields[key] = draft[key] ?? "";
      }
      await api.pluginSetConfig(plugin.name, plainFields);

      // Submit password updates individually — only for fields the
      // user touched, so unchanged secrets stay put.
      for (const [key, field] of fieldEntries) {
        if (field.type !== "password") continue;
        if (!pwTouched[key]) continue;
        const value = draft[key] ?? "";
        if (value === "") {
          await api.pluginDeleteSecret(plugin.name, key);
        } else {
          await api.pluginSetSecret(plugin.name, key, value);
        }
      }
      onSaved();
    } catch (e) {
      onError(String(e));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="flex h-full flex-col gap-4">
      <header className="flex items-baseline justify-between gap-3 border-b border-slate-800 pb-3">
        <div>
          <h2 className="text-lg font-semibold text-slate-100">{plugin.name}</h2>
          {plugin.description && (
            <p className="mt-1 text-sm text-slate-400">{plugin.description}</p>
          )}
        </div>
        <span className="shrink-0 text-xs text-slate-500">
          Version {plugin.version || "—"}
        </span>
      </header>

      {plugin.state === "needs_config" &&
        plugin.missing_fields &&
        plugin.missing_fields.length > 0 && (
          <div className="rounded border border-amber-700/50 bg-amber-900/20 p-3 text-sm text-amber-200">
            Fehlende Pflichtfelder:{" "}
            <span className="font-mono">{plugin.missing_fields.join(", ")}</span>
          </div>
        )}

      {plugin.state === "failed" && plugin.last_error && (
        <div className="rounded border border-rose-700/50 bg-rose-900/20 p-3 text-sm text-rose-200">
          {plugin.last_error}
        </div>
      )}

      {fieldEntries.length === 0 ? (
        <p className="text-sm text-slate-400">
          Dieses Plugin deklariert keine Konfigurationsfelder.
        </p>
      ) : (
        <div className="flex flex-col gap-3">
          {fieldEntries.map(([key, field]) => (
            <FieldRow
              key={key}
              fieldKey={key}
              field={field}
              value={draft[key] ?? ""}
              secretSaved={view?.secrets_set?.[key] === true}
              onChange={(v) => {
                setDraft((d) => ({ ...d, [key]: v }));
                if (field.type === "password") {
                  setPwTouched((t) => ({ ...t, [key]: true }));
                }
              }}
            />
          ))}
        </div>
      )}

      <div className="mt-2 flex flex-wrap items-center gap-3">
        <button
          onClick={save}
          disabled={saving || view === null}
          className="rounded bg-emerald-700 px-3 py-1.5 text-sm font-medium text-white hover:bg-emerald-600 disabled:bg-slate-700 disabled:text-slate-400"
        >
          {saving ? "Speichere…" : "Speichern"}
        </button>
        <RestartButton
          state={plugin.state}
          disabled={saving}
          onClick={() => {
            // host.Reload kills the subprocess (if running) and re-evaluates
            // the plugin from scratch — exactly what "Neu starten" means
            // both for a crashed plugin (failed → running) and an
            // already-running one (refresh of config + subprocess).
            setInfo(null);
            void api
              .pluginReload(plugin.name)
              .then(onSaved)
              .catch((e) => onError(String(e)));
          }}
        />
        {isTagProvider && (
          <button
            onClick={() => {
              setRefreshing(true);
              setInfo(null);
              onError(null);
              api
                .pluginRefreshTags(plugin.name)
                .then((created) => {
                  setInfo(
                    created === 0
                      ? "Keine neuen Tags — bestehende Tags wurden nicht überschrieben."
                      : `${created} neue Tag${created === 1 ? "" : "s"} importiert.`,
                  );
                  onSaved();
                })
                .catch((e) => onError(String(e)))
                .finally(() => setRefreshing(false));
            }}
            disabled={saving || refreshing || plugin.state !== "running"}
            className="rounded border border-slate-700 bg-slate-800 px-3 py-1.5 text-sm text-slate-200 hover:bg-slate-700 disabled:opacity-50"
            title="Pull-Import: das Plugin liefert seinen Tag-Katalog, der Host mergt neue Pfade in die Tag-Hierarchie."
          >
            {refreshing ? "Importiere…" : "Tags neu laden"}
          </button>
        )}
      </div>
      {info && (
        <div className="rounded border border-emerald-700/40 bg-emerald-900/30 px-3 py-2 text-xs text-emerald-200">
          {info}
        </div>
      )}
    </div>
  );
}

interface FieldRowProps {
  fieldKey: string;
  field: ManifestField;
  value: string;
  secretSaved: boolean;
  onChange: (v: string) => void;
}

function FieldRow({
  fieldKey,
  field,
  value,
  secretSaved,
  onChange,
}: FieldRowProps) {
  const labelText = field.label || fieldKey;
  const required = field.required ? (
    <span className="ml-1 text-rose-400" aria-label="Pflichtfeld">
      *
    </span>
  ) : null;
  return (
    <label className="flex flex-col gap-1 text-sm">
      <span className="flex items-center text-slate-300">
        {labelText}
        {required}
        <span className="ml-2 text-[10px] text-slate-500">({fieldKey})</span>
        {field.type === "password" && secretSaved && (
          <span className="ml-2 rounded bg-slate-700 px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-slate-300">
            gespeichert
          </span>
        )}
      </span>
      {field.type === "boolean" ? (
        <select
          value={value === "true" ? "true" : "false"}
          onChange={(e) => onChange(e.target.value)}
          className="w-32 rounded border border-slate-700 bg-slate-900 px-2 py-1 text-slate-100"
        >
          <option value="false">Aus</option>
          <option value="true">An</option>
        </select>
      ) : field.type === "password" ? (
        <input
          type="password"
          value={value}
          placeholder={secretSaved ? "•••••• (leer lassen, um beizubehalten)" : field.default || ""}
          onChange={(e) => onChange(e.target.value)}
          className="rounded border border-slate-700 bg-slate-900 px-2 py-1 text-slate-100"
          autoComplete="new-password"
        />
      ) : (
        <input
          type="text"
          value={value || SECRET_PLACEHOLDER}
          placeholder={field.default || ""}
          onChange={(e) => onChange(e.target.value)}
          className="rounded border border-slate-700 bg-slate-900 px-2 py-1 text-slate-100"
        />
      )}
    </label>
  );
}

// RestartButton offers "kill subprocess + relaunch" for any non-disabled
// plugin. When the plugin is in StateFailed (typically: subprocess
// crashed) the button switches to a rose tint to suggest recovery
// rather than a routine config-cycle, so a user opening the tab after
// a crash has an obvious next step.
function RestartButton({
  state,
  disabled,
  onClick,
}: {
  state: PluginState;
  disabled: boolean;
  onClick: () => void;
}) {
  const isRecovery = state === "failed";
  const classes = isRecovery
    ? "rounded bg-rose-700 px-3 py-1.5 text-sm font-medium text-white hover:bg-rose-600 disabled:bg-slate-700 disabled:text-slate-400"
    : "rounded border border-slate-600 bg-slate-800 px-3 py-1.5 text-sm font-medium text-slate-100 hover:bg-slate-700 disabled:border-slate-700 disabled:bg-slate-800 disabled:text-slate-500";
  return (
    <button onClick={onClick} disabled={disabled} className={classes}>
      Neu starten
    </button>
  );
}
