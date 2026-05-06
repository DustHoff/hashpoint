import { Fragment, useEffect, useMemo, useRef, useState } from "react";
import { api } from "../api";
import type { ProcessTrack, Tag, TagBlock } from "../types";
import {
  dateInputValue,
  formatDuration,
  formatHHMM,
  fromDateInput,
  startOfDayUTCISO,
} from "../lib/time";

const MS_PER_DAY = 24 * 3600 * 1000;
const MIN_VIEW_SPAN_MS = 5 * 60 * 1000;
const UNTAGGED_COLOR = "#475569";

function dayBounds(day: Date): { from: number; to: number } {
  const d = new Date(day.getFullYear(), day.getMonth(), day.getDate());
  return { from: d.getTime(), to: d.getTime() + MS_PER_DAY };
}

function clampPct(pct: number): number {
  return Math.max(0, Math.min(1, pct));
}

function tagBlockBounds(b: TagBlock): { start: number; end: number } {
  const start = new Date(b.start_time).getTime();
  const end = b.end_time ? new Date(b.end_time).getTime() : Date.now();
  return { start, end: Math.max(end, start + 1000) };
}

function trackBounds(t: ProcessTrack): { start: number; end: number } {
  const start = new Date(t.start_time).getTime();
  const end = t.end_time ? new Date(t.end_time).getTime() : Date.now();
  return { start, end: Math.max(end, start + 1000) };
}

// Hash a process name to a deterministic muted color.
function colorFromName(name: string): string {
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) | 0;
  const hue = Math.abs(h) % 360;
  return `hsl(${hue} 35% 45%)`;
}

interface MsRange {
  start: number;
  end: number;
}

// Group adjacent process tracks with the same process name + tag state for
// table display. Communication tracks are kept distinct from focused tracks
// so the phone-icon marker stays scoped to the rows that earned it.
interface TrackGroup {
  trackIDs: number[];
  startMs: number;
  endMs: number;
  durationSec: number;
  processName: string;
  windowTitle: string;
  isIdle: boolean;
  isCommunication: boolean;
  members: ProcessTrack[];
}

function groupTracksForTable(tracks: ProcessTrack[]): TrackGroup[] {
  const out: TrackGroup[] = [];
  for (const t of tracks) {
    const { start, end } = trackBounds(t);
    const last = out[out.length - 1];
    const sameKey =
      last &&
      last.processName === t.process_name &&
      last.isIdle === t.is_idle &&
      last.isCommunication === t.is_communication;
    if (last && sameKey) {
      last.trackIDs.push(t.id);
      last.endMs = Math.max(last.endMs, end);
      last.durationSec += t.duration_sec;
      last.members.push(t);
    } else {
      out.push({
        trackIDs: [t.id],
        startMs: start,
        endMs: end,
        durationSec: t.duration_sec,
        processName: t.process_name,
        windowTitle: t.window_title,
        isIdle: t.is_idle,
        isCommunication: t.is_communication,
        members: [t],
      });
    }
  }
  return out;
}

