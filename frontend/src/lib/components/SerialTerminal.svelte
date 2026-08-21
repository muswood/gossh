<!-- owner: muswood | Email: mumu920@outlook.com -->
<script lang="ts">
  import { Terminal } from "@xterm/xterm";
  import { FitAddon } from "@xterm/addon-fit";
  import { WebLinksAddon } from "@xterm/addon-web-links";
  import { UnicodeGraphemesAddon } from "@xterm/addon-unicode-graphemes";
  import { onMount, onDestroy } from "svelte";
  import { terminalThemes } from "$lib/themes";
  import { highlightTerminalOutput } from "$lib/terminalPrivacy";
  import {
    terminalTheme, terminalCursorColor, terminalFontFamily, terminalFontSize, terminalFontWeight,
    terminalLineHeight, terminalLetterSpacing, terminalCursorStyle, terminalCursorBlink,
    terminalScrollback, terminalHighlightEnabled, terminalHighlightRules,
  } from "$lib/stores";
  import {
    SerialListPorts, SerialConnect, SerialWrite, SerialWriteBase64,
    SerialRead, SerialDisconnect, SerialIsConnected
  } from "../../../wailsjs/go/main/App";

  let { config = null } = $props<{ config?: {
    portName: string; baudRate: number; dataBits: number; stopBits: number; parity: string;
    autoReconnect?: boolean;
  } | null }>();

  let containerEl: HTMLDivElement;
  let term: Terminal | null = null;
  let fitAddon: FitAddon | null = null;
  let webLinksAddon: WebLinksAddon | null = null;
  let unicodeAddon: UnicodeGraphemesAddon | null = null;
  let connected = $state(false);
  let ports = $state<string[]>([]);
  let selectedPort = $state("COM1");
  let baudRate = $state(115200);
  let dataBits = $state(8);
  let stopBits = $state(1);
  let parity = $state("none");
  let hexMode = $state(false);
  let autoReconnect = $state(true);
  let lineEnding = $state("crlf");
  let status = $state("未连接");
  let readInterval: any = null;
  let baudOptions = [9600, 19200, 38400, 57600, 115200];

  $effect(() => {
    if (config) {
      selectedPort = config.portName || "COM1";
      baudRate = config.baudRate || 115200;
      dataBits = config.dataBits || 8;
      stopBits = config.stopBits || 1;
      parity = config.parity || "none";
      autoReconnect = config.autoReconnect !== false;
    }
  });

  $effect(() => {
    if (term) term.options.scrollback = Math.max(100, Math.min(100000, Math.round(Number($terminalScrollback) || 5000)));
  });

  $effect(() => {
    if (term) {
      const theme = terminalThemes[$terminalTheme] || terminalThemes.deepSpace;
      term.options.theme = { ...theme, cursor: $terminalCursorColor || theme.cursor };
      term.options.cursorBlink = $terminalCursorBlink;
      term.options.cursorStyle = $terminalCursorStyle;
    }
  });

  onMount(async () => {
    initTerminal();
    try {
      ports = await SerialListPorts();
      if (!config?.portName && ports.length > 0) selectedPort = ports[0];
      if (config?.portName || ports.length > 0) await connect(true);
    } catch (e) {
      status = `串口初始化失败: ${e}`;
    }
  });

  function initTerminal() {
    if (!containerEl) return;
    const baseTheme = terminalThemes[$terminalTheme] || terminalThemes.deepSpace;
    const theme = { ...baseTheme, cursor: $terminalCursorColor || baseTheme.cursor };
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
      scrollback: Math.max(100, Math.min(100000, Math.round(Number($terminalScrollback) || 5000))),
    });
    fitAddon = new FitAddon();
    unicodeAddon = new UnicodeGraphemesAddon();
    webLinksAddon = new WebLinksAddon((_event, uri) => openTerminalLink(uri));
    term.loadAddon(fitAddon);
    term.loadAddon(unicodeAddon);
    term.loadAddon(webLinksAddon);
    term.open(containerEl);
    fitAddon.fit();
    term.writeln("\x1b[1;35m===== 串口终端 =====\x1b[0m");
    term.writeln("\x1b[90m选择端口并点击连接\x1b[0m");
    new ResizeObserver(() => fitAddon?.fit()).observe(containerEl);
  }

  function openTerminalLink(uri: string) {
    if (!/^https?:\/\//i.test(uri)) return;
    window.open(uri, "_blank", "noopener,noreferrer");
  }

  async function connect(auto = false) {
    try {
      await SerialConnect({
        portName: selectedPort, baudRate,
        dataBits, stopBits, parity,
        hexMode, autoReconnect, encoding: "utf-8",
      });
      connected = await SerialIsConnected();
      status = connected ? `已连接 ${selectedPort} @ ${baudRate}` : "连接中，等待自动重连";
      term?.writeln(connected
        ? `\x1b[1;32m已连接 ${selectedPort} @ ${baudRate}bps\x1b[0m`
        : `\x1b[1;33m串口暂不可用，${auto ? "正在自动重连..." : "请检查设备后重试"}\x1b[0m`);
      startReadLoop();
    } catch (e) {
      status = `连接失败: ${e}`;
      term?.writeln(`\x1b[1;31m连接失败: ${e}\x1b[0m`);
    }
  }

  async function disconnect() {
    try { await SerialDisconnect(); } catch (e) {}
    connected = false;
    status = "已断开";
    if (readInterval) { clearInterval(readInterval); readInterval = null; }
    term?.writeln(`\x1b[1;33m已断开\x1b[0m`);
  }

  function startReadLoop() {
    if (readInterval) clearInterval(readInterval);
    readInterval = setInterval(async () => {
      try {
        const live = await SerialIsConnected();
        if (live !== connected) {
          connected = live;
          status = live ? `已连接 ${selectedPort} @ ${baudRate}` : "连接中，等待自动重连";
          term?.writeln(live ? "\x1b[1;32m串口已恢复连接\x1b[0m" : "\x1b[1;33m串口连接已断开，正在重连...\x1b[0m");
        }
        if (!live) return;
        const data = await SerialRead(256);
        if (data && term) {
          try {
            const raw = atob(data);
            const bytes = Uint8Array.from(raw, c => c.charCodeAt(0));
            if (hexMode) {
              term.write(Array.from(bytes, b => b.toString(16).padStart(2, "0")).join(" "));
            } else {
              term.write(highlightTerminalOutput(new TextDecoder().decode(bytes), $terminalHighlightRules, $terminalHighlightEnabled));
            }
          } catch { term.write("[数据]"); }
        }
      } catch (e) {}
    }, 150);
  }

  async function sendData(data: string) {
    if (!connected) return;
    const suffix = lineEnding === "cr" ? "\r" : lineEnding === "lf" ? "\n" : lineEnding === "none" ? "" : "\r\n";
    try { await SerialWrite(data + suffix); } catch (e) {}
  }

  function toBase64(bytes: Uint8Array): string {
    let text = "";
    const size = 0x8000;
    for (let offset = 0; offset < bytes.length; offset += size) {
      text += String.fromCharCode(...bytes.subarray(offset, offset + size));
    }
    return btoa(text);
  }

  async function sendHexBytes() {
    if (!connected) return;
    const raw = prompt("输入 HEX 字节，例如: 48 65 6c 6c 6f 0d 0a");
    if (!raw?.trim()) return;
    const parts = raw.trim().split(/[\s,;:-]+/).filter(Boolean);
    if (!parts.length || parts.some((part) => !/^[0-9a-fA-F]{1,2}$/.test(part))) {
      status = "HEX 格式无效";
      return;
    }
    try {
      await SerialWriteBase64(toBase64(new Uint8Array(parts.map((part) => Number.parseInt(part, 16)))));
      status = `已发送 ${parts.length} 字节`;
    } catch (e: any) {
      status = `HEX 发送失败: ${e?.message || String(e)}`;
    }
  }

  onDestroy(() => {
    if (readInterval) clearInterval(readInterval);
    disconnect();
    webLinksAddon?.dispose();
    unicodeAddon?.dispose();
    fitAddon?.dispose();
    term?.dispose();
  });
</script>

<div class="serial-root">
  <div class="serial-toolbar">
    <select class="serial-select" bind:value={selectedPort}>
      {#each ports as p}<option value={p}>{p}</option>{/each}
      {#if ports.length === 0}<option value="COM1">COM1</option>{/if}
    </select>
    <select class="serial-select" bind:value={baudRate}>
      {#each baudOptions as b}
        <option value={b}>{b}</option>
      {/each}
    </select>
    <button type="button" class="hex-toggle" title="十六进制模式" aria-pressed={hexMode}
            onclick={() => hexMode = !hexMode}>
      <span class="mini-switch"></span>
      <span class="hex-label">HEX</span>
    </button>
    <button type="button" class="hex-toggle" title="连接失败后自动重连" aria-pressed={autoReconnect}
            onclick={() => autoReconnect = !autoReconnect}>
      <span class="mini-switch"></span>
      <span class="hex-label">自动重连</span>
    </button>
    <select class="serial-select" bind:value={lineEnding} title="发送行尾">
      <option value="crlf">CRLF</option>
      <option value="cr">CR</option>
      <option value="lf">LF</option>
      <option value="none">None</option>
    </select>
    {#if !connected}
      <button class="serial-btn connect" onclick={() => connect()}>连接</button>
    {:else}
      <button class="serial-btn disconnect" onclick={disconnect}>断开</button>
      <button class="serial-btn" onclick={() => sendData(prompt('输入要发送的数据:') || '')}>发送</button>
      <button class="serial-btn" onclick={sendHexBytes}>HEX</button>
    {/if}
    <span class="serial-status">{status}</span>
  </div>
  <div class="serial-terminal">
    <div bind:this={containerEl} class="serial-xterm"></div>
  </div>
</div>

<style>
  .serial-root { display: flex; flex-direction: column; height: 100%; }
  .serial-toolbar {
    display: flex; align-items: center; gap: 8px;
    padding: 6px 12px; background: rgba(30, 41, 59, 0.6);
    border-bottom: 1px solid rgba(255,255,255,0.05);
    flex-wrap: wrap;
  }
  .serial-select {
    background: #0f172a; border: 1px solid rgba(255,255,255,0.08);
    border-radius: 6px; padding: 5px 8px; font-size: 12px; color: #e2e8f0;
    outline: none;
  }
  .hex-toggle {
    display: flex; align-items: center; gap: 6px; padding: 0; border: 0;
    background: transparent; cursor: pointer; appearance: none;
  }
  .mini-switch {
    position: relative; width: 28px; height: 16px; border-radius: 10px;
    background: #334155; transition: background 0.18s;
  }
  .mini-switch::before {
    content: ""; position: absolute; width: 12px; height: 12px; left: 2px; top: 2px;
    border-radius: 50%; background: #e2e8f0; transition: transform 0.18s;
  }
  .hex-toggle[aria-pressed="true"] .mini-switch { background: #6366f1; }
  .hex-toggle[aria-pressed="true"] .mini-switch::before { transform: translateX(12px); }
  .hex-toggle:focus-visible .mini-switch { outline: 2px solid #a5b4fc; outline-offset: 2px; }
  .hex-label { font-size: 11px; color: #94a3b8; font-family: monospace; }
  .serial-btn {
    padding: 5px 14px; border-radius: 6px; border: none; font-size: 12px;
    cursor: pointer; font-weight: 500; transition: all 0.15s;
    background: rgba(255,255,255,0.06); color: #e2e8f0;
  }
  .serial-btn.connect { background: #6366f1; color: white; }
  .serial-btn.disconnect { background: rgba(239, 68, 68, 0.5); color: #fca5a5; }
  .serial-btn:hover { opacity: 0.85; }
  .serial-status { font-size: 11px; color: #64748b; margin-left: auto; }
  .serial-terminal { flex: 1; overflow: hidden; background: #0d1117; }
  .serial-xterm { width: 100%; height: 100%; padding: 8px; }
</style>
