import { format, formatDuration as fnsFormatDuration, intervalToDuration } from "date-fns";

export function startOfDayUTCISO(d: Date): string {
  const utc = new Date(Date.UTC(d.getFullYear(), d.getMonth(), d.getDate()));
  return utc.toISOString();
}

export function formatHHMM(iso: string): string {
  return format(new Date(iso), "HH:mm");
}

export function formatDuration(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  const dur = intervalToDuration({ start: 0, end: seconds * 1000 });
  return fnsFormatDuration(dur, { format: ["hours", "minutes"] }) || "<1m";
}

export function dateInputValue(d: Date): string {
  return format(d, "yyyy-MM-dd");
}

export function fromDateInput(s: string): Date {
  // s is yyyy-MM-dd in local time; promote to start of UTC day.
  const [y, m, d] = s.split("-").map(Number);
  return new Date(Date.UTC(y, m - 1, d));
}
