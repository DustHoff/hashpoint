import { useEffect, useMemo, useState } from "react";
import { api } from "../api";
import type { Rule, Tag } from "../types";
import { dateInputValue, fromDateInput, startOfDayUTCISO } from "../lib/time";

const empty: Partial<Rule> = {
  match_field: "process_name",
  match_type: "contains",
  pattern: "",
  description: "",
  priority: 0,
  enabled: true,
};

const MAX_DESCRIPTION = 250;

export default function RuleManager() {
  const [rules, setRules] = useState<Rule[]>([]);
  const [tags, setTags] = useState<Tag[]>([]);
  const [draft, setDraft] = useState<Partial<Rule>>(empty);
  const [error, setError] = useState<string | null>(null);
  const [testDay, setTestDay] = useState(new Date());
  const [testResult, setTestResult] = useState<
    Array<{
      track_id: number;
      process_name: string;
      window_title: string;
      matched: boolean;
    }>
  >([]);

  async function refresh() {
    try {
      const [r, t] = await Promise.all([api.listRules(), api.listTags()]);
      setRules(r ?? []);
      setTags(t ?? []);
      setError(null);
    } catch (e) {
      setError(String(e));
    }
  }
  useEffect(() => {
    refresh();
  }, []);

  const tagsByID = useMemo(() => {
    const m: Record<number, Tag> = {};
    tags.forEach((t) => (m[t.id] = t));
    return m;
  }, [tags]);

  async function save() {
    try {
      if (!draft.tag_id) {
        setError("Bitte Ziel-Tag wählen");
        return;
      }
      const desc = (draft.description ?? "").trim();
      if (desc.length > MAX_DESCRIPTION) {
        setError(`Beschreibung darf max. ${MAX_DESCRIPTION} Zeichen haben`);
        return;
      }
      const payload: Partial<Rule> = { ...draft, description: desc || undefined };
      if (payload.id) await api.updateRule(payload as Rule);
      else await api.createRule(payload);
      setDraft(empty);
      refresh();
    } catch (e) {
      setError(String(e));
    }
  }

  async function remove(id: number) {
    if (!confirm("Regel löschen?")) return;
    await api.deleteRule(id);
    refresh();
  }

  async function toggleEnabled(rule: Rule, next: boolean) {
    try {
      await api.updateRule({ ...rule, enabled: next });
      refresh();
    } catch (e) {
      setError(String(e));
    }
  }

  async function test() {
    try {
      const r = await api.testRule(draft, startOfDayUTCISO(testDay));
      setTestResult(r);
    } catch (e) {
      setError(String(e));
    }
  }

  return (
    <div className="grid gap-6 lg:grid-cols-[1fr,420px]">
      <div>
        <h2 className="mb-3 font-semibold">Auto-Tagging-Regeln</h2>
        <ul className="divide-y divide-slate-700 rounded bg-surface">
          {rules.map((r) => {
            const tag = tagsByID[r.tag_id];
            return (
              <li key={r.id} className="px-3 py-2 text-sm">
                <div className="flex items-center gap-2">
                  <span className="font-mono text-xs text-slate-400">
                    P{r.priority}
                  </span>
                  <span className={r.enabled ? "text-slate-300" : "text-slate-500"}>
                    {r.match_field} {r.match_type}{" "}
                    <code className="rounded bg-bg px-1">{r.pattern}</code> →{" "}
                    {tag ? tag.name : `tag ${r.tag_id}`}
                  </span>
                  <button
                    type="button"
                    role="switch"
                    aria-checked={r.enabled}
                    aria-label={r.enabled ? "Regel deaktivieren" : "Regel aktivieren"}
                    title={r.enabled ? "Regel deaktivieren" : "Regel aktivieren"}
                    onClick={() => toggleEnabled(r, !r.enabled)}
                    className={`ml-auto inline-flex h-5 w-9 shrink-0 cursor-pointer items-center rounded-full transition-colors ${
                      r.enabled ? "bg-accent" : "bg-slate-600"
                    }`}
                  >
                    <span
                      className={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
                        r.enabled ? "translate-x-4" : "translate-x-0.5"
                      }`}
                    />
                  </button>
                  <button
                    onClick={() => setDraft(r)}
                    className="text-xs text-accent hover:underline"
                  >
                    Bearbeiten
                  </button>
                  <button
                    onClick={() => remove(r.id)}
                    className="text-xs text-red-400 hover:underline"
                  >
                    Löschen
                  </button>
                </div>
                {r.description && (
                  <div
                    className="ml-6 mt-1 truncate text-xs text-slate-400"
                    title={r.description}
                  >
                    Beschreibung: {r.description}
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      </div>

      <aside className="space-y-3 rounded bg-surface p-4">
        <h3 className="font-semibold">{draft.id ? "Regel bearbeiten" : "Neue Regel"}</h3>
        <div className="grid grid-cols-2 gap-2 text-xs text-slate-400">
          <label>
            Feld
            <select
              value={draft.match_field ?? "process_name"}
              onChange={(e) =>
                setDraft({ ...draft, match_field: e.target.value as Rule["match_field"] })
              }
              className="mt-1 w-full rounded bg-bg px-2 py-1 text-sm"
            >
              <option value="process_name">Prozess</option>
              <option value="window_title">Fenstertitel</option>
              <option value="both">Beide</option>
            </select>
          </label>
          <label>
            Typ
            <select
              value={draft.match_type ?? "contains"}
              onChange={(e) =>
                setDraft({ ...draft, match_type: e.target.value as Rule["match_type"] })
              }
              className="mt-1 w-full rounded bg-bg px-2 py-1 text-sm"
            >
              <option value="contains">enthält</option>
              <option value="equals">gleich</option>
              <option value="regex">Regex (RE2)</option>
            </select>
          </label>
        </div>
        <label className="block text-xs text-slate-400">
          Pattern
          <input
            value={draft.pattern ?? ""}
            onChange={(e) => setDraft({ ...draft, pattern: e.target.value })}
            className="mt-1 w-full rounded bg-bg px-2 py-1 text-sm"
          />
        </label>
        <label className="block text-xs text-slate-400">
          Ziel-Tag
          <select
            value={draft.tag_id ?? ""}
            onChange={(e) =>
              setDraft({ ...draft, tag_id: Number(e.target.value) || undefined })
            }
            className="mt-1 w-full rounded bg-bg px-2 py-1 text-sm"
          >
            <option value="">— wählen —</option>
            {tags.map((t) => {
              const parent = t.parent_id ? tagsByID[t.parent_id] : undefined;
              return (
                <option key={t.id} value={t.id}>
                  {parent ? `${parent.name} ${t.name}` : t.name}
                </option>
              );
            })}
          </select>
        </label>
        <label className="block text-xs text-slate-400">
          <div className="flex items-baseline justify-between">
            <span>Beschreibung (optional)</span>
            <span
              className={
                (draft.description ?? "").length > MAX_DESCRIPTION
                  ? "text-red-400"
                  : "text-slate-500"
              }
            >
              {(draft.description ?? "").length}/{MAX_DESCRIPTION}
            </span>
          </div>
          <input
            value={draft.description ?? ""}
            maxLength={MAX_DESCRIPTION}
            placeholder="Wird in Auto-Tag-Blöcke übernommen"
            onChange={(e) =>
              setDraft({ ...draft, description: e.target.value })
            }
            className="mt-1 w-full rounded bg-bg px-2 py-1 text-sm"
          />
        </label>
        <div className="grid grid-cols-2 gap-2 text-xs text-slate-400">
          <label>
            Priorität
            <input
              type="number"
              value={draft.priority ?? 0}
              onChange={(e) =>
                setDraft({ ...draft, priority: Number(e.target.value) })
              }
              className="mt-1 w-full rounded bg-bg px-2 py-1 text-sm"
            />
          </label>
          <label className="flex items-end gap-2">
            <input
              type="checkbox"
              checked={draft.enabled ?? true}
              onChange={(e) => setDraft({ ...draft, enabled: e.target.checked })}
            />
            aktiviert
          </label>
        </div>
        {error && <div className="text-sm text-red-400">{error}</div>}
        <div className="flex gap-2">
          <button
            onClick={save}
            className="rounded bg-accent px-3 py-1 text-sm text-white"
          >
            Speichern
          </button>
          <button
            onClick={() => setDraft(empty)}
            className="rounded bg-slate-700 px-3 py-1 text-sm"
          >
            Abbrechen
          </button>
        </div>

        <div className="mt-4 border-t border-slate-700 pt-3">
          <h4 className="mb-2 text-sm font-semibold">Live-Test</h4>
          <div className="flex items-center gap-2">
            <input
              type="date"
              value={dateInputValue(testDay)}
              onChange={(e) => setTestDay(fromDateInput(e.target.value))}
              className="rounded bg-bg px-2 py-1 text-sm"
            />
            <button
              onClick={test}
              className="rounded bg-slate-700 px-3 py-1 text-sm"
            >
              Testen
            </button>
          </div>
          {testResult.length > 0 && (
            <ul className="mt-2 max-h-48 overflow-auto rounded bg-bg text-xs">
              {testResult.map((r) => {
                const desc = (draft.description ?? "").trim();
                return (
                  <li
                    key={r.track_id}
                    className={`flex gap-2 px-2 py-1 ${
                      r.matched ? "text-emerald-300" : "text-slate-500"
                    }`}
                  >
                    <span className="w-3">{r.matched ? "✓" : "·"}</span>
                    <span className="w-32 truncate">{r.process_name}</span>
                    <span className="flex-1 truncate">{r.window_title}</span>
                    {r.matched && desc && (
                      <span
                        className="ml-2 truncate text-slate-400"
                        title={desc}
                      >
                        → „{desc}"
                      </span>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      </aside>
    </div>
  );
}
