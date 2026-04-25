import { useEffect, useState } from "react";
import { api } from "../api";
import type { VersionInfo } from "../types";

export default function About() {
  const [v, setV] = useState<VersionInfo | null>(null);
  useEffect(() => {
    api.version().then(setV).catch(() => setV(null));
  }, []);
  return (
    <div className="mx-auto max-w-md rounded bg-surface p-6 text-sm text-slate-300">
      <h2 className="mb-3 text-lg font-semibold text-slate-100">
        Hashpoint TimeTracker
      </h2>
      <dl className="grid grid-cols-[120px,1fr] gap-y-1">
        <dt>Version</dt>
        <dd className="font-mono">{v?.version ?? "—"}</dd>
        <dt>Commit</dt>
        <dd className="font-mono">{v?.commit ?? "—"}</dd>
        <dt>Build</dt>
        <dd className="font-mono">{v?.build_date ?? "—"}</dd>
      </dl>
      <p className="mt-4 text-xs text-slate-500">
        Datenverzeichnis: %LOCALAPPDATA%\TimeTracker
      </p>
    </div>
  );
}
