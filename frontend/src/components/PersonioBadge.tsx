import { useCallback, useEffect, useState } from "react";
import { api } from "../api";
import type { PersonioStatus } from "../types";

// PersonioBadge sits in the top header and reflects whether the Personio
// session cookies still authenticate. A green dot means "logged in", amber is
// "logged in but unchecked / cookie missing tenant", red is "expired or never
// logged in". Clicking the badge in any non-green state launches the
// interactive CDP login. Background poll runs every 60s so a session that
// expires mid-day surfaces without a manual refresh.
const POLL_INTERVAL_MS = 60_000;

type State = "loading" | "ok" | "warn" | "error";

function classify(s: PersonioStatus | null): State {
  if (!s) return "loading";
  if (!s.has_session) return "error";
  if (!s.valid) return "error";
  return "ok";
}

function colorFor(state: State): string {
  switch (state) {
    case "ok":
      return "bg-emerald-500";
    case "warn":
      return "bg-amber-500";
    case "error":
      return "bg-red-500";
    default:
      return "bg-slate-500";
  }
}

function labelFor(state: State, s: PersonioStatus | null): string {
  if (!s) return "Personio prüfen…";
  if (state === "ok") return `Personio: angemeldet${s.tenant ? ` · ${s.tenant}` : ""}`;
  if (state === "error" && !s.has_session) return "Personio: nicht angemeldet";
  if (state === "error") return `Personio: ${s.reason || "Session abgelaufen"}`;
  return "Personio";
}

export default function PersonioBadge() {
  const [status, setStatus] = useState<PersonioStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      const s = await api.personioCheck();
      setStatus(s);
    } catch (e) {
      // Probe failed entirely (offline?) — fall back to local-only status.
      try {
        const s = await api.personioStatus();
        setStatus(s);
      } catch {
        setStatus(null);
      }
      setError(String(e));
    }
  }, []);

  useEffect(() => {
    refresh();
    const t = setInterval(refresh, POLL_INTERVAL_MS);
    return () => clearInterval(t);
  }, [refresh]);

  const state = classify(status);
  const label = labelFor(state, status);

  async function onClick() {
    if (busy) return;
    if (state === "ok") {
      // Already valid — just re-probe so the user gets immediate feedback.
      setBusy(true);
      try {
        await refresh();
      } finally {
        setBusy(false);
      }
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await api.personioLogin();
      await refresh();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <button
      type="button"
      onClick={onClick}
      disabled={busy}
      title={
        error
          ? `${label} — Klick: erneut anmelden\n${error}`
          : state === "ok"
            ? `${label} — Klick: Status erneut prüfen`
            : `${label} — Klick: bei Personio anmelden`
      }
      className="ml-auto flex items-center gap-2 rounded bg-slate-800/60 px-3 py-1 text-xs text-slate-200 hover:bg-slate-700 disabled:opacity-60"
    >
      <span
        className={`inline-block h-2 w-2 rounded-full ${colorFor(state)} ${
          busy ? "animate-pulse" : ""
        }`}
      />
      <span className="truncate max-w-[18rem]">
        {busy
          ? state === "ok"
            ? "Prüfe…"
            : "Anmeldung läuft…"
          : label}
      </span>
    </button>
  );
}
