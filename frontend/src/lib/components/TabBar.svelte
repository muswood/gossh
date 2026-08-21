<!-- owner: muswood | Email: mumu920@outlook.com -->
<script lang="ts">
  import { tabs, activeTabId, splitPaneIds, language } from "$lib/stores";
  import { t } from "$lib/i18n";
  import type { Tab } from "$lib/stores";
  import { X, Terminal, Cpu, MessageSquare, HardDrive, Plus, Home, Settings, Globe, PanelsTopLeft, CopyPlus } from "lucide-svelte";
  import { onDestroy, onMount } from "svelte";
  import { SSHDisconnect, TCPConnect, TCPDisconnect, ListConnections } from "../../../wailsjs/go/main/App";
  import { connectSSHWithHostTrust } from "$lib/sshConnect";
  import { showErrorDialog } from "$lib/dialogs";

  let contextMenu = $state<{ tab: Tab; x: number; y: number } | null>(null);

  function selectTab(id: string) {
    activeTabId.set(id);
    contextMenu = null;
  }

  function closeTab(id: string) {
    const tab = $tabs.find(tab => tab.id === id);
    splitPaneIds.update(ids => ids.filter(splitId => splitId !== id));
    if (tab?.type === "ssh" && tab.sessionId) {
      void SSHDisconnect(tab.sessionId).catch(() => {});
    } else if ((tab?.type === "telnet" || tab?.type === "raw") && tab.sessionId) {
      void TCPDisconnect(tab.sessionId).catch(() => {});
    }
    tabs.update(currentTabs => {
      const remaining = currentTabs.filter(tab => tab.id !== id);
      activeTabId.update(current => current === id ? (remaining[0]?.id || "") : current);
      return remaining;
    });
  }

  function newTab() {
    if ($tabs.length >= 20) return;
    const id = `ssh-${Date.now()}`;
    tabs.update(tabs => [...tabs, { id, type: "ssh", name: t("newSession", $language), connected: false }]);
    activeTabId.set(id);
  }

  function openContextMenu(event: MouseEvent, tab: Tab) {
    event.preventDefault();
    event.stopPropagation();
    contextMenu = {
      tab,
      x: Math.min(event.clientX, Math.max(window.innerWidth - 190, 8)),
      y: Math.min(event.clientY, Math.max(window.innerHeight - 150, 8)),
    };
    activeTabId.set(tab.id);
  }

  function canClone(tab: Tab) {
    return tab.connected && Boolean(tab.connectionId) && (tab.type === "ssh" || tab.type === "telnet" || tab.type === "raw");
  }

  async function cloneSession(tab: Tab) {
    contextMenu = null;
    if (!canClone(tab) || !tab.connectionId) return;
    if ($tabs.length >= 20) {
      showErrorDialog("克隆会话失败", "标签页数量已达到上限。");
      return;
    }

    const cloneId = `${tab.type}-clone-${tab.connectionId}-${Date.now()}`;
    tabs.update(items => [...items, {
      id: cloneId,
      type: tab.type,
      name: tab.name,
      connected: false,
      sessionId: cloneId,
      connectionId: tab.connectionId,
      groupColor: tab.groupColor,
      terminalTheme: tab.terminalTheme,
      showSFTP: tab.showSFTP,
    }]);
    activeTabId.set(cloneId);

    try {
      const sessionId = tab.type === "ssh"
        ? await connectSSHWithHostTrust(tab.connectionId, 80, 24)
        : await cloneTCPConnection(tab, cloneId);
      tabs.update(items => items.map(item => item.id === cloneId ? { ...item, sessionId, connected: true } : item));
    } catch (error: any) {
      tabs.update(items => items.filter(item => item.id !== cloneId));
      activeTabId.set(tab.id);
      showErrorDialog("克隆会话失败", error?.toString?.() || String(error));
    }
  }

  async function cloneTCPConnection(tab: Tab, cloneId: string) {
    const raw = await ListConnections();
    const connections = JSON.parse(raw || "[]");
    const conn = connections.find((item: any) => item.id === tab.connectionId);
    if (!conn) throw new Error("保存的连接记录不存在，无法克隆该会话。");
    return await TCPConnect({
      id: cloneId,
      host: conn.host,
      port: Number(conn.port || (tab.type === "telnet" ? 23 : 0)),
      protocol: tab.type,
    });
  }

  function toggleSplit(tab: Tab) {
    if (!tab.connected || !tab.sessionId || (tab.type !== "ssh" && tab.type !== "telnet" && tab.type !== "raw")) return;
    contextMenu = null;
    splitPaneIds.update(ids => ids.includes(tab.id) ? ids.filter(id => id !== tab.id) : [...ids, tab.id].slice(-4));
  }

  function getIcon(type: Tab["type"]) {
    switch (type) {
      case "ssh": return Terminal;
      case "telnet": return Globe;
      case "raw": return Globe;
      case "serial": return Cpu;
      case "ai": return MessageSquare;
      case "sftp": return HardDrive;
      case "welcome": return Home;
      case "settings": return Settings;
      case "portforward": return Globe;
    }
  }

  function getTabStatus(tab: Tab): string {
    if (tab.reconnecting) return "reconnecting";
    if (tab.connected) return "connected";
    if (tab.type === "welcome" || tab.type === "settings" || tab.type === "portforward") return "static";
    if (tab.type === "ai") return "ready";
    return "idle";
  }

  function getStatusDotClass(status: string): string {
    switch (status) {
      case "connected": return "dot-green";
      case "reconnecting": return "dot-yellow";
      case "ready": return "dot-purple";
      default: return "dot-none";
    }
  }

  function handleWindowPointerDown(event: MouseEvent) {
    if ((event.target as HTMLElement)?.closest?.(".tab-context-menu")) return;
    contextMenu = null;
  }

  function handleWindowKeydown(event: KeyboardEvent) {
    if (event.key === "Escape") contextMenu = null;
  }

  onMount(() => {
    window.addEventListener("pointerdown", handleWindowPointerDown);
    window.addEventListener("keydown", handleWindowKeydown);
  });

  onDestroy(() => {
    window.removeEventListener("pointerdown", handleWindowPointerDown);
    window.removeEventListener("keydown", handleWindowKeydown);
  });
