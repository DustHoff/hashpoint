import { useEffect, useMemo, useRef, useState } from "react";
import { api } from "../api";
import type { FocusBlock, Tag } from "../types";
import {
  dateInputValue,
  formatDuration,
  formatHHMM,
  fromDateInput,
  startOfDayUTCISO,
} from "../lib/time";

// Day-in-ms helpers — the timeline strip maps the visible day [00:00, 24:00)
// in local time to a 0..1 percentage along the strip width.
const MS_PER_DAY = 24 * 3600 * 1000;

function dayBounds(day: Date): { from: number; to: number } {
  const d = new Date(day.getFullYear(), day.getMonth(), day.getDate());
  return { from: d.getTime(), to: d.getTime() + MS_PER_DAY };
}

function clampPct(pct: number): number {
  return Math.max(0, Math.min(1, pct));
}

function blockBounds(b: FocusBlock): { start: number; end: number } {
  const start = new Date(b.start_time).getTime();
  const end = b.end_time ? new Date(b.end_time).getTime() : Date.now();
  return { start, end: Math.max(end, start + 1000) };
}

// A contiguous tag segment groups adjacent blocks that share the same tag id.
// Blocks without a tag are not grouped into a segment (they remain selectable
// individually but are shown as a separate "untagged" stripe).
interface Segment {
  tagID: number | null;
  blockIDs: number[];
  start: number;
  end: number;
  description: string;
  hasMixedDescriptions: boolean;
}

function buildSegments(blocks: FocusBlock[]): Segment[] {
  const out: Segment[] = [];
  let cur: Segment | null = null;
  for (const b of blocks) {
    if (b.is_idle) {
      cur = null;
      continue;
    }
    const { start, end } = blockBounds(b);
    const tagID = b.tag_id ?? null;
    if (cur && cur.tagID === tagID) {
      cur.blockIDs.push(b.id);
      cur.end = Math.max(cur.end, end);
      const d = b.description ?? "";
      if (d && cur.description === "") cur.description = d;
      else if (d && d !== cur.description) cur.hasMixedDescriptions = true;
    } else {
      cur = {
        tagID,
        blockIDs: [b.id],
        start,
        end,
        description: b.description ?? "",
        hasMixedDescriptions: false,
      };
      out.push(cur);
    }
  }
  return out;
}

interface MsRange {
  start: number;
  end: number;
}

