// Centralized Wails API layer per CLAUDE.md §9: components must never call
// `window.go.*` directly.

import type {
  AppConfig,
  FocusBlock,
  PersonioStatus,
  Rule,
  SyncResult,
  Tag,
  VersionInfo,
} from "../types";

// Wails injects bindings under window.go.<package>.<Type>.<Method>.
type GoFn<T = unknown> = (...args: unknown[]) => Promise<T>;

interface WailsBridge {
  app?: {
    App?: Record<string, GoFn>;
  };
}

declare global {
  interface Window {
    go?: WailsBridge;
  }
}

function bridge(): Record<string, GoFn> {
  const a = window.go?.app?.App;
  if (!a) {
    throw new Error("Wails bindings not available — running in browser?");
  }
  return a;
}

export const api = {
  version: () => bridge().Version() as Promise<VersionInfo>,

  blocksByDay: (dayISO: string) =>
    bridge().BlocksByDay(dayISO) as Promise<FocusBlock[]>,

  blocksBetween: (from: string, to: string) =>
    bridge().BlocksBetween(from, to) as Promise<FocusBlock[]>,

  assignTag: (blockIds: number[], tagId: number) =>
    bridge().AssignTag(blockIds, tagId) as Promise<void>,

  // tagId: 0 clears the tag, -1 leaves the tag untouched (description-only update).
  // rangeStart/rangeEnd (RFC3339, "" = ignore): if a tag is being set, any
  // sub-intervals of the range not covered by a non-idle block in blockIds are
  // filled with synthetic placeholder blocks. When the tag is cleared, any
  // placeholder blocks among blockIds are deleted.
  assignTagAndDescription: (
    blockIds: number[],
    tagId: number,
    description: string,
    rangeStart: string,
    rangeEnd: string,
  ) =>
    bridge().AssignTagAndDescription(
      blockIds,
      tagId,
      description,
      rangeStart,
      rangeEnd,
    ) as Promise<void>,

  setBlockDescription: (id: number, description: string) =>
    bridge().SetBlockDescription(id, description) as Promise<void>,

  splitBlock: (id: number, atISO: string) =>
    bridge().SplitBlock(id, atISO) as Promise<FocusBlock>,

  updateBlock: (b: FocusBlock) =>
    bridge().UpdateBlock(b) as Promise<void>,

  deleteBlock: (id: number) => bridge().DeleteBlock(id) as Promise<void>,

  // Bulk-delete: removes every block in `ids` (real and placeholder alike).
  // Returns the number of rows actually deleted.
  deleteBlocks: (ids: number[]) => bridge().DeleteBlocks(ids) as Promise<number>,

  listTags: () => bridge().ListTags() as Promise<Tag[]>,
  createTag: (t: Partial<Tag>) => bridge().CreateTag(t) as Promise<Tag>,
  updateTag: (t: Tag) => bridge().UpdateTag(t) as Promise<void>,
  deleteTag: (id: number) => bridge().DeleteTag(id) as Promise<void>,

  listRules: () => bridge().ListRules() as Promise<Rule[]>,
  createRule: (r: Partial<Rule>) => bridge().CreateRule(r) as Promise<Rule>,
  updateRule: (r: Rule) => bridge().UpdateRule(r) as Promise<void>,
  deleteRule: (id: number) => bridge().DeleteRule(id) as Promise<void>,

  testRule: (r: Partial<Rule>, dayISO: string) =>
    bridge().TestRule(r, dayISO) as Promise<
      Array<{
        block_id: number;
        process_name: string;
        window_title: string;
        matched: boolean;
      }>
    >,

  applyRuleToHistory: (id: number) =>
    bridge().ApplyRuleToHistory(id) as Promise<number>,

  pauseTracking: () => bridge().PauseTracking() as Promise<void>,
  resumeTracking: () => bridge().ResumeTracking() as Promise<void>,
  isTrackingPaused: () => bridge().IsTrackingPaused() as Promise<boolean>,

  syncDay: (dayISO: string) => bridge().SyncDay(dayISO) as Promise<SyncResult>,
  syncRange: (from: string, to: string) =>
    bridge().SyncRange(from, to) as Promise<SyncResult>,

  // Settings ---------------------------------------------------------------
  getConfig: () => bridge().GetConfig() as Promise<AppConfig>,
  saveConfig: (c: AppConfig) => bridge().SaveConfig(c) as Promise<void>,

  // Personio interactive login --------------------------------------------
  personioStatus: () => bridge().PersonioStatus() as Promise<PersonioStatus>,
  personioLogin: () => bridge().PersonioLogin() as Promise<void>,
  personioLogout: () => bridge().PersonioLogout() as Promise<void>,

  // Log forwarding --------------------------------------------------------
  logFrontend: (
    level: "debug" | "info" | "warn" | "error",
    message: string,
    fields?: Record<string, unknown>,
  ) =>
    bridge().LogFrontend(level, message, fields ?? {}) as Promise<void>,
};
