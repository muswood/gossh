<!-- owner: muswood | Email: mumu920@outlook.com -->
<script lang="ts">
  import { activeTabId, activeAIConversationId, aiConversationIdForTab, aiConversations, aiMessages, aiLoading, tabs, terminalCommand, listAIConversations, createAIConversation, selectAIConversation, remoteTargetKey } from "$lib/stores";
  import type { AIConversation } from "$lib/stores";
  import type { AIMessage, Tab } from "$lib/stores";
  import { extractExecutableCommands, isBlockedTerminalCommand, isDeleteCommand, prepareAssistantMessage } from "$lib/aiCommandSafety";
  import { summarizeConversationForAgent } from "$lib/aiConversations";
  import { sanitizeTerminalOutput } from "$lib/terminalPrivacy";
  import { Sparkles, Send, Copy, Terminal, Zap, Bug, FileSearch, FolderOpen, Play, Folder, FileText, ChevronRight, RefreshCw, X, Square, ShieldCheck, Trash2, Plus, List } from "lucide-svelte";
  import { marked } from "marked";
  import DOMPurify from "dompurify";
  import { AgentApprove, AgentGetTask, AgentListTasks, AgentReject, AgentResume, AgentStart, AgentStop, AssessAgentCommand, SkillList, SFTPListDir } from "../../../wailsjs/go/main/App";
  import { EventsOn } from "../../../wailsjs/runtime/runtime";
  import { onDestroy, onMount, tick } from "svelte";

  interface RemoteEntry { name: string; path?: string; size: number; isDir: boolean; perm?: string; modTime?: string; }
  interface AgentApproval { taskId: string; stepId: string; toolName: string; command: string; purpose: string; risk: string; approvalLevel?: number; }
  interface AgentEvent { id?: string; taskId: string; stepId?: string; type: string; payload?: any; timestamp: string; }
  interface AgentTimelineItem extends AgentEvent { title: string; detail: string; }
  interface AgentToolView {
    name: string;
    status: string;
    statusClass: "ok" | "error" | "running" | "info";
    summary: string;
    command: string;
    purpose: string;
    targetId: string;
    output: string;
    error: string;
    raw: string;
  }
  interface AgentTask { id: string; tabId?: string; goal: string; status: string; result?: string; report?: any; error?: string; persistenceState?: string; persistenceError?: string; persistenceFailures?: number; pendingApproval?: AgentApproval; events?: AgentEvent[]; updatedAt?: string; }
  interface SkillParameter { type: string; description?: string; required?: boolean; default?: any; enum?: string[]; }
  interface SkillManifest { id: string; name: string; version: string; description?: string; mode?: string; allowedTools: string[]; maxSteps?: number; timeoutSeconds?: number; prompt: string; parameters?: Record<string, SkillParameter>; enabled?: boolean; builtin?: boolean; }

  let { tabId = "", targetTab, terminalTabs = [] } = $props<{ tabId?: string; targetTab?: Tab & { host?: string; port?: number; username?: string }; terminalTabs?: Tab[] }>();

  let input = $state("");
  let filePath = $state("");
  let showFileAnalyzer = $state(false);
  let remoteBrowserOpen = $state(false);
  let remotePath = $state("/");
  let remoteFiles = $state<RemoteEntry[]>([]);
  let remoteLoading = $state(false);
  let remoteError = $state("");
  let analyzingFile = $state(false);
  let activeAgentTaskId = $state("");
  let resumableAgentTaskId = $state("");
  let pendingApproval = $state<AgentApproval | null>(null);
  let agentTimeline = $state<AgentTimelineItem[]>([]);
  let agentTasks = $state<AgentTask[]>([]);
  let showAgentTasks = $state(false);
  let multiTarget = $state(false);
  let selectedTargetId = $state("");
  let selectedTargetIds = $state<string[]>([]);
  let textareaEl: HTMLTextAreaElement;
  let errorMsg = $state("");
  let availableSkills = $state<SkillManifest[]>([]);
  let selectedSkillId = $state("");
  let skillParameters = $state<Record<string, any>>({});
  let skillDryRun = $state(false);
  let targetParametersJSON = $state("{}");
  let agentTaskPoller: ReturnType<typeof setInterval> | undefined;
  let conversationTargetKey = $state<string | undefined>(undefined);
  let conversationChoices = $state<AIConversation[]>([]);
  let showConversationPicker = $state(false);
  let lastConversationTargetKey = $state<string | undefined>(undefined);
  let aiBody: HTMLDivElement;
  let availableTerminalTabs = $derived(terminalTabs.filter((tab) =>
    ["ssh", "telnet", "raw"].includes(tab.type) && tab.connected && Boolean(tab.sessionId)
  ));
  let sshTerminalTabs = $derived(availableTerminalTabs.filter((tab) => tab.type === "ssh"));

  function refreshConversationChoices() {
    const targetKey = remoteTargetKey(targetTab);
    if (!targetKey) {
      conversationTargetKey = undefined;
      conversationChoices = [];
      showConversationPicker = false;
      activeAIConversationId.set(aiConversationIdForTab(targetTab));
      return;
    }
    if (targetKey === lastConversationTargetKey && conversationChoices.length > 0) return;
    lastConversationTargetKey = targetKey;
    conversationTargetKey = targetKey;
	conversationChoices = listAIConversations(conversationTargetKey);
    if (conversationChoices.length === 0) {
      createAIConversation(conversationTargetKey);
	  conversationChoices = listAIConversations(conversationTargetKey);
    } else if (!conversationChoices.some(item => item.id === $activeAIConversationId)) {
      selectAIConversation(conversationChoices[0].id);
    }
    showConversationPicker = conversationChoices.length > 1;
  }

  $effect(() => {
    const key = remoteTargetKey(targetTab);
    void key;
    refreshConversationChoices();
  });

  function chooseConversation(id: string) {
    if ($aiLoading || !selectAIConversation(id)) return;
    showConversationPicker = false;
  }

  function newConversation() {
    if ($aiLoading) return;
    createAIConversation(conversationTargetKey);
    conversationChoices = listAIConversations(conversationTargetKey);
    showConversationPicker = false;
    agentTimeline = [];
    errorMsg = "";
  }

  function conversationTime(timestamp: number) {
    return new Date(timestamp).toLocaleString([], { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" });
  }

  function currentConversationTitle() {
    return $aiConversations.find(item => item.id === $activeAIConversationId)?.title || "新会话";
  }

  $effect(() => {
    const revision = $aiConversations.map(item => `${item.id}:${item.updatedAt}`).join(",");
    void revision;
	if (remoteTargetKey(targetTab)) conversationChoices = listAIConversations(conversationTargetKey);
  });

  $effect(() => {
    const ids = new Set(availableTerminalTabs.map((tab) => tab.id));
    if (availableTerminalTabs.length === 1) selectedTargetId = availableTerminalTabs[0].id;
    else if (!ids.has(selectedTargetId)) selectedTargetId = "";
  });

  $effect(() => {
    const messageCount = $aiMessages.length;
    const timelineCount = agentTimeline.length;
    const loading = $aiLoading;
    const error = errorMsg;
    void messageCount;
    void timelineCount;
    void loading;
    void error;
    void tick().then(() => {
      if (aiBody) aiBody.scrollTop = aiBody.scrollHeight;
    });
  });

  function appendMessage(message: AIMessage) {
    aiMessages.update((messages) => [...messages, message].slice(-100));
  }

  function insertMessageByTime(message: AIMessage) {
    aiMessages.update((messages) => {
      const next = [...messages, message].sort((a, b) => a.timestamp - b.timestamp);
      return next.slice(-100);
    });
  }

  function renderMarkdown(content: string) {
    const html = marked.parse(content, { async: false, breaks: true, gfm: true });
    return DOMPurify.sanitize(String(html));
  }

  async function clearHistory() {
    aiMessages.set([]);
  }

  function closeAssistant() {
    const tab = $tabs.find((item) => item.id === tabId);
    if (tab?.type === "ai") {
      const remaining = $tabs.filter((item) => item.id !== tabId);
      tabs.set(remaining);
      activeTabId.set(remaining[0]?.id || "welcome");
      return;
    }
    tabs.update((items) => items.map((item) => item.id === tabId ? { ...item, showAI: false } : item));
  }

  const quickPrompts = [
    "解释上面这条命令的含义",
    "查找占用 8080 端口的进程",
    "如何查看 Linux 系统 CPU 和内存使用情况？",
    "批量重命名当前目录下所有 .jpg 文件",
  ];

  async function handleSend() {
    const content = input.trim();
    if (!content || $aiLoading) return;

    const userMsg: AIMessage = { role: "user", content, timestamp: Date.now() };
    appendMessage(userMsg);
    input = "";
    errorMsg = "";
    aiLoading.set(true);

    try {
      const target = activeTerminalTab();
      await startAgent(content, "chat", [
        "用户正在使用 GoSSH 运维助手。",
        "如果用户要求查看当前终端、命令输出或错误，请使用关联终端工具收集证据；不要依赖前端屏幕快照。",
        target?.sessionId ? `当前终端会话: ${target.sessionId}` : "当前没有关联的 SSH 终端会话。",
      ].join("\n"), target?.sessionId || "");
    } catch (e: any) {
      errorMsg = e?.toString() || "AI 请求失败";
      aiLoading.set(false);
    } finally {
      if (!activeAgentTaskId) aiLoading.set(false);
    }
  }

  async function runSpecialAction(action: "explain" | "generate" | "diagnose") {
    const content = input.trim();
    if (!content || $aiLoading) return;
    const labels = { explain: "解释命令", generate: "生成命令", diagnose: "诊断错误" };
    appendMessage({ role: "user", content: `${labels[action]}：${content}`, timestamp: Date.now() });
    input = "";
    errorMsg = "";
    aiLoading.set(true);
    try {
      const mode = action === "diagnose" ? "diagnose" : "general";
      const goal = action === "explain"
        ? `解释以下命令或运维操作，并说明风险与更安全的检查方式：\n${content}`
        : action === "generate"
          ? `根据以下需求生成低风险、默认只读的运维方案或命令：\n${content}`
          : `诊断以下错误或输出，给出问题、原因、证据需求和分级排查步骤：\n${content}`;
      await startAgent(goal, mode, "优先使用 gossh_diagnostics、rag_search 和其他只读工具补充证据；危险操作必须明确要求审批。", activeTerminalTab()?.sessionId || "");
    } catch (e: any) {
      errorMsg = e?.toString?.() || "AI 请求失败";
      aiLoading.set(false);
    } finally {
      if (!activeAgentTaskId) aiLoading.set(false);
    }
  }

  async function startAgent(goal: string, mode: string, context: string, sessionId = "") {
    agentTimeline = [];
    pendingApproval = null;
    activeAgentTaskId = "";
    resumableAgentTaskId = "";
    const target = activeTerminalTab();
    if (availableTerminalTabs.length > 1 && !target && !multiTarget) {
      throw new Error("当前标签页有多个终端，请先选择主机");
    }
    sessionId = target?.sessionId || sessionId;
    const connectedTargets = availableTerminalTabs.filter((tab) => tab.type === "ssh");
    const targets = multiTarget
      ? connectedTargets.filter((tab) => selectedTargetIds.includes(tab.id)).map((tab) => ({
        id: tab.id,
        sessionId: tab.sessionId,
        name: tab.name,
      }))
      : [];
    if (multiTarget && targets.length === 0) {
      throw new Error("请至少选择一个多目标 SSH 会话");
    }
    let targetParameters: Record<string, Record<string, any>> = {};
    if (multiTarget && targetParametersJSON.trim()) {
      try {
        targetParameters = JSON.parse(targetParametersJSON) as Record<string, Record<string, any>>;
      } catch {
        throw new Error("多目标参数必须是 JSON 对象");
      }
    }
    const taskId = await AgentStart({
      sessionId,
	  transport: target?.type,
      tabId,
      targets,
      goal,
      mode,
      context,
      history: summarizeConversationForAgent($aiMessages) as any,
      autonomous: true,
      skillId: selectedSkillId || undefined,
      skillParameters,
      dryRun: skillDryRun,
      targetParameters,
    } as any);
    activeAgentTaskId = taskId;
    await refreshAgentTasks();
  }

  async function loadSkills() {
    try {
      availableSkills = JSON.parse(await SkillList()) as SkillManifest[];
    } catch (error) {
      errorMsg = `加载 Skill 失败: ${error}`;
    }
  }

  function selectSkill(id: string) {
    selectedSkillId = id;
    const skill = availableSkills.find((item) => item.id === id);
    const next: Record<string, any> = {};
    for (const [name, parameter] of Object.entries(skill?.parameters || {})) {
      next[name] = parameter.default ?? (parameter.type === "boolean" ? false : "");
    }
    skillParameters = next;
  }

  function selectedSkill() {
    return availableSkills.find((item) => item.id === selectedSkillId);
  }

  function setSkillParameter(name: string, parameter: SkillParameter, value: any) {
    if (parameter.type === "integer") value = value === "" ? "" : Number.parseInt(value, 10);
    if (parameter.type === "number") value = value === "" ? "" : Number(value);
    skillParameters = {...skillParameters, [name]: value};
  }

  function toggleMultiTarget() {
    multiTarget = !multiTarget;
    if (multiTarget) {
      selectedTargetIds = selectedTargetId ? [selectedTargetId] : [];
    } else {
      selectedTargetIds = [];
    }
  }

  async function runAutonomousAnalysis() {
    const content = input.trim();
    if (!content || $aiLoading) return;
    const target = activeTerminalTab();
    if (!target?.sessionId || target.type !== "ssh") {
      errorMsg = "没有可用的已连接终端，无法启动自主分析";
      return;
    }
    if (!window.confirm([
      "确认授权 AI 启动自主分析？",
      "",
      "AI 只能提出分析思路和需要执行的命令。",
      "所有命令在插入或执行终端前都必须由你再次审批确认。",
    ].join("\n"))) return;

    appendMessage({ role: "user", content: `授权自主分析：${content}`, timestamp: Date.now() });
    input = "";
    errorMsg = "";
    aiLoading.set(true);
    try {
      await startAgent(content, "autonomous_analysis", [
        "用户已授权启动一次自主分析。",
        "所有终端命令仍必须逐条向用户申请审批。",
        `目标终端: ${target.name || target.id}`,
      ].join("\n"), target.sessionId);
    } catch (e: any) {
      errorMsg = e?.toString?.() || "自主分析请求失败";
      aiLoading.set(false);
      activeAgentTaskId = "";
      pendingApproval = null;
    }
  }

  async function approveAgentCommand() {
    if (!pendingApproval) return;
    const approval = pendingApproval;
    pendingApproval = null;
    try {
      const task = await AgentGetTask(approval.taskId) as AgentTask;
      if (task.status === "interrupted") {
        await resumeAgentTask(task);
        return;
      }
      await AgentApprove(approval.taskId, approval.stepId);
    } catch (e: any) {
      errorMsg = e?.toString?.() || "审批失败";
    }
  }

  async function rejectAgentCommand() {
    if (!pendingApproval) return;
    const approval = pendingApproval;
    pendingApproval = null;
    try {
      await AgentReject(approval.taskId, approval.stepId);
    } catch (e: any) {
      errorMsg = e?.toString?.() || "拒绝失败";
    }
  }

  function handleAgentEvent(event: AgentEvent) {
    if (!event?.taskId) return;
    // The assistant component is recreated when the user changes tabs. During
    // that window the task id is not in component memory, so recover the task
    // from the durable checkpoint before handling the event.
    if (!activeAgentTaskId) {
      void bindAgentTaskForEvent(event);
      return;
    }
    if (event.taskId !== activeAgentTaskId) return;
    appendTimelineEvent(event);
    if (event.type === "approval-required") {
      pendingApproval = event.payload as AgentApproval;
      aiLoading.set(true);
      return;
    }
    if (event.type === "tool-started") {
      return;
    }
    if (event.type === "tool-finished") {
      return;
    }
    if (event.type === "persistence-error") {
      errorMsg = `Checkpoint 持久化降级：${eventPayloadText(event.payload)}`;
      void refreshAgentTasks();
      return;
    }
    if (event.type === "final") {
      finishAgentTaskWithReport(eventPayloadText(event.payload, 12000), event.timestamp);
      return;
    }
    if (event.type === "error") {
      finishAgentTaskWithError(eventPayloadText(event.payload));
      return;
    }
    if (event.type === "cancelled") {
      finishAgentTaskWithError("自主分析已取消。", "自主分析已取消。");
    }
  }

  function appendTimelineEvent(event: AgentEvent) {
    const duplicate = agentTimeline.some((item) =>
      (event.id && item.id === event.id) ||
      (!event.id && item.taskId === event.taskId && item.stepId === event.stepId && item.type === event.type && item.timestamp === event.timestamp)
    );
    if (duplicate) return;
    agentTimeline = [...agentTimeline, {
      ...event,
      title: eventTitle(event.type),
      detail: eventPayloadText(event.payload, event.type === "final" ? 12000 : 4000),
    }].sort(compareTimelineEvents).slice(-80);
  }

  function compareTimelineEvents(a: AgentEvent, b: AgentEvent) {
    const time = (Date.parse(a.timestamp || "") || 0) - (Date.parse(b.timestamp || "") || 0);
    if (time !== 0) return time;
    return String(a.id || "").localeCompare(String(b.id || ""));
  }

  function finishAgentTaskWithReport(detail: string, completedAt = new Date().toISOString()) {
    const content = cleanOperationalResponse(detail);
    const timestamp = Date.parse(completedAt) || Date.now();
    const message = prepareAssistantMessage(content, timestamp);
    if (!$aiMessages.some((item) => item.role === "assistant" && item.content === message.content)) {
      insertMessageByTime(message);
    }
    aiLoading.set(false);
    activeAgentTaskId = "";
    pendingApproval = null;
    errorMsg = "";
    void refreshAgentTasks();
  }

  function finishAgentTaskWithError(error: string, assistantContent = `AI 分析失败：${error || "未知错误"}`) {
    const message = prepareAssistantMessage(assistantContent);
    if (!$aiMessages.some((item) => item.role === "assistant" && item.content === message.content)) {
      appendMessage(message);
    }
    errorMsg = error || "AI 分析失败";
    aiLoading.set(false);
    activeAgentTaskId = "";
    pendingApproval = null;
    void refreshAgentTasks();
  }

  function eventTitle(type: string) {
    const titles: Record<string, string> = {
      "task-created": "任务创建",
      planning: "Agent 规划",
      "plan-created": "计划生成",
      "approval-required": "等待审批",
      "approval-result": "审批结果",
      "tool-started": "工具开始",
      "tool-output": "工具输出",
      "tool-finished": "工具完成",
      replan: "重新规划",
      assistant: "Agent 分析",
	      "model-diagnostics": "模型诊断",
      final: "最终回复",
      error: "任务失败",
      cancelled: "任务取消",
      interrupted: "任务中断，可恢复",
      "persistence-error": "Checkpoint 持久化失败",
    };
    return titles[type] || type;
  }

  function eventPayloadText(payload: any, limit = 4000) {
    if (payload === undefined || payload === null) return "";
    if (typeof payload === "string") return sanitizeTerminalOutput(payload).slice(-limit);
    try {
      return sanitizeTerminalOutput(JSON.stringify(payload, null, 2)).slice(-limit);
    } catch {
      return String(payload);
    }
  }

  function parseAgentPayload(payload: any): any {
    if (typeof payload !== "string") return payload;
    try {
      return JSON.parse(payload);
    } catch {
      return payload;
    }
  }

  function toolEventView(event: AgentEvent): AgentToolView {
    const payload = parseAgentPayload(event.payload);
    const data = payload && typeof payload === "object" ? payload : {};
    const args = data.arguments && typeof data.arguments === "object" ? data.arguments : {};
    const rawOutput = typeof data.output === "string" ? data.output : "";
    const error = typeof data.error === "string" ? data.error : "";
    const command = String(data.command || args.command || "");
    const purpose = String(data.purpose || args.purpose || "");
    const targetId = String(data.targetId || args.targetId || "");
    const statusValue = String(data.status || (event.type === "tool-started" ? "running" : "info"));
    const statusLabels: Record<string, string> = {
      ok: "成功",
      error: "失败",
      rejected: "已拦截",
      dry_run: "试运行",
      partial: "部分成功",
      timeout: "超时",
      cancelled: "已取消",
      submitted: "已发送，等待终端确认",
      running: "执行中",
      executing: "执行中",
    };
    const statusClass: AgentToolView["statusClass"] =
      ["ok", "dry_run"].includes(statusValue) ? "ok" :
      ["error", "rejected", "timeout", "cancelled"].includes(statusValue) ? "error" :
      ["running", "executing"].includes(statusValue) ? "running" : "info";
    const details = [
      statusLabels[statusValue] || statusValue,
      data.exitCode !== undefined && data.exitCode !== null ? `退出码 ${data.exitCode}` : "",
      data.durationMillis !== undefined ? `${data.durationMillis} ms` : "",
      targetId ? `目标 ${targetId}` : "",
      Number(data.attempts) > 1 ? `尝试 ${data.attempts} 次` : "",
    ].filter(Boolean).join(" · ");

    let raw = "";
    try {
      raw = sanitizeTerminalOutput(JSON.stringify(payload, null, 2)).slice(-8000);
    } catch {
      raw = sanitizeTerminalOutput(String(payload || "")).slice(-8000);
    }

    return {
      name: String(data.toolName || data.name || "工具"),
      status: statusLabels[statusValue] || statusValue,
      statusClass,
      summary: details,
      command: sanitizeTerminalOutput(command),
      purpose: sanitizeTerminalOutput(purpose),
      targetId: sanitizeTerminalOutput(targetId),
      output: sanitizeTerminalOutput(rawOutput).slice(-8000),
      error: sanitizeTerminalOutput(error),
      raw,
    };
  }

  async function refreshAgentTasks(): Promise<AgentTask[]> {
    try {
      const raw = await AgentListTasks(tabId);
      agentTasks = JSON.parse(raw || "[]") as AgentTask[];
      return agentTasks;
    } catch {
      agentTasks = [];
      return [];
    }
  }

  function isAgentRunning(status: string) {
    return status === "running" || status === "waiting_approval";
  }

  function taskFinalReport(task: AgentTask) {
    return [...(task.events || [])].reverse().find((event) => event.type === "final");
  }

  function storedReportMarkdown(report: any) {
    if (!report || typeof report !== "object") return "";
    const lines = [
      `# ${String(report.title || "Agent 报告")}`,
      "",
      `**严重性：** ${String(report.severity || "info")}`,
      "",
      "## 摘要",
      "",
      String(report.summary || "报告已生成，但没有可展示的摘要。"),
    ];
    const findings = Array.isArray(report.findings) ? report.findings : [];
    if (findings.length > 0) {
      lines.push("", "## 发现");
      for (const finding of findings) {
        lines.push("", `### ${String(finding?.title || "未命名发现")}`, "", String(finding?.description || ""));
      }
    }
    const recommendations = Array.isArray(report.recommendations) ? report.recommendations : [];
    if (recommendations.length > 0) {
      lines.push("", "## 建议", ...recommendations.map((item) => `- ${String(item)}`));
    }
    const limitations = Array.isArray(report.limitations) ? report.limitations : [];
    if (limitations.length > 0) {
      lines.push("", "## 限制", ...limitations.map((item) => `- ${String(item)}`));
    }
    return lines.join("\n");
  }

  async function hydrateAgentTask(source: AgentTask) {
    if (!source?.id) return;
    let task = source;
    try {
      task = await AgentGetTask(source.id) as AgentTask;
    } catch (error) {
      errorMsg = `读取 Agent 任务失败：${error}`;
    }
    if (task.tabId && task.tabId !== tabId) return;

    const previousApproval = activeAgentTaskId === task.id ? pendingApproval : null;
    const eventApproval = [...(task.events || [])].reverse().find((event) => event.type === "approval-required")?.payload;
    const recoveredApproval = task.pendingApproval || (eventApproval && typeof eventApproval === "object" ? eventApproval as AgentApproval : null);
    activeAgentTaskId = isAgentRunning(task.status) ? task.id : "";
    agentTimeline = timelineFromEvents(task.events || []);
    // A polling snapshot can briefly lag the approval event. Never erase a
    // live approval card from that stale intermediate snapshot.
    pendingApproval = task.status === "interrupted" ? null : recoveredApproval || previousApproval;
    resumableAgentTaskId = task.status === "interrupted" ? task.id : "";
    if (task.persistenceState === "degraded") {
      errorMsg = `Checkpoint 持久化降级：${task.persistenceError || "部分事件可能无法恢复"}`;
    } else if (!isAgentRunning(task.status)) {
      errorMsg = task.status === "owned_by_other_process" ? (task.error || "任务由另一应用进程执行") : "";
    }

    if (isAgentRunning(task.status)) {
      aiLoading.set(true);
      return;
    }

    aiLoading.set(false);
    if (task.status === "completed") {
      const finalEvent = taskFinalReport(task);
      if (finalEvent) finishAgentTaskWithReport(eventPayloadText(finalEvent.payload, 12000), finalEvent.timestamp);
      else if (task.report) finishAgentTaskWithReport(storedReportMarkdown(task.report), task.updatedAt);
      else if (task.result?.trim()) finishAgentTaskWithReport(task.result, task.updatedAt);
      return;
    }
    if (task.status === "failed") {
      finishAgentTaskWithError(task.error || "Agent 执行失败");
      return;
    }
    if (task.status === "cancelled") {
      finishAgentTaskWithError(task.error || "自主分析已取消。", "自主分析已取消。");
      return;
    }
    if (task.status === "interrupted") {
      errorMsg = task.error || "Agent 任务已中断，需要恢复后继续";
    }
  }

  async function bindAgentTaskForEvent(event: AgentEvent) {
    try {
      const task = await AgentGetTask(event.taskId) as AgentTask;
      if (task.tabId && task.tabId !== tabId) return;
      if (!activeAgentTaskId) await hydrateAgentTask(task);
    } catch {
      // The event may belong to another tab or to a task already removed.
    }
  }

  async function pollActiveAgentTask() {
    if (!activeAgentTaskId) return;
    try {
      const task = await AgentGetTask(activeAgentTaskId) as AgentTask;
      if (!isAgentRunning(task.status)) await hydrateAgentTask(task);
    } catch {
      // The event stream remains the primary path; polling is only recovery.
    }
  }

  async function resumeAgentTask(task: AgentTask) {
    if (!task?.id || $aiLoading || task.status === "owned_by_other_process") return;
    errorMsg = "";
    aiLoading.set(true);
    activeAgentTaskId = task.id;
    pendingApproval = null;
    agentTimeline = timelineFromEvents(task.events || []);
    try {
      await AgentResume(task.id);
      resumableAgentTaskId = "";
      showAgentTasks = false;
    } catch (e: any) {
      errorMsg = e?.toString?.() || "恢复 Agent 任务失败";
      activeAgentTaskId = "";
      aiLoading.set(false);
    }
  }

  async function resumeActiveAgentTask() {
    if (!resumableAgentTaskId || $aiLoading) return;
    try {
      const task = await AgentGetTask(resumableAgentTaskId) as AgentTask;
      await resumeAgentTask(task);
    } catch (e: any) {
      errorMsg = e?.toString?.() || "读取待恢复 Agent 任务失败";
    }
  }

  async function stopAgentTask(taskId = activeAgentTaskId) {
    if (!taskId) return;
    try {
      await AgentStop(taskId);
      aiLoading.set(false);
      activeAgentTaskId = "";
      pendingApproval = null;
      await refreshAgentTasks();
    } catch (e: any) {
      errorMsg = e?.toString?.() || "停止 Agent 任务失败";
    }
  }

  function timelineFromEvents(events: AgentEvent[]) {
    const timeline: AgentTimelineItem[] = [];
    for (const event of events) {
      if (timeline.some((item) =>
        (event.id && item.id === event.id) ||
        (!event.id && item.taskId === event.taskId && item.stepId === event.stepId && item.type === event.type && item.timestamp === event.timestamp)
      )) continue;
      timeline.push({
        ...event,
        title: eventTitle(event.type),
        detail: eventPayloadText(event.payload, event.type === "final" ? 12000 : 4000),
      });
    }
    return timeline.sort(compareTimelineEvents).slice(-80);
  }

  onMount(() => {
    const off = EventsOn("agent:event", (event: AgentEvent) => handleAgentEvent(event));
    agentTaskPoller = setInterval(() => void pollActiveAgentTask(), 1500);
    void (async () => {
      const tasks = await refreshAgentTasks();
      const task = tasks.find((item) => isAgentRunning(item.status)) || tasks[0];
      if (task) {
        await hydrateAgentTask(task);
      } else {
        // aiLoading is conversation-scoped. Clear a stale value left by a
        // destroyed component when there is no durable task for this tab.
        aiLoading.set(false);
      }
    })();
    void loadSkills();
    return () => {
      off();
      if (agentTaskPoller) clearInterval(agentTaskPoller);
      agentTaskPoller = undefined;
    };
  });

  onDestroy(() => {
    pendingApproval = null;
    if (agentTaskPoller) clearInterval(agentTaskPoller);
    agentTaskPoller = undefined;
  });

  function shellQuote(value: string) {
    return `'${value.replace(/'/g, "'\\''")}'`;
  }

  function extractRemotePath(content: string) {
    const match = content.match(/(?:^|\s)(\/[A-Za-z0-9._~+@%:=,\/-]+)/);
    if (!match) return "";
    return match[1].replace(/[，。；;:、)）\]】>]+$/g, "");
  }

  function buildAutonomousReadOnlyCommand(content: string, terminalContent = "") {
    const text = `${content}\n${terminalContent}`;
    const path = extractRemotePath(content);
    if (/system restart required|需要重启|重启.*required|reboot.*required/i.test(text)) {
      return "cat /var/run/reboot-required /var/run/reboot-required.pkgs";
    }
    const service = content.match(/\b(nginx|apache2?|httpd|mysql|mariadb|postgresql|redis|docker|containerd|ssh|sshd)\b/i)?.[1];
    if (/状态|status|失败|failed|启动|服务|service/i.test(content) && service) {
      return `systemctl status ${service} --no-pager -l`;
    }
    const port = content.match(/(?:端口|port|:)(\d{2,5})\b/i)?.[1];
    if (port) {
      return `ss -ltnp | grep -E '(:${port}\\b|:${port} )'`;
    }
    if (/磁盘|disk|空间|容量|df\b/i.test(content)) {
      return "df -hT";
    }
    if (/cpu|内存|memory|负载|load|进程|process/i.test(content)) {
      return "uptime && free -h && ps -eo pid,ppid,cmd,%mem,%cpu --sort=-%cpu | head -n 15";
    }
    if (/journal|系统日志|启动日志/i.test(content)) {
      return "journalctl -xb -n 200 --no-pager";
    }
    if (!path) return "";
    if (/日志|log|syslog|messages|journal|error/i.test(content + " " + path)) {
      return `tail -n 200 -- ${shellQuote(path)}`;
    }
    if (/查看|看下|看一下|分析|检查|读取|文件/i.test(content)) {
      return `sed -n '1,200p' -- ${shellQuote(path)}`;
    }
    return "";
  }

  function cleanOperationalResponse(content: string) {
    return content.replace(
      /^.*(?:没有能力直接连接|无法直接连接|没有实际连接|无法在您的系统上执行|不能执行您服务器上的命令|没有接入您的终端).*$/gmi,
      "本应用可以在你审批后把只读采集命令发送到当前终端；以下基于已采集到的终端输出分析。",
    );
  }

  function activeSSHSession() {
    const current = activeTerminalTab();
    return current?.type === "ssh" ? current : undefined;
  }

  function normalizeRemoteEntry(entry: any): RemoteEntry {
    const path = String(entry?.path ?? entry?.Path ?? "");
    return {
      name: String(entry?.name ?? entry?.Name ?? ""),
      path: path ? path.replace(/\\/g, "/") : undefined,
      size: Number(entry?.size ?? entry?.Size ?? 0),
      isDir: Boolean(entry?.isDir ?? entry?.is_dir ?? entry?.IsDir),
      perm: String(entry?.perm ?? entry?.Perm ?? ""),
      modTime: String(entry?.modTime ?? entry?.mod_time ?? entry?.ModTime ?? ""),
    };
  }

  function joinRemotePath(base: string, name: string) {
    if (!base || base === "/") return `/${name}`;
    return `${base.replace(/\/+$/, "")}/${name}`;
  }

  function formatSize(bytes: number) {
    if (!bytes) return "0 B";
    const units = ["B", "KB", "MB", "GB"];
    let size = bytes;
    let index = 0;
    while (size >= 1024 && index < units.length - 1) {
      size /= 1024;
      index++;
    }
    return `${size.toFixed(index === 0 ? 0 : 1)} ${units[index]}`;
  }

  async function loadRemoteDir(path = remotePath) {
    const session = activeSSHSession();
    if (!session?.sessionId) {
      remoteError = "没有可用的已连接 SSH 会话";
      return;
    }
    remoteLoading = true;
    remoteError = "";
    try {
      const json = await SFTPListDir(session.sessionId, path || "/");
      remoteFiles = (JSON.parse(json || "[]") as any[])
        .map(normalizeRemoteEntry)
        .sort((a, b) => Number(b.isDir) - Number(a.isDir) || a.name.localeCompare(b.name));
      remotePath = path || "/";
    } catch (e: any) {
      remoteError = e?.message || String(e) || "读取远端目录失败";
    } finally {
      remoteLoading = false;
    }
  }

  async function chooseAnalysisFile() {
    if ($aiLoading || analyzingFile) return;
    remoteBrowserOpen = !remoteBrowserOpen;
    if (remoteBrowserOpen && remoteFiles.length === 0) await loadRemoteDir(remotePath);
  }

  async function goRemoteUp() {
    if (remoteLoading || remotePath === "/") return;
    const parts = remotePath.replace(/\/+$/, "").split("/");
    parts.pop();
    await loadRemoteDir(parts.join("/") || "/");
  }

  async function selectRemoteEntry(entry: RemoteEntry) {
    const path = entry.path || joinRemotePath(remotePath, entry.name);
    if (entry.isDir) {
      await loadRemoteDir(path);
      return;
    }
    filePath = path;
    remoteBrowserOpen = false;
  }

  async function analyzeFile() {
    const path = filePath.trim();
    if (!path || $aiLoading || analyzingFile) return;
    const session = activeSSHSession();
    if (!session?.sessionId) {
      errorMsg = "没有可用的已连接 SSH 会话";
      return;
    }

    appendMessage({ role: "user", content: `分析文件：${path}`, timestamp: Date.now() });
    errorMsg = "";
    analyzingFile = true;
    aiLoading.set(true);

    try {
      await startAgent([
        `分析远端文件 ${path}，判断用途、关键问题、生产风险、安全隐患和只读验证步骤。`,
        "必须先使用 sftp_read_file 获取文件内容，再基于真实内容分析。",
        "sftp_read_file 默认按 200 行分页；如果返回 metadata.hasMore=true，必须使用 metadata.nextStartLine 继续读取后续分片，直到覆盖完成分析所需的时间或行号范围。",
        "文件内容由后端工具脱敏和截断，禁止还原、猜测或输出凭据。",
        "如需提出修改、删除、重启、权限或数据库写入建议，必须明确风险、备份、回滚和审批要求。",
      ].join("\n"), "file_analysis", `远端会话: ${session.name || session.id}\n远端文件路径: ${path}`, session.sessionId);
    } catch (e: any) {
      errorMsg = e?.toString?.() || "文件分析失败";
      aiLoading.set(false);
    } finally {
      analyzingFile = false;
      if (!activeAgentTaskId) aiLoading.set(false);
    }
  }

  function activeTerminalTabId() {
    return activeTerminalTab()?.id;
  }

  function activeTerminalTab() {
    if (availableTerminalTabs.length === 1) return availableTerminalTabs[0];
    return availableTerminalTabs.find((tab) => tab.id === selectedTargetId);
  }

  function closeFileAnalyzer() {
    showFileAnalyzer = false;
    remoteBrowserOpen = false;
    remoteError = "";
  }

  async function sendCommandToTerminal(command: string, execute: boolean, reason = "AI 建议命令") {
    const safeCommand = command.trim();
    if (!safeCommand) return false;
    let decision;
    try {
      decision = await AssessAgentCommand(safeCommand);
    } catch (error) {
      errorMsg = `后端命令策略检查失败：${error}`;
      return false;
    }
    if (!decision?.allowed || isBlockedTerminalCommand(safeCommand)) {
      errorMsg = isDeleteCommand(safeCommand)
        ? "已拦截删除命令，不能插入或执行"
        : "已拦截高风险命令，不能插入或执行";
      return false;
    }
    const targetTabId = activeTerminalTabId();
    if (!targetTabId) {
      errorMsg = "没有可用的已连接终端";
      return false;
    }
    if (!window.confirm([
      `${reason}需要审批确认。`,
      "",
      `操作: ${execute ? "立即执行" : "仅插入终端"}`,
      "命令:",
      safeCommand,
      "",
      execute ? "确认后会发送到当前终端执行。" : "确认后只会插入当前终端，不会自动回车执行。",
    ].join("\n"))) return false;
    terminalCommand.set({ command: safeCommand, execute, targetTabId });
    return true;
  }

  async function approveAndExecuteCommand(command: string) {
    if (isBlockedTerminalCommand(command)) {
      errorMsg = isDeleteCommand(command)
        ? "已拦截删除命令，不能执行"
        : "已拦截高风险命令，不能执行";
      return;
    }
    await sendCommandToTerminal(command, true);
  }

  function copyMessage(content: string) {
    void navigator.clipboard?.writeText(content);
  }

  function insertMessage(content: string) {
    const command = extractExecutableCommands(content)[0];
    if (command) sendCommandToTerminal(command, false);
  }

  function useQuickPrompt(prompt: string) {
    input = prompt;
    handleSend();
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  }

  function handleFilePathKeydown(e: KeyboardEvent) {
    if (e.key === "Enter") {
      e.preventDefault();
      analyzeFile();
    }
  }

  function autoResize(el: HTMLTextAreaElement) {
    el.style.height = "auto";
    el.style.height = Math.min(el.scrollHeight, 120) + "px";
  }
