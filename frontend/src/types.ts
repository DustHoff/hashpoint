// Mirrors the Go domain types in internal/storage. Keep in sync manually
// (Wails generates wailsjs/go bindings, but we re-declare richer types here).

export interface ProcessTrack {
  id: number;
  process_name: string;
  process_path?: string;
  window_title: string;
  start_time: string; // RFC3339 UTC
  end_time?: string;
  duration_sec: number;
  is_idle: boolean;
  is_communication: boolean;
}

export interface TagBlock {
  id: number;
  tag_id: number;
  description?: string;
  start_time: string; // RFC3339 UTC
  end_time?: string;
  duration_sec: number;
  is_manual: boolean;
  personio_id?: string;
  synced_at?: string;
}

export interface Tag {
  id: number;
  parent_id?: number;
  name: string;
  description?: string;
  color?: string;
  personio_project_id?: string;
  personio_activity_id?: string;
  sync_to_personio: boolean;
  // Optional external-order mapping. Either a name supplied by a
  // tag_provider plugin's order catalogue or a user-entered freitext
  // string. Empty / undefined ⇒ no mapping.
  order_name?: string;
  created_at: string;
}

// PluginOrder mirrors sdk.Order. Description is rendered as helper text
// in the Tag-Manager combobox but is never persisted on the tag.
export interface PluginOrder {
  ID: string;
  Name: string;
  Description: string;
}

// PluginOrderGroup mirrors pluginhost.PluginOrders — one group per
// running tag_provider plugin in the result of api.listPluginOrders().
export interface PluginOrderGroup {
  plugin_name: string;
  orders: PluginOrder[] | null;
}

export type MatchField = "process_name" | "window_title" | "both";
export type MatchType = "contains" | "equals" | "regex";

export interface Rule {
  id: number;
  match_field: MatchField;
  match_type: MatchType;
  pattern: string;
  tag_id: number;
  description?: string;
  priority: number;
  enabled: boolean;
  created_at: string;
}

export interface SyncResult {
  Periods: number;
  BlocksProcessed: number;
  BlocksSkipped: number;
  Errors: string[] | null;
}

// Returned by App.PreflightSyncDay. The frontend opens the override/import
// modal whenever existing_periods is non-empty (or trackable is false).
export interface SyncPreflight {
  day: string; // YYYY-MM-DD (local)
  day_id: string;
  state: string;
  trackable: boolean;
  existing_periods: PreflightPeriod[];
  local_block_count: number;
  local_duration_sec: number;
}

export interface PreflightPeriod {
  id: string;
  start: string; // local-naive "YYYY-MM-DDTHH:MM:SS"
  end: string;
  type: string; // "work"
  comment: string;
  project_id?: string;
  tag_name?: string;
}

export interface ImportResult {
  periods_considered: number;
  blocks_created: number;
  periods_skipped: number;
  fallback_tag_used: boolean;
  errors?: string[] | null;
}

export interface VersionInfo {
  version: string;
  commit: string;
  build_date: string;
}

export interface TrackingConfig {
  poll_interval_sec: number;
  idle_threshold_min: number;
  enabled: boolean;
  tag_block_granularity_min: number;
}

export interface PersonioConfig {
  tenant: string;
  // When true, the periodic session probe (PersonioCheck) will trigger an
  // interactive CDP login as soon as it detects an expired session. When
  // false, the user has to launch the login manually from the badge.
  // Plugin-initiated session requests (RequestPersonioSession) always
  // trigger a login regardless of this flag.
  auto_relogin: boolean;
}

export interface EntraConfig {
  client_id: string;
  tenant_id: string;
}

export interface QuickTagConfig {
  enabled: boolean;
  hotkey: string;
}

export interface CommunicationConfig {
  process_names: string[];
  title_exclude_phrases: string[];
}

// Canonical English short-name keys for the seven weekdays. Matches the Go
// side's WorkScheduleConfig.WorkDays vocabulary; the UI maps these to
// localized German labels at render time.
export type WorkDay = "Mon" | "Tue" | "Wed" | "Thu" | "Fri" | "Sat" | "Sun";

export interface WorkScheduleConfig {
  start_hour: number; // 0..23, inclusive
  end_hour: number; // 1..24, exclusive; must be > start_hour
  work_days: WorkDay[];
}

// On-call ("Rufbereitschaft") feature configuration. TagIDs lists the
// root tags whose blocks (including their descendants) are eligible for
// auto-generated on-call documentation when they fall into off-hours.
// Empty list = feature dormant.
export interface OnCallConfig {
  tag_ids: number[];
}

export interface AppConfig {
  tracking: TrackingConfig;
  personio: PersonioConfig;
  entra: EntraConfig;
  quick_tag: QuickTagConfig;
  communication: CommunicationConfig;
  work_schedule: WorkScheduleConfig;
  on_call: OnCallConfig;
}

export interface QuickTagSlot {
  index: number;
  tag_id: number;
  label: string;
  color?: string;
  is_active: boolean;
}

export interface PersonioStatus {
  has_session: boolean;
  tenant: string;
  employee_id: number;
  captured_at?: string;
  valid: boolean;
  checked_at?: string;
  reason?: string;
}

export interface EntraStatus {
  configured: boolean;
  has_account: boolean;
  username?: string;
  home_account_id?: string;
  tenant_id?: string;
  client_id?: string;
  reason?: string;
}

// On-call ("Rufbereitschaft") --------------------------------------------

