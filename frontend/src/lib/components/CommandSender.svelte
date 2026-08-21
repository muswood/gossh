<!-- owner: muswood | Email: mumu920@outlook.com -->
<script lang="ts">
  import { Send, SplitSquareHorizontal, Rows3, Columns3 } from "lucide-svelte";
  import { tabs, splitPaneIds, splitLayout, syncInputEnabled } from "$lib/stores";
  import { SSHWrite, TCPWrite } from "../../../wailsjs/go/main/App";

  let command = $state("");
  let sending = $state(false);
  let error = $state("");
  let targetTabs = $derived($tabs.filter(tab => tab.connected && tab.sessionId && (tab.type === "ssh" || tab.type === "telnet" || tab.type === "raw") && ($splitPaneIds.length === 0 || $splitPaneIds.includes(tab.id))));

  async function send() {
    const value = command.trim();
    if (!value || targetTabs.length === 0 || sending) return;
    sending = true; error = "";
    try {
      await Promise.all(targetTabs.map(tab => tab.type === "ssh" ? SSHWrite(tab.sessionId!, value + "\n") : TCPWrite(tab.sessionId!, value + "\n")));
      command = "";
    } catch (e: any) { error = e?.message || String(e); }
    finally { sending = false; }
  }
</script>

<div class="command-sender">
  <button class:active={$syncInputEnabled} class="icon" title="同步输入" aria-pressed={$syncInputEnabled} onclick={() => syncInputEnabled.update(v => !v)}><SplitSquareHorizontal size={15} /></button>
  <button class:active={$splitLayout === "vertical"} class="icon" title="垂直分屏" onclick={() => splitLayout.set("vertical")}><Columns3 size={15} /></button>
  <button class:active={$splitLayout === "horizontal"} class="icon" title="水平分屏" onclick={() => splitLayout.set("horizontal")}><Rows3 size={15} /></button>
  <input aria-label="发送命令" bind:value={command} placeholder={targetTabs.length ? `向 ${targetTabs.length} 个会话发送命令` : "先将已连接会话加入分屏"} onkeydown={(event) => event.key === "Enter" && send()} />
  <button class="send" title="发送命令" disabled={!command.trim() || !targetTabs.length || sending} onclick={send}><Send size={15} /></button>
  {#if error}<span class="error" title={error}>发送失败</span>{/if}
</div>

<style>
  .command-sender { height: 34px; display: flex; align-items: center; gap: 4px; padding: 4px 6px; border-bottom: 1px solid rgba(255,255,255,.06); background: #182235; }
  .command-sender input { flex: 1; min-width: 0; height: 25px; border: 1px solid rgba(255,255,255,.09); border-radius: 4px; background: #0e1626; color: #e2e8f0; padding: 0 8px; font-size: 12px; outline: none; }
  .command-sender input:focus { border-color: #64748b; }
  .icon, .send { display: grid; place-items: center; width: 25px; height: 25px; border: 0; border-radius: 4px; background: transparent; color: #94a3b8; cursor: pointer; }
  .icon:hover, .icon.active { background: rgba(99,102,241,.18); color: #c7d2fe; }
  .send { background: #4f46e5; color: white; } .send:disabled { opacity: .4; cursor: not-allowed; }
  .error { color: #fca5a5; font-size: 11px; white-space: nowrap; }
</style>
