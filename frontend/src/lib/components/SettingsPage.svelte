<!-- owner: muswood | Email: mumu920@outlook.com -->
<script lang="ts">
  import {
    terminalTheme, terminalCursorColor, terminalFontFamily, terminalFontSize, terminalFontWeight,
    terminalLineHeight, terminalLetterSpacing, terminalCursorStyle, terminalCursorBlink,
    terminalScrollback,
    terminalBackgroundImage, terminalBackgroundOpacity,
    terminalHighlightEnabled, terminalHighlightRules,
    showSidebar, showSFTP, tabs, settingsSection, uiTheme, language,
  } from "$lib/stores";
  import { t } from "$lib/i18n";
  import { terminalThemes, themeLabels, themeNames } from "$lib/themes";
  import { defaultTerminalHighlightRules } from "$lib/terminalPrivacy";
  import { compressTerminalBackgroundImage } from "$lib/terminalBackground";
  import AISettingsPage from "$lib/components/AISettingsPage.svelte";
  import { Monitor, Palette, Info, Check, Activity, ShieldCheck, ShieldAlert, Sparkles, Image as ImageIcon, Trash2 } from "lucide-svelte";
  import { LoadDiagnostics, LoadSecurityConfig, SaveSecurityConfig, ExportConfigToFile, ImportConfigFromFile, ImportOpenSSHConfigFromFile, ImportKnownHostsFromFile } from "../../../wailsjs/go/main/App";
  import { onMount } from "svelte";
  import logoUrl from "../../assets/images/logo-universal.png";

  let toast = $state("");
  let diagnostics = $state<any>(null);
  let settingsContentEl: HTMLDivElement;
  let backgroundInput = $state<HTMLInputElement>();
  let backgroundError = $state("");
  let commandWhitelistEnabled = $state(true);
  let commandBlacklistEnabled = $state(true);
  let mutationsEnabled = $state(true);
  let deletionsEnabled = $state(false);
  let administratorEnabled = $state(false);
  let readOnlyNoApproval = $state(false);
  let commandWhitelistText = $state("");
  let commandBlacklistText = $state("");
  let securityBusy = $state(false);

  type DiagnosticsCounter = { count: number; failures: number; lastMillis: number };

  const fontOptions = [
    { label: "Consolas", value: '"Consolas", monospace' },
    { label: "Cascadia Code", value: '"Cascadia Code", monospace' },
    { label: "JetBrains Mono", value: '"JetBrains Mono", monospace' },
    { label: "Fira Code", value: '"Fira Code", monospace' },
    { label: "Source Code Pro", value: '"Source Code Pro", monospace' },
    { label: "系统等宽字体", value: 'monospace' },
  ];

  function showToast(msg: string) { toast = msg; setTimeout(() => toast = "", 2000); }

  async function selectBackgroundImage(event: Event) {
    const input = event.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    input.value = "";
    if (!file) return;
    backgroundError = "";
    if (!file.type.startsWith("image/")) {
      backgroundError = "请选择图片文件";
      return;
    }
    if (file.size > 12 * 1024 * 1024) {
      backgroundError = "图片不能超过 12 MB";
      return;
    }
    try {
      terminalBackgroundImage.set(await compressTerminalBackgroundImage(file));
    } catch (e: any) {
      backgroundError = `设置背景图失败：${e?.message || String(e)}`;
    }
  }

  function clearBackgroundImage() {
    terminalBackgroundImage.set("");
    backgroundError = "";
  }

  function resetCursorColor() {
    terminalCursorColor.set("");
  }

  function splitHighlightKeywords(value: string) {
    return value.split(/[,\n]/).map(item => item.trim()).filter(Boolean);
  }

  function updateHighlightKeywords(id: string, event: Event) {
    const value = (event.currentTarget as HTMLTextAreaElement).value;
    terminalHighlightRules.update(rules => rules.map(rule => rule.id === id
      ? { ...rule, keywords: splitHighlightKeywords(value) }
      : rule));
  }

  function updateHighlightColor(id: string, event: Event) {
    const value = (event.currentTarget as HTMLInputElement).value;
    terminalHighlightRules.update(rules => rules.map(rule => rule.id === id ? { ...rule, color: value } : rule));
  }

  function resetHighlightRules() {
    terminalHighlightRules.set(defaultTerminalHighlightRules.map(rule => ({ ...rule, keywords: [...rule.keywords] })));
  }

  function splitLines(value: string) {
    return value.split(/\r?\n/).map(item => item.trim()).filter(Boolean);
  }

  async function loadSecurityConfig() {
    try {
      const config = await LoadSecurityConfig() as any;
      commandWhitelistEnabled = config.whitelistEnabled !== false;
      commandBlacklistEnabled = config.blacklistEnabled !== false;
      mutationsEnabled = config.mutationsEnabled !== false;
      deletionsEnabled = config.deletionsEnabled === true;
      administratorEnabled = config.administratorEnabled === true;
      readOnlyNoApproval = config.readOnlyNoApproval === true;
      commandWhitelistText = (config.commandWhitelist || []).join("\n");
      commandBlacklistText = (config.commandBlacklist || []).join("\n");
    } catch (e: any) {
      showToast("加载安全配置失败: " + e);
    }
  }

  async function saveSecurityConfig() {
    securityBusy = true;
    try {
      await SaveSecurityConfig({
        whitelistEnabled: commandWhitelistEnabled,
        blacklistEnabled: commandBlacklistEnabled,
        mutationsEnabled,
        deletionsEnabled,
        administratorEnabled,
        readOnlyNoApproval,
        commandWhitelist: splitLines(commandWhitelistText),
        commandBlacklist: splitLines(commandBlacklistText),
      } as any);
      await loadSecurityConfig();
      showToast("安全配置已保存");
    } catch (e: any) {
      showToast("保存安全配置失败: " + e);
    } finally {
      securityBusy = false;
    }
  }

  function toggleAdministratorMode() {
    if (administratorEnabled) {
      administratorEnabled = false;
      return;
    }
    if (!window.confirm("管理员模式会取消 Agent 的命令安全拦截。每条命令仍需人工批准，删除操作仍需二次确认。是否继续？")) return;
    if (!window.confirm("请再次确认：开启后 Agent 可以提交任意命令和操作供你审批执行。")) return;
    administratorEnabled = true;
  }

  onMount(() => { void loadSecurityConfig(); });

  function selectNav(section: string) {
    settingsSection.set(section);
    requestAnimationFrame(() => settingsContentEl?.scrollTo({ top: 0 }));
  }

  function toggleSFTPForAllTabs() {
    showSFTP.update(value => {
      const next = !value;
      tabs.update(items => items.map(tab => tab.type === "ssh" ? { ...tab, showSFTP: next } : tab));
      return next;
    });
  }

  async function handleExport() {
    try {
      await ExportConfigToFile();
      showToast("配置已导出");
    } catch (e: any) { showToast("导出失败: " + e); }
  }

  async function handleImport() {
    try {
      await ImportConfigFromFile();
      showToast("导入成功");
    } catch (e: any) { showToast("导入失败: " + e); }
  }

  async function importOpenSSH() {
    try { showToast(await ImportOpenSSHConfigFromFile()); }
    catch (e: any) { showToast("OpenSSH 导入失败: " + e); }
  }

  async function importKnownHosts() {
    try { showToast(await ImportKnownHostsFromFile()); }
    catch (e: any) { showToast("known_hosts 导入失败: " + e); }
  }

  async function loadDiagnostics() {
    try {
      diagnostics = JSON.parse(await LoadDiagnostics());
    } catch (e: any) {
      showToast("加载诊断信息失败: " + e);
    }
  }

