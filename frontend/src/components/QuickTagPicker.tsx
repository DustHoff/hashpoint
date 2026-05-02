import { useEffect, useRef, useState } from "react";
import { api } from "../api";
import { log } from "../lib/log";
import type { QuickTagSlot } from "../types";

interface Props {
  onClose: () => void;
}

// QuickTagPicker is the small popup the global hotkey opens. It lives in
// the same Wails window — App.tsx swaps the regular UI out while the
// picker is mounted. Selection (number key 0–9 or click) calls the
// backend's StartManualTag and dismisses; Esc dismisses without changing
// the tag.
export default function QuickTagPicker({ onClose }: Props) {
  const [slots, setSlots] = useState<QuickTagSlot[]>([]);
  const [error, setError] = useState<string | null>(null);
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const s = await api.quickTagSlots();
        if (!cancelled) setSlots(s ?? []);
      } catch (e) {
        log.error("quick-tag: load slots failed", { err: String(e) });
        if (!cancelled) setError(String(e));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  // Pull keyboard focus into the picker so 0–9 / Esc work without the user
  // first clicking inside.
  useEffect(() => {
    containerRef.current?.focus();
  }, []);

  async function pickByIndex(idx: number) {
    const slot = slots[idx];
    if (!slot) return;
    try {
      await api.quickTagSelect(slot.tag_id);
    } catch (e) {
      log.error("quick-tag: select failed", { err: String(e) });
      setError(String(e));
    }
  }

  async function dismiss() {
    try {
      await api.quickTagDismiss();
    } catch (e) {
      log.error("quick-tag: dismiss failed", { err: String(e) });
      onClose();
    }
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLDivElement>) {
    if (e.key === "Escape") {
      e.preventDefault();
      void dismiss();
      return;
    }
    if (e.key >= "0" && e.key <= "9") {
      e.preventDefault();
      void pickByIndex(Number(e.key));
    }
  }

  return (
    <div
      ref={containerRef}
      tabIndex={-1}
      onKeyDown={onKeyDown}
      className="fixed inset-0 flex flex-col bg-surface text-slate-100 outline-none"
    >
      <header className="flex items-center justify-between border-b border-slate-700 bg-slate-900/40 px-3 py-2">
        <span className="text-xs font-semibold uppercase tracking-wider text-slate-300">
          Quick Tag
        </span>
        <button
          onClick={() => void dismiss()}
          className="rounded px-2 text-xs text-slate-400 hover:bg-slate-700 hover:text-slate-100"
          title="Schließen (Esc)"
        >
          ✕
        </button>
      </header>

      {error && (
        <div className="border-b border-red-900/50 bg-red-900/40 px-3 py-1 text-xs text-red-200">
          {error}
        </div>
      )}

      <ul className="flex-1 overflow-auto py-1">
        {slots.length === 0 && !error && (
          <li className="px-3 py-4 text-center text-xs text-slate-500">
            Keine Tags vorhanden.
          </li>
        )}
        {slots.map((s) => (
          <li key={s.tag_id}>
            <button
              onClick={() => void pickByIndex(s.index)}
              className={`flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm transition-colors ${
                s.is_active
                  ? "bg-accent/20 text-white"
                  : "text-slate-200 hover:bg-slate-700"
              }`}
            >
              <kbd className="inline-flex h-5 w-5 items-center justify-center rounded bg-slate-900/70 font-mono text-[11px] text-slate-300">
                {s.index}
              </kbd>
              {s.color && (
                <span
                  className="h-2.5 w-2.5 shrink-0 rounded-full"
                  style={{ backgroundColor: s.color }}
                  aria-hidden
                />
              )}
              <span className="flex-1 truncate" title={s.label}>
                {s.label}
              </span>
              {s.is_active && (
                <span className="text-[10px] uppercase tracking-wider text-emerald-300">
                  aktiv
                </span>
              )}
            </button>
          </li>
        ))}
      </ul>

      <footer className="border-t border-slate-700 bg-slate-900/40 px-3 py-1.5 text-[10px] text-slate-400">
        <kbd className="font-mono">0</kbd>–<kbd className="font-mono">9</kbd>{" "}
        wählt · <kbd className="font-mono">Esc</kbd> schließt
      </footer>
    </div>
  );
}
