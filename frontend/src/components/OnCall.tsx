// Rufbereitschaft tab: inbox of off-hours tag-blocks needing documentation
// on the left, a form for the selected block on the right. Listens to
// "oncall:doc-changed" + "oncall:submit-result" events from the host to
// refresh after the user (or a plugin) makes a change.

import { useCallback, useEffect, useMemo, useState } from "react";
import { api } from "../api";
import type {
  OnCallDocChangedPayload,
  OnCallDocStatus,
  OnCallDocView,
  OnCallIncidentType,
  OnCallSubmitResultPayload,
} from "../types";

const statusColors: Record<OnCallDocStatus, string> = {
  draft: "bg-slate-700 text-slate-200",
  pending: "bg-amber-700 text-amber-100",
  submitted: "bg-emerald-700 text-emerald-100",
  partial: "bg-orange-700 text-orange-100",
  failed: "bg-red-700 text-red-100",
};

const statusLabels: Record<OnCallDocStatus, string> = {
  draft: "Entwurf",
  pending: "Wird übertragen…",
  submitted: "Übertragen",
  partial: "Teilweise übertragen",
  failed: "Fehlgeschlagen",
};

const incidentLabels: Record<OnCallIncidentType, string> = {
  "": "— bitte wählen —",
  planned_maintenance: "Geplante Wartung",
  service_disruption: "Service-Störung",
};

function formatRange(startISO: string, endISO: string): string {
  const start = new Date(startISO);
  const end = new Date(endISO);
  const date = start.toLocaleDateString("de-DE", {
    weekday: "short",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  });
  const hm = (d: Date) =>
    d.toLocaleTimeString("de-DE", { hour: "2-digit", minute: "2-digit" });
  return `${date} ${hm(start)} – ${hm(end)}`;
}

