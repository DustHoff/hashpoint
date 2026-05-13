import { useEffect, useMemo, useState } from "react";
import {
  eachDayOfInterval,
  endOfMonth,
  endOfWeek,
  format,
  isSameMonth,
  startOfMonth,
  startOfWeek,
} from "date-fns";
import { de } from "date-fns/locale";
import { api } from "../api";
import type { Tag, TagBlock } from "../types";

interface Props {
  month: Date;
  tags: Tag[];
  // Canonical English short names ("Mon" .. "Sun") of the user's configured
  // working weekdays. Cells outside this set are rendered muted to make the
  // working week stand out at a glance.
  workDays: string[];
  onSelectDay: (day: Date) => void;
}

// jsDayToWorkDayKey converts JavaScript's getDay() index (Sunday=0) to the
// canonical short name used in the config / work_days array.
const JS_DAY_TO_KEY: ReadonlyArray<string> = [
  "Sun",
  "Mon",
  "Tue",
  "Wed",
  "Thu",
  "Fri",
  "Sat",
];

type SyncState = "none-blocks" | "all-synced" | "partial" | "none-synced";

interface DaySummary {
  totalSec: number;
  perTagSec: Map<number, number>;
  syncState: SyncState;
}

// Compact "Hh Mm" formatter for tight cells; the project-wide formatDuration
// expands to "6 hours 30 minutes" which is too wide for a calendar cell.
function fmtHM(sec: number): string {
  if (sec <= 0) return "0m";
  if (sec < 60) return `${sec}s`;
  const h = Math.floor(sec / 3600);
  const m = Math.round((sec % 3600) / 60);
  if (h === 0) return `${m}m`;
  if (m === 0) return `${h}h`;
  return `${h}h ${m}m`;
}

// Day key: yyyy-MM-dd in local time. Aligns with how blockDateKey extracts
// the UTC date from start_time, matching the existing day-view convention
// where tagBlocksByDay queries a UTC day labelled with the local calendar
// date components.
function dateKey(d: Date): string {
  return format(d, "yyyy-MM-dd");
}

function blockDateKey(b: TagBlock): string {
  return b.start_time.slice(0, 10);
}

function gridRange(month: Date): { start: Date; end: Date } {
  const monthStart = startOfMonth(month);
  const monthEnd = endOfMonth(month);
  const start = startOfWeek(monthStart, { weekStartsOn: 1 });
  const end = endOfWeek(monthEnd, { weekStartsOn: 1 });
  return { start, end };
}

