<!-- owner: muswood | Email: mumu920@outlook.com -->

# GoSSH

> A Windows desktop operations workspace for SSH, Telnet, raw TCP, serial devices, SFTP, port forwarding, and approval-driven AI assistance.

[中文](#中文说明) | [English](#english)

GoSSH is a local-first desktop application built with Wails, Go, Svelte, and xterm.js. It brings interactive terminal sessions, SSH file management, saved connections, local knowledge retrieval, MCP tools, and a checkpointed AI Agent into one operational workspace.

> [!WARNING]
> GoSSH is a single-user desktop tool, not a multi-tenant bastion host or centralized audit platform. Treat every connected target as production. Verify host fingerprints, review every approval card, and keep a backup before manual destructive SFTP operations.

## Highlights

| Area | What GoSSH provides |
| --- | --- |
| Connections | Saved SSH, Telnet, raw TCP, and serial profiles; groups, favorites, search, import/export, OpenSSH config import |
| SSH | Password, key, SSH Agent, keyboard-interactive authentication, host-key trust, jump hosts, proxies, PTY resizing, keepalive |
| Terminal | xterm.js terminal, split panes, search, copy/paste, themes, cursor customization, keyword highlighting, terminal snapshots and transfers |
| SSH file management | SFTP browse, upload/download with resume and SHA-256 verification, rename, editor, permissions, links, disk usage, cancellation |
| Networking | Local, remote, and dynamic SOCKS SSH port forwarding with persisted profiles |
| AI Agent | Tool-based tasks, per-step approval, command policy, structured reports, streaming output, checkpoints, recovery, and task history |
| Knowledge and extensions | Local RAG documents, optional Qdrant backend, Markdown Skills, stdio/HTTP MCP clients with target ACL support |
| Local security | SQLite persistence, AES-256-GCM protection for sensitive settings, host-key verification, output redaction, bounded local reads |

## Screens and Workflow

1. Create or import a connection, assign it to a group, and save it without needing to connect immediately.
2. Open one or more terminal tabs. SSH, Telnet, raw TCP, and serial sessions can be used interactively.
3. Use SFTP and port forwarding only from an active SSH session.
4. Open the AI panel for a connected terminal, ask for analysis, and approve each requested operation unless the configured read-only exception applies.
5. Review tool output, final report, and durable task timeline. Interrupted Agent tasks can be recovered when their session remains available.

## Requirements

### End users

- Windows 10 or Windows 11, 64-bit.
- Microsoft Edge WebView2 Runtime, preferably the Evergreen release.
- Network access to the remote targets and any configured AI/MCP/RAG services.

### Developers

- Go `1.26` or newer, as declared in `go.mod`.
- Node.js and npm compatible with the frontend dependencies.
- Wails CLI v2.
- A Windows toolchain when creating a native Windows release outside the provided build environment.

## Install and Run

### Use a release build

Download a Windows executable from the project's releases, then run it directly. The application creates its local data directory on first launch:

```text
C:\Users\<your-user>\.gossh
```

The directory contains configuration, encrypted secrets, Agent checkpoints, RAG data, and visible SSH session logs.

### Development mode

```bash
git clone <repository-url> gossh
cd gossh

go mod download
cd frontend && npm install && cd ..

wails dev
```

Wails starts the Vite development server and the desktop application. The development web endpoint is normally available through Wails at `http://localhost:34115`.

### Build a Windows executable

```bash
wails build -platform windows/amd64
```

The default output is written under `build/bin/`. In restricted environments where the default Go cache is not writable:

```bash
GOCACHE=/tmp/gossh-go-build-cache wails build -platform windows/amd64
```

## Verification

Run the backend and frontend checks before publishing a build:

```bash
GOCACHE=/tmp/gossh-go-build-cache go test ./...
npm run check --prefix frontend
```

Create the release artifact with the full frontend build enabled:

```bash
GOCACHE=/tmp/gossh-go-build-cache wails build -platform windows/amd64
```

## Connection Types

| Type | Interactive terminal | AI terminal command completion | SFTP | Port forwarding |
| --- | --- | --- | --- | --- |
| SSH | Yes | Yes, with a remote completion marker and prompt-aware fallback | Yes | Yes |
| Telnet | Yes | Yes, when the device returns a recognizable prompt | No | No |
| Raw TCP | Yes | No deterministic completion detection | No | No |
| Serial | Yes | Not available as an Agent terminal target | No | No |

### SSH authentication and trust

SSH profiles support passwords, inline keys, key paths, certificates, SSH Agent authentication, jump hosts, HTTP CONNECT/SOCKS5/ProxyCommand proxies, startup commands, keepalive, and keyboard-interactive prompts.

GoSSH does not silently accept a new or changed host key. Confirm the displayed fingerprint before it is written to `known_hosts`.

### SFTP

SFTP is deliberately SSH-only. The file panel supports local and remote browsing, upload/download, resumed transfers, retries, SHA-256 verification, rename, edit, directory creation, recursive removal, permissions, symbolic links, disk usage, and server extension discovery.

Manual SFTP operations are direct user actions. AI Agent SFTP reads use dedicated tools and require their own approval cards.

## AI Agent Safety Model

The Agent is designed to collect evidence before drawing conclusions. It does not see the terminal screen; it receives terminal data through the application's tool results and streaming output.

### Command lifecycle

```text
AI request
  -> command policy and semantic assessment
  -> approval card (unless eligible read-only approval is disabled)
  -> terminal/SFTP/MCP/web tool execution
  -> streamed and redacted tool output
  -> durable step/event checkpoint
  -> final answer or structured report
```

### Safety controls

- Every Agent terminal command is policy-checked before execution.
- Read-only commands can be configured to skip approval; writes, deletes, unknown commands, and Agent SFTP reads still require approval.
- Non-administrator write operations require an idempotency key plus precondition, snapshot, verification, and rollback commands.
- Deletes require explicit enablement and a second approval.
- Agent tasks wait for the user's decision. Terminal operations use a 30-minute per-step ceiling; users can interrupt a running remote command from the terminal with `Ctrl-C`.
- For SSH, the first Agent task probes the remote system with `uname -a`; later tasks reuse that session-scoped result.
- SSH command completion uses a unique remote marker. Telnet completion depends on the terminal prompt returned by the device.

### AI configuration

Configure the provider under **Settings -> AI**. The application supports OpenAI-compatible chat endpoints and provider presets for OpenAI, DeepSeek, Qwen, and Claude-style services. Tool calling requires a compatible Chat Completions-style endpoint.

Sensitive API keys are encrypted in local storage. Never paste credentials into prompts, terminal output, Skills, or RAG documents unless you accept their local retention and configured provider exposure risks.

### Conversation history

AI conversations are keyed to a saved connection ID. SSH, Telnet, raw TCP, and serial connection histories remain separate. Legacy history records without an attributable connection are not automatically assigned to or sent with a current connection.

## Skills, RAG, and MCP

### RAG

Add operational documentation, runbooks, or incident evidence to the local knowledge base. GoSSH supports local persistence and can be configured to use Qdrant for vector storage. Results remain evidence for the Agent, not proof that a remote command has run.

### Skills

Skills are versioned Markdown task templates. A Skill can define parameters, allowed tools, workflow stages, report templates, integrity metadata, and an execution timeout. Tool allowlists never bypass command policy or approval requirements.

### MCP

GoSSH can connect to stdio and Streamable HTTP MCP servers. MCP tools are wrapped in the Agent approval flow. When an MCP server has target ACLs, the Agent must provide an allowed target ID. Because external tools can have arbitrary side effects, review their risk descriptions carefully.

## Data and Privacy

| Data | Location / protection |
| --- | --- |
| Connections, groups, settings | `~/.gossh/gossh.db` |
| Master key | `~/.gossh/master.key`, intended permission `0600` |
| SSH visible session logs | `~/.gossh/logs/YYYY-MM-DD/<session-id>.log` |
| Agent task checkpoints | Local SQLite checkpoint storage, with common secret fields redacted before persistence |
| Connection and AI secrets | AES-256-GCM encrypted through the local vault |

SSH session logs contain visible terminal text after ANSI and shell-integration metadata removal. They are not a complete command audit trail and do not preserve screen positioning or color state. Telnet, raw TCP, and serial sessions do not write SSH session logs.

## Terminal Customization

The terminal settings page includes color schemes, background configuration, cursor color/style/blink, fonts, scrollback, and case-insensitive output highlighting. Default highlighting covers common success, error, warning, running-state, IP-address, and numeric patterns; custom rules can be added in settings.

## Troubleshooting

### SSH host key prompt appears

Verify the fingerprint with the server owner. Do not accept a changed key solely to continue the connection.

### Telnet Agent command never completes

Telnet needs a recognizable device prompt after command output. Ensure the device returns a prompt such as `Router#`, `<HUAWEI>`, or a configured equivalent, and disable interactive paging when possible.

### An Agent command runs for a long time

The Agent waits for terminal completion rather than sending a follow-up command. Use `Ctrl-C` directly in the terminal when you decide to stop the remote process. Do not rely on a short remote `timeout` wrapper for normal long-running commands such as broad `du` scans.

### SFTP is unavailable

SFTP requires an active SSH session and a server-side SFTP subsystem. Telnet, raw TCP, and serial connections cannot use it.

### AI returns a provider error

Check the provider URL, API key, model name, and API mode in Settings. For tool-driven Agent tasks, use a provider endpoint that supports tool calls through Chat Completions-compatible APIs.

## Repository Layout

```text
.
├── app.go                  # Wails application API and integration wiring
├── internal/
│   ├── agent/              # Task runtime, policy, approvals, checkpoints, reports
│   ├── ai/                 # Provider and Eino integration
│   ├── config/             # SQLite configuration store
│   ├── crypto/             # Local encryption vault
│   ├── mcp/                # MCP clients and ACL wrapper
│   ├── portforward/        # SSH forwarding
│   ├── rag/                # Knowledge retrieval and Qdrant support
│   ├── serial/             # Serial client
│   ├── sftp/               # SFTP client and transfers
│   ├── ssh/                # SSH sessions, host trust, proxies, logs
│   ├── skills/             # Markdown Skill registry
│   └── tcp/                # Telnet and raw TCP sessions
├── frontend/               # Svelte 5 UI and Wails bindings
├── docs/                   # Design, operations, and protocol documents
└── build/bin/              # Release artifacts
```

## Contributing

1. Create a focused branch.
2. Keep changes scoped to the module that owns the behavior.
3. Add a regression test for behavior changes in Go when practical.
4. Run the verification commands listed above.
5. For Windows releases, build with Wails without skipping the frontend build.

Do not commit local databases, private keys, API keys, terminal logs, generated release executables, or exported configurations containing secrets.

## Limitations

- The application is local-first and designed for one desktop user. It does not provide centralized RBAC, server-side auditing, or separated approvers.
- Agent safety classification reduces risk but cannot prove that every external command, MCP action, or remote system behavior is harmless.
- Raw TCP and serial protocols do not provide a general-purpose, reliable command-completion signal for Agent automation.
- Remote device prompts, pagers, vendor CLIs, and nonstandard shells can require operator intervention.

## License

No repository license file is currently included. Add an explicit license before distributing or accepting external contributions.

---

## 中文说明

GoSSH 是面向 Windows 单机运维人员的桌面工作台，整合 SSH、Telnet、Raw TCP、串口终端、SSH SFTP、端口转发、连接管理、知识库、MCP 和带审批流程的 AI Agent。

完整中文使用说明见 [用户使用指南.md](./用户使用指南.md)，设计与协议文档位于 [docs](./docs)。

### 快速开始

```bash
git clone <repository-url> gossh
cd gossh
go mod download
cd frontend && npm install && cd ..
wails dev
```

构建 Windows x64 可执行文件：

```bash
wails build -platform windows/amd64
```

### 核心约束

- SFTP 与端口转发仅支持 SSH。
- Telnet 的 AI 命令完成判定依赖设备返回可识别的命令提示符。
- Raw TCP 与串口可以交互使用，但不具备通用、可靠的 AI 命令完成判定。
- Agent 不读取终端屏幕截图；它仅使用终端工具返回的输出。
- 默认每条 Agent 命令需要审批。可在安全配置中开启“只读命令无需审批”，但写入、删除、未知命令和 Agent SFTP 读取仍需审批。
- 长时间远端命令由 Agent 等待完成，用户可在终端中按 `Ctrl-C` 中断；不要对正常耗时命令默认套用短时间 `timeout`。

### 归属

- Owner: muswood
- Email: mumu920@outlook.com

## English

The English sections above describe the supported protocols, local data model, Agent approval flow, build commands, and operational limitations. For Chinese setup and feature guidance, see [用户使用指南.md](./用户使用指南.md).
