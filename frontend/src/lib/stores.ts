// owner: muswood | Email: mumu920@outlook.com
import { derived, get, writable } from "svelte/store";
import { migrateLegacyConversations, pruneConversationRecords, remoteTargetKey, titleForMessages } from "./aiConversations";
import { defaultTerminalHighlightRules, type TerminalHighlightRule } from "./terminalPrivacy";

function persistedWritable<T>(key: string, initial: T) {
  let value = initial;
  if (typeof localStorage !== "undefined") {
    try {
      const stored = localStorage.getItem(key);
      if (stored !== null) value = JSON.parse(stored) as T;
    } catch { /* use the default when stored settings are invalid */ }
  }
  const store = writable<T>(value);
  if (typeof localStorage !== "undefined") {
    store.subscribe(next => {
      try { localStorage.setItem(key, JSON.stringify(next)); } catch { /* storage may be unavailable */ }
    });
  }
  return store;
}

export type TabType = "ssh" | "telnet" | "raw" | "serial" | "ai" | "sftp" | "welcome" | "settings" | "portforward";

export interface Tab {
  id: string;
  type: TabType;
  name: string;
  connected: boolean;
  reconnecting?: boolean;
  sessionId?: string;
  connectionId?: string;
  groupColor?: string;
  terminalTheme?: string;
  serialBaudRate?: number;
  serialDataBits?: number;
  serialStopBits?: number;
  serialParity?: string;
  serialAutoReconnect?: boolean;
  showSFTP?: boolean;
  showAI?: boolean;
  serialConfig?: {
    portName: string;
    baudRate: number;
    dataBits: number;
    stopBits: number;
    parity: string;
    autoReconnect?: boolean;
  };
}

export interface Connection {
  id: string;
  name: string;
	protocol?: "ssh" | "telnet" | "raw" | "serial";
  host: string;
  port: number;
  username: string;
  groupId: string;
  groupColor?: string;
  connected: boolean;
  starred?: boolean;
  authMethod?: string;
  password?: string;
  privateKeyPath?: string;
  certificatePath?: string;
  jumpHost?: string;
  proxyType?: string;
  proxyHost?: string;
  proxyUsername?: string;
  proxyCommand?: string;
  startupCmd?: string;
  encoding?: string;
  keepAliveSeconds?: number;
  terminalTheme?: string;
  serialBaudRate?: number;
  serialDataBits?: number;
  serialStopBits?: number;
  serialParity?: string;
  serialAutoReconnect?: boolean;
}

export interface ConnectionGroup {
  id: string;
  name: string;
  items: Connection[];
}

export interface AIMessage {
  role: "user" | "assistant" | "system";
  content: string;
  commands?: string[];
  timestamp: number;
}

export interface AIConversation {
  id: string;
  targetKey?: string;
  title: string;
  messages: AIMessage[];
  createdAt: number;
  updatedAt: number;
}

export type AIConversationMap = Record<string, AIMessage[]>;
export { migrateLegacyConversations, pruneConversationRecords, remoteTargetKey };

export interface TerminalCommandRequest {
  command: string;
  execute: boolean;
  targetTabId?: string;
}

export interface TerminalCaptureRequest {
  id: string;
  targetTabId?: string;
  createdAt: number;
}

export interface TerminalCaptureSnapshot {
  requestId: string;
  tabId: string;
  sessionId?: string;
  name?: string;
  content: string;
  timestamp: number;
  error?: string;
}

export interface FileInfo {
  name: string;
  path: string;
  size: number;
  isDir: boolean;
  perm: string;
  modTime: string;
}

export const tabs = writable<Tab[]>([
  { id: "welcome", type: "welcome", name: "欢迎", connected: false },
]);

export const activeTabId = writable<string>("welcome");

export const connectionGroups = writable<ConnectionGroup[]>([]);

export function aiConversationIdForTab(tab?: Pick<Tab, "id" | "type">) {
  if (!tab) return "global";
  if (tab.type === "ssh" || tab.type === "telnet" || tab.type === "raw" || tab.type === "serial") {
    return `terminal:${tab.id}`;
  }
  if (tab.type === "ai") return `ai:${tab.id}`;
  return "global";
}