</script>

<div class="ai-root">
  <div class="ai-header">
    <div class="ai-brand">
      <div class="ai-icon">
        <Sparkles class="w-3.5 h-3.5 text-white" />
      </div>
      <div>
        <div class="ai-title">AI 助手</div>
        <div class="ai-sub">智能命令助手</div>
      </div>
      <button class="conversation-toggle" title="选择会话" onclick={() => showConversationPicker = !showConversationPicker} disabled={$aiLoading}>
        <List class="w-3 h-3" />
        <span>{currentConversationTitle()}</span>
      </button>
      <button class="conversation-new" title="新建会话" aria-label="新建会话" onclick={newConversation} disabled={$aiLoading}>
        <Plus class="w-3.5 h-3.5" />
      </button>
      <button class="clear-history" title="查看已保存任务" onclick={() => { showAgentTasks = !showAgentTasks; if (showAgentTasks) void refreshAgentTasks(); }}>任务</button>
      <button class="clear-history" title="清空对话" onclick={() => { clearHistory(); agentTimeline = []; }}>清空</button>
      <button class="assistant-close" title="关闭 AI 助手" aria-label="关闭 AI 助手" onclick={closeAssistant}>
        <X class="w-3.5 h-3.5" />
      </button>
    </div>
  </div>

  <div class="ai-body" bind:this={aiBody}>
    {#if showConversationPicker}
      <section class="conversation-list" aria-label="AI 会话列表">
        <div class="conversation-list-head"><span>已保存会话</span><span>{conversationChoices.length} 个</span></div>
        {#each conversationChoices as conversation (conversation.id)}
          <button type="button" class="conversation-item" class:selected={conversation.id === $activeAIConversationId} onclick={() => chooseConversation(conversation.id)} disabled={$aiLoading}>
            <span class="conversation-item-main">{conversation.title || "新会话"}</span>
            <span class="conversation-item-meta">{conversationTime(conversation.updatedAt)} · {conversation.messages.length} 条消息</span>
          </button>
        {/each}
        <button type="button" class="conversation-create" onclick={newConversation} disabled={$aiLoading}><Plus class="w-3 h-3" />新建会话</button>
      </section>
    {/if}
    {#if showAgentTasks}
      <div class="agent-task-list">
        <div class="agent-task-list-head">
          <span>已保存 Agent 任务</span>
          <button class="copy-msg" title="刷新任务列表" onclick={refreshAgentTasks}><RefreshCw class="w-3 h-3" /></button>
        </div>
        {#if agentTasks.length === 0}
          <div class="agent-task-empty">暂无保存任务</div>
        {:else}
          {#each agentTasks as task (task.id)}
            <button class="agent-task-item" onclick={() => resumeAgentTask(task)} disabled={task.status === "completed" || task.status === "owned_by_other_process"}>
              <span class="agent-task-status" data-status={task.status}></span>
              <span class="agent-task-goal">{task.goal}</span>
              <span class="agent-task-state">{task.persistenceState === "degraded" ? "持久化降级" : task.status}</span>
            </button>
          {/each}
        {/if}
      </div>
    {/if}

    {#if agentTimeline.length > 0}
      <section class="agent-timeline" aria-label="Agent 执行时间线">
        <div class="agent-timeline-head">
          <span>Agent timeline</span>
          <span class="agent-task-state">{activeAgentTaskId || "已完成任务"}</span>
        </div>
        {#each agentTimeline as item (item.timestamp + item.type + item.stepId)}
          <article class="timeline-item timeline-{item.type}"
                   class:timeline-deletion-approval={item.type === "approval-required" && (item.payload?.approvalLevel === 2 || isDeleteCommand(item.payload?.command || ""))}>
            <div class="timeline-marker"></div>
            <div class="timeline-content">
              <div class="timeline-title"><span>{item.title}</span><time>{new Date(item.timestamp).toLocaleTimeString()}</time></div>
              {#if item.type === "approval-required" && item.payload}
                <div class="timeline-meta">目的：{item.payload.purpose || "未说明"} · 风险：{item.payload.risk || "未说明"}</div>
                <pre>{item.payload.command || item.detail}</pre>
              {:else if ["tool-started", "tool-output", "tool-finished"].includes(item.type)}
                {@const tool = toolEventView(item)}
                <div class="tool-result-card">
                  <div class="tool-result-head">
                    <code>{tool.name}</code>
                    <span class="tool-result-status tool-status-{tool.statusClass}">{tool.summary || tool.status}</span>
                  </div>
                  {#if tool.targetId}<div class="timeline-meta">目标：{tool.targetId}</div>{/if}
                  {#if tool.purpose}<div class="timeline-meta">目的：{tool.purpose}</div>{/if}
                  {#if tool.command}<pre class="tool-command">{tool.command}</pre>{/if}
                  {#if tool.error}<div class="tool-result-error">{tool.error}</div>{/if}
                  {#if tool.output}<pre class="tool-result-output">{tool.output}</pre>{/if}
                  {#if item.type === "tool-finished" && tool.raw}
                    <details class="tool-raw-details">
                      <summary>查看原始结果</summary>
                      <pre>{tool.raw}</pre>
                    </details>
                  {/if}
                </div>
              {:else if item.type === "final" && item.detail}
                <div class="markdown-content timeline-report">{@html renderMarkdown(cleanOperationalResponse(item.detail))}</div>
              {:else if item.detail}
                <pre>{item.detail}</pre>
              {/if}
            </div>
          </article>
        {/each}
      </section>
    {/if}

    {#if $aiMessages.length === 0}
      <div class="ai-empty">
        <div class="ai-empty-icon">
          <Sparkles class="w-6 h-6 text-violet-400/60" />
        </div>
        <p class="ai-empty-title">AI 运维助手</p>
        <p class="ai-empty-desc">可以帮你解释命令、生成脚本、诊断问题</p>
        <div class="ai-prompts">
          {#each quickPrompts as prompt}
            <button class="ai-prompt-btn" onclick={() => useQuickPrompt(prompt)}>
              {prompt}
            </button>
          {/each}
        </div>
      </div>
    {/if}

    {#each $aiMessages as msg}
      <div class="ai-msg {msg.role === 'user' ? 'msg-user' : 'msg-bot'}">
        {#if msg.role === 'assistant'}
          <div class="msg-avatar bot">
            <Sparkles class="w-3 h-3 text-white" />
          </div>
        {/if}
        <div class="msg-bubble">
          {#if msg.role === 'assistant'}
            <div class="msg-text markdown-content">{@html renderMarkdown(msg.content)}</div>
          {:else}
            <div class="msg-text">{msg.content}</div>
          {/if}
          {#if msg.role === 'assistant' && msg.commands?.length}
            <div class="command-suggestions">
              {#each msg.commands as command}
                <div class="command-suggestion">
                  <code>{command}</code>
                  <div class="command-suggestion-actions">
                    <button class="copy-msg" title="复制命令" onclick={() => copyMessage(command)}><Copy class="w-3 h-3" /></button>
                    <button class="copy-msg" title="审批后插入终端" onclick={() => sendCommandToTerminal(command, false)}><Terminal class="w-3 h-3" /></button>
                    <button class="copy-msg command-execute" title="审批并执行" onclick={() => approveAndExecuteCommand(command)}><Play class="w-3 h-3" /></button>
                  </div>
                </div>
              {/each}
            </div>
          {/if}
          {#if msg.role === 'assistant'}
            <div class="msg-actions">
              <button class="copy-msg" title="复制回复" onclick={() => copyMessage(msg.content)}><Copy class="w-3 h-3" /></button>
              <button class="copy-msg" title="审批后插入首条命令" onclick={() => insertMessage(msg.content)}><Terminal class="w-3 h-3" /></button>
            </div>
          {/if}
        </div>
        {#if msg.role === 'user'}
          <div class="msg-avatar user">ME</div>
        {/if}
      </div>
    {/each}

    {#if pendingApproval}
      <div class:deletion-approval={pendingApproval.approvalLevel === 2 || isDeleteCommand(pendingApproval.command)} class="agent-approval">
        <div class="agent-approval-head">
          {#if pendingApproval.approvalLevel === 2 || isDeleteCommand(pendingApproval.command)}
            <Trash2 class="w-3.5 h-3.5" />
            <span>{pendingApproval.approvalLevel === 2 ? "删除操作二次确认" : "高风险删除审批"}</span>
          {:else}
            <ShieldCheck class="w-3.5 h-3.5" />
            <span>Agent 命令审批</span>
          {/if}
        </div>
        {#if pendingApproval.approvalLevel === 2}
          <div class="deletion-approval-warning">这是删除操作的第二次确认。请再次核对命令、目标和路径，确认后才会执行。</div>
        {:else if isDeleteCommand(pendingApproval.command)}
          <div class="deletion-approval-warning">该命令包含删除操作。首次批准后还需要一次删除二次确认。</div>
        {/if}
        <div class="agent-approval-meta">
          <span>目的：{pendingApproval.purpose || "未说明"}</span>
          <span>风险：{pendingApproval.risk || "未说明"}</span>
        </div>
        <pre>{pendingApproval.command}</pre>
        <div class="agent-approval-actions">
          <button class="agent-reject" onclick={rejectAgentCommand}>拒绝</button>
          <button class:agent-approve-delete={pendingApproval.approvalLevel === 2 || isDeleteCommand(pendingApproval.command)} class="agent-approve" onclick={approveAgentCommand}>{pendingApproval.approvalLevel === 2 ? "确认删除并执行" : isDeleteCommand(pendingApproval.command) ? "批准删除（下一步）" : "批准执行"}</button>
        </div>
      </div>
    {/if}

    {#if $aiLoading}
      <div class="ai-msg msg-bot">
        <div class="msg-avatar bot">
          <Sparkles class="w-3 h-3 text-white" />
        </div>
        <div class="msg-bubble loading-bubble">
          <span class="dot"></span>
          <span class="dot" style="animation-delay: 0.15s"></span>
          <span class="dot" style="animation-delay: 0.3s"></span>
        </div>
      </div>
    {/if}

    {#if errorMsg}
      <div class="ai-error">
        <span>{errorMsg}</span>
        {#if resumableAgentTaskId}
          <button class="agent-resume" onclick={resumeActiveAgentTask} disabled={$aiLoading}>恢复任务</button>
        {/if}
      </div>
    {/if}
  </div>

  <div class="ai-footer">
    <div class="skill-controls">
      <label class="skill-picker">
        <span>Skill</span>
        <select value={selectedSkillId} onchange={(event) => selectSkill((event.currentTarget as HTMLSelectElement).value)} disabled={$aiLoading}>
          <option value="">普通 Agent</option>
          {#each availableSkills.filter((skill) => skill.enabled !== false) as skill}
            <option value={skill.id}>{skill.name}</option>
          {/each}
        </select>
      </label>
      {#if selectedSkill()}
        {@const skill = selectedSkill()!}
        <div class="skill-meta" title={skill.description || skill.prompt}>{skill.version} · {skill.allowedTools.join(", ")}</div>
        {#each Object.entries(skill.parameters || {}) as [name, parameter]}
          <label class="skill-param">
            <span>{name}{parameter.required ? " *" : ""}</span>
            {#if parameter.enum?.length}
              <select value={skillParameters[name] ?? ""} onchange={(event) => skillParameters = {...skillParameters, [name]: (event.currentTarget as HTMLSelectElement).value}} disabled={$aiLoading}>
                <option value="">请选择</option>
                {#each parameter.enum as item}<option value={item}>{item}</option>{/each}
              </select>
            {:else if parameter.type === "boolean"}
              <input type="checkbox" checked={Boolean(skillParameters[name])} onchange={(event) => skillParameters = {...skillParameters, [name]: (event.currentTarget as HTMLInputElement).checked}} disabled={$aiLoading} />
            {:else}
              <input type={parameter.type === "number" || parameter.type === "integer" ? "number" : "text"} placeholder={parameter.description || name} value={skillParameters[name] ?? ""} onchange={(event) => setSkillParameter(name, parameter, (event.currentTarget as HTMLInputElement).value)} disabled={$aiLoading} />
            {/if}
          </label>
        {/each}
      {/if}
      <label class="skill-param dry-run-control"><span>试运行</span><input type="checkbox" bind:checked={skillDryRun} disabled={$aiLoading} /></label>
      {#if multiTarget && selectedSkill()}
        <label class="skill-param target-params"><span>目标参数 JSON</span><input type="text" bind:value={targetParametersJSON} placeholder={"{\"target-id\":{\"service\":\"nginx\"}}"} disabled={$aiLoading} /></label>
      {/if}
    </div>
    <div class="ai-actions">
      <button disabled={!input.trim() || $aiLoading} onclick={() => runSpecialAction('explain')}><Terminal class="w-3 h-3" />解释</button>
      <button disabled={!input.trim() || $aiLoading} onclick={() => runSpecialAction('generate')}><Zap class="w-3 h-3" />生成命令</button>
      <button disabled={!input.trim() || $aiLoading} onclick={() => runSpecialAction('diagnose')}><Bug class="w-3 h-3" />诊断</button>
      <button disabled={!input.trim() || $aiLoading} onclick={runAutonomousAnalysis}><ShieldCheck class="w-3 h-3" />自主分析</button>
      {#if availableTerminalTabs.length > 1 && !multiTarget}
        <select class="target-select" bind:value={selectedTargetId} disabled={$aiLoading} aria-label="选择当前标签页的终端主机">
          <option value="">选择主机...</option>
          {#each availableTerminalTabs as tab}
            <option value={tab.id}>{tab.name || tab.id}</option>
          {/each}
        </select>
      {/if}
      {#if sshTerminalTabs.length > 1}
        <button class:active={multiTarget} disabled={$aiLoading} title="在当前标签页的多个 SSH 主机上执行相同检查" onclick={toggleMultiTarget}><Terminal class="w-3 h-3" />多目标</button>
      {/if}
      {#if multiTarget}
        <select class="target-select" multiple size="1" bind:value={selectedTargetIds} disabled={$aiLoading} aria-label="选择 Agent SSH 目标">
          {#each sshTerminalTabs as tab}
            <option value={tab.id}>{tab.name || tab.id}</option>
          {/each}
        </select>
      {/if}
      <button class:active={showFileAnalyzer} disabled={$aiLoading} onclick={() => showFileAnalyzer = !showFileAnalyzer}><FileSearch class="w-3 h-3" />分析文件</button>
    </div>
    {#if showFileAnalyzer}
      <div class="file-analyzer">
        <div class="file-analyzer-head">
          <span>分析文件</span>
          <button class="file-close" title="关闭分析文件" onclick={closeFileAnalyzer}>
            <X class="w-3.5 h-3.5" />
          </button>
        </div>
        <div class="file-path-row">
          <input
            bind:value={filePath}
            onkeydown={handleFilePathKeydown}
            placeholder="输入远端文件路径..."
            class="file-path-input"
          />
          <button class="file-pick" title="选择远端文件" disabled={$aiLoading || analyzingFile} onclick={chooseAnalysisFile}>
            <FolderOpen class="w-3.5 h-3.5" />
          </button>
          <button class="file-analyze" disabled={!filePath.trim() || $aiLoading || analyzingFile} onclick={analyzeFile}>
            <FileSearch class="w-3 h-3" />分析
          </button>
        </div>
        {#if remoteBrowserOpen}
          <div class="remote-browser">
            <div class="remote-browser-head">
              <button class="remote-nav-btn" title="上级目录" disabled={remoteLoading || remotePath === '/'} onclick={goRemoteUp}>
                <ChevronRight class="w-3 h-3 rotate-180" />
              </button>
              <span class="remote-current" title={remotePath}>{remotePath}</span>
              <button class="remote-nav-btn" title="刷新" disabled={remoteLoading} onclick={() => loadRemoteDir(remotePath)}>
                <RefreshCw class="w-3 h-3" />
              </button>
            </div>
            <div class="remote-browser-list">
              {#if remoteError}
                <div class="remote-browser-state error">{remoteError}</div>
              {:else if remoteLoading}
                <div class="remote-browser-state">正在读取目录...</div>
              {:else if remoteFiles.length === 0}
                <div class="remote-browser-state">空目录</div>
              {:else}
                {#each remoteFiles as entry}
                  <button type="button" class="remote-entry" onclick={() => selectRemoteEntry(entry)}>
                    {#if entry.isDir}
                      <span class="remote-entry-icon"><Folder class="w-3.5 h-3.5" /></span>
                    {:else}
                      <span class="remote-entry-icon"><FileText class="w-3.5 h-3.5" /></span>
                    {/if}
                    <span class="remote-entry-name">{entry.name}</span>
                    <span class="remote-entry-size">{entry.isDir ? "目录" : formatSize(entry.size || 0)}</span>
                  </button>
                {/each}
              {/if}
            </div>
          </div>
        {/if}
      </div>
    {/if}
    <div class="ai-input-row">
      <textarea
        bind:this={textareaEl}
        bind:value={input}
        onkeydown={handleKeydown}
        oninput={(e) => autoResize(e.currentTarget as HTMLTextAreaElement)}
        placeholder="描述你想执行的操作..."
        rows="1"
        class="ai-input"
      ></textarea>
      <button class="ai-send" disabled={!input.trim() || $aiLoading} onclick={handleSend}>
        <Send class="w-3.5 h-3.5" />
      </button>
    </div>
    <div class="ai-footer-bottom">
      <div class="ai-hint">Enter 发送 · Shift+Enter 换行</div>
      {#if activeAgentTaskId && $aiLoading}
        <button class="assistant-stop" title="停止当前任务" aria-label="停止当前任务" onclick={() => stopAgentTask()}>
          <Square class="w-3 h-3" />
          <span>停止任务</span>
        </button>
      {/if}
    </div>
  </div>
</div>

<style>
  .ai-root { display: flex; flex-direction: column; height: 100%; background: var(--app-panel); color: var(--app-text); }
  .ai-header { padding: 12px 16px; border-bottom: 1px solid var(--app-border); }
  .ai-brand { display: flex; align-items: center; gap: 10px; }
  .ai-icon {
    width: 32px; height: 32px; border-radius: 10px;
    background: linear-gradient(135deg, #8b5cf6, #ec4899);
    display: flex; align-items: center; justify-content: center;
    box-shadow: 0 4px 12px rgba(139, 92, 246, 0.3);
  }
  .ai-title { font-size: 14px; font-weight: 600; color: var(--app-text); }
  .ai-sub { font-size: 10px; color: var(--app-muted); }
  .conversation-toggle { min-width: 0; max-width: 150px; margin-left: auto; display: inline-flex; align-items: center; gap: 4px; border: 1px solid var(--app-border); border-radius: 5px; padding: 4px 6px; background: var(--app-panel-muted); color: var(--app-muted); font-size: 10px; cursor: pointer; }
  .conversation-toggle span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .conversation-toggle:hover:not(:disabled), .conversation-new:hover:not(:disabled) { color: var(--app-text); border-color: var(--app-accent); }
  .conversation-new { width: 24px; height: 24px; display: inline-flex; align-items: center; justify-content: center; border: 1px solid var(--app-border); border-radius: 5px; background: var(--app-panel-muted); color: var(--app-muted); cursor: pointer; }
  .conversation-toggle:disabled, .conversation-new:disabled { cursor: wait; opacity: .55; }
  .clear-history { margin-left: auto; padding: 4px 7px; border: 1px solid #3b82f6; border-radius: 5px; background: #172554; color: #dbeafe; font-size: 10px; font-weight: 600; cursor: pointer; box-shadow: inset 0 1px 0 rgba(255,255,255,.08); }
  .clear-history + .clear-history { margin-left: 0; }
  .clear-history:hover { color: #fff; border-color: #60a5fa; background: #1d4ed8; }
  .assistant-stop { height: 24px; display: inline-flex; align-items: center; justify-content: center; gap: 5px; padding: 0 8px; border: 1px solid rgba(248,113,113,.45); border-radius: 5px; background: rgba(127,29,29,.18); color: #fca5a5; font-size: 10px; cursor: pointer; }
  .assistant-stop:hover { color: #fff; border-color: #f87171; background: rgba(185,28,28,.45); }
  .assistant-close { width: 24px; height: 24px; margin-left: 4px; display: inline-flex; align-items: center; justify-content: center; border: 1px solid transparent; border-radius: 5px; background: transparent; color: var(--app-muted); cursor: pointer; }
  .assistant-close:hover { color: #fca5a5; border-color: rgba(248,113,113,0.3); background: rgba(248,113,113,0.08); }
  .ai-body { flex: 1 1 0; min-height: 0; overflow-y: auto; overscroll-behavior: contain; padding: 12px; display: flex; flex-direction: column; gap: 12px; }
  .agent-task-list, .agent-timeline { border: 1px solid var(--app-border); border-radius: 8px; background: var(--app-panel-muted); padding: 8px; }
  .agent-task-list-head, .agent-timeline-head { display: flex; align-items: center; justify-content: space-between; gap: 8px; color: var(--app-muted); font-size: 11px; font-weight: 600; margin-bottom: 6px; }
  .agent-task-empty { padding: 8px 4px; color: var(--app-subtle); font-size: 11px; text-align: center; }
  .agent-task-item { display: flex; align-items: center; gap: 7px; width: 100%; min-width: 0; border: 0; border-top: 1px solid var(--app-border); background: transparent; color: var(--app-text); padding: 7px 2px; text-align: left; cursor: pointer; }
  .agent-task-item:hover:not(:disabled) { color: var(--app-accent); }
  .agent-task-item:disabled { cursor: default; opacity: .7; }
  .agent-task-status { width: 7px; height: 7px; border-radius: 50%; background: #94a3b8; flex-shrink: 0; }
  .agent-task-status[data-status="running"] { background: #60a5fa; }
  .agent-task-status[data-status="waiting_approval"] { background: #fbbf24; }
  .agent-task-status[data-status="completed"] { background: #4ade80; }
  .agent-task-status[data-status="failed"] { background: #f87171; }
  .agent-task-status[data-status="interrupted"] { background: #fb923c; }
  .agent-resume { margin-left: 8px; border: 1px solid #60a5fa; border-radius: 4px; background: #1d4ed8; color: #fff; padding: 3px 7px; font-size: 11px; cursor: pointer; white-space: nowrap; }
  .agent-resume:hover:not(:disabled) { background: #2563eb; }
  .agent-resume:disabled { cursor: wait; opacity: .7; }
  .agent-task-goal { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 11px; }
  .agent-task-state { flex-shrink: 0; color: var(--app-subtle); font: 10px monospace; }
  .conversation-list { border: 1px solid var(--app-border); border-radius: 8px; background: var(--app-panel-muted); padding: 8px; }
  .conversation-list-head { display: flex; justify-content: space-between; color: var(--app-muted); font-size: 11px; margin-bottom: 4px; }
  .conversation-item { width: 100%; display: flex; flex-direction: column; align-items: flex-start; gap: 2px; border: 0; border-top: 1px solid var(--app-border); background: transparent; color: var(--app-text); padding: 7px 2px; text-align: left; cursor: pointer; }
  .conversation-item:hover:not(:disabled), .conversation-item.selected { color: var(--app-accent); }
  .conversation-item:disabled { cursor: wait; opacity: .6; }
  .conversation-item-main { width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 11px; }
  .conversation-item-meta { color: var(--app-subtle); font-size: 10px; }
  .conversation-create { display: inline-flex; align-items: center; gap: 4px; margin-top: 5px; border: 0; background: transparent; color: var(--app-accent); font-size: 10px; cursor: pointer; }
  .agent-timeline { background: var(--app-panel-strong); }
  .agent-timeline-head { color: var(--app-text); }
  .timeline-item { display: flex; gap: 8px; position: relative; padding: 5px 0; }
  .timeline-item:not(:last-child)::before { content: ""; position: absolute; left: 3px; top: 14px; bottom: -6px; width: 1px; background: var(--app-border); }
  .timeline-marker { width: 7px; height: 7px; border-radius: 50%; background: #818cf8; margin-top: 5px; flex-shrink: 0; z-index: 1; }
  .timeline-approval-required .timeline-marker { background: #fbbf24; }
  .timeline-deletion-approval { margin: 2px -4px; padding: 7px 4px; border: 1px solid rgba(248,113,113,.42); border-radius: 6px; background: rgba(127,29,29,.14); }
  .timeline-deletion-approval .timeline-marker { background: #ef4444; box-shadow: 0 0 0 3px rgba(239,68,68,.14); }
  .timeline-deletion-approval .timeline-title { color: #fca5a5; }
  .timeline-tool-finished .timeline-marker, .timeline-final .timeline-marker { background: #4ade80; }
  .timeline-error .timeline-marker { background: #f87171; }
  .timeline-content { min-width: 0; flex: 1; }
  .timeline-title { display: flex; align-items: baseline; justify-content: space-between; gap: 8px; color: var(--app-text); font-size: 12px; font-weight: 600; }
  .timeline-title time { color: var(--app-subtle); font: 10px monospace; flex-shrink: 0; }
  .timeline-meta { color: var(--app-muted); font-size: 11px; margin-top: 2px; }
  .timeline-content pre { margin: 4px 0 0; padding: 7px; border-radius: 5px; background: var(--app-panel-muted); color: var(--app-muted); white-space: pre-wrap; overflow-wrap: anywhere; max-height: 220px; overflow: auto; font: 12px/1.55 monospace; }
  .tool-result-card { margin-top: 5px; padding: 7px; border: 1px solid var(--app-border); border-radius: 6px; background: var(--app-panel-muted); }
  .tool-result-head { display: flex; align-items: center; justify-content: space-between; gap: 8px; min-width: 0; }
  .tool-result-head code { min-width: 0; overflow: hidden; text-overflow: ellipsis; color: var(--app-text); font: 11px/1.4 monospace; }
  .tool-result-status { flex-shrink: 0; font-size: 10px; }
  .tool-status-ok { color: #86efac; }
  .tool-status-error { color: #fca5a5; }
  .tool-status-running { color: #fde68a; }
  .tool-status-info { color: var(--app-muted); }
  .tool-command { color: var(--app-text) !important; }
  .tool-result-output { max-height: 180px !important; color: var(--app-muted) !important; }
  .tool-result-error { margin-top: 5px; color: #fca5a5; font: 11px/1.5 monospace; white-space: pre-wrap; overflow-wrap: anywhere; }
  .tool-raw-details { margin-top: 5px; color: var(--app-muted); font-size: 10px; }
  .tool-raw-details summary { cursor: pointer; user-select: none; }
  .tool-raw-details pre { margin-top: 4px; max-height: 160px; }
  .timeline-report { margin-top: 5px; padding: 7px; border-radius: 6px; background: var(--app-panel-muted); }
  .timeline-report :global(p:first-child) { margin-top: 0; }
  .timeline-report :global(p:last-child) { margin-bottom: 0; }
  .ai-empty { text-align: center; padding: 24px 8px; }
  .ai-empty-icon { width: 48px; height: 48px; border-radius: 14px; background: rgba(139, 92, 246, 0.1); display: flex; align-items: center; justify-content: center; margin: 0 auto 12px; border: 1px solid rgba(139, 92, 246, 0.1); }
  .ai-empty-title { font-size: 14px; font-weight: 500; color: var(--app-text); margin-bottom: 4px; }
  .ai-empty-desc { font-size: 11px; color: var(--app-muted); margin-bottom: 16px; }
  .ai-prompts { display: flex; flex-direction: column; gap: 6px; }
  .ai-prompt-btn {
    padding: 8px 12px; border-radius: 8px; border: 1px solid #334155;
    background: var(--app-panel-strong); color: #cbd5e1; font-size: 11px;
    cursor: pointer; transition: all 0.15s; text-align: left; box-shadow: inset 0 1px 0 rgba(255,255,255,.04);
  }
  .ai-prompt-btn:hover { background: #172554; border-color: #3b82f6; color: #dbeafe; }
  .ai-msg { display: flex; gap: 8px; }
  .msg-user { justify-content: flex-end; }
  .msg-avatar {
    width: 26px; height: 26px; border-radius: 8px; display: flex; align-items: center; justify-content: center; flex-shrink: 0;
  }
  .msg-avatar.bot { background: linear-gradient(135deg, #8b5cf6, #ec4899); }
  .msg-avatar.user { background: linear-gradient(135deg, #3b82f6, #6366f1); font-size: 9px; font-weight: 700; color: white; }
  .msg-bubble { max-width: 82%; padding: 9px 13px; border-radius: 12px; font-size: 14px; line-height: 1.7; }
  .msg-user .msg-bubble { background: #6366f1; color: white; border-bottom-right-radius: 4px; }
  .msg-bot .msg-bubble { background: var(--app-panel-muted); color: var(--app-text); border-bottom-left-radius: 4px; border: 1px solid var(--app-border); }
  .loading-bubble { display: flex; gap: 4px; padding: 12px 16px; }
  .dot { width: 6px; height: 6px; border-radius: 50%; background: #a5b4fc; animation: bounce 0.8s infinite; }
  @keyframes bounce { 0%, 100% { transform: translateY(0); } 50% { transform: translateY(-6px); } }
  .msg-text { word-break: break-word; }
  .markdown-content :global(p) { margin: 0 0 10px; }
  .markdown-content :global(p:last-child) { margin-bottom: 0; }
  .markdown-content :global(h1), .markdown-content :global(h2), .markdown-content :global(h3) { margin: 14px 0 7px; line-height: 1.35; color: var(--app-text); }
  .markdown-content :global(h1) { font-size: 1.35em; }
  .markdown-content :global(h2) { font-size: 1.2em; }
  .markdown-content :global(h3) { font-size: 1.08em; }
  .markdown-content :global(ul), .markdown-content :global(ol) { margin: 7px 0 10px; padding-left: 22px; }
  .markdown-content :global(li) { margin: 3px 0; }
  .markdown-content :global(blockquote) { margin: 8px 0; padding: 5px 10px; border-left: 3px solid var(--app-accent); color: var(--app-muted); background: var(--app-panel-strong); }
  .markdown-content :global(code) { padding: 2px 4px; border-radius: 4px; background: var(--app-panel-strong); color: #c4b5fd; font: 0.9em/1.4 "Consolas", "Cascadia Code", monospace; }
  .markdown-content :global(pre) { margin: 9px 0; padding: 10px; max-width: 100%; overflow-x: auto; border-radius: 7px; background: #0b1120; color: #e2e8f0; }
  .markdown-content :global(pre code) { padding: 0; background: transparent; color: inherit; white-space: pre; font-size: 0.9em; }
  .markdown-content :global(table) { display: block; max-width: 100%; overflow-x: auto; border-collapse: collapse; margin: 9px 0; }
  .markdown-content :global(th), .markdown-content :global(td) { padding: 5px 8px; border: 1px solid var(--app-border); text-align: left; white-space: nowrap; }
  .markdown-content :global(th) { background: var(--app-panel-strong); color: var(--app-text); }
  .markdown-content :global(a) { color: var(--app-accent); text-decoration: underline; text-underline-offset: 2px; }
  .command-suggestions { display: flex; flex-direction: column; gap: 5px; margin-top: 8px; }
  .command-suggestion {
    display: flex; align-items: center; gap: 6px; min-width: 0;
    padding: 5px 6px; border: 1px solid rgba(129,140,248,0.22);
    border-radius: 6px; background: var(--app-panel-strong);
  }
  .command-suggestion code {
    flex: 1; min-width: 0; color: #c4b5fd; font-size: 12px;
    white-space: pre-wrap; overflow-wrap: anywhere;
  }
  .command-suggestion-actions { display: flex; flex-shrink: 0; gap: 2px; }
  .command-suggestion .copy-msg { margin-top: 0; }
  .command-suggestion .command-execute { background: #14532d; border-color: #22c55e; color: #dcfce7; }
  .command-suggestion .command-execute:hover { background: #166534; border-color: #4ade80; color: #fff; }
  .msg-actions { display: flex; gap: 4px; }
  .copy-msg { display: inline-flex; align-items: center; justify-content: center; width: 24px; height: 24px; margin-top: 6px; padding: 0; border: 1px solid #334155; border-radius: 5px; background: var(--app-panel-strong); color: #cbd5e1; cursor: pointer; }
  .copy-msg:hover { color: #fff; background: #1e3a8a; border-color: #60a5fa; }
  .ai-error { padding: 8px 12px; border-radius: 8px; background: rgba(239, 68, 68, 0.1); color: #fca5a5; font-size: 12px; }
  .agent-approval {
    border: 1px solid rgba(245,158,11,0.35); border-radius: 8px;
    background: rgba(245,158,11,0.08); padding: 10px; color: var(--app-text);
  }
  .agent-approval.deletion-approval {
    border-color: rgba(248,113,113,0.78); background: rgba(127,29,29,0.2);
    box-shadow: 0 0 0 1px rgba(248,113,113,0.14), 0 8px 24px rgba(127,29,29,0.18);
  }
  .agent-approval-head { display: flex; align-items: center; gap: 6px; color: #fbbf24; font-size: 12px; font-weight: 600; margin-bottom: 7px; }
  .deletion-approval .agent-approval-head { color: #f87171; }
  .deletion-approval-warning { margin-bottom: 8px; padding: 7px 8px; border-left: 3px solid #ef4444; background: rgba(127,29,29,0.28); color: #fecaca; font-size: 11px; line-height: 1.45; }
  .agent-approval-meta { display: flex; flex-direction: column; gap: 3px; color: var(--app-muted); font-size: 11px; margin-bottom: 7px; }
  .agent-approval pre {
    margin: 0; padding: 8px; border-radius: 6px; background: var(--app-panel-strong);
    color: #fde68a; white-space: pre-wrap; overflow-wrap: anywhere; font-size: 12px;
  }
  .agent-approval-actions { display: flex; justify-content: flex-end; gap: 6px; margin-top: 8px; }
  .agent-approval-actions button {
    border: 1px solid var(--app-border); border-radius: 6px; padding: 5px 8px;
    font-size: 11px; cursor: pointer;
  }
  .agent-reject { background: transparent; color: var(--app-muted); }
  .agent-reject:hover { color: #fca5a5; border-color: rgba(248,113,113,0.35); }
  .agent-approve { background: #16a34a; color: white; border-color: #16a34a; }
  .agent-approve:hover { background: #15803d; }
  .agent-approve-delete { background: #dc2626; border-color: #dc2626; }
  .agent-approve-delete:hover { background: #b91c1c; }
  .ai-footer { padding: 12px; border-top: 1px solid var(--app-border); }
  .skill-controls { display: flex; flex-wrap: wrap; align-items: end; gap: 7px; margin-bottom: 8px; padding: 8px; border: 1px solid var(--app-border); border-radius: 6px; background: var(--app-panel-muted); }
  .skill-picker, .skill-param { display: grid; gap: 4px; min-width: 110px; }
  .skill-picker span, .skill-param span { color: var(--app-muted); font-size: 9px; }
  .skill-controls select, .skill-controls input[type="text"], .skill-controls input[type="number"] { min-width: 0; height: 28px; padding: 4px 7px; border: 1px solid var(--app-border-strong); border-radius: 5px; background: var(--app-panel-strong); color: var(--app-text); font-size: 10px; }
  .skill-controls input[type="checkbox"] { width: 28px; height: 28px; accent-color: var(--app-accent); }
  .target-params { flex: 1 1 260px; }
  .target-params input { width: 100%; }
  .skill-meta { max-width: 300px; overflow: hidden; color: var(--app-subtle); font: 9px monospace; text-overflow: ellipsis; white-space: nowrap; }
  .ai-actions { display: flex; flex-wrap: wrap; gap: 5px; margin-bottom: 7px; }
  .target-select { max-width: 180px; min-width: 90px; height: 24px; border: 1px solid var(--app-border); border-radius: 5px; background: var(--app-panel); color: var(--app-text); font-size: 10px; }
  .ai-actions button { display: flex; align-items: center; gap: 4px; padding: 5px 7px; border: 1px solid #3b82f6; border-radius: 6px; background: #172554; color: #dbeafe; font-size: 10px; font-weight: 600; cursor: pointer; box-shadow: inset 0 1px 0 rgba(255,255,255,.08); }
  .ai-actions button:hover:not(:disabled) { color: #fff; border-color: #93c5fd; background: #1d4ed8; }
  .ai-actions button.active { color: #ecfdf5; border-color: #34d399; background: #065f46; }
  .ai-actions button.active:hover:not(:disabled) { background: #047857; border-color: #6ee7b7; }
  .ai-actions button:disabled { opacity: 0.38; cursor: not-allowed; }
  .file-analyzer {
    margin-bottom: 8px; padding: 7px; border-radius: 8px;
    background: var(--app-panel-muted); border: 1px solid var(--app-border);
  }
  .file-analyzer-head {
    display: flex; align-items: center; justify-content: space-between; gap: 8px;
    margin-bottom: 6px; color: var(--app-muted); font-size: 10px;
  }
  .file-close {
    width: 22px; height: 22px; border: 1px solid #475569; border-radius: 5px; background: var(--app-panel-strong);
    color: #cbd5e1; display: inline-flex; align-items: center; justify-content: center;
    cursor: pointer; flex-shrink: 0;
  }
  .file-close:hover { color: #fff; border-color: #f87171; background: #7f1d1d; }
  .file-path-row { display: flex; align-items: center; gap: 6px; min-width: 0; }
  .file-path-input {
    flex: 1; min-width: 0; height: 30px; border: none; outline: none;
    background: var(--app-panel-strong); color: var(--app-text); border-radius: 6px;
    padding: 0 8px; font-size: 13px; font-family: inherit;
  }
  .file-path-input::placeholder { color: var(--app-subtle); }
  .file-pick, .file-analyze {
    height: 30px; border: 1px solid #3b82f6; border-radius: 6px;
    background: #172554; color: #dbeafe; cursor: pointer;
    display: inline-flex; align-items: center; justify-content: center; flex-shrink: 0;
  }
  .file-pick { width: 30px; }
  .file-analyze { gap: 4px; padding: 0 8px; border-color: #14b8a6; background: #134e4a; color: #ccfbf1; font-size: 10px; font-weight: 600; }
  .file-pick:hover:not(:disabled) { color: #fff; border-color: #93c5fd; background: #1d4ed8; }
  .file-analyze:hover:not(:disabled) { color: #fff; border-color: #5eead4; background: #0f766e; }
  .file-pick:disabled, .file-analyze:disabled { opacity: 0.35; cursor: not-allowed; }
  .remote-browser {
    margin-top: 7px; border: 1px solid var(--app-border); border-radius: 7px;
    background: var(--app-panel-strong); overflow: hidden;
  }
  .remote-browser-head {
    display: flex; align-items: center; gap: 5px; padding: 5px;
    border-bottom: 1px solid var(--app-border);
  }
  .remote-current {
    flex: 1; min-width: 0; color: var(--app-muted); font: 10px/1.2 monospace;
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .remote-nav-btn {
    width: 24px; height: 24px; border: 1px solid #475569; border-radius: 5px; background: var(--app-panel-muted);
    color: #cbd5e1; display: inline-flex; align-items: center; justify-content: center; cursor: pointer;
  }
  .remote-nav-btn:hover:not(:disabled) { color: #fff; border-color: #60a5fa; background: #1e3a8a; }
  .remote-nav-btn:disabled { opacity: 0.35; cursor: not-allowed; }
  .remote-browser-list { max-height: 168px; overflow-y: auto; padding: 4px; }
  .remote-browser-state { padding: 10px 7px; color: var(--app-muted); font-size: 11px; text-align: center; }
  .remote-browser-state.error { color: #fca5a5; }
  .remote-entry {
    display: flex; align-items: center; gap: 6px; width: 100%; min-width: 0;
    padding: 5px 6px; border: 0; border-radius: 5px; background: transparent;
    color: var(--app-text); cursor: pointer; font-size: 11px; text-align: left;
  }
  .remote-entry:hover { background: rgba(99,102,241,0.14); color: #e0e7ff; }
  .remote-entry-icon { display: inline-flex; flex-shrink: 0; color: #818cf8; }
  .remote-entry-name { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .remote-entry-size { flex-shrink: 0; color: var(--app-muted); font-size: 10px; }
  .ai-input-row { display: flex; gap: 8px; padding: 8px; border-radius: 12px; background: var(--app-panel-muted); border: 1px solid var(--app-border); }
  .ai-input {
    flex: 1; border: none; outline: none; background: transparent; color: var(--app-text);
    resize: none; font-size: 13px; min-height: 32px; max-height: 120px;
    font-family: inherit; padding: 4px;
  }
  .ai-input::placeholder { color: var(--app-subtle); }
  .ai-send {
    width: 32px; height: 32px; border-radius: 32px; border: none;
    background: #6366f1; color: white; cursor: pointer; display: flex;
    align-items: center; justify-content: center; flex-shrink: 0; box-shadow: 0 3px 10px rgba(99,102,241,.42);
    transition: all 0.15s; align-self: flex-end;
  }
  .ai-send:hover { background: #4f46e5; box-shadow: 0 4px 14px rgba(99,102,241,.6); }
  .ai-send:disabled { opacity: 0.4; cursor: not-allowed; }
  .ai-root button:focus-visible { outline: 2px solid #93c5fd; outline-offset: 2px; }
  .ai-footer-bottom { display: flex; align-items: center; justify-content: space-between; gap: 8px; min-height: 24px; margin-top: 6px; }
  .ai-hint { text-align: left; font-size: 10px; color: var(--app-subtle); }
</style>
