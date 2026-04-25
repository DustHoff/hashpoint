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
const MIN_VIEW_SPAN_MS = 5 * 60 * 1000; // 5 min — clamp for wheel zoom-in
const UNTAGGED_COLOR = "#475569";

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

// Hash a process name to a deterministic muted color so untagged segments
// remain visually distinguishable from one another.
function colorFromName(name: string): string {
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) | 0;
  const hue = Math.abs(h) % 360;
  return `hsl(${hue} 35% 45%)`;
}

// A contiguous segment groups adjacent blocks that share both tag and
// process — same-program neighbours collapse into one rectangle on the strip
// and share a single hover/select target.
interface Segment {
  tagID: number | null;
  processName: string;
  blockIDs: number[];
  start: number;
  end: number;
  description: string;
  hasMixedDescriptions: boolean;
  allPlaceholder: boolean;
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
    if (cur && cur.tagID === tagID && cur.processName === b.process_name) {
      cur.blockIDs.push(b.id);
      cur.end = Math.max(cur.end, end);
      const d = b.description ?? "";
      if (d && cur.description === "") cur.description = d;
      else if (d && d !== cur.description) cur.hasMixedDescriptions = true;
      cur.allPlaceholder = cur.allPlaceholder && b.is_placeholder;
    } else {
      cur = {
        tagID,
        processName: b.process_name,
        blockIDs: [b.id],
        start,
        end,
        description: b.description ?? "",
        hasMixedDescriptions: false,
        allPlaceholder: b.is_placeholder,
      };
      out.push(cur);
    }
  }
  return out;
}

// A row in the table view: collapses adjacent blocks that share program, tag
// and description so a long run of identical entries reads as one line.
interface BlockGroup {
  blockIDs: number[];
  startMs: number;
  endMs: number;
  durationSec: number;
  processName: string;
  windowTitle: string;
  tagID: number | null;
  description: string;
  isIdle: boolean;
  isPlaceholder: boolean;
  autoTagged: boolean;
}

