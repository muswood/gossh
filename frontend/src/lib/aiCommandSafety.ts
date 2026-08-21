// owner: muswood | Email: mumu920@outlook.com
import type { AIMessage } from "$lib/stores";

const shellLanguages = new Set(["", "bash", "sh", "shell", "zsh", "fish", "console", "terminal"]);
const commandStart = /^(?:sudo\s+|timeout\s+\S+\s+|env\s+)?(?:kubectl|oc|helm|docker|podman|crictl|ctr|nerdctl|systemctl|journalctl|nginx|openresty|mysql|psql|redis-cli|mongo|mongosh|curl|wget|openssl|dig|nslookup|ping|traceroute|tracepath|ip|ss|netstat|lsof|ps|top|free|df|du|ls|find|grep|egrep|fgrep|awk|sed|cat|echo|printf|less|tail|head|sort|uniq|wc|date|whoami|uname|tar|gzip|gunzip|zip|unzip|cp|mv|chmod|chown|chgrp|rsync|scp|ssh|aliyun|aws|az|gcloud|git|npm|pnpm|yarn|go|python|python3|node|bash|sh)\b/;

function cleanCommandLine(line: string) {
  return line
    .trim()
    .replace(/^\$+\s*/, "")
    .replace(/^#+\s+(?=\S)/, "")
    .replace(/^>\s*/, "")
    .replace(/^[\w.-]+@[\w.-]+(?::[^\s$#]+)?[$#]\s*/, "")
    .trim();
}

function isStandaloneCommandLine(line: string) {
  const command = cleanCommandLine(line);
  // Eino/HTTP errors can start with words such as "node path:". They are
  // diagnostics, not shell commands, and must remain readable in reports.
  if (/^[A-Za-z][\w.-]*\s*:/i.test(command)) return false;
  return looksLikeCommand(command);
}

export function isDeleteCommand(command: string) {
  const normalized = cleanCommandLine(command).replace(/\s+/g, " ").trim();
  if (!normalized || normalized.startsWith("#")) return false;
  const checks = [
    /\brm\s+(-[A-Za-z]*\s*)?(?:--\s*)?\S+/i,
    /\brmdir\s+\S+/i,
    /\bunlink\s+\S+/i,
    /\bshred\s+\S+/i,
    /\bfind\b.*(?:\s-delete\b|\s-exec\s+rm\b)/i,
    /\bxargs\s+rm\b/i,
    /\bgit\s+rm\b/i,
    /\b(?:kubectl|oc)\s+delete\b/i,
    /\bhelm\s+(?:delete|uninstall)\b/i,
    /\b(?:docker|podman)\s+(?:container\s+|image\s+|volume\s+|network\s+)?rmi?\b/i,
    /\bdocker\s+compose\s+rm\b/i,
    /\b(?:crictl|ctr|nerdctl)\s+(?:rmi?|images\s+rm|containers\s+rm)\b/i,
    /\bterraform\s+destroy\b/i,
    /\b(?:apt|apt-get|yum|dnf|zypper)\s+(?:remove|purge|autoremove|erase)\b/i,
    /\brpm\s+-e\b/i,
    /\b(?:pip|pip3|npm|pnpm|yarn)\s+(?:uninstall|remove)\b/i,
    /\bDELETE\s+FROM\b/i,
    /\bDROP\s+(?:TABLE|DATABASE|SCHEMA|INDEX|COLLECTION)\b/i,
    /\bTRUNCATE\s+(?:TABLE\s+)?\S+/i,
    /\b(?:aws|aliyun|az|gcloud)\b.*\bdelete[-\w]*\b/i,
  ];
  return checks.some((pattern) => pattern.test(normalized));
}

export function isHighRiskCommand(command: string) {
  const normalized = cleanCommandLine(command).replace(/\s+/g, " ").trim();
  if (!normalized || normalized.startsWith("#")) return false;
  const checks = [
    /(?:^|[;&|]\s*)(?:sudo\s+)?(?:systemctl|service)\s+(?:start|stop|restart|reload|enable|disable|mask|unmask)\b/i,
    /(?:^|[;&|]\s*)(?:sudo\s+)?(?:reboot|shutdown|poweroff|halt|init)\b/i,
    /(?:^|[;&|]\s*)(?:sudo\s+)?(?:docker|podman|crictl|ctr|nerdctl)\s+(?:start|stop|restart|kill|exec|run|create|update|commit|push|tag|volume|network)\b/i,
    /(?:^|[;&|]\s*)(?:sudo\s+)?(?:kubectl|oc)\s+(?:apply|create|edit|patch|replace|scale|annotate|label|cordon|uncordon|drain|taint|rollout\s+restart|set)\b/i,
    /(?:^|[;&|]\s*)(?:sudo\s+)?helm\s+(?:install|upgrade|rollback|repo\s+add|repo\s+update)\b/i,
    /(?:^|[;&|]\s*)(?:sudo\s+)?(?:apt|apt-get|yum|dnf|zypper|apk)\s+(?:install|upgrade|dist-upgrade|full-upgrade|update|add)\b/i,
    /(?:^|[;&|]\s*)(?:sudo\s+)?(?:pip|pip3|npm|pnpm|yarn|go)\s+(?:install|add|update|upgrade|publish)\b/i,
    /(?:^|[;&|]\s*)(?:sudo\s+)?(?:chmod|chown|chgrp|usermod|groupmod|passwd|visudo)\b/i,
    /(?:^|[;&|]\s*)(?:sudo\s+)?(?:cp|mv|rsync|scp)\b/i,
    /(?:^|[;&|]\s*)(?:sudo\s+)?(?:tee|dd|truncate|mkfs|mount|umount|swapon|swapoff|iptables|nft|firewall-cmd|ufw)\b/i,
    /(?:^|[;&|]\s*)(?:sudo\s+)?(?:sed|perl|python|python3|node|bash|sh)\b.*\b(?:-i|open\(|write\(|appendFile|writeFile)\b/i,
    /(?:^|[;&|]\s*)(?:sudo\s+)?git\s+(?:checkout|switch|reset|revert|clean|pull|merge|rebase|commit|push|stash|apply)\b/i,
    /(?:^|[;&|]\s*)(?:sudo\s+)?(?:mysql|psql|redis-cli|mongo|mongosh)\b.*\b(?:UPDATE|INSERT|DELETE|DROP|ALTER|CREATE|TRUNCATE|FLUSH|SET)\b/i,
    /(?:^|[;&|]\s*)(?:sudo\s+)?(?:aws|aliyun|az|gcloud)\b.*\b(?:create|update|put|set|start|stop|restart|attach|detach|modify|reboot|terminate|delete)[-\w]*\b/i,
    /(^|[^<])>\s*\S+|>>\s*\S+/,
  ];
  return checks.some((pattern) => pattern.test(normalized));
}

export function isBlockedTerminalCommand(command: string) {
  return isDeleteCommand(command) || isHighRiskCommand(command);
}

function looksLikeCommand(command: string) {
  if (!command || command.length > 320) return false;
  if (/[\u4e00-\u9fff]/.test(command)) return false;
  if (/^(?:#|\/\/|{|}|\[|\]|<|>|```)/.test(command)) return false;
  if (/^\w+\s*:\s+/.test(command)) return false;
  return commandStart.test(command) || /^[A-Za-z0-9_./-]+(?:\s+[-./\w${}"'=:@,%]+)+$/.test(command);
}

export function sanitizeDeleteCommandsInAIResponse(content: string) {
  let safe = content.replace(/```([^\n`]*)\n([\s\S]*?)```/g, (_match, lang: string, body: string) => {
    const sanitizedBody = body
      .split(/\r?\n/)
      .map((line) => isDeleteCommand(line) ? "# [已拦截删除命令]" : isHighRiskCommand(line) ? "# [已拦截高风险命令]" : line)
      .join("\n");
    return `\`\`\`${lang}\n${sanitizedBody}\`\`\``;
  });
  safe = safe.replace(/`([^`\n]+)`/g, (_match, inline: string) => {
    if (isDeleteCommand(inline)) return "`[已拦截删除命令]`";
    if (isHighRiskCommand(inline)) return "`[已拦截高风险命令]`";
    return `\`${inline}\``;
  });
  return safe
    .split(/\r?\n/)
    .map((line) => {
      const command = line
        .trim()
        .replace(/^(?:[-*+]|\d+[.)])\s+/, "")
        .replace(/^\$+\s*/, "");
      if (!isStandaloneCommandLine(command)) return line;
      if (isDeleteCommand(command)) return "[已拦截删除命令]";
      if (isHighRiskCommand(command)) return "[已拦截高风险命令]";
      return line;
    })
    .join("\n");
}

export function extractExecutableCommands(content: string) {
  const commands: string[] = [];
  const addCommand = (value: string) => {
    const command = cleanCommandLine(value).replace(/\s+$/, "");
    if (!looksLikeCommand(command) || isBlockedTerminalCommand(command)) return;
    if (!commands.includes(command)) commands.push(command);
  };

  for (const match of content.matchAll(/```([^\n`]*)\n([\s\S]*?)```/g)) {
    const lang = (match[1] || "").trim().toLowerCase();
    if (!shellLanguages.has(lang)) continue;
    for (const line of match[2].split(/\r?\n/)) addCommand(line);
  }

  for (const match of content.matchAll(/`([^`\n]+)`/g)) addCommand(match[1]);
  return commands.slice(0, 8);
}

export function prepareAssistantMessage(content: string, timestamp = Date.now()): AIMessage {
  const safeContent = sanitizeDeleteCommandsInAIResponse(content);
  const commands = extractExecutableCommands(safeContent);
  return {
    role: "assistant",
    content: safeContent,
    commands: commands.length ? commands : undefined,
    timestamp,
  };
}
