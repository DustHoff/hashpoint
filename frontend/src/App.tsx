import { useEffect, useState } from "react";
import { api } from "./api";
import Timeline from "./components/Timeline";
import TagManager from "./components/TagManager";
import RuleManager from "./components/RuleManager";
import Settings from "./components/Settings";
import About from "./components/About";
import Help from "./components/Help";
import PersonioBadge from "./components/PersonioBadge";
import QuickTagPicker from "./components/QuickTagPicker";
import SyncConflictModal from "./components/SyncConflictModal";
import type { SyncPreflight } from "./types";

type Tab = "timeline" | "tags" | "rules" | "settings" | "help" | "about";

const tabs: { id: Tab; label: string }[] = [
  { id: "timeline", label: "Zeitachse" },
  { id: "tags", label: "Tags" },
  { id: "rules", label: "Auto-Tagging" },
  { id: "settings", label: "Einstellungen" },
  { id: "help", label: "Hilfe" },
  { id: "about", label: "Über" },
];

interface StartupSyncEvent {
  status: "ok" | "partial" | "failed";
  day?: string;
  periods?: number;
  blocks_processed?: number;
  blocks_skipped?: number;
  errors?: string[];
  error_message?: string;
}

interface StartupSyncBanner {
  level: "success" | "info" | "error";
  text: string;
}

function describeStartupSync(ev: StartupSyncEvent): StartupSyncBanner {
  const day = ev.day ?? "";
  switch (ev.status) {
    case "ok": {
      const periods = ev.periods ?? 0;
      const blocks = ev.blocks_processed ?? 0;
      const skipped = ev.blocks_skipped ?? 0;
      return {
        level: "success",
        text: `Auto-Sync für ${day}: ${periods} Periode(n), ${blocks} Block/Blöcke gebucht${
          skipped > 0 ? ` (${skipped} übersprungen)` : ""
        }.`,
      };
    }
    case "partial": {
      const errors = ev.errors ?? [];
      return {
        level: "error",
        text: `Auto-Sync für ${day} mit Fehlern: ${errors.join("; ")}`,
      };
    }
    case "failed":
    default:
      return {
        level: "error",
        text: day
          ? `Auto-Sync für ${day} fehlgeschlagen: ${ev.error_message ?? "unbekannter Fehler"}`
          : `Auto-Sync fehlgeschlagen: ${ev.error_message ?? "unbekannter Fehler"}`,
      };
  }
}

export default function App() {
  const [tab, setTab] = useState<Tab>("timeline");
  const [quickTagOpen, setQuickTagOpen] = useState(false);
  const [startupSync, setStartupSync] = useState<StartupSyncBanner | null>(
    null,
  );
  const [startupConflict, setStartupConflict] = useState<SyncPreflight | null>(
    null,
  );

  useEffect(() => {
    const offOpen = api.onEvent("quick-tag-picker:open", () =>
      setQuickTagOpen(true),
    );
    const offClose = api.onEvent("quick-tag-picker:close", () =>
      setQuickTagOpen(false),
    );
    const offHelp = api.onEvent("help:open", () => setTab("help"));
    const offSync = api.onEventPayload<StartupSyncEvent>(
      "startup-sync:result",
      (ev) => setStartupSync(describeStartupSync(ev)),
    );
    const offConflict = api.onEventPayload<SyncPreflight>(
      "startup-sync:conflict",
      (ev) => setStartupConflict(ev),
    );
    return () => {
      offOpen();
      offClose();
      offHelp();
      offSync();
      offConflict();
    };
  }, []);

  // Build a YYYY-MM-DD-keyed UTC midnight ISO string for the conflict-day
  // so the Override / Import calls re-use the existing per-day endpoints.
  function dayISOFromYYYYMMDD(local: string): string {
    const [y, m, d] = local.split("-").map(Number);
    return new Date(Date.UTC(y, m - 1, d)).toISOString();
  }

  return (
    <div className="flex h-full flex-col">
      <header className="flex items-center gap-2 border-b border-slate-700 bg-surface px-4 py-3">
        <h1 className="text-lg font-semibold text-slate-100">Hashpoint</h1>
        <nav className="ml-6 flex gap-1">
          {tabs.map((t) => (
            <button
              key={t.id}
              onClick={() => setTab(t.id)}
              className={`rounded px-3 py-1 text-sm transition-colors ${
                tab === t.id
                  ? "bg-accent text-white"
                  : "text-slate-300 hover:bg-slate-700"
              }`}
            >
              {t.label}
            </button>
          ))}
        </nav>
        <PersonioBadge />
      </header>
      {startupSync && (
        <div
          className={`flex items-start justify-between gap-3 px-4 py-2 text-sm ${
            startupSync.level === "success"
              ? "bg-emerald-900/40 text-emerald-200"
              : startupSync.level === "error"
                ? "bg-red-900/40 text-red-200"
                : "bg-amber-900/30 text-amber-200"
          }`}
        >
          <span>{startupSync.text}</span>
          <button
            onClick={() => setStartupSync(null)}
            className="text-xs opacity-70 hover:opacity-100"
            aria-label="Meldung schließen"
          >
            ✕
          </button>
        </div>
      )}
      <main className="flex-1 overflow-auto p-4">
        {tab === "timeline" && <Timeline />}
        {tab === "tags" && <TagManager />}
        {tab === "rules" && <RuleManager />}
        {tab === "settings" && <Settings />}
        {tab === "help" && <Help />}
        {tab === "about" && <About />}
      </main>
      {quickTagOpen && (
        <QuickTagPicker onClose={() => setQuickTagOpen(false)} />
      )}
      {startupConflict && (
        <SyncConflictModal
          preflight={startupConflict}
          onOverride={(day) => api.syncDay(dayISOFromYYYYMMDD(day))}
          onImport={(day) => api.importPersonioDay(dayISOFromYYYYMMDD(day))}
          onClose={(banner) => {
            setStartupConflict(null);
            if (banner) setStartupSync(banner);
          }}
        />
      )}
    </div>
  );
}
