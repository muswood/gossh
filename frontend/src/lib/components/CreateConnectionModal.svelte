<!-- owner: muswood | Email: mumu920@outlook.com -->
<script lang="ts">
  import { X, Server, Cpu, Eye, EyeOff, Folder, Copy, Check, Save } from "lucide-svelte";
  import { ListGroups, SaveConnection } from "../../../wailsjs/go/main/App";
  import { config } from "../../../wailsjs/go/models";
  import { themeLabels, themeNames } from "$lib/themes";
  import { language, type ConnectionGroup } from "$lib/stores";
  import { t } from "$lib/i18n";

  let { show = false, connection, groups = [], onClose, onConnect, onSaved } = $props<{
    show?: boolean; 
    connection?: any;
    groups?: ConnectionGroup[];
    onClose?: () => void; 
    onConnect?: (data: any) => void | Promise<void>;
    onSaved?: (data: any) => void | Promise<void>;
  }>();

  let name = $state("");
  let host = $state("");
  let port = $state(22);
  let username = $state("root");
  let authMethod = $state("password");
  let password = $state("");
  let privateKey = $state("");
	let privateKeyPath = $state("");
	let certificatePath = $state("");
  let passphrase = $state("");
  let showPassword = $state(false);
  let encoding = $state("utf-8");
  let keepAlive = $state(30);
  let terminalTheme = $state("");
  let connType = $state("ssh");
  let jumpHost = $state("");
  let proxyType = $state("none");
  let proxyHost = $state("");
  let proxyUsername = $state("");
  let proxyPassword = $state("");
  let proxyCommand = $state("");
  let startupCmd = $state("");
  let serialBaudRate = $state(115200);
  let serialDataBits = $state(8);
  let serialStopBits = $state(1);
  let serialParity = $state("none");
  let serialAutoReconnect = $state(true);
  let groupId = $state("ungrouped");
  let errorMsg = $state("");
  let errorCopied = $state(false);
  let hydratedConnectionId = $state<string | null>(null);
  let fetchedGroups = $state<ConnectionGroup[]>([]);
  let availableGroups = $derived(groups.length ? groups : fetchedGroups);

  $effect(() => {
    if (!show || groups.length > 0 || !(window as any).go?.main?.App) return;
    void ListGroups().then((raw) => {
      const parsed = JSON.parse(raw || "[]");
      fetchedGroups = parsed.map((group: any) => ({ id: String(group.id), name: String(group.name), items: [] }));
    }).catch(() => { fetchedGroups = []; });
  });

  $effect(() => {
    if (!show) return;
    const id = connection?.id || null;
    if (id === hydratedConnectionId) return;
    hydratedConnectionId = id;
    if (!connection) {
      resetForm();
      return;
    }
    name = connection.name || "";
    host = connection.host || "";
    port = connection.port || 22;
    username = connection.username || "root";
    authMethod = connection.authMethod || "password";
    // Secrets are intentionally omitted by ListConnections. Empty means keep them.
    password = "";
    privateKey = "";
		privateKeyPath = connection.privateKeyPath || "";
		certificatePath = connection.certificatePath || "";
    passphrase = "";
    encoding = connection.encoding || "utf-8";
    keepAlive = connection.keepAliveSeconds || 30;
    terminalTheme = connection.terminalTheme || "";
    jumpHost = connection.jumpHost || "";
    proxyType = connection.proxyType || "none";
    proxyHost = connection.proxyHost || "";
    proxyUsername = connection.proxyUsername || "";
    proxyPassword = "";
    proxyCommand = connection.proxyCommand || "";
    startupCmd = connection.startupCmd || "";
    connType = connection.protocol || "ssh";
    serialBaudRate = connection.serialBaudRate || 115200;
    serialDataBits = connection.serialDataBits || 8;
    serialStopBits = connection.serialStopBits || 1;
    serialParity = connection.serialParity || "none";
    serialAutoReconnect = connection.serialAutoReconnect !== false;
    groupId = connection.groupId || "ungrouped";
    errorMsg = "";
  });

  async function handleSubmit(connectAfterSave = true) {
    const target = host.trim();
    if (!target) {
      errorMsg = connType === "serial" ? "请输入串口名称" : "请输入主机地址";
      return;
    }
    if (connType === "ssh" && authMethod === "private_key" && !connection && !privateKey.trim() && !privateKeyPath.trim()) {
      errorMsg = "密钥认证需要填写私钥内容或私钥文件路径";
      return;
    }
    if (!(window as any).go?.main?.App) {
      errorMsg = "当前页面仅用于前端预览。请使用桌面版 GoSSH 建立 SSH 连接。";
      return;
    }
    const cfg = {
      id: connection?.id || `conn-${Date.now()}`,
      name: name.trim() || host,
	      protocol: connType,
      host: target,
      port,
      username: username.trim(),
      authMethod,
      password,
      privateKey,
		privateKeyPath,
		certificatePath,
      passphrase,
      jumpHost: jumpHost.trim(),
      proxyType,
      proxyHost: proxyHost.trim(),
      proxyUsername: proxyUsername.trim(),
      proxyPassword,
      proxyCommand: proxyCommand.trim(),
      encoding,
      startupCmd: startupCmd.trim(),
      keepAliveSeconds: keepAlive,
      terminalTheme,
      serialBaudRate,
      serialDataBits,
      serialStopBits,
      serialParity,
      serialAutoReconnect,
      groupId: groupId === "ungrouped" ? "" : groupId,
      starred: Boolean(connection?.starred),
      connType,
      serialConfig: { portName: host.trim(), baudRate: serialBaudRate, dataBits: serialDataBits, stopBits: serialStopBits, parity: serialParity, autoReconnect: serialAutoReconnect },
    };
    errorMsg = "";
    try {
      await SaveConnection(new config.ConnectionRecord(cfg));
      if (connectAfterSave) {
        await onConnect?.({ ...cfg, isEdit: Boolean(connection?.id) });
      } else {
        await onSaved?.({ ...cfg, isEdit: Boolean(connection?.id) });
      }
    } catch (e: any) {
      errorMsg = `操作失败: ${e?.toString?.() || String(e)}`;
      errorCopied = false;
      return;
    }
    resetForm();
    onClose?.();
  }

  function resetForm() {
    name = ""; host = ""; port = 22; username = "root";
    authMethod = "password"; password = ""; privateKey = ""; privateKeyPath = ""; certificatePath = ""; passphrase = ""; encoding = "utf-8";
    jumpHost = ""; proxyType = "none"; proxyHost = ""; proxyUsername = ""; proxyPassword = ""; proxyCommand = ""; startupCmd = ""; terminalTheme = ""; connType = "ssh";
    serialBaudRate = 115200; serialDataBits = 8; serialStopBits = 1; serialParity = "none"; serialAutoReconnect = true; groupId = "ungrouped";
    errorMsg = "";
    errorCopied = false;
    hydratedConnectionId = null;
  }

  async function copyError() {
    if (!errorMsg) return;
    try {
      await navigator.clipboard.writeText(errorMsg);
    } catch {
      const area = document.createElement("textarea");
      area.value = errorMsg;
      area.style.position = "fixed";
      area.style.opacity = "0";
      document.body.appendChild(area);
      area.select();
      document.execCommand("copy");
      area.remove();
    }
    errorCopied = true;
  }

  function handleCancel() {
    resetForm();
    onClose?.();
  }
