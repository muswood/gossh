// owner: muswood | Email: mumu920@outlook.com
export function remoteTargetKey(tab: any) {
  if (!tab || !["ssh", "telnet", "raw", "serial"].includes(tab.type)) return undefined;
  const connectionID = String(tab.connectionId || "").trim();
  if (connectionID) return `connection:${encodeURIComponent(connectionID)}`;
  if (tab.type !== "ssh") return undefined;
  const host = String(tab.host || "").trim();
  const username = String(tab.username || "").trim();
  const port = Number(tab.port);
  if (!host || !username || !Number.isFinite(port) || port <= 0) return undefined;
  return ["ssh", host, Math.round(port), username].map(encodeURIComponent).join(":");
}

export function titleForMessages(messages: any[]) {
  return String(messages?.find(message => message?.role === "user")?.content || "新会话").trim().slice(0, 80) || "新会话";
}

const agentConversationSummaryLimit = 8000;
const agentConversationExcerptLimit = 1200;

function conversationExcerpt(value: unknown, limit = agentConversationExcerptLimit) {
  const text = String(value || "").replace(/\s+/g, " ").trim();
  return text.length <= limit ? text : `${text.slice(0, limit - 1)}…`;
}

export function summarizeConversationForAgent(messages: any[]) {
  const items = (Array.isArray(messages) ? messages : []).filter((message) =>
    message?.role === "user" || message?.role === "assistant",
  );
  if (items.length === 0) return [];

  const users = items.filter(message => message.role === "user");
  const assistants = items.filter(message => message.role === "assistant");
  const lines = ["[会话历史摘要]", `历史消息数: ${items.length}`];
  if (users[0]) lines.push(`用户目标: ${conversationExcerpt(users[0].content)}`);
  for (const message of users.slice(-3)) {
    lines.push(`近期用户请求: ${conversationExcerpt(message.content)}`);
  }
  for (const message of assistants.slice(-3)) {
    lines.push(`近期助手结论: ${conversationExcerpt(message.content)}`);
  }
  let content = lines.join("\n");
  if (content.length > agentConversationSummaryLimit) {
    content = `${content.slice(0, agentConversationSummaryLimit - 1)}…`;
  }
  return [{ role: "user", content }];
}

export function migrateLegacyConversations(value: any) {
  if (Array.isArray(value)) return value;
  if (!value || typeof value !== "object") return [];
  return Object.entries(value).flatMap(([id, messages]: [string, any]) => {
    if (!Array.isArray(messages)) return [];
    const safeMessages = messages.slice(-100);
    const updatedAt = safeMessages.reduce((latest, message) => Math.max(latest, Number(message?.timestamp) || 0), 0) || Date.now();
    return [{ id, targetKey: undefined, title: titleForMessages(safeMessages), messages: safeMessages, createdAt: updatedAt, updatedAt }];
  });
}

export function pruneConversationRecords(records: any[], retentionDays: number, now = Date.now()) {
  const days = Number(retentionDays);
  if (!Number.isFinite(days) || days <= 0) return [];
  const cutoff = now - Math.round(days) * 86400000;
  return (Array.isArray(records) ? records : []).flatMap(record => {
    if (!record || typeof record.id !== "string") return [];
    const messages = Array.isArray(record.messages) ? record.messages.slice(-100) : [];
    const updatedAt = Math.max(Number(record.updatedAt) || 0, ...messages.map(message => Number(message?.timestamp) || 0));
    return updatedAt >= cutoff ? [{ ...record, messages, title: titleForMessages(messages), updatedAt }] : [];
  });
}
