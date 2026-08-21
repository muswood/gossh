<!-- owner: muswood | Email: mumu920@outlook.com -->
<script lang="ts">
  import TabBar from "./lib/components/TabBar.svelte";
  import TerminalPanel from "./lib/components/TerminalPanel.svelte";
  import ConnectionTree from "./lib/components/ConnectionTree.svelte";
  import AIAssistant from "./lib/components/AIAssistant.svelte";
  import SFTPPanel from "./lib/components/SFTPPanel.svelte";
  import StatusBar from "./lib/components/StatusBar.svelte";
  import WelcomeScreen from "./lib/components/WelcomeScreen.svelte";
  import SettingsPage from "./lib/components/SettingsPage.svelte";
  import SerialTerminal from "./lib/components/SerialTerminal.svelte";
  import PortForwardPanel from "./lib/components/PortForwardPanel.svelte";
  import CommandSender from "./lib/components/CommandSender.svelte";
  import CreateConnectionModal from "./lib/components/CreateConnectionModal.svelte";
  import AppDialog from "./lib/components/AppDialog.svelte";
  import PasswordPrompt from "./lib/components/PasswordPrompt.svelte";
  import KeyboardInteractivePrompt from "./lib/components/KeyboardInteractivePrompt.svelte";
  import { onMount, onDestroy } from "svelte";
  import { EventsOn } from "../wailsjs/runtime/runtime";
  import { SSHKeyboardInteractiveResponse, TCPConnect } from "../wailsjs/go/main/App";
  import { requestKeyboardInteractive } from "$lib/passwordPrompt";
  import { activeTabId, tabs, showSidebar, connRefreshTrigger, splitPaneIds, splitLayout, recoverableConnectionIds, sftpPanelHeight, aiPanelWidth, uiTheme, activeAIConversationId, aiConversationIdForTab, connectionGroups } from "./lib/stores";
  import { getGroupColor } from "$lib/groupColors";
  import { connectSSHWithHostTrust } from "$lib/sshConnect";
  import { showErrorDialog } from "$lib/dialogs";

  let currentTab = $derived($tabs.find(t => t.id === $activeTabId));
  let currentAITargetTab = $derived(currentTab?.type === "ssh" ? (() => {
    const connection = $connectionGroups.flatMap(group => group.items).find(item => item.id === currentTab.connectionId);
    return connection ? { ...currentTab, host: connection.host, port: connection.port, username: connection.username } : undefined;
  })() : undefined);
  let activeSplitPaneIds = $derived($splitPaneIds.filter(id => $tabs.some(tab => tab.id === id && isTerminalTab(tab))));
  let displayedSessionTabs = $derived((activeSplitPaneIds.includes($activeTabId)
    ? $tabs.filter(tab => activeSplitPaneIds.includes(tab.id))
    : $tabs.filter(tab => tab.id === $activeTabId)
  ).filter(isTerminalTab));
  let mountedSessionTabIds = $state(new Set<string>());
  let renderedSessionTabs = $derived($tabs.filter(tab => isTerminalTab(tab) && (displayedSessionTabs.some(visible => visible.id === tab.id) || mountedSessionTabIds.has(tab.id))));

  let connModalShow = $state(false);
  let editingConnection = $state<any | undefined>(undefined);
  let sftpDragging = $state(false);
  let sftpDragStartY = 0;
  let sftpDragStartHeight = 220;
  let aiDragging = $state(false);
  let aiDragStartX = 0;
  let aiDragStartWidth = 320;

  function isTerminalTab(tab: { type: string }) {
    return tab.type === "ssh" || tab.type === "telnet" || tab.type === "raw";
  }

  function isSessionDisplayed(id: string) {
    return displayedSessionTabs.some(tab => tab.id === id);
  }

  $effect(() => {
    if (currentTab?.type !== "ssh") activeAIConversationId.set(aiConversationIdForTab(currentTab));

    const validSplitPaneIds = $splitPaneIds.filter(id => $tabs.some(tab => tab.id === id && isTerminalTab(tab)));
    if (
      validSplitPaneIds.length !== $splitPaneIds.length ||
      validSplitPaneIds.some((id, index) => id !== $splitPaneIds[index])
    ) {
      splitPaneIds.set(validSplitPaneIds);
    }

    const next = new Set(mountedSessionTabIds);
    let changed = false;
    for (const tab of displayedSessionTabs) {
      if (tab.connected && tab.sessionId && !next.has(tab.id)) {
        next.add(tab.id);
        changed = true;
      }
    }
    for (const id of next) {
      if (!$tabs.some(tab => tab.id === id && isTerminalTab(tab))) {
        next.delete(id);
        changed = true;
      }
    }
    if (changed) mountedSessionTabIds = next;
  });

  onMount(() => {
    const cleanup = EventsOn("ssh:keyboard-interactive", async (data: any) => {
      if (!data?.requestId || !Array.isArray(data.questions) || !Array.isArray(data.echos)) return;
      const answers = await requestKeyboardInteractive({
        requestId: String(data.requestId), user: String(data.user || ""), instruction: String(data.instruction || ""),
        questions: data.questions.map((question: unknown) => String(question)), echos: data.echos.map(Boolean),
      });
      try { await SSHKeyboardInteractiveResponse(String(data.requestId), answers || [], !answers); }
      catch (error) { console.warn("提交 SSH 二次验证失败", error); }
    });
    void restoreSSHConnections();
    return cleanup;
  });

  async function restoreSSHConnections() {
    if (!(window as any).go?.main?.App) return;
    for (const connectionId of $recoverableConnectionIds.slice(0, 8)) {
      if ($tabs.some(tab => tab.connectionId === connectionId && tab.connected)) continue;
      const tabId = `ssh-restore-${connectionId}-${Date.now()}`;
      tabs.update(items => [...items, { id: tabId, type: "ssh", name: "恢复会话", connected: false, sessionId: tabId, connectionId }]);
      try {
        const sessionId = await connectSSHWithHostTrust(connectionId, 80, 24);
        tabs.update(items => items.map(tab => tab.id === tabId ? { ...tab, sessionId, connected: true, name: tab.name === "恢复会话" ? "已恢复会话" : tab.name } : tab));
      } catch {
        tabs.update(items => items.filter(tab => tab.id !== tabId));
      }
    }
  }

  async function handleModalConnect(data: any) {
    if (data.isEdit) {
      connRefreshTrigger.update(v => v + 1);
      return;
    }
    const newTabId = `${data.connType || 'ssh'}-${Date.now()}`;
    tabs.update(t => [...t, {
      id: newTabId,
      type: data.connType === 'serial' ? 'serial' : data.connType === 'telnet' ? 'telnet' : data.connType === 'raw' ? 'raw' : 'ssh',
      name: data.name || data.host,
      connected: false,
      sessionId: data.connType === 'serial' ? undefined : newTabId,
	  connectionId: data.id,
      groupColor: data.connType === 'serial' ? undefined : getGroupColor(data.groupId || "default"),
      terminalTheme: data.terminalTheme || undefined,
      showSFTP: false,
      serialConfig: data.serialConfig,
    }]);
    activeTabId.set(newTabId);
    if (data.connType === 'ssh') {
      try {
        const sessionId = await connectSSHWithHostTrust(data.id, 80, 24);
        tabs.update(t => t.map(x => x.id === newTabId ? { ...x, sessionId, connected: true } : x));
        recoverableConnectionIds.update(ids => ids.includes(data.id) ? ids : [...ids, data.id]);
      } catch (e) {
        tabs.update(t => t.filter(x => x.id !== newTabId));
        activeTabId.set("welcome");
        showErrorDialog("SSH 连接失败", e?.toString?.() || String(e));
        return;
      }
    }
    if (data.connType === 'telnet' || data.connType === 'raw') {
      try {
        const sessionId = await TCPConnect({ id: newTabId, host: data.host, port: Number(data.port), protocol: data.connType });
        tabs.update(t => t.map(x => x.id === newTabId ? { ...x, sessionId, connected: true } : x));
      } catch (e) {
        tabs.update(t => t.filter(x => x.id !== newTabId));
        activeTabId.set("welcome");
        showErrorDialog(`${data.connType === "telnet" ? "Telnet" : "TCP"} 连接失败`, e?.toString?.() || String(e));
        return;
      }
    }
    connRefreshTrigger.update(v => v + 1);
  }

  function beginSftpResize(event: PointerEvent) {
    if (!currentTab?.showSFTP) return;
    sftpDragging = true;
    sftpDragStartY = event.clientY;
    sftpDragStartHeight = $sftpPanelHeight || 220;
    window.addEventListener("pointermove", moveSftpResize);
    window.addEventListener("pointerup", endSftpResize);
    window.addEventListener("pointercancel", endSftpResize);
    (event.currentTarget as HTMLElement).setPointerCapture(event.pointerId);
  }

  function moveSftpResize(event: PointerEvent) {
    if (!sftpDragging) return;
    const delta = sftpDragStartY - event.clientY;
    const next = Math.max(140, Math.min(520, sftpDragStartHeight + delta));
    sftpPanelHeight.set(next);
  }

  function endSftpResize() {
    sftpDragging = false;
    window.removeEventListener("pointermove", moveSftpResize);
    window.removeEventListener("pointerup", endSftpResize);
    window.removeEventListener("pointercancel", endSftpResize);
  }

  function clampAiPanelWidth(value: number) {
    return Math.max(260, Math.min(1200, value));
  }

  function beginAiResize(event: PointerEvent) {
    if (!currentTab?.showAI) return;
    aiDragging = true;
    aiDragStartX = event.clientX;
    aiDragStartWidth = $aiPanelWidth || 320;
    window.addEventListener("pointermove", moveAiResize);
    window.addEventListener("pointerup", endAiResize);
    window.addEventListener("pointercancel", endAiResize);
    (event.currentTarget as HTMLElement).setPointerCapture(event.pointerId);
  }

  function moveAiResize(event: PointerEvent) {
    if (!aiDragging) return;
    const delta = aiDragStartX - event.clientX;
    aiPanelWidth.set(clampAiPanelWidth(aiDragStartWidth + delta));
  }

  function endAiResize() {
    aiDragging = false;
    window.removeEventListener("pointermove", moveAiResize);
    window.removeEventListener("pointerup", endAiResize);
    window.removeEventListener("pointercancel", endAiResize);
  }

  onDestroy(() => {
    endSftpResize();
    endAiResize();
  });
