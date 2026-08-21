// owner: muswood | Email: mumu920@outlook.com
const ANSI_SEQUENCE_SOURCE = "\\x1B(?:\\[[0-?]*[ -/]*[@-~]|\\][^\\x07]*(?:\\x07|\\x1B\\\\))";
const ANSI_SEQUENCE = new RegExp(ANSI_SEQUENCE_SOURCE, "g");

export interface TerminalHighlightRule {
  id: string;
  label: string;
  color: string;
  keywords: string[];
  kind?: "keyword" | "ip" | "number";
}

export const defaultTerminalHighlightRules: TerminalHighlightRule[] = [
  { id: "error", label: "错误", color: "#f87171", keywords: ["error", "fatal", "failed", "failure", "exception", "panic", "traceback", "denied", "refused", "not found", "command not found", "错误", "失败", "异常", "拒绝"] },
  { id: "warning", label: "警告", color: "#fbbf24", keywords: ["warn", "warning", "deprecated", "caution", "警告", "注意"] },
  { id: "success", label: "成功", color: "#4ade80", keywords: ["success", "succeeded", "completed", "ready", "listening", "started", "ok", "成功", "完成", "就绪", "已启动"] },
  { id: "status", label: "运行状态", color: "#22d3ee", keywords: ["running", "pending", "waiting", "connected", "disconnected", "stopped", "active", "运行", "等待", "已连接", "已断开", "停止"] },
  { id: "info", label: "信息", color: "#60a5fa", keywords: ["info", "notice", "debug", "information", "信息"] },
  { id: "ip", label: "IP 地址", color: "#c084fc", kind: "ip", keywords: [] },
  { id: "number", label: "数字", color: "#facc15", kind: "number", keywords: [] },
];

export function stripTerminalControlCodes(value: string): string {
  return value.replace(ANSI_SEQUENCE, "");
}

/** Remove common credentials and machine identifiers before terminal text can leave the app. */
export function sanitizeTerminalOutput(value: string): string {
  let safe = stripTerminalControlCodes(value);
  safe = safe.replace(/-----BEGIN [^-]+-----[\s\S]*?-----END [^-]+-----/gi, "[PRIVATE_KEY_REDACTED]");
  safe = safe.replace(/\b(Bearer\s+|Basic\s+)[A-Za-z0-9+/=._~-]+/gi, "$1[CREDENTIAL_REDACTED]");
  safe = safe.replace(/\b(?:sk-[A-Za-z0-9_-]{8,}|sk-ant-[A-Za-z0-9_-]{8,}|gh[pousr]_[A-Za-z0-9_]{8,}|glpat-[A-Za-z0-9_-]{8,}|xox[baprs]-[A-Za-z0-9-]{8,})\b/g, "[TOKEN_REDACTED]");
  safe = safe.replace(/(\b(?:password|passwd|pwd|token|api[_-]?key|secret|passphrase|authorization|private[_-]?key)\b\s*[:=]\s*)([^\s,;]+)/gi, "$1[CREDENTIAL_REDACTED]");
  safe = safe.replace(/(--(?:password|passwd|pwd|token|api[-_]?key|secret|passphrase)(?:=|\s+))([^\s]+)/gi, "$1[CREDENTIAL_REDACTED]");
  safe = safe.replace(/(https?:\/\/)([^\s/@:]+)(?::[^\s/@]*)?@/gi, "$1[CREDENTIAL_REDACTED]@");
  safe = safe.replace(/\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/gi, "[EMAIL_REDACTED]");
  safe = safe.replace(/\b(?:[a-zA-Z0-9._-]+)@(?:[a-zA-Z0-9._-]+)\b/g, "[USER_HOST_REDACTED]");
  safe = safe.replace(/\b(?:\d{1,3}\.){3}\d{1,3}\b/g, "[IP_REDACTED]");
  return safe;
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function colorSequence(color: string): string {
  const match = /^#([0-9a-f]{6})$/i.exec(color.trim());
  if (!match) return "";
  const hex = match[1];
  return `\x1b[38;2;${Number.parseInt(hex.slice(0, 2), 16)};${Number.parseInt(hex.slice(2, 4), 16)};${Number.parseInt(hex.slice(4, 6), 16)}m`;
}

function sourceForRule(rule: TerminalHighlightRule): string {
  const keywords = (rule.keywords || []).map(item => item.trim()).filter(Boolean).sort((a, b) => b.length - a.length);
  const keywordSource = keywords.length
    ? `(?<![\\p{L}\\p{N}_-])(?:${keywords.map(escapeRegExp).join("|")})(?![\\p{L}\\p{N}_-])`
    : "";
  if (rule.kind === "ip" || rule.id === "ip") {
    return ["\\b(?:\\d{1,3}\\.){3}\\d{1,3}\\b", keywordSource].filter(Boolean).join("|");
  }
  if (rule.kind === "number" || rule.id === "number") {
    return ["(?<![\\w.])[-+]?(?:\\d+(?:\\.\\d+)?|\\.\\d+)(?![\\w.])", keywordSource].filter(Boolean).join("|");
  }
  return keywordSource;
}

function highlightPlainText(value: string, rules: TerminalHighlightRule[]): string {
  const active = rules.map(rule => ({ rule, source: sourceForRule(rule), color: colorSequence(rule.color) })).filter(item => item.source && item.color);
  if (!active.length) return value;
  const matcher = new RegExp(active.map(item => `(${item.source})`).join("|"), "giu");
  return value.replace(matcher, (...args) => {
    const matched = String(args[0]);
    const index = active.findIndex((_, itemIndex) => args[itemIndex + 1] !== undefined);
    const color = active[index]?.color || "";
    return color ? `${color}${matched}\x1b[39m` : matched;
  });
}

export function highlightTerminalOutput(value: string, rules: TerminalHighlightRule[] = defaultTerminalHighlightRules, enabled = true): string {
  if (!enabled || !value) return value;
  const activeRules = Array.isArray(rules) ? rules : defaultTerminalHighlightRules;
  const ansi = new RegExp(ANSI_SEQUENCE_SOURCE, "g");
  let result = "";
  let offset = 0;
  let match: RegExpExecArray | null;
  while ((match = ansi.exec(value))) {
    result += highlightPlainText(value.slice(offset, match.index), activeRules);
    result += match[0];
    offset = match.index + match[0].length;
  }
  return result + highlightPlainText(value.slice(offset), activeRules);
}