function groupBlocksForTable(blocks: FocusBlock[]): BlockGroup[] {
  const out: BlockGroup[] = [];
  for (const b of blocks) {
    const { start, end } = blockBounds(b);
    const last = out[out.length - 1];
    const sameKey =
      last &&
      last.processName === b.process_name &&
      last.tagID === (b.tag_id ?? null) &&
      last.description === (b.description ?? "") &&
      last.isIdle === b.is_idle &&
      last.isPlaceholder === b.is_placeholder;
    if (last && sameKey) {
      last.blockIDs.push(b.id);
      last.endMs = Math.max(last.endMs, end);
      last.durationSec += b.duration_sec;
      last.autoTagged = last.autoTagged || b.auto_tagged;
    } else {
      out.push({
        blockIDs: [b.id],
        startMs: start,
        endMs: end,
        durationSec: b.duration_sec,
        processName: b.process_name,
        windowTitle: b.window_title,
        tagID: b.tag_id ?? null,
        description: b.description ?? "",
        isIdle: b.is_idle,
        isPlaceholder: b.is_placeholder,
        autoTagged: b.auto_tagged,
      });
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
  // Independent of `error`: refresh() clears `error` on success, which would
  // otherwise wipe the sync result a few hundred ms after the user clicks.
  const [syncMessage, setSyncMessage] = useState<
    { level: "success" | "info" | "error"; text: string } | null
  >(null);

  // Visible window for the strip — wheel zoom narrows it; shift+wheel pans;
  // double-click resets to the full day.
  const [viewStart, setViewStart] = useState<number>(() => dayBounds(new Date()).from);
  const [viewEnd, setViewEnd] = useState<number>(() => dayBounds(new Date()).to);

  const stripRef = useRef<HTMLDivElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
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
  const { from: dayFromMs, to: dayToMs } = useMemo(() => dayBounds(day), [day]);

  const selectedBlocks = useMemo(
    () => blocks.filter((b) => selected.has(b.id)),
    [blocks, selected],
  );

  // Reset zoom whenever the day changes.
  useEffect(() => {
    setViewStart(dayFromMs);
    setViewEnd(dayToMs);
  }, [dayFromMs, dayToMs]);

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

  // Auto-scroll the page back to the editor panel whenever a fresh selection
  // appears — saves the user from scrolling up after picking a row in a long
  // table.
  const hadSelectionRef = useRef(false);
  useEffect(() => {
    const has = selected.size > 0 || selectedRange != null;
    if (has && !hadSelectionRef.current) {
      panelRef.current?.scrollIntoView({ behavior: "smooth", block: "start" });
    }
    hadSelectionRef.current = has;
  }, [selected, selectedRange]);

  function clearSelection() {
    setSelected(new Set());
    setSelectedRange(null);
    setDescription("");
  }

  function toggleGroup(g: BlockGroup, range: boolean) {
    setSelectedRange(null);
    const next = new Set(selected);
    if (range && selected.size > 0) {
      // Range select across groups: extend selection to cover all blocks
      // between the last picked block and this group's first block.
      const ids = blocks.map((b) => b.id);
      const lastSelected = [...selected].pop()!;
      const a = ids.indexOf(lastSelected);
      const b = ids.indexOf(g.blockIDs[0]);
      if (a >= 0 && b >= 0) {
        const [lo, hi] = a < b ? [a, b] : [b, a];
        for (let i = lo; i <= hi; i++) next.add(ids[i]);
      }
      for (const id of g.blockIDs) next.add(id);
    } else {
      const allSelected = g.blockIDs.every((id) => next.has(id));
      if (allSelected) {
        for (const id of g.blockIDs) next.delete(id);
      } else {
        for (const id of g.blockIDs) next.add(id);
      }
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

  async function deleteSelectedBlocks() {
    const ids = [...selected];
    if (ids.length === 0) return;
    const ok = window.confirm(
      `${ids.length} Eintrag/Einträge wirklich löschen? Diese Aktion kann nicht rückgängig gemacht werden.`,
    );
    if (!ok) return;
    try {
      const removed = await api.deleteBlocks(ids);
      if (removed !== ids.length) {
        setError(
          `Es wurden nur ${removed} von ${ids.length} Einträgen gelöscht — bitte Logfile prüfen.`,
        );
      } else {
        setError(null);
      }
      clearSelection();
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
    setSyncMessage(null);
    try {
      const r = await api.syncDay(startOfDayUTCISO(day));
      if (!r) {
        setSyncMessage({
          level: "error",
          text: "Sync fehlgeschlagen — keine Antwort vom Backend.",
        });
      } else if (r.Errors && r.Errors.length > 0) {
        setSyncMessage({
          level: "error",
          text: `Sync fehlgeschlagen: ${r.Errors.join("; ")}`,
        });
      } else if (r.Periods > 0) {
        setSyncMessage({
          level: "success",
          text: `Synchronisiert: ${r.Periods} Periode(n), ${r.BlocksProcessed} Block/Blöcke gebucht${
            r.BlocksSkipped > 0 ? ` (${r.BlocksSkipped} übersprungen)` : ""
          }.`,
        });
      } else if (r.BlocksSkipped > 0) {
        setSyncMessage({
          level: "info",
          text: `Nichts an Personio gesendet — alle ${r.BlocksSkipped} Block/Blöcke übersprungen. Tags müssen "Zu Personio synchronisieren" aktiviert haben (Idle- und offene Blöcke werden immer übersprungen).`,
        });
      } else {
        setSyncMessage({
          level: "info",
          text: "Keine getaggten Blöcke für diesen Tag — bitte zuerst Blöcke taggen.",
        });
      }
    } catch (e) {
      setSyncMessage({
        level: "error",
        text: `Sync fehlgeschlagen: ${String(e)}`,
      });
    } finally {
      setSyncing(false);
      refresh();
    }
  }

  const totalSec = blocks
    .filter((b) => !b.is_idle)
    .reduce((s, b) => s + b.duration_sec, 0);

  // -- Strip geometry helpers (view-window aware) -------------------------

  const viewSpan = Math.max(1, viewEnd - viewStart);

  function pctOfMs(ms: number): number {
    return clampPct((ms - viewStart) / viewSpan);
  }

  function pctFromEvent(e: React.MouseEvent | MouseEvent): number {
    const rect = stripRef.current?.getBoundingClientRect();
    if (!rect) return 0;
    return clampPct((e.clientX - rect.left) / rect.width);
  }

  function pctRangeToMs(a: number, b: number): MsRange {
    const lo = Math.min(a, b);
    const hi = Math.max(a, b);
    return { start: viewStart + lo * viewSpan, end: viewStart + hi * viewSpan };
  }

  function blocksInMsRange(r: MsRange): FocusBlock[] {
    return blocks.filter((bl) => {
      if (bl.is_idle) return false;
      const { start, end } = blockBounds(bl);
      return end > r.start && start < r.end;
    });
  }

  // -- Wheel zoom + pan ---------------------------------------------------

  useEffect(() => {
    const el = stripRef.current;
    if (!el) return;
    function onWheel(e: WheelEvent) {
      e.preventDefault();
      const rect = el!.getBoundingClientRect();
      const cursorPct = clampPct((e.clientX - rect.left) / rect.width);
      const span = viewEnd - viewStart;
      if (e.shiftKey) {
        // Pan: 1 wheel step ≈ 10% of the visible span horizontally.
        const dx = (e.deltaY / 100) * span * 0.5;
        let ns = viewStart + dx;
        let ne = viewEnd + dx;
        if (ns < dayFromMs) {
          ns = dayFromMs;
          ne = ns + span;
        }
        if (ne > dayToMs) {
          ne = dayToMs;
          ns = ne - span;
        }
        setViewStart(ns);
        setViewEnd(ne);
        return;
      }
      const cursorMs = viewStart + cursorPct * span;
      const factor = e.deltaY < 0 ? 0.8 : 1.25;
      const newSpan = Math.max(MIN_VIEW_SPAN_MS, Math.min(MS_PER_DAY, span * factor));
      let ns = cursorMs - cursorPct * newSpan;
      let ne = ns + newSpan;
      if (ns < dayFromMs) {
        ns = dayFromMs;
        ne = ns + newSpan;
      }
      if (ne > dayToMs) {
        ne = dayToMs;
        ns = ne - newSpan;
      }
      setViewStart(ns);
      setViewEnd(ne);
    }
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  }, [viewStart, viewEnd, dayFromMs, dayToMs]);

  function resetZoom() {
    setViewStart(dayFromMs);
    setViewEnd(dayToMs);
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
  }, [blocks, selected, viewStart, viewEnd]);

  function onSegmentMouseEnter(seg: Segment) {
    if (dragStartPctRef.current != null) return;
    setHoverRange({ start: seg.start, end: seg.end });
  }

  function onSegmentMouseLeave() {
    if (dragStartPctRef.current != null) return;
    setHoverRange(null);
  }

  // -- Table filtering & grouping -----------------------------------------

  const visibleBlocks = useMemo<FocusBlock[]>(() => {
    if (!hoverRange) return blocks;
    return blocks.filter((b) => {
      const { start, end } = blockBounds(b);
      return end > hoverRange.start && start < hoverRange.end;
    });
  }, [blocks, hoverRange]);

  const visibleGroups = useMemo<BlockGroup[]>(
    () => groupBlocksForTable(visibleBlocks),
    [visibleBlocks],
  );

  // Visible time-axis ticks: hours falling within the current view window.
  const hourTicks = useMemo<number[]>(() => {
    const ticks: number[] = [];
    for (let h = 0; h <= 24; h++) {
      const ms = dayFromMs + h * 3600 * 1000;
      if (ms >= viewStart && ms <= viewEnd) ticks.push(ms);
    }
    return ticks;
  }, [dayFromMs, viewStart, viewEnd]);

  // Top-axis labels: 5 evenly-spaced timestamps across the visible window.
  const axisLabels = useMemo<string[]>(() => {
    const out: string[] = [];
    for (let i = 0; i < 5; i++) {
      const ms = viewStart + (i / 4) * (viewEnd - viewStart);
      out.push(formatHHMM(new Date(ms).toISOString()));
    }
    return out;
  }, [viewStart, viewEnd]);

  // -- Render --------------------------------------------------------------

  const hasSelection = selected.size > 0 || selectedRange != null;
  const rangeMsLabel = (r: MsRange) =>
    `${formatHHMM(new Date(r.start).toISOString())}–${formatHHMM(new Date(r.end).toISOString())}`;
  const isZoomed = viewStart > dayFromMs || viewEnd < dayToMs;

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

      {syncMessage && (
        <div
          className={`flex items-start justify-between gap-3 rounded px-3 py-2 text-sm ${
            syncMessage.level === "success"
              ? "bg-emerald-900/40 text-emerald-200"
              : syncMessage.level === "error"
                ? "bg-red-900/40 text-red-200"
                : "bg-amber-900/30 text-amber-200"
          }`}
        >
          <span>{syncMessage.text}</span>
          <button
            onClick={() => setSyncMessage(null)}
            className="text-xs opacity-70 hover:opacity-100"
            aria-label="Meldung schließen"
          >
            ✕
          </button>
        </div>
      )}

      {/* --- Selection / tagging panel ------------------------------------ */}
      <div ref={panelRef}>
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
                  className="rounded px-2 py-1 text-xs text-white hover:opacity-80"
                  style={{ background: t.color ?? "#4f8cff" }}
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
              <div className="flex flex-col gap-1">
                <button
                  onClick={saveDescriptionOnly}
                  disabled={selected.size === 0}
                  className="rounded bg-accent px-3 py-1 text-xs text-white hover:bg-accent/80 disabled:opacity-50"
                  title="Beschreibung speichern (Tag bleibt unverändert)"
                >
                  Speichern
                </button>
                <button
                  onClick={deleteSelectedBlocks}
                  disabled={selected.size === 0}
                  className="rounded bg-red-700 px-3 py-1 text-xs text-white hover:bg-red-600 disabled:opacity-50"
                  title="Markierte Einträge endgültig löschen"
                >
                  Löschen
                </button>
              </div>
            </div>
            <p className="text-[11px] text-slate-500">
              „Tag wählen" speichert Tag und Beschreibung in einem Schritt;
              „Speichern" lässt das bestehende Tagging unverändert. „Löschen"
              entfernt die markierten Einträge dauerhaft. Bei einem gezogenen
              Bereich ohne Programme wird beim Taggen ein Platzhalter erzeugt —
              „Tag entfernen" löscht ihn wieder.
            </p>
          </div>
        )}
      </div>

      {/* --- Timeline strip ------------------------------------------------ */}
      <div className="rounded bg-surface px-3 py-3">
        <div className="mb-1 flex justify-between text-[10px] text-slate-500">
          {axisLabels.map((l, i) => (
            <span key={i}>{l}</span>
          ))}
        </div>
        <div
          ref={stripRef}
          onMouseDown={onStripMouseDown}
          onDoubleClick={resetZoom}
          className="relative h-10 cursor-crosshair select-none rounded bg-slate-900/60"
          title="Zeitspanne ziehen, um Blöcke zu markieren · Mausrad: zoom · Shift+Mausrad: schwenken · Doppelklick: Reset"
        >
          {/* Hour gridlines (only those visible in the current zoom window). */}
          {hourTicks.map((ms) => (
            <div
              key={ms}
              className="absolute inset-y-0 w-px bg-slate-700/40"
              style={{ left: `${pctOfMs(ms) * 100}%` }}
            />
          ))}

          {/* Idle blocks (faint) */}
          {blocks
            .filter((b) => b.is_idle)
            .map((b) => {
              const { start, end } = blockBounds(b);
              if (end <= viewStart || start >= viewEnd) return null;
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

          {/* Tag/program segments — color = tag color (untagged uses a stable
              per-program hue so consecutive different programs read apart). */}
          {segments.map((seg, i) => {
            if (seg.end <= viewStart || seg.start >= viewEnd) return null;
            const left = pctOfMs(seg.start) * 100;
            const width = (pctOfMs(seg.end) - pctOfMs(seg.start)) * 100;
            const tag = seg.tagID != null ? tagsByID[seg.tagID] : undefined;
            const bg =
              tag?.color ??
              (seg.tagID == null
                ? colorFromName(seg.processName || "untagged")
                : UNTAGGED_COLOR);
            const isHovered =
              hoverRange != null &&
              seg.end > hoverRange.start &&
              seg.start < hoverRange.end;
            const isSelected = seg.blockIDs.every((id) => selected.has(id));
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
                } ${seg.tagID == null ? "opacity-70" : ""} ${
                  seg.allPlaceholder
                    ? "border border-dashed border-white/60"
                    : ""
                }`}
                style={{
                  left: `${left}%`,
                  width: `${Math.max(width, 0.2)}%`,
                  background: bg,
                }}
                title={
                  `${seg.processName || "(Platzhalter)"} · ${tag ? tag.name : "ohne Tag"}` +
                  (seg.allPlaceholder ? " · manuelle Zeitspanne" : "") +
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
        <div className="mt-1 flex items-center justify-between text-[11px] text-slate-500">
          <span>
            Tipp: Zeitspanne ziehen, um zu markieren · Hover gruppiert gleiche
            Programme automatisch · Mausrad zoomt, Shift+Mausrad schwenkt,
            Doppelklick setzt zurück.
          </span>
          {isZoomed && (
            <button
              onClick={resetZoom}
              className="rounded bg-slate-700 px-2 py-0.5 text-[10px] hover:bg-slate-600"
            >
              Zoom zurücksetzen
            </button>
          )}
        </div>
      </div>

      {/* --- Block list (table view) -------------------------------------- */}
      <ul className="divide-y divide-slate-700 rounded bg-surface">
        {visibleGroups.length === 0 && (
          <li className="px-3 py-6 text-center text-sm text-slate-400">
            {hoverRange
              ? "Keine Programme im markierten Zeitraum."
              : "Keine Blöcke an diesem Tag."}
          </li>
        )}
        {visibleGroups.map((g) => {
          const tag = g.tagID != null ? tagsByID[g.tagID] : undefined;
          const isSel = g.blockIDs.every((id) => selected.has(id));
          const startISO = new Date(g.startMs).toISOString();
          const endISO = new Date(g.endMs).toISOString();
          return (
            <li
              key={g.blockIDs[0]}
              onClick={(e) => toggleGroup(g, e.shiftKey)}
              className={`cursor-pointer px-3 py-2 text-sm transition-colors ${
                isSel ? "bg-accent/20" : "hover:bg-slate-700/40"
              } ${g.isIdle ? "opacity-50" : ""} ${
                g.isPlaceholder ? "italic" : ""
              }`}
            >
              <div className="flex items-center gap-3">
                <span className="w-24 font-mono text-xs text-slate-400">
                  {formatHHMM(startISO)}–{formatHHMM(endISO)}
                </span>
                <span className="w-16 text-xs text-slate-500">
                  {formatDuration(g.durationSec)}
                </span>
                <span className="w-40 truncate text-slate-300">
                  {g.isPlaceholder ? "(Manueller Eintrag)" : g.processName}
                </span>
                <span className="flex-1 truncate text-slate-400">
                  {g.isPlaceholder ? "—" : g.windowTitle}
                </span>
                {g.blockIDs.length > 1 && (
                  <span className="text-[10px] text-slate-500">
                    ×{g.blockIDs.length}
                  </span>
                )}
                {g.description && (
                  <span
                    className="max-w-[20%] truncate text-xs italic text-slate-500"
                    title={g.description}
                  >
                    📝 {g.description}
                  </span>
                )}
                {tag && (
                  <span
                    className="rounded px-2 py-0.5 text-xs"
                    style={{ background: tag.color ?? "#4f8cff" }}
                  >
                    {tag.name}
                    {g.autoTagged ? " ⚙" : ""}
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
