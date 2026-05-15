import { useEffect, useState } from "react";
import { api } from "../api";
import type { Tag } from "../types";
import OrderCombobox from "./OrderCombobox";

const empty: Partial<Tag> = {
  name: "",
  description: "",
  color: "#4f8cff",
  sync_to_personio: true,
};

export default function TagManager() {
  const [tags, setTags] = useState<Tag[]>([]);
  const [draft, setDraft] = useState<Partial<Tag>>(empty);
  const [error, setError] = useState<string | null>(null);

  async function refresh() {
    try {
      setTags((await api.listTags()) ?? []);
      setError(null);
    } catch (e) {
      setError(String(e));
    }
  }
  useEffect(() => {
    refresh();
  }, []);

  async function save() {
    try {
      if (draft.id) {
        await api.updateTag(draft as Tag);
      } else {
        await api.createTag(draft);
      }
      setDraft(empty);
      refresh();
    } catch (e) {
      setError(String(e));
    }
  }

  async function remove(id: number) {
    if (!confirm("Tag (inkl. Sub-Tags) löschen?")) return;
    await api.deleteTag(id);
    refresh();
  }

  const parents = tags.filter((t) => !t.parent_id);

  return (
    <div className="grid gap-6 lg:grid-cols-[1fr,360px]">
      <div>
        <h2 className="mb-3 font-semibold">Tags</h2>
        <ul className="divide-y divide-slate-700 rounded bg-surface">
          {parents.map((p) => {
            const subs = tags.filter((t) => t.parent_id === p.id);
            return (
              <li key={p.id} className="px-3 py-2">
                <div className="flex items-center gap-2">
                  <span
                    className="rounded px-2 py-0.5 text-xs"
                    style={{ background: p.color ?? "#4f8cff" }}
                  >
                    {p.name}
                  </span>
                  <span className="text-xs text-slate-400">
                    {p.personio_project_id ?? "—"} /{" "}
                    {p.personio_activity_id ?? "—"}
                  </span>
                  <button
                    onClick={() => setDraft(p)}
                    className="ml-auto text-xs text-accent hover:underline"
                  >
                    Bearbeiten
                  </button>
                  <button
                    onClick={() => remove(p.id)}
                    className="text-xs text-red-400 hover:underline"
                  >
                    Löschen
                  </button>
                </div>
                {subs.length > 0 && (
                  <ul className="mt-1 ml-4 space-y-1">
                    {subs.map((s) => (
                      <li key={s.id} className="flex items-center gap-2 text-sm">
                        <span
                          className="rounded px-2 py-0.5 text-xs"
                          style={{ background: s.color ?? "#374863" }}
                        >
                          {s.name}
                        </span>
                        <span className="text-slate-400">{s.description}</span>
                        <button
                          onClick={() => setDraft(s)}
                          className="ml-auto text-xs text-accent hover:underline"
                        >
                          Bearbeiten
                        </button>
                        <button
                          onClick={() => remove(s.id)}
                          className="text-xs text-red-400 hover:underline"
                        >
                          Löschen
                        </button>
                      </li>
                    ))}
                  </ul>
                )}
              </li>
            );
          })}
        </ul>
      </div>

      <aside className="space-y-3 rounded bg-surface p-4">
        <h3 className="font-semibold">{draft.id ? "Tag bearbeiten" : "Neuer Tag"}</h3>
        <label className="block text-xs text-slate-400">
          Name (mit oder ohne #)
          <input
            value={draft.name ?? ""}
            onChange={(e) => setDraft({ ...draft, name: e.target.value })}
            className="mt-1 w-full rounded bg-bg px-2 py-1 text-sm"
            placeholder="#projekta"
          />
        </label>
        <label className="block text-xs text-slate-400">
          Parent-Tag
          <select
            value={draft.parent_id ?? ""}
            onChange={(e) =>
              setDraft({
                ...draft,
                parent_id: e.target.value ? Number(e.target.value) : undefined,
              })
            }
            className="mt-1 w-full rounded bg-bg px-2 py-1 text-sm"
          >
            <option value="">— Top-Level —</option>
            {parents
              .filter((p) => p.id !== draft.id)
              .map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
          </select>
        </label>
        <label className="block text-xs text-slate-400">
          Beschreibung (Sub-Tag)
          <input
            value={draft.description ?? ""}
            onChange={(e) =>
              setDraft({ ...draft, description: e.target.value })
            }
            className="mt-1 w-full rounded bg-bg px-2 py-1 text-sm"
          />
        </label>
        <label className="block text-xs text-slate-400">
          Farbe
          <input
            type="color"
            value={draft.color ?? "#4f8cff"}
            onChange={(e) => setDraft({ ...draft, color: e.target.value })}
            className="mt-1 h-8 w-full rounded bg-bg"
          />
        </label>
        <div className="grid grid-cols-2 gap-2">
          <label className="block text-xs text-slate-400">
            Personio Project ID
            <input
              value={draft.personio_project_id ?? ""}
              onChange={(e) =>
                setDraft({ ...draft, personio_project_id: e.target.value })
              }
              className="mt-1 w-full rounded bg-bg px-2 py-1 text-sm"
            />
          </label>
          <label className="block text-xs text-slate-400">
            Personio Activity ID
            <input
              value={draft.personio_activity_id ?? ""}
              onChange={(e) =>
                setDraft({ ...draft, personio_activity_id: e.target.value })
              }
              className="mt-1 w-full rounded bg-bg px-2 py-1 text-sm"
            />
          </label>
        </div>
        <label className="flex items-center gap-2 text-xs text-slate-400">
          <input
            type="checkbox"
            checked={draft.sync_to_personio ?? true}
            onChange={(e) =>
              setDraft({ ...draft, sync_to_personio: e.target.checked })
            }
          />
          Zu Personio synchronisieren
        </label>
        <div className="block text-xs text-slate-400">
          Auftrag
          <OrderCombobox
            value={draft.order_name ?? ""}
            onChange={(v) =>
              setDraft({ ...draft, order_name: v === "" ? undefined : v })
            }
          />
          <span className="mt-1 block text-[11px] leading-tight text-slate-500">
            Optional. Wähle einen Auftrag aus einem tag_provider-Plugin oder gib
            einen beliebigen Text ein. Wird gespeichert, aber nicht zu Personio
            synchronisiert.
          </span>
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
      </aside>
    </div>
  );
}
