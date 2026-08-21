<!-- owner: muswood | Email: mumu920@outlook.com -->
<script lang="ts">
  import { Check, X } from "lucide-svelte";
  import { passwordPrompt } from "$lib/passwordPrompt";

  let password = $state("");
  let passwordInput = $state<HTMLInputElement | null>(null);

  $effect(() => {
    if ($passwordPrompt) passwordInput?.focus();
  });

  function close(value: string | null) {
    const prompt = $passwordPrompt;
    passwordPrompt.set(null);
    prompt?.resolve(value);
    password = "";
  }

  function submit() {
    if (password) close(password);
  }
</script>

{#if $passwordPrompt}
  <div class="password-backdrop">
    <button class="backdrop-close" aria-label="取消输入" onclick={() => close(null)}></button>
    <dialog open class="password-dialog" aria-labelledby="password-title">
      <div class="password-header">
        <h2 id="password-title">{$passwordPrompt.title}</h2>
        <button class="icon-button" title="取消" aria-label="取消" onclick={() => close(null)}><X size={17} /></button>
      </div>
      <div class="password-body">
        <p>{$passwordPrompt.message}</p>
        <input bind:this={passwordInput} type="password" autocomplete="current-password" placeholder="输入 SSH 密码" bind:value={password} onkeydown={(event) => event.key === "Enter" && submit()} />
      </div>
      <div class="password-actions">
        <button class="cancel-button" onclick={() => close(null)}>取消</button>
        <button class="submit-button" disabled={!password} onclick={submit}><Check size={15} />连接</button>
      </div>
    </dialog>
  </div>
{/if}

<style>
  .password-backdrop { position: fixed; inset: 0; z-index: 110; display: grid; place-items: center; padding: 20px; background: rgba(2,6,23,.7); }
  .backdrop-close { position: absolute; inset: 0; border: 0; background: transparent; cursor: default; }
  .password-dialog { position: relative; z-index: 1; width: min(420px, 100%); margin: 0; padding: 0; border: 1px solid rgba(255,255,255,.12); border-radius: 8px; background: #121c30; color: #e2e8f0; box-shadow: 0 22px 60px rgba(0,0,0,.45); }
  .password-header { display: flex; align-items: center; justify-content: space-between; padding: 16px 16px 12px 20px; border-bottom: 1px solid rgba(255,255,255,.08); }
  h2 { margin: 0; font-size: 15px; font-weight: 600; }
  .icon-button { display: grid; place-items: center; width: 30px; height: 30px; border: 0; border-radius: 5px; background: transparent; color: #94a3b8; cursor: pointer; }
  .icon-button:hover { background: rgba(255,255,255,.09); color: #e2e8f0; }
  .password-body { padding: 18px 20px 4px; }
  p { margin: 0 0 12px; color: #94a3b8; font-size: 12px; line-height: 1.5; }
  input { box-sizing: border-box; width: 100%; padding: 10px 11px; border: 1px solid rgba(255,255,255,.12); border-radius: 6px; outline: none; background: #0f172a; color: #e2e8f0; font: 13px var(--font-ui); }
  input:focus { border-color: #818cf8; }
  .password-actions { display: flex; justify-content: flex-end; gap: 8px; padding: 16px 20px; }
  .cancel-button, .submit-button { display: inline-flex; align-items: center; gap: 6px; min-height: 34px; padding: 7px 13px; border-radius: 6px; font-size: 13px; cursor: pointer; }
  .cancel-button { border: 1px solid rgba(255,255,255,.12); background: transparent; color: #cbd5e1; }
  .submit-button { border: 1px solid #6366f1; background: #6366f1; color: white; }
  .submit-button:hover { background: #4f46e5; }
  button:disabled { opacity: .5; cursor: not-allowed; }
</style>
