<!-- owner: muswood | Email: mumu920@outlook.com -->
<script lang="ts">
  import {
    PortForwardStart, PortForwardStop, PortForwardList
  } from "../../../wailsjs/go/main/App";
  import { Globe, Link, Delete, Shield, ArrowRight } from "lucide-svelte";
  import { showErrorDialog } from "$lib/dialogs";

  let { sessionId = "" } = $props<{ sessionId?: string }>();

  let showForm = $state(false);
  let fwType = $state("local");
  let localPort = $state(8080);
  let remoteHost = $state("localhost");
  let remotePort = $state(80);
  let rules = $state<any[]>([]);

  async function loadRules() {
    try {
      const json = await PortForwardList(sessionId);
      rules = JSON.parse(json || "[]");
    } catch (e) {}
  }

  async function startForward() {
    try {
      await PortForwardStart({
        sessionId,
        id: `pf-${Date.now()}`,
        type: fwType,
        localHost: "127.0.0.1",
        localPort,
        remoteHost,
        remotePort,
      });
      showForm = false;
      await loadRules();
    } catch (e) { showErrorDialog("启动端口转发失败", String(e)); }
  }

  async function stopForward(id: string) {
    try { await PortForwardStop(id); await loadRules(); } catch (e) {}
  }

  loadRules();
</script>

<div class="pf-root">
  <div class="pf-header">
    <Globe class="w-4 h-4 text-primary" />
    <span class="pf-title">端口转发</span>
    <button class="pf-add" onclick={() => showForm = !showForm}>
      {showForm ? '取消' : '+ 新建'}
    </button>
  </div>

  {#if showForm}
    <div class="pf-form">
      <div class="form-tabs">
        <button class="ftab {fwType === 'local' ? 'active' : ''}" onclick={() => fwType = 'local'}>
          本地 (-L)
        </button>
        <button class="ftab {fwType === 'remote' ? 'active' : ''}" onclick={() => fwType = 'remote'}>
          远程 (-R)
        </button>
        <button class="ftab {fwType === 'dynamic' ? 'active' : ''}" onclick={() => fwType = 'dynamic'}>
          SOCKS5 (-D)
        </button>
      </div>
      <div class="form-row">
        {#if fwType !== 'dynamic'}
          <div class="form-field">
            <label for="forward-local-port">本地端口</label>
            <input id="forward-local-port" type="number" bind:value={localPort} />
          </div>
          <div class="form-field">
            <label for="forward-remote-host">远程主机</label>
            <input id="forward-remote-host" type="text" bind:value={remoteHost} />
          </div>
          <div class="form-field">
            <label for="forward-remote-port">远程端口</label>
            <input id="forward-remote-port" type="number" bind:value={remotePort} />
          </div>
        {:else}
          <div class="form-field">
            <label for="forward-socks-port">本地 SOCKS5 端口</label>
            <input id="forward-socks-port" type="number" bind:value={localPort} />
          </div>
        {/if}
      </div>
      <button class="pf-start" onclick={startForward}>启动转发</button>
    </div>
  {/if}

  <div class="pf-list">
    {#if rules.length === 0}
      <div class="pf-empty">暂无转发规则</div>
    {:else}
      {#each rules as rule}
        <div class="pf-rule">
          <div class="rule-badge">
            {#if rule.type === 'local'}<ArrowRight class="w-3 h-3" />
            {:else if rule.type === 'remote'}<Shield class="w-3 h-3" />
            {:else}<Globe class="w-3 h-3" />{/if}
            <span>{rule.type}</span>
          </div>
          <div class="rule-info">
            {#if rule.type === 'dynamic'}
              0.0.0.0:{rule.localPort} → SOCKS5 Proxy
            {:else if rule.type === 'local'}
              0.0.0.0:{rule.localPort} → {rule.remoteHost}:{rule.remotePort}
            {:else}
              {rule.remoteHost}:{rule.remotePort} → 127.0.0.1:{rule.localPort}
            {/if}
          </div>
          <span class="rule-status {rule.active ? 'on' : 'off'}">
            {rule.active ? '运行中' : '已停止'}
          </span>
          <button class="rule-stop" onclick={() => stopForward(rule.id)}>
            <Delete class="w-3 h-3" />
          </button>
        </div>
      {/each}
    {/if}
  </div>
</div>

<style>
  .pf-root { display: flex; flex-direction: column; height: 100%; padding: 16px; overflow-y: auto; background: var(--app-panel); color: var(--app-text); }
  .pf-header { display: flex; align-items: center; gap: 8px; margin-bottom: 16px; }
  .pf-title { font-size: 15px; font-weight: 600; color: var(--app-text); }
  .pf-add {
    margin-left: auto; padding: 5px 14px; border-radius: 6px; border: 1px solid rgba(99,102,241,0.2);
    background: var(--app-accent-soft); color: var(--app-accent); font-size: 12px; cursor: pointer;
  }
  .pf-form {
    background: var(--app-panel-muted); border: 1px solid var(--app-border);
    border-radius: 10px; padding: 14px; margin-bottom: 16px;
  }
  .form-tabs { display: flex; gap: 4px; margin-bottom: 12px; }
  .ftab {
    padding: 4px 12px; border-radius: 6px; border: none; font-size: 11px;
    cursor: pointer; background: transparent; color: var(--app-muted);
  }
  .ftab.active { background: var(--app-accent-soft); color: var(--app-accent); }
  .form-row { display: flex; gap: 10px; margin-bottom: 12px; }
  .form-field { display: flex; flex-direction: column; gap: 4px; flex: 1; }
  .form-field label { font-size: 10px; color: var(--app-muted); text-transform: uppercase; }
  .form-field input {
    background: var(--app-panel-strong); border: 1px solid var(--app-border-strong);
    border-radius: 6px; padding: 6px 10px; font-size: 12px; color: var(--app-text); outline: none;
  }
  .pf-start {
    padding: 7px 20px; border-radius: 6px; border: none; background: #6366f1;
    color: white; font-size: 12px; cursor: pointer;
  }
  .pf-list { display: flex; flex-direction: column; gap: 6px; }
  .pf-empty { text-align: center; padding: 24px; font-size: 12px; color: var(--app-subtle); }
  .pf-rule {
    display: flex; align-items: center; gap: 10px;
    padding: 8px 12px; border-radius: 8px;
    background: var(--app-panel-muted); border: 1px solid var(--app-border);
    font-size: 12px;
  }
  .rule-badge {
    display: flex; align-items: center; gap: 4px;
    padding: 2px 8px; border-radius: 4px;
    background: var(--app-accent-soft); color: var(--app-accent); font-size: 10px;
    text-transform: uppercase; font-weight: 600;
  }
  .rule-info { flex: 1; color: var(--app-muted); font-family: monospace; font-size: 11px; }
  .rule-status { font-size: 10px; }
  .rule-status.on { color: #4ade80; }
  .rule-status.off { color: #f87171; }
  .rule-stop {
    background: none; border: none; padding: 4px; border-radius: 4px;
    cursor: pointer; color: var(--app-muted);
  }
  .rule-stop:hover { background: rgba(239,68,68,0.2); color: #fca5a5; }
</style>
