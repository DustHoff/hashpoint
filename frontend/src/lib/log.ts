// Frontend → backend log bridge. The Wails LogFrontend binding pushes
// records into the same slog pipeline that the Go backend writes to, so
// all events (incl. JS exceptions and unhandled rejections) end up in
// %LOCALAPPDATA%\TimeTracker\log\timetracker.log.

type Level = "debug" | "info" | "warn" | "error";

interface Bridge {
  go?: { app?: { App?: { LogFrontend?: (...args: unknown[]) => Promise<void> } } };
}

function bridgeLog(): ((...args: unknown[]) => Promise<void>) | null {
  const fn = (window as unknown as Bridge).go?.app?.App?.LogFrontend;
  return typeof fn === "function" ? fn : null;
}

function send(level: Level, message: string, fields?: Record<string, unknown>) {
  const safeFields: Record<string, unknown> = {};
  if (fields) {
    for (const [k, v] of Object.entries(fields)) {
      safeFields[k] = serialize(v);
    }
  }
  const fn = bridgeLog();
  if (fn) {
    void fn(level, message, safeFields).catch(() => {
      /* avoid recursive logging if the bridge itself is broken */
    });
  }
  // Mirror everything to the dev console so opening DevTools is still useful.
  const c = console as unknown as Record<string, (...a: unknown[]) => void>;
  const fnName = level === "warn" ? "warn" : level === "error" ? "error" : "log";
  c[fnName]?.(`[${level}]`, message, safeFields);
}

function serialize(v: unknown): unknown {
  if (v instanceof Error) {
    return { name: v.name, message: v.message, stack: v.stack };
  }
  if (typeof v === "object" && v !== null) {
    try {
      JSON.stringify(v);
      return v;
    } catch {
      return String(v);
    }
  }
  return v;
}

export const log = {
  debug: (msg: string, fields?: Record<string, unknown>) =>
    send("debug", msg, fields),
  info: (msg: string, fields?: Record<string, unknown>) =>
    send("info", msg, fields),
  warn: (msg: string, fields?: Record<string, unknown>) =>
    send("warn", msg, fields),
  error: (msg: string, fields?: Record<string, unknown>) =>
    send("error", msg, fields),
};

/**
 * installGlobalHandlers wires window.onerror, unhandledrejection and a thin
 * console.error/warn override so any UI-side failure is surfaced in the
 * backend log. Must be called before React mounts.
 */
export function installGlobalHandlers(): void {
  // Stash original console fns so the wrapper can still print locally.
  const origError = console.error.bind(console);
  const origWarn = console.warn.bind(console);

  console.error = (...args: unknown[]) => {
    origError(...args);
    log.error("console.error", { args: args.map(serialize) });
  };
  console.warn = (...args: unknown[]) => {
    origWarn(...args);
    log.warn("console.warn", { args: args.map(serialize) });
  };

  window.addEventListener("error", (event) => {
    log.error("window.onerror", {
      message: event.message,
      filename: event.filename,
      lineno: event.lineno,
      colno: event.colno,
      error: event.error,
    });
  });

  window.addEventListener("unhandledrejection", (event) => {
    log.error("unhandledrejection", {
      reason: serialize(event.reason),
    });
  });

  log.info("frontend handlers installed");
}
