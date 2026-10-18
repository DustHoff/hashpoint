// Personio may save a day but attach policy warnings (e.g. overtime
// approaching). The backend confirms those like the Personio web app does and
// reports them in Result.Warnings; this renders them as a banner suffix.
export function warningSuffix(warnings: string[] | null | undefined): string {
  if (!warnings || warnings.length === 0) return "";
  return ` Hinweis von Personio: ${warnings.join("; ")}`;
}
