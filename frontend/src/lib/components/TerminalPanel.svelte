<!-- owner: muswood | Email: mumu920@outlook.com -->
<script lang="ts">
  import { Terminal } from "@xterm/xterm";
  import { FitAddon } from "@xterm/addon-fit";
  import { SearchAddon } from "@xterm/addon-search";
  import { SerializeAddon } from "@xterm/addon-serialize";
  import { WebglAddon } from "@xterm/addon-webgl";
  import { WebLinksAddon } from "@xterm/addon-web-links";
  import { UnicodeGraphemesAddon } from "@xterm/addon-unicode-graphemes";
  import { Search, Copy, ClipboardPaste, Download, Settings2, X, ChevronUp, ChevronDown, FileCode2, Sparkles, FolderOpen, Palette, ScanSearch, PanelsTopLeft, Upload, Download as DownloadIcon, Image as ImageIcon, Trash2 } from "lucide-svelte";
  import Zmodem from "zmodem.js";
  import { XModemReceiver, XModemSender, YModemReceiver, YModemSender, type TransferResult } from "$lib/terminalTransfer";
  import { terminalThemes, themeLabels, themeNames } from "$lib/themes";
  import { ShellIntegrationParser, type CommandBlock } from "$lib/shellIntegration";
  import { compressTerminalBackgroundImage } from "$lib/terminalBackground";
  import type { AIMessage } from "$lib/stores";
  import { sanitizeTerminalOutput, highlightTerminalOutput } from "$lib/terminalPrivacy";
  import {
    terminalTheme, terminalCursorColor, terminalFontFamily, terminalFontSize, terminalFontWeight,
    terminalLineHeight, terminalLetterSpacing, terminalCursorStyle, terminalCursorBlink,
    terminalScrollback, terminalHighlightEnabled, terminalHighlightRules,
    terminalBackgroundImage, terminalBackgroundOpacity,
    tabs, activeTabId, terminalCommand, aiMessages, aiLoading, splitPaneIds, syncInputEnabled,
    activeAIConversationId, aiConversationIdForTab, terminalCaptureRequest, terminalCaptureSnapshots, language,
  } from "$lib/stores";
  import { t } from "$lib/i18n";
  import { onMount, onDestroy } from "svelte";
  import { EventsOn } from "../../../wailsjs/runtime/runtime";
  import { SSHWrite, SSHWriteBase64, SSHRead, SSHReadSessionLog, SSHResize, SSHDisconnect, SSHAppendSessionLogBase64, TCPWrite, TCPWriteBase64, TCPRead, TCPResize, TCPDisconnect, TCPConnect, ListConnections, AgentStart, SetConnectionTerminalTheme } from "../../../wailsjs/go/main/App";
  import { connectSSHWithHostTrust } from "$lib/sshConnect";

  let { sessionId = "default", transport = "ssh", visible = true } = $props<{ sessionId?: string; transport?: "ssh" | "tcp"; visible?: boolean }>();

  let containerEl: HTMLDivElement;
  let term = $state<Terminal | null>(null);
  let fitAddon: FitAddon | null = null;
  let searchAddon: SearchAddon | null = null;
  let serializeAddon: SerializeAddon | null = null;
  let webglAddon: WebglAddon | null = null;
  let webLinksAddon: WebLinksAddon | null = null;
  let unicodeAddon: UnicodeGraphemesAddon | null = null;
  let showSearch = $state(false);
  let searchQuery = $state("");
  let searchResult = $state({ index: 0, count: 0 });
  let searchCaseSensitive = $state(false);
  let searchRegex = $state(false);
  let rendererMode = $state("Canvas");
  let error = $state("");
  let currentTheme = $state("deepSpace");
  let cols = 80;
  let rows = 24;
  let pollTimer: ReturnType<typeof setInterval> | undefined;
  let fitTimer: ReturnType<typeof setTimeout> | undefined;
  let resizeObserver: ResizeObserver | undefined;
  let pollInFlight = false;
  let stopOutputEvents: (() => void) | undefined;
  let disposed = false;
  const shellParser = new ShellIntegrationParser();
  let shellStatus = $state("等待 Shell Integration");
  let currentCwd = $state("");
  let failedBlock = $state<CommandBlock | null>(null);
  let reconnecting = $state(false);
  let diagnosing = $state(false);
  let analyzingTerminal = $state(false);
  let analysisEnabled = $state(false);
  let currentSessionTab = $derived($tabs.find(tab => tab.sessionId === sessionId));
  let aiPanelVisible = $derived(currentSessionTab?.showAI === true);
  let showThemePicker = $state(false);
  let backgroundError = $state("");
  let showTransferMenu = $state(false);
  let selectionContextMenu = $state<{ x: number; y: number; text: string } | null>(null);
  let zmodemInput: HTMLInputElement;
  let zmodemSentry: any = null;
  let zmodemFiles: File[] = [];
  let zmodemStatus = $state("");
  let zmodemProgress = $state(0);
  type PacketProtocol = "xmodem" | "ymodem";
  let packetInput: HTMLInputElement;
  let backgroundInput: HTMLInputElement;
  let packetUploadProtocol: PacketProtocol = "xmodem";
  let packetTransfer = $state<{ push(data: Uint8Array): void; cancel(): void } | null>(null);
  let packetTransferLabel = $state("XModem");
  let packetStatus = $state("");
  let packetProgress = $state(0);
  let sessionLogQueue: Promise<void> = Promise.resolve();

  class TerminalTextLogFilter {
    private state: "text" | "escape" | "csi" | "string" | "stringEscape" = "text";

    filter(bytes: Uint8Array): Uint8Array {
      const text: number[] = [];
      for (const value of bytes) {
        if (this.state === "text") {
          if (value === 0x1b) {
            this.state = "escape";
          } else if (value === 0x09 || value === 0x0a || value === 0x0d || value >= 0x20) {
            text.push(value);
          }
          continue;
        }
        if (this.state === "escape") {
          this.state = value === 0x5b ? "csi" : (value === 0x5d || value === 0x50 || value === 0x58 || value === 0x5e || value === 0x5f) ? "string" : "text";
          continue;
        }
        if (this.state === "csi") {
          if (value >= 0x40 && value <= 0x7e) this.state = "text";
          continue;
        }
        if (this.state === "string") {
          if (value === 0x07) {
            this.state = "text";
          } else if (value === 0x1b) {
            this.state = "stringEscape";
          }
          continue;
        }
        this.state = value === 0x5c ? "text" : "string";
      }
      return new Uint8Array(text);
    }
  }
  const sessionLogFilter = new TerminalTextLogFilter();

  $effect(() => {
    const nextTheme = currentSessionTab?.terminalTheme || $terminalTheme || "deepSpace";
    if (nextTheme !== currentTheme) {
      currentTheme = nextTheme;
      applyTerminalOptions();
    }
  });
  $effect(() => {
    const backgroundImage = $terminalBackgroundImage;
    const backgroundOpacity = $terminalBackgroundOpacity;
    void backgroundImage;
    void backgroundOpacity;
    applyTerminalOptions();
  });
  terminalFontFamily.subscribe(() => applyTerminalOptions());
  terminalFontSize.subscribe(() => applyTerminalOptions());
  terminalFontWeight.subscribe(() => applyTerminalOptions());
  terminalLineHeight.subscribe(() => applyTerminalOptions());
  terminalLetterSpacing.subscribe(() => applyTerminalOptions());
  terminalCursorStyle.subscribe(() => applyTerminalOptions());
  terminalCursorBlink.subscribe(() => applyTerminalOptions());
  terminalCursorColor.subscribe(() => applyTerminalOptions());
  terminalScrollback.subscribe(() => applyTerminalOptions());

  function applyTerminalOptions() {
    if (!term) return;
    const t = terminalThemes[currentTheme] || terminalThemes.deepSpace;
    try {
      term.options.theme = {
        ...t,
        cursor: $terminalCursorColor || t.cursor,
        background: $terminalBackgroundImage ? colorWithAlpha(t.background || "#0d1117", 0.72) : t.background,
      };
      term.options.fontFamily = $terminalFontFamily;
      term.options.fontSize = $terminalFontSize;
      term.options.fontWeight = $terminalFontWeight as any;
      term.options.lineHeight = $terminalLineHeight;
      term.options.letterSpacing = $terminalLetterSpacing;
      term.options.cursorStyle = $terminalCursorStyle;
      term.options.cursorBlink = $terminalCursorBlink;
      term.options.scrollback = Math.max(100, Math.min(100000, Math.round(Number($terminalScrollback) || 5000)));
      scheduleVisibleFit(0);
    } catch (e) {}
  }

  function fitVisibleTerminal() {
    if (!visible || !containerEl?.clientWidth || !containerEl?.clientHeight) return;
    fitAddon?.fit();
  }

  function scheduleVisibleFit(delay = 50) {
    if (fitTimer) clearTimeout(fitTimer);
    fitTimer = undefined;
    if (!visible) return;
    fitTimer = setTimeout(() => {
      fitTimer = undefined;
      fitVisibleTerminal();
    }, delay);
  }

  function stopPolling() {
    if (pollTimer) clearInterval(pollTimer);
    pollTimer = undefined;
  }

  function startPolling() {
    stopPolling();
    if (!visible) return;
    void readPendingOutput();
    pollTimer = setInterval(() => void readPendingOutput(), 100);
  }

  function consumeOutputBytes(bytes: Uint8Array) {
    if (!bytes.length || !term) return;
    if (packetTransfer) { packetTransfer.push(bytes); return; }
    if (zmodemSentry) { zmodemSentry.consume(bytes); return; }
    const rendered = highlightTerminalOutput(new TextDecoder().decode(bytes), $terminalHighlightRules, $terminalHighlightEnabled);
    writeVisibleBytes(new TextEncoder().encode(rendered));
  }

  function startOutputEvents() {
    stopOutputEvents?.();
    const eventName = `${transport === "tcp" ? "tcp" : "ssh"}:output:${sessionId}`;
    stopOutputEvents = EventsOn(eventName, (encoded: string) => {
      if (!visible || disposed || typeof encoded !== "string") return;
      try {
        const raw = atob(encoded);
        const bytes = new Uint8Array(raw.length);
        for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i);
        consumeOutputBytes(bytes);
      } catch (e) {
        error = `读取终端事件失败: ${e instanceof Error ? e.message : String(e)}`;
      }
    });
  }

  function initTerminal() {
    if (!containerEl) { error = "DOM 未就绪"; return; }
    const baseTheme = terminalThemes[currentTheme] || terminalThemes.deepSpace;
    const theme = {
      ...baseTheme,
      cursor: $terminalCursorColor || baseTheme.cursor,
      background: $terminalBackgroundImage ? colorWithAlpha(baseTheme.background || "#0d1117", 0.72) : baseTheme.background,
    };
    try {
      term = new Terminal({
        allowProposedApi: true,
        theme,
        fontFamily: $terminalFontFamily,
        fontSize: $terminalFontSize,
        fontWeight: $terminalFontWeight as any,
        lineHeight: $terminalLineHeight,
        letterSpacing: $terminalLetterSpacing,
        cursorBlink: $terminalCursorBlink,
        cursorStyle: $terminalCursorStyle,
        scrollback: Math.max(100, Math.min(100000, Math.round(Number($terminalScrollback) || 5000))), fontWeightBold: 600,
      });
      fitAddon = new FitAddon();
      searchAddon = new SearchAddon({ highlightLimit: 2000 });
      serializeAddon = new SerializeAddon();
      unicodeAddon = new UnicodeGraphemesAddon();
      webLinksAddon = new WebLinksAddon((_event, uri) => openTerminalLink(uri));
      term.loadAddon(fitAddon);
      term.loadAddon(searchAddon);
      term.loadAddon(serializeAddon);
      term.loadAddon(unicodeAddon);
      term.loadAddon(webLinksAddon);
      try {
        webglAddon = new WebglAddon();
        term.loadAddon(webglAddon);
        webglAddon.onContextLoss(() => {
          webglAddon?.dispose();
          webglAddon = null;
          rendererMode = "Canvas";
        });
        rendererMode = "WebGL";
      } catch {
        webglAddon = null;
        rendererMode = "Canvas";
      }
      searchAddon.onDidChangeResults(({ resultIndex, resultCount }) => {
        searchResult = { index: resultIndex >= 0 ? resultIndex + 1 : 0, count: resultCount };
      });
      term.open(containerEl);

      zmodemSentry = new Zmodem.Sentry({
        to_terminal: (octets: Uint8Array | number[]) => writeVisibleBytes(new Uint8Array(octets)),
        sender: (octets: Uint8Array | number[]) => { void writeBinary(new Uint8Array(octets)); },
        on_detect: (detection: any) => startZmodemSession(detection),
        on_retract: () => { zmodemStatus = ""; zmodemProgress = 0; },
      });
      setTimeout(() => {
        fitVisibleTerminal();
        if (visible) term?.focus();
      }, 50);

      term.onData((data) => { void writeUserInput(data); });

      term.onResize(async ({ cols: c, rows: r }) => {
        if (!visible || !containerEl?.clientWidth || !containerEl?.clientHeight) return;
        cols = c; rows = r;
        if (transport === "ssh") try { await SSHResize(sessionId, c, r); } catch (e) {}
        if (transport === "tcp") try { await TCPResize(sessionId, c, r); } catch (e) {}
      });

      resizeObserver = new ResizeObserver(() => scheduleVisibleFit());
      resizeObserver.observe(containerEl);
      containerEl.addEventListener("paste", handlePaste, { capture: true });
      containerEl.addEventListener("contextmenu", handleTerminalContextMenu);
    } catch (e: any) {
      error = "终端初始化失败: " + (e?.message || String(e));
    }
  }

  async function writeUserInput(data: string) {
    try {
      if (transport === "tcp") await TCPWrite(sessionId, data); else await SSHWrite(sessionId, data);
      if ($syncInputEnabled && currentSessionTab?.id === $activeTabId) {
        const peers = $tabs.filter(tab => $splitPaneIds.includes(tab.id) && tab.id !== currentSessionTab?.id && tab.connected && tab.sessionId);
        await Promise.all(peers.map(tab => tab.type === "ssh"
          ? SSHWrite(tab.sessionId!, data)
          : TCPWrite(tab.sessionId!, data)));
      }
    } catch (e: any) {
      if (!disposed) term?.writeln(`\r\n[write failed: ${e?.message || String(e)}]`);
    }
  }

  function waitForReconnect(ms: number) {
    return new Promise<void>(resolve => window.setTimeout(resolve, ms));
  }

  async function reconnectSession() {
    if (reconnecting || disposed) return;
    const tab = currentSessionTab;
    const tabId = tab?.id;
    const connectionId = tab?.connectionId;
    if (!tabId || !connectionId) return;

    reconnecting = true;
    tabs.update(items => items.map(item => item.id === tabId ? { ...item, reconnecting: true } : item));
    error = "连接已断开，正在自动重连...";
    term?.writeln("\r\n\x1b[1;33m连接已断开，正在自动重连...\x1b[0m");
    if (transport === "ssh") await SSHDisconnect(sessionId).catch(() => {});
    else await TCPDisconnect(sessionId).catch(() => {});

    for (let attempt = 1; !disposed; attempt++) {
      try {
        const nextSession = transport === "ssh"
          ? await connectSSHWithHostTrust(connectionId, cols, rows)
          : await reconnectTCP(connectionId, tab?.type || "raw", tabId);
        if (disposed || !$tabs.some(item => item.id === tabId)) {
          if (transport === "ssh") await SSHDisconnect(nextSession).catch(() => {});
          else await TCPDisconnect(nextSession).catch(() => {});
          return;
        }
        tabs.update(items => items.map(item => item.id === tabId
          ? { ...item, sessionId: nextSession, connected: true, reconnecting: false }
          : item));
        reconnecting = false;
        error = "";
        term?.writeln(`\x1b[1;32m重连成功（第 ${attempt} 次）\x1b[0m`);
        return;
      } catch (e: any) {
        error = `重连中（第 ${attempt} 次）: ${e?.message || String(e)}`;
        await waitForReconnect(Math.min(10000, 1000 + attempt * 1000));
      }
    }
    reconnecting = false;
  }

  async function reconnectTCP(connectionId: string, protocol: string, tabId: string) {
    const connections = JSON.parse(await ListConnections() || "[]");
    const connection = connections.find((item: any) => item.id === connectionId);
    if (!connection) throw new Error("保存的 TCP 连接记录不存在");
    return TCPConnect({
      id: `${tabId}-reconnect`,
      host: connection.host,
      port: Number(connection.port || (protocol === "telnet" ? 23 : 0)),
      protocol,
    });
  }

  function handlePaste(event: ClipboardEvent) {
    const text = event.clipboardData?.getData("text/plain") || "";
    if (!text || !needsPasteConfirmation(text)) return;
    event.preventDefault();
    event.stopPropagation();
    const lines = text.split(/\r\n|\r|\n/).length;
    if (window.confirm(`即将粘贴 ${text.length} 个字符、${lines} 行到当前终端。确认发送？`)) {
      void writeUserInput(text);
    }
  }

  function needsPasteConfirmation(text: string) {
    if (text.length > 200 || /\r|\n/.test(text)) return true;
    return /\b(rm\s+-rf|mkfs|dd\s+if=|shutdown|reboot|poweroff|chmod\s+-R\s+777)\b|: *\(\) *\{/i.test(text);
  }

  function openTerminalLink(uri: string) {
    if (!/^https?:\/\//i.test(uri)) return;
    window.open(uri, "_blank", "noopener,noreferrer");
  }

  function searchOptions(incremental = false) {
    return {
      regex: searchRegex,
      caseSensitive: searchCaseSensitive,
      incremental,
      decorations: {
        matchBackground: "#334155",
        matchBorder: "#64748b",
        matchOverviewRuler: "#64748b",
        activeMatchBackground: "#4f46e5",
        activeMatchBorder: "#a5b4fc",
        activeMatchColorOverviewRuler: "#a5b4fc",
      },
    };
  }

  function handleSearch() {
    showSearch = !showSearch;
    if (showSearch && searchQuery && searchAddon) searchAddon.findNext(searchQuery, searchOptions(true));
    if (!showSearch) clearSearch();
  }

  function clearSearch() {
    searchAddon?.clearDecorations();
    searchResult = { index: 0, count: 0 };
  }

  function doSearch() {
    if (!searchAddon || !searchQuery) {
      clearSearch();
      return;
    }
    searchAddon.findNext(searchQuery, searchOptions(true));
  }

  function findNext() { if (searchAddon && searchQuery) searchAddon.findNext(searchQuery, searchOptions(false)); }
  function findPrevious() { if (searchAddon && searchQuery) searchAddon.findPrevious(searchQuery, searchOptions(false)); }

  function handleSearchKeydown(e: KeyboardEvent) {
    if (e.key === "Enter") {
      e.preventDefault();
      e.shiftKey ? findPrevious() : findNext();
    } else if (e.key === "Escape") {
      showSearch = false;
      clearSearch();
    }
  }

  function copySelection() { if (term) { const s = term.getSelection(); if (s) navigator.clipboard.writeText(s); } }
  function copyContextSelection() {
    const selected = selectionContextMenu?.text || term?.getSelection() || "";
    selectionContextMenu = null;
    if (selected) void navigator.clipboard?.writeText(selected);
  }

  async function pasteFromClipboard() {
    selectionContextMenu = null;
    try {
      const text = await navigator.clipboard?.readText();
      if (!text) return;
      const lines = text.split(/\r\n|\r|\n/).length;
      if (needsPasteConfirmation(text) && !window.confirm(`即将粘贴 ${text.length} 个字符、${lines} 行到当前终端。确认发送？`)) return;
      await writeUserInput(text);
    } catch (e: any) {
      error = `读取剪贴板失败: ${e?.message || String(e)}`;
    }
  }
  function terminalPlainText() {
    if (!term) return;
    const lines: string[] = [];
    const buffer = term.buffer.active;
    for (let i = 0; i < buffer.length; i++) {
      lines.push(buffer.getLine(i)?.translateToString(true) || "");
    }
    return lines.join("\n");
  }

  function downloadFile(content: string, name: string, type: string) {
    const blob = new Blob([content], { type });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a"); a.href = url; a.download = name;
    a.click(); URL.revokeObjectURL(url);
  }

  function saveLog() {
    const content = terminalPlainText();
    if (content === undefined) return;
    downloadFile(`GoSSH Terminal Log\n=================\n\n${content}\n`, "terminal-log.txt", "text/plain");
  }

  function saveHtmlSnapshot() {
    if (!serializeAddon) return saveLog();
    const html = serializeAddon.serializeAsHTML({ scrollback: Math.max(100, Math.min(100000, Math.round(Number($terminalScrollback) || 5000))), includeGlobalBackground: true });
    downloadFile(`<!doctype html><meta charset="utf-8"><title>GoSSH Terminal Snapshot</title>${html}`, "terminal-snapshot.html", "text/html");
  }

  async function diagnoseFailedCommand() {
    if (!failedBlock || diagnosing || $aiLoading) return;
    diagnosing = true;
    const block = failedBlock;
    const context = sanitizeTerminalOutput([
      "这是一次由 Shell Integration 精确标记的失败命令。",
      `工作目录: ${block.cwd || "未提供"}`,
      `命令: ${block.command || "未提供"}`,
      `退出码: ${block.exitCode}`,
      `输出:\n${block.output || "未提供"}`,
      "请用中文说明可能原因，给出从低风险到高风险的检查步骤。若给出修复命令，放入代码块；不得假设用户已执行这些命令，且不要建议破坏性操作。",
    ].join("\n\n"));
    const userMessage: AIMessage = { role: "user", content: `分析失败命令：${sanitizeTerminalOutput(block.command || "(未提供)")}`, timestamp: Date.now() };
    activateTerminalAIConversation();
    aiMessages.update(messages => [...messages, userMessage].slice(-100));
    openAIPanel();
    aiLoading.set(true);
    try {
      await AgentStart({
        sessionId,
		transport: transport === "ssh" ? "ssh" : currentSessionTab?.type,
        tabId: currentSessionTab?.id || "",
        goal: "诊断一次失败的终端命令，并给出基于证据的低风险排查报告。",
        mode: "diagnose",
        context,
        history: [],
        autonomous: true,
      } as any);
    } catch (e: any) {
      const reply: AIMessage = { role: "assistant", content: `AI 分析失败：${e?.message || String(e)}`, timestamp: Date.now() };
      aiMessages.update(messages => [...messages, reply].slice(-100));
      aiLoading.set(false);
    } finally {
      diagnosing = false;
    }
  }

  function toggleAIDisplay() {
    if (!currentSessionTab?.id) return;
    activeTabId.set(currentSessionTab.id);
    tabs.update(items => items.map(tab => tab.id === currentSessionTab?.id
      ? { ...tab, showAI: !tab.showAI }
      : tab));
  }

  function openAIPanel() {
    if (!currentSessionTab?.id) return;
    activeTabId.set(currentSessionTab.id);
    tabs.update(items => items.map(tab => tab.id === currentSessionTab?.id
      ? { ...tab, showAI: true }
      : tab));
  }

  function appendAIMessage(message: AIMessage) {
    aiMessages.update(messages => [...messages, message].slice(-100));
  }

  async function startTerminalAgent(goal: string, mode: string, context: string) {
    try {
      await AgentStart({
        sessionId,
		transport: transport === "ssh" ? "ssh" : currentSessionTab?.type,
        tabId: currentSessionTab?.id || "",
        goal,
        mode,
        context,
        history: [],
        autonomous: true,
      } as any);
    } catch (e: any) {
      appendAIMessage({ role: "assistant", content: `AI 分析失败：${e?.message || String(e)}`, timestamp: Date.now() });
      aiLoading.set(false);
      throw e;
    }
  }

  function activateTerminalAIConversation() {
    activeAIConversationId.set(aiConversationIdForTab(currentSessionTab || {
      id: sessionId,
      type: transport === "ssh" ? "ssh" : "raw",
    }));
  }

  function publishTerminalCapture(requestId: string) {
    const tabId = currentSessionTab?.id || sessionId;
    const raw = terminalPlainText();
    const safeOutput = sanitizeTerminalOutput(raw || "").slice(-16000);
    terminalCaptureSnapshots.update(items => ({
      ...items,
      [requestId]: {
        requestId,
        tabId,
        sessionId,
        name: currentSessionTab?.name,
        content: safeOutput,
        timestamp: Date.now(),
        error: safeOutput.trim() ? undefined : "暂无终端输出可供分析",
      },
    }));
  }

  function openSelectionContextMenu(event: MouseEvent, text: string) {
    const menuWidth = 156;
    const menuHeight = 44;
    selectionContextMenu = {
      x: Math.min(event.clientX, Math.max(0, window.innerWidth - menuWidth - 8)),
      y: Math.min(event.clientY, Math.max(0, window.innerHeight - menuHeight - 8)),
      text,
    };
    showThemePicker = false;
    showTransferMenu = false;
  }

  function handleTerminalContextMenu(event: MouseEvent) {
    const selected = term?.getSelection() || "";
    if (!selected.trim()) {
      selectionContextMenu = null;
      return;
    }
    event.preventDefault();
    event.stopPropagation();
    openSelectionContextMenu(event, selected);
  }

  async function analyzeSelectedText() {
    if (analyzingTerminal || $aiLoading) return;
    const selected = selectionContextMenu?.text || term?.getSelection() || "";
    selectionContextMenu = null;
    const safeSelection = sanitizeTerminalOutput(selected).slice(-12000);
    if (!safeSelection.trim()) {
      error = "脱敏后没有可分析的选中内容";
      return;
    }
    analyzingTerminal = true;
    activateTerminalAIConversation();
    openAIPanel();
    aiLoading.set(true);
    appendAIMessage({
      role: "user",
      content: "分析选中的终端内容（内容已在本地脱敏）",
      timestamp: Date.now(),
    });
    try {
      const prompt = [
        "请分析下面用户在终端中选中的内容。重点指出错误、风险、可能原因和低风险排查步骤。",
        "输出已在本地移除常见密钥、口令、Token、私钥、邮箱和 IP 地址；不要尝试还原或猜测被隐藏的信息。",
        `当前目录: ${currentCwd || "未提供"}`,
        `Shell 状态: ${shellStatus}`,
        `选中内容（仅最近 ${safeSelection.length} 个字符）：\n${safeSelection}`,
      ].join("\n\n");
      await startTerminalAgent("分析用户选中的终端内容，并给出风险、原因和低风险排查报告。", "terminal_selection", prompt);
    } catch (e: any) {
      appendAIMessage({ role: "assistant", content: `AI 分析失败：${e?.message || String(e)}`, timestamp: Date.now() });
      aiLoading.set(false);
    } finally {
      analyzingTerminal = false;
    }
  }

  async function analyzeTerminalOutput() {
    if (analyzingTerminal || $aiLoading) return;
    let raw = terminalPlainText();
    if (transport === "ssh") {
      try {
        let offset = 0;
        let logBytes = new Uint8Array(0);
        for (let page = 0; page < 16; page++) {
          const result = await SSHReadSessionLog(sessionId, offset, 64 * 1024);
          const encoded = atob(result.data || "");
          const pageBytes = new Uint8Array(encoded.length);
          for (let i = 0; i < encoded.length; i++) pageBytes[i] = encoded.charCodeAt(i);
          logBytes = new Uint8Array([...logBytes, ...pageBytes]).slice(-16000);
          offset = result.nextOffset;
          if (result.eof || !pageBytes.length) break;
        }
        raw = new TextDecoder().decode(logBytes);
      } catch (e: any) {
        error = `读取会话日志失败: ${e?.message || String(e)}`;
        return;
      }
    }
    if (!raw?.trim()) {
      error = "暂无终端输出可供分析";
      return;
    }
    const safeOutput = sanitizeTerminalOutput(raw).slice(-16000);
    if (!safeOutput.trim()) {
      error = "脱敏后没有可分析的终端内容";
      return;
    }
    analyzingTerminal = true;
    activateTerminalAIConversation();
    openAIPanel();
    aiLoading.set(true);
    appendAIMessage({
      role: "user",
      content: "分析当前终端输出（内容已在本地脱敏）",
      timestamp: Date.now(),
    });
    try {
      const prompt = [
        "请分析下面的终端输出。重点指出错误、警告、可能原因和低风险排查步骤。",
        "输出已在本地移除常见密钥、口令、Token、私钥、邮箱和 IP 地址；不要尝试还原或猜测被隐藏的信息。",
        `终端输出（仅最近 ${safeOutput.length} 个字符）：\n${safeOutput}`,
      ].join("\n\n");
      await startTerminalAgent("分析当前终端输出，并给出错误、警告、原因和低风险排查报告。", "terminal_output", prompt);
    } catch (e: any) {
      appendAIMessage({ role: "assistant", content: `AI 分析失败：${e?.message || String(e)}`, timestamp: Date.now() });
      aiLoading.set(false);
    } finally {
      analyzingTerminal = false;
    }
  }

  function processShellIntegration(raw: string) {
    const blocks = shellParser.feed(raw);
    currentCwd = shellParser.getCwd();
    shellStatus = shellParser.isActive() ? (shellParser.getCommand() || "命令运行中") : "Shell Integration 已启用";
    for (const block of blocks) {
      shellStatus = block.exitCode === 0 ? "命令完成" : `命令失败 (退出码 ${block.exitCode})`;
      if (block.exitCode !== 0) failedBlock = block;
    }
  }
  function openSettings() {
    tabs.update(t => t.find(x => x.id === "settings")
      ? (activeTabId.set("settings"), t)
      : [...t, { id: "settings", type: "settings", name: "设置", connected: false }]
    );
    activeTabId.set("settings");
  }

  function toggleSFTP() {
	if (transport !== "ssh") return;
    const tabId = currentSessionTab?.id;
    if (!tabId) return;
    tabs.update(items => items.map(tab => tab.id === tabId
      ? { ...tab, showSFTP: !tab.showSFTP }
      : tab));
  }

  async function attachTmux() {
    if (transport !== "ssh") return;
    try {
      await SSHWrite(sessionId, "tmux new-session -A -s gossh\n");
    } catch (e: any) { error = `Tmux 附着失败: ${e?.message || String(e)}`; }
  }

  function toBase64(bytes: Uint8Array): string {
    let text = "";
    const size = 0x8000;
    for (let offset = 0; offset < bytes.length; offset += size) {
      text += String.fromCharCode(...bytes.subarray(offset, offset + size));
    }
    return btoa(text);
  }

  async function writeBinary(bytes: Uint8Array) {
    const encoded = toBase64(bytes);
    if (transport === "tcp") await TCPWriteBase64(sessionId, encoded);
    else await SSHWriteBase64(sessionId, encoded);
  }

  function writeVisibleBytes(bytes: Uint8Array) {
    if (!bytes.length) return;
    if (transport === "ssh") {
      const logBytes = sessionLogFilter.filter(bytes);
      if (logBytes.length) {
        const encoded = toBase64(logBytes);
        sessionLogQueue = sessionLogQueue
          .then(() => SSHAppendSessionLogBase64(sessionId, encoded))
          .catch(() => {});
      }
    }
    processShellIntegration(typeof TextDecoder !== "undefined" ? new TextDecoder().decode(bytes) : String.fromCharCode(...bytes));
    term?.write(bytes);
  }

  async function sendHexBytes() {
    const raw = window.prompt("HEX 字节，例如: 48 65 6c 6c 6f 0d 0a");
    if (!raw?.trim()) return;
    const parts = raw.trim().split(/[\s,;:-]+/).filter(Boolean);
    if (!parts.length || parts.some((part) => !/^[0-9a-fA-F]{1,2}$/.test(part))) {
      error = "HEX 格式无效：请输入 1-2 位十六进制字节，用空格、逗号或冒号分隔";
      return;
    }
    try {
      await writeBinary(new Uint8Array(parts.map((part) => Number.parseInt(part, 16))));
      error = "";
    } catch (e: any) {
      error = `HEX 发送失败: ${e?.message || String(e)}`;
    }
  }

  function startZmodemSession(detection: any) {
    const session = detection.confirm();
    if (session.type === "send") {
      if (!zmodemFiles.length) {
        zmodemStatus = "等待选择上传文件";
        session.close?.();
        return;
      }
      zmodemStatus = "正在上传";
      const total = zmodemFiles.reduce((sum, file) => sum + file.size, 0) || 1;
      let sent = 0;
      Zmodem.Browser.send_files(session, zmodemFiles, {
        on_progress: (_file: File, _transfer: any, chunk: Uint8Array) => { sent += chunk.length; zmodemProgress = Math.min(100, Math.round(sent * 100 / total)); },
      }).then(() => session.close()).then(() => {
        zmodemStatus = "上传完成"; zmodemProgress = 100; zmodemFiles = [];
      }).catch((e: any) => { zmodemStatus = `上传失败: ${e?.message || String(e)}`; });
      return;
    }
    zmodemStatus = "正在接收";
    session.on("offer", (transfer: any) => {
      const details = transfer.get_details();
      let received = 0;
      transfer.on("input", (chunk: Uint8Array) => {
        received += chunk.length;
        zmodemProgress = details.size ? Math.min(100, Math.round(received * 100 / details.size)) : 0;
      });
      transfer.accept().then(() => Zmodem.Browser.save_to_disk(transfer.get_payloads(), details.name));
    });
    session.on("session_end", () => { zmodemStatus = "下载完成"; zmodemProgress = 100; });
    session.start();
  }

  function chooseZmodemFiles() {
    showTransferMenu = false;
    zmodemInput?.click();
  }

  async function beginZmodemUpload(event: Event) {
    const files = Array.from((event.currentTarget as HTMLInputElement).files || []);
    if (!files.length) return;
    zmodemFiles = files;
    zmodemProgress = 0;
    zmodemStatus = "正在请求远端接收";
    try { await (transport === "tcp" ? TCPWrite(sessionId, "rz\r") : SSHWrite(sessionId, "rz\r")); }
    catch (e: any) { zmodemStatus = `无法启动远端 rz: ${e?.message || String(e)}`; }
  }

  async function beginZmodemDownload() {
    showTransferMenu = false;
    const remotePath = window.prompt("远端文件路径");
    if (!remotePath?.trim()) return;
    zmodemProgress = 0;
    zmodemStatus = "正在请求远端发送";
    const escaped = remotePath.replace(/'/g, "'\\''");
    try { await (transport === "tcp" ? TCPWrite(sessionId, `sz -- '${escaped}'\r`) : SSHWrite(sessionId, `sz -- '${escaped}'\r`)); }
    catch (e: any) { zmodemStatus = `无法启动远端 sz: ${e?.message || String(e)}`; }
  }

  function packetLabel(protocol: PacketProtocol) {
    return protocol === "ymodem" ? "YModem" : "XModem";
  }

  function saveReceivedFile(result?: TransferResult, transferError?: Error) {
    const label = packetTransferLabel || "XModem";
    if (transferError) { packetStatus = `${label} 失败: ${transferError.message}`; packetTransfer = null; return; }
    if (!result) { packetStatus = `${label} 已取消`; packetTransfer = null; return; }
    const blob = new Blob([result.data.buffer as ArrayBuffer]);
    const link = document.createElement("a");
    link.href = URL.createObjectURL(blob); link.download = result.name || `${label.toLowerCase()}-download.bin`; link.click(); URL.revokeObjectURL(link.href);
    packetStatus = `${label} 下载完成`; packetProgress = 100; packetTransfer = null;
  }

  function choosePacketUpload(protocol: PacketProtocol) {
    showTransferMenu = false;
    packetUploadProtocol = protocol;
    if (packetInput) packetInput.value = "";
    packetInput?.click();
  }

  async function beginPacketUpload(event: Event) {
    const file = (event.currentTarget as HTMLInputElement).files?.[0];
    if (!file) return;
    const content = new Uint8Array(await file.arrayBuffer());
    const protocol = packetUploadProtocol;
    const label = packetLabel(protocol);
    packetTransferLabel = label;
    packetProgress = 0; packetStatus = `正在请求远端 ${label} 接收`;
    packetTransfer = protocol === "ymodem"
      ? new YModemSender(writeBinary, content, file.name, (done, total) => { packetProgress = total ? Math.round(done * 100 / total) : 0; }, (_result, transferError) => {
        packetStatus = transferError ? `${label} 上传失败: ${transferError.message}` : `${label} 上传完成`;
        if (!transferError) packetProgress = 100;
        packetTransfer = null;
      }, writeVisibleBytes)
      : new XModemSender(writeBinary, content, file.name, (done, total) => { packetProgress = total ? Math.round(done * 100 / total) : 0; }, (_result, transferError) => {
        packetStatus = transferError ? `${label} 上传失败: ${transferError.message}` : `${label} 上传完成`;
        if (!transferError) packetProgress = 100;
        packetTransfer = null;
      }, writeVisibleBytes);
    const command = protocol === "ymodem" ? "rb\r" : "rx\r";
    try { await (transport === "tcp" ? TCPWrite(sessionId, command) : SSHWrite(sessionId, command)); }
    catch (e: any) { packetTransfer = null; packetStatus = `无法启动远端 ${protocol === "ymodem" ? "rb" : "rx"}: ${e?.message || String(e)}`; }
  }

  async function beginPacketDownload(protocol: PacketProtocol) {
    showTransferMenu = false;
    const remotePath = window.prompt("远端文件路径");
    if (!remotePath?.trim()) return;
    const label = packetLabel(protocol);
    packetTransferLabel = label;
    packetProgress = 0; packetStatus = `正在请求远端 ${label} 发送`;
    packetTransfer = protocol === "ymodem"
      ? new YModemReceiver(writeBinary, (done, total) => { packetProgress = total ? Math.round(done * 100 / total) : 0; }, saveReceivedFile, writeVisibleBytes)
      : new XModemReceiver(writeBinary, remotePath.split("/").pop() || "xmodem-download.bin", (done, total) => { packetProgress = total ? Math.round(done * 100 / total) : 0; }, saveReceivedFile, writeVisibleBytes);
    const escaped = remotePath.replace(/'/g, "'\\''");
    const command = protocol === "ymodem" ? `sb -- '${escaped}'\r` : `sx '${escaped}'\r`;
    try { await (transport === "tcp" ? TCPWrite(sessionId, command) : SSHWrite(sessionId, command)); }
    catch (e: any) { packetTransfer = null; packetStatus = `无法启动远端 ${protocol === "ymodem" ? "sb" : "sx"}: ${e?.message || String(e)}`; }
  }

  function selectSessionTheme(theme: string) {
    const tabId = currentSessionTab?.id;
    if (!tabId) {
      terminalTheme.set(theme);
      showThemePicker = false;
      return;
    }
    tabs.update(items => items.map(tab => tab.id === tabId ? { ...tab, terminalTheme: theme } : tab));
    if (currentSessionTab?.connectionId) {
      void SetConnectionTerminalTheme(currentSessionTab.connectionId, theme).catch((e) => {
        error = `保存终端主题失败: ${e?.message || String(e)}`;
      });
    }
    showThemePicker = false;
  }

  function toggleTransferMenu() {
    showTransferMenu = !showTransferMenu;
    if (showTransferMenu) showThemePicker = false;
  }

  function colorWithAlpha(color: string, alpha: number) {
    const match = color.trim().match(/^#([0-9a-f]{6})$/i);
    if (!match) return color;
    const hex = match[1];
    const channels = [0, 2, 4].map((offset) => Number.parseInt(hex.slice(offset, offset + 2), 16));
    return `rgba(${channels.join(",")},${alpha})`;
  }

  async function selectBackgroundImage(event: Event) {
    const input = event.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    input.value = "";
    if (!file) return;
    backgroundError = "";
    if (!file.type.startsWith("image/")) {
      backgroundError = "请选择图片文件";
      return;
    }
    if (file.size > 12 * 1024 * 1024) {
      backgroundError = "图片不能超过 12 MB";
      return;
    }
    try {
      terminalBackgroundImage.set(await compressTerminalBackgroundImage(file));
    } catch (e: any) {
      backgroundError = `设置背景图失败：${e?.message || String(e)}`;
    }
  }

  function clearBackgroundImage() {
    terminalBackgroundImage.set("");
    backgroundError = "";
  }

  function handleTransferMenuPointerDown(event: PointerEvent) {
    const target = event.target as HTMLElement;
    if (!target?.closest?.(".transfer-menu, .transfer-menu-trigger")) showTransferMenu = false;
    if (!target?.closest?.(".selection-context-menu")) selectionContextMenu = null;
  }

  function handleTransferMenuKeydown(event: KeyboardEvent) {
    if (event.key === "Escape") {
      showTransferMenu = false;
      selectionContextMenu = null;
    }
  }

  async function readPendingOutput() {
    if (!visible || !term || !sessionId || pollInFlight) return;
    pollInFlight = true;
    try {
      let normalOutput = new Uint8Array(0);
      for (let i = 0; i < 32; i++) {
        const encoded = transport === "tcp" ? await TCPRead(sessionId) : await SSHRead(sessionId);
        if (!encoded) break;
        const raw = atob(encoded);
        const bytes = new Uint8Array(raw.length);
        for (let j = 0; j < raw.length; j++) bytes[j] = raw.charCodeAt(j);
        normalOutput = new Uint8Array([...normalOutput, ...bytes]);
      }
      if (normalOutput.length) {
        consumeOutputBytes(normalOutput);
      }
    } catch (e: any) {
      if (!disposed) {
        error = `SSH session error: ${e?.message || String(e)}`;
        void reconnectSession();
      }
    } finally {
      pollInFlight = false;
    }
  }

  $effect(() => {
    if (visible) {
      scheduleVisibleFit();
      startOutputEvents();
      void readPendingOutput();
    } else {
      stopPolling();
      stopOutputEvents?.();
      stopOutputEvents = undefined;
      if (fitTimer) clearTimeout(fitTimer);
      fitTimer = undefined;
    }
  });

  onMount(() => {
    initTerminal();
    startOutputEvents();
    void readPendingOutput();
    window.addEventListener("pointerdown", handleTransferMenuPointerDown);
    window.addEventListener("keydown", handleTransferMenuKeydown);
    const unsubscribeCommand = terminalCommand.subscribe((request) => {
      if (!term || !request) return;
      const command = request.command.trimEnd();
      const targetTabId = request.targetTabId || $activeTabId;
      if (!command || currentSessionTab?.id !== targetTabId) return;
      term.focus();
      if (request.execute) {
        void writeUserInput(`${command}\r`);
      } else {
        term.paste(command);
      }
      terminalCommand.set(null);
    });
    const unsubscribeCapture = terminalCaptureRequest.subscribe((request) => {
      if (!term || !request) return;
      const tabId = currentSessionTab?.id || sessionId;
      const targetTabId = request.targetTabId || $activeTabId;
      if (tabId !== targetTabId) return;
      publishTerminalCapture(request.id);
      terminalCaptureRequest.set(null);
    });
    return () => {
      stopPolling();
      stopOutputEvents?.();
      stopOutputEvents = undefined;
      if (fitTimer) clearTimeout(fitTimer);
      fitTimer = undefined;
      resizeObserver?.disconnect();
      resizeObserver = undefined;
      unsubscribeCommand();
      unsubscribeCapture();
      window.removeEventListener("pointerdown", handleTransferMenuPointerDown);
      window.removeEventListener("keydown", handleTransferMenuKeydown);
    };
  });
  onDestroy(() => {
    disposed = true;
    stopPolling();
    stopOutputEvents?.();
    stopOutputEvents = undefined;
    if (fitTimer) clearTimeout(fitTimer);
    resizeObserver?.disconnect();
    containerEl?.removeEventListener("paste", handlePaste, true);
    containerEl?.removeEventListener("contextmenu", handleTerminalContextMenu);
    webglAddon?.dispose();
    webLinksAddon?.dispose();
    unicodeAddon?.dispose();
    serializeAddon?.dispose();
    searchAddon?.dispose();
    fitAddon?.dispose();
    term?.dispose();
    term = null;
  });
</script>

  <div class="term-root">
  {#if $terminalBackgroundImage}
    <div class="terminal-background" aria-hidden="true" style={`background-image: url("${$terminalBackgroundImage}"); opacity: ${Math.max(0, Math.min(100, $terminalBackgroundOpacity)) / 100}`}></div>
  {/if}
  <div class="toolbar">
    <button class="tool-btn" title={t("search", $language)} onclick={handleSearch}><Search class="w-3.5 h-3.5" /></button>
    <button class="tool-btn" title={t("copy", $language)} onclick={copySelection}><Copy class="w-3.5 h-3.5" /></button>
    <div class="sep"></div>
    <button class="tool-btn" title={t("saveLog", $language)} onclick={saveLog}><Download class="w-3.5 h-3.5" /></button>
    <button class="tool-btn" title="保存 HTML 快照" onclick={saveHtmlSnapshot}><FileCode2 class="w-3.5 h-3.5" /></button>
    <button class:active={aiPanelVisible} class="tool-btn ai-display-btn" title={aiPanelVisible ? "隐藏 AI 助手" : "显示 AI 助手"} aria-pressed={aiPanelVisible} onclick={toggleAIDisplay}><Sparkles class="w-3.5 h-3.5" /></button>
    <button class:active={analysisEnabled} class="tool-btn ai-analysis-btn" title={analysisEnabled ? "关闭 AI 终端分析" : "开启 AI 终端分析（本地脱敏）"} aria-pressed={analysisEnabled} disabled={analyzingTerminal || $aiLoading} onclick={() => { analysisEnabled = !analysisEnabled; if (analysisEnabled) void analyzeTerminalOutput(); }}><ScanSearch class="w-3.5 h-3.5" /></button>
    {#if failedBlock}<button class="tool-btn ai-diagnose-btn" title="AI 分析失败命令" disabled={diagnosing || $aiLoading} onclick={diagnoseFailedCommand}><Sparkles class="w-3.5 h-3.5" /></button>{/if}
    <button class:active={showThemePicker} class="tool-btn" title="终端配色" aria-expanded={showThemePicker} onclick={() => { showThemePicker = !showThemePicker; if (showThemePicker) showTransferMenu = false; }}><Palette class="w-3.5 h-3.5" /></button>
	{#if transport === "ssh"}<button class:active={currentSessionTab?.showSFTP === true} class="tool-btn" title={`${currentSessionTab?.showSFTP ? t("hide", $language) : t("show", $language)} SFTP`} aria-pressed={currentSessionTab?.showSFTP === true} onclick={toggleSFTP}><FolderOpen class="w-3.5 h-3.5" /></button>{/if}
    {#if transport === "ssh"}<button class="tool-btn" title="附着或创建 Tmux 会话" onclick={attachTmux}><PanelsTopLeft class="w-3.5 h-3.5" /></button>{/if}
    <button class:active={showTransferMenu} class="tool-btn transfer-menu-trigger" title="文件传输" aria-haspopup="menu" aria-expanded={showTransferMenu} onclick={toggleTransferMenu}><Upload class="w-3.5 h-3.5" /><ChevronDown class="w-3 h-3" /></button>
    {#if transport === "tcp"}<button class="tool-btn transfer-btn" title="发送 HEX 字节" onclick={sendHexBytes}>HEX</button>{/if}
    <button class="tool-btn" title={t("settings", $language)} onclick={openSettings}><Settings2 class="w-3.5 h-3.5" /></button>
  </div>
  {#if showTransferMenu}
    <div class="transfer-menu" role="menu" aria-label="文件传输">
      <div class="transfer-menu-title">文件传输</div>
      <div class="transfer-menu-group">
        <span class="transfer-menu-protocol">ZModem</span>
        <div class="transfer-menu-actions">
          <button type="button" role="menuitem" onclick={chooseZmodemFiles}><Upload class="w-3 h-3" />上传</button>
          <button type="button" role="menuitem" onclick={beginZmodemDownload}><DownloadIcon class="w-3 h-3" />下载</button>
        </div>
      </div>
      <div class="transfer-menu-group">
        <span class="transfer-menu-protocol">XModem</span>
        <div class="transfer-menu-actions">
          <button type="button" role="menuitem" onclick={() => choosePacketUpload("xmodem")}><Upload class="w-3 h-3" />上传</button>
          <button type="button" role="menuitem" onclick={() => beginPacketDownload("xmodem")}><DownloadIcon class="w-3 h-3" />下载</button>
        </div>
      </div>
      <div class="transfer-menu-group">
        <span class="transfer-menu-protocol">YModem</span>
        <div class="transfer-menu-actions">
          <button type="button" role="menuitem" onclick={() => choosePacketUpload("ymodem")}><Upload class="w-3 h-3" />上传</button>
          <button type="button" role="menuitem" onclick={() => beginPacketDownload("ymodem")}><DownloadIcon class="w-3 h-3" />下载</button>
        </div>
      </div>
    </div>
  {/if}
  {#if selectionContextMenu}
    <div class="selection-context-menu" role="menu" style={`left: ${selectionContextMenu.x}px; top: ${selectionContextMenu.y}px`}>
      <button type="button" role="menuitem" onclick={copyContextSelection}>
        <Copy class="w-3.5 h-3.5" /> 复制
      </button>
      <button type="button" role="menuitem" onclick={pasteFromClipboard}>
        <ClipboardPaste class="w-3.5 h-3.5" /> 粘贴
      </button>
      <div class="selection-context-sep"></div>
      <button type="button" role="menuitem" disabled={analyzingTerminal || $aiLoading} onclick={analyzeSelectedText}>
        <Sparkles class="w-3.5 h-3.5" /> AI 分析
      </button>
    </div>
  {/if}
  {#if showThemePicker}
    <div class="theme-menu">
      <div class="theme-menu-title">{themeLabels[currentTheme] || currentTheme}</div>
      <div class="background-settings">
        <div class="background-settings-head">
          <span>背景图片</span>
          <div class="background-settings-actions">
            <button class="background-icon-btn" type="button" title="选择背景图片" aria-label="选择背景图片" onclick={() => backgroundInput?.click()}><ImageIcon class="w-3.5 h-3.5" /></button>
            {#if $terminalBackgroundImage}<button class="background-icon-btn danger" type="button" title="移除背景图片" aria-label="移除背景图片" onclick={clearBackgroundImage}><Trash2 class="w-3.5 h-3.5" /></button>{/if}
          </div>
        </div>
        <label class="background-opacity" for="terminal-background-opacity">
          <span>图片透明度</span><output>{$terminalBackgroundOpacity}%</output>
        </label>
        <input id="terminal-background-opacity" type="range" min="0" max="100" step="5" bind:value={$terminalBackgroundOpacity} />
        {#if backgroundError}<div class="background-error">{backgroundError}</div>{/if}
      </div>
      {#each themeNames as themeName}
        <button class:chosen={currentTheme === themeName} class="theme-menu-item" onclick={() => selectSessionTheme(themeName)}>
          <span class="theme-swatch" style={`background: ${terminalThemes[themeName]?.background || '#0d1117'}`}></span>
          <span>{themeLabels[themeName] || themeName}</span>
        </button>
      {/each}
    </div>
  {/if}
  {#if showSearch}
    <div class="search-bar">
      <input type="text" placeholder="搜索..." bind:value={searchQuery} oninput={doSearch} onkeydown={handleSearchKeydown} />
      <span class="search-count">{searchResult.count ? `${searchResult.index}/${searchResult.count}` : "0/0"}</span>
      <button class="tool-btn" title="上一个" onclick={findPrevious}><ChevronUp class="w-3 h-3" /></button>
      <button class="tool-btn" title="下一个" onclick={findNext}><ChevronDown class="w-3 h-3" /></button>
      <label class="search-toggle" title="区分大小写"><input type="checkbox" bind:checked={searchCaseSensitive} onchange={doSearch} />Aa</label>
      <label class="search-toggle" title="正则表达式"><input type="checkbox" bind:checked={searchRegex} onchange={doSearch} />.*</label>
      <button class="tool-btn" onclick={() => { showSearch = false; clearSearch(); }}><X class="w-3 h-3" /></button>
    </div>
  {/if}
  <div class="term-area">
    <input bind:this={backgroundInput} class="zmodem-file-input" type="file" accept="image/*" onchange={selectBackgroundImage} />
    <input bind:this={zmodemInput} class="zmodem-file-input" type="file" multiple onchange={beginZmodemUpload} />
    <input bind:this={packetInput} class="zmodem-file-input" type="file" onchange={beginPacketUpload} />
    {#if error}<div class="error-msg">{error}</div>{/if}
    {#if zmodemStatus}<div class="zmodem-status">{zmodemStatus}{zmodemProgress ? ` ${zmodemProgress}%` : ""}</div>{/if}
    {#if packetStatus}<div class="packet-status">{packetStatus}{packetProgress ? ` ${packetProgress}%` : ""}{#if packetTransfer}<button onclick={() => packetTransfer?.cancel()}>取消</button>{/if}</div>{/if}
    <div bind:this={containerEl} class="term-container"></div>
  </div>
  <div class="status-bar">
    <div class="status-left">
      <span class="dot-green"></span><span>{sessionId ? t("connected", $language) : t("local", $language)}</span>
      <span class="status-sep">·</span><span>UTF-8</span>
      {#if term}<span class="status-sep">·</span><span>{term.cols}x{term.rows}</span>{/if}
      <span class="status-sep">·</span><span class:failed={failedBlock}>{shellStatus}</span>
      {#if currentCwd}<span class="cwd" title={currentCwd}>{currentCwd}</span>{/if}
    </div>
    <div class="status-right"><span>{rendererMode}</span><span>延迟 —ms</span></div>
  </div>
</div>

<style>
  .term-root { display: flex; flex-direction: column; height: 100%; position: relative; background: #0d1117; }
  .terminal-background { position: absolute; inset: 0; z-index: 0; background-position: center; background-size: cover; background-repeat: no-repeat; pointer-events: none; transition: opacity 0.15s ease; }
  .toolbar { position: absolute; top: 8px; right: 8px; z-index: 10; display: flex; align-items: center; gap: 4px; max-width: calc(100% - 16px); overflow-x: auto; padding: 6px; border: 1px solid rgba(165,180,252,0.72); border-radius: 9px; background: rgba(8,15,32,0.94); box-shadow: 0 8px 24px rgba(0,0,0,0.42), 0 0 18px rgba(99,102,241,0.16), inset 0 1px 0 rgba(255,255,255,0.12); backdrop-filter: blur(14px); scrollbar-width: none; }
  .toolbar::-webkit-scrollbar { display: none; }
  .tool-btn { width: 30px; height: 30px; flex: 0 0 30px; padding: 6px; border-radius: 6px; border: 1px solid rgba(165,180,252,0.34); background: rgba(99,102,241,0.2); color: #f8fafc; cursor: pointer; display: inline-flex; align-items: center; justify-content: center; box-shadow: inset 0 1px 0 rgba(255,255,255,0.1); }
  .transfer-btn { gap: 2px; font: 10px/1 monospace; min-width: 30px; }
  .transfer-menu-trigger { gap: 1px; }
  .tool-btn:hover { border-color: rgba(224,231,255,0.92); background: rgba(129,140,248,0.48); color: #ffffff; box-shadow: 0 0 12px rgba(99,102,241,0.34), inset 0 1px 0 rgba(255,255,255,0.16); }
  .tool-btn:focus-visible { outline: 2px solid #fbbf24; outline-offset: 2px; }
  .tool-btn.active { border-color: #c4b5fd; background: rgba(129,140,248,0.72); color: #ffffff; box-shadow: 0 0 14px rgba(129,140,248,0.5), inset 0 1px 0 rgba(255,255,255,0.24); }
  .transfer-menu { position: absolute; top: 48px; right: 12px; z-index: 30; width: 218px; padding: 6px; border: 1px solid rgba(255,255,255,0.1); border-radius: 8px; background: rgba(15,23,42,0.96); box-shadow: 0 12px 32px rgba(0,0,0,0.42); backdrop-filter: blur(16px); }
  .transfer-menu-title { padding: 5px 8px 7px; color: #94a3b8; font-size: 10px; border-bottom: 1px solid rgba(255,255,255,0.06); }
  .transfer-menu-group { display: flex; align-items: center; gap: 8px; padding: 6px 2px 2px; }
  .transfer-menu-protocol { width: 54px; color: #e2e8f0; font: 11px/1.2 monospace; }
  .transfer-menu-actions { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 4px; flex: 1; }
  .transfer-menu-actions button { display: inline-flex; align-items: center; justify-content: center; gap: 4px; min-width: 0; padding: 5px 6px; border: 0; border-radius: 4px; background: transparent; color: #cbd5e1; cursor: pointer; font-size: 11px; }
  .transfer-menu-actions button:hover { background: rgba(99,102,241,0.18); color: #e0e7ff; }
  .selection-context-menu { position: fixed; z-index: 10000; min-width: 148px; padding: 5px; border: 1px solid rgba(148,163,184,0.18); border-radius: 8px; background: rgba(15,23,42,0.96); box-shadow: 0 14px 34px rgba(0,0,0,0.38); backdrop-filter: blur(16px); }
  .selection-context-menu button { display: flex; align-items: center; gap: 8px; width: 100%; padding: 7px 9px; border: 0; border-radius: 5px; background: transparent; color: #cbd5e1; cursor: pointer; font-size: 12px; text-align: left; }
  .selection-context-menu button:hover:not(:disabled) { background: rgba(99,102,241,0.18); color: #e0e7ff; }
  .selection-context-menu button:disabled { color: #64748b; cursor: wait; }
  .selection-context-sep { height: 1px; margin: 4px 2px; background: rgba(148,163,184,0.14); }
  .theme-menu { position: absolute; top: 48px; right: 12px; z-index: 30; width: 210px; max-height: min(520px, calc(100% - 64px)); overflow-y: auto; padding: 6px; border: 1px solid rgba(255,255,255,0.1); border-radius: 8px; background: rgba(15,23,42,0.96); box-shadow: 0 12px 32px rgba(0,0,0,0.42); backdrop-filter: blur(16px); }
  .theme-menu-title { padding: 5px 8px 7px; color: #94a3b8; font-size: 10px; border-bottom: 1px solid rgba(255,255,255,0.06); }
  .background-settings { margin: 5px 2px 6px; padding: 7px; border: 1px solid rgba(148,163,184,0.14); border-radius: 5px; background: rgba(15,23,42,0.38); }
  .background-settings-head, .background-opacity { display: flex; align-items: center; justify-content: space-between; gap: 8px; color: #cbd5e1; font-size: 11px; }
  .background-settings-actions { display: flex; align-items: center; gap: 2px; }
  .background-icon-btn { display: inline-flex; align-items: center; justify-content: center; padding: 4px; border: 0; border-radius: 4px; background: transparent; color: #cbd5e1; cursor: pointer; }
  .background-icon-btn:hover { background: rgba(99,102,241,0.18); color: #e0e7ff; }
  .background-icon-btn.danger:hover { background: rgba(239,68,68,0.16); color: #fca5a5; }
  .background-opacity { margin-top: 7px; color: #94a3b8; font-size: 10px; }
  .background-opacity output { color: #c4b5fd; font: 10px/1 monospace; }
  .background-settings input[type="range"] { display: block; width: 100%; height: 12px; margin: 4px 0 0; accent-color: #818cf8; }
  .background-error { margin-top: 5px; color: #fca5a5; font-size: 10px; line-height: 1.35; }
  .theme-menu-item { display: flex; align-items: center; gap: 8px; width: 100%; padding: 6px 8px; border: 0; border-radius: 5px; background: transparent; color: #cbd5e1; cursor: pointer; text-align: left; font-size: 11px; }
  .theme-menu-item:hover, .theme-menu-item.chosen { background: rgba(99,102,241,0.18); color: #e0e7ff; }
  .theme-swatch { width: 18px; height: 12px; border: 1px solid rgba(255,255,255,0.16); border-radius: 3px; flex-shrink: 0; }
  .ai-diagnose-btn { color: #c4b5fd; }
  .ai-diagnose-btn:disabled { opacity: 0.45; cursor: wait; }
  .ai-display-btn { color: #f0abfc; }
  .ai-analysis-btn { color: #67e8f9; }
  .ai-analysis-btn:disabled { opacity: 0.45; cursor: wait; }
  .sep { width: 1px; height: 16px; background: rgba(148,163,184,0.2); margin: 0 2px; }
  .search-bar { position: absolute; top: 48px; right: 12px; z-index: 20; display: flex; align-items: center; gap: 4px; padding: 6px 8px; border-radius: 8px; background: rgba(30,41,59,0.9); backdrop-filter: blur(16px); box-shadow: 0 8px 24px rgba(0,0,0,0.4); }
  .search-bar input { background: transparent; border: 1px solid rgba(148,163,184,0.3); color: #e2e8f0; border-radius: 4px; padding: 4px 8px; font-size: 12px; outline: none; width: 200px; }
  .search-count { min-width: 42px; text-align: center; color: #94a3b8; font: 11px/1 monospace; }
  .search-toggle { display: inline-flex; align-items: center; gap: 3px; padding: 3px 5px; border-radius: 4px; color: #94a3b8; font: 10px/1 monospace; cursor: pointer; user-select: none; }
  .search-toggle:hover { background: rgba(255,255,255,0.08); color: #e2e8f0; }
  .search-toggle input { width: 10px; height: 10px; accent-color: #6366f1; }
  .term-area { position: relative; z-index: 1; flex: 1; overflow: hidden; min-height: 200px; padding: 4px; }
  .term-container { width: 100%; height: 100%; background: transparent; }
  .term-container :global(.xterm), .term-container :global(.xterm-viewport), .term-container :global(.xterm-screen) { background: transparent !important; }
  .zmodem-file-input { display: none; }
  .zmodem-status { position: absolute; right: 12px; bottom: 34px; z-index: 12; max-width: 240px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; padding: 4px 7px; border: 1px solid rgba(34,211,238,.25); border-radius: 4px; background: rgba(8,47,73,.88); color: #a5f3fc; font: 11px/1.3 monospace; }
  .packet-status { position: absolute; right: 12px; bottom: 58px; z-index: 12; max-width: 280px; padding: 4px 7px; border: 1px solid rgba(251,191,36,.25); border-radius: 4px; background: rgba(69,39,4,.9); color: #fde68a; font: 11px/1.3 monospace; }
  .packet-status button { margin-left: 7px; border: 0; border-radius: 3px; background: rgba(255,255,255,.12); color: inherit; cursor: pointer; font-size: 11px; }
  .error-msg { padding: 16px; color: #f87171; font-family: monospace; font-size: 13px; }
  .status-bar { position: relative; z-index: 1; display: flex; align-items: center; justify-content: space-between; padding: 4px 12px; background: rgba(30,41,59,0.85); border-top: 1px solid rgba(148,163,184,0.1); font-size: 11px; font-family: monospace; color: rgba(226,232,240,0.5); }
  .status-left, .status-right { display: flex; align-items: center; gap: 8px; }
  .status-sep { opacity: 0.4; }
  .failed { color: #fca5a5; }
  .cwd { max-width: 260px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: rgba(165,180,252,0.75); }
  .dot-green { width: 6px; height: 6px; border-radius: 50%; background: #4ade80; box-shadow: 0 0 6px rgba(74,222,128,0.6); }
</style>
