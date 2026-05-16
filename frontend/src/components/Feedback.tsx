import { useEffect, useRef, useState } from "react";
import { api } from "../api";
import { log } from "../lib/log";
import type {
  FeedbackCategory,
  FeedbackDeviceCode,
  FeedbackInput,
  FeedbackLogWindow,
  FeedbackSeverity,
  FeedbackStatus,
  FeedbackSubmitResult,
} from "../types";

// Form state mirrors FeedbackInput but allows progressive entry —
// the Submit / Preview handlers coerce into the backend DTO and the
// backend re-validates. Keeping a single shape avoids duplicate
// "draft vs. payload" types.
type FormState = FeedbackInput;

const emptyForm: FormState = {
  title: "",
  category: "bug",
  severity: "medium",
  description: "",
  expected: "",
  actual: "",
  repro: "",
  include_log: true,
  log_window: "today",
};

// CATEGORY_LABELS / SEVERITY_LABELS / WINDOW_LABELS pair the wire
// values with their localized form labels in one place — same pattern
// the Settings.tsx WORK_DAYS row uses.
const CATEGORY_LABELS: ReadonlyArray<{ value: FeedbackCategory; label: string }> = [
  { value: "bug", label: "Fehler" },
  { value: "feature", label: "Feature-Wunsch" },
  { value: "question", label: "Frage" },
];

const SEVERITY_LABELS: ReadonlyArray<{ value: FeedbackSeverity; label: string }> = [
  { value: "low", label: "Niedrig" },
  { value: "medium", label: "Mittel" },
  { value: "high", label: "Hoch" },
  { value: "critical", label: "Kritisch" },
];

const WINDOW_LABELS: ReadonlyArray<{ value: FeedbackLogWindow; label: string }> = [
  { value: "today", label: "Heute" },
  { value: "1h", label: "Letzte Stunde" },
  { value: "24h", label: "Letzte 24 Stunden" },
];

