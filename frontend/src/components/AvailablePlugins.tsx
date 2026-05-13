import { useCallback, useEffect, useState } from "react";
import { api } from "../api";
import type { AvailablePluginEntry } from "../types";

// rowState classifies a catalog entry against the installed-version we
// know about. The buttons + badges below key off this single discriminator
// so the rendering stays declarative.
type RowState = "not_installed" | "up_to_date" | "update_available";

function classify(entry: AvailablePluginEntry): RowState {
  if (entry.installed_version === "") return "not_installed";
  if (entry.installed_version === entry.version) return "up_to_date";
  return "update_available";
}

export default function AvailablePlugins() {
  const [entries, setEntries] = useState<AvailablePluginEntry[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // busyKey is the catalog row whose action button is currently
  // running, keyed as `${source_plugin}/${name}`. Only one action runs
  // at a time so the UI can prove progress without juggling per-button
  // spinners.
  const [busyKey, setBusyKey] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      const list = (await api.pluginListAvailable()) ?? [];
      setEntries(list);
      setError(null);
      setLoaded(true);
    } catch (e) {
      setError(String(e));
    }
  }, []);

  useEffect(() => {
    void refresh();
    // Discovery events refresh both the catalog (a new source might
    // have appeared) and any installed-version annotations. We
    // intentionally use the same refresh path for both.
    const off = api.onEvent("plugins:discovered", () => {
      void refresh();
    });
    return off;
  }, [refresh]);

  async function runAction(
    entry: AvailablePluginEntry,
    action: "install" | "update" | "uninstall",
  ) {
    const key = `${entry.source_plugin}/${entry.name}`;
    setBusyKey(key);
    setError(null);
    try {
      if (action === "install") {
        await api.pluginInstall(entry.source_plugin, entry.name);
      } else if (action === "update") {
        await api.pluginUpdate(entry.source_plugin, entry.name);
      } else {
        await api.pluginUninstall(entry.source_plugin, entry.name);
      }
      await refresh();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusyKey(null);
    }
  }

  if (!loaded) {
    return (
      <p className="text-sm text-slate-400">Lade verfügbare Plugins…</p>
    );
  }

  return (
    <div className="flex h-full flex-col gap-4">
      <header className="flex items-baseline justify-between gap-3">
        <div>
          <h2 className="text-lg font-semibold text-slate-100">
            Verfügbare Plugins
          </h2>
          <p className="mt-1 text-sm text-slate-400">
            Pluginquellen liefern hier ihre Kataloge. Installation, Update
            und Deinstallation werden durch die jeweilige Quelle ausgeführt.
          </p>
        </div>
        <button
          onClick={() => void refresh()}
          className="text-xs text-slate-400 hover:text-slate-200"
          title="Katalog neu laden"
        >
          ↻ Neu laden
        </button>
      </header>

      {error && (
        <div className="rounded border border-rose-700 bg-rose-900/30 p-2 text-sm text-rose-200">
          {error}
        </div>
      )}

      {entries.length === 0 ? (
        <div className="rounded bg-surface p-4 text-sm text-slate-400">
          Keine Pluginquellen aktiv. Installiere ein Plugin mit der
          Capability <code className="text-slate-300">plugin_management</code>{" "}
          und konfiguriere es im Tab <strong>Plugins</strong>, damit hier
          ein Katalog erscheint.
        </div>
      ) : (
        <ul className="divide-y divide-slate-800 overflow-hidden rounded bg-surface">
          {entries.map((entry) => (
            <CatalogRow
              key={`${entry.source_plugin}/${entry.name}`}
              entry={entry}
              busy={busyKey === `${entry.source_plugin}/${entry.name}`}
              disabled={busyKey !== null}
              onAction={(action) => void runAction(entry, action)}
            />
          ))}
        </ul>
      )}
    </div>
  );
}

interface CatalogRowProps {
  entry: AvailablePluginEntry;
  busy: boolean;
  disabled: boolean;
  onAction: (action: "install" | "update" | "uninstall") => void;
}

function CatalogRow({ entry, busy, disabled, onAction }: CatalogRowProps) {
  const state = classify(entry);
  const isSelfSource = entry.name === entry.source_plugin;

  return (
    <li className="flex items-center justify-between gap-4 px-3 py-3">
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate font-medium text-slate-100">
            {entry.name}
          </span>
          <span className="shrink-0 text-xs text-slate-400">
            v{entry.version || "?"}
          </span>
          {state === "up_to_date" && (
            <span className="shrink-0 rounded bg-emerald-700/30 px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-emerald-200 border border-emerald-700/50">
              installiert
            </span>
          )}
          {state === "update_available" && (
            <span className="shrink-0 rounded bg-amber-700/30 px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-amber-200 border border-amber-700/50">
              Update verfügbar (lokal v{entry.installed_version})
            </span>
          )}
        </div>
        {entry.description && (
          <p className="mt-1 text-sm text-slate-400">{entry.description}</p>
        )}
        <p className="mt-1 text-[11px] uppercase tracking-wide text-slate-500">
          Quelle: <span className="text-slate-400">{entry.source_plugin}</span>
        </p>
      </div>

      <div className="flex shrink-0 items-center gap-2">
        {state === "not_installed" && (
          <button
            onClick={() => onAction("install")}
            disabled={disabled}
            className="rounded bg-emerald-700 px-3 py-1.5 text-sm font-medium text-white hover:bg-emerald-600 disabled:bg-slate-700 disabled:text-slate-400"
          >
            {busy ? "Installiere…" : "Installieren"}
          </button>
        )}
        {state === "update_available" && (
          <button
            onClick={() => onAction("update")}
            disabled={disabled}
            className="rounded bg-amber-700 px-3 py-1.5 text-sm font-medium text-white hover:bg-amber-600 disabled:bg-slate-700 disabled:text-slate-400"
          >
            {busy ? "Aktualisiere…" : "Aktualisieren"}
          </button>
        )}
        {state === "up_to_date" && (
          <button
            disabled
            className="rounded bg-slate-700 px-3 py-1.5 text-sm font-medium text-slate-400"
            title="Bereits auf dem neusten Stand"
          >
            Aktualisieren
          </button>
        )}
        {entry.installed_version !== "" && (
          <button
            onClick={() => onAction("uninstall")}
            disabled={disabled || isSelfSource}
            title={
              isSelfSource
                ? "Eine Quelle kann sich nicht selbst entfernen"
                : undefined
            }
            className="rounded border border-rose-700/50 px-3 py-1.5 text-sm text-rose-300 hover:bg-rose-900/30 disabled:opacity-50"
          >
            {busy ? "Entferne…" : "Entfernen"}
          </button>
        )}
      </div>
    </li>
  );
}