export const activeAIConversationId = writable<string>("global");
export const aiHistoryRetentionDays = persistedWritable<number>("gossh.aiHistoryRetentionDays", 7);

function normalizedRetentionDays(value: unknown) {
  const n = Number(value);
  if (!Number.isFinite(n)) return 7;
  return Math.max(0, Math.min(3650, Math.round(n)));
}

function loadAIConversations() {
  if (typeof localStorage === "undefined") return [];
  try {
    const stored = localStorage.getItem("gossh.aiConversations");
    if (!stored) return [];
    return migrateLegacyConversations(JSON.parse(stored)) as AIConversation[];
  } catch {
    return [];
  }
}

function pruneAIConversations(conversations: AIConversation[], retentionDays: number) {
  return pruneConversationRecords(conversations, retentionDays) as AIConversation[];
}

function createAIConversationsStore() {
  let retentionDays = normalizedRetentionDays(get(aiHistoryRetentionDays));
  let value = retentionDays <= 0 ? [] : pruneAIConversations(loadAIConversations(), retentionDays);
  const store = writable<AIConversation[]>(value);

  function commit(next: AIConversation[]) {
    value = pruneAIConversations(next, retentionDays);
    store.set(value);
    if (typeof localStorage !== "undefined") {
      try {
        localStorage.setItem("gossh.aiConversations", JSON.stringify(retentionDays <= 0 ? [] : value));
      } catch {}
    }
  }

  aiHistoryRetentionDays.subscribe(next => {
    retentionDays = normalizedRetentionDays(next);
    commit(value);
  });
  if (typeof window !== "undefined") {
    window.setInterval(() => commit(value), 60 * 60 * 1000);
  }

  return {
    subscribe: store.subscribe,
    set: commit,
    update(updater: (conversations: AIConversation[]) => AIConversation[]) {
      commit(updater(value));
    },
  };
}

export const aiConversations = createAIConversationsStore();

export function listAIConversations(targetKey?: string, includeUnassigned = false) {
  return get(aiConversations)
    .filter(conversation => conversation.targetKey === targetKey || (includeUnassigned && !conversation.targetKey))
    .sort((a, b) => b.updatedAt - a.updatedAt);
}

function newConversationId() {
  return `ai:${Date.now()}:${Math.random().toString(36).slice(2, 8)}`;
}

export function createAIConversation(targetKey?: string) {
  const now = Date.now();
  const conversation: AIConversation = {
    id: newConversationId(), targetKey, title: "新会话", messages: [], createdAt: now, updatedAt: now,
  };
  aiConversations.update(items => [...items, conversation]);
  activeAIConversationId.set(conversation.id);
  return conversation;
}

export function selectAIConversation(id: string) {
  if (get(aiConversations).some(conversation => conversation.id === id)) {
    activeAIConversationId.set(id);
    return true;
  }
  return false;
}

function ensureActiveAIConversation() {
  const id = get(activeAIConversationId);
  if (get(aiConversations).some(item => item.id === id)) return id;
  const now = Date.now();
  aiConversations.update(items => [...items, { id, title: "新会话", messages: [], createdAt: now, updatedAt: now }]);
  return id;
}

export const aiMessages = {
  subscribe: derived(
    [aiConversations, activeAIConversationId],
    ([$aiConversations, $activeAIConversationId]) => $aiConversations.find(item => item.id === $activeAIConversationId)?.messages || [],
  ).subscribe,
  set(messages: AIMessage[]) {
    const id = ensureActiveAIConversation();
    aiConversations.update(conversations => {
      const next = conversations.map(item => item.id === id ? { ...item, messages: messages.slice(-100), title: titleForMessages(messages), updatedAt: Date.now() } : item);
      return next;
    });
  },
  update(updater: (messages: AIMessage[]) => AIMessage[]) {
    const id = ensureActiveAIConversation();
    aiConversations.update(conversations => conversations.map(item => {
      if (item.id !== id) return item;
      const messages = updater(item.messages || []).slice(-100);
      return { ...item, messages, title: titleForMessages(messages), updatedAt: Date.now() };
    }));
  },
};