export default function Feedback() {
  const [status, setStatus] = useState<FeedbackStatus | null>(null);
  const [statusError, setStatusError] = useState<string | null>(null);
  const [device, setDevice] = useState<FeedbackDeviceCode | null>(null);
  const [deviceError, setDeviceError] = useState<string | null>(null);
  const [form, setForm] = useState<FormState>(emptyForm);
  const [preview, setPreview] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [submitted, setSubmitted] = useState<FeedbackSubmitResult | null>(null);

  async function refreshStatus() {
    try {
      const s = await api.feedbackStatus();
      setStatus(s);
      setStatusError(null);
    } catch (e) {
      setStatusError(String(e));
      log.error("feedback: status fetch failed", { err: String(e) });
    }
  }

  useEffect(() => {
    void refreshStatus();
  }, []);

  async function handleStartLogin() {
    setDeviceError(null);
    try {
      const dc = await api.feedbackStartDeviceLogin();
      setDevice(dc);
    } catch (e) {
      setDeviceError(String(e));
    }
  }

  async function handleLogout() {
    try {
      await api.feedbackLogout();
      await refreshStatus();
    } catch (e) {
      setStatusError(String(e));
    }
  }

  async function handlePreview() {
    setFormError(null);
    try {
      const body = await api.feedbackPreview(form);
      setPreview(body);
    } catch (e) {
      setFormError(String(e));
    }
  }

  async function handleSubmit() {
    setSubmitting(true);
    setFormError(null);
    try {
      const res = await api.feedbackSubmit(form);
      setSubmitted(res);
      setPreview(null);
      setForm(emptyForm);
    } catch (e) {
      setFormError(String(e));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="mx-auto max-w-3xl space-y-6">
      <header>
        <h2 className="text-lg font-semibold text-slate-100">Feedback</h2>
        <p className="mt-1 text-sm text-slate-400">
          Melde Fehler oder Feature-Wünsche direkt als GitHub-Issue im{" "}
          <span className="font-mono">DustHoff/hashpoint</span>-Repo. Vor dem
          Absenden zeigt eine Vorschau, was inkl. Logs hochgeladen wird.
        </p>
      </header>

      <ConnectBox
        status={status}
        statusError={statusError}
        onLogin={handleStartLogin}
        onLogout={handleLogout}
      />

      {submitted && (
        <div className="rounded bg-emerald-900/40 px-3 py-2 text-sm text-emerald-200">
          Issue #{submitted.number} angelegt.{" "}
          <a
            href={submitted.html_url}
            target="_blank"
            rel="noreferrer"
            className="underline underline-offset-2 hover:text-emerald-100"
          >
            Auf GitHub öffnen ↗
          </a>
          <button
            onClick={() => setSubmitted(null)}
            className="ml-3 text-xs opacity-70 hover:opacity-100"
          >
            schließen
          </button>
        </div>
      )}

      <FeedbackForm
        form={form}
        onChange={setForm}
        disabled={!status?.linked || submitting}
        onPreview={handlePreview}
        error={formError}
      />

      {device && (
        <DeviceCodeModal
          device={device}
          onClose={() => setDevice(null)}
          onLinked={async () => {
            setDevice(null);
            await refreshStatus();
          }}
          error={deviceError}
          setError={setDeviceError}
        />
      )}

      {preview && (
        <PreviewModal
          markdown={preview}
          submitting={submitting}
          onCancel={() => setPreview(null)}
          onSubmit={handleSubmit}
        />
      )}
    </div>
  );
}

interface ConnectBoxProps {
  status: FeedbackStatus | null;
  statusError: string | null;
  onLogin: () => void;
  onLogout: () => void;
}

function ConnectBox({ status, statusError, onLogin, onLogout }: ConnectBoxProps) {
  if (statusError) {
    return (
      <div className="rounded bg-surface p-4 ring-1 ring-red-900/60">
        <p className="text-sm text-red-300">{statusError}</p>
      </div>
    );
  }
  if (!status) {
    return (
      <div className="rounded bg-surface p-4 text-sm text-slate-400">
        Lade Status …
      </div>
    );
  }
  if (status.linked) {
    return (
      <div className="flex items-center justify-between rounded bg-surface p-4">
        <div>
          <p className="text-sm text-slate-300">
            Verbunden mit GitHub
            {status.login && (
              <>
                {" als "}
                <span className="font-mono text-slate-100">@{status.login}</span>
              </>
            )}
            .
          </p>
        </div>
        <button
          onClick={onLogout}
          className="rounded bg-slate-700 px-3 py-1.5 text-sm hover:bg-slate-600"
        >
          Trennen
        </button>
      </div>
    );
  }
  return (
    <div className="flex items-center justify-between rounded bg-surface p-4">
      <div>
        <p className="text-sm text-slate-300">Nicht mit GitHub verbunden.</p>
        {status.reason && (
          <p className="mt-1 text-xs text-slate-500">{status.reason}</p>
        )}
      </div>
      <button
        onClick={onLogin}
        className="rounded bg-accent px-3 py-1.5 text-sm text-white hover:bg-accent/80"
      >
        Mit GitHub verbinden
      </button>
    </div>
  );
}

interface FormProps {
  form: FormState;
  onChange: (f: FormState) => void;
  disabled: boolean;
  onPreview: () => void;
  error: string | null;
}

function FeedbackForm({ form, onChange, disabled, onPreview, error }: FormProps) {
  function update<K extends keyof FormState>(key: K, value: FormState[K]) {
    onChange({ ...form, [key]: value });
  }
  const canPreview =
    !disabled && form.title.trim().length > 0 && form.description.trim().length > 0;

  return (
    <div className={`space-y-4 rounded bg-surface p-4 ${disabled ? "opacity-60" : ""}`}>
      <div>
        <label className="mb-1 block text-xs uppercase tracking-wide text-slate-500">
          Titel
        </label>
        <input
          type="text"
          value={form.title}
          onChange={(e) => update("title", e.target.value)}
          disabled={disabled}
          maxLength={200}
          placeholder="Kurze Zusammenfassung des Problems"
          className="w-full rounded bg-slate-900/60 px-3 py-2 text-sm text-slate-100 ring-1 ring-slate-700 focus:outline-none focus:ring-accent"
        />
      </div>

      <div className="grid grid-cols-2 gap-3">
        <div>
          <label className="mb-1 block text-xs uppercase tracking-wide text-slate-500">
            Kategorie
          </label>
          <select
            value={form.category}
            onChange={(e) => update("category", e.target.value as FeedbackCategory)}
            disabled={disabled}
            className="w-full rounded bg-slate-900/60 px-2 py-2 text-sm text-slate-100 ring-1 ring-slate-700 focus:outline-none focus:ring-accent"
          >
            {CATEGORY_LABELS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        </div>
        <div>
          <label className="mb-1 block text-xs uppercase tracking-wide text-slate-500">
            Schweregrad
          </label>
          <select
            value={form.severity}
            onChange={(e) => update("severity", e.target.value as FeedbackSeverity)}
            disabled={disabled}
            className="w-full rounded bg-slate-900/60 px-2 py-2 text-sm text-slate-100 ring-1 ring-slate-700 focus:outline-none focus:ring-accent"
          >
            {SEVERITY_LABELS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        </div>
      </div>

      <TextArea
        label="Beschreibung"
        value={form.description}
        onChange={(v) => update("description", v)}
        disabled={disabled}
        placeholder="Was ist passiert?"
        required
      />
      <TextArea
        label="Erwartetes Verhalten"
        value={form.expected}
        onChange={(v) => update("expected", v)}
        disabled={disabled}
        placeholder="Was hättest du erwartet?"
      />
      <TextArea
        label="Tatsächliches Verhalten"
        value={form.actual}
        onChange={(v) => update("actual", v)}
        disabled={disabled}
        placeholder="Was hat tatsächlich passiert?"
      />
      <TextArea
        label="Schritte zur Reproduktion"
        value={form.repro}
        onChange={(v) => update("repro", v)}
        disabled={disabled}
        placeholder="1. ...\n2. ...\n3. ..."
        rows={4}
      />

      <div className="rounded bg-slate-900/40 px-3 py-2">
        <label className="flex items-center gap-2 text-sm text-slate-300">
          <input
            type="checkbox"
            checked={form.include_log}
            onChange={(e) => update("include_log", e.target.checked)}
            disabled={disabled}
            className="h-4 w-4 accent-accent"
          />
          Anwendungslog anhängen
        </label>
        {form.include_log && (
          <div className="mt-2 flex items-center gap-2 pl-6 text-sm text-slate-400">
            <span>Zeitraum:</span>
            <select
              value={form.log_window}
              onChange={(e) =>
                update("log_window", e.target.value as FeedbackLogWindow)
              }
              disabled={disabled}
              className="rounded bg-slate-900/60 px-2 py-1 text-sm text-slate-100 ring-1 ring-slate-700 focus:outline-none focus:ring-accent"
            >
              {WINDOW_LABELS.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
          </div>
        )}
        <p className="mt-2 pl-6 text-xs text-slate-500">
          Debug-Einträge und Fenstertitel werden vor dem Upload entfernt. Im
          Vorschau-Schritt kannst du den fertigen Inhalt prüfen.
        </p>
      </div>

      {error && (
        <div className="rounded bg-red-900/40 px-3 py-2 text-sm text-red-200">
          {error}
        </div>
      )}

      <div className="flex justify-end">
        <button
          onClick={onPreview}
          disabled={!canPreview}
          className="rounded bg-accent px-4 py-2 text-sm text-white hover:bg-accent/80 disabled:opacity-40"
        >
          Vorschau
        </button>
      </div>
    </div>
  );
}

interface TextAreaProps {
  label: string;
  value: string;
  onChange: (v: string) => void;
  disabled?: boolean;
  placeholder?: string;
  rows?: number;
  required?: boolean;
}

function TextArea({
  label,
  value,
  onChange,
  disabled,
  placeholder,
  rows = 3,
  required,
}: TextAreaProps) {
  return (
    <div>
      <label className="mb-1 block text-xs uppercase tracking-wide text-slate-500">
        {label}
        {required && <span className="ml-1 text-red-400">*</span>}
      </label>
      <textarea
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        placeholder={placeholder}
        rows={rows}
        className="w-full rounded bg-slate-900/60 px-3 py-2 font-sans text-sm text-slate-100 ring-1 ring-slate-700 focus:outline-none focus:ring-accent"
      />
    </div>
  );
}

interface DeviceModalProps {
  device: FeedbackDeviceCode;
  onClose: () => void;
  onLinked: () => void;
  error: string | null;
  setError: (s: string | null) => void;
}

function DeviceCodeModal({
  device,
  onClose,
  onLinked,
  error,
  setError,
}: DeviceModalProps) {
  const [interval, setInterval] = useState(device.interval_seconds);
  const cancelled = useRef(false);

  useEffect(() => {
    cancelled.current = false;
    let timer: number | null = null;
    async function poll() {
      if (cancelled.current) return;
      try {
        const res = await api.feedbackPollDeviceLogin();
        if (cancelled.current) return;
        switch (res.status) {
          case "linked":
            onLinked();
            return;
          case "pending":
            timer = window.setTimeout(poll, interval * 1000);
            return;
          case "slow_down": {
            const next = res.interval && res.interval > interval ? res.interval : interval + 5;
            setInterval(next);
            timer = window.setTimeout(poll, next * 1000);
            return;
          }
          case "expired":
            setError("Der Login-Code ist abgelaufen — bitte erneut starten.");
            return;
          case "denied":
            setError("Zugriff wurde abgelehnt.");
            return;
          case "error":
          default:
            setError(res.error ?? "Unbekannter Fehler beim Login.");
            return;
        }
      } catch (e) {
        if (!cancelled.current) setError(String(e));
      }
    }
    timer = window.setTimeout(poll, interval * 1000);
    return () => {
      cancelled.current = true;
      if (timer != null) window.clearTimeout(timer);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [device.user_code]);

  const expires = new Date(device.expires_at);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 px-4">
      <div className="w-full max-w-md rounded-lg bg-surface p-5 shadow-xl ring-1 ring-slate-700">
        <h3 className="text-lg font-semibold text-slate-100">
          Mit GitHub verbinden
        </h3>
        <ol className="mt-3 space-y-2 text-sm text-slate-300">
          <li>
            1. Öffne{" "}
            <a
              href={device.verification_uri}
              target="_blank"
              rel="noreferrer"
              className="text-accent underline underline-offset-2 hover:opacity-80"
            >
              {device.verification_uri}
            </a>{" "}
            in deinem Browser.
          </li>
          <li>2. Trage diesen Code ein:</li>
        </ol>
        <div className="mt-3 flex items-center justify-center rounded bg-slate-900/60 p-4">
          <span className="select-all font-mono text-2xl tracking-widest text-slate-100">
            {device.user_code}
          </span>
        </div>
        <p className="mt-3 text-xs text-slate-500">
          Wartet auf Bestätigung … Code gültig bis {expires.toLocaleTimeString()}.
        </p>
        {error && (
          <div className="mt-3 rounded bg-red-900/40 px-3 py-2 text-sm text-red-200">
            {error}
          </div>
        )}
        <div className="mt-4 flex justify-end">
          <button
            onClick={() => {
              cancelled.current = true;
              onClose();
            }}
            className="rounded bg-slate-700 px-3 py-1.5 text-sm hover:bg-slate-600"
          >
            Abbrechen
          </button>
        </div>
      </div>
    </div>
  );
}

interface PreviewProps {
  markdown: string;
  submitting: boolean;
  onCancel: () => void;
  onSubmit: () => void;
}

function PreviewModal({ markdown, submitting, onCancel, onSubmit }: PreviewProps) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 px-4">
      <div className="flex max-h-[80vh] w-full max-w-3xl flex-col rounded-lg bg-surface p-5 shadow-xl ring-1 ring-slate-700">
        <h3 className="text-lg font-semibold text-slate-100">Vorschau</h3>
        <p className="mt-1 text-xs text-slate-400">
          So wird das Issue auf GitHub aussehen (Markdown-Quelltext).
        </p>
        <pre className="mt-3 flex-1 overflow-auto rounded bg-slate-900/70 p-3 font-mono text-xs leading-relaxed text-slate-200">
          {markdown}
        </pre>
        <div className="mt-4 flex justify-end gap-2">
          <button
            onClick={onCancel}
            disabled={submitting}
            className="rounded bg-slate-700 px-3 py-1.5 text-sm hover:bg-slate-600 disabled:opacity-50"
          >
            Zurück
          </button>
          <button
            onClick={onSubmit}
            disabled={submitting}
            className="rounded bg-emerald-700 px-3 py-1.5 text-sm text-white hover:bg-emerald-600 disabled:opacity-50"
          >
            {submitting ? "Sende …" : "Issue erstellen"}
          </button>
        </div>
      </div>
    </div>
  );
}