export default function Timeline() {
  const [day, setDay] = useState<Date>(new Date());
  const [blocks, setBlocks] = useState<FocusBlock[]>([]);
  const [tags, setTags] = useState<Tag[]>([]);
  const [selected, setSelected] = useState<Set<number>>(new Set());
  // Active hover range — used to filter the table to apps within it.
  const [hoverRange, setHoverRange] = useState<MsRange | null>(null);
  // Range committed by the last mouse-drag. Forwarded to the backend so any
  // gap between the dragged time window and actual tracked blocks is filled
  // with placeholder blocks (which then sync as a contiguous Personio period).
  const [selectedRange, setSelectedRange] = useState<MsRange | null>(null);
  const [description, setDescription] = useState<string>("");
  const [paused, setPaused] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [syncing, setSyncing] = useState(false);

  const stripRef = useRef<HTMLDivElement>(null);
  const dragStartPctRef = useRef<number | null>(null);
  const [dragRange, setDragRange] = useState<{ a: number; b: number } | null>(
    null,
  );

  const tagsByID = useMemo(() => {
    const m: Record<number, Tag> = {};
    tags.forEach((t) => (m[t.id] = t));
    return m;
  }, [tags]);

  const segments = useMemo(() => buildSegments(blocks), [blocks]);
  const { from: dayFromMs } = useMemo(() => dayBounds(day), [day]);

  const selectedBlocks = useMemo(
    () => blocks.filter((b) => selected.has(b.id)),
    [blocks, selected],
  );

  // Single-tag detection: when all selected blocks share one tag, the
  // description editor targets the contiguous tag segment as a whole.
  const sharedTagID = useMemo<number | null | "mixed">(() => {
    if (selectedBlocks.length === 0) return null;
    const first = selectedBlocks[0].tag_id ?? null;
    for (const b of selectedBlocks) {
      const cur = b.tag_id ?? null;
      if (cur !== first) return "mixed";
    }
    return first;
  }, [selectedBlocks]);

  const sharedDescription = useMemo<string>(() => {
    if (selectedBlocks.length === 0) return "";
    const first = selectedBlocks[0].description ?? "";
    for (const b of selectedBlocks) {
      if ((b.description ?? "") !== first) return "";
    }
    return first;
  }, [selectedBlocks]);

  // Sync the description editor to the current selection's shared value.
  useEffect(() => {
    setDescription(sharedDescription);
  }, [sharedDescription]);

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

  function clearSelection() {
    setSelected(new Set());
    setSelectedRange(null);
    setDescription("");
  }

  function toggleSelect(id: number, range: boolean) {
    // Any individual click invalidates the previously committed drag range.
    setSelectedRange(null);
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

  function selectSegment(seg: Segment, additive: boolean) {
    setSelectedRange(null);
    const next = additive ? new Set(selected) : new Set<number>();
    for (const id of seg.blockIDs) next.add(id);
    setSelected(next);
  }

  function rangeToISO(r: MsRange | null): { start: string; end: string } {
    if (!r) return { start: "", end: "" };
    return {
      start: new Date(r.start).toISOString(),
      end: new Date(r.end).toISOString(),
    };
  }

  async function assignTag(tagID: number) {
    if (selected.size === 0 && !selectedRange) return;
    const { start, end } = rangeToISO(selectedRange);
    try {
      await api.assignTagAndDescription(
        [...selected],
        tagID,
        description,
        start,
        end,
      );
      clearSelection();
      await refresh();
    } catch (e) {
      setError(String(e));
    }
  }

  async function saveDescriptionOnly() {
    if (selected.size === 0) return;
    try {
      // tagID = -1 leaves the existing tag(s) untouched; only writes description.
      // No range is forwarded — description-only edits never spawn placeholders.
      await api.assignTagAndDescription(
        [...selected],
        -1,
        description,
        "",
        "",
      );
      await refresh();
    } catch (e) {
      setError(String(e));
    }
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

  // -- Strip geometry helpers ---------------------------------------------

  function pctOfMs(ms: number): number {
    return clampPct((ms - dayFromMs) / MS_PER_DAY);
  }

  function pctFromEvent(e: React.MouseEvent | MouseEvent): number {
    const rect = stripRef.current?.getBoundingClientRect();
    if (!rect) return 0;
    return clampPct((e.clientX - rect.left) / rect.width);
  }

  function pctRangeToMs(a: number, b: number): MsRange {
    const lo = Math.min(a, b);
    const hi = Math.max(a, b);
    return { start: dayFromMs + lo * MS_PER_DAY, end: dayFromMs + hi * MS_PER_DAY };
  }

  function blocksInMsRange(r: MsRange): FocusBlock[] {
    return blocks.filter((bl) => {
      if (bl.is_idle) return false;
      const { start, end } = blockBounds(bl);
      return end > r.start && start < r.end;
    });
  }

  // -- Strip mouse handlers ------------------------------------------------

  function onStripMouseDown(e: React.MouseEvent) {
    if (e.button !== 0) return;
    const pct = pctFromEvent(e);
    dragStartPctRef.current = pct;
    setDragRange({ a: pct, b: pct });
    setHoverRange(pctRangeToMs(pct, pct));
    e.preventDefault();
  }

  useEffect(() => {
    function onMove(e: MouseEvent) {
      if (dragStartPctRef.current == null) return;
      const pct = pctFromEvent(e);
      setDragRange({ a: dragStartPctRef.current, b: pct });
      setHoverRange(pctRangeToMs(dragStartPctRef.current, pct));
    }
    function onUp(e: MouseEvent) {
      if (dragStartPctRef.current == null) return;
      const startPct = dragStartPctRef.current;
      const endPct = pctFromEvent(e);
      dragStartPctRef.current = null;
      const moved = Math.abs(endPct - startPct) > 0.001;
      const r = pctRangeToMs(startPct, endPct);
      const matched = moved ? blocksInMsRange(r) : [];
      if (moved) {
        const next = e.shiftKey ? new Set(selected) : new Set<number>();
        for (const b of matched) next.add(b.id);
        setSelected(next);
        setSelectedRange(r);
      }
      setDragRange(null);
      setHoverRange(null);
    }
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
    return () => {
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [blocks, selected, dayFromMs]);

  function onSegmentMouseEnter(seg: Segment) {
    if (dragStartPctRef.current != null) return;
    setHoverRange({ start: seg.start, end: seg.end });
  }

  function onSegmentMouseLeave() {
    if (dragStartPctRef.current != null) return;
    setHoverRange(null);
  }

  // -- Table filtering -----------------------------------------------------

  const visibleBlocks = useMemo<FocusBlock[]>(() => {
    if (!hoverRange) return blocks;
    return blocks.filter((b) => {
      const { start, end } = blockBounds(b);
      return end > hoverRange.start && start < hoverRange.end;
    });
  }, [blocks, hoverRange]);

  // -- Render --------------------------------------------------------------

  const hasSelection = selected.size > 0 || selectedRange != null;
  const rangeMsLabel = (r: MsRange) =>
    `${formatHHMM(new Date(r.start).toISOString())}–${formatHHMM(new Date(r.end).toISOString())}`;

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

      {/* --- Timeline strip ------------------------------------------------ */}
      <div className="rounded bg-surface px-3 py-3">
        <div className="mb-1 flex justify-between text-[10px] text-slate-500">
          {[0, 6, 12, 18, 24].map((h) => (
            <span key={h}>{h.toString().padStart(2, "0")}:00</span>
          ))}
        </div>
        <div
          ref={stripRef}
          onMouseDown={onStripMouseDown}
          className="relative h-10 cursor-crosshair select-none rounded bg-slate-900/60"
          title="Zeitspanne ziehen, um Blöcke zu markieren"
        >
          {/* Hour gridlines */}
          {Array.from({ length: 23 }, (_, i) => i + 1).map((h) => (
            <div
              key={h}
              className="absolute inset-y-0 w-px bg-slate-700/40"
              style={{ left: `${(h / 24) * 100}%` }}
            />
          ))}

          {/* Idle blocks (faint) */}
          {blocks
            .filter((b) => b.is_idle)
            .map((b) => {
              const { start, end } = blockBounds(b);
              const left = pctOfMs(start) * 100;
              const width = (pctOfMs(end) - pctOfMs(start)) * 100;
              return (
                <div
                  key={`idle-${b.id}`}
                  className="absolute inset-y-2 rounded bg-slate-700/40"
                  style={{ left: `${left}%`, width: `${width}%` }}
                  title="Idle"
                />
              );
            })}

          {/* Tag segments */}
          {segments.map((seg, i) => {
            const left = pctOfMs(seg.start) * 100;
            const width = (pctOfMs(seg.end) - pctOfMs(seg.start)) * 100;
            const tag = seg.tagID != null ? tagsByID[seg.tagID] : undefined;
            const bg = tag?.color ?? (seg.tagID == null ? "#475569" : "#4f8cff");
            const isHovered =
              hoverRange != null &&
              seg.end > hoverRange.start &&
              seg.start < hoverRange.end;
            const isSelected = seg.blockIDs.every((id) => selected.has(id));
            // A segment is "all placeholder" if every contained block is one —
            // marked with a dashed outline so it reads as user-authored time.
            const allPlaceholder = seg.blockIDs.every(
              (id) => blocks.find((b) => b.id === id)?.is_placeholder,
            );
            return (
              <div
                key={`seg-${i}`}
                onClick={(e) => {
                  e.stopPropagation();
                  selectSegment(seg, e.shiftKey);
                }}
                onMouseEnter={() => onSegmentMouseEnter(seg)}
                onMouseLeave={onSegmentMouseLeave}
                className={`absolute top-1 bottom-1 cursor-pointer rounded transition-[outline] ${
                  isSelected
                    ? "outline outline-2 outline-white"
                    : isHovered
                      ? "outline outline-1 outline-white/70"
                      : ""
                } ${seg.tagID == null ? "opacity-50" : ""} ${
                  allPlaceholder ? "border border-dashed border-white/60" : ""
                }`}
                style={{
                  left: `${left}%`,
                  width: `${Math.max(width, 0.2)}%`,
                  background: bg,
                }}
                title={
                  (tag ? tag.name : "ohne Tag") +
                  (allPlaceholder ? " · manuelle Zeitspanne" : "") +
                  (seg.description
                    ? `\n${seg.description}${seg.hasMixedDescriptions ? " (gemischt)" : ""}`
                    : "")
                }
              />
            );
          })}

          {/* Active drag-range overlay */}
          {dragRange && (
            <div
              className="pointer-events-none absolute inset-y-0 rounded bg-accent/30 outline outline-1 outline-accent"
              style={{
                left: `${Math.min(dragRange.a, dragRange.b) * 100}%`,
                width: `${Math.abs(dragRange.b - dragRange.a) * 100}%`,
              }}
            />
          )}

          {/* Committed selected-range marker (after mouse-up) */}
          {!dragRange && selectedRange && (
            <div
              className="pointer-events-none absolute inset-y-0 rounded outline outline-1 outline-accent/70"
              style={{
                left: `${pctOfMs(selectedRange.start) * 100}%`,
                width: `${(pctOfMs(selectedRange.end) - pctOfMs(selectedRange.start)) * 100}%`,
              }}
            />
          )}
        </div>
        <div className="mt-1 text-[11px] text-slate-500">
          Tipp: Zeitspanne ziehen, um alle Blöcke darin zu markieren · Hover auf
          einen Tag-Abschnitt filtert die Programmliste · Ein gezogener Bereich
          ohne Programme erzeugt beim Taggen einen Platzhalter.
        </div>
      </div>

      {/* --- Selection / tagging panel ------------------------------------ */}
      {hasSelection && (
        <div className="space-y-2 rounded bg-surface px-3 py-3">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-sm text-slate-300">
              {selected.size > 0 && (
                <>
                  {selected.size} Block(s) markiert
                  {sharedTagID === "mixed" && (
                    <span className="ml-2 text-xs text-amber-300">
                      (verschiedene Tags)
                    </span>
                  )}
                </>
              )}
              {selectedRange && (
                <span className="ml-2 text-xs text-slate-400">
                  · Bereich {rangeMsLabel(selectedRange)}
                  {selected.size === 0 && " (ohne Programme)"}
                </span>
              )}
              <span className="ml-1">→</span>
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
              title="Tag entfernen — Platzhalterblöcke werden gelöscht"
            >
              Tag entfernen
            </button>
            <button
              onClick={clearSelection}
              className="ml-auto rounded bg-slate-700 px-2 py-1 text-xs hover:bg-slate-600"
            >
              Auswahl aufheben
            </button>
          </div>
          <div className="flex items-start gap-2">
            <textarea
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Tätigkeitsbeschreibung — wird allen markierten Blöcken zugewiesen"
              rows={2}
              className="flex-1 resize-y rounded bg-slate-900/60 px-2 py-1 text-sm text-slate-100 placeholder:text-slate-500"
            />
            <button
              onClick={saveDescriptionOnly}
              disabled={selected.size === 0}
              className="rounded bg-accent px-3 py-1 text-xs text-white hover:bg-accent/80 disabled:opacity-50"
            >
              Beschreibung speichern
            </button>
          </div>
          <p className="text-[11px] text-slate-500">
            „Tag wählen" speichert Tag und Beschreibung in einem Schritt;
            „Beschreibung speichern" lässt das bestehende Tagging unverändert.
            Bei einem gezogenen Bereich ohne Programme wird beim Taggen ein
            Platzhalter erzeugt — „Tag entfernen" löscht ihn wieder.
          </p>
        </div>
      )}

      {/* --- Block list (table view) -------------------------------------- */}
      <ul className="divide-y divide-slate-700 rounded bg-surface">
        {visibleBlocks.length === 0 && (
          <li className="px-3 py-6 text-center text-sm text-slate-400">
            {hoverRange
              ? "Keine Programme im markierten Zeitraum."
              : "Keine Blöcke an diesem Tag."}
          </li>
        )}
        {visibleBlocks.map((b) => {
          const tag = b.tag_id ? tagsByID[b.tag_id] : undefined;
          const isSel = selected.has(b.id);
          return (
            <li
              key={b.id}
              onClick={(e) => toggleSelect(b.id, e.shiftKey)}
              className={`cursor-pointer px-3 py-2 text-sm transition-colors ${
                isSel ? "bg-accent/20" : "hover:bg-slate-700/40"
              } ${b.is_idle ? "opacity-50" : ""} ${
                b.is_placeholder ? "italic" : ""
              }`}
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
                  {b.is_placeholder ? "(Manueller Eintrag)" : b.process_name}
                </span>
                <span className="flex-1 truncate text-slate-400">
                  {b.is_placeholder ? "—" : b.window_title}
                </span>
                {b.description && (
                  <span
                    className="max-w-[20%] truncate text-xs italic text-slate-500"
                    title={b.description}
                  >
                    📝 {b.description}
                  </span>
                )}
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
