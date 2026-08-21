// owner: muswood | Email: mumu920@outlook.com
import { get } from "svelte/store";
import { language, type Language } from "$lib/stores";

const messages = {
  "zh-CN": {
    settings: "设置", general: "通用", terminal: "终端", security: "安全", aiConfig: "AI 配置", about: "关于", diagnostics: "诊断",
    generalSettings: "通用设置", interfaceTheme: "界面主题", themeDesc: "主界面明暗外观，终端配色可在终端页单独选择", dark: "深色", light: "亮色",
    language: "语言", languageDesc: "选择应用界面语言，修改后立即生效", simplifiedChinese: "简体中文", english: "English",
    sidebar: "显示侧边栏", sidebarDesc: "控制左侧连接管理面板的显示", sftpPanel: "显示 SFTP 面板", sftpPanelDesc: "控制底部文件传输面板的显示",
    dataManagement: "数据管理", exportConfig: "导出配置", importConfig: "导入配置", openssh: "OpenSSH 集成", importSSHConfig: "导入 SSH Config", importKnownHosts: "导入 known_hosts",
    welcome: "欢迎", welcomeTitle: "欢迎使用 GoSSH", welcomeDesc: "现代化的 SSH / SFTP / 串口 / AI 终端工具", sshConnection: "SSH 连接", sshDesc: "连接 Linux 服务器", serialConnection: "串口连接", serialDesc: "连接嵌入式设备", aiAssistant: "AI 助手", aiDesc: "智能命令生成与诊断", appSettings: "应用设置", appSettingsDesc: "AI / 主题 / 终端配置",
    ready: "就绪", show: "显示", hide: "隐藏", theme: "主题", newSession: "新建会话", ungrouped: "未分组", searchConnections: "搜索连接...", noConnections: "暂无保存的连接", newGroup: "新建分组", newConnection: "新建连接",
    loading: "正在连接", ssh: "SSH", telnet: "Telnet", tcp: "TCP", settingsSaved: "语言设置已更新", editConnection: "编辑连接", serial: "串口", connectionName: "连接名称", connectionNamePlaceholder: "可选，如：生产服务器", host: "主机地址", portName: "端口名称", port: "端口", username: "用户名", authentication: "认证方式", password: "密码", group: "分组", cancel: "取消", save: "保存", connect: "连接", optional: "可选", saveLog: "保存日志", copy: "复制", search: "搜索", connected: "已连接", local: "本地", fileTransfer: "文件传输", upload: "上传", download: "下载", directory: "目录", rename: "重命名", preview: "预览", permissions: "权限",
  },
  "en-US": {
    settings: "Settings", general: "General", terminal: "Terminal", security: "Security", aiConfig: "AI", about: "About", diagnostics: "Diagnostics",
    generalSettings: "General settings", interfaceTheme: "Interface theme", themeDesc: "Appearance of the main interface; terminal themes are configured per terminal", dark: "Dark", light: "Light",
    language: "Language", languageDesc: "Choose the application language. Changes take effect immediately", simplifiedChinese: "Simplified Chinese", english: "English",
    sidebar: "Show sidebar", sidebarDesc: "Show the connection management panel", sftpPanel: "Show SFTP panel", sftpPanelDesc: "Show the file transfer panel at the bottom",
    dataManagement: "Data management", exportConfig: "Export configuration", importConfig: "Import configuration", openssh: "OpenSSH integration", importSSHConfig: "Import SSH Config", importKnownHosts: "Import known_hosts",
    welcome: "Welcome", welcomeTitle: "Welcome to GoSSH", welcomeDesc: "A modern SSH / SFTP / serial / AI terminal tool", sshConnection: "SSH connection", sshDesc: "Connect to a Linux server", serialConnection: "Serial connection", serialDesc: "Connect to an embedded device", aiAssistant: "AI assistant", aiDesc: "Intelligent command generation and diagnostics", appSettings: "Application settings", appSettingsDesc: "AI / theme / terminal configuration",
    ready: "Ready", show: "Show", hide: "Hide", theme: "Theme", newSession: "New session", ungrouped: "Ungrouped", searchConnections: "Search connections...", noConnections: "No saved connections", newGroup: "New group", newConnection: "New connection",
    loading: "Connecting", ssh: "SSH", telnet: "Telnet", tcp: "TCP", settingsSaved: "Language updated", editConnection: "Edit connection", serial: "Serial", connectionName: "Connection name", connectionNamePlaceholder: "Optional, e.g. production server", host: "Host", portName: "Port name", port: "Port", username: "Username", authentication: "Authentication", password: "Password", group: "Group", cancel: "Cancel", save: "Save", connect: "Connect", optional: "Optional", saveLog: "Save log", copy: "Copy", search: "Search", connected: "Connected", local: "Local", fileTransfer: "File transfer", upload: "Upload", download: "Download", directory: "Directory", rename: "Rename", preview: "Preview", permissions: "Permissions",
  },
} as const;

export type I18nKey = keyof typeof messages["zh-CN"];

export function t(key: I18nKey, locale: Language = get(language)): string {
  return messages[locale]?.[key] ?? messages["zh-CN"][key] ?? key;
}
