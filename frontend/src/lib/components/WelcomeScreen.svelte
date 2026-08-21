<!-- owner: muswood | Email: mumu920@outlook.com -->
<script lang="ts">
  import { Terminal, Sparkles, Cpu, ArrowRight } from "lucide-svelte";
  import { tabs, activeTabId, connRefreshTrigger, language } from "$lib/stores";
  import { t } from "$lib/i18n";
  import { getGroupColor } from "$lib/groupColors";
  import CreateConnectionModal from "./CreateConnectionModal.svelte";
  import { connectSSHWithHostTrust } from "$lib/sshConnect";
  import { showErrorDialog } from "$lib/dialogs";
  import { TCPConnect } from "../../../wailsjs/go/main/App";
  import logoUrl from "../../assets/images/logo-universal.png";
  import type { ConnectionGroup } from "$lib/stores";

  let { groups = [] } = $props<{ groups?: ConnectionGroup[] }>();
  let showConnModal = $state(false);

  async function handleModalConnect(data: any) {
    const id = `tab-${Date.now()}`;
    tabs.update(t => [...t, {
      id,
      type: data.connType === 'serial' ? 'serial' : data.connType === 'telnet' ? 'telnet' : data.connType === 'raw' ? 'raw' : 'ssh',
      name: data.name || data.host,
      connected: false,
      sessionId: data.connType === 'serial' ? undefined : id,
	  connectionId: data.id,
      groupColor: data.connType === 'serial' ? undefined : getGroupColor(data.groupId || "default"),
      terminalTheme: data.terminalTheme || undefined,
      serialConfig: data.serialConfig,
    }]);
    activeTabId.set(id);
    if (data.connType === 'ssh') {
      try {
        const sessionId = await connectSSHWithHostTrust(data.id, 80, 24);
        tabs.update(t => t.map(x => x.id === id ? { ...x, sessionId, connected: true } : x));
      } catch (e) {
        tabs.update(t => t.filter(x => x.id !== id));
        activeTabId.set("welcome");
        showErrorDialog("SSH 连接失败", e?.toString?.() || String(e));
        return;
      }
    }
    if (data.connType === 'telnet' || data.connType === 'raw') {
      try {
        const sessionId = await TCPConnect({ id, host: data.host, port: Number(data.port), protocol: data.connType });
        tabs.update(t => t.map(x => x.id === id ? { ...x, sessionId, connected: true } : x));
      } catch (e) {
        tabs.update(t => t.filter(x => x.id !== id));
        activeTabId.set("welcome");
        showErrorDialog(`${data.connType === 'telnet' ? 'Telnet' : 'TCP'} 连接失败`, e?.toString?.() || String(e));
        return;
      }
    }
    showConnModal = false;
    connRefreshTrigger.update(v => v + 1);
  }

  function openSettings() {
    tabs.update(t => t.find(x => x.id === "settings")
      ? (activeTabId.set("settings"), t)
      : [...t, { id: "settings", type: "settings", name: "设置", connected: false }]
    );
    activeTabId.set("settings");
  }
</script>

<CreateConnectionModal
  show={showConnModal}
  groups={groups}
  onClose={() => showConnModal = false}
  onConnect={handleModalConnect}
  onSaved={() => connRefreshTrigger.update(v => v + 1)}
/>

