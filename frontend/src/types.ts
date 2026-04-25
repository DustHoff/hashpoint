// Mirrors the Go domain types in internal/storage. Keep in sync manually
// (Wails generates wailsjs/go bindings, but we re-declare richer types here).

export interface FocusBlock {
  id: number;
  process_name: string;
  process_path?: string;
  window_title: string;
  start_time: string; // RFC3339 UTC
  end_time?: string;
  duration_sec: number;
  is_idle: boolean;
  tag_id?: number;
  auto_tagged: boolean;
  description?: string;
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

export interface VersionInfo {
  version: string;
  commit: string;
  build_date: string;
}