</script>

<main data-theme={$uiTheme} class:ai-resizing={aiDragging} class="app-root">
  <AppDialog />
  <PasswordPrompt />
  <KeyboardInteractivePrompt />
  <CreateConnectionModal
    show={connModalShow}
    connection={editingConnection}
    groups={$connectionGroups}
    onClose={() => { connModalShow = false; editingConnection = undefined; }}
    onConnect={handleModalConnect}
    onSaved={() => connRefreshTrigger.update(v => v + 1)}
  />
  <TabBar />
  <div class="app-body">
    {#if $showSidebar}
      <div class="sidebar">
        <ConnectionTree
          onNewClick={() => { editingConnection = undefined; connModalShow = true; }}
          onEditClick={(connection) => { editingConnection = connection; connModalShow = true; }}
        />
      </div>
    {/if}
    <div class="main-area">
      <div class="center-area">
        <div class="center-top">
          <div class="terminal-wrapper">
            <div class="session-stack" class:horizontal={$splitLayout === "horizontal"} class:stack-hidden={displayedSessionTabs.length === 0}>
              {#if displayedSessionTabs.length > 0}<CommandSender />{/if}
              <div class="pane-grid">
              {#each renderedSessionTabs as tab (tab.id)}
                <div class="session-view session-active" class:session-hidden={!isSessionDisplayed(tab.id)}>
                  {#if tab.connected && tab.sessionId}
                    <TerminalPanel sessionId={tab.sessionId} transport={tab.type === "ssh" ? "ssh" : "tcp"} visible={isSessionDisplayed(tab.id)} />
                  {:else}
                    <div class="connection-loading">正在连接 {tab.type === "telnet" ? "Telnet" : tab.type === "raw" ? "TCP" : "SSH"}...</div>
                  {/if}
                </div>
              {/each}
              </div>
            </div>
            {#if currentTab?.type === "welcome"}
              <WelcomeScreen groups={$connectionGroups} />
            {:else if currentTab?.type === "settings"}
              <SettingsPage />
            {:else if currentTab?.type === "serial"}
              <SerialTerminal config={currentTab?.serialConfig} />
            {:else if currentTab?.type === "portforward"}
              <PortForwardPanel sessionId={currentTab.sessionId || ''} />
            {:else if currentTab?.type === "ai"}
              {#key currentTab.id}<AIAssistant tabId={currentTab.id} terminalTabs={[]} />{/key}
            {/if}
          </div>
          {#if currentTab?.showAI && currentTab.type !== "ai"}
            <button type="button" class:resizing={aiDragging} class="ai-resizer"
                 aria-label="拖动或用方向键调整 AI 助手宽度"
                 title="拖动调整 AI 助手宽度"
                 onpointerdown={(event) => { event.preventDefault(); beginAiResize(event); }}
                 onkeydown={(e) => {
                   if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return;
                   e.preventDefault();
                   aiPanelWidth.update(value => clampAiPanelWidth(value + (e.key === 'ArrowLeft' ? 24 : -24)));
                 }}>
              <span></span>
            </button>
            <div class="ai-panel" style={`--ai-panel-width: ${$aiPanelWidth}px`}>
              {#key currentTab?.id || "no-tab"}
				<AIAssistant tabId={currentTab?.id || ""} targetTab={currentAITargetTab || currentTab} terminalTabs={displayedSessionTabs} />
              {/key}
            </div>
          {/if}
        </div>
		{#if currentTab?.type === "ssh" && currentTab.showSFTP}
          <button type="button" class:resizing={sftpDragging} class="sftp-resizer"
               aria-label="拖动或用方向键调整 SFTP 高度"
               title="拖动调整 SFTP 高度"
               onpointerdown={beginSftpResize}
               onkeydown={(e) => {
                 if (e.key !== 'ArrowUp' && e.key !== 'ArrowDown') return;
                 e.preventDefault();
                 sftpPanelHeight.update(value => Math.max(140, Math.min(520, value + (e.key === 'ArrowUp' ? 24 : -24))));
               }}>
            <span></span>
          </button>
          <div class="sftp-area" style={`--sftp-panel-height: ${$sftpPanelHeight}px`}>
            <SFTPPanel sessionId={currentTab?.sessionId || ''} />
          </div>
        {/if}
      </div>
    </div>
  </div>
  <StatusBar />
</main>

<style>
  .app-root {
    display: flex; flex-direction: column;
    height: 100vh; background: var(--app-bg);
    overflow: hidden; color: var(--app-text);
  }
  .app-body { flex: 1; display: flex; overflow: hidden; min-height: 0; }
  .sidebar { width: 260px; flex-shrink: 0; overflow: hidden; }
  .main-area { flex: 1 1 0; min-width: 0; display: flex; flex-direction: column; overflow: hidden; padding: 8px; gap: 8px; }
  .center-area { flex: 1; min-width: 0; min-height: 0; display: flex; flex-direction: column; overflow: hidden; }
  .center-top { flex: 1; min-width: 0; min-height: 0; display: flex; overflow: hidden; }
  .terminal-wrapper {
    position: relative; flex: 1 1 0; min-width: 0; width: 100%; border-radius: 12px; overflow: hidden;
    border: 1px solid var(--app-border);
    background: var(--app-panel);
    box-shadow: var(--app-shadow);
  }
  .session-stack { width: 100%; min-width: 0; height: 100%; display: flex; flex-direction: column; }
  .stack-hidden { display: none; }
  .pane-grid { flex: 1; min-width: 0; min-height: 0; display: grid; grid-template-columns: repeat(auto-fit, minmax(320px, 1fr)); gap: 4px; }
  .session-stack.horizontal .pane-grid { grid-template-columns: 1fr; grid-template-rows: repeat(auto-fit, minmax(180px, 1fr)); }
  .session-view { min-width: 0; min-height: 0; display: block; }
  .session-hidden { display: none; }
  .connection-loading {
    display: flex; align-items: center; justify-content: center;
    height: 100%; color: var(--app-muted); font-size: 13px;
  }
  .ai-panel { flex: 0 0 var(--ai-panel-width, 320px); width: var(--ai-panel-width, 320px); min-width: 260px; max-width: min(1200px, 66.6667%); border-radius: 12px; overflow: hidden; }
  .ai-resizer {
    position: relative; z-index: 2; width: 14px; flex: 0 0 14px; padding: 0; border: 0;
    display: flex; align-items: center; justify-content: center;
    background: transparent;
    cursor: col-resize; touch-action: none; user-select: none;
  }
  .ai-resizer span {
    width: 3px; height: 72px; border-radius: 999px; background: rgba(148,163,184,0.45);
    transition: background 0.15s, height 0.15s;
  }
  .ai-resizer:hover span, .ai-resizer.resizing span { background: rgba(165,180,252,0.9); height: 104px; }
  .ai-resizing { cursor: col-resize; user-select: none; }
  .sftp-resizer {
    height: 10px; flex-shrink: 0; margin: 0 2px;
    display: flex; align-items: center; justify-content: center;
    width: calc(100% - 4px); padding: 0; border: 0;
    background: transparent;
    cursor: row-resize; touch-action: none; user-select: none;
  }
  .sftp-resizer span {
    width: 72px; height: 3px; border-radius: 999px; background: rgba(148,163,184,0.45);
    transition: background 0.15s, width 0.15s;
  }
  .sftp-resizer:hover span, .sftp-resizer.resizing span { background: rgba(165,180,252,0.9); width: 104px; }
  .sftp-area { height: calc(var(--sftp-panel-height, 220px)); flex-shrink: 0; border-radius: 12px; overflow: hidden; }
</style>
