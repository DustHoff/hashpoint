import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import "./index.css";
import { installGlobalHandlers, log } from "./lib/log";

installGlobalHandlers();

class ErrorBoundary extends React.Component<
  { children: React.ReactNode },
  { error: Error | null }
> {
  state: { error: Error | null } = { error: null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error, info: React.ErrorInfo) {
    log.error("react boundary", {
      message: error.message,
      stack: error.stack,
      componentStack: info.componentStack,
    });
  }

  render() {
    if (this.state.error) {
      return (
        <div className="m-6 rounded bg-red-900/40 p-4 text-sm text-red-200">
          <div className="font-semibold">Render-Fehler</div>
          <pre className="mt-2 whitespace-pre-wrap text-xs">
            {this.state.error.message}
          </pre>
          <button
            onClick={() => this.setState({ error: null })}
            className="mt-3 rounded bg-slate-700 px-3 py-1 text-xs hover:bg-slate-600"
          >
            Erneut versuchen
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}

ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>
    <ErrorBoundary>
      <App />
    </ErrorBoundary>
  </React.StrictMode>,
);