const aiLoadingByConversation = writable<Record<string, boolean>>({});

export const aiLoading = {
  subscribe: derived(
    [aiLoadingByConversation, activeAIConversationId],
    ([$aiLoadingByConversation, $activeAIConversationId]) => Boolean($aiLoadingByConversation[$activeAIConversationId]),
  ).subscribe,
  set(loading: boolean) {
    const id = get(activeAIConversationId);
    aiLoadingByConversation.update(items => ({ ...items, [id]: loading }));
  },
  update(updater: (loading: boolean) => boolean) {
    const id = get(activeAIConversationId);
    aiLoadingByConversation.update(items => ({ ...items, [id]: updater(Boolean(items[id])) }));
  },
};
export const terminalCommand = writable<TerminalCommandRequest | null>(null);
export const terminalCaptureRequest = writable<TerminalCaptureRequest | null>(null);
export const terminalCaptureSnapshots = writable<Record<string, TerminalCaptureSnapshot>>({});
export const splitPaneIds = persistedWritable<string[]>("gossh.workspace.splitPaneIds", []);
export const splitLayout = persistedWritable<"vertical" | "horizontal">("gossh.workspace.splitLayout", "vertical");
export const sftpPanelHeight = persistedWritable<number>("gossh.workspace.sftpPanelHeight", 220);
export const aiPanelWidth = persistedWritable<number>("gossh.workspace.aiPanelWidth", 320);
export const syncInputEnabled = writable<boolean>(false);
export const recoverableConnectionIds = persistedWritable<string[]>("gossh.workspace.recoverableConnectionIds", []);

export const sftpLocalFiles = writable<FileInfo[]>([]);
export const sftpRemoteFiles = writable<FileInfo[]>([]);

export const showSidebar = writable<boolean>(true);
export const showSFTP = writable<boolean>(false);
export const settingsSection = writable<string>("general");

export const terminalTheme = writable<string>("deepSpace");
export const terminalCursorColor = persistedWritable<string>("gossh.terminal.cursorColor", "");
export const terminalBackgroundImage = persistedWritable<string>("gossh.terminal.backgroundImage", "");
export const terminalBackgroundOpacity = persistedWritable<number>("gossh.terminal.backgroundOpacity", 35);
export const terminalFontFamily = persistedWritable<string>(
  "gossh.terminal.fontFamily",
  '"Consolas", "Cascadia Code", "JetBrains Mono", monospace',
);
export const terminalFontSize = persistedWritable<number>("gossh.terminal.fontSize", 14);
export const terminalFontWeight = persistedWritable<number>("gossh.terminal.fontWeight", 400);
export const terminalLineHeight = persistedWritable<number>("gossh.terminal.lineHeight", 1.3);
export const terminalLetterSpacing = persistedWritable<number>("gossh.terminal.letterSpacing", 0);
export const terminalCursorStyle = persistedWritable<"bar" | "block" | "underline">(
  "gossh.terminal.cursorStyle",
  "bar",
);
export const terminalCursorBlink = persistedWritable<boolean>("gossh.terminal.cursorBlink", true);
export const terminalScrollback = persistedWritable<number>("gossh.terminal.scrollback", 5000);
export const terminalHighlightEnabled = persistedWritable<boolean>("gossh.terminal.highlightEnabled", true);
export const terminalHighlightRules = persistedWritable<TerminalHighlightRule[]>(
  "gossh.terminal.highlightRules",
  defaultTerminalHighlightRules,
);
export type UITheme = "cyber-night" | "aurora-light";
export const uiTheme = persistedWritable<UITheme>("gossh.uiTheme", "cyber-night");
export type Language = "zh-CN" | "en-US";
export const language = persistedWritable<Language>("gossh.language", "zh-CN");
export const connRefreshTrigger = writable<number>(0);

export const statusMessage = writable<string>("就绪");
