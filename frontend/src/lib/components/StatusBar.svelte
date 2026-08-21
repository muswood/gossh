<!-- owner: muswood | Email: mumu920@outlook.com -->
<script lang="ts">
  import { statusMessage, showSidebar, tabs, activeTabId, language } from "$lib/stores";
  import { t } from "$lib/i18n";
  import { themeLabels, themeNames } from "$lib/themes";
  import { terminalTheme } from "$lib/stores";
  import { SetConnectionTerminalTheme } from "../../../wailsjs/go/main/App";

  let showThemePanel = $state(false);
  let currentTab = $derived($tabs.find(tab => tab.id === $activeTabId));
  let activeTheme = $derived(currentTab?.terminalTheme || $terminalTheme || "deepSpace");
  let canShowAI = $derived(Boolean(currentTab && ["ssh", "telnet", "raw"].includes(currentTab.type)));

  function toggleCurrentSFTP() {
    if (!currentTab?.id || currentTab.type !== "ssh") return;
    tabs.update(items => items.map(tab => tab.id === currentTab?.id
      ? { ...tab, showSFTP: !tab.showSFTP }
      : tab));
  }

  function selectTheme(theme: string) {
    if (currentTab?.type === "ssh" && currentTab.id) {
      tabs.update(items => items.map(tab => tab.id === currentTab?.id ? { ...tab, terminalTheme: theme } : tab));
      if (currentTab.connectionId) void SetConnectionTerminalTheme(currentTab.connectionId, theme).catch(() => {});
    } else {
      terminalTheme.set(theme);
    }
    showThemePanel = false;
  }

  function openSettings() {
    tabs.update(t => t.find(x => x.id === "settings")
      ? (activeTabId.set("settings"), t)
      : [...t, { id: "settings", type: "settings", name: "设置", connected: false }]
    );
    activeTabId.set("settings");
  }

  function toggleCurrentAI() {
    if (!currentTab?.id || !canShowAI) return;
    tabs.update(items => items.map(tab => tab.id === currentTab?.id
      ? { ...tab, showAI: !tab.showAI }
      : tab));
  }
</script>

<footer class="sbar">
  <div class="sbar-left">
    <button class="sbar-btn" onclick={() => showSidebar.update(v => !v)}>
      <span class="dot {$showSidebar ? 'on' : 'off'}"></span>
      <span>{$statusMessage}</span>
    </button>
    <span class="sep">|</span>
    <button class="sbar-btn" disabled={currentTab?.type !== "ssh"} onclick={toggleCurrentSFTP}>
      {currentTab?.showSFTP ? t("hide", $language) : t("show", $language)} SFTP
    </button>
    <button class="sbar-btn" disabled={!canShowAI} onclick={toggleCurrentAI}>
      {currentTab?.showAI ? t("hide", $language) : t("show", $language)} AI
    </button>
  </div>
  <div class="sbar-right">
    <div class="theme-wrap">
      <button class="sbar-btn" onclick={() => showThemePanel = !showThemePanel}>
        <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M7 21a4 4 0 01-4-4V5a2 2 0 012-2h4a2 2 0 012 2v12a4 4 0 01-4 4zm0 0h12a2 2 0 002-2v-4a2 2 0 00-2-2h-2.343M11 7.343l1.657-1.657a2 2 0 012.828 0l2.829 2.829a2 2 0 010 2.828l-8.486 8.485M7 17h.01" /></svg>
        {t("theme", $language)}
      </button>
      {#if showThemePanel}
        <div class="theme-drop">
          {#each themeNames as name}
            <button class="theme-opt {activeTheme === name ? 'sel' : ''}"
                    onclick={() => selectTheme(name)}>
              {themeLabels[name] || name}
            </button>
          {/each}
        </div>
      {/if}
    </div>
    <button class="sbar-btn settings-btn" onclick={openSettings}>
      <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" /><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" /></svg>
      {t("settings", $language)}
    </button>
  </div>
</footer>

<style>
  .sbar {
    display: flex; align-items: center; justify-content: space-between;
    height: 28px; padding: 0 12px;
    background: var(--app-panel-muted);
    border-top: 1px solid var(--app-border);
    font-size: 11px; color: var(--app-muted);
  }
  .sbar-left, .sbar-right { display: flex; align-items: center; gap: 8px; height: 100%; }
  .sbar-btn {
    display: flex; align-items: center; gap: 5px;
    padding: 2px 6px; border-radius: 4px; border: none;
    background: transparent; color: inherit; font-size: inherit;
    cursor: pointer; height: 22px; transition: all 0.1s;
    white-space: nowrap;
  }
  .sbar-btn:hover { background: var(--app-hover); color: var(--app-text); }
  .settings-btn { font-weight: 500; }
  .settings-btn:hover { background: var(--app-accent-soft); color: var(--app-accent); }
  .sep { opacity: 0.2; }
  .dot { width: 6px; height: 6px; border-radius: 50%; }
  .dot.on { background: #4ade80; box-shadow: 0 0 5px rgba(74,222,128,0.5); }
  .dot.off { background: var(--app-muted); }
  .theme-wrap { position: relative; }
  .theme-drop {
    position: absolute; bottom: 32px; right: 0; width: 180px; max-height: 320px; overflow-y: auto; z-index: 100;
    background: var(--app-panel-strong); border: 1px solid var(--app-border-strong);
    border-radius: 10px; padding: 6px; box-shadow: var(--app-shadow);
    display: flex; flex-direction: column; gap: 2px;
  }
  .theme-opt {
    display: block; width: 100%; text-align: left; padding: 6px 10px;
    border-radius: 6px; border: none; background: transparent;
    font-size: 11px; color: var(--app-muted); cursor: pointer; transition: all 0.1s;
  }
  .theme-opt:hover { background: var(--app-hover); color: var(--app-text); }
  .theme-opt.sel { background: var(--app-accent-soft); color: var(--app-accent); }
</style>
