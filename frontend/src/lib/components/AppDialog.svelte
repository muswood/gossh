<!-- owner: muswood | Email: mumu920@outlook.com -->
<script lang="ts">
  import { Check, Copy, X } from "lucide-svelte";
  import { appDialog } from "$lib/dialogs";

  let copied = $state(false);

  function close(confirmed = false) {
    const dialog = $appDialog;
    appDialog.set(null);
    copied = false;
    dialog?.resolve?.(confirmed);
  }

  async function copyMessage() {
    const message = $appDialog?.message || "";
    if (!message) return;
    try {
      await navigator.clipboard.writeText(message);
    } catch {
      const area = document.createElement("textarea");
      area.value = message;
      area.style.position = "fixed";
      area.style.opacity = "0";
      document.body.appendChild(area);
      area.select();
      document.execCommand("copy");
      area.remove();
    }
    copied = true;
  }
</script>

{#if $appDialog}
  <div class="dialog-backdrop">
    <button class="backdrop-close" aria-label="关闭弹窗" onclick={() => close(false)}></button>
    <dialog open class="dialog" aria-labelledby="dialog-title">
      <div class="dialog-header">
        <h2 id="dialog-title">{$appDialog.title}</h2>
        <button class="icon-button" title="关闭" aria-label="关闭" onclick={() => close(false)}><X size={17} /></button>
      </div>
      <div class="message-wrap">
        <pre>{$appDialog.message}</pre>
        <button class="copy-button" title="复制信息" aria-label="复制信息" onclick={copyMessage}>
          {#if copied}<Check size={16} />{:else}<Copy size={16} />{/if}
        </button>
      </div>
      <div class="dialog-actions">
        {#if $appDialog.resolve}
          <button class="secondary-button" onclick={() => close(false)}>取消</button>
          <button class="primary-button" onclick={() => close(true)}>{$appDialog.confirmLabel || "确认"}</button>
        {:else}
          <button class="primary-button" onclick={() => close(true)}>关闭</button>
        {/if}
      </div>
    </dialog>
  </div>
{/if}

<style>
  .dialog-backdrop { position: fixed; inset: 0; z-index: 100; display: grid; place-items: center; padding: 20px; background: rgba(2,6,23,.66); }
  .backdrop-close { position: absolute; inset: 0; border: 0; background: transparent; cursor: default; }
  .dialog { position: relative; z-index: 1; width: min(620px, 100%); max-height: min(540px, calc(100vh - 40px)); display: flex; flex-direction: column; margin: 0; padding: 0; border: 1px solid rgba(255,255,255,.12); border-radius: 8px; background: #121c30; color: #e2e8f0; box-shadow: 0 22px 60px rgba(0,0,0,.45); }
  .dialog-header { display: flex; align-items: center; justify-content: space-between; padding: 16px 16px 12px 20px; border-bottom: 1px solid rgba(255,255,255,.08); }
  h2 { margin: 0; color: #f8fafc; font-size: 15px; font-weight: 600; }
  .icon-button, .copy-button { display: grid; place-items: center; border: 0; border-radius: 5px; background: transparent; color: #94a3b8; cursor: pointer; }
  .icon-button { width: 30px; height: 30px; }
  .icon-button:hover, .copy-button:hover { background: rgba(255,255,255,.09); color: #e2e8f0; }
  .message-wrap { position: relative; min-height: 104px; overflow: auto; padding: 16px 52px 16px 20px; }
  pre { margin: 0; color: #cbd5e1; font: 12px/1.55 var(--font-mono); white-space: pre-wrap; overflow-wrap: anywhere; user-select: text; outline: none; }
  .copy-button { position: absolute; top: 12px; right: 14px; width: 32px; height: 32px; background: rgba(99,102,241,.13); color: #c7d2fe; }
  .dialog-actions { display: flex; justify-content: flex-end; gap: 8px; padding: 12px 16px 16px; border-top: 1px solid rgba(255,255,255,.08); }
  .primary-button, .secondary-button { min-height: 34px; padding: 7px 13px; border-radius: 6px; font-size: 13px; cursor: pointer; }
  .primary-button { border: 1px solid #6366f1; background: #6366f1; color: white; }
  .primary-button:hover { background: #4f46e5; }
  .secondary-button { border: 1px solid rgba(255,255,255,.12); background: transparent; color: #cbd5e1; }
  .secondary-button:hover { background: rgba(255,255,255,.06); }
</style>