</script>

<div class="settings-root">
  <div class="settings-nav">
    <button type="button" aria-current={$settingsSection === 'general' ? 'page' : undefined} class="nav-item {$settingsSection === 'general' ? 'active' : ''}" onclick={() => selectNav('general')}>
      <Monitor class="w-4 h-4" /> {t("general", $language)}
    </button>
    <button type="button" aria-current={$settingsSection === 'terminal' ? 'page' : undefined} class="nav-item {$settingsSection === 'terminal' ? 'active' : ''}" onclick={() => selectNav('terminal')}>
      <Palette class="w-4 h-4" /> {t("terminal", $language)}
    </button>
    <button type="button" aria-current={$settingsSection === 'security' ? 'page' : undefined} class="nav-item {$settingsSection === 'security' ? 'active' : ''}" onclick={() => selectNav('security')}>
      <ShieldCheck class="w-4 h-4" /> {t("security", $language)}
    </button>
    <button type="button" aria-current={$settingsSection === 'ai' ? 'page' : undefined} class="nav-item {$settingsSection === 'ai' ? 'active' : ''}" onclick={() => selectNav('ai')}>
      <Sparkles class="w-4 h-4" /> {t("aiConfig", $language)}
    </button>
    <button type="button" aria-current={$settingsSection === 'about' ? 'page' : undefined} class="nav-item {$settingsSection === 'about' ? 'active' : ''}" onclick={() => selectNav('about')}>
      <Info class="w-4 h-4" /> {t("about", $language)}
    </button>
    <button type="button" aria-current={$settingsSection === 'diagnostics' ? 'page' : undefined} class="nav-item {$settingsSection === 'diagnostics' ? 'active' : ''}" onclick={() => { selectNav('diagnostics'); void loadDiagnostics(); }}>
      <Activity class="w-4 h-4" /> {t("diagnostics", $language)}
    </button>
  </div>

  <div bind:this={settingsContentEl} class="settings-content">
    {#if $settingsSection === 'general'}
      <h3 class="section-title">{t("generalSettings", $language)}</h3>
      <div class="setting-row">
        <div class="setting-info">
          <div class="setting-label">{t("interfaceTheme", $language)}</div>
          <div class="setting-desc">{t("themeDesc", $language)}</div>
        </div>
        <div class="ui-theme-switch" role="group" aria-label="界面主题">
          <button type="button" class:active={$uiTheme === "cyber-night"} onclick={() => uiTheme.set("cyber-night")}>{t("dark", $language)}</button>
          <button type="button" class:active={$uiTheme === "aurora-light"} onclick={() => uiTheme.set("aurora-light")}>{t("light", $language)}</button>
        </div>
      </div>
      <div class="setting-row">
        <div class="setting-info">
          <div class="setting-label">{t("language", $language)}</div>
          <div class="setting-desc">{t("languageDesc", $language)}</div>
        </div>
        <select class="terminal-select" aria-label={t("language", $language)} bind:value={$language}>
          <option value="zh-CN">{t("simplifiedChinese", $language)}</option>
          <option value="en-US">{t("english", $language)}</option>
        </select>
      </div>
      <div class="setting-row">
        <div class="setting-info">
          <div class="setting-label">{t("sidebar", $language)}</div>
          <div class="setting-desc">{t("sidebarDesc", $language)}</div>
        </div>
        <button type="button" class="toggle" role="switch" aria-label="显示侧边栏" aria-checked={$showSidebar}
                onclick={() => showSidebar.update(v => !v)}>
          <span class="toggle-slider"></span>
        </button>
      </div>
      <div class="setting-row">
        <div class="setting-info">
          <div class="setting-label">{t("sftpPanel", $language)}</div>
          <div class="setting-desc">{t("sftpPanelDesc", $language)}</div>
        </div>
        <button type="button" class="toggle" role="switch" aria-label="显示 SFTP 面板" aria-checked={$showSFTP}
                onclick={toggleSFTPForAllTabs}>
          <span class="toggle-slider"></span>
        </button>
      </div>

      <h3 class="section-title" style="margin-top: 24px;">数据管理</h3>
      <div class="io-buttons">
        <button class="io-btn export" onclick={handleExport}>导出配置</button>
        <button class="io-btn import" onclick={handleImport}>导入配置</button>
      </div>
      <h3 class="section-title" style="margin-top: 24px;">OpenSSH 集成</h3>
      <div class="io-buttons">
        <button class="io-btn export" onclick={importOpenSSH}>导入 SSH Config</button>
        <button class="io-btn import" onclick={importKnownHosts}>导入 known_hosts</button>
      </div>

    {:else if $settingsSection === 'terminal'}
      <h3 class="section-title">终端配色</h3>
      <div class="theme-grid">
        {#each themeNames as name}
          <button class="theme-card {$terminalTheme === name ? 'selected' : ''}"
                  onclick={() => terminalTheme.set(name)}>
            <div class="theme-preview" style="background: {terminalThemes[name]?.background || '#000'}">
              <span class="theme-dot" style="background: {terminalThemes[name]?.red || '#f00'}"></span>
              <span class="theme-dot" style="background: {terminalThemes[name]?.green || '#0f0'}"></span>
              <span class="theme-dot" style="background: {terminalThemes[name]?.blue || '#00f'}"></span>
              <span class="theme-dot" style="background: {terminalThemes[name]?.yellow || '#ff0'}"></span>
            </div>
            <div class="theme-name">{themeLabels[name] || name}</div>
            {#if $terminalTheme === name}<Check class="w-3 h-3 text-primary" />{/if}
          </button>
        {/each}
      </div>

      <h3 class="section-title terminal-background-title">全局默认背景</h3>
      <div class="terminal-background-settings">
        <div class="background-preview" class:empty={!$terminalBackgroundImage}
             style={$terminalBackgroundImage ? `background-image: url(\"${$terminalBackgroundImage}\")` : ""}>
          {#if !$terminalBackgroundImage}<span>未设置背景图片</span>{/if}
        </div>
        <div class="background-setting-actions">
          <button type="button" class="io-btn export background-action" onclick={() => backgroundInput?.click()}>
            <ImageIcon class="w-3.5 h-3.5" /> 选择背景图片
          </button>
          {#if $terminalBackgroundImage}
            <button type="button" class="io-btn import background-action" onclick={clearBackgroundImage}>
              <Trash2 class="w-3.5 h-3.5" /> 移除
            </button>
          {/if}
          <input bind:this={backgroundInput} class="background-file-input" type="file" accept="image/*" onchange={selectBackgroundImage} />
        </div>
        <div class="form-group background-opacity-group">
          <label for="settings-terminal-background-opacity">图片透明度 <output>{$terminalBackgroundOpacity}%</output></label>
          <input id="settings-terminal-background-opacity" type="range" min="0" max="100" step="5" bind:value={$terminalBackgroundOpacity} />
        </div>
        {#if backgroundError}<div class="background-error">{backgroundError}</div>{/if}
        <div class="setting-desc">该背景将作为所有终端标签页的默认背景，并自动保存到本机。</div>
      </div>

      <h3 class="section-title terminal-typography-title">字体与显示</h3>
      <div class="terminal-settings-grid">
        <div class="form-group">
          <label for="terminal-font-family">字体</label>
          <select id="terminal-font-family" bind:value={$terminalFontFamily} class="terminal-select font-preview">
            {#each fontOptions as option}
              <option value={option.value}>{option.label}</option>
            {/each}
          </select>
        </div>
        <div class="form-group">
          <label for="terminal-font-size">字号 <output>{ $terminalFontSize } px</output></label>
          <input id="terminal-font-size" type="range" min="10" max="24" step="1" bind:value={$terminalFontSize} />
        </div>
        <div class="form-group">
          <label for="terminal-font-weight">字重</label>
          <select id="terminal-font-weight" bind:value={$terminalFontWeight} class="terminal-select">
            <option value={400}>常规</option>
            <option value={500}>中等</option>
            <option value={600}>半粗</option>
          </select>
        </div>
        <div class="form-group">
          <label for="terminal-line-height">行高 <output>{ $terminalLineHeight.toFixed(2) }</output></label>
          <input id="terminal-line-height" type="range" min="1" max="2" step="0.05" bind:value={$terminalLineHeight} />
        </div>
        <div class="form-group">
          <label for="terminal-letter-spacing">字间距 <output>{ $terminalLetterSpacing } px</output></label>
          <input id="terminal-letter-spacing" type="range" min="0" max="2" step="0.5" bind:value={$terminalLetterSpacing} />
        </div>
        <div class="form-group">
          <label for="terminal-cursor-style">光标样式</label>
          <select id="terminal-cursor-style" bind:value={$terminalCursorStyle} class="terminal-select">
            <option value="bar">竖线</option>
            <option value="block">方块</option>
            <option value="underline">下划线</option>
          </select>
        </div>
        <div class="form-group">
          <label for="terminal-cursor-color">光标颜色</label>
          <div class="cursor-color-control">
            <input id="terminal-cursor-color" type="color"
              value={$terminalCursorColor || (terminalThemes[$terminalTheme]?.cursor || "#ffffff")}
              onchange={(event) => terminalCursorColor.set((event.currentTarget as HTMLInputElement).value)} />
            <button type="button" class="io-btn import" onclick={resetCursorColor}>跟随主题</button>
          </div>
          <div class="setting-desc">可自定义输入光标颜色；选择“跟随主题”恢复主题默认颜色。</div>
        </div>
        <div class="form-group">
          <label for="terminal-scrollback">回滚历史 <output>{$terminalScrollback.toLocaleString()} 行</output></label>
          <input id="terminal-scrollback" type="number" min="100" max="100000" step="100" bind:value={$terminalScrollback} />
          <div class="setting-desc">限制每个终端可回看的内存行数；不会把终端内容保存到磁盘。</div>
        </div>
        <div class="setting-row compact-setting">
          <div class="setting-info">
            <div class="setting-label">光标闪烁</div>
            <div class="setting-desc">控制终端输入光标是否闪烁</div>
          </div>
          <button type="button" class="toggle" role="switch" aria-label="光标闪烁" aria-checked={$terminalCursorBlink}
                  onclick={() => terminalCursorBlink.update(v => !v)}>
            <span class="toggle-slider"></span>
          </button>
        </div>
      </div>

      <h3 class="section-title terminal-typography-title">关键词高亮</h3>
      <div class="setting-row highlight-toggle-row">
        <div class="setting-info">
          <div class="setting-label">启用终端关键词高亮</div>
          <div class="setting-desc">实时输出中的状态词、IP 地址和数字会使用不同颜色显示；设置只保存在本机。</div>
        </div>
        <button type="button" class="toggle" role="switch" aria-label="启用终端关键词高亮" aria-checked={$terminalHighlightEnabled}
                onclick={() => terminalHighlightEnabled.update(v => !v)}>
          <span class="toggle-slider"></span>
        </button>
      </div>
      <div class="highlight-rules">
        {#each $terminalHighlightRules as rule (rule.id)}
          <div class="highlight-rule">
            <div class="highlight-rule-header">
              <div class="setting-label"><span class="highlight-swatch" style={`background: ${rule.color}`}></span>{rule.label}</div>
              <label class="highlight-color-label" title={`${rule.label}颜色`}>
                <span>颜色</span>
                <input type="color" value={rule.color} aria-label={`${rule.label}颜色`} oninput={(event) => updateHighlightColor(rule.id, event)} />
              </label>
            </div>
            {#if rule.id === "ip" || rule.id === "number"}
              <div class="setting-desc highlight-auto-desc">自动识别{rule.id === "ip" ? " IPv4 地址" : "整数和小数"}；可在下方追加关键词。</div>
            {/if}
            <textarea class="security-textarea highlight-keywords" rows="2" value={rule.keywords.join(", ")}
                      aria-label={`${rule.label}关键词`} oninput={(event) => updateHighlightKeywords(rule.id, event)}
                      placeholder="用逗号分隔关键词"></textarea>
          </div>
        {/each}
      </div>
      <div class="highlight-actions">
        <button type="button" class="io-btn import" onclick={resetHighlightRules}>恢复默认关键词</button>
        <span class="setting-desc">关键词不区分大小写；修改后对新收到的终端输出立即生效。</span>
      </div>

    {:else if $settingsSection === 'security'}
      <h3 class="section-title">安全配置</h3>
      <p class="setting-desc security-intro">配置 Agent 命令执行边界。删除权限独立控制；Shell 控制符、命令解释器和审批流程仍由安全底线保护。</p>

      <div class="security-section">
        <div class="security-section-header">
          <div>
            <div class="setting-label">只读命令无需审批</div>
            <div class="setting-desc">开启后，已通过程序和 AI 只读判定的终端命令可直接执行；写入、删除、未知命令和 SFTP 操作仍需审批。</div>
          </div>
          <button type="button" class="toggle" role="switch" aria-label="只读命令无需审批" aria-checked={readOnlyNoApproval}
                  onclick={() => readOnlyNoApproval = !readOnlyNoApproval}><span class="toggle-slider"></span></button>
        </div>
        <div class="security-baseline"><ShieldCheck class="w-4 h-4" /> 默认关闭；仅对明确判定为只读的终端命令生效</div>
      </div>

      <div class="security-section">
        <div class="security-section-header">
          <div>
            <div class="setting-label">命令白名单</div>
            <div class="setting-desc">每行填写一个命令名称或完整路径。启用后，只有列表中的只读命令可以执行。</div>
          </div>
          <button type="button" class="toggle" role="switch" aria-label="启用命令白名单" aria-checked={commandWhitelistEnabled}
                  onclick={() => commandWhitelistEnabled = !commandWhitelistEnabled}><span class="toggle-slider"></span></button>
        </div>
        <textarea class="security-textarea" rows="8" bind:value={commandWhitelistText} spellcheck="false" placeholder="例如：\nlscpu\nnproc\nuname"></textarea>
      </div>

      <div class="security-section">
        <div class="security-section-header">
          <div>
            <div class="setting-label">命令黑名单</div>
            <div class="setting-desc">每行填写一个命令名称。黑名单优先级高于白名单，适合临时禁用特定命令。</div>
          </div>
          <button type="button" class="toggle" role="switch" aria-label="启用命令黑名单" aria-checked={commandBlacklistEnabled}
                  onclick={() => commandBlacklistEnabled = !commandBlacklistEnabled}><span class="toggle-slider"></span></button>
        </div>
        <textarea class="security-textarea" rows="5" bind:value={commandBlacklistText} spellcheck="false" placeholder="例如：\ndocker\nkubectl"></textarea>
      </div>

      <div class="security-section">
        <div class="security-section-header">
          <div>
            <div class="setting-label">允许 Agent 执行写操作</div>
            <div class="setting-desc">关闭后，即使任务明确授权，也不会执行文件写入或服务启停、重启等变更操作；命令审批仍然保留。</div>
          </div>
          <button type="button" class="toggle" role="switch" aria-label="允许 Agent 执行写操作" aria-checked={mutationsEnabled}
                  onclick={() => mutationsEnabled = !mutationsEnabled}><span class="toggle-slider"></span></button>
        </div>
        <div class="security-baseline"><ShieldCheck class="w-4 h-4" /> 仅允许逐段校验的安全命令序列；重定向、命令替换和危险命令拦截始终启用</div>
      </div>

      <div class="security-section deletion-setting">
        <div class="security-section-header">
          <div>
            <div class="setting-label">允许 Agent 执行删除操作</div>
            <div class="setting-desc">打开后，rm、rmdir、kubectl delete 等删除命令仍需任务授权、首次审批和删除二次确认。</div>
          </div>
          <button type="button" class="toggle" role="switch" aria-label="允许 Agent 执行删除操作" aria-checked={deletionsEnabled}
                  onclick={() => deletionsEnabled = !deletionsEnabled}><span class="toggle-slider"></span></button>
        </div>
        <div class="security-baseline deletion-baseline"><Trash2 class="w-4 h-4" /> 删除操作不可逆，启用后每条删除命令都必须二次确认</div>
      </div>

      <div class="security-section administrator-setting">
        <div class="security-section-header">
          <div>
            <div class="setting-label">管理员模式</div>
            <div class="setting-desc">开启后，Agent 不再受命令白名单、黑名单、AI 判断、Shell 语法、写操作与安全计划限制；每条命令仍需人工批准，删除操作仍需二次确认。</div>
          </div>
          <button type="button" class="toggle" role="switch" aria-label="管理员模式" aria-checked={administratorEnabled}
                  onclick={toggleAdministratorMode}><span class="toggle-slider"></span></button>
        </div>
        <div class="security-baseline administrator-baseline"><ShieldAlert class="w-4 h-4" /> 开启需要两次确认，保存后立即生效</div>
      </div>

      <button type="button" class="primary-settings-action" onclick={saveSecurityConfig} disabled={securityBusy}>
        {securityBusy ? "保存中..." : "保存安全配置"}
      </button>

    {:else if $settingsSection === 'ai'}
      <AISettingsPage />

    {:else if $settingsSection === 'about'}
      <h3 class="section-title">关于 GoSSH</h3>
      <div class="about-card">
        <div class="about-logo">
          <div class="logo-icon">
            <img src={logoUrl} alt="" />
          </div>
          <div class="about-info">
            <div class="about-name">GoSSH</div>
            <div class="about-ver">v0.1.0</div>
          </div>
        </div>
        <div class="about-desc">
          现代化的 SSH / SFTP / 串口 / AI 终端工具<br/>
          使用 Go + Svelte 5 + Wails 2 构建
        </div>
        <div class="about-tech">
          <span class="tech-badge">Go 1.26</span>
          <span class="tech-badge">Wails 2.13</span>
          <span class="tech-badge">Svelte 5</span>
          <span class="tech-badge">xterm.js 6</span>
          <span class="tech-badge">DaisyUI 5</span>
        </div>
      </div>
    {:else if $settingsSection === 'diagnostics'}
      <div class="diagnostics-header">
        <div>
          <h3 class="section-title">连接诊断</h3>
          <div class="setting-desc">查看运行环境、SSH 算法能力和最近连接事件</div>
        </div>
        <button class="io-btn export" onclick={loadDiagnostics}>刷新</button>
      </div>
      {#if diagnostics}
        <div class="diagnostic-grid">
          <div class="diagnostic-item"><span>Go 版本</span><strong>{diagnostics.goVersion}</strong></div>
          <div class="diagnostic-item"><span>x/crypto 版本</span><strong>{diagnostics.xCryptoVersion || "构建信息不可用"}</strong></div>
          <div class="diagnostic-item"><span>SSH Agent</span><strong class:diagnostic-ok={diagnostics.sshAgentAvailable}>{diagnostics.sshAgentAvailable ? "可用" : "未设置"}</strong></div>
          <div class="diagnostic-item"><span>known_hosts</span><strong class:diagnostic-ok={diagnostics.knownHostsExists}>{diagnostics.knownHostsExists ? "已找到" : "不存在"}</strong></div>
          <div class="diagnostic-item"><span>后量子 KEX</span><strong class:diagnostic-ok={diagnostics.security?.postQuantumKex}>{diagnostics.security?.postQuantumKex ? "支持" : "不支持"}</strong></div>
          <div class="diagnostic-item"><span>不安全算法</span><strong>{diagnostics.security?.insecureAlgorithms ?? 0} 项已排除</strong></div>
        </div>
        <div class="diagnostic-section">
          <div class="diagnostic-section-title"><ShieldCheck class="w-4 h-4" /> SSH 算法</div>
          <div class="diagnostic-code">
            KEX: {diagnostics.security?.keyExchanges?.join(", ")}<br />
            HostKey: {diagnostics.security?.hostKeyAlgorithms?.join(", ")}<br />
            Cipher: {diagnostics.security?.ciphers?.join(", ")}
          </div>
        </div>
        <div class="diagnostic-section">
          <div class="diagnostic-section-title"><Activity class="w-4 h-4" /> 操作统计</div>
          {#if Object.keys(diagnostics.observability?.counters || {}).length === 0}
            <div class="setting-desc">暂无连接或传输记录</div>
          {:else}
            <div class="diagnostic-events">
              {#each (Object.entries(diagnostics.observability.counters) as [string, DiagnosticsCounter][]) as [name, counter]}
                <div class="diagnostic-event">
                  <span>{name}</span>
                  <span>{counter.count} 次 / {counter.failures} 次失败 / 最近 {counter.lastMillis} ms</span>
                </div>
              {/each}
            </div>
          {/if}
        </div>
        <div class="diagnostic-section">
          <div class="diagnostic-section-title">最近事件</div>
          <div class="diagnostic-events">
            {#each (diagnostics.observability?.events || []).slice().reverse().slice(0, 20) as event: any}
              <div class="diagnostic-event">
                <span>{event.area}.{event.name}</span>
                <span class:diagnostic-ok={event.status === "ok"}>{event.status} / {event.durationMs} ms</span>
              </div>
            {/each}
          </div>
        </div>
      {:else}
        <div class="setting-desc">正在加载诊断信息...</div>
      {/if}
    {/if}
  </div>
</div>

{#if toast}<div class="toast global-toast">{toast}</div>{/if}

<style>
  .settings-root {
    display: flex; height: 100%; overflow: hidden;
    background: var(--app-panel);
  }
  .settings-nav {
    width: 180px; border-right: 1px solid var(--app-border);
    background: var(--app-panel-muted); padding: 12px 8px;
    display: flex; flex-direction: column; gap: 2px;
    flex-shrink: 0;
  }
  .nav-item {
    display: flex; align-items: center; gap: 8px;
    padding: 8px 12px; border-radius: 8px; border: none; cursor: pointer;
    font-size: 13px; color: var(--app-muted); background: transparent;
    transition: all 0.15s; text-align: left;
  }
  .nav-item:hover { background: var(--app-hover); color: var(--app-text); }
  .nav-item.active { background: var(--app-accent-soft); color: var(--app-accent); }
  .settings-content {
    flex: 1; padding: 24px; overflow-y: auto;
  }
  .section-title {
    font-size: 18px; font-weight: 600; color: var(--app-text); margin-bottom: 20px;
  }
  .setting-row {
    display: flex; align-items: center; justify-content: space-between;
    gap: 16px; padding: 12px 0; border-bottom: 1px solid var(--app-border);
  }
  .setting-label { font-size: 13px; font-weight: 500; color: var(--app-text); margin-bottom: 2px; }
  .setting-desc { font-size: 11px; color: var(--app-muted); }
  .ui-theme-switch {
    display: inline-flex; align-items: center; gap: 3px; padding: 3px;
    border: 1px solid var(--app-border);
    border-radius: 8px; background: var(--app-panel-muted); flex: 0 0 auto;
  }
  .ui-theme-switch button {
    min-width: 54px; height: 28px; padding: 0 10px;
    border: 0; border-radius: 6px; background: transparent;
    color: var(--app-muted); font-size: 12px; cursor: pointer;
    transition: background 0.15s, color 0.15s, box-shadow 0.15s;
  }
  .ui-theme-switch button:hover { color: var(--app-text); background: var(--app-hover); }
  .ui-theme-switch button.active {
    color: var(--app-accent); background: var(--app-accent-soft);
    box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--app-accent) 18%, transparent);
  }
  .toggle {
    position: relative; display: inline-block; width: 40px; height: 22px;
    padding: 0; border: 0; border-radius: 22px; background: transparent;
    cursor: pointer; appearance: none; flex: 0 0 auto;
  }
  .toggle-slider {
    position: absolute; inset: 0; border-radius: 22px; background: var(--app-subtle);
    transition: 0.2s;
  }
  .toggle-slider::before {
    content: ""; position: absolute; left: 2px; top: 2px;
    width: 18px; height: 18px; border-radius: 50%; background: white;
    transition: 0.2s;
  }
  .toggle[aria-checked="true"] .toggle-slider { background: var(--app-accent-strong); }
  .toggle[aria-checked="true"] .toggle-slider::before { transform: translateX(18px); }
  .toggle:focus-visible .toggle-slider { outline: 2px solid var(--app-accent); outline-offset: 2px; }
  .theme-grid {
    display: grid; grid-template-columns: repeat(auto-fill, minmax(120px, 1fr));
    gap: 10px;
  }
  .theme-card {
    padding: 10px; border-radius: 10px;
    border: 2px solid transparent; cursor: pointer;
    background: var(--app-panel-muted); transition: all 0.15s; text-align: center;
  }
  .theme-card:hover { background: var(--app-hover); }
  .theme-card.selected { border-color: var(--app-accent-strong); background: var(--app-accent-soft); }
  .theme-preview {
    height: 32px; border-radius: 6px; display: flex; align-items: center; justify-content: center;
    gap: 4px; margin-bottom: 8px;
  }
  .theme-dot { width: 8px; height: 8px; border-radius: 50%; }
  .theme-name { font-size: 11px; color: var(--app-muted); text-transform: capitalize; }
  .security-intro { max-width: 720px; margin: -8px 0 20px; line-height: 1.6; }
  .security-section { max-width: 720px; margin-bottom: 16px; padding: 14px; border: 1px solid var(--app-border); border-radius: 8px; background: var(--app-panel-muted); }
  .deletion-setting { border-color: rgba(248,113,113,0.35); background: rgba(127,29,29,0.08); }
  .deletion-setting .setting-label { color: #fca5a5; }
  .administrator-setting { border-color: rgba(251,146,60,0.5); background: rgba(124,45,18,0.12); }
  .administrator-setting .setting-label { color: #fdba74; }
  .security-section-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
  .security-textarea { display: block; box-sizing: border-box; width: 100%; min-height: 88px; margin-top: 12px; padding: 9px 10px; resize: vertical; border: 1px solid var(--app-border-strong); border-radius: 6px; outline: none; background: var(--app-panel-strong); color: var(--app-text); font: 12px/1.5 var(--font-mono); }
  .security-textarea:focus { border-color: var(--app-accent); box-shadow: 0 0 0 2px var(--app-accent-soft); }
  .security-baseline { display: flex; align-items: center; gap: 7px; margin-top: 12px; padding-top: 10px; border-top: 1px solid var(--app-border); color: var(--app-muted); font-size: 11px; }
  .deletion-baseline { color: #fca5a5; border-top-color: rgba(248,113,113,0.24); }
  .administrator-baseline { color: #fdba74; border-top-color: rgba(251,146,60,0.3); }
  .primary-settings-action { padding: 9px 16px; border: 1px solid var(--app-accent-strong); border-radius: 6px; background: var(--app-accent-strong); color: white; font-size: 12px; cursor: pointer; }
  .primary-settings-action:hover:not(:disabled) { filter: brightness(1.08); }
  .primary-settings-action:disabled { cursor: wait; opacity: .55; }
  .terminal-background-title { margin-top: 28px; margin-bottom: 16px; }
  .terminal-background-settings { max-width: 640px; padding: 14px; border: 1px solid var(--app-border); border-radius: 8px; background: var(--app-panel-muted); }
  .background-preview { display: flex; align-items: center; justify-content: center; height: 120px; margin-bottom: 12px; border-radius: 6px; border: 1px solid var(--app-border); background-color: var(--app-panel-strong); background-position: center; background-size: cover; background-repeat: no-repeat; color: var(--app-muted); font-size: 12px; }
  .background-preview.empty { background-image: repeating-linear-gradient(135deg, transparent 0 10px, rgba(148,163,184,.08) 10px 20px); }
  .background-setting-actions { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
  .background-action { display: inline-flex; align-items: center; gap: 5px; }
  .background-file-input { display: none; }
  .background-opacity-group { margin: 14px 0 0; }
  .background-error { margin-top: 8px; color: #fca5a5; font-size: 11px; line-height: 1.4; }
  .terminal-typography-title { margin-top: 28px; margin-bottom: 16px; }
  .terminal-settings-grid {
    display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px 20px;
    max-width: 640px;
  }
  .terminal-select {
    width: 100%; background: var(--app-panel-strong); border: 1px solid var(--app-border-strong);
    border-radius: 8px; padding: 9px 10px; font-size: 13px; color: var(--app-text);
    outline: none; min-height: 38px;
  }
  .font-preview { font-family: var(--font-mono); }
  .terminal-settings-grid input[type="range"] { width: 100%; accent-color: var(--app-accent); }
  .terminal-settings-grid input[type="number"] { width: 100%; box-sizing: border-box; min-height: 38px; padding: 8px 10px; border: 1px solid var(--app-border-strong); border-radius: 6px; background: var(--app-panel-strong); color: var(--app-text); font: 13px var(--font-mono); outline: none; }
  .cursor-color-control { display: flex; align-items: center; gap: 8px; }
  .cursor-color-control input[type="color"] { width: 38px; height: 38px; padding: 3px; border: 1px solid var(--app-border-strong); border-radius: 6px; background: var(--app-panel-strong); cursor: pointer; }
  .terminal-settings-grid input[type="number"]:focus { border-color: var(--app-accent); box-shadow: 0 0 0 2px var(--app-accent-soft); }
  .terminal-settings-grid output { float: right; color: var(--app-accent); font-size: 11px; font-weight: 400; text-transform: none; }
  .compact-setting { grid-column: 1 / -1; max-width: 640px; padding: 12px 0 0; border-bottom: none; }
  .highlight-toggle-row { max-width: 640px; }
  .highlight-rules { display: grid; gap: 10px; max-width: 640px; margin-top: 14px; }
  .highlight-rule { padding: 12px; border: 1px solid var(--app-border); border-radius: 8px; background: var(--app-panel-muted); }
  .highlight-rule-header { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
  .highlight-swatch { display: inline-block; width: 10px; height: 10px; margin-right: 7px; border-radius: 50%; vertical-align: 1px; }
  .highlight-color-label { display: inline-flex; align-items: center; gap: 6px; color: var(--app-muted); font-size: 11px; }
  .highlight-color-label input { width: 28px; height: 22px; padding: 0; border: 1px solid var(--app-border-strong); border-radius: 4px; background: transparent; cursor: pointer; }
  .highlight-auto-desc { margin-top: 6px; }
  .highlight-keywords { min-height: 46px; margin-top: 8px; }
  .highlight-actions { display: flex; align-items: center; gap: 10px; max-width: 640px; margin-top: 12px; flex-wrap: wrap; }
  .form-group { display: flex; flex-direction: column; gap: 6px; margin-bottom: 16px; }
  .form-group label { font-size: 11px; font-weight: 500; color: var(--app-muted); text-transform: uppercase; letter-spacing: 0.5px; }
  .toast {
    position: fixed; bottom: 30px; right: 30px;
    padding: 10px 20px; border-radius: 8px;
    background: #22c55e; color: white; font-size: 13px;
    box-shadow: 0 8px 24px rgba(0,0,0,0.3);
    animation: slideUp 0.3s ease;
  }
  @keyframes slideUp { from { transform: translateY(20px); opacity: 0; } to { transform: none; opacity: 1; } }
  .about-card { padding: 20px; border-radius: 12px; background: var(--app-panel-muted); border: 1px solid var(--app-border); }
  .about-logo { display: flex; align-items: center; gap: 14px; margin-bottom: 16px; }
  .logo-icon {
    width: 56px; height: 56px; border-radius: 16px;
    display: flex; align-items: center; justify-content: center;
    box-shadow: 0 8px 24px rgba(99, 102, 241, 0.3);
    overflow: hidden;
  }
  .logo-icon img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .about-name { font-size: 20px; font-weight: 700; color: var(--app-text); }
  .about-ver { font-size: 12px; color: var(--app-muted); }
  .about-desc { font-size: 13px; color: var(--app-muted); line-height: 1.8; margin-bottom: 14px; }
  .about-tech { display: flex; gap: 6px; flex-wrap: wrap; }
  .tech-badge {
    padding: 4px 10px; border-radius: 6px; font-size: 11px;
    background: var(--app-accent-soft); color: var(--app-accent);
    border: 1px solid rgba(99, 102, 241, 0.2);
  }
  .io-buttons { display: flex; gap: 8px; }
  .io-btn {
    padding: 8px 18px; border-radius: 8px; border: 1px solid var(--app-border);
    font-size: 12px; font-weight: 500; cursor: pointer; transition: all 0.15s;
  }
  .io-btn.export { background: var(--app-accent-soft); color: var(--app-accent); border-color: rgba(99, 102, 241, 0.2); }
  .io-btn.import { background: var(--app-panel-muted); color: var(--app-muted); }
  .io-btn.export:hover { background: rgba(99, 102, 241, 0.2); }
  .io-btn.import:hover { background: var(--app-hover); color: var(--app-text); }
  .diagnostics-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 20px; }
  .diagnostics-header .section-title { margin-bottom: 4px; }
  .diagnostic-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px 16px; max-width: 720px; margin: 20px 0; }
  .diagnostic-item { display: flex; justify-content: space-between; gap: 12px; padding: 10px 0; border-bottom: 1px solid var(--app-border); color: var(--app-muted); font-size: 12px; }
  .diagnostic-item strong { color: var(--app-text); font-weight: 500; text-align: right; }
  .diagnostic-ok { color: #4ade80 !important; }
  .diagnostic-section { max-width: 900px; margin-top: 20px; }
  .diagnostic-section-title { display: flex; align-items: center; gap: 6px; color: var(--app-text); font-size: 12px; font-weight: 600; margin-bottom: 8px; }
  .diagnostic-code { padding: 10px 12px; border: 1px solid var(--app-border); border-radius: 6px; background: var(--app-panel-muted); color: var(--app-accent); font: 11px/1.8 monospace; overflow-wrap: anywhere; }
  .diagnostic-events { display: flex; flex-direction: column; border-top: 1px solid var(--app-border); }
  .diagnostic-event { display: flex; justify-content: space-between; gap: 16px; padding: 8px 0; border-bottom: 1px solid var(--app-border); color: var(--app-muted); font: 11px/1.4 monospace; }
  .diagnostic-event span:last-child { color: var(--app-muted); text-align: right; }
  @media (max-width: 700px) {
    .diagnostic-grid { grid-template-columns: 1fr; }
    .diagnostic-event { flex-direction: column; gap: 2px; }
    .diagnostic-event span:last-child { text-align: left; }
  }
</style>
