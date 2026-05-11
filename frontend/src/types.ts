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
  created_at: string;
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

export interface AppConfig {
  tracking: TrackingConfig;
  personio: PersonioConfig;
  entra: EntraConfig;
  quick_tag: QuickTagConfig;
  communication: CommunicationConfig;
  work_schedule: WorkScheduleConfig;
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
