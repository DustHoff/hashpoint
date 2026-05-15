import { useEffect, useMemo, useRef, useState } from "react";
import type { Tag } from "../types";

// TagMultiSelect renders a searchable combobox over the flat tag list.
// Selected tags are shown as removable chips above the input. Typing
// filters the dropdown by tag name (case-insensitive substring) and by
// the resolved parent/child path so users can target nested tags by
// either name or branch. Designed for short tag lists (<200) — there is
// no virtualisation.
//
// onChange is called with the next selected-id list whenever the user
// adds or removes a tag. The order of the returned list mirrors the
// click sequence — callers wanting a canonical order should sort.
export default function TagMultiSelect({
  tags,
  selected,
  onChange,
  placeholder = "Tag suchen…",
}: {
  tags: Tag[];
  selected: number[];
  onChange: (next: number[]) => void;
  placeholder?: string;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const wrapRef = useRef<HTMLDivElement>(null);

  // Resolve "parent/child/grandchild" path for every tag once per render.
  // Used both for display and search so users can type a branch name.
  const paths = useMemo(() => buildPaths(tags), [tags]);

  const selectedSet = useMemo(() => new Set(selected), [selected]);
  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return tags;
    return tags.filter((t) => {
      const path = paths.get(t.id) ?? t.name;
      return path.toLowerCase().includes(q);
    });
  }, [tags, paths, query]);

  // Close the dropdown when the user clicks anywhere outside the wrapper.
  // The mousedown listener fires before any inner onClick — that order
  // matters so an "X" on a chip inside the wrapper does not also close
  // the picker via the outside-click handler.
  useEffect(() => {
    if (!open) return;
    function onDocMouseDown(e: MouseEvent) {
      if (!wrapRef.current) return;
      if (wrapRef.current.contains(e.target as Node)) return;
      setOpen(false);
    }
    document.addEventListener("mousedown", onDocMouseDown);
    return () => document.removeEventListener("mousedown", onDocMouseDown);
  }, [open]);

  function toggle(id: number) {
    if (selectedSet.has(id)) {
      onChange(selected.filter((x) => x !== id));
    } else {
      onChange([...selected, id]);
    }
  }

  return (
    <div ref={wrapRef} className="relative">
      {selected.length > 0 && (
        <div className="mb-2 flex flex-wrap gap-1">
          {selected.map((id) => {
            const tag = tags.find((t) => t.id === id);
            const label = tag ? (paths.get(id) ?? tag.name) : `id=${id}`;
            return (
              <span
                key={id}
                className="inline-flex items-center gap-1 rounded bg-accent/80 px-2 py-0.5 text-xs text-white"
              >
                <span>{label}</span>
                <button
                  type="button"
                  onClick={() => toggle(id)}
                  className="rounded px-1 hover:bg-black/30"
                  aria-label={`${label} entfernen`}
                >
                  ×
                </button>
              </span>
            );
          })}
        </div>
      )}

      <input
        type="text"
        value={query}
        onChange={(e) => {
          setQuery(e.target.value);
          setOpen(true);
        }}
        onFocus={() => setOpen(true)}
        placeholder={placeholder}
        className="w-full rounded bg-slate-900/60 px-2 py-1 text-sm"
      />

      {open && filtered.length > 0 && (
        <ul className="absolute z-10 mt-1 max-h-64 w-full overflow-y-auto rounded border border-slate-700 bg-slate-900 py-1 text-sm shadow-lg">
          {filtered.map((t) => {
            const checked = selectedSet.has(t.id);
            const path = paths.get(t.id) ?? t.name;
            return (
              <li key={t.id}>
                <label className="flex cursor-pointer items-center gap-2 px-2 py-1 hover:bg-slate-800">
                  <input
                    type="checkbox"
                    checked={checked}
                    onChange={() => toggle(t.id)}
                    className="h-3.5 w-3.5"
                  />
                  <span className={checked ? "text-slate-100" : "text-slate-300"}>
                    {path}
                  </span>
                </label>
              </li>
            );
          })}
        </ul>
      )}

      {open && filtered.length === 0 && (
        <div className="absolute z-10 mt-1 w-full rounded border border-slate-700 bg-slate-900 px-2 py-2 text-xs text-slate-500">
          Keine Treffer für „{query}".
        </div>
      )}
    </div>
  );
}

// buildPaths walks the parent chain for every tag and produces
// "root/child/leaf" strings keyed by tag id. Loops in the data (which
// the storage layer prevents on insert but we still defend against)
// terminate at the first repeated id so the function never recurses
// indefinitely.
function buildPaths(tags: Tag[]): Map<number, string> {
  const byId = new Map<number, Tag>();
  for (const t of tags) byId.set(t.id, t);
  const cache = new Map<number, string>();
  function resolve(id: number, seen: Set<number>): string {
    if (cache.has(id)) return cache.get(id) as string;
    const t = byId.get(id);
    if (!t) return `id=${id}`;
    if (seen.has(id)) return t.name;
    seen.add(id);
    const path =
      t.parent_id && byId.has(t.parent_id)
        ? `${resolve(t.parent_id, seen)}/${t.name}`
        : t.name;
    cache.set(id, path);
    return path;
  }
  for (const t of tags) resolve(t.id, new Set());
  return cache;
}