export default function OnCall() {
  const [docs, setDocs] = useState<OnCallDocView[]>([]);
  const [selectedID, setSelectedID] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [includeStale, setIncludeStale] = useState(true);

  const refresh = useCallback(async () => {
    try {
      const list = await api.onCallDocList({ include_stale: includeStale });
      setDocs(list ?? []);
      setError(null);
    } catch (e) {
      setError(String(e));
    }
  }, [includeStale]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  // Auto-refresh on host events: doc edits + plugin responses both come
  // through "oncall:doc-changed"; per-plugin progress arrives via
  // "oncall:submit-result" and we surface it as an inbox refresh.
  useEffect(() => {
    const offChanged = api.onEventPayload<OnCallDocChangedPayload>(
      "oncall:doc-changed",
      () => refresh(),
    );
    const offResult = api.onEventPayload<OnCallSubmitResultPayload>(
      "oncall:submit-result",
      () => refresh(),
    );
    return () => {
      offChanged();
      offResult();
    };
  }, [refresh]);

  const selected = useMemo(
    () => docs.find((d) => d.id === selectedID) ?? null,
    [docs, selectedID],
  );

  return (
    <div className="grid h-full gap-6 lg:grid-cols-[360px,1fr]">
      <Inbox
        docs={docs}
        selectedID={selectedID}
        onSelect={setSelectedID}
        includeStale={includeStale}
        onToggleStale={setIncludeStale}
      />
      <div className="flex-1">
        {error && (
          <div className="mb-3 rounded bg-red-900/40 px-3 py-2 text-sm text-red-200">
            {error}
          </div>
        )}
        {selected ? (
          <DocForm
            doc={selected}
            onChanged={() => refresh()}
            onError={(e) => setError(e)}
          />
        ) : (
          <div className="text-sm text-slate-400">
            Wähle links einen Eintrag, um die Dokumentation zu erfassen.
          </div>
        )}
      </div>
    </div>
  );
}

function Inbox({
  docs,
  selectedID,
  onSelect,
  includeStale,
  onToggleStale,
}: {
  docs: OnCallDocView[];
  selectedID: number | null;
  onSelect: (id: number) => void;
  includeStale: boolean;
  onToggleStale: (v: boolean) => void;
}) {
  return (
    <div className="flex flex-col">
      <div className="mb-3 flex items-center justify-between">
        <h2 className="font-semibold">Rufbereitschaft</h2>
        <label className="flex cursor-pointer items-center gap-2 text-xs text-slate-300">
          <input
            type="checkbox"
            checked={includeStale}
            onChange={(e) => onToggleStale(e.target.checked)}
          />
          Veraltete anzeigen
        </label>
      </div>
      <ul className="flex-1 divide-y divide-slate-700 overflow-auto rounded bg-surface">
        {docs.length === 0 && (
          <li className="px-3 py-6 text-center text-sm text-slate-400">
            Keine Einträge.
          </li>
        )}
        {docs.map((d) => (
          <li
            key={d.id}
            className={`cursor-pointer px-3 py-2 hover:bg-slate-700 ${
              selectedID === d.id ? "bg-slate-700" : ""
            }`}
            onClick={() => onSelect(d.id)}
          >
            <div className="flex items-center justify-between gap-2">
              <span className="text-sm font-medium">{d.tag_name || "—"}</span>
              <span
                className={`rounded px-2 py-0.5 text-[10px] uppercase ${statusColors[d.status]}`}
              >
                {statusLabels[d.status]}
              </span>
            </div>
            <div className="mt-0.5 text-xs text-slate-400">
              {formatRange(d.start_time, d.end_time)}
            </div>
            {d.stale && (
              <div className="mt-1 text-[11px] text-amber-300">
                ⚠ Tag oder Zeitfenster wurde geändert.
              </div>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}

function DocForm({
  doc,
  onChanged,
  onError,
}: {
  doc: OnCallDocView;
  onChanged: () => void;
  onError: (msg: string) => void;
}) {
  const [application, setApplication] = useState(doc.application);
  const [incidentType, setIncidentType] = useState<OnCallIncidentType>(
    doc.incident_type,
  );
  const [solution, setSolution] = useState(doc.solution);
  const [busy, setBusy] = useState<"save" | "submit" | "dismiss" | null>(null);

  // Reload form state ONLY when the user selects a different doc — we
  // deliberately ignore in-flight edits to doc.{application,incident_type,
  // solution} so concurrent host refreshes (e.g. plugin submit-result)
  // don't blow away whatever the user is currently typing.
  useEffect(() => {
    setApplication(doc.application);
    setIncidentType(doc.incident_type);
    setSolution(doc.solution);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [doc.id]);

  async function save() {
    setBusy("save");
    try {
      await api.onCallDocSave(doc.id, {
        application,
        incident_type: incidentType,
        solution,
      });
      onChanged();
    } catch (e) {
      onError(String(e));
    } finally {
      setBusy(null);
    }
  }

  async function submit() {
    setBusy("submit");
    try {
      // Save the current draft first so the plugin sees the latest text.
      await api.onCallDocSave(doc.id, {
        application,
        incident_type: incidentType,
        solution,
      });
      await api.onCallDocSubmit(doc.id);
      onChanged();
    } catch (e) {
      onError(String(e));
    } finally {
      setBusy(null);
    }
  }

  async function dismiss() {
    if (!confirm("Dokumentation verwerfen?")) return;
    setBusy("dismiss");
    try {
      await api.onCallDocDismiss(doc.id);
      onChanged();
    } catch (e) {
      onError(String(e));
    } finally {
      setBusy(null);
    }
  }

  const canEdit = doc.status === "draft" || doc.status === "failed";

  return (
    <div className="flex h-full flex-col gap-3">
      <header>
        <h2 className="font-semibold">{doc.tag_name || "Rufbereitschaft"}</h2>
        <p className="text-xs text-slate-400">
          {formatRange(doc.start_time, doc.end_time)}
        </p>
      </header>

      {doc.stale && (
        <div className="rounded bg-amber-900/40 px-3 py-2 text-sm text-amber-200">
          Dieser Block qualifiziert sich nicht mehr für die
          Rufbereitschafts-Dokumentation. Du kannst die Dokumentation
          beibehalten oder verwerfen.
          <button
            onClick={dismiss}
            disabled={busy != null}
            className="ml-2 rounded bg-amber-700 px-2 py-0.5 text-xs hover:bg-amber-600 disabled:opacity-50"
          >
            Verwerfen
          </button>
        </div>
      )}

      <label className="flex flex-col gap-1 text-sm">
        Betroffene Anwendung
        <input
          type="text"
          value={application}
          onChange={(e) => setApplication(e.target.value)}
          disabled={!canEdit}
          className="rounded bg-slate-800 px-3 py-2 disabled:opacity-50"
        />
      </label>

      <label className="flex flex-col gap-1 text-sm">
        Art des Vorgangs
        <select
          value={incidentType}
          onChange={(e) => setIncidentType(e.target.value as OnCallIncidentType)}
          disabled={!canEdit}
          className="rounded bg-slate-800 px-3 py-2 disabled:opacity-50"
        >
          <option value="">{incidentLabels[""]}</option>
          <option value="planned_maintenance">
            {incidentLabels.planned_maintenance}
          </option>
          <option value="service_disruption">
            {incidentLabels.service_disruption}
          </option>
        </select>
      </label>

      <label className="flex flex-1 flex-col gap-1 text-sm">
        Lösung / Bearbeitung
        <textarea
          value={solution}
          onChange={(e) => setSolution(e.target.value)}
          disabled={!canEdit}
          className="min-h-[160px] flex-1 rounded bg-slate-800 px-3 py-2 disabled:opacity-50"
        />
      </label>

      <div className="flex items-center gap-2">
        <button
          onClick={save}
          disabled={!canEdit || busy != null}
          className="rounded bg-slate-700 px-3 py-1.5 text-sm hover:bg-slate-600 disabled:opacity-50"
        >
          {busy === "save" ? "Speichern…" : "Entwurf speichern"}
        </button>
        <button
          onClick={submit}
          disabled={!canEdit || busy != null}
          className="rounded bg-accent px-3 py-1.5 text-sm text-white hover:bg-blue-500 disabled:opacity-50"
        >
          {busy === "submit"
            ? "Übertragen…"
            : doc.status === "failed"
              ? "Erneut versuchen"
              : "An Dokumentationssystem senden"}
        </button>
      </div>

      {doc.submissions && doc.submissions.length > 0 && (
        <div className="mt-3 rounded bg-slate-800/60 p-3 text-xs">
          <div className="mb-1 font-semibold text-slate-200">Plugin-Status</div>
          <ul className="space-y-1">
            {doc.submissions.map((s) => (
              <li
                key={s.plugin_name}
                className="flex items-center justify-between gap-2"
              >
                <span className="text-slate-300">{s.plugin_name}</span>
                <span className="flex items-center gap-2">
                  {s.external_url ? (
                    <a
                      href={s.external_url}
                      target="_blank"
                      rel="noreferrer"
                      className="text-accent hover:underline"
                    >
                      {s.external_ref || "öffnen"}
                    </a>
                  ) : (
                    <span className="text-slate-400">
                      {s.external_ref ?? ""}
                    </span>
                  )}
                  <span
                    className={`rounded px-1.5 py-0.5 text-[10px] uppercase ${
                      s.status === "submitted"
                        ? "bg-emerald-700 text-emerald-100"
                        : s.status === "failed"
                          ? "bg-red-700 text-red-100"
                          : "bg-amber-700 text-amber-100"
                    }`}
                  >
                    {s.status}
                  </span>
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