// Status discriminators must match storage.OnCallDocStatus + the literal
// strings the Go side serialises. Treat as opaque on the frontend; the
// roll-up is computed server-side from the per-plugin Submissions rows.
export type OnCallDocStatus =
  | "draft"
  | "pending"
  | "submitted"
  | "partial"
  | "failed";

export type OnCallIncidentType =
  | ""
  | "planned_maintenance"
  | "service_disruption";

export interface OnCallSubmissionView {
  plugin_name: string;
  status: "pending" | "submitted" | "failed";
  external_ref?: string;
  external_url?: string;
  last_error?: string;
  submitted_at?: string;
}

export interface OnCallDocView {
  id: number;
  block_id: number;
  start_time: string; // RFC3339 UTC
  end_time: string;
  tag_id: number;
  tag_name: string;
  tag_at_creation: number;
  stale: boolean;
  application: string;
  incident_type: OnCallIncidentType;
  solution: string;
  status: OnCallDocStatus;
  submissions?: OnCallSubmissionView[];
}

export interface OnCallDocDraft {
  application: string;
  incident_type: OnCallIncidentType;
  solution: string;
}

export interface OnCallListFilter {
  status?: OnCallDocStatus;
  from?: string;
  to?: string;
  include_stale?: boolean;
}

// Per-plugin submit result event payload.
export interface OnCallSubmitResultPayload {
  doc_id: number;
  plugin_name: string;
  status: "submitted" | "failed";
  external_ref?: string;
  external_url?: string;
  error_message?: string;
}

export interface OnCallDocChangedPayload {
  doc_id: number;
}

// Plugin admin -----------------------------------------------------------

// PluginState mirrors internal/plugin.PluginState. `needs_config` is the
// state the host parks a plugin in when its manifest declares required
// fields the user has not yet filled in — the subprocess is not running
// and the inbox skips it for fan-outs.
export type PluginState =
  | "running"
  | "needs_config"
  | "failed"
  | "disabled";

// Mirrors the host-side sdk.Capability constants. Plain string union so
// the frontend stays decoupled from the wire enum; the backend serialises
// these verbatim.
export type PluginCapability =
  | "oncall_documentation"
  | "plugin_management"
  | "process_autotag"
  | "off_hours_provider"
  | "tag_provider";

// FieldType discriminates input rendering + persistence strategy.
// `password` values are encrypted at rest and never round-tripped to
// the UI — the API surfaces a `secrets_set` flag so the form can hint
// "saved" without exposing the cleartext.
export type FieldType = "text" | "password" | "boolean";

export interface ManifestField {
  label: string;
  type: FieldType;
  required: boolean;
  default?: string;
}

export interface ManifestConfigSchema {
  fields: Record<string, ManifestField>;
}

export interface PluginInfo {
  name: string;
  version: string;
  description: string;
  capabilities: PluginCapability[];
  state: PluginState;
  last_error?: string;
  enabled: boolean;
  missing_fields?: string[];
  config_schema: ManifestConfigSchema;
}

// PluginConfigView is the payload returned by api.pluginGetConfig.
// `fields` carries the plain values (text / boolean as strings);
// `secrets_set[key] === true` means a password is persisted but its
// cleartext is intentionally absent so the UI can't leak it.
export interface PluginConfigView {
  fields: Record<string, string>;
  secrets_set: Record<string, boolean>;
}

// AvailablePluginEntry mirrors internal/plugin.AvailablePluginEntry.
// `installed_version` is the version currently loaded on disk (empty
// when the plugin is not installed). `source_plugin` is the name of
// the running plugin_management plugin that surfaced this entry —
// Install/Update/Uninstall RPCs route back through it.
export interface AvailablePluginEntry {
  name: string;
  version: string;
  description: string;
  source_plugin: string;
  installed_version: string;
}

// Feedback (GitHub issue submitter) ---------------------------------------

// FeedbackStatus mirrors app.FeedbackStatusDTO. `linked` is the source
// of truth for whether the form's Submit button is enabled; `reason`
// renders next to the login button when not linked (e.g. "erneute
// Anmeldung erforderlich" after a refresh-token expiry).
export interface FeedbackStatus {
  linked: boolean;
  login?: string;
  reason?: string;
  checked_at: string;
}

// FeedbackDeviceCode is the public face of a Device-Flow start. The
// underlying device_code stays in the backend; the UI only ever
// renders user_code and the verification URI.
export interface FeedbackDeviceCode {
  user_code: string;
  verification_uri: string;
  interval_seconds: number;
  expires_at: string;
}

// FeedbackPollStatus enumerates every poll outcome the backend may
// return. The UI maps each to one of: keep polling (pending /
// slow_down), transition (linked), or stop with error (expired /
// denied / error).
export type FeedbackPollStatus =
  | "pending"
  | "linked"
  | "slow_down"
  | "expired"
  | "denied"
  | "error";

export interface FeedbackPollResult {
  status: FeedbackPollStatus;
  interval?: number;
  error?: string;
}

export type FeedbackCategory = "bug" | "feature" | "question";
export type FeedbackSeverity = "low" | "medium" | "high" | "critical";
export type FeedbackLogWindow = "today" | "1h" | "24h";

export interface FeedbackInput {
  title: string;
  category: FeedbackCategory;
  severity: FeedbackSeverity;
  description: string;
  expected: string;
  actual: string;
  repro: string;
  include_log: boolean;
  log_window: FeedbackLogWindow;
}

export interface FeedbackSubmitResult {
  number: number;
  html_url: string;
}
