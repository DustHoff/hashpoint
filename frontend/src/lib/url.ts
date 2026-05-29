// safeExternalHref returns url only when it is an absolute http(s) URL, and
// undefined otherwise. Dangerous schemes (javascript:, data:, vbscript:,
// file:, …) and relative/garbage values are rejected, so an
// attacker-influenced value — a plugin's submission URL, a crafted GitHub
// device-flow response — cannot execute script in the privileged Wails
// renderer when it is rendered into an anchor href.
export function safeExternalHref(
  url: string | undefined | null,
): string | undefined {
  if (!url) return undefined;
  try {
    const parsed = new URL(url);
    if (parsed.protocol === "http:" || parsed.protocol === "https:") {
      return url;
    }
    return undefined;
  } catch {
    return undefined;
  }
}
