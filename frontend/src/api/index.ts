// Centralized Wails API layer per CLAUDE.md §9: components must never call
// `window.go.*` directly.

import type {
  AppConfig,
  AvailablePluginEntry,
  EntraStatus,
  FeedbackDeviceCode,
  FeedbackInput,
  FeedbackPollResult,
  FeedbackStatus,
  FeedbackSubmitResult,
  ImportResult,
  OnCallDocDraft,
  OnCallDocView,
  OnCallListFilter,
  PersonioStatus,
  PluginConfigView,
  PluginInfo,
  PluginOrderGroup,
  ProcessTrack,
  QuickTagSlot,
  Rule,
  SyncPreflight,
  SyncResult,
  Tag,
  TagBlock,
  VersionInfo,
} from "../types";

// Wails injects bindings under window.go.<package>.<Type>.<Method>.
type GoFn<T = unknown> = (...args: unknown[]) => Promise<T>;

interface WailsBridge {
  app?: {
    App?: Record<string, GoFn>;
  };
}

interface WailsRuntime {
  EventsOn(name: string, callback: (...payload: unknown[]) => void): () => void;
  EventsOff(name: string, ...names: string[]): void;
}

declare global {
  interface Window {
    go?: WailsBridge;
    runtime?: WailsRuntime;
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

  // Process tracks (raw window-focus events) ----------------------------
  processTracksByDay: (dayISO: string) =>
    bridge().ProcessTracksByDay(dayISO) as Promise<ProcessTrack[]>,
  processTracksBetween: (from: string, to: string) =>
    bridge().ProcessTracksBetween(from, to) as Promise<ProcessTrack[]>,

  // Tag blocks (tagging spans) ------------------------------------------
  tagBlocksByDay: (dayISO: string) =>
    bridge().TagBlocksByDay(dayISO) as Promise<TagBlock[]>,
  tagBlocksBetween: (from: string, to: string) =>
    bridge().TagBlocksBetween(from, to) as Promise<TagBlock[]>,

  createManualTagRange: (
    startISO: string,
    endISO: string,
    tagId: number,
    description: string,
  ) =>
    bridge().CreateManualTagRange(
      startISO,
      endISO,
      tagId,
      description,
    ) as Promise<void>,

  setTagBlockDescription: (id: number, description: string) =>
    bridge().SetTagBlockDescription(id, description) as Promise<void>,

  setTagBlockTag: (id: number, tagId: number) =>
    bridge().SetTagBlockTag(id, tagId) as Promise<void>,

  resizeTagBlock: (id: number, startISO: string, endISO: string) =>
    bridge().ResizeTagBlock(id, startISO, endISO) as Promise<void>,

  deleteTagBlock: (id: number) =>
    bridge().DeleteTagBlock(id) as Promise<void>,

  deleteTagBlocks: (ids: number[]) =>
    bridge().DeleteTagBlocks(ids) as Promise<number>,

  // Tags ----------------------------------------------------------------
  listTags: () => bridge().ListTags() as Promise<Tag[]>,
  createTag: (t: Partial<Tag>) => bridge().CreateTag(t) as Promise<Tag>,
  updateTag: (t: Tag) => bridge().UpdateTag(t) as Promise<void>,
  deleteTag: (id: number) => bridge().DeleteTag(id) as Promise<void>,

  // Rules ---------------------------------------------------------------
  listRules: () => bridge().ListRules() as Promise<Rule[]>,
  createRule: (r: Partial<Rule>) => bridge().CreateRule(r) as Promise<Rule>,
  updateRule: (r: Rule) => bridge().UpdateRule(r) as Promise<void>,
  deleteRule: (id: number) => bridge().DeleteRule(id) as Promise<void>,

  testRule: (r: Partial<Rule>, dayISO: string) =>
    bridge().TestRule(r, dayISO) as Promise<
      Array<{
        track_id: number;
        process_name: string;
        window_title: string;
        matched: boolean;
      }>
    >,

  // Tracking ------------------------------------------------------------
  pauseTracking: () => bridge().PauseTracking() as Promise<void>,
  resumeTracking: () => bridge().ResumeTracking() as Promise<void>,
  isTrackingPaused: () => bridge().IsTrackingPaused() as Promise<boolean>,

  // Manual tagging ------------------------------------------------------
  startManualTag: (tagId: number, description: string) =>
    bridge().StartManualTag(tagId, description) as Promise<void>,
  stopManualTag: () => bridge().StopManualTag() as Promise<void>,
  isManualTagActive: () =>
    bridge().IsManualTagActive() as Promise<[number, boolean]>,

  // Quick-tag-picker ----------------------------------------------------
  quickTagSlots: () => bridge().QuickTagSlots() as Promise<QuickTagSlot[]>,
  quickTagSelect: (tagId: number) =>
    bridge().QuickTagSelect(tagId) as Promise<void>,
  quickTagDismiss: () => bridge().QuickTagDismiss() as Promise<void>,

  // Sync ----------------------------------------------------------------
  syncDay: (dayISO: string) => bridge().SyncDay(dayISO) as Promise<SyncResult>,
  syncRange: (from: string, to: string) =>
    bridge().SyncRange(from, to) as Promise<SyncResult>,
  preflightSyncDay: (dayISO: string) =>
    bridge().PreflightSyncDay(dayISO) as Promise<SyncPreflight>,
  importPersonioDay: (dayISO: string) =>
    bridge().ImportPersonioDay(dayISO) as Promise<ImportResult>,

  // Settings ------------------------------------------------------------
  getConfig: () => bridge().GetConfig() as Promise<AppConfig>,
  saveConfig: (c: AppConfig) => bridge().SaveConfig(c) as Promise<void>,

  // Personio ------------------------------------------------------------
  personioStatus: () => bridge().PersonioStatus() as Promise<PersonioStatus>,
  personioCheck: () => bridge().PersonioCheck() as Promise<PersonioStatus>,
  personioLogin: () => bridge().PersonioLogin() as Promise<void>,
  personioLogout: () => bridge().PersonioLogout() as Promise<void>,

  // Entra ID ------------------------------------------------------------
  entraStatus: () => bridge().EntraStatus() as Promise<EntraStatus>,
  entraLogin: () => bridge().EntraLogin() as Promise<void>,
  entraLogout: () => bridge().EntraLogout() as Promise<void>,

  // On-call documentation ----------------------------------------------
  onCallDocList: (filter: OnCallListFilter = {}) =>
    bridge().OnCallDocList(filter) as Promise<OnCallDocView[]>,
  onCallDocGet: (id: number) =>
    bridge().OnCallDocGet(id) as Promise<OnCallDocView | null>,
  onCallDocSave: (id: number, draft: OnCallDocDraft) =>
    bridge().OnCallDocSave(id, draft) as Promise<void>,
  onCallDocSubmit: (id: number) =>
    bridge().OnCallDocSubmit(id) as Promise<void>,
  onCallDocDismiss: (id: number) =>
    bridge().OnCallDocDismiss(id) as Promise<void>,

  // Plugin admin -------------------------------------------------------
  pluginList: () => bridge().PluginList() as Promise<PluginInfo[]>,
  pluginGetConfig: (name: string) =>
    bridge().PluginGetConfig(name) as Promise<PluginConfigView>,
  pluginSetConfig: (name: string, fields: Record<string, string>) =>
    bridge().PluginSetConfig(name, fields) as Promise<void>,
  pluginSetSecret: (name: string, key: string, value: string) =>
    bridge().PluginSetSecret(name, key, value) as Promise<void>,
  pluginDeleteSecret: (name: string, key: string) =>
    bridge().PluginDeleteSecret(name, key) as Promise<void>,
  pluginSetEnabled: (name: string, enabled: boolean) =>
    bridge().PluginSetEnabled(name, enabled) as Promise<void>,
  pluginReload: (name: string) =>
    bridge().PluginReload(name) as Promise<void>,
  // Asks the host to re-pull the named plugin's tag catalogue via the
  // tag_provider capability. Returns the count of newly-created tags
  // (existing paths are no-ops per the user-tag-wins rule).
  pluginRefreshTags: (name: string) =>
    bridge().PluginRefreshTags(name) as Promise<number>,
  // Live-queries every running tag_provider plugin for its order
  // catalogue. Used by the Tag-Manager combobox; never cached, so a
  // newly-installed plugin shows up on the next tab open.
  listPluginOrders: () =>
    bridge().ListPluginOrders() as Promise<PluginOrderGroup[] | null>,
  pluginListAvailable: () =>
    bridge().PluginListAvailable() as Promise<AvailablePluginEntry[] | null>,
  pluginInstall: (sourcePlugin: string, name: string) =>
    bridge().PluginInstall(sourcePlugin, name) as Promise<void>,
  pluginUpdate: (sourcePlugin: string, name: string) =>
    bridge().PluginUpdate(sourcePlugin, name) as Promise<void>,
  pluginUninstall: (sourcePlugin: string, name: string) =>
    bridge().PluginUninstall(sourcePlugin, name) as Promise<void>,

  // Log forwarding ------------------------------------------------------
  logFrontend: (
    level: "debug" | "info" | "warn" | "error",
    message: string,
    fields?: Record<string, unknown>,
  ) =>
    bridge().LogFrontend(level, message, fields ?? {}) as Promise<void>,

  // Help / user docs ----------------------------------------------------
  listUserDocs: () =>
    bridge().ListUserDocs() as Promise<Array<{ slug: string; title: string }>>,
  getUserDoc: (slug: string) =>
    bridge().GetUserDoc(slug) as Promise<string>,

  // Feedback (GitHub issue submitter) -----------------------------------
  feedbackStatus: () => bridge().FeedbackStatus() as Promise<FeedbackStatus>,
  feedbackStartDeviceLogin: () =>
    bridge().FeedbackStartDeviceLogin() as Promise<FeedbackDeviceCode>,
  feedbackPollDeviceLogin: () =>
    bridge().FeedbackPollDeviceLogin() as Promise<FeedbackPollResult>,
  feedbackLogout: () => bridge().FeedbackLogout() as Promise<void>,
  feedbackPreview: (input: FeedbackInput) =>
    bridge().FeedbackPreview(input) as Promise<string>,
  feedbackSubmit: (input: FeedbackInput) =>
    bridge().FeedbackSubmit(input) as Promise<FeedbackSubmitResult>,

  // Wails event subscription -------------------------------------------
  // Returns an unsubscribe function. The handler receives nothing useful
  // for our picker events (they carry no payload).
  onEvent: (name: string, handler: () => void): (() => void) => {
    const rt = window.runtime;
    if (!rt) {
      // No-op when running outside Wails (e.g. Vite dev preview).
      return () => {};
    }
    return rt.EventsOn(name, () => handler());
  },

  // Like onEvent but forwards the first payload argument. Used for events
  // that carry data (e.g. startup-sync result).
  onEventPayload: <T = unknown>(
    name: string,
    handler: (payload: T) => void,
  ): (() => void) => {
    const rt = window.runtime;
    if (!rt) {
      return () => {};
    }
    return rt.EventsOn(name, (...args: unknown[]) => {
      handler(args[0] as T);
    });
  },
};
