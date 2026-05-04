import { useEffect, useState } from "react";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { api } from "../api";

interface DocPage {
  slug: string;
  title: string;
}

export default function Help() {
  const [pages, setPages] = useState<DocPage[]>([]);
  const [activeSlug, setActiveSlug] = useState<string | null>(null);
  const [content, setContent] = useState<string>("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    api
      .listUserDocs()
      .then((p) => {
        if (cancelled) return;
        setPages(p);
        if (p.length > 0 && activeSlug == null) setActiveSlug(p[0].slug);
      })
      .catch((e) => !cancelled && setError(String(e)));
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (!activeSlug) return;
    let cancelled = false;
    setLoading(true);
    setError(null);
    api
      .getUserDoc(activeSlug)
      .then((md) => {
        if (cancelled) return;
        setContent(md);
        const article = document.getElementById("help-article");
        if (article) article.scrollTop = 0;
      })
      .catch((e) => !cancelled && setError(String(e)))
      .finally(() => !cancelled && setLoading(false));
    return () => {
      cancelled = true;
    };
  }, [activeSlug]);

  // Sidebar links inside a doc body (e.g. [Tags verwalten](tags.md)) should
  // navigate the Help tab, not open a browser.
  function handleDocLinkClick(href: string | undefined): boolean {
    if (!href) return false;
    if (!href.endsWith(".md")) return false;
    const slug = href.replace(/^\.\//, "").replace(/\.md$/, "");
    if (pages.some((p) => p.slug === slug)) {
      setActiveSlug(slug);
      return true;
    }
    return false;
  }

  return (
    <div className="flex h-full gap-4">
      <aside className="w-56 shrink-0 overflow-y-auto rounded bg-surface p-2">
        <ul className="space-y-1">
          {pages.map((p) => (
            <li key={p.slug}>
              <button
                onClick={() => setActiveSlug(p.slug)}
                className={`w-full rounded px-2 py-1.5 text-left text-sm transition-colors ${
                  p.slug === activeSlug
                    ? "bg-accent text-white"
                    : "text-slate-300 hover:bg-slate-700"
                }`}
              >
                {p.title}
              </button>
            </li>
          ))}
        </ul>
      </aside>
      <article
        id="help-article"
        className="flex-1 overflow-y-auto rounded bg-surface px-6 py-4 text-sm leading-relaxed text-slate-300"
      >
        {error && (
          <div className="mb-3 rounded bg-red-900/40 px-3 py-2 text-sm text-red-200">
            {error}
          </div>
        )}
        {loading && !content && (
          <div className="text-sm text-slate-400">Lade …</div>
        )}
        <ReactMarkdown
          remarkPlugins={[remarkGfm]}
          components={mdComponents(handleDocLinkClick)}
        >
          {content}
        </ReactMarkdown>
      </article>
    </div>
  );
}

// Tailwind classes for each Markdown element. Centralised here so the
// styling stays consistent without pulling @tailwindcss/typography.
function mdComponents(
  onInternalLink: (href: string | undefined) => boolean,
): Components {
  return {
    h1: ({ children, ...props }) => (
      <h1
        {...props}
        className="mb-4 mt-2 border-b border-slate-700 pb-2 text-2xl font-semibold text-slate-100"
      >
        {children}
      </h1>
    ),
    h2: ({ children, ...props }) => (
      <h2
        {...props}
        className="mb-3 mt-6 text-xl font-semibold text-slate-100"
      >
        {children}
      </h2>
    ),
    h3: ({ children, ...props }) => (
      <h3 {...props} className="mb-2 mt-5 text-base font-semibold text-slate-100">
        {children}
      </h3>
    ),
    h4: ({ children, ...props }) => (
      <h4 {...props} className="mb-2 mt-4 text-sm font-semibold text-slate-100">
        {children}
      </h4>
    ),
    p: ({ children, ...props }) => (
      <p {...props} className="mb-3">
        {children}
      </p>
    ),
    ul: ({ children, ...props }) => (
      <ul {...props} className="mb-3 list-disc space-y-1 pl-6">
        {children}
      </ul>
    ),
    ol: ({ children, ...props }) => (
      <ol {...props} className="mb-3 list-decimal space-y-1 pl-6">
        {children}
      </ol>
    ),
    li: ({ children, ...props }) => <li {...props}>{children}</li>,
    strong: ({ children, ...props }) => (
      <strong {...props} className="font-semibold text-slate-100">
        {children}
      </strong>
    ),
    em: ({ children, ...props }) => (
      <em {...props} className="italic">
        {children}
      </em>
    ),
    a: ({ href, children, ...props }) => (
      <a
        {...props}
        href={href}
        onClick={(e) => {
          if (onInternalLink(href)) e.preventDefault();
        }}
        className="text-accent underline-offset-2 hover:underline"
      >
        {children}
      </a>
    ),
    code: ({ children, className, ...props }) => {
      const isBlock = className?.includes("language-");
      if (isBlock) {
        return (
          <code
            className="block whitespace-pre-wrap break-words rounded bg-slate-900/70 px-3 py-2 font-mono text-xs text-amber-200"
            {...props}
          >
            {children}
          </code>
        );
      }
      return (
        <code
          className="rounded bg-slate-900/70 px-1 py-0.5 font-mono text-xs text-amber-200"
          {...props}
        >
          {children}
        </code>
      );
    },
    pre: ({ children, ...props }) => (
      <pre {...props} className="mb-3 overflow-x-auto">
        {children}
      </pre>
    ),
    blockquote: ({ children, ...props }) => (
      <blockquote
        {...props}
        className="mb-3 border-l-4 border-accent/60 bg-slate-900/30 px-3 py-2 text-slate-300"
      >
        {children}
      </blockquote>
    ),
    table: ({ children, ...props }) => (
      <div className="mb-3 overflow-x-auto">
        <table
          {...props}
          className="min-w-full border-collapse text-left text-xs"
        >
          {children}
        </table>
      </div>
    ),
    th: ({ children, ...props }) => (
      <th
        {...props}
        className="border-b border-slate-700 bg-slate-900/40 px-2 py-1 font-semibold text-slate-200"
      >
        {children}
      </th>
    ),
    td: ({ children, ...props }) => (
      <td {...props} className="border-b border-slate-800 px-2 py-1 align-top">
        {children}
      </td>
    ),
    hr: ({ ...props }) => (
      <hr {...props} className="my-5 border-slate-700" />
    ),
  };
}
