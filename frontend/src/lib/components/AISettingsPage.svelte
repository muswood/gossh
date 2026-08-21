<!-- owner: muswood | Email: mumu920@outlook.com -->
<script lang="ts">
  import { onMount } from "svelte";
  import { Check, Code2, Database, Download, History, KeyRound, Search, Server, ShieldCheck, Sparkles, Trash2, Unplug, Upload, Workflow, X } from "lucide-svelte";
  import { AgentConnectMCP, AgentDeleteMCPServer, AgentDisconnectMCP, AgentListMCPServers, LoadAIConfig, RAGAddDocument, RAGDeleteDocument, RAGListDocuments, RAGReindex, RAGSearch, SaveAIConfig, SkillCheck, SkillDelete, SkillDocument, SkillEnable, SkillExportToFile, SkillGenerateSigningKey, SkillHistory, SkillImportFromFile, SkillRestore, SkillRevokeKey, SkillSave, SkillSearch, SkillSign, SkillTrustKey } from "../../../wailsjs/go/main/App";
  import { main } from "../../../wailsjs/go/models";
  import { aiHistoryRetentionDays } from "$lib/stores";

  type RAGDocument = { id: string; title: string };
  type RAGResult = { title: string; snippet: string };
  type MCPServer = { id: string; transport?: string; endpoint?: string; command: string; args?: string[]; targetIds?: string[]; connected?: boolean; hasEnv?: boolean; hasAuthToken?: boolean; hasOAuthToken?: boolean };
  type SkillParameter = { type: string; description?: string; required?: boolean; default?: any; enum?: string[] };
  type SkillManifest = { id: string; name: string; version: string; description?: string; mode?: string; allowedTools: string[]; maxSteps?: number; timeoutSeconds?: number; requiresApproval?: boolean; prompt: string; parameters?: Record<string, SkillParameter>; category?: string; tags?: string[]; dependencies?: any[]; workflow?: any[]; reportTemplate?: string; trustStatus?: string; source?: string; enabled?: boolean; builtin?: boolean };

  const providerPresets = [
    { value: "deepseek", label: "DeepSeek", model: "deepseek-chat", baseURL: "https://api.deepseek.com/v1" },
    { value: "openai", label: "OpenAI", model: "gpt-4o", baseURL: "https://api.openai.com/v1" },
    { value: "claude", label: "Claude", model: "claude-3-5-sonnet-20241022", baseURL: "https://api.anthropic.com/v1" },
    { value: "qwen", label: "通义千问", model: "qwen-plus", baseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1" },
    { value: "newapi", label: "New API", model: "gpt-4o-mini", baseURL: "https://token.uino.com/v1" },
  ];

  let apiKey = $state("");
  let provider = $state("deepseek");
  let customProvider = $state("");
  let model = $state("deepseek-chat");
  let embeddingModel = $state("text-embedding-3-small");
  let baseURL = $state("https://api.deepseek.com/v1");
  let apiMode = $state("chat");
  let maxTokens = $state(393216);
  let agentMaxSteps = $state(16);
  let ragEnabled = $state(false);
	let ragVectorBackend = $state("sqlite");
	let ragVectorEndpoint = $state("");
	let ragVectorCollection = $state("gossh_documents");
	let ragVectorApiKey = $state("");
  let documents = $state<RAGDocument[]>([]);
  let title = $state("");
  let content = $state("");
  let query = $state("");
  let results = $state<RAGResult[]>([]);
  let toast = $state("");
  let saving = $state(false);
  let mcpServers = $state<MCPServer[]>([]);
  let mcpId = $state("");
  let mcpTransport = $state("stdio");
  let mcpEndpoint = $state("");
  let mcpCommand = $state("");
  let mcpArgs = $state("");
  let mcpEnv = $state("");
  let mcpAuthToken = $state("");
  let mcpOAuthAccessToken = $state("");
  let mcpAllowedTools = $state("");
  let mcpBusy = $state(false);
  let skills = $state<SkillManifest[]>([]);
  let skillEditor = $state("");
	let editingSkillID = $state("");
  let skillBusy = $state(false);
  let skillQuery = $state("");
  let skillHistory = $state<SkillManifest[]>([]);
  let skillHistoryID = $state("");
  let skillCheckResult = $state("");
  let trustedSigner = $state("");
  let trustedPublicKey = $state("");
  let signingPrivateKey = $state("");

  onMount(async () => {
    await Promise.all([loadConfig(), loadDocuments(), loadMCPServers(), loadSkills()]);
  });

  function notify(message: string) {
    toast = message;
    window.setTimeout(() => {
      if (toast === message) toast = "";
    }, 2200);
  }

  function selectProvider(value: string) {
    provider = value;
    const preset = providerPresets.find(item => item.value === value);
    if (!preset) {
      model = "";
      baseURL = "";
      apiMode = "chat";
      return;
    }
    model = preset.model;
    baseURL = preset.baseURL;
    if (value !== "openai") apiMode = "chat";
  }

  async function loadConfig() {
    try {
      const config = await LoadAIConfig();
      if (!config) return;
      const preset = providerPresets.find(item => item.value === config.provider);
      provider = preset ? config.provider : "custom";
      customProvider = preset ? "" : config.provider || "";
      model = config.model || model;
      embeddingModel = config.embeddingModel || embeddingModel;
      baseURL = config.baseURL || baseURL;
      apiMode = config.apiMode || "chat";
      const configuredMaxTokens = Number(config.maxTokens);
      maxTokens = Number.isInteger(configuredMaxTokens) && configuredMaxTokens > 0
        ? Math.min(configuredMaxTokens, 900000)
        : 393216;
      const configuredAgentMaxSteps = Number(config.agentMaxSteps);
      agentMaxSteps = Number.isInteger(configuredAgentMaxSteps) && configuredAgentMaxSteps >= 1 && configuredAgentMaxSteps <= 50
        ? configuredAgentMaxSteps
        : 16;
      if (provider !== "openai") apiMode = "chat";
      ragEnabled = Boolean(config.ragEnabled);
		ragVectorBackend = config.ragVectorBackend || "sqlite";
		ragVectorEndpoint = config.ragVectorEndpoint || "";
		ragVectorCollection = config.ragVectorCollection || "gossh_documents";
    } catch (error) {
      notify(`加载 AI 配置失败: ${error}`);
    }
  }

  async function saveConfig() {
    const selectedProvider = provider === "custom" ? customProvider.trim() : provider;
    if (!selectedProvider) {
      notify("请填写自定义供应商名称");
      return;
    }
    if (!model.trim()) {
      notify("请填写模型名称");
      return;
    }
    if (!embeddingModel.trim()) {
      notify("请填写 Embedding 模型名称");
      return;
    }
    if (!baseURL.trim()) {
      notify("请填写 API 地址");
      return;
    }
    const parsedMaxTokens = Number(maxTokens);
    if (!Number.isInteger(parsedMaxTokens) || parsedMaxTokens < 1 || parsedMaxTokens > 900000) {
      notify("最大输出 Token 必须是 1 到 900000 的整数");
      return;
    }
    const parsedAgentMaxSteps = Number(agentMaxSteps);
    if (!Number.isInteger(parsedAgentMaxSteps) || parsedAgentMaxSteps < 1 || parsedAgentMaxSteps > 50) {
      notify("Agent 最大迭代次数必须是 1 到 50 的整数");
      return;
    }
    saving = true;
    try {
      await SaveAIConfig({
        provider: selectedProvider,
        model: model.trim(),
        embeddingModel: embeddingModel.trim(),
        apiKey,
        baseURL: baseURL.trim().replace(/\/+$/, ""),
        apiMode,
        ragEnabled,
		ragVectorBackend,
		ragVectorEndpoint: ragVectorEndpoint.trim(),
		ragVectorCollection: ragVectorCollection.trim(),
		ragVectorApiKey,
        maxTokens: parsedMaxTokens,
        temperature: 0.7,
        agentMaxSteps: parsedAgentMaxSteps,
      });
      apiKey = "";
		ragVectorApiKey = "";
      notify("AI 配置已保存");
    } catch (error) {
      notify(`保存失败: ${error}`);
    } finally {
      saving = false;
    }
  }

  async function loadDocuments() {
    try {
      documents = JSON.parse(await RAGListDocuments()) as RAGDocument[];
    } catch (error) {
      notify(`加载知识库失败: ${error}`);
    }
  }

  async function addDocument() {
    if (!title.trim() || !content.trim()) return;
    try {
      await RAGAddDocument(new main.RAGDocumentRequest({ title: title.trim(), content: content.trim() }));
      title = "";
      content = "";
      await loadDocuments();
      notify("知识库文档已添加");
    } catch (error) {
      notify(`添加失败: ${error}`);
    }
  }

  async function deleteDocument(id: string) {
    try {
      await RAGDeleteDocument(id);
      await loadDocuments();
      notify("知识库文档已删除");
    } catch (error) {
      notify(`删除失败: ${error}`);
    }
  }

  async function reindexDocuments() {
    try {
      const changed = await RAGReindex();
      notify(changed ? `已更新 ${changed} 个文档的向量索引` : "向量索引已经是最新");
      await loadDocuments();
    } catch (error) {
      notify(`重建索引失败: ${error}`);
    }
  }

  async function searchDocuments() {
    if (!query.trim()) {
      results = [];
      return;
    }
    try {
      results = JSON.parse(await RAGSearch(query.trim())) as RAGResult[];
    } catch (error) {
      notify(`检索失败: ${error}`);
    }
  }

  async function loadMCPServers() {
    try {
      mcpServers = JSON.parse(await AgentListMCPServers()) as MCPServer[];
    } catch (error) {
      notify(`加载 MCP 配置失败: ${error}`);
    }
  }

  function splitLines(value: string): string[] {
    return value.split(/\r?\n/).map(item => item.trim()).filter(Boolean);
  }

  async function connectMCP() {
    const id = mcpId.trim();
    const command = mcpCommand.trim();
    if (!id || !command) {
      notify("请填写 MCP 标识和启动命令");
      return;
    }
    mcpBusy = true;
    try {
      await AgentConnectMCP({
        id,
        transport: mcpTransport,
        endpoint: mcpEndpoint.trim(),
        command,
        args: splitLines(mcpArgs),
        env: splitLines(mcpEnv),
        authToken: mcpAuthToken.trim(),
        oauthAccessToken: mcpOAuthAccessToken.trim(),
        allowedTools: splitLines(mcpAllowedTools),
        targetIds: [],
      } as any);
      await loadMCPServers();
      notify("MCP 已连接，工具已按白名单加载");
    } catch (error) {
      notify(`MCP 连接失败: ${error}`);
    } finally {
      mcpBusy = false;
    }
  }

  async function disconnectMCP(id: string) {
    mcpBusy = true;
    try {
      await AgentDisconnectMCP(id);
      await loadMCPServers();
      notify("MCP 已断开");
    } catch (error) {
      notify(`断开 MCP 失败: ${error}`);
    } finally {
      mcpBusy = false;
    }
  }

  async function deleteMCP(id: string) {
    if (!window.confirm(`删除 MCP 配置 ${id}?`)) return;
    mcpBusy = true;
    try {
      await AgentDeleteMCPServer(id);
      await loadMCPServers();
      notify("MCP 配置已删除");
    } catch (error) {
      notify(`删除 MCP 配置失败: ${error}`);
    } finally {
      mcpBusy = false;
    }
  }

  async function loadSkills() {
    try {
      skills = JSON.parse(await SkillSearch(skillQuery)) as SkillManifest[];
    } catch (error) {
      notify(`加载 Skill 失败: ${error}`);
    }
  }

  async function checkSkill(skill: SkillManifest) {
    try {
      const result = JSON.parse(await SkillCheck(skill.id));
      skillCheckResult = result.ok ? `${skill.name} 依赖完整，信任状态：${result.trustStatus}` : `${skill.name} 缺少依赖：${result.missing.join(", ")}`;
    } catch (error) { notify(`检查 Skill 失败: ${error}`); }
  }

  async function loadSkillHistory(skill: SkillManifest) {
    try {
      skillHistoryID = skill.id;
      skillHistory = JSON.parse(await SkillHistory(skill.id)) as SkillManifest[];
    } catch (error) { notify(`加载版本历史失败: ${error}`); }
  }

  async function restoreSkill(skill: SkillManifest) {
    if (!window.confirm(`恢复 Skill ${skill.id} 到版本 ${skill.version}?`)) return;
    try {
      await SkillRestore(skill.id, skill.version);
      await loadSkills();
      notify("Skill 已恢复");
    } catch (error) { notify(`恢复 Skill 失败: ${error}`); }
  }

  async function importSkill() {
    try { await SkillImportFromFile(); await loadSkills(); notify("Skill 已导入"); }
    catch (error) { notify(`导入 Skill 失败: ${error}`); }
  }

  async function exportSkill(skill: SkillManifest) {
    try { await SkillExportToFile(skill.id); notify("Skill 已导出"); }
    catch (error) { notify(`导出 Skill 失败: ${error}`); }
  }

  async function saveTrustedKey() {
    if (!trustedSigner.trim() || !trustedPublicKey.trim()) { notify("请填写签名者和 Ed25519 公钥"); return; }
    try { await SkillTrustKey(trustedSigner.trim(), trustedPublicKey.trim()); notify("可信公钥已保存"); }
    catch (error) { notify(`保存可信公钥失败: ${error}`); }
  }

  async function generateSigningKey() {
    try {
      const keys = JSON.parse(await SkillGenerateSigningKey());
      trustedPublicKey = keys.publicKey || "";
      signingPrivateKey = keys.privateKey || "";
      notify("已生成密钥对；请安全保存私钥");
    } catch (error) { notify(`生成签名密钥失败: ${error}`); }
  }

  async function signEditedSkill() {
    if (!skillEditor.trim() || !trustedSigner.trim() || !signingPrivateKey.trim()) { notify("请准备 Skill Markdown、签名者和私钥"); return; }
    try { skillEditor = await SkillSign(skillEditor, trustedSigner.trim(), signingPrivateKey.trim()); notify("Skill 已签名，请保存"); }
    catch (error) { notify(`签名失败: ${error}`); }
  }

  async function revokeTrustedKey() {
    if (!trustedSigner.trim()) { notify("请填写签名者"); return; }
    try { await SkillRevokeKey(trustedSigner.trim()); notify("可信公钥已撤销"); }
    catch (error) { notify(`撤销可信公钥失败: ${error}`); }
  }

  async function editSkill(skill: SkillManifest) {
    try {
      skillEditor = await SkillDocument(skill.id);
      editingSkillID = skill.id;
    } catch (error) {
      notify(`读取 Skill Markdown 失败: ${error}`);
    }
  }

  function newSkill() {
    editingSkillID = "";
    skillEditor = `---
name: my-skill
description: Describe when this skill should be used.
---

# My Skill

Use the available tools only when they are necessary. State assumptions, collect evidence, and provide a concise final report.`;
  }

  async function saveSkill() {
    if (!skillEditor.trim()) return;
    skillBusy = true;
    try {
      await SkillSave(skillEditor);
      await loadSkills();
		editingSkillID = "";
      notify("Skill 已保存");
    } catch (error) {
      notify(`保存 Skill 失败: ${error}`);
    } finally {
      skillBusy = false;
    }
  }

  async function toggleSkill(skill: SkillManifest) {
    skillBusy = true;
    try {
      await SkillEnable(skill.id, !skill.enabled);
      await loadSkills();
    } catch (error) {
      notify(`更新 Skill 状态失败: ${error}`);
    } finally {
      skillBusy = false;
    }
  }

  async function deleteSkill(skill: SkillManifest) {
    if (skill.builtin || !window.confirm(`删除 Skill ${skill.name}?`)) return;
    skillBusy = true;
    try {
      await SkillDelete(skill.id);
      if (editingSkillID === skill.id) {
        skillEditor = "";
        editingSkillID = "";
      }
      await loadSkills();
      notify("Skill 已删除");
    } catch (error) {
      notify(`删除 Skill 失败: ${error}`);
    } finally {
      skillBusy = false;
    }
  }
</script>

<section class="ai-settings" aria-label="AI 配置">
  <header class="page-header">
    <div class="header-icon"><Sparkles size={20} /></div>
    <div>
      <h2>AI 配置</h2>
      <p>配置终端助手使用的模型与本地知识库。</p>
    </div>
  </header>

  <div class="content-grid">
    <section class="config-section" aria-label="模型配置">
      <h3><KeyRound size={16} /> 模型连接</h3>
      <div class="field-grid">
        <label>
          <span>模型供应商</span>
          <select value={provider} onchange={(event) => selectProvider((event.currentTarget as HTMLSelectElement).value)}>
            {#each providerPresets as preset}
              <option value={preset.value}>{preset.label}</option>
            {/each}
            <option value="custom">自定义供应商</option>
          </select>
        </label>
        {#if provider === "custom"}
          <label>
            <span>自定义供应商名称</span>
            <input placeholder="例如：moonshot、ollama、公司内部网关" bind:value={customProvider} />
          </label>
        {/if}
        <label>
          <span>模型名称</span>
          <input placeholder="例如：deepseek-chat、Qwen/Qwen3-32B" bind:value={model} />
        </label>
        <label>
          <span>Embedding 模型</span>
          <input placeholder="例如：text-embedding-3-small" bind:value={embeddingModel} />
        </label>
        <label>
          <span>API Key</span>
          <input type="password" autocomplete="off" placeholder="保留已保存的密钥" bind:value={apiKey} />
        </label>
        <label>
          <span>自定义 API 地址</span>
          <input type="url" placeholder="https://api.example.com/v1" bind:value={baseURL} />
        </label>
        <label>
          <span>API 模式</span>
          <select bind:value={apiMode}>
            <option value="chat">Chat Completions</option>
            {#if provider === "openai"}<option value="responses">OpenAI Responses + Tools</option>{/if}
          </select>
        </label>
        <label>
          <span>最大输出 Token</span>
          <input type="number" min="1" max="900000" step="1" bind:value={maxTokens} />
        </label>
        <label>
          <span>Agent 最大迭代次数</span>
          <input type="number" min="1" max="50" step="1" bind:value={agentMaxSteps} />
        </label>
        <label>
          <span>AI 历史保存天数</span>
          <input type="number" min="0" max="3650" step="1" bind:value={$aiHistoryRetentionDays} />
        </label>
      </div>
      <p class="field-hint">最大输出 Token 越小越容易截断工具调用或最终报告；Agent 最大迭代次数控制单次分析可调用模型和工具的轮数，范围为 1 到 50，Skill 自身设置的上限优先。AI 历史按终端和 AI 标签页独立保存，超过保存天数会自动清理，填 0 表示不保存历史。</p>
      {#if provider === "custom"}
        <p class="field-hint">自定义供应商按 OpenAI 兼容的 Chat Completions 接口请求，API 地址填写到版本根路径，例如 `https://example.com/v1`。</p>
      {/if}
      <div class="switch-row">
        <div>
          <strong>启用本地知识库</strong>
          <p>允许 AI 检索此设备上的私有文档。</p>
        </div>
        <button type="button" class="rag-toggle" role="switch" aria-label="启用本地知识库" aria-checked={ragEnabled} onclick={() => ragEnabled = !ragEnabled}>
          <span></span>
        </button>
      </div>
		<div class="field-grid rag-vector-fields">
			<label><span>向量索引后端</span><select bind:value={ragVectorBackend}><option value="sqlite">本地 SQLite</option><option value="qdrant">Qdrant ANN</option></select></label>
			{#if ragVectorBackend === "qdrant"}
				<label><span>Qdrant Endpoint</span><input type="url" placeholder="https://cluster.example.cloud" bind:value={ragVectorEndpoint} /></label>
				<label><span>Qdrant Collection</span><input placeholder="gossh_documents" bind:value={ragVectorCollection} /></label>
				<label><span>Qdrant API Key</span><input type="password" autocomplete="off" placeholder="保留已保存的密钥" bind:value={ragVectorApiKey} /></label>
			{/if}
		</div>
		{#if ragVectorBackend === "qdrant"}<p class="field-hint">文档和访问范围仍保留在本地 SQLite；向量索引同步到 Qdrant。保存后点击“更新向量索引”执行首次同步。</p>{/if}
      <button class="primary-action" onclick={saveConfig} disabled={saving}>
        <Check size={16} /> {saving ? "正在保存" : "保存配置"}
      </button>
    </section>

    <section class="knowledge-section" aria-label="本地知识库">
      <h3><Database size={16} /> 本地知识库</h3>
      <div class="knowledge-grid">
        <div class="document-editor">
          <input aria-label="文档标题" placeholder="文档标题" bind:value={title} />
          <textarea aria-label="文档内容" placeholder="粘贴 SSH/SFTP 故障案例、项目文档或命令规范..." bind:value={content}></textarea>
          <button class="secondary-action" onclick={addDocument} disabled={!title.trim() || !content.trim()}>添加文档</button>
        </div>
        <div class="document-list">
          <div class="search-row">
            <input aria-label="检索知识库" placeholder="检索知识库" bind:value={query} onkeydown={(event) => event.key === "Enter" && searchDocuments()} />
            <button class="icon-button" onclick={searchDocuments} title="检索知识库" aria-label="检索知识库"><Search size={16} /></button>
          </div>
          {#if results.length > 0}
            <div class="search-results">
              {#each results as result}
                <article><strong>{result.title}</strong><p>{result.snippet}</p></article>
              {/each}
            </div>
          {/if}
          <div class="stored-documents">
            {#each documents as document (document.id)}
              <div class="document-row">
                <span>{document.title}</span>
                <button class="icon-button danger" onclick={() => deleteDocument(document.id)} title="删除文档" aria-label={`删除 ${document.title}`}><Trash2 size={15} /></button>
              </div>
            {:else}
              <p class="empty-state">暂无文档</p>
            {/each}
          </div>
          <button class="secondary-action" onclick={reindexDocuments}><Database size={15} />更新向量索引</button>
        </div>
      </div>
    </section>

    <section class="mcp-section" aria-label="MCP 工具服务">
      <h3><Server size={16} /> MCP 工具服务</h3>
      <p class="field-hint">仅连接这里明确配置的本地 stdio 服务；环境变量使用加密配置保存，工具白名单为空表示全部暴露。</p>
      <div class="field-grid">
        <label><span>服务标识</span><input placeholder="例如：ops-tools" bind:value={mcpId} /></label>
        <label><span>传输方式</span><select bind:value={mcpTransport}><option value="stdio">本地 stdio</option><option value="http">Streamable HTTP</option></select></label>
        {#if mcpTransport === "http"}
          <label><span>HTTP Endpoint</span><input type="url" placeholder="https://mcp.example.com/mcp" bind:value={mcpEndpoint} /></label>
          <label><span>OAuth Access Token（可选）</span><input type="password" autocomplete="off" placeholder="已有 token；401 时发现 OAuth 元数据" bind:value={mcpOAuthAccessToken} /></label>
        {:else}
          <label><span>启动命令</span><input placeholder="例如：npx" bind:value={mcpCommand} /></label>
        {/if}
        <label><span>启动参数（每行一个）</span><textarea rows="3" bind:value={mcpArgs}></textarea></label>
        <label><span>认证环境变量（每行一个 KEY=VALUE）</span><textarea rows="3" bind:value={mcpEnv}></textarea></label>
        {#if mcpTransport === "stdio"}<label><span>协议认证令牌（可选）</span><input type="password" autocomplete="off" placeholder="服务端需支持 gossh/authenticate" bind:value={mcpAuthToken} /></label>{/if}
        <label><span>工具白名单（每行一个完整工具名）</span><textarea rows="3" placeholder="例如：mcp_echo" bind:value={mcpAllowedTools}></textarea></label>
      </div>
      <button class="secondary-action" onclick={connectMCP} disabled={mcpBusy}><Server size={15} />连接并保存</button>
      <div class="mcp-list">
        {#each mcpServers as server (server.id)}
          <div class="mcp-row">
            <div><strong>{server.id}</strong><span>{server.transport === "http" ? server.endpoint : `${server.command} ${server.args?.join(" ") || ""}`}{server.hasAuthToken || server.hasOAuthToken ? " · 已配置认证" : ""}</span></div>
            <div class="mcp-actions">
              <span class:connected={server.connected} class="mcp-status">{server.connected ? "已连接" : "未连接"}</span>
              {#if server.connected}<button class="icon-button" title="断开 MCP" aria-label={`断开 ${server.id}`} onclick={() => disconnectMCP(server.id)}><Unplug size={15} /></button>{/if}
              <button class="icon-button danger" title="删除 MCP 配置" aria-label={`删除 ${server.id}`} onclick={() => deleteMCP(server.id)}><Trash2 size={15} /></button>
            </div>
          </div>
        {:else}<p class="empty-state">暂无 MCP 服务</p>{/each}
      </div>
    </section>

    <section class="skills-section" aria-label="Agent Skill">
      <h3><Workflow size={16} /> Agent Skill</h3>
      <p class="field-hint">Skill 是可复用的 Agent 模板，只能调用列出的工具；变更工具仍受逐步审批和安全策略约束。</p>
      <div class="skill-toolbar">
        <input aria-label="搜索 Skill" placeholder="搜索名称、分类或标签" bind:value={skillQuery} onkeydown={(event) => event.key === "Enter" && loadSkills()} />
        <button class="icon-button" title="搜索 Skill" aria-label="搜索 Skill" onclick={loadSkills}><Search size={15} /></button>
        <button class="secondary-action" onclick={importSkill}><Upload size={14} />导入文件</button>
      </div>
      <div class="trusted-key-row">
        <input placeholder="签名者，例如 ops-team" bind:value={trustedSigner} />
        <input placeholder="Ed25519 公钥 Base64" bind:value={trustedPublicKey} />
        <button class="secondary-action" onclick={saveTrustedKey}><ShieldCheck size={14} />保存可信公钥</button>
        <button class="secondary-action" onclick={revokeTrustedKey}>撤销</button>
      </div>
      <div class="trusted-key-row signing-row">
        <input type="password" autocomplete="off" placeholder="Ed25519 私钥 Base64，仅用于本次签名" bind:value={signingPrivateKey} />
        <button class="secondary-action" onclick={generateSigningKey}>生成密钥</button>
        <button class="secondary-action" onclick={signEditedSkill}>签名编辑器内容</button>
      </div>
      {#if skillCheckResult}<p class="field-hint skill-check-result">{skillCheckResult}</p>{/if}
      <div class="skill-list">
        {#each skills as skill (skill.id)}
          <div class="skill-row">
            <div class="skill-info">
              <strong>{skill.name}</strong>
              <span>{skill.id} · Markdown Skill</span>
              {#if skill.description}<p>{skill.description}</p>{/if}
            </div>
            <div class="skill-actions">
              <span class="skill-source">{skill.builtin ? "内置" : "用户"}</span>
              <span class="skill-source">{skill.trustStatus || "unverified"}</span>
              <button class="icon-button" title="检查依赖" aria-label={`检查 ${skill.name} 依赖`} onclick={() => checkSkill(skill)}><ShieldCheck size={15} /></button>
              <button class="icon-button" title="查看版本历史" aria-label={`查看 ${skill.name} 历史`} onclick={() => loadSkillHistory(skill)}><History size={15} /></button>
              <button class="icon-button" title="导出 Skill" aria-label={`导出 ${skill.name}`} onclick={() => exportSkill(skill)}><Download size={15} /></button>
              <button class="icon-button" title="编辑 Skill Markdown" aria-label={`编辑 ${skill.name}`} onclick={() => editSkill(skill)}><Code2 size={15} /></button>
              {#if !skill.builtin}<button class="icon-button" title={skill.enabled ? "禁用 Skill" : "启用 Skill"} aria-label={`${skill.enabled ? "禁用" : "启用"} ${skill.name}`} onclick={() => toggleSkill(skill)} disabled={skillBusy}><Check size={15} /></button>{/if}
              {#if !skill.builtin}<button class="icon-button danger" title="删除 Skill" aria-label={`删除 ${skill.name}`} onclick={() => deleteSkill(skill)} disabled={skillBusy}><Trash2 size={15} /></button>{/if}
            </div>
          </div>
        {:else}<p class="empty-state">暂无 Skill</p>{/each}
      </div>
      {#if skillHistoryID}
        <div class="skill-history">
          <div class="skill-editor-head"><span>{skillHistoryID} 版本历史</span><button class="icon-button" title="关闭历史" aria-label="关闭版本历史" onclick={() => skillHistoryID = ""}><X size={15} /></button></div>
          {#each skillHistory as version (version.version + version.source)}
            <div class="skill-history-row"><span>v{version.version}</span><button class="secondary-action" onclick={() => restoreSkill(version)}>恢复</button></div>
          {:else}<p class="empty-state">暂无历史版本</p>{/each}
        </div>
      {/if}
      <div class="skill-editor-head">
        <span>SKILL.md 编辑器</span>
        <button class="secondary-action" onclick={newSkill}>新建</button>
      </div>
      <textarea class="skill-editor" rows="14" bind:value={skillEditor} placeholder="粘贴标准 SKILL.md 内容..."></textarea>
      <button class="primary-action" onclick={saveSkill} disabled={skillBusy || !skillEditor.trim()}><Check size={16} />保存 Skill</button>
    </section>
  </div>
</section>

{#if toast}<div class="toast" role="status">{toast}</div>{/if}

<style>
  .ai-settings { height: 100%; overflow: auto; padding: 28px; color: var(--app-text); background: var(--app-panel); }
  .page-header { display: flex; align-items: center; gap: 12px; padding-bottom: 22px; border-bottom: 1px solid var(--app-border); }
  .header-icon { display: grid; place-items: center; width: 40px; height: 40px; border-radius: 8px; color: #f0abfc; background: rgba(217,70,239,0.14); }
  h2, h3, p { margin: 0; }
  h2 { font-size: 19px; font-weight: 650; }
  .page-header p { margin-top: 4px; color: var(--app-muted); font-size: 13px; }
  .content-grid { display: grid; gap: 24px; max-width: 1080px; padding-top: 24px; }
  .config-section, .knowledge-section, .mcp-section { padding: 20px; border: 1px solid var(--app-border); border-radius: 8px; background: var(--app-panel-muted); }
  h3 { display: flex; align-items: center; gap: 8px; color: var(--app-text); font-size: 14px; font-weight: 600; }
  .field-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; margin-top: 18px; }
  label { display: grid; gap: 7px; min-width: 0; }
  label span { color: var(--app-muted); font-size: 12px; }
  input, select, textarea { box-sizing: border-box; width: 100%; border: 1px solid var(--app-border-strong); border-radius: 6px; padding: 10px 11px; background: var(--app-panel-strong); color: var(--app-text); font: inherit; font-size: 13px; outline: none; }
  input:focus, select:focus, textarea:focus { border-color: #818cf8; }
  .field-hint { margin-top: 12px; color: var(--app-muted); font-size: 11px; line-height: 1.5; }
  .switch-row { display: flex; align-items: center; justify-content: space-between; gap: 18px; margin-top: 18px; padding-top: 16px; border-top: 1px solid var(--app-border); }
  .switch-row strong { font-size: 13px; font-weight: 500; }
  .switch-row p { margin-top: 3px; color: var(--app-muted); font-size: 12px; }
  .rag-toggle { position: relative; display: block; flex: 0 0 auto; width: 42px; height: 24px; padding: 0; border: 0; border-radius: 999px; appearance: none; background: var(--app-subtle); cursor: pointer; }
  .rag-toggle span { position: absolute; display: block; top: 3px; left: 3px; width: 18px; height: 18px; margin: 0; border-radius: 50%; background: #f8fafc; transition: transform .18s ease; }
  .rag-toggle[aria-checked="true"] { background: #6366f1; }
  .rag-toggle[aria-checked="true"] span { transform: translateX(18px); }
  .primary-action, .secondary-action, .icon-button { border: 0; cursor: pointer; font: inherit; }
  .primary-action, .secondary-action { display: inline-flex; align-items: center; justify-content: center; gap: 7px; margin-top: 18px; min-height: 36px; padding: 8px 14px; border-radius: 6px; font-size: 13px; font-weight: 600; }
  .primary-action { background: #6366f1; color: white; }
  .primary-action:hover { background: #4f46e5; }
  .secondary-action { margin-top: 10px; background: var(--app-accent-soft); color: var(--app-accent); }
  button:disabled { opacity: .55; cursor: not-allowed; }
  .knowledge-grid { display: grid; grid-template-columns: minmax(250px, .85fr) minmax(300px, 1.15fr); gap: 20px; margin-top: 18px; }
  .document-editor { display: flex; flex-direction: column; }
  textarea { min-height: 150px; margin-top: 10px; resize: vertical; line-height: 1.5; }
  .search-row { display: flex; gap: 8px; }
  .icon-button { display: grid; place-items: center; flex: 0 0 36px; width: 36px; height: 36px; border-radius: 6px; background: var(--app-hover); color: var(--app-text); }
  .icon-button:hover { background: var(--app-accent-soft); }
  .danger:hover { background: rgba(239,68,68,0.18); color: #fca5a5; }
  .search-results, .stored-documents { display: flex; flex-direction: column; gap: 8px; margin-top: 12px; }
  .search-results article, .document-row { border: 1px solid var(--app-border); border-radius: 6px; background: var(--app-panel-strong); }
  .search-results article { padding: 10px 11px; }
  .search-results strong { font-size: 12px; }
  .search-results p { margin-top: 5px; color: var(--app-muted); font-size: 12px; line-height: 1.45; }
  .document-row { display: flex; align-items: center; justify-content: space-between; gap: 10px; min-height: 48px; padding: 6px 7px 6px 11px; }
  .document-row span { overflow: hidden; color: var(--app-text); font-size: 13px; text-overflow: ellipsis; white-space: nowrap; }
  .document-row .icon-button { flex-basis: 30px; width: 30px; height: 30px; }
  .empty-state { padding: 16px 0; color: var(--app-muted); font-size: 13px; text-align: center; }
  .mcp-list { display: flex; flex-direction: column; gap: 8px; margin-top: 16px; }
  .mcp-row { display: flex; align-items: center; justify-content: space-between; gap: 14px; padding: 10px 11px; border: 1px solid var(--app-border); border-radius: 6px; background: var(--app-panel-strong); }
  .mcp-row > div:first-child { min-width: 0; display: grid; gap: 4px; }
  .mcp-row strong { font-size: 12px; }
  .mcp-row span { overflow: hidden; color: var(--app-muted); font: 11px monospace; text-overflow: ellipsis; white-space: nowrap; }
  .mcp-actions { display: flex; align-items: center; gap: 6px; flex: 0 0 auto; }
  .mcp-status { color: var(--app-muted); font-size: 11px !important; font-family: inherit !important; }
  .mcp-status.connected { color: #86efac; }
  .skill-list { display: flex; flex-direction: column; gap: 8px; margin-top: 16px; }
  .skill-toolbar { display: flex; align-items: center; gap: 8px; margin-top: 14px; }
  .skill-toolbar input { min-width: 0; flex: 1; }
  .skill-toolbar .secondary-action { margin-top: 0; }
  .trusted-key-row { display: flex; gap: 8px; margin-top: 8px; }
  .trusted-key-row input { min-width: 0; flex: 1; }
  .trusted-key-row .secondary-action { margin-top: 0; flex: 0 0 auto; }
  .skill-check-result { padding: 8px 10px; border: 1px solid var(--app-border); border-radius: 6px; background: var(--app-panel-strong); }
  .skill-row { display: flex; align-items: center; justify-content: space-between; gap: 14px; padding: 10px 11px; border: 1px solid var(--app-border); border-radius: 6px; background: var(--app-panel-strong); }
  .skill-info { min-width: 0; display: grid; gap: 4px; }
  .skill-info strong { font-size: 12px; }
  .skill-info span, .skill-info p { overflow: hidden; color: var(--app-muted); font-size: 11px; text-overflow: ellipsis; white-space: nowrap; }
  .skill-info span { font-family: monospace; }
  .skill-actions { display: flex; align-items: center; gap: 6px; flex: 0 0 auto; }
  .skill-source { color: var(--app-muted); font-size: 10px; }
  .skill-editor-head { display: flex; align-items: center; justify-content: space-between; margin-top: 18px; color: var(--app-muted); font-size: 12px; }
  .skill-editor-head .secondary-action { margin-top: 0; }
  .skill-editor { min-height: 220px; margin-top: 8px; font: 11px/1.5 monospace; resize: vertical; }
  .skill-history { margin-top: 14px; padding-top: 12px; border-top: 1px solid var(--app-border); }
  .skill-history-row { display: flex; align-items: center; justify-content: space-between; gap: 10px; min-height: 34px; border-bottom: 1px solid var(--app-border); color: var(--app-muted); font: 11px monospace; }
  .skill-history-row .secondary-action { margin-top: 0; min-height: 28px; padding: 5px 9px; }
  .toast { position: fixed; right: 28px; bottom: 30px; z-index: 10; padding: 10px 14px; border-radius: 6px; background: #16a34a; color: white; box-shadow: 0 8px 24px rgba(0,0,0,.28); font-size: 13px; }
  @media (max-width: 760px) { .ai-settings { padding: 18px; } .field-grid, .knowledge-grid { grid-template-columns: 1fr; } }
</style>