export default function MonthCalendar({
  month,
  tags,
  workDays,
  onSelectDay,
}: Props) {
  const workDaySet = useMemo(() => new Set(workDays), [workDays]);
  const [blocks, setBlocks] = useState<TagBlock[]>([]);
  const [error, setError] = useState<string | null>(null);

  const tagsByID = useMemo(() => {
    const m: Record<number, Tag> = {};
    tags.forEach((t) => (m[t.id] = t));
    return m;
  }, [tags]);

  const { start: gridStart, end: gridEnd } = useMemo(
    () => gridRange(month),
    [month],
  );

  const days = useMemo(
    () => eachDayOfInterval({ start: gridStart, end: gridEnd }),
    [gridStart, gridEnd],
  );

  useEffect(() => {
    let cancelled = false;
    async function load() {
      try {
        const fromUTC = new Date(
          Date.UTC(
            gridStart.getFullYear(),
            gridStart.getMonth(),
            gridStart.getDate(),
          ),
        );
        const toUTC = new Date(
          Date.UTC(
            gridEnd.getFullYear(),
            gridEnd.getMonth(),
            gridEnd.getDate() + 1,
          ),
        );
        const data = await api.tagBlocksBetween(
          fromUTC.toISOString(),
          toUTC.toISOString(),
        );
        if (!cancelled) {
          setBlocks(data ?? []);
          setError(null);
        }
      } catch (e) {
        if (!cancelled) setError(String(e));
      }
    }
    load();
    return () => {
      cancelled = true;
    };
  }, [gridStart, gridEnd]);

  const summaryByDay = useMemo(() => {
    type Acc = DaySummary & { closedTotal: number; syncedTotal: number };
    const m = new Map<string, Acc>();
    for (const b of blocks) {
      const key = blockDateKey(b);
      let s = m.get(key);
      if (!s) {
        s = {
          totalSec: 0,
          perTagSec: new Map(),
          syncState: "none-blocks",
          closedTotal: 0,
          syncedTotal: 0,
        };
        m.set(key, s);
      }
      s.totalSec += b.duration_sec;
      s.perTagSec.set(
        b.tag_id,
        (s.perTagSec.get(b.tag_id) ?? 0) + b.duration_sec,
      );
      // Open blocks (no end_time) can't be synced yet — exclude from the
      // sync-state denominator so an in-progress block doesn't make today
      // look "partial" forever.
      if (b.end_time) {
        s.closedTotal++;
        if (b.synced_at) s.syncedTotal++;
      }
    }
    const out = new Map<string, DaySummary>();
    for (const [k, s] of m.entries()) {
      let syncState: SyncState = "none-blocks";
      if (s.closedTotal > 0) {
        if (s.syncedTotal === s.closedTotal) syncState = "all-synced";
        else if (s.syncedTotal === 0) syncState = "none-synced";
        else syncState = "partial";
      }
      out.set(k, {
        totalSec: s.totalSec,
        perTagSec: s.perTagSec,
        syncState,
      });
    }
    return out;
  }, [blocks]);

  const monthTotalSec = useMemo(() => {
    let total = 0;
    for (const d of days) {
      if (!isSameMonth(d, month)) continue;
      total += summaryByDay.get(dateKey(d))?.totalSec ?? 0;
    }
    return total;
  }, [days, month, summaryByDay]);

  const todayKey = useMemo(() => dateKey(new Date()), []);

  return (
    <div className="space-y-3">
      {monthTotalSec > 0 && (
        <div className="text-sm text-slate-400">
          Getaggt im Monat: {fmtHM(monthTotalSec)}
        </div>
      )}

      {error && (
        <div className="rounded bg-red-900/40 px-3 py-2 text-sm text-red-200">
          {error}
        </div>
      )}

      <div className="grid grid-cols-7 gap-px overflow-hidden rounded bg-slate-700">
        {(
          [
            ["Mo", "Mon"],
            ["Di", "Tue"],
            ["Mi", "Wed"],
            ["Do", "Thu"],
            ["Fr", "Fri"],
            ["Sa", "Sat"],
            ["So", "Sun"],
          ] as const
        ).map(([label, key]) => {
          const isWork = workDaySet.has(key);
          return (
            <div
              key={key}
              className={`bg-surface px-2 py-1 text-[11px] uppercase tracking-wide ${
                isWork ? "text-slate-400" : "text-slate-600"
              }`}
            >
              {label}
            </div>
          );
        })}
        {days.map((d) => {
          const key = dateKey(d);
          const isWork = workDaySet.has(JS_DAY_TO_KEY[d.getDay()]);
          return (
            <DayCell
              key={key}
              day={d}
              inMonth={isSameMonth(d, month)}
              isToday={key === todayKey}
              isWorkDay={isWork}
              summary={summaryByDay.get(key)}
              tagsByID={tagsByID}
              onClick={() => onSelectDay(d)}
            />
          );
        })}
      </div>

      <div className="flex flex-wrap items-center gap-3 text-[11px] text-slate-500">
        <span className="font-medium text-slate-400">Sync-Status:</span>
        <span className="inline-flex items-center gap-1">
          <span className="text-emerald-400">✓</span> alle gesynct
        </span>
        <span className="inline-flex items-center gap-1">
          <span className="text-amber-300">◐</span> teilweise
        </span>
        <span className="inline-flex items-center gap-1">
          <span className="text-red-400">⊘</span> nichts gesynct
        </span>
        <span className="ml-auto">Klick auf Tag öffnet die Tagesansicht.</span>
      </div>
    </div>
  );
}

