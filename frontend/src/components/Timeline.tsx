import { useEffect, useMemo, useState } from "react";
import { api } from "../api";
import type { FocusBlock, Tag } from "../types";
import {
  dateInputValue,
  formatDuration,
  formatHHMM,
  fromDateInput,
  startOfDayUTCISO,
} from "../lib/time";

export default function Timeline() {
  const [day, setDay] = useState<Date>(new Date());
  const [blocks, setBlocks] = useState<FocusBlock[]>([]);
  const [tags, setTags] = useState<Tag[]>([]);
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [paused, setPaused] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [syncing, setSyncing] = useState(false);

  const tagsByID = useMemo(() => {
    const m: Record<number, Tag> = {};
    tags.forEach((t) => (m[t.id] = t));
    return m;
  }, [tags]);

  async function refresh() {
    try {
      const [b, t, p] = await Promise.all([
        api.blocksByDay(startOfDayUTCISO(day)),
        api.listTags(),
        api.isTrackingPaused(),
      ]);
      setBlocks(b ?? []);
      setTags(t ?? []);
      setPaused(p);
      setError(null);
    } catch (e) {
      setError(String(e));
    }
  }

  useEffect(() => {
    refresh();
    const t = setInterval(refresh, 5000);
    return () => clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [day]);

  function toggleSelect(id: number, range: boolean) {
    const next = new Set(selected);
    if (range && selected.size > 0) {
      const ids = blocks.map((b) => b.id);
      const lastSelected = [...selected].pop()!;
      const a = ids.indexOf(lastSelected);
      const b = ids.indexOf(id);
      if (a >= 0 && b >= 0) {
        const [lo, hi] = a < b ? [a, b] : [b, a];
        for (let i = lo; i <= hi; i++) next.add(ids[i]);
      }
    } else if (next.has(id)) {
      next.delete(id);
    } else {
      next.add(id);
    }
    setSelected(next);
  }

  async function assignTag(tagID: number) {
    if (selected.size === 0) return;
    await api.assignTag([...selected], tagID);
    setSelected(new Set());
    refresh();
  }

  async function togglePause() {
    if (paused) await api.resumeTracking();
    else await api.pauseTracking();
    setPaused(!paused);
  }

  async function syncDay() {
    setSyncing(true);
    try {
      const r = await api.syncDay(startOfDayUTCISO(day));
      if (r.Errors && r.Errors.length > 0) setError(r.Errors.join("; "));
      else setError(null);
    } catch (e) {
      setError(String(e));
    } finally {
      setSyncing(false);
      refresh();
    }
  }

  const totalSec = blocks
    .filter((b) => !b.is_idle)
    .reduce((s, b) => s + b.duration_sec, 0);

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-3">
        <input
          type="date"
          value={dateInputValue(day)}
          onChange={(e) => setDay(fromDateInput(e.target.value))}
          className="rounded bg-surface px-2 py-1 text-sm text-slate-100"
        />
        <button
          onClick={() =>
            setDay((d) => new Date(d.getTime() - 24 * 3600 * 1000))
          }
          className="rounded bg-surface px-3 py-1 text-sm hover:bg-slate-700"
        >
          ← Vortag
        </button>
        <button
          onClick={() =>
            setDay((d) => new Date(d.getTime() + 24 * 3600 * 1000))
          }
          className="rounded bg-surface px-3 py-1 text-sm hover:bg-slate-700"
        >
          Folgetag →
        </button>
        <span className="ml-auto text-sm text-slate-400">
          Summe: {formatDuration(totalSec)}
        </span>
        <button
          onClick={togglePause}
          className={`rounded px-3 py-1 text-sm ${
            paused ? "bg-amber-600" : "bg-emerald-600"
          } text-white`}
        >
          {paused ? "Fortsetzen" : "Pausieren"}
        </button>
        <button
          onClick={syncDay}
          disabled={syncing}
          className="rounded bg-accent px-3 py-1 text-sm text-white disabled:opacity-50"
        >
          {syncing ? "Synchronisiere…" : "Sync zu Personio"}
        </button>
      </div>

      {error && (
        <div className="rounded bg-red-900/40 px-3 py-2 text-sm text-red-200">
          {error}
        </div>
      )}

      {selected.size > 0 && (
        <div className="flex flex-wrap items-center gap-2 rounded bg-surface px-3 py-2">
          <span className="text-sm text-slate-300">
            {selected.size} Block(s) markiert →
          </span>
          {tags.map((t) => (
            <button
              key={t.id}
              onClick={() => assignTag(t.id)}
              className="rounded bg-slate-700 px-2 py-1 text-xs hover:bg-slate-600"
            >
              {t.name}
            </button>
          ))}
          <button
            onClick={() => assignTag(0)}
            className="rounded bg-slate-700 px-2 py-1 text-xs hover:bg-slate-600"
          >
            Tag entfernen
          </button>
        </div>
      )}

      <ul className="divide-y divide-slate-700 rounded bg-surface">
        {blocks.length === 0 && (
          <li className="px-3 py-6 text-center text-sm text-slate-400">
            Keine Blöcke an diesem Tag.
          </li>
        )}
        {blocks.map((b) => {
          const tag = b.tag_id ? tagsByID[b.tag_id] : undefined;
          return (
            <li
              key={b.id}
              onClick={(e) => toggleSelect(b.id, e.shiftKey)}
              className={`cursor-pointer px-3 py-2 text-sm ${
                selected.has(b.id) ? "bg-accent/20" : "hover:bg-slate-700/40"
              } ${b.is_idle ? "opacity-50" : ""}`}
            >
              <div className="flex items-center gap-3">
                <span className="w-24 font-mono text-xs text-slate-400">
                  {formatHHMM(b.start_time)}
                  {b.end_time ? `–${formatHHMM(b.end_time)}` : ""}
                </span>
                <span className="w-16 text-xs text-slate-500">
                  {formatDuration(b.duration_sec)}
                </span>
                <span className="w-40 truncate text-slate-300">
                  {b.process_name}
                </span>
                <span className="flex-1 truncate text-slate-400">
                  {b.window_title}
                </span>
                {tag && (
                  <span
                    className="rounded px-2 py-0.5 text-xs"
                    style={{ background: tag.color ?? "#4f8cff" }}
                  >
                    {tag.name}
                    {b.auto_tagged ? " ⚙" : ""}
                  </span>
                )}
              </div>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
