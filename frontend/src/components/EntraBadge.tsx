import { useCallback, useEffect, useState } from "react";
import { api } from "../api";
import type { EntraStatus } from "../types";

// EntraBadge sits in the top header next to the Personio badge and mirrors its
// behaviour for the optional Microsoft Entra ID sign-in. It renders nothing
// until Entra is configured (client_id + tenant_id set); a tenant that never
// enables the feature sees the header exactly as it was before Entra existed.
//
// Green means "signed in and the cached token still acquires silently", red is
// "configured but not signed in / re-login required", slate is the brief
// loading gap. Clicking a red badge launches the interactive browser login;
// clicking a green one just re-checks. A 60s background poll surfaces a session
// that lapses mid-day (CA-policy drift, password reset) without a manual
// refresh — EntraProbe is cache-first, so it only touches the network once the
// access token has actually expired.
const POLL_INTERVAL_MS = 60_000;

type State = "loading" | "ok" | "error";

function classify(s: EntraStatus | null): State {
  if (!s) return "loading";
  if (!s.has_account) return "error";
  if (!s.valid) return "error";
  return "ok";
}

function colorFor(state: State): string {
  switch (state) {
    case "ok":
      return "bg-emerald-500";
    case "error":
      return "bg-red-500";
    default:
      return "bg-slate-500";
  }
}

function labelFor(state: State, s: EntraStatus | null): string {
  if (!s) return "Entra ID prüfen…";
  if (state === "ok")
    return `Entra ID: angemeldet${s.username ? ` · ${s.username}` : ""}`;
  if (state === "error" && !s.has_account) return "Entra ID: nicht angemeldet";
  if (state === "error")
    return `Entra ID: ${s.reason || "Anmeldung erforderlich"}`;
  return "Entra ID";
}

export default function EntraBadge() {
  const [status, setStatus] = useState<EntraStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      const s = await api.entraProbe();
      setStatus(s);
    } catch (e) {
      // Probe call failed entirely (running outside Wails?) — fall back to the
      // cheap local cache read so the badge can still classify.
      try {
        const s = await api.entraStatus();
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

  // Feature dormant or first probe still in flight: render nothing. Waiting for
  // the first status (rather than showing a loading chip) keeps the header
  // unchanged for the common case where Entra is never configured.
  if (!status || !status.configured) return null;

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
      await api.entraLogin();
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
            : `${label} — Klick: bei Entra ID anmelden`
      }
      className="flex items-center gap-2 rounded bg-slate-800/60 px-3 py-1 text-xs text-slate-200 hover:bg-slate-700 disabled:opacity-60"
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