interface DayCellProps {
  day: Date;
  inMonth: boolean;
  isToday: boolean;
  // Whether this cell falls on one of the user's configured working
  // weekdays. Non-workdays are rendered with a dimmer background and muted
  // text so the working week stands out at a glance. Sync badges, totals,
  // and the tag-segments bar still render — the cell remains clickable and
  // informative for any blocks that did get tracked on a non-workday.
  isWorkDay: boolean;
  summary: DaySummary | undefined;
  tagsByID: Record<number, Tag>;
  onClick: () => void;
}

interface SyncBadge {
  icon: string;
  className: string;
  title: string;
}

function syncBadge(state: SyncState | undefined): SyncBadge | null {
  switch (state) {
    case "all-synced":
      return {
        icon: "✓",
        className: "text-emerald-400",
        title: "Alle Tag-Blöcke gesynct",
      };
    case "partial":
      return {
        icon: "◐",
        className: "text-amber-300",
        title: "Tag-Blöcke teilweise gesynct",
      };
    case "none-synced":
      return {
        icon: "⊘",
        className: "text-red-400",
        title: "Tag-Blöcke noch nicht gesynct",
      };
    default:
      return null;
  }
}

function DayCell({
  day,
  inMonth,
  isToday,
  isWorkDay,
  summary,
  tagsByID,
  onClick,
}: DayCellProps) {
  const sync = syncBadge(summary?.syncState);
  const segments =
    summary && summary.totalSec > 0
      ? [...summary.perTagSec.entries()]
          .sort((a, b) => b[1] - a[1])
          .map(([tagID, sec]) => {
            const tag = tagsByID[tagID];
            return {
              tagID,
              sec,
              pct: (sec / summary.totalSec) * 100,
              color: tag?.color ?? "#475569",
              label: tag?.name ?? "?",
            };
          })
      : [];

  const dayLabel = format(day, "EEEE, d. MMMM yyyy", { locale: de });
  const tooltip = summary
    ? `${dayLabel} · ${fmtHM(summary.totalSec)}`
    : dayLabel;

  return (
    <button
      type="button"
      onClick={onClick}
      title={tooltip}
      className={`relative flex min-h-[5rem] flex-col gap-1 px-2 py-1.5 text-left text-xs transition-colors hover:bg-slate-700/40 ${
        inMonth
          ? isWorkDay
            ? "bg-surface"
            : "bg-surface/60"
          : isWorkDay
            ? "bg-surface/40"
            : "bg-surface/20"
      } ${isToday ? "ring-2 ring-accent ring-inset" : ""}`}
    >
      <div className="flex items-center justify-between">
        <span
          className={`font-medium ${
            inMonth
              ? isWorkDay
                ? "text-slate-100"
                : "text-slate-500"
              : "text-slate-500"
          } ${isToday ? "text-accent" : ""}`}
        >
          {format(day, "d")}
        </span>
        {sync && (
          <span
            className={`text-sm leading-none ${sync.className}`}
            title={sync.title}
          >
            {sync.icon}
          </span>
        )}
      </div>
      {summary && summary.totalSec > 0 && (
        <>
          <span
            className={`text-[11px] tabular-nums ${
              inMonth ? "text-slate-300" : "text-slate-500"
            }`}
          >
            {fmtHM(summary.totalSec)}
          </span>
          <div className="mt-auto flex h-1.5 w-full overflow-hidden rounded bg-slate-900/40">
            {segments.map((s, i) => (
              <span
                key={`${s.tagID}-${i}`}
                style={{ width: `${s.pct}%`, background: s.color }}
                title={`${s.label} · ${fmtHM(s.sec)}`}
              />
            ))}
          </div>
        </>
      )}
    </button>
  );
}
