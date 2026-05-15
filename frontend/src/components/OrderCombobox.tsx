import { useEffect, useRef, useState } from "react";
import { api } from "../api";
import type { PluginOrderGroup } from "../types";

interface Props {
  value: string;
  onChange: (value: string) => void;
  // When true, the dropdown auto-fetches plugin groups on mount and on
  // every focus. The Tag-Manager passes true on open; tests and detail
  // forms can disable the live pull.
  autoLoad?: boolean;
  placeholder?: string;
}

// OrderCombobox is the per-tag Auftrag picker rendered under the
// Personio fields in the Tag-Manager aside. The input is the source of
// truth — typing edits the value directly, the dropdown is just a
// shortcut to copy a plugin-supplied name verbatim. Orders are live-
// pulled (no DB cache) so a freshly-configured tag_provider plugin
// shows up on the next focus.
//
// Layout: input + dropdown that lists plugin groups (optgroup-style
// header rows, non-clickable). Only groups whose name contains the
// current query (case-insensitive) are rendered. Empty query ⇒ all
// groups visible.
export default function OrderCombobox({
  value,
  onChange,
  autoLoad = true,
  placeholder,
}: Props) {
  const [groups, setGroups] = useState<PluginOrderGroup[]>([]);
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const containerRef = useRef<HTMLDivElement | null>(null);

  async function refresh() {
    setLoading(true);
    try {
      const got = await api.listPluginOrders();
      setGroups(got ?? []);
    } catch {
      // Plugin errors are best-effort — the user can still type freitext.
      setGroups([]);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    if (autoLoad) refresh();
  }, [autoLoad]);

  // Close the dropdown when the user clicks outside the combobox.
  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (!containerRef.current) return;
      if (!containerRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    window.addEventListener("mousedown", handler);
    return () => window.removeEventListener("mousedown", handler);
  }, [open]);

  const query = value.trim().toLowerCase();
  const visibleGroups = groups
    .map((g) => ({
      plugin_name: g.plugin_name,
      orders: (g.orders ?? []).filter(
        (o) => query === "" || o.Name.toLowerCase().includes(query),
      ),
    }))
    .filter((g) => g.orders.length > 0);
  const totalVisible = visibleGroups.reduce((n, g) => n + g.orders.length, 0);

  function handleFocus() {
    setOpen(true);
    if (autoLoad && groups.length === 0 && !loading) refresh();
  }

  function pick(name: string) {
    onChange(name);
    setOpen(false);
  }

  return (
    <div ref={containerRef} className="relative">
      <input
        value={value}
        onChange={(e) => {
          onChange(e.target.value);
          setOpen(true);
        }}
        onFocus={handleFocus}
        placeholder={placeholder ?? "Auftrag wählen oder eintippen"}
        className="mt-1 w-full rounded bg-bg px-2 py-1 text-sm"
        autoComplete="off"
      />
      {open && (
        <div className="absolute z-20 mt-1 max-h-60 w-full overflow-y-auto rounded border border-slate-700 bg-surface shadow-lg">
          {loading && (
            <div className="px-2 py-1 text-xs text-slate-400">Lade Aufträge…</div>
          )}
          {!loading && totalVisible === 0 && (
            <div className="px-2 py-1 text-xs text-slate-400">
              {groups.length === 0
                ? "Kein tag_provider-Plugin liefert Aufträge."
                : "Keine Treffer — als Freitext speichern."}
            </div>
          )}
          {!loading &&
            visibleGroups.map((g) => (
              <div key={g.plugin_name} className="py-1">
                <div className="px-2 text-xs font-semibold uppercase tracking-wide text-slate-500">
                  {g.plugin_name}
                </div>
                <ul>
                  {g.orders.map((o) => (
                    <li key={`${g.plugin_name}:${o.ID}`}>
                      <button
                        type="button"
                        onClick={() => pick(o.Name)}
                        className="block w-full px-2 py-1 text-left text-sm hover:bg-slate-700"
                      >
                        <span className="text-slate-100">{o.Name}</span>
                        {o.Description && (
                          <span className="ml-2 text-xs text-slate-400">
                            {o.Description}
                          </span>
                        )}
                      </button>
                    </li>
                  ))}
                </ul>
              </div>
            ))}
        </div>
      )}
    </div>
  );
}