<div class="welcome-root">
  <div class="welcome-inner">
    <div class="welcome-hero">
      <div class="hero-icon">
        <img src={logoUrl} alt="" />
      </div>
      <div>
        <h1>{t("welcomeTitle", $language)}</h1>
        <p>{t("welcomeDesc", $language)}</p>
      </div>
    </div>

    <div class="cards-grid">
      <button class="card" onclick={() => showConnModal = true}>
        <div class="card-icon-wrap"><Terminal class="w-5 h-5 text-primary" /></div>
        <div class="card-body"><div class="card-title">{t("sshConnection", $language)}</div><div class="card-desc">{t("sshDesc", $language)}</div></div>
        <ArrowRight class="card-arrow" />
      </button>
      <button class="card" onclick={() => showConnModal = true}>
        <div class="card-icon-wrap accent"><Cpu class="w-5 h-5 text-accent" /></div>
        <div class="card-body"><div class="card-title">{t("serialConnection", $language)}</div><div class="card-desc">{t("serialDesc", $language)}</div></div>
        <ArrowRight class="card-arrow" />
      </button>
      <button class="card" onclick={() => { const id = `ai-${Date.now()}`; tabs.update(t => [...t, { id, type: "ai", name: "AI 助手", connected: false }]); activeTabId.set(id); }}>
        <div class="card-icon-wrap purple"><Sparkles class="w-5 h-5 text-violet-400" /></div>
        <div class="card-body"><div class="card-title">{t("aiAssistant", $language)}</div><div class="card-desc">{t("aiDesc", $language)}</div></div>
        <ArrowRight class="card-arrow" />
      </button>
      <button class="card" onclick={openSettings}>
        <div class="card-icon-wrap amber">
          <svg class="w-5 h-5 text-amber-400" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" /><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" /></svg>
        </div>
        <div class="card-body"><div class="card-title">{t("appSettings", $language)}</div><div class="card-desc">{t("appSettingsDesc", $language)}</div></div>
        <ArrowRight class="card-arrow" />
      </button>
    </div>

    <div class="welcome-footer">GoSSH v0.1.0 · Wails + Svelte 5 + xterm.js</div>
  </div>
</div>

<style>
  .welcome-root {
    display: flex; align-items: center; justify-content: center;
    height: 100%; padding: 24px;
    background: linear-gradient(180deg, color-mix(in srgb, var(--app-panel) 84%, transparent), transparent),
                var(--app-panel);
  }
  .welcome-inner { max-width: 600px; width: 100%; }
  .welcome-hero { display: flex; align-items: center; gap: 16px; margin-bottom: 32px; }
  .hero-icon {
    width: 56px; height: 56px; border-radius: 16px;
    display: flex; align-items: center; justify-content: center;
    box-shadow: var(--app-shadow); overflow: hidden;
  }
  .hero-icon img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .welcome-hero h1 { font-size: 24px; font-weight: 700; color: var(--app-text); margin: 0; }
  .welcome-hero p { font-size: 13px; color: var(--app-muted); margin: 4px 0 0; }
  .cards-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; }
  .card {
    display: flex; align-items: center; gap: 12px;
    padding: 14px 16px; border-radius: 12px; border: 1px solid var(--app-border);
    background: var(--app-panel-muted); cursor: pointer;
    transition: all 0.15s; text-align: left;
  }
  .card:hover { background: var(--app-hover); transform: scale(1.02); }
  .card-icon-wrap {
    width: 40px; height: 40px; border-radius: 10px;
    display: flex; align-items: center; justify-content: center;
    background: var(--app-accent-soft); flex-shrink: 0;
  }
  .card-icon-wrap.accent { background: color-mix(in srgb, var(--app-accent) 14%, transparent); }
  .card-icon-wrap.purple { background: linear-gradient(135deg, color-mix(in srgb, #8b5cf6 15%, transparent), color-mix(in srgb, #ec4899 12%, transparent)); }
  .card-icon-wrap.amber { background: color-mix(in srgb, #f59e0b 14%, transparent); }
  .card-body { flex: 1; }
  .card-title { font-size: 14px; font-weight: 600; color: var(--app-text); margin-bottom: 2px; }
  .card-desc { font-size: 11px; color: var(--app-muted); }
  :global(.card-arrow) { width: 16px; height: 16px; color: var(--app-subtle); flex-shrink: 0; }
  .card:hover :global(.card-arrow) { color: var(--app-accent); }
  .welcome-footer { text-align: center; margin-top: 24px; font-size: 11px; color: var(--app-subtle); }
</style>