export default function Timeline() {
  const [day, setDay] = useState<Date>(new Date());
  const [tagBlocks, setTagBlocks] = useState<TagBlock[]>([]);
  const [processTracks, setProcessTracks] = useState<ProcessTrack[]>([]);
  const [tags, setTags] = useState<Tag[]>([]);
  const [granularityMs, setGranularityMs] = useState<number>(0);

  // Currently-selected tag-blocks (click on top strip / table row).
  const [selectedBlockIDs, setSelectedBlockIDs] = useState<Set<number>>(new Set());
  // Active hover range (used by readout + table filter).
  const [hoverRange, setHoverRange] = useState<MsRange | null>(null);
  // Drag-committed range awaiting tag pick.
  const [selectedRange, setSelectedRange] = useState<MsRange | null>(null);
  const [expandedGroups, setExpandedGroups] = useState<Set<number>>(new Set());
  const [description, setDescription] = useState<string>("");
  const [paused, setPaused] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [syncing, setSyncing] = useState(false);
  const [syncMessage, setSyncMessage] = useState<
    { level: "success" | "info" | "error"; text: string } | null
  >(null);

  // Shared view window for both strips.
  const [viewStart, setViewStart] = useState<number>(() => dayBounds(new Date()).from);
  const [viewEnd, setViewEnd] = useState<number>(() => dayBounds(new Date()).to);

  const tagStripRef = useRef<HTMLDivElement>(null);
  const trackStripRef = useRef<HTMLDivElement>(null);
  const commStripRef = useRef<HTMLDivElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  const dragStartPctRef = useRef<number | null>(null);
  const [dragRange, setDragRange] = useState<{ a: number; b: number } | null>(
    null,
  );
  const [cursorPctX, setCursorPctX] = useState<number | null>(null);
  const resizeEdgeRef = useRef<"start" | "end" | null>(null);

  // Live resize of an existing closed tag-block. blockResizeCtxRef holds
  // the immutable context (id, dragged edge, neighbor fences); blockResize
  // mirrors the live, snapped range so the strip can re-render the block
  // at its new position during drag.
  const blockResizeCtxRef = useRef<{
    id: number;
    edge: "start" | "end";
    leftFenceMs: number;
    rightFenceMs: number;
    originalStart: number;
    originalEnd: number;
  } | null>(null);
  const [blockResize, setBlockResize] = useState<{
    id: number;
    start: number;
    end: number;
  } | null>(null);

  const tagsByID = useMemo(() => {
    const m: Record<number, Tag> = {};
    tags.forEach((t) => (m[t.id] = t));
    return m;
  }, [tags]);

  // Tags grouped under their parent (top-level first, then their children),
  // so the picker keeps subtags visually anchored to the parent that gives
  // them meaning.
  const orderedTags = useMemo<Tag[]>(() => {
    const parents = tags.filter((t) => t.parent_id == null);
    const childrenByParent: Record<number, Tag[]> = {};
    for (const t of tags) {
      if (t.parent_id != null) {
        (childrenByParent[t.parent_id] ??= []).push(t);
      }
    }
    const out: Tag[] = [];
    for (const p of parents) {
      out.push(p);
      const kids = childrenByParent[p.id] ?? [];
      for (const k of kids) out.push(k);
    }
    // Orphan subtags (parent missing) — keep them visible at the end.
    for (const t of tags) {
      if (t.parent_id != null && !tagsByID[t.parent_id]) out.push(t);
    }
    return out;
  }, [tags, tagsByID]);

  const { from: dayFromMs, to: dayToMs } = useMemo(() => dayBounds(day), [day]);

  const selectedBlocks = useMemo(
    () => tagBlocks.filter((b) => selectedBlockIDs.has(b.id)),
    [tagBlocks, selectedBlockIDs],
  );

  // Reset zoom whenever the day changes.
  useEffect(() => {
    setViewStart(dayFromMs);
    setViewEnd(dayToMs);
  }, [dayFromMs, dayToMs]);

  // Detect shared tag/description across selection so the editor can target
  // the whole group.
  const sharedTagID = useMemo<number | null | "mixed">(() => {
    if (selectedBlocks.length === 0) return null;
    const first = selectedBlocks[0].tag_id;
    for (const b of selectedBlocks) {
      if (b.tag_id !== first) return "mixed";
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

  useEffect(() => {
    setDescription(sharedDescription);
  }, [sharedDescription]);

  async function refresh() {
    try {
      const [blocks, tracks, t, p, cfg] = await Promise.all([
        api.tagBlocksByDay(startOfDayUTCISO(day)),
        api.processTracksByDay(startOfDayUTCISO(day)),
        api.listTags(),
        api.isTrackingPaused(),
        api.getConfig(),
      ]);
      setTagBlocks(blocks ?? []);
      setProcessTracks(tracks ?? []);
      setTags(t ?? []);
      setPaused(p);
      setGranularityMs(
        Math.max(0, cfg?.tracking?.tag_block_granularity_min ?? 0) * 60_000,
      );
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

  // Auto-scroll back to editor whenever a fresh selection appears.
  const hadSelectionRef = useRef(false);
  useEffect(() => {
    const has = selectedBlockIDs.size > 0 || selectedRange != null;
    if (has && !hadSelectionRef.current) {
      panelRef.current?.scrollIntoView({ behavior: "smooth", block: "start" });
    }
    hadSelectionRef.current = has;
  }, [selectedBlockIDs, selectedRange]);

  function clearSelection() {
    setSelectedBlockIDs(new Set());
    setSelectedRange(null);
    setDescription("");
  }

  function toggleBlock(id: number, additive: boolean) {
    setSelectedRange(null);
    const next = additive ? new Set(selectedBlockIDs) : new Set<number>();
    if (next.has(id)) next.delete(id);
    else next.add(id);
    setSelectedBlockIDs(next);
  }

  function rangeToISO(r: MsRange | null): { start: string; end: string } {
    if (!r) return { start: "", end: "" };
    return {
      start: new Date(r.start).toISOString(),
      end: new Date(r.end).toISOString(),
    };
  }

  async function applyTag(tagID: number) {
    try {
      // 1) Drag-committed range: create a manual tag range (orchestrator
      // handles overlap with auto-tag blocks, snaps to granularity).
      if (selectedRange) {
        const { start, end } = rangeToISO(selectedRange);
        await api.createManualTagRange(start, end, tagID, description);
      }
      // 2) Selected existing blocks: re-point their tag (and update description).
      for (const b of selectedBlocks) {
        if (b.tag_id !== tagID) {
          await api.setTagBlockTag(b.id, tagID);
        }
        if ((b.description ?? "") !== description) {
          await api.setTagBlockDescription(b.id, description);
        }
      }
      clearSelection();
      await refresh();
    } catch (e) {
      setError(String(e));
    }
  }

  async function saveDescriptionOnly() {
    try {
      for (const b of selectedBlocks) {
        await api.setTagBlockDescription(b.id, description);
      }
      await refresh();
    } catch (e) {
      setError(String(e));
    }
  }

  // Delete-key shortcut mirrors the Löschen button — same confirm dialog,
  // same scope (only committed blocks; ignores an in-progress drag range).
  // Skipped while focus sits in an input/textarea so editing the
  // description doesn't accidentally wipe blocks.
  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      if (e.key !== "Delete") return;
      if (selectedBlockIDs.size === 0) return;
      const t = e.target as HTMLElement | null;
      const tag = t?.tagName?.toLowerCase();
      if (tag === "input" || tag === "textarea" || t?.isContentEditable) return;
      e.preventDefault();
      deleteSelectedBlocks();
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedBlockIDs]);

  async function deleteSelectedBlocks() {
    const ids = [...selectedBlockIDs];
    if (ids.length === 0) return;
    const ok = window.confirm(
      `${ids.length} Tag-Block/Blöcke wirklich löschen?`,
    );
    if (!ok) return;
    try {
      const removed = await api.deleteTagBlocks(ids);
      if (removed !== ids.length) {
        setError(
          `Es wurden nur ${removed} von ${ids.length} Tag-Blöcken gelöscht.`,
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
        setSyncMessage({ level: "error", text: "Sync fehlgeschlagen — keine Antwort vom Backend." });
      } else if (r.Errors && r.Errors.length > 0) {
        setSyncMessage({ level: "error", text: `Sync fehlgeschlagen: ${r.Errors.join("; ")}` });
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
          text: `Nichts an Personio gesendet — alle ${r.BlocksSkipped} Block/Blöcke übersprungen.`,
        });
      } else {
        setSyncMessage({ level: "info", text: "Keine getaggten Blöcke für diesen Tag." });
      }
    } catch (e) {
      setSyncMessage({ level: "error", text: `Sync fehlgeschlagen: ${String(e)}` });
    } finally {
      setSyncing(false);
      refresh();
    }
  }

  const totalTaggedSec = tagBlocks.reduce((s, b) => s + b.duration_sec, 0);

  // -- Strip geometry helpers -----------------------------------------------

  const viewSpan = Math.max(1, viewEnd - viewStart);

  function pctOfMs(ms: number): number {
    return clampPct((ms - viewStart) / viewSpan);
  }

  function pctFromEvent(
    e: React.MouseEvent | MouseEvent,
    ref: React.RefObject<HTMLDivElement | null>,
  ): number {
    const rect = ref.current?.getBoundingClientRect();
    if (!rect) return 0;
    return clampPct((e.clientX - rect.left) / rect.width);
  }

  function pctRangeToMs(a: number, b: number): MsRange {
    const lo = Math.min(a, b);
    const hi = Math.max(a, b);
    return { start: viewStart + lo * viewSpan, end: viewStart + hi * viewSpan };
  }

  function snapMsToGrid(ms: number, mode: "floor" | "ceil"): number {
    if (granularityMs <= 0) return ms;
    const d = new Date(ms);
    const midnight = new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
    const delta = ms - midnight;
    const remainder = delta % granularityMs;
    if (remainder === 0) return ms;
    if (mode === "floor") return midnight + (delta - remainder);
    return midnight + (delta - remainder + granularityMs);
  }

  function snapRange(r: MsRange): MsRange {
    if (granularityMs <= 0) return r;
    const start = snapMsToGrid(r.start, "floor");
    let end = snapMsToGrid(r.end, "ceil");
    if (end <= start) end = start + granularityMs;
    return { start, end };
  }

  // -- Wheel zoom + pan (active on both strips) -----------------------------

  useEffect(() => {
    const els = [
      tagStripRef.current,
      trackStripRef.current,
      commStripRef.current,
    ].filter((e): e is HTMLDivElement => e !== null);
    if (els.length === 0) return;
    function onWheel(e: WheelEvent) {
      e.preventDefault();
      const target = e.currentTarget as HTMLDivElement;
      const rect = target.getBoundingClientRect();
      const cursorPct = clampPct((e.clientX - rect.left) / rect.width);
      const span = viewEnd - viewStart;
      if (e.shiftKey) {
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
    els.forEach((el) => el.addEventListener("wheel", onWheel, { passive: false }));
    return () => els.forEach((el) => el.removeEventListener("wheel", onWheel));
  }, [viewStart, viewEnd, dayFromMs, dayToMs]);

  function resetZoom() {
    setViewStart(dayFromMs);
    setViewEnd(dayToMs);
  }

  // -- Tag-strip mouse handlers (drag to create manual range) ---------------

  function onTagStripMouseDown(e: React.MouseEvent) {
    if (e.button !== 0) return;
    const pct = pctFromEvent(e, tagStripRef);
    dragStartPctRef.current = pct;
    setDragRange({ a: pct, b: pct });
    setHoverRange(pctRangeToMs(pct, pct));
    setCursorPctX(pct);
    e.preventDefault();
  }

  function onTagStripMouseMove(e: React.MouseEvent) {
    setCursorPctX(pctFromEvent(e, tagStripRef));
  }

  function onTagStripMouseLeave() {
    if (dragStartPctRef.current == null) setCursorPctX(null);
  }

  useEffect(() => {
    function snappedDragPct(startPct: number, endPct: number): { a: number; b: number } {
      if (granularityMs <= 0) return { a: startPct, b: endPct };
      const span = viewEnd - viewStart;
      if (span <= 0) return { a: startPct, b: endPct };
      const r = snapRange(pctRangeToMs(startPct, endPct));
      return {
        a: (r.start - viewStart) / span,
        b: (r.end - viewStart) / span,
      };
    }
    function onMove(e: MouseEvent) {
      if (dragStartPctRef.current == null) return;
      const pct = pctFromEvent(e, tagStripRef);
      const startPct = dragStartPctRef.current;
      setDragRange(snappedDragPct(startPct, pct));
      setHoverRange(snapRange(pctRangeToMs(startPct, pct)));
      setCursorPctX(pct);
    }
    function onUp(e: MouseEvent) {
      if (dragStartPctRef.current == null) return;
      const startPct = dragStartPctRef.current;
      const endPct = pctFromEvent(e, tagStripRef);
      dragStartPctRef.current = null;
      const moved = Math.abs(endPct - startPct) > 0.001;
      const r = snapRange(pctRangeToMs(startPct, endPct));
      if (moved) {
        setSelectedBlockIDs(new Set());
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
  }, [viewStart, viewEnd, granularityMs]);

  // Resize the still-uncommitted selectedRange via edge handles.
  useEffect(() => {
    function onMove(e: MouseEvent) {
      const edge = resizeEdgeRef.current;
      if (edge == null || !selectedRange) return;
      const pct = pctFromEvent(e, tagStripRef);
      let ms = viewStart + pct * (viewEnd - viewStart);
      ms = Math.max(dayFromMs, Math.min(dayToMs, ms));
      let start = selectedRange.start;
      let end = selectedRange.end;
      if (edge === "start") start = ms;
      else end = ms;
      if (start > end) {
        const t = start;
        start = end;
        end = t;
        resizeEdgeRef.current = edge === "start" ? "end" : "start";
      }
      setSelectedRange(snapRange({ start, end }));
    }
    function onUp() {
      resizeEdgeRef.current = null;
    }
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
    return () => {
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedRange, viewStart, viewEnd, dayFromMs, dayToMs, granularityMs]);

  function onResizeHandleDown(e: React.MouseEvent, edge: "start" | "end") {
    e.stopPropagation();
    e.preventDefault();
    resizeEdgeRef.current = edge;
  }

  // -- Existing-block resize ------------------------------------------------

  // Single closed selection — only then resize handles appear. The selected
  // block must be closed (end_time set); we never resize the open one.
  const soloSelectedBlock = useMemo<TagBlock | null>(() => {
    if (selectedRange) return null;
    if (selectedBlockIDs.size !== 1) return null;
    const id = [...selectedBlockIDs][0];
    const b = tagBlocks.find((x) => x.id === id);
    if (!b || !b.end_time) return null;
    return b;
  }, [selectedBlockIDs, selectedRange, tagBlocks]);

  // Compute neighbor fences relative to the original block, then minimum
  // gap to the opposite edge (one granularity step or 1s as a floor).
  function computeBlockFences(b: TagBlock): {
    leftFenceMs: number;
    rightFenceMs: number;
    originalStart: number;
    originalEnd: number;
  } {
    const { start, end } = tagBlockBounds(b);
    let leftFence = dayFromMs;
    let rightFence = dayToMs;
    for (const o of tagBlocks) {
      if (o.id === b.id) continue;
      const ob = tagBlockBounds(o);
      if (ob.end <= start && ob.end > leftFence) leftFence = ob.end;
      if (ob.start >= end && ob.start < rightFence) rightFence = ob.start;
    }
    return { leftFenceMs: leftFence, rightFenceMs: rightFence, originalStart: start, originalEnd: end };
  }

  function onBlockResizeHandleDown(
    e: React.MouseEvent,
    block: TagBlock,
    edge: "start" | "end",
  ) {
    e.stopPropagation();
    e.preventDefault();
    const fences = computeBlockFences(block);
    blockResizeCtxRef.current = { id: block.id, edge, ...fences };
    setBlockResize({ id: block.id, start: fences.originalStart, end: fences.originalEnd });
  }

  useEffect(() => {
    function clampMs(ms: number): number {
      const ctx = blockResizeCtxRef.current;
      if (!ctx) return ms;
      const minGap = Math.max(granularityMs, 1000);
      if (ctx.edge === "start") {
        const upperBound = ctx.originalEnd - minGap;
        return Math.max(ctx.leftFenceMs, Math.min(ms, upperBound));
      }
      const lowerBound = ctx.originalStart + minGap;
      return Math.max(lowerBound, Math.min(ms, ctx.rightFenceMs));
    }
    function onMove(e: MouseEvent) {
      const ctx = blockResizeCtxRef.current;
      if (!ctx) return;
      const pct = pctFromEvent(e, tagStripRef);
      const rawMs = viewStart + pct * (viewEnd - viewStart);
      const clamped = clampMs(rawMs);
      const snapped =
        ctx.edge === "start"
          ? snapMsToGrid(clamped, "floor")
          : snapMsToGrid(clamped, "ceil");
      const finalMs = clampMs(snapped);
      if (ctx.edge === "start") {
        setBlockResize({ id: ctx.id, start: finalMs, end: ctx.originalEnd });
      } else {
        setBlockResize({ id: ctx.id, start: ctx.originalStart, end: finalMs });
      }
    }
    async function onUp() {
      const ctx = blockResizeCtxRef.current;
      if (!ctx) return;
      blockResizeCtxRef.current = null;
      const live = blockResize;
      setBlockResize(null);
      if (!live) return;
      const changed =
        Math.abs(live.start - ctx.originalStart) > 500 ||
        Math.abs(live.end - ctx.originalEnd) > 500;
      if (!changed) return;
      try {
        await api.resizeTagBlock(
          ctx.id,
          new Date(live.start).toISOString(),
          new Date(live.end).toISOString(),
        );
        await refresh();
      } catch (err) {
        setError(String(err));
      }
    }
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
    return () => {
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [viewStart, viewEnd, granularityMs, blockResize]);

  // -- Track strip mouse handlers (read-only hover only) -------------------

  function onTrackStripMouseMove(e: React.MouseEvent) {
    setCursorPctX(pctFromEvent(e, trackStripRef));
  }

  function onTrackStripMouseLeave() {
    if (dragStartPctRef.current == null) setCursorPctX(null);
  }

  // -- Table grouping & filtering ------------------------------------------

  // The timeline renders focused-window tracks and communication-window
  // tracks on separate rails (see "Prozesse" + "Kommunikation" strips). The
  // table merges both so users see one chronological list, with comm rows
  // visually marked by a phone icon.
  const focusedTracks = useMemo<ProcessTrack[]>(
    () => processTracks.filter((t) => !t.is_communication),
    [processTracks],
  );
  const commTracks = useMemo<ProcessTrack[]>(
    () => processTracks.filter((t) => t.is_communication),
    [processTracks],
  );

  const visibleTracks = useMemo<ProcessTrack[]>(() => {
    if (!hoverRange) return processTracks;
    return processTracks.filter((t) => {
      const { start, end } = trackBounds(t);
      return end > hoverRange.start && start < hoverRange.end;
    });
  }, [processTracks, hoverRange]);

  const visibleTrackGroups = useMemo<TrackGroup[]>(
    () => groupTracksForTable(visibleTracks),
    [visibleTracks],
  );

  const hourTicks = useMemo<number[]>(() => {
    const ticks: number[] = [];
    for (let h = 0; h <= 24; h++) {
      const ms = dayFromMs + h * 3600 * 1000;
      if (ms >= viewStart && ms <= viewEnd) ticks.push(ms);
    }
    return ticks;
  }, [dayFromMs, viewStart, viewEnd]);

  const axisLabels = useMemo<string[]>(() => {
    const out: string[] = [];
    for (let i = 0; i < 5; i++) {
      const ms = viewStart + (i / 4) * (viewEnd - viewStart);
      out.push(formatHHMM(new Date(ms).toISOString()));
    }
    return out;
  }, [viewStart, viewEnd]);

  function toggleExpandGroup(g: TrackGroup) {
    const key = g.trackIDs[0];
    setExpandedGroups((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }

  // -- Render --------------------------------------------------------------

  const hasSelection = selectedBlockIDs.size > 0 || selectedRange != null;
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
          onClick={() => setDay((d) => new Date(d.getTime() - 24 * 3600 * 1000))}
          className="rounded bg-surface px-3 py-1 text-sm hover:bg-slate-700"
        >
          ← Vortag
        </button>
        <button
          onClick={() => setDay((d) => new Date(d.getTime() + 24 * 3600 * 1000))}
          className="rounded bg-surface px-3 py-1 text-sm hover:bg-slate-700"
        >
          Folgetag →
        </button>
        <span className="ml-auto text-sm text-slate-400">
          Getaggt: {formatDuration(totalTaggedSec)}
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

      {/* --- Selection / tagging panel ---------------------------------- */}
      <div ref={panelRef}>
        {hasSelection && (
          <div className="space-y-2 rounded bg-surface px-3 py-3">
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-sm text-slate-300">
                {selectedBlockIDs.size > 0 && (
                  <>
                    {selectedBlockIDs.size} Tag-Block/Blöcke markiert
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
                  </span>
                )}
                <span className="ml-1">→</span>
              </span>
              {orderedTags.map((t) => {
                const parent = t.parent_id != null ? tagsByID[t.parent_id] : undefined;
                const fullLabel = parent ? `${parent.name} › ${t.name}` : t.name;
                return (
                  <button
                    key={t.id}
                    onClick={() => applyTag(t.id)}
                    className="rounded px-2 py-1 text-xs text-white hover:opacity-80"
                    style={{ background: t.color ?? "#4f8cff" }}
                    title={fullLabel}
                  >
                    {parent && (
                      <span className="mr-1 text-[10px] text-white/60">
                        {parent.name} ›
                      </span>
                    )}
                    {t.name}
                  </button>
                );
              })}
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
                placeholder="Tätigkeitsbeschreibung — wird beim Taggen mit zugewiesen"
                rows={2}
                className="flex-1 resize-y rounded bg-slate-900/60 px-2 py-1 text-sm text-slate-100 placeholder:text-slate-500"
              />
              <div className="flex flex-col gap-1">
                <button
                  onClick={saveDescriptionOnly}
                  disabled={selectedBlockIDs.size === 0}
                  className="rounded bg-accent px-3 py-1 text-xs text-white hover:bg-accent/80 disabled:opacity-50"
                  title="Beschreibung speichern"
                >
                  Speichern
                </button>
                <button
                  onClick={deleteSelectedBlocks}
                  disabled={selectedBlockIDs.size === 0}
                  className="rounded bg-red-700 px-3 py-1 text-xs text-white hover:bg-red-600 disabled:opacity-50"
                >
                  Löschen
                </button>
              </div>
            </div>
            <p className="text-[11px] text-slate-500">
              Tag wählen erstellt aus dem gezogenen Bereich einen manuellen
              Tag-Block oder ändert das Tagging der markierten Blöcke. Ein
              gezogener Bereich ersetzt überlappende Auto-Tag-Blöcke; eine
              Überschneidung mit einem manuellen Tag-Block wird abgelehnt.
            </p>
          </div>
        )}
      </div>

      {/* --- Tag-block strip (top) + Process-track strip (bottom) -------- */}
      <div className="rounded bg-surface px-3 py-3 space-y-2">
        {/* Axis labels with hover/cursor readout */}
        <div className="relative mb-1 flex justify-between text-[10px] text-slate-500">
          {axisLabels.map((l, i) => (
            <span key={i}>{l}</span>
          ))}
          {hoverRange
            ? (() => {
                const { start, end } = hoverRange;
                const visStart = Math.max(start, viewStart);
                const visEnd = Math.min(end, viewEnd);
                const midPct = pctOfMs((visStart + visEnd) / 2);
                const durSec = Math.round((end - start) / 1000);
                return (
                  <div
                    className="pointer-events-none absolute -top-0.5 z-10 -translate-x-1/2 whitespace-nowrap rounded bg-slate-900/95 px-2 py-0.5 font-medium text-slate-100 shadow ring-1 ring-slate-700"
                    style={{ left: `${Math.max(0.06, Math.min(0.94, midPct)) * 100}%` }}
                  >
                    {formatHHMM(new Date(start).toISOString())}–
                    {formatHHMM(new Date(end).toISOString())}
                    {end - start > 500 && (
                      <span className="ml-1 text-slate-400">
                        · {formatDuration(durSec)}
                      </span>
                    )}
                  </div>
                );
              })()
            : cursorPctX != null &&
              (() => {
                const ms = viewStart + cursorPctX * (viewEnd - viewStart);
                return (
                  <div
                    className="pointer-events-none absolute -top-0.5 z-10 -translate-x-1/2 whitespace-nowrap rounded bg-slate-900/95 px-2 py-0.5 font-medium text-slate-100 shadow ring-1 ring-slate-700"
                    style={{ left: `${Math.max(0.04, Math.min(0.96, cursorPctX)) * 100}%` }}
                  >
                    {formatHHMM(new Date(ms).toISOString())}
                  </div>
                );
              })()}
        </div>

        {/* Top strip: Tag blocks (drag to create manual range tag) */}
        <div className="text-[10px] uppercase tracking-wide text-slate-500">Tags</div>
        <div
          ref={tagStripRef}
          onMouseDown={onTagStripMouseDown}
          onMouseMove={onTagStripMouseMove}
          onMouseLeave={onTagStripMouseLeave}
          onDoubleClick={resetZoom}
          className="relative h-10 cursor-crosshair select-none rounded bg-slate-900/60"
          title="Bereich ziehen, um manuell zu taggen · Mausrad: zoom · Shift+Mausrad: schwenken · Doppelklick: Reset"
        >
          {hourTicks.map((ms) => (
            <div
              key={ms}
              className="absolute inset-y-0 w-px bg-slate-700/40"
              style={{ left: `${pctOfMs(ms) * 100}%` }}
            />
          ))}

          {tagBlocks.map((b) => {
            const native = tagBlockBounds(b);
            const live =
              blockResize && blockResize.id === b.id
                ? { start: blockResize.start, end: blockResize.end }
                : native;
            const { start, end } = live;
            if (end <= viewStart || start >= viewEnd) return null;
            const left = pctOfMs(start) * 100;
            const width = (pctOfMs(end) - pctOfMs(start)) * 100;
            const tag = tagsByID[b.tag_id];
            const bg = tag?.color ?? UNTAGGED_COLOR;
            const isSelected = selectedBlockIDs.has(b.id);
            const isResizing = blockResize?.id === b.id;
            const showHandles =
              soloSelectedBlock?.id === b.id && !dragRange && !selectedRange;
            return (
              <div
                key={`tb-${b.id}`}
                onClick={(e) => {
                  e.stopPropagation();
                  if (isResizing) return;
                  toggleBlock(b.id, e.shiftKey);
                }}
                onMouseEnter={() => setHoverRange({ start, end })}
                onMouseLeave={() => setHoverRange(null)}
                className={`absolute top-1 bottom-1 cursor-pointer rounded transition-[outline] ${
                  isSelected
                    ? "outline outline-2 outline-white"
                    : "hover:outline hover:outline-1 hover:outline-white/70"
                } ${b.is_manual ? "" : "border border-dashed border-white/40"}`}
                style={{
                  left: `${left}%`,
                  width: `${Math.max(width, 0.2)}%`,
                  background: bg,
                }}
                title={
                  `${formatHHMM(new Date(start).toISOString())}–${formatHHMM(new Date(end).toISOString())} · ${formatDuration(Math.round((end - start) / 1000))}\n` +
                  `${tag ? tag.name : "?"} · ${b.is_manual ? "manuell" : "auto"}` +
                  (b.description ? `\n${b.description}` : "")
                }
              >
                {showHandles && (
                  <>
                    <div
                      onMouseDown={(e) => onBlockResizeHandleDown(e, b, "start")}
                      className="absolute inset-y-0 left-0 z-20 w-1.5 -translate-x-1/2 cursor-ew-resize rounded-l bg-white/90 hover:bg-white"
                      title="Block-Anfang ziehen — bis zum Nachbarn"
                    />
                    <div
                      onMouseDown={(e) => onBlockResizeHandleDown(e, b, "end")}
                      className="absolute inset-y-0 right-0 z-20 w-1.5 translate-x-1/2 cursor-ew-resize rounded-r bg-white/90 hover:bg-white"
                      title="Block-Ende ziehen — bis zum Nachbarn"
                    />
                  </>
                )}
              </div>
            );
          })}

          {dragRange && (
            <div
              className="pointer-events-none absolute inset-y-0 rounded bg-accent/30 outline outline-1 outline-accent"
              style={{
                left: `${Math.min(dragRange.a, dragRange.b) * 100}%`,
                width: `${Math.abs(dragRange.b - dragRange.a) * 100}%`,
              }}
            />
          )}

          {!dragRange && selectedRange && (
            <>
              <div
                className="pointer-events-none absolute inset-y-0 rounded outline outline-1 outline-accent/70"
                style={{
                  left: `${pctOfMs(selectedRange.start) * 100}%`,
                  width: `${(pctOfMs(selectedRange.end) - pctOfMs(selectedRange.start)) * 100}%`,
                }}
              />
              <div
                onMouseDown={(e) => onResizeHandleDown(e, "start")}
                className="absolute inset-y-0 z-20 w-1.5 cursor-ew-resize rounded-l bg-accent hover:bg-white"
                style={{ left: `calc(${pctOfMs(selectedRange.start) * 100}% - 3px)` }}
                title="Bereich-Anfang ziehen"
              />
              <div
                onMouseDown={(e) => onResizeHandleDown(e, "end")}
                className="absolute inset-y-0 z-20 w-1.5 cursor-ew-resize rounded-r bg-accent hover:bg-white"
                style={{ left: `calc(${pctOfMs(selectedRange.end) * 100}% - 3px)` }}
                title="Bereich-Ende ziehen"
              />
            </>
          )}
        </div>

        {/* Middle strip: focused-window process tracks (read-only) */}
        <div className="text-[10px] uppercase tracking-wide text-slate-500">Prozesse</div>
        <div
          ref={trackStripRef}
          onMouseMove={onTrackStripMouseMove}
          onMouseLeave={onTrackStripMouseLeave}
          onDoubleClick={resetZoom}
          className="relative h-8 select-none rounded bg-slate-900/60"
          title="Aufgezeichnete Prozesse · Mausrad zoomt, Shift+Mausrad schwenkt"
        >
          {hourTicks.map((ms) => (
            <div
              key={`pt-tick-${ms}`}
              className="absolute inset-y-0 w-px bg-slate-700/40"
              style={{ left: `${pctOfMs(ms) * 100}%` }}
            />
          ))}

          {focusedTracks.map((t) => {
            const { start, end } = trackBounds(t);
            if (end <= viewStart || start >= viewEnd) return null;
            const left = pctOfMs(start) * 100;
            const width = (pctOfMs(end) - pctOfMs(start)) * 100;
            const bg = t.is_idle ? "#1f2937" : colorFromName(t.process_name || "?");
            return (
              <div
                key={`pt-${t.id}`}
                onMouseEnter={() => setHoverRange({ start, end })}
                onMouseLeave={() => setHoverRange(null)}
                className={`absolute inset-y-1 rounded ${t.is_idle ? "opacity-50" : "opacity-80"}`}
                style={{
                  left: `${left}%`,
                  width: `${Math.max(width, 0.2)}%`,
                  background: bg,
                }}
                title={
                  `${formatHHMM(new Date(start).toISOString())}–${formatHHMM(new Date(end).toISOString())} · ${formatDuration(t.duration_sec)}\n` +
                  `${t.is_idle ? "Idle" : t.process_name}` +
                  (t.window_title ? `\n${t.window_title}` : "")
                }
              />
            );
          })}
        </div>

        {/* Bottom strip: communication-window tracks (Teams etc., parallel
            to focus). Same time axis, marked with a phone glyph in the rail
            label and tooltip. */}
        <div className="text-[10px] uppercase tracking-wide text-slate-500">
          📞 Kommunikation
        </div>
        <div
          ref={commStripRef}
          onMouseMove={(e) => setCursorPctX(pctFromEvent(e, commStripRef))}
          onMouseLeave={() => {
            if (dragStartPctRef.current == null) setCursorPctX(null);
          }}
          onDoubleClick={resetZoom}
          className="relative h-8 select-none rounded bg-slate-900/60"
          title="Kommunikations-Prozesse (Teams, Zoom …) · Mausrad zoomt, Shift+Mausrad schwenkt"
        >
          {hourTicks.map((ms) => (
            <div
              key={`comm-tick-${ms}`}
              className="absolute inset-y-0 w-px bg-slate-700/40"
              style={{ left: `${pctOfMs(ms) * 100}%` }}
            />
          ))}

          {commTracks.map((t) => {
            const { start, end } = trackBounds(t);
            if (end <= viewStart || start >= viewEnd) return null;
            const left = pctOfMs(start) * 100;
            const width = (pctOfMs(end) - pctOfMs(start)) * 100;
            const bg = colorFromName(t.process_name || "?");
            return (
              <div
                key={`comm-${t.id}`}
                onMouseEnter={() => setHoverRange({ start, end })}
                onMouseLeave={() => setHoverRange(null)}
                className="absolute inset-y-1 flex items-center overflow-hidden rounded opacity-90 ring-1 ring-emerald-300/60"
                style={{
                  left: `${left}%`,
                  width: `${Math.max(width, 0.2)}%`,
                  background: bg,
                }}
                title={
                  `📞 ${formatHHMM(new Date(start).toISOString())}–${formatHHMM(new Date(end).toISOString())} · ${formatDuration(t.duration_sec)}\n` +
                  `${t.process_name}` +
                  (t.window_title ? `\n${t.window_title}` : "")
                }
              >
                {width > 1.5 && (
                  <span className="ml-1 text-[10px] leading-none">📞</span>
                )}
              </div>
            );
          })}
          {commTracks.length === 0 && (
            <div className="pointer-events-none absolute inset-0 flex items-center justify-center text-[10px] text-slate-600">
              Keine Kommunikations-Aktivität an diesem Tag
            </div>
          )}
        </div>

        <div className="mt-1 flex items-center justify-between text-[11px] text-slate-500">
          <span>
            Oben: Tag-Blöcke (manuell + auto). Mitte: Fokus-Prozesse. Unten:
            Kommunikations-Prozesse (parallel zum Fokus). Mausrad zoomt,
            Shift+Mausrad schwenkt, Doppelklick setzt zurück.
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

      {/* --- Tag-block table (left) + Process-track table (right) ------- */}
      <div className="flex gap-4">
      <div className="w-2/5 min-w-0">
        <div className="mb-1 text-[11px] uppercase tracking-wide text-slate-500">
          Tag-Blöcke
        </div>
        <ul className="max-h-[65vh] divide-y divide-slate-700 overflow-y-auto rounded bg-surface">
          {tagBlocks.length === 0 && (
            <li className="px-3 py-6 text-center text-sm text-slate-400">
              Keine Tag-Blöcke an diesem Tag.
            </li>
          )}
          {tagBlocks.map((b) => {
            const tag = tagsByID[b.tag_id];
            const parentTag = tag?.parent_id != null ? tagsByID[tag.parent_id] : undefined;
            const isSel = selectedBlockIDs.has(b.id);
            const { start, end } = tagBlockBounds(b);
            const startISO = new Date(start).toISOString();
            const endISO = new Date(end).toISOString();
            return (
              <li
                key={`tb-row-${b.id}`}
                onClick={(e) => toggleBlock(b.id, e.shiftKey)}
                className={`cursor-pointer px-3 py-2 text-sm transition-colors ${
                  isSel ? "bg-accent/20" : "hover:bg-slate-700/40"
                }`}
              >
                <div className="flex items-center gap-3">
                  <span className="w-24 font-mono text-xs text-slate-400">
                    {formatHHMM(startISO)}–{formatHHMM(endISO)}
                  </span>
                  <span className="w-16 text-xs text-slate-500">
                    {formatDuration(b.duration_sec)}
                  </span>
                  <span className="w-20 text-xs text-slate-500">
                    {b.is_manual ? "manuell" : "auto"}
                  </span>
                  {b.description && (
                    <span className="flex-1 truncate text-xs italic text-slate-500" title={b.description}>
                      📝 {b.description}
                    </span>
                  )}
                  {!b.description && <span className="flex-1" />}
                  {parentTag && (
                    <span
                      className="rounded px-2 py-0.5 text-xs"
                      style={{ background: parentTag.color ?? "#4f8cff" }}
                    >
                      {parentTag.name}
                    </span>
                  )}
                  {tag && (
                    <span
                      className="rounded px-2 py-0.5 text-xs"
                      style={{ background: tag.color ?? "#4f8cff" }}
                    >
                      {tag.name}
                    </span>
                  )}
                </div>
              </li>
            );
          })}
        </ul>
      </div>

      {/* --- Process-track table (right column) ------------------------- */}
      <div className="w-3/5 min-w-0">
        <div className="mb-1 text-[11px] uppercase tracking-wide text-slate-500">
          Prozesse {hoverRange && "(im markierten Zeitraum)"}
        </div>
        <ul className="max-h-[65vh] divide-y divide-slate-700 overflow-y-auto rounded bg-surface">
          {visibleTrackGroups.length === 0 && (
            <li className="px-3 py-6 text-center text-sm text-slate-400">
              {hoverRange ? "Keine Prozesse im markierten Zeitraum." : "Keine Prozessdaten."}
            </li>
          )}
          {visibleTrackGroups.map((g) => {
            const expandable = g.trackIDs.length > 1;
            const expanded = expandable && expandedGroups.has(g.trackIDs[0]);
            const startISO = new Date(g.startMs).toISOString();
            const endISO = new Date(g.endMs).toISOString();
            return (
              <Fragment key={`${g.isCommunication ? "c" : "p"}-${g.trackIDs[0]}`}>
                <li
                  className={`px-3 py-2 text-sm ${g.isIdle ? "opacity-50" : ""}`}
                  title={g.isCommunication ? "Kommunikations-Prozess (parallel zum Fokus erfasst)" : undefined}
                >
                  <div className="flex items-center gap-3">
                    <button
                      type="button"
                      onClick={() => {
                        if (expandable) toggleExpandGroup(g);
                      }}
                      aria-label={expanded ? "Einklappen" : "Ausklappen"}
                      className={`w-3 text-[10px] text-slate-500 ${
                        expandable ? "cursor-pointer hover:text-slate-300" : "invisible"
                      }`}
                      tabIndex={expandable ? 0 : -1}
                    >
                      {expanded ? "▾" : "▸"}
                    </button>
                    <span className="w-24 font-mono text-xs text-slate-400">
                      {formatHHMM(startISO)}–{formatHHMM(endISO)}
                    </span>
                    <span className="w-16 text-xs text-slate-500">
                      {formatDuration(g.durationSec)}
                    </span>
                    <span className="w-40 truncate text-slate-300">
                      {g.isCommunication && (
                        <span className="mr-1" aria-hidden>
                          📞
                        </span>
                      )}
                      {g.processName || "—"}
                    </span>
                    <span className="flex-1 truncate text-slate-400">{g.windowTitle}</span>
                    {expandable && (
                      <span className="text-[10px] text-slate-500">×{g.trackIDs.length}</span>
                    )}
                  </div>
                </li>
                {expanded &&
                  g.members.map((m) => {
                    const mb = trackBounds(m);
                    return (
                      <li key={`m-${m.id}`} className="px-3 py-1 pl-12 text-xs bg-slate-900/40">
                        <div className="flex items-center gap-3">
                          <span className="w-24 font-mono text-[11px] text-slate-500">
                            {formatHHMM(new Date(mb.start).toISOString())}–
                            {formatHHMM(new Date(mb.end).toISOString())}
                          </span>
                          <span className="w-16 text-[11px] text-slate-600">
                            {formatDuration(m.duration_sec)}
                          </span>
                          <span className="flex-1 truncate text-slate-400">
                            {m.window_title || "—"}
                          </span>
                        </div>
                      </li>
                    );
                  })}
              </Fragment>
            );
          })}
        </ul>
      </div>
      </div>
    </div>
  );
}