</script>

<div class="tabbar">
  <div class="tablist" role="tablist">
    {#each $tabs as tab (tab.id)}
      {@const TabIcon = getIcon(tab.type)}
      <div
        role="tab"
        tabindex="0"
        class="tab-item {tab.id === $activeTabId ? 'active' : ''}"
        style={tab.type === "ssh" && tab.groupColor ? `--tab-color: ${tab.groupColor}` : undefined}
        aria-selected={tab.id === $activeTabId}
        onclick={() => selectTab(tab.id)}
        oncontextmenu={(e) => openContextMenu(e, tab)}
        onkeydown={(e) => (e.key === 'Enter' || e.key === ' ') && selectTab(tab.id)}
      >
        <TabIcon class="tab-icon" />
        <span class="tab-label">{tab.name}</span>

        {#if getTabStatus(tab) !== "idle"}
          <span class="tab-dot {getStatusDotClass(getTabStatus(tab))}"></span>
        {/if}

        {#if tab.type !== 'welcome'}
          {#if tab.connected && (tab.type === 'ssh' || tab.type === 'telnet' || tab.type === 'raw')}
            <button class:split-selected={$splitPaneIds.includes(tab.id)} class="tab-close" onclick={(e) => { e.stopPropagation(); toggleSplit(tab); }} title="加入或移出分屏"><PanelsTopLeft class="w-3 h-3" /></button>
          {/if}
          <button
            class="tab-close"
            onclick={(e) => { e.stopPropagation(); closeTab(tab.id); }}
            title="关闭标签"
          >
            <X class="w-3 h-3" />
          </button>
        {/if}
      </div>
    {/each}
  </div>

  <button class="tab-add" onclick={newTab} title="新建标签页 (Ctrl+T)">
    <Plus class="w-4 h-4" />
  </button>

  {#if contextMenu}
    <div
      class="tab-context-menu"
      style={`left: ${contextMenu.x}px; top: ${contextMenu.y}px;`}
      role="menu"
      tabindex="0"
      oncontextmenu={(e) => e.preventDefault()}
    >
      <button role="menuitem" disabled={!canClone(contextMenu.tab)} onclick={() => cloneSession(contextMenu!.tab)}>
        <CopyPlus class="menu-icon" /> 克隆会话
      </button>
      {#if contextMenu.tab.connected && (contextMenu.tab.type === 'ssh' || contextMenu.tab.type === 'telnet' || contextMenu.tab.type === 'raw')}
        <button role="menuitem" onclick={() => toggleSplit(contextMenu!.tab)}>
          <PanelsTopLeft class="menu-icon" /> {$splitPaneIds.includes(contextMenu.tab.id) ? "移出分屏" : "加入分屏"}
        </button>
      {/if}
      {#if contextMenu.tab.type !== 'welcome'}
        <button role="menuitem" onclick={() => { const id = contextMenu!.tab.id; contextMenu = null; closeTab(id); }}>
          <X class="menu-icon" /> 关闭标签
        </button>
      {/if}
    </div>
  {/if}
</div>

<style>
  .tabbar {
    display: flex; align-items: center;
    padding: 4px 8px;
    background: var(--app-panel-muted);
    backdrop-filter: blur(12px);
    border-bottom: 1px solid var(--app-border);
    position: relative;
   height: 38px;
    overflow: visible;
    z-index: 1000;
  }
  .tablist {
    display: flex; align-items: center; gap: 3px;
    overflow-x: auto; overflow-y: hidden;
    flex: 1; min-width: 0; height: 100%;
  }
  .tablist::-webkit-scrollbar { height: 0; }
  .tab-item {
    display: flex; align-items: center; gap: 6px;
    padding: 5px 8px;
    border-radius: 6px;
    font-size: 12px; font-weight: 500;
    cursor: pointer; user-select: none;
    white-space: nowrap; flex-shrink: 0;
    transition: all 0.15s;
    color: var(--app-muted);
    border: 1px solid transparent;
    height: 30px;
  }
  .tab-item:hover {
    color: var(--app-text);
    background: var(--app-hover);
  }
  .tab-item.active {
    color: var(--app-accent);
    background: var(--app-accent-soft);
    border-color: rgba(99, 102, 241, 0.2);
    box-shadow: 0 0 12px rgba(99, 102, 241, 0.08);
  }
  .tab-item[style] .tab-icon, .tab-item[style] .tab-label { color: var(--tab-color); }
  .tab-item[style].active { background: color-mix(in srgb, var(--tab-color) 16%, transparent); border-color: color-mix(in srgb, var(--tab-color) 35%, transparent); }
  .tab-icon { width: 14px; height: 14px; flex-shrink: 0; }
  .tab-label { max-width: 100px; overflow: hidden; text-overflow: ellipsis; }
  .tab-dot {
    width: 5px; height: 5px; border-radius: 50%; flex-shrink: 0;
  }
  .dot-green { background: #4ade80; box-shadow: 0 0 5px rgba(74, 222, 128, 0.6); }
  .dot-yellow { background: #fbbf24; box-shadow: 0 0 5px rgba(251, 191, 36, 0.6); }
  .dot-purple { background: #a78bfa; box-shadow: 0 0 5px rgba(167, 139, 250, 0.4); }
  .dot-none { display: none; }
  .tab-close {
    background: none; border: none;
    padding: 2px; border-radius: 3px;
    cursor: pointer; display: flex;
    color: var(--app-subtle); opacity: 0.7;
    transition: all 0.1s;
  }
  .tab-close:hover {
    background: rgba(239, 68, 68, 0.14);
    color: #fda4af; opacity: 1;
  }
  .tab-close.split-selected { color: var(--app-accent); opacity: 1; background: var(--app-accent-soft); }
  .tab-add {
    display: flex; align-items: center; justify-content: center;
    padding: 4px; margin-left: 4px;
    border-radius: 6px; border: none; cursor: pointer;
    background: transparent; color: var(--app-subtle);
    transition: all 0.15s; flex-shrink: 0;
  }
  .tab-add:hover {
    background: var(--app-hover);
    color: var(--app-accent);
  }
  .tab-context-menu {
    position: fixed;
    z-index: 10000;
    min-width: 170px;
    padding: 5px;
    border: 1px solid var(--app-border-strong);
    border-radius: 8px;
    background: var(--app-panel-strong);
    box-shadow: var(--app-shadow);
    backdrop-filter: blur(16px);
  }
  .tab-context-menu button {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    padding: 7px 9px;
    border: 0;
    border-radius: 5px;
    background: transparent;
    color: var(--app-text);
    cursor: pointer;
    font-size: 12px;
    text-align: left;
  }
  .tab-context-menu button:hover:not(:disabled) {
    background: var(--app-accent-soft);
    color: var(--app-text-strong);
  }
  .tab-context-menu button:disabled {
    color: var(--app-subtle);
    cursor: not-allowed;
  }
  .menu-icon {
    width: 14px;
    height: 14px;
    flex-shrink: 0;
  }
</style>
