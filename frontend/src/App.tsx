import { useState } from "react";
import Timeline from "./components/Timeline";
import TagManager from "./components/TagManager";
import RuleManager from "./components/RuleManager";
import Settings from "./components/Settings";
import About from "./components/About";

type Tab = "timeline" | "tags" | "rules" | "settings" | "about";

const tabs: { id: Tab; label: string }[] = [
  { id: "timeline", label: "Zeitachse" },
  { id: "tags", label: "Tags" },
  { id: "rules", label: "Auto-Tagging" },
  { id: "settings", label: "Einstellungen" },
  { id: "about", label: "Über" },
];

export default function App() {
  const [tab, setTab] = useState<Tab>("timeline");

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
      </header>
      <main className="flex-1 overflow-auto p-4">
        {tab === "timeline" && <Timeline />}
        {tab === "tags" && <TagManager />}
        {tab === "rules" && <RuleManager />}
        {tab === "settings" && <Settings />}
        {tab === "about" && <About />}
      </main>
    </div>
  );
}