</script>

{#if show}
  <div class="modal-overlay" role="presentation" onclick={handleCancel}>
    <div class="modal-card" role="presentation" onclick={(e) => e.stopPropagation()}>
      <div class="modal-header">
        <h2 class="modal-title">{connection ? t("editConnection", $language) : t("newConnection", $language)}</h2>
        <button class="modal-close" onclick={handleCancel}>
          <X class="w-4 h-4" />
        </button>
      </div>

      <div class="modal-body">
        <div class="form-tabs">
          <button class="form-tab {connType === 'ssh' ? 'active' : ''}"
                  onclick={() => connType = 'ssh'}>
            <Server class="w-3.5 h-3.5" /> SSH
          </button>
          <button class="form-tab {connType === 'serial' ? 'active' : ''}"
                  onclick={() => connType = 'serial'}>
            <Cpu class="w-3.5 h-3.5" /> {t("serial", $language)}
          </button>
          <button class="form-tab {connType === 'telnet' ? 'active' : ''}" onclick={() => connType = 'telnet'}>Telnet</button>
          <button class="form-tab {connType === 'raw' ? 'active' : ''}" onclick={() => connType = 'raw'}>Raw TCP</button>
        </div>

        <div class="form-grid">
          <div class="form-group">
            <label for="connection-name">{t("connectionName", $language)}</label>
            <input id="connection-name" type="text" placeholder={t("connectionNamePlaceholder", $language)} bind:value={name} />
          </div>

          <div class="form-group">
            <label for="connection-host">{connType === 'serial' ? t("portName", $language) : t("host", $language)}</label>
            <input id="connection-host" type="text" placeholder={connType === 'serial' ? 'COM1' : '192.168.1.1'} bind:value={host} />
          </div>

          {#if connType === 'ssh'}
            <div class="form-group">
              <label for="connection-port">{t("port", $language)}</label>
              <input id="connection-port" type="number" placeholder="22" bind:value={port} />
            </div>

            <div class="form-group">
              <label for="connection-username">{t("username", $language)}</label>
              <input id="connection-username" type="text" placeholder="root" bind:value={username} />
            </div>

            <div class="form-group">
              <label for="connection-auth-method">{t("authentication", $language)}</label>
              <select id="connection-auth-method" bind:value={authMethod}>
                <option value="password">密码</option>
                <option value="private_key">密钥</option>
                <option value="ssh_agent">SSH Agent（FIDO2 / PKCS#11）</option>
              </select>
            </div>

            {#if authMethod === 'password'}
              <div class="form-group">
                <label for="connection-password">{t("password", $language)}</label>
                <div class="input-with-icon">
                  <input id="connection-password" type={showPassword ? 'text' : 'password'} placeholder={connection ? '留空保持原密码' : '输入密码'} bind:value={password} />
                  <button class="icon-btn" onclick={() => showPassword = !showPassword}>
                    {#if showPassword}<EyeOff class="w-3.5 h-3.5" />{:else}<Eye class="w-3.5 h-3.5" />{/if}
                  </button>
                </div>
              </div>
            {/if}

            {#if authMethod === 'private_key'}
              <div class="form-group full-width">
                <label for="connection-private-key">私钥内容</label>
                <textarea id="connection-private-key" placeholder={connection ? '留空保持原私钥' : '粘贴 PEM 格式私钥...'} rows="3" bind:value={privateKey}></textarea>
              </div>
				<div class="form-group full-width">
				  <label for="connection-private-key-path">私钥文件路径</label>
				  <input id="connection-private-key-path" type="text" placeholder={connection ? '留空保持原私钥文件' : '~/.ssh/id_ed25519'} bind:value={privateKeyPath} />
				</div>
				<div class="form-group full-width">
				  <label for="connection-certificate-path">OpenSSH 用户证书路径</label>
				  <input id="connection-certificate-path" type="text" placeholder={connection ? '留空保持原证书' : '~/.ssh/id_ed25519-cert.pub'} bind:value={certificatePath} />
				</div>
              <div class="form-group full-width">
                <label for="private-key-passphrase">私钥口令</label>
                <input id="private-key-passphrase" type="password" placeholder="可选" bind:value={passphrase} />
              </div>
            {/if}
          {/if}

          <div class="form-group">
            <label for="connection-group">{t("group", $language)}</label>
            <select id="connection-group" bind:value={groupId}>
              <option value="ungrouped">{t("ungrouped", $language)}</option>
              {#each availableGroups.filter(group => group.id !== 'ungrouped') as group}
                <option value={group.id}>{group.name}</option>
              {/each}
            </select>
          </div>

          {#if connType === 'telnet' || connType === 'raw'}
            <div class="form-group">
              <label for="connection-tcp-port">{t("port", $language)}</label>
              <input id="connection-tcp-port" type="number" placeholder={connType === 'telnet' ? '23' : '端口'} bind:value={port} />
            </div>
          {/if}

          {#if connType === 'serial'}
            <div class="form-group">
              <label for="serial-baud-rate">波特率</label>
              <select id="serial-baud-rate" bind:value={serialBaudRate}>
                <option value="9600">9600</option>
                <option value="19200">19200</option>
                <option value="38400">38400</option>
                <option value="57600">57600</option>
                <option value="115200">115200</option>
              </select>
            </div>
            <div class="form-group">
              <label for="serial-data-bits">数据位</label>
              <select id="serial-data-bits" bind:value={serialDataBits}>
                <option value="7">7</option>
                <option value="8">8</option>
              </select>
            </div>
            <div class="form-group">
              <label for="serial-stop-bits">停止位</label>
              <select id="serial-stop-bits" bind:value={serialStopBits}>
                <option value="1">1</option>
                <option value="2">2</option>
              </select>
            </div>
            <div class="form-group">
              <label for="serial-parity">校验位</label>
              <select id="serial-parity" bind:value={serialParity}>
                <option value="none">无</option>
                <option value="even">偶校验</option>
                <option value="odd">奇校验</option>
              </select>
            </div>
            <label class="serial-reconnect-option"><input type="checkbox" bind:checked={serialAutoReconnect} />断线自动重连</label>
          {/if}

          {#if connType === 'ssh'}
            <div class="form-group">
              <label for="connection-encoding">编码</label>
              <select id="connection-encoding" bind:value={encoding}>
                <option value="utf-8">UTF-8</option>
                <option value="gbk">GBK</option>
                <option value="gb2312">GB2312</option>
              </select>
            </div>

            <div class="form-group">
              <label for="connection-keep-alive">心跳间隔 (秒)</label>
              <input id="connection-keep-alive" type="number" placeholder="30" bind:value={keepAlive} />
            </div>

            <div class="form-group">
              <label for="connection-terminal-theme">终端配色</label>
              <select id="connection-terminal-theme" bind:value={terminalTheme}>
                <option value="">使用默认主题</option>
                {#each themeNames as themeName}
                  <option value={themeName}>{themeLabels[themeName] || themeName}</option>
                {/each}
              </select>
            </div>

            <div class="form-group full-width">
              <label for="connection-jump-host">跳板机（可多跳，逗号分隔）</label>
              <input id="connection-jump-host" type="text" placeholder="root@jump-a:22,jump-b:22" bind:value={jumpHost} />
            </div>

            <div class="form-group">
              <label for="connection-proxy-type">网络代理</label>
              <select id="connection-proxy-type" bind:value={proxyType}>
                <option value="none">不使用</option>
                <option value="http">HTTP CONNECT</option>
                <option value="socks5">SOCKS5</option>
                <option value="command">ProxyCommand</option>
              </select>
            </div>

            {#if proxyType === 'http' || proxyType === 'socks5'}
              <div class="form-group">
                <label for="connection-proxy-host">代理地址</label>
                <input id="connection-proxy-host" type="text" placeholder="proxy.example.com:1080" bind:value={proxyHost} />
              </div>
              <div class="form-group">
                <label for="connection-proxy-user">代理用户名</label>
                <input id="connection-proxy-user" type="text" autocomplete="off" bind:value={proxyUsername} />
              </div>
              <div class="form-group">
                <label for="connection-proxy-password">代理口令</label>
                <input id="connection-proxy-password" type="password" placeholder={connection ? '留空保持原口令' : '可选'} bind:value={proxyPassword} />
              </div>
            {:else if proxyType === 'command'}
              <div class="form-group full-width">
                <label for="connection-proxy-command">ProxyCommand</label>
                <input id="connection-proxy-command" type="text" placeholder="nc -x proxy.example.com:1080 %h %p" bind:value={proxyCommand} />
              </div>
            {/if}

            <div class="form-group full-width">
              <label for="connection-startup-command">登录后执行命令</label>
              <input id="connection-startup-command" type="text" placeholder="sudo -i" bind:value={startupCmd} />
            </div>
          {/if}
        </div>
      </div>

      <div class="modal-footer">
        {#if errorMsg}
          <div class="form-error" title={errorMsg}>
            <span>{errorMsg}</span>
            <button class="error-copy" title="复制错误信息" aria-label="复制错误信息" onclick={copyError}>
              {#if errorCopied}<Check class="w-3.5 h-3.5" />{:else}<Copy class="w-3.5 h-3.5" />{/if}
            </button>
          </div>
        {/if}
        <button class="btn-cancel" onclick={handleCancel}>{t("cancel", $language)}</button>
        {#if !connection}
          <button class="btn-save" onclick={() => handleSubmit(false)}><Save class="w-3.5 h-3.5" />{t("save", $language)}</button>
        {/if}
        <button class="btn-submit" onclick={() => handleSubmit(true)}>{connection ? t("save", $language) : t("connect", $language)}</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .modal-overlay {
    position: fixed; inset: 0; z-index: 100;
    background: rgba(0,0,0,0.6);
    backdrop-filter: blur(4px);
    display: flex; align-items: center; justify-content: center;
  }
  .modal-card {
    width: 520px; max-width: 90vw; max-height: 85vh;
    background: #1e293b; border: 1px solid rgba(255,255,255,0.08);
    border-radius: 16px; box-shadow: 0 24px 64px rgba(0,0,0,0.5);
    overflow: hidden; display: flex; flex-direction: column;
  }
  .modal-header {
    display: flex; align-items: center; justify-content: space-between;
    padding: 16px 20px; border-bottom: 1px solid rgba(255,255,255,0.06);
  }
  .modal-title { font-size: 15px; font-weight: 600; color: #e2e8f0; }
  .modal-close {
    background: none; border: none; color: #64748b; cursor: pointer;
    padding: 4px; border-radius: 4px; display: flex;
  }
  .modal-close:hover { background: rgba(255,255,255,0.08); color: #e2e8f0; }
  .modal-body { padding: 20px; overflow-y: auto; flex: 1; }
  .form-tabs { display: flex; gap: 1px; background: #0f172a; border-radius: 8px; padding: 3px; margin-bottom: 20px; }
  .form-tab {
    flex: 1; display: flex; align-items: center; justify-content: center; gap: 6px;
    padding: 8px; border: none; border-radius: 6px; cursor: pointer;
    font-size: 12px; font-weight: 500; color: #64748b; background: transparent;
    transition: all 0.15s;
  }
  .form-tab.active { background: #6366f1; color: white; }
  .form-tab:hover:not(.active) { color: #94a3b8; background: rgba(255,255,255,0.03); }
  .form-grid {
    display: grid; grid-template-columns: 1fr 1fr; gap: 14px;
  }
  .form-group.full-width { grid-column: 1 / -1; }
  .form-group { display: flex; flex-direction: column; gap: 5px; }
  .serial-reconnect-option { grid-column: 1 / -1; display: inline-flex; align-items: center; gap: 6px; color: #94a3b8; font-size: 11px; }
  .serial-reconnect-option input { accent-color: #6366f1; }
  .form-group label {
    font-size: 11px; font-weight: 500; color: #94a3b8; text-transform: uppercase;
    letter-spacing: 0.5px;
  }
  .form-group input, .form-group select, .form-group textarea {
    background: #0f172a; border: 1px solid rgba(255,255,255,0.08);
    border-radius: 8px; padding: 8px 12px; font-size: 13px; color: #e2e8f0;
    outline: none; transition: border-color 0.15s; font-family: inherit;
  }
  .form-group input:focus, .form-group select:focus, .form-group textarea:focus {
    border-color: rgba(99, 102, 241, 0.4);
  }
  .input-with-icon { position: relative; display: flex; }
  .input-with-icon input { flex: 1; padding-right: 36px; }
  .icon-btn {
    position: absolute; right: 4px; top: 50%; transform: translateY(-50%);
    background: none; border: none; color: #64748b; cursor: pointer;
    padding: 4px; display: flex; border-radius: 4px;
  }
  .icon-btn:hover { color: #e2e8f0; background: rgba(255,255,255,0.06); }
  .modal-footer {
    display: flex; justify-content: flex-end; gap: 10px;
    padding: 14px 20px; border-top: 1px solid rgba(255,255,255,0.06);
  }
  .btn-cancel {
    padding: 8px 18px; border-radius: 8px; border: 1px solid rgba(255,255,255,0.08);
    background: transparent; color: #94a3b8; font-size: 13px; cursor: pointer;
    transition: all 0.15s;
  }
  .btn-cancel:hover { background: rgba(255,255,255,0.04); color: #e2e8f0; }
  .btn-submit {
    padding: 8px 24px; border-radius: 8px; border: none;
    background: #6366f1; color: white; font-size: 13px; font-weight: 500;
    cursor: pointer; transition: all 0.15s;
  }
  .btn-submit:hover { background: #4f46e5; }
  .btn-submit:disabled { opacity: 0.4; cursor: not-allowed; }
  .btn-save {
    display: inline-flex; align-items: center; gap: 6px;
    padding: 8px 18px; border-radius: 8px; border: 1px solid rgba(99,102,241,0.45);
    background: rgba(99,102,241,0.12); color: #c7d2fe; font-size: 13px; cursor: pointer;
    transition: all 0.15s;
  }
  .btn-save:hover { background: rgba(99,102,241,0.22); color: white; }
  .form-error { flex: 1; min-width: 0; display: flex; align-items: center; gap: 5px; align-self: center; color: #fca5a5; font-size: 11px; }
  .form-error span { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; user-select: text; }
  .error-copy { display: inline-flex; flex: 0 0 auto; padding: 4px; border: 0; border-radius: 4px; background: transparent; color: #fca5a5; cursor: pointer; }
  .error-copy:hover { background: rgba(248,113,113,.14); color: #fecaca; }
</style>
