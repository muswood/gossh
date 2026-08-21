// owner: muswood | Email: mumu920@outlook.com
package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/crypto/ssh/knownhosts"
	"gossh/internal/agent"
	"gossh/internal/ai"
	"gossh/internal/config"
	"gossh/internal/crypto"
	"gossh/internal/mcp"
	"gossh/internal/observability"
	"gossh/internal/openssh"
	pfmgr "gossh/internal/portforward"
	"gossh/internal/rag"
	"gossh/internal/serial"
	sftpmgr "gossh/internal/sftp"
	"gossh/internal/skills"
	sshmgr "gossh/internal/ssh"
	"gossh/internal/tcp"
)

type App struct {
	ctx               context.Context
	sshManager        *sshmgr.Manager
	serialClient      *serial.Client
	tcpManager        *tcp.Manager
	aiClient          *ai.AIClient
	agentRuntime      *agent.Runtime
	agentRuntimeMu    sync.Mutex
	store             *config.Store
	vault             *crypto.Vault
	forwards          map[string]*pfmgr.Manager
	forwardIDs        map[string]string
	sessionConnIDs    map[string]string
	forwardsMu        sync.Mutex
	forwardProfilesMu sync.Mutex
	sftpTransfers     map[string]context.CancelFunc
	sftpTransfersMu   sync.Mutex
	sftpClients       map[string]*sftpmgr.Client
	sftpClientsMu     sync.Mutex
	observability     *observability.Recorder
	ragStore          *rag.Store
	ragEnabled        bool
	keyboardAuthMu    sync.Mutex
	keyboardAuth      map[string]chan keyboardAuthResponse
	mcpMu             sync.Mutex
	mcpClients        map[string]mcpConnection
	mcpToolSets       map[string][]tool.BaseTool
	skillRegistry     *skills.Registry
}

type mcpConnection interface {
	Close() error
	ListTools(context.Context) ([]tool.BaseTool, error)
}

type keyboardAuthResponse struct {
	answers   []string
	cancelled bool
}

var (
	agentPrivateKeyPattern = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)
	agentSecretPattern     = regexp.MustCompile(`(?i)\b(password|passwd|token|api[_-]?key|secret|authorization)\s*[:=]\s*\S+`)
)

func NewApp() *App {
	registry, _ := skills.NewRegistry("")
	return &App{
		sshManager:     sshmgr.NewManager(),
		serialClient:   serial.NewClient(),
		tcpManager:     tcp.NewManager(),
		forwards:       make(map[string]*pfmgr.Manager),
		forwardIDs:     make(map[string]string),
		sessionConnIDs: make(map[string]string),
		sftpTransfers:  make(map[string]context.CancelFunc),
		sftpClients:    make(map[string]*sftpmgr.Client),
		observability:  observability.NewRecorder(200),
		keyboardAuth:   make(map[string]chan keyboardAuthResponse),
		mcpClients:     make(map[string]mcpConnection),
		mcpToolSets:    make(map[string][]tool.BaseTool),
		skillRegistry:  registry,
	}
}

func (a *App) ensureAgentRuntime() *agent.Runtime {
	a.agentRuntimeMu.Lock()
	defer a.agentRuntimeMu.Unlock()

	if a.agentRuntime != nil {
		a.agentRuntime.SetClient(a.aiClient)
		return a.agentRuntime
	}
	a.agentRuntime = agent.NewRuntime(a.aiClient, func(event agent.Event) {
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "agent:event", event)
		}
	}, agent.ToolSet{
		Terminal: func(ctx context.Context, sessionID, command string) agent.ToolResult {
			if a.isTCPSession(sessionID) {
				return runAgentTelnetTerminal(ctx, sessionID, command, a.tcpManager, nil)
			}
			return runAgentTerminal(ctx, sessionID, command, a.sshManager, nil)
		},
		TerminalStream: func(ctx context.Context, sessionID, command string, onChunk func(string)) agent.ToolResult {
			if a.isTCPSession(sessionID) {
				return runAgentTelnetTerminal(ctx, sessionID, command, a.tcpManager, onChunk)
			}
			return runAgentTerminal(ctx, sessionID, command, a.sshManager, onChunk)
		},
		TerminalCancel: func(ctx context.Context, sessionID string) error {
			if a.isTCPSession(sessionID) {
				return a.tcpManager.Write(sessionID, []byte{3})
			}
			return a.sshManager.Write(sessionID, []byte{3})
		},
		SSHSystemProbeDone: a.sshManager.HasSystemProbe,
		MarkSSHSystemProbe: a.sshManager.MarkSystemProbe,
		SFTPListDir: func(ctx context.Context, sessionID, path string) agent.ToolResult {
			started := time.Now()
			output, err := a.SFTPListDir(sessionID, path)
			return agentToolResult("sftp_list_dir", "", output, err, started, map[string]any{"path": path})
		},
		SFTPReadFile: func(ctx context.Context, sessionID, path string) agent.ToolResult {
			started := time.Now()
			output, err := a.SFTPReadFile(sessionID, path)
			return agentToolResult("sftp_read_file", "", sanitizeAgentOutput(output), err, started, map[string]any{"path": path})
		},
		SFTPReadFileRange: func(ctx context.Context, sessionID, path string, startLine, lineCount int) agent.ToolResult {
			started := time.Now()
			rangeResult, err := a.readSFTPFileRange(sessionID, path, startLine, lineCount)
			metadata := map[string]any{
				"path": path, "startLine": rangeResult.StartLine, "endLine": rangeResult.EndLine,
				"returnedLines": rangeResult.ReturnedLines, "nextStartLine": rangeResult.NextStartLine,
				"hasMore": rangeResult.HasMore,
			}
			content := sanitizeAgentOutput(rangeResult.Content)
			metadata["truncatedByOutputLimit"] = len(content) < len(rangeResult.Content)
			return agentToolResult("sftp_read_file", "", content, err, started, metadata)
		},
		RAGSearch: func(ctx context.Context, query string, limit int) agent.ToolResult {
			started := time.Now()
			if limit <= 0 {
				limit = 5
			}
			output, err := a.ragSearchWithLimit(query, limit)
			return agentToolResult("rag_search", "", output, err, started, map[string]any{"query": query, "limit": limit})
		},
		RAGSearchTarget: func(ctx context.Context, targetID, query string, limit int) agent.ToolResult {
			started := time.Now()
			if limit <= 0 {
				limit = 5
			}
			// RAG is shared today; preserve target identity in fan-out results
			// so deployments can add scope-aware retrieval without changing the Agent contract.
			output, err := a.ragSearchWithLimit(query, limit)
			result := agentToolResult("rag_search", "", output, err, started, map[string]any{"query": query, "limit": limit, "scope": "shared-rag"})
			result.TargetID = targetID
			return result
		},
		Diagnostics: func(ctx context.Context) agent.ToolResult {
			started := time.Now()
			output, err := a.LoadDiagnostics()
			return agentToolResult("gossh_diagnostics", "", output, err, started, nil)
		},
		LocalGoSSHConfig: func(ctx context.Context) agent.ToolResult {
			started := time.Now()
			output, err := a.readLocalGoSSHConfig()
			return agentToolResult("local_gossh_config", "", output, err, started, map[string]any{"scope": "~/.gossh", "secrets": "excluded"})
		},
		LocalSessionLog: func(ctx context.Context, date, sessionID string, offset int64, maxBytes int) agent.ToolResult {
			started := time.Now()
			output, metadata, err := readLocalSessionLog(date, sessionID, offset, maxBytes)
			return agentToolResult("local_session_log", "", output, err, started, metadata)
		},
		LocalDocumentRead: func(ctx context.Context, path string, offset int64, maxBytes int) agent.ToolResult {
			started := time.Now()
			output, metadata, err := readLocalDocument(path, offset, maxBytes)
			return agentToolResult("local_document_read", "", output, err, started, metadata)
		},
		WebSearch: func(ctx context.Context, query string, limit int) agent.ToolResult {
			started := time.Now()
			output, err := webSearch(ctx, query, limit)
			return agentToolResult("web_search", "", output, err, started, map[string]any{"query": query, "provider": "DuckDuckGo HTML"})
		},
		WebRead: func(ctx context.Context, targetURL string, maxBytes int) agent.ToolResult {
			started := time.Now()
			output, metadata, err := webRead(ctx, targetURL, maxBytes)
			return agentToolResult("web_read", "", output, err, started, metadata)
		},
	}, newAgentCheckpointStore())
	a.agentRuntime.SetTracer(observability.NewTracer(a.observability))
	return a.agentRuntime
}

func agentCheckpointPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "gossh-agent.db")
	}
	return filepath.Join(home, ".gossh", "gossh.db")
}

func legacyAgentCheckpointPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "gossh-agent-checkpoints.json")
	}
	return filepath.Join(home, ".gossh", "agent-checkpoints.json")
}

func newAgentCheckpointStore() agent.CheckpointStore {
	store, err := agent.NewSQLiteCheckpointStore(agentCheckpointPath())
	if err != nil {
		fmt.Printf("初始化 Agent SQLite checkpoint 失败，回退到 JSON: %v\n", err)
		return agent.NewFileCheckpointStore(legacyAgentCheckpointPath())
	}
	if tasks, listErr := store.ListTasks(""); listErr == nil && len(tasks) == 0 {
		if _, statErr := os.Stat(legacyAgentCheckpointPath()); statErr == nil {
			if importErr := store.ImportFileCheckpoint(legacyAgentCheckpointPath()); importErr != nil {
				fmt.Printf("导入旧 Agent checkpoint 失败: %v\n", importErr)
			}
		}
	}
	return store
}

func runAgentTerminal(ctx context.Context, sessionID, command string, manager *sshmgr.Manager, onChunk func(string)) agent.ToolResult {
	started := time.Now()
	if err := ctx.Err(); err != nil {
		return agent.ToolResult{ToolName: "terminal_command", Command: command, ExitCode: -1,
			DurationMillis: time.Since(started).Milliseconds(), Error: err.Error(), ErrorKind: "cancelled", Status: "error", Cancelled: true, Redacted: true}
	}
	interactive := manager.ExecuteInteractiveContext(ctx, sessionID, command, func(chunk []byte) {
		if onChunk != nil {
			onChunk(sanitizeAgentOutput(string(chunk)))
		}
	})
	status := interactive.Status
	if status == "completed" {
		status = "ok"
	}
	result := agent.ToolResult{ToolName: "terminal_command", Command: command, Output: sanitizeAgentOutput(interactive.Output), ExitCode: interactive.ExitCode,
		DurationMillis: time.Since(started).Milliseconds(), Redacted: true, Status: interactive.Status,
		Metadata: map[string]any{"execution": "interactive_terminal", "completion": interactive.Completion, "prompt": interactive.Prompt}}
	result.Status = status
	if interactive.Status != "completed" {
		result.ExitCode = -1
		result.Error = interactive.Error
		result.ErrorKind = "execution"
		if interactive.Status == "cancelled" {
			result.ErrorKind, result.Cancelled = "cancelled", true
			result.Error = ctx.Err().Error()
		}
		if interactive.Status == "timed_out" {
			result.ErrorKind, result.Error = "timeout", "等待终端提示符超时"
		}
	}
	if onChunk != nil {
		if result.Output == "" {
			onChunk("命令已发送到当前终端；等待提示符完成。")
		}
	}
	return result
}

func runAgentTelnetTerminal(ctx context.Context, sessionID, command string, manager *tcp.Manager, onChunk func(string)) agent.ToolResult {
	started := time.Now()
	if err := ctx.Err(); err != nil {
		return agent.ToolResult{ToolName: "terminal_command", Command: command, ExitCode: -1, DurationMillis: time.Since(started).Milliseconds(), Error: err.Error(), ErrorKind: "cancelled", Status: "error", Cancelled: true, Redacted: true}
	}
	interactive := manager.ExecuteCommand(ctx, sessionID, command)
	status := interactive.Status
	if status == "completed" {
		status = "ok"
	}
	result := agent.ToolResult{ToolName: "terminal_command", Command: command, Output: sanitizeAgentOutput(interactive.Output), ExitCode: 0, DurationMillis: time.Since(started).Milliseconds(), Redacted: true, Status: status, Metadata: map[string]any{"execution": "interactive_telnet", "completion": "prompt", "prompt": interactive.Prompt, "pagerDetected": interactive.PagerDetected}}
	if interactive.Status != "completed" {
		result.ExitCode = -1
		result.Error = interactive.Error
		result.ErrorKind = "execution"
		result.Cancelled = interactive.Status == "cancelled"
	}
	if onChunk != nil {
		if result.Output != "" {
			onChunk(result.Output)
		} else {
			onChunk("命令已发送到当前 Telnet 终端；等待提示符完成。")
		}
	}
	return result
}

func (a *App) isTCPSession(sessionID string) bool {
	return a.tcpManager.HasSession(sessionID)
}

func agentToolResult(toolName, command, output string, err error, started time.Time, metadata map[string]any) agent.ToolResult {
	result := agent.ToolResult{
		ToolName: toolName, Command: command, Output: output, ExitCode: 0,
		DurationMillis: time.Since(started).Milliseconds(), Redacted: true, Metadata: metadata, Status: "ok",
	}
	if err != nil {
		result.ExitCode = -1
		result.Error = err.Error()
		result.Status = "error"
		result.ErrorKind = "execution"
	}
	return result
}

const localReadMaxBytes = 128 * 1024

func goSSHDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法确定本机用户目录: %w", err)
	}
	return filepath.Join(home, ".gossh"), nil
}

func (a *App) readLocalGoSSHConfig() (string, error) {
	if a.store == nil {
		return "{}", errors.New("GoSSH 配置存储未初始化")
	}
	connections, err := a.store.ListConnections()
	if err != nil {
		return "", err
	}
	// Keep only connection metadata. Credentials and private key material are
	// deliberately excluded before the result reaches the Agent.
	type safeConnection struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Protocol string `json:"protocol,omitempty"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Username string `json:"username"`
		GroupID  string `json:"groupId,omitempty"`
		Starred  bool   `json:"starred"`
	}
	safe := make([]safeConnection, 0, len(connections))
	for _, connection := range connections {
		safe = append(safe, safeConnection{ID: connection.ID, Name: connection.Name, Protocol: connection.Protocol, Host: connection.Host, Port: connection.Port, Username: connection.Username, GroupID: connection.GroupID, Starred: connection.Starred})
	}
	groups, err := a.store.ListGroups()
	if err != nil {
		return "", err
	}
	result := map[string]any{"dataDir": "~/.gossh", "connections": safe, "groups": groups, "ai": map[string]any{}}
	if aiConfig, configErr := a.store.LoadAIConfig(); configErr == nil && aiConfig != nil {
		result["ai"] = map[string]any{"provider": aiConfig.Provider, "model": aiConfig.Model, "baseURL": aiConfig.BaseURL, "apiMode": aiConfig.APIMode, "ragEnabled": aiConfig.RAGEnabled, "agentMaxSteps": aiConfig.AgentMaxSteps}
	}
	raw, err := json.Marshal(result)
	return sanitizeAgentOutput(string(raw)), err
}

func readLocalSessionLog(date, sessionID string, offset int64, maxBytes int) (string, map[string]any, error) {
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return "", nil, errors.New("日志日期格式必须为 YYYY-MM-DD")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9._-]+$`).MatchString(sessionID) {
		return "", nil, errors.New("sessionId 包含不安全字符")
	}
	maxBytes = normalizeLocalReadLimit(maxBytes)
	root, err := goSSHDataDir()
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(root, "logs", date, sessionID+".log")
	data, next, eof, err := readLocalFileRange(path, offset, maxBytes)
	metadata := map[string]any{"path": filepath.Join("~/.gossh/logs", date, sessionID+".log"), "offset": offset, "nextOffset": next, "eof": eof, "maxBytes": maxBytes}
	return sanitizeAgentOutput(string(data)), metadata, err
}

func readLocalDocument(path string, offset int64, maxBytes int) (string, map[string]any, error) {
	if !filepath.IsAbs(path) {
		return "", nil, errors.New("本地文档路径必须是绝对路径")
	}
	maxBytes = normalizeLocalReadLimit(maxBytes)
	data, next, eof, err := readLocalFileRange(filepath.Clean(path), offset, maxBytes)
	return sanitizeAgentOutput(string(data)), map[string]any{"path": filepath.Clean(path), "offset": offset, "nextOffset": next, "eof": eof, "maxBytes": maxBytes}, err
}

func normalizeLocalReadLimit(value int) int {
	if value <= 0 || value > localReadMaxBytes {
		return localReadMaxBytes
	}
	return value
}

func readLocalFileRange(path string, offset int64, maxBytes int) ([]byte, int64, bool, error) {
	if offset < 0 {
		return nil, 0, false, errors.New("文件偏移不能为负数")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, offset, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, offset, false, err
	}
	if info.IsDir() {
		return nil, offset, false, errors.New("指定路径是目录，不是文档")
	}
	if offset > info.Size() {
		offset = info.Size()
	}
	data := make([]byte, maxBytes)
	n, readErr := file.ReadAt(data, offset)
	if readErr != nil && readErr != io.EOF {
		return nil, offset, false, readErr
	}
	next := offset + int64(n)
	return data[:n], next, next >= info.Size(), nil
}

type webSearchResult struct {
	Title string `json:"title"`
	URL   string `json:"url"`
	Text  string `json:"text,omitempty"`
}

func webHTTPClient() *http.Client {
	redirects := 0
	return &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		redirects++
		if redirects > 5 {
			return errors.New("网页重定向次数超过 5 次")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return errors.New("网页重定向目标协议不受支持")
		}
		return nil
	}}
}

func webSearch(ctx context.Context, query string, limit int) (string, error) {
	if limit <= 0 || limit > 10 {
		return "", errors.New("搜索结果数必须在 1 到 10 之间")
	}
	endpoint := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "GoSSH/1.0")
	response, err := webHTTPClient().Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("搜索服务返回 HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 512*1024))
	if err != nil {
		return "", err
	}
	pattern := regexp.MustCompile(`(?s)<a[^>]*class="result__a"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	results := make([]webSearchResult, 0, limit)
	for _, match := range pattern.FindAllSubmatch(body, -1) {
		if len(results) >= limit {
			break
		}
		link, linkErr := url.QueryUnescape(html.UnescapeString(string(match[1])))
		if linkErr != nil {
			link = html.UnescapeString(string(match[1]))
		}
		parsed, parseErr := url.Parse(link)
		if parseErr == nil && parsed.Query().Get("uddg") != "" {
			link = parsed.Query().Get("uddg")
		}
		results = append(results, webSearchResult{Title: cleanHTMLText(string(match[2])), URL: link})
	}
	encoded, err := json.Marshal(map[string]any{"query": query, "results": results})
	return string(encoded), err
}

func webRead(ctx context.Context, targetURL string, maxBytes int) (string, map[string]any, error) {
	parsed, err := url.Parse(strings.TrimSpace(targetURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", nil, errors.New("网页 URL 只允许 http 或 https")
	}
	maxBytes = normalizeLocalReadLimit(maxBytes)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", nil, err
	}
	request.Header.Set("User-Agent", "GoSSH/1.0")
	response, err := webHTTPClient().Do(request)
	if err != nil {
		return "", nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", nil, fmt.Errorf("网页返回 HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, int64(maxBytes)))
	if err != nil {
		return "", nil, err
	}
	text := cleanHTMLText(string(body))
	return sanitizeAgentOutput(text), map[string]any{"url": response.Request.URL.String(), "status": response.StatusCode, "contentType": response.Header.Get("Content-Type"), "maxBytes": maxBytes, "truncated": len(body) >= maxBytes}, nil
}

func cleanHTMLText(value string) string {
	value = regexp.MustCompile(`(?s)<script[^>]*>.*?</script>|<style[^>]*>.*?</style>`).ReplaceAllString(value, " ")
	value = regexp.MustCompile(`(?s)<[^>]+>`).ReplaceAllString(value, " ")
	value = html.UnescapeString(value)
	return strings.Join(strings.Fields(value), " ")
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.sshManager.SetOnData(func(sessionID string, data []byte) {
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "ssh:output:"+sessionID, base64.StdEncoding.EncodeToString(data))
		}
	})
	a.tcpManager.SetOnData(func(sessionID string, data []byte) {
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "tcp:output:"+sessionID, base64.StdEncoding.EncodeToString(data))
		}
	})

	vault, err := crypto.NewVault()
	if err != nil {
		fmt.Printf("初始化加密保险箱失败: %v\n", err)
	} else {
		a.vault = vault
	}

	store, err := config.NewStore()
	if err != nil {
		fmt.Printf("打开配置存储失败: %v\n", err)
		return
	}
	a.store = store
	if err := a.loadSecurityConfig(); err != nil {
		fmt.Printf("加载安全配置失败，使用默认安全策略: %v\n", err)
	}
	if home, homeErr := os.UserHomeDir(); homeErr == nil {
		ragDBPath := filepath.Join(home, ".gossh", "rag.db")
		legacyRAGPath := filepath.Join(home, ".gossh", "rag.json")
		if sqliteStore, storeErr := rag.NewSQLiteStore(ragDBPath, nil); storeErr == nil {
			a.ragStore = sqliteStore
			if _, importErr := sqliteStore.ImportJSON(legacyRAGPath); importErr != nil {
				fmt.Printf("导入旧 RAG 索引失败: %v\n", importErr)
			}
		} else {
			fmt.Printf("初始化 SQLite RAG 存储失败，回退到 JSON: %v\n", storeErr)
			a.ragStore = rag.NewStore(legacyRAGPath)
		}
	}

	aiCfg, err := store.LoadAIConfig()
	if err != nil {
		fmt.Printf("加载 AI 配置失败: %v\n", err)
	}
	if aiCfg != nil {
		if a.vault != nil && aiCfg.APIKey != "" {
			k, e := a.vault.Decrypt(aiCfg.APIKey)
			if e != nil {
				fmt.Printf("解密 AI API Key 失败: %v\n", e)
				return
			}
			aiCfg.APIKey = k
		}
		a.aiClient = ai.NewClient(ai.Config{
			Provider:       ai.Provider(aiCfg.Provider),
			Model:          aiCfg.Model,
			EmbeddingModel: aiCfg.EmbeddingModel,
			APIKey:         aiCfg.APIKey,
			BaseURL:        aiCfg.BaseURL,
			APIMode:        aiCfg.APIMode,
			MaxTokens:      aiCfg.MaxTokens,
			Temperature:    aiCfg.Temperature,
		})
		a.ensureAgentRuntime().SetDefaultMaxSteps(aiCfg.AgentMaxSteps)
		if a.vault != nil && aiCfg.RAGVectorAPIKey != "" {
			key, decryptErr := a.vault.Decrypt(aiCfg.RAGVectorAPIKey)
			if decryptErr != nil {
				fmt.Printf("解密 RAG 向量库 API Key 失败: %v\n", decryptErr)
			} else {
				aiCfg.RAGVectorAPIKey = key
			}
		}
		a.configureRAGEmbedder(aiCfg.EmbeddingModel, aiCfg.BaseURL)
		if err := a.configureRAGBackend(*aiCfg); err != nil {
			fmt.Printf("初始化 RAG 向量后端失败，继续使用本地索引: %v\n", err)
		}
		a.ragEnabled = aiCfg.RAGEnabled
	}
}

func (a *App) loadSecurityConfig() error {
	if a.store == nil {
		return fmt.Errorf("配置存储未初始化")
	}
	raw, err := a.store.LoadSetting("security.config")
	if err != nil {
		return err
	}
	config := agent.DefaultSecurityConfig()
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &config); err != nil {
			return fmt.Errorf("安全配置格式无效: %w", err)
		}
	}
	agent.SetSecurityConfig(config)
	return nil
}

func (a *App) LoadSecurityConfig() (agent.SecurityConfig, error) {
	if err := a.loadSecurityConfig(); err != nil {
		return agent.GetSecurityConfig(), err
	}
	return agent.GetSecurityConfig(), nil
}

func (a *App) SaveSecurityConfig(config agent.SecurityConfig) error {
	if a.store == nil {
		return fmt.Errorf("配置存储未初始化")
	}
	agent.SetSecurityConfig(config)
	config = agent.GetSecurityConfig()
	raw, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("序列化安全配置失败: %w", err)
	}
	return a.store.SaveSetting("security.config", string(raw))
}

func (a *App) configureRAGEmbedder(model, baseURL string) {
	if a.ragStore == nil || a.aiClient == nil {
		return
	}
	if strings.TrimSpace(model) == "" {
		model = "text-embedding-3-small"
	}
	version := "openai-compatible:" + strings.TrimRight(baseURL, "/") + ":" + model
	a.ragStore.SetEmbedder(rag.FunctionEmbedder{
		VersionValue: version,
		Fn: func(ctx context.Context, value string) ([]float32, error) {
			vectors, err := a.aiClient.Embed(ctx, []string{value})
			if err != nil {
				return nil, err
			}
			if len(vectors) != 1 {
				return nil, fmt.Errorf("Embedding 返回数量无效: %d", len(vectors))
			}
			return vectors[0], nil
		},
		BatchFn: func(ctx context.Context, values []string) ([][]float32, error) {
			return a.aiClient.Embed(ctx, values)
		},
	})
}

func (a *App) configureRAGBackend(cfg config.AIProviderConfig) error {
	if a.ragStore == nil {
		return nil
	}
	backend := strings.ToLower(strings.TrimSpace(cfg.RAGVectorBackend))
	if backend == "" || backend == "sqlite" || backend == "local" {
		a.ragStore.SetVectorBackend(nil)
		return nil
	}
	if backend != "qdrant" {
		return fmt.Errorf("不支持的 RAG 向量后端: %s", cfg.RAGVectorBackend)
	}
	client, err := rag.NewQdrantBackend(rag.QdrantConfig{
		Endpoint: cfg.RAGVectorEndpoint, Collection: cfg.RAGVectorCollection, APIKey: cfg.RAGVectorAPIKey,
	})
	if err != nil {
		return err
	}
	a.ragStore.SetVectorBackend(client)
	return nil
}

func (a *App) shutdown(ctx context.Context) {
	a.agentRuntimeMu.Lock()
	agentRuntime := a.agentRuntime
	a.agentRuntimeMu.Unlock()
	if agentRuntime != nil {
		agentRuntime.Close()
	}
	a.mcpMu.Lock()
	for id, client := range a.mcpClients {
		_ = client.Close()
		delete(a.mcpClients, id)
		delete(a.mcpToolSets, id)
	}
	a.mcpMu.Unlock()
	a.keyboardAuthMu.Lock()
	for requestID, response := range a.keyboardAuth {
		response <- keyboardAuthResponse{cancelled: true}
		delete(a.keyboardAuth, requestID)
	}
	a.keyboardAuthMu.Unlock()
	a.closeAllSFTPClients()
	a.sshManager.DisconnectAll()
	a.tcpManager.DisconnectAll()
	a.forwardsMu.Lock()
	for _, pf := range a.forwards {
		pf.StopAll()
	}
	a.forwards = make(map[string]*pfmgr.Manager)
	a.forwardIDs = make(map[string]string)
	a.sessionConnIDs = make(map[string]string)
	a.forwardsMu.Unlock()
	a.sftpTransfersMu.Lock()
	for _, cancel := range a.sftpTransfers {
		cancel()
	}
	a.sftpTransfers = make(map[string]context.CancelFunc)
	a.sftpTransfersMu.Unlock()
	if a.store != nil {
		a.store.Close()
	}
	if a.ragStore != nil {
		_ = a.ragStore.Close()
	}
}

type ConnectRequest struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Host            string `json:"host"`
	Port            int    `json:"port"`
	Username        string `json:"username"`
	AuthMethod      string `json:"authMethod"`
	Password        string `json:"password"`
	PrivateKey      string `json:"privateKey"`
	PrivateKeyPath  string `json:"privateKeyPath"`
	CertificatePath string `json:"certificatePath"`
	Passphrase      string `json:"passphrase"`
	JumpHost        string `json:"jumpHost"`
	ProxyType       string `json:"proxyType"`
	ProxyHost       string `json:"proxyHost"`
	ProxyUsername   string `json:"proxyUsername"`
	ProxyPassword   string `json:"proxyPassword"`
	ProxyCommand    string `json:"proxyCommand"`
	Encoding        string `json:"encoding"`
	StartupCmd      string `json:"startupCmd"`
	KeepAlive       int    `json:"keepAliveSeconds"`
	Cols            uint32 `json:"cols"`
	Rows            uint32 `json:"rows"`
}

func (a *App) SSHConnect(req ConnectRequest) (string, error) {
	started := time.Now()
	cfg := &sshmgr.ConnectionConfig{
		ID:                  req.ID,
		Name:                req.Name,
		Host:                req.Host,
		Port:                req.Port,
		Username:            req.Username,
		AuthMethod:          sshmgr.AuthMethod(req.AuthMethod),
		Password:            req.Password,
		PrivateKey:          req.PrivateKey,
		PrivateKeyPath:      req.PrivateKeyPath,
		CertificatePath:     req.CertificatePath,
		Passphrase:          req.Passphrase,
		JumpHost:            req.JumpHost,
		ProxyType:           req.ProxyType,
		ProxyHost:           req.ProxyHost,
		ProxyUsername:       req.ProxyUsername,
		ProxyPassword:       req.ProxyPassword,
		ProxyCommand:        req.ProxyCommand,
		Encoding:            req.Encoding,
		StartupCmd:          req.StartupCmd,
		KeepAlive:           req.KeepAlive,
		KeyboardInteractive: a.keyboardInteractiveCallback(),
	}
	sessionID, err := a.sshManager.Connect(cfg, req.Cols, req.Rows)
	if err != nil {
		a.recordObservation("ssh", "connect", "error", started, map[string]interface{}{
			"host": req.Host, "port": req.Port, "authMethod": req.AuthMethod,
		})
		return "", err
	}
	a.recordObservation("ssh", "connect", "ok", started, map[string]interface{}{
		"host": req.Host, "port": req.Port, "authMethod": req.AuthMethod,
	})
	a.rememberSessionConnection(sessionID, req.ID)
	a.restoreForwardRules(sessionID, req.ID)
	return sessionID, nil
}

// SSHConnectByID resolves credentials inside the backend so the UI never
// needs to keep passwords or private keys in its connection store.
func (a *App) SSHConnectByID(id string, cols, rows uint32) (string, error) {
	started := time.Now()
	cfg, err := a.connectionConfigByID(id)
	if err != nil {
		a.recordObservation("ssh", "connectByID", "error", started, map[string]interface{}{"connectionId": id})
		return "", err
	}
	cfg.KeyboardInteractive = a.keyboardInteractiveCallback()
	sessionID, err := a.sshManager.Connect(cfg, cols, rows)
	if err != nil {
		a.recordObservation("ssh", "connectByID", "error", started, map[string]interface{}{
			"connectionId": id, "host": cfg.Host, "port": cfg.Port, "authMethod": cfg.AuthMethod,
		})
		return "", err
	}
	a.recordObservation("ssh", "connectByID", "ok", started, map[string]interface{}{
		"connectionId": id, "host": cfg.Host, "port": cfg.Port, "authMethod": cfg.AuthMethod,
	})
	a.rememberSessionConnection(sessionID, cfg.ID)
	a.restoreForwardRules(sessionID, cfg.ID)
	return sessionID, nil
}

// SSHConnectByIDWithPassword uses a password supplied for this connection
// attempt only. It deliberately does not update the saved connection record.
func (a *App) SSHConnectByIDWithPassword(id, password string, cols, rows uint32) (string, error) {
	started := time.Now()
	cfg, err := a.connectionConfigByID(id)
	if err != nil {
		a.recordObservation("ssh", "connectByIDWithPassword", "error", started, map[string]interface{}{"connectionId": id})
		return "", err
	}
	if cfg.AuthMethod != sshmgr.AuthPassword {
		return "", fmt.Errorf("当前连接不是密码认证方式")
	}
	cfg.Password = password
	cfg.KeyboardInteractive = a.keyboardInteractiveCallback()
	sessionID, err := a.sshManager.Connect(cfg, cols, rows)
	if err != nil {
		a.recordObservation("ssh", "connectByIDWithPassword", "error", started, map[string]interface{}{
			"connectionId": id, "host": cfg.Host, "port": cfg.Port, "authMethod": cfg.AuthMethod,
		})
		return "", err
	}
	a.recordObservation("ssh", "connectByIDWithPassword", "ok", started, map[string]interface{}{
		"connectionId": id, "host": cfg.Host, "port": cfg.Port, "authMethod": cfg.AuthMethod,
	})
	a.rememberSessionConnection(sessionID, cfg.ID)
	a.restoreForwardRules(sessionID, cfg.ID)
	return sessionID, nil
}

func (a *App) keyboardInteractiveCallback() sshmgr.KeyboardInteractiveCallback {
	return func(user, instruction string, questions []string, echos []bool) ([]string, error) {
		if a.ctx == nil {
			return nil, fmt.Errorf("SSH 交互认证不可用")
		}
		if len(questions) == 0 || len(questions) > 16 || len(echos) != len(questions) {
			return nil, fmt.Errorf("SSH 交互认证请求无效")
		}
		requestID := fmt.Sprintf("ssh-auth-%d", time.Now().UnixNano())
		response := make(chan keyboardAuthResponse, 1)
		a.keyboardAuthMu.Lock()
		if a.keyboardAuth == nil {
			a.keyboardAuth = make(map[string]chan keyboardAuthResponse)
		}
		a.keyboardAuth[requestID] = response
		a.keyboardAuthMu.Unlock()
		defer func() {
			a.keyboardAuthMu.Lock()
			delete(a.keyboardAuth, requestID)
			a.keyboardAuthMu.Unlock()
		}()

		runtime.EventsEmit(a.ctx, "ssh:keyboard-interactive", map[string]interface{}{
			"requestId":   requestID,
			"user":        user,
			"instruction": instruction,
			"questions":   questions,
			"echos":       echos,
		})

		select {
		case result := <-response:
			if result.cancelled {
				return nil, fmt.Errorf("SSH 交互认证已取消")
			}
			if len(result.answers) != len(questions) {
				return nil, fmt.Errorf("SSH 交互认证答案数量不匹配")
			}
			return result.answers, nil
		case <-time.After(2 * time.Minute):
			return nil, fmt.Errorf("SSH 交互认证超时")
		}
	}
}

// SSHKeyboardInteractiveResponse completes one pending MFA prompt.
func (a *App) SSHKeyboardInteractiveResponse(requestID string, answers []string, cancelled bool) error {
	if requestID == "" || len(answers) > 16 {
		return fmt.Errorf("SSH 交互认证请求无效")
	}
	for _, answer := range answers {
		if len(answer) > 1024 {
			return fmt.Errorf("SSH 交互认证答案过长")
		}
	}
	a.keyboardAuthMu.Lock()
	response, ok := a.keyboardAuth[requestID]
	a.keyboardAuthMu.Unlock()
	if !ok {
		return fmt.Errorf("SSH 交互认证请求不存在或已过期")
	}
	select {
	case response <- keyboardAuthResponse{answers: answers, cancelled: cancelled}:
		return nil
	default:
		return fmt.Errorf("SSH 交互认证请求已完成")
	}
}

func (a *App) SSHTrustHostKey(id, fingerprint string) error {
	cfg, err := a.connectionConfigByID(id)
	if err != nil {
		return err
	}
	return a.sshManager.TrustHostKey(cfg, fingerprint)
}

func (a *App) connectionConfigByID(id string) (*sshmgr.ConnectionConfig, error) {
	if a.store == nil {
		return nil, fmt.Errorf("配置存储未初始化")
	}
	conn, err := a.store.FindConnection(id)
	if err != nil {
		return nil, err
	}
	if conn == nil {
		return nil, fmt.Errorf("连接 %s 不存在", id)
	}
	decrypt := func(value string) (string, error) {
		if value == "" || a.vault == nil {
			return value, nil
		}
		return a.vault.Decrypt(value)
	}
	if conn.Password, err = decrypt(conn.Password); err != nil {
		return nil, err
	}
	if conn.PrivateKey, err = decrypt(conn.PrivateKey); err != nil {
		return nil, err
	}
	if conn.Passphrase, err = decrypt(conn.Passphrase); err != nil {
		return nil, err
	}
	if conn.ProxyPassword, err = decrypt(conn.ProxyPassword); err != nil {
		return nil, err
	}
	return &sshmgr.ConnectionConfig{
		ID: conn.ID, Name: conn.Name, Host: conn.Host, Port: conn.Port,
		Username: conn.Username, AuthMethod: sshmgr.AuthMethod(conn.AuthMethod),
		Password: conn.Password, PrivateKey: conn.PrivateKey, PrivateKeyPath: conn.PrivateKeyPath, CertificatePath: conn.CertificatePath, Passphrase: conn.Passphrase,
		JumpHost: conn.JumpHost, ProxyType: conn.ProxyType, ProxyHost: conn.ProxyHost,
		ProxyUsername: conn.ProxyUsername, ProxyPassword: conn.ProxyPassword, ProxyCommand: conn.ProxyCommand,
		Encoding: conn.Encoding, StartupCmd: conn.StartupCmd,
		KeepAlive: conn.KeepAlive,
	}, nil
}

func (a *App) SSHWrite(sessionID string, data string) error {
	return a.sshManager.Write(sessionID, []byte(data))
}

// SSHWriteBase64 keeps binary terminal protocols outside UTF-8 conversion.
func (a *App) SSHWriteBase64(sessionID, encoded string) error {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("SSH 二进制数据无效: %w", err)
	}
	return a.sshManager.Write(sessionID, data)
}

func decodeSessionLogBytes(encoded string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("终端日志字节无效: %w", err)
	}
	return data, nil
}

// SSHAppendSessionLogBase64 appends bytes already identified by the open
// frontend terminal as visible output. The bytes are not decoded as text.
func (a *App) SSHAppendSessionLogBase64(sessionID, encoded string) error {
	data, err := decodeSessionLogBytes(encoded)
	if err != nil {
		return err
	}
	return a.sshManager.AppendSessionLog(sessionID, data)
}

type sessionLogReadResult struct {
	Data       string `json:"data"`
	NextOffset int64  `json:"nextOffset"`
	EOF        bool   `json:"eof"`
}

func (a *App) SSHReadSessionLog(sessionID string, offset int64, maxBytes int) (sessionLogReadResult, error) {
	data, nextOffset, eof, err := a.sshManager.ReadSessionLog(sessionID, offset, maxBytes)
	if err != nil {
		return sessionLogReadResult{}, err
	}
	return sessionLogReadResult{Data: base64.StdEncoding.EncodeToString(data), NextOffset: nextOffset, EOF: eof}, nil
}

func (a *App) SSHRead(sessionID string) (string, error) {
	b, err := a.sshManager.Read(sessionID)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

func (a *App) SSHResize(sessionID string, cols, rows uint32) error {
	return a.sshManager.Resize(sessionID, cols, rows)
}

func (a *App) SSHDisconnect(sessionID string) error {
	a.closeSFTPClient(sessionID)
	a.forwardsMu.Lock()
	pf := a.forwards[sessionID]
	delete(a.forwards, sessionID)
	delete(a.sessionConnIDs, sessionID)
	for id, mappedSession := range a.forwardIDs {
		if mappedSession == sessionID {
			delete(a.forwardIDs, id)
		}
	}
	a.forwardsMu.Unlock()
	if pf != nil {
		pf.StopAll()
	}
	return a.sshManager.Disconnect(sessionID)
}

func (a *App) SSHExecute(sessionID, command string) (string, error) {
	return a.sshManager.Execute(sessionID, command)
}

func (a *App) SSHListSessions() string {
	return a.sshManager.ListSessions()
}

type TCPConnectRequest struct {
	ID       string `json:"id"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
}

func (a *App) TCPConnect(req TCPConnectRequest) (string, error) {
	return a.tcpManager.Connect(req.ID, req.Host, req.Port, tcp.Protocol(req.Protocol))
}
func (a *App) TCPWrite(sessionID, data string) error {
	return a.tcpManager.Write(sessionID, []byte(data))
}
func (a *App) TCPWriteBase64(sessionID, encoded string) error {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("TCP 二进制数据无效: %w", err)
	}
	return a.tcpManager.Write(sessionID, data)
}
func (a *App) TCPRead(sessionID string) (string, error) {
	data, err := a.tcpManager.Read(sessionID)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}
func (a *App) TCPResize(sessionID string, cols, rows uint16) error {
	return a.tcpManager.SetSize(sessionID, cols, rows)
}
func (a *App) TCPDisconnect(sessionID string) error { return a.tcpManager.Disconnect(sessionID) }

func (a *App) SerialListPorts() ([]string, error) {
	return a.serialClient.ListPorts()
}

func (a *App) SerialConnect(cfg serial.Config) error {
	return a.serialClient.Connect(cfg)
}

func (a *App) SerialWrite(data string) (int, error) {
	return a.serialClient.Write([]byte(data))
}

func (a *App) SerialWriteBase64(encoded string) (int, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return 0, fmt.Errorf("串口二进制数据无效: %w", err)
	}
	return a.serialClient.Write(data)
}

func (a *App) SerialRead(size int) (string, error) {
	buf := make([]byte, size)
	n, err := a.serialClient.Read(buf)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf[:n]), nil
}

func (a *App) SerialDisconnect() error {
	return a.serialClient.Disconnect()
}

func (a *App) SerialIsConnected() bool {
	return a.serialClient.IsConnected()
}

type RAGDocumentRequest struct {
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Source  string   `json:"source,omitempty"`
	Tags    []string `json:"tags,omitempty"`
}

func (a *App) AgentStart(req agent.StartRequest) (string, error) {
	if a.aiClient == nil {
		return "", fmt.Errorf("AI 客户端未配置。请先到设置中配置 AI 提供商。")
	}
	if err := a.applySkill(&req); err != nil {
		return "", err
	}
	if protocol, ok := a.tcpManager.Protocol(req.SessionID); ok {
		req.Transport = string(protocol)
	} else if a.sshManager.HasSession(req.SessionID) {
		req.Transport = "ssh"
	} else {
		req.Transport = ""
	}
	runtime := a.ensureAgentRuntime()
	if a.store != nil {
		if cfg, err := a.store.LoadAIConfig(); err == nil && cfg != nil {
			runtime.SetDefaultMaxSteps(cfg.AgentMaxSteps)
		}
	}
	return runtime.Start(a.ctx, req)
}

func (a *App) AgentApprove(taskID, stepID string) error {
	runtime := a.ensureAgentRuntime()
	err := runtime.Approve(taskID, stepID, true)
	if !errors.Is(err, agent.ErrTaskNotRunning) {
		return err
	}
	task, getErr := runtime.Get(taskID)
	if getErr != nil {
		return err
	}
	if task.Status != agent.StatusInterrupted && task.Status != agent.StatusRunning && task.Status != agent.StatusWaitingApproval {
		return err
	}
	// Approval decisions are intentionally not replayed after process loss.
	// Resume rebuilds the runner and makes it issue a fresh approval card.
	if _, resumeErr := a.AgentResume(taskID); resumeErr != nil {
		return fmt.Errorf("Agent 审批通道已中断，自动恢复任务失败: %w", resumeErr)
	}
	return nil
}

func (a *App) AgentReject(taskID, stepID string) error {
	return a.ensureAgentRuntime().Approve(taskID, stepID, false)
}

func (a *App) AgentStop(taskID string) error {
	return a.ensureAgentRuntime().Stop(taskID)
}

// AssessAgentCommand exposes the same backend command policy used by Agent
// tools so frontend command suggestions cannot bypass the server-side rules.
func (a *App) AssessAgentCommand(command string) agent.PolicyDecision {
	return agent.AssessCommand(command)
}

func (a *App) AgentResume(taskID string) (string, error) {
	if a.aiClient == nil {
		return "", fmt.Errorf("AI 客户端未配置。请先到设置中配置 AI 提供商。")
	}
	runtime := a.ensureAgentRuntime()
	task, err := runtime.Get(taskID)
	if err != nil {
		return "", err
	}
	if err := a.validateSkillResume(task); err != nil {
		return "", err
	}
	return runtime.Resume(a.ctx, taskID)
}

type MCPServerRequest struct {
	ID               string   `json:"id"`
	Transport        string   `json:"transport,omitempty"`
	Endpoint         string   `json:"endpoint,omitempty"`
	Command          string   `json:"command"`
	Args             []string `json:"args,omitempty"`
	Env              []string `json:"env,omitempty"`
	AuthToken        string   `json:"authToken,omitempty"`
	OAuthAccessToken string   `json:"oauthAccessToken,omitempty"`
	AllowedTools     []string `json:"allowedTools,omitempty"`
	TargetIDs        []string `json:"targetIds,omitempty"`
}

type MCPServerConfig struct {
	ID            string   `json:"id"`
	Transport     string   `json:"transport,omitempty"`
	Endpoint      string   `json:"endpoint,omitempty"`
	Command       string   `json:"command"`
	Args          []string `json:"args,omitempty"`
	AllowedTools  []string `json:"allowedTools,omitempty"`
	TargetIDs     []string `json:"targetIds,omitempty"`
	Connected     bool     `json:"connected"`
	HasEnv        bool     `json:"hasEnv"`
	HasAuthToken  bool     `json:"hasAuthToken"`
	HasOAuthToken bool     `json:"hasOAuthToken"`
}

type persistedMCPServer struct {
	MCPServerConfig
	EnvCipher        string `json:"envCipher,omitempty"`
	AuthTokenCipher  string `json:"authTokenCipher,omitempty"`
	OAuthTokenCipher string `json:"oauthTokenCipher,omitempty"`
}

// AgentConnectMCP starts one explicitly configured stdio MCP server and
// exposes its tools to subsequently created Agent tasks. The model cannot
// choose the executable or its arguments.
func (a *App) AgentConnectMCP(req MCPServerRequest) (string, error) {
	if req.ID == "" {
		return "", fmt.Errorf("MCP server id 不能为空")
	}
	if req.Transport == "" {
		req.Transport = "stdio"
	}
	req.Transport = strings.ToLower(strings.TrimSpace(req.Transport))
	if req.Transport == "stdio" && req.Command == "" {
		return "", fmt.Errorf("stdio MCP server command 不能为空")
	}
	if req.Transport == "http" && req.Endpoint == "" {
		return "", fmt.Errorf("HTTP MCP server endpoint 不能为空")
	}
	if len(req.Env) == 0 {
		if saved, loadErr := a.loadMCPServers(); loadErr == nil {
			for _, server := range saved {
				if server.ID != req.ID || server.EnvCipher == "" || a.vault == nil {
					continue
				}
				encoded, decryptErr := a.vault.Decrypt(server.EnvCipher)
				if decryptErr != nil {
					return "", fmt.Errorf("读取 MCP 凭据失败: %w", decryptErr)
				}
				_ = json.Unmarshal([]byte(encoded), &req.Env)
				break
			}
		}
	}
	if req.AuthToken == "" {
		if saved, loadErr := a.loadMCPServers(); loadErr == nil {
			for _, server := range saved {
				if server.ID != req.ID || server.AuthTokenCipher == "" || a.vault == nil {
					continue
				}
				decoded, decryptErr := a.vault.Decrypt(server.AuthTokenCipher)
				if decryptErr != nil {
					return "", fmt.Errorf("读取 MCP 协议认证凭据失败: %w", decryptErr)
				}
				req.AuthToken = decoded
				break
			}
		}
	}
	if req.OAuthAccessToken == "" {
		if saved, loadErr := a.loadMCPServers(); loadErr == nil {
			for _, server := range saved {
				if server.ID != req.ID || server.OAuthTokenCipher == "" || a.vault == nil {
					continue
				}
				decoded, decryptErr := a.vault.Decrypt(server.OAuthTokenCipher)
				if decryptErr != nil {
					return "", fmt.Errorf("读取 MCP OAuth token 失败: %w", decryptErr)
				}
				req.OAuthAccessToken = decoded
				break
			}
		}
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	var client mcpConnection
	var err error
	if req.Transport == "http" {
		client, err = mcp.NewHTTPClient(ctx, mcp.HTTPConfig{Endpoint: req.Endpoint, OAuthAccessToken: req.OAuthAccessToken})
	} else {
		client, err = mcp.NewClient(ctx, mcp.Config{Command: req.Command, Args: req.Args, Env: req.Env, AuthToken: req.AuthToken})
	}
	if err != nil {
		return "", err
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		_ = client.Close()
		return "", err
	}
	tools = filterMCPTools(ctx, tools, req.AllowedTools, req.TargetIDs)
	if err := a.saveMCPServer(req); err != nil {
		_ = client.Close()
		return "", err
	}
	a.mcpMu.Lock()
	if a.mcpClients == nil {
		a.mcpClients = make(map[string]mcpConnection)
	}
	old := a.mcpClients[req.ID]
	a.mcpClients[req.ID] = client
	a.mcpToolSets[req.ID] = tools
	a.mcpMu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	a.rebuildMCPTools()
	names := make([]string, 0, len(tools))
	for _, candidate := range tools {
		if info, infoErr := candidate.Info(ctx); infoErr == nil && info != nil {
			names = append(names, info.Name)
		}
	}
	raw, _ := json.Marshal(map[string]any{"id": req.ID, "tools": names})
	return string(raw), nil
}

func (a *App) AgentDisconnectMCP(id string) error {
	a.mcpMu.Lock()
	client := a.mcpClients[id]
	delete(a.mcpClients, id)
	delete(a.mcpToolSets, id)
	a.mcpMu.Unlock()
	if client == nil {
		return fmt.Errorf("MCP server 不存在: %s", id)
	}
	a.rebuildMCPTools()
	return client.Close()
}

func filterMCPTools(ctx context.Context, tools []tool.BaseTool, allowed, targetIDs []string) []tool.BaseTool {
	if len(allowed) == 0 && len(targetIDs) == 0 {
		return tools
	}
	wanted := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		if strings.TrimSpace(name) != "" {
			wanted[strings.TrimSpace(name)] = true
		}
	}
	filtered := make([]tool.BaseTool, 0, len(tools))
	for _, candidate := range tools {
		info, err := candidate.Info(ctx)
		if err == nil && info != nil && (len(allowed) == 0 || wanted[info.Name]) {
			filtered = append(filtered, mcp.WrapTargetACL(candidate, targetIDs))
		}
	}
	return filtered
}

func (a *App) rebuildMCPTools() {
	a.mcpMu.Lock()
	combined := make([]tool.BaseTool, 0)
	for _, tools := range a.mcpToolSets {
		combined = append(combined, tools...)
	}
	a.mcpMu.Unlock()
	a.ensureAgentRuntime().SetMCPTools(combined)
}

func (a *App) loadMCPServers() ([]persistedMCPServer, error) {
	if a.store == nil {
		return nil, nil
	}
	raw, err := a.store.LoadSetting("mcp.servers")
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil, err
	}
	var servers []persistedMCPServer
	if err := json.Unmarshal([]byte(raw), &servers); err != nil {
		return nil, fmt.Errorf("读取 MCP 配置失败: %w", err)
	}
	return servers, nil
}

func (a *App) saveMCPServer(req MCPServerRequest) error {
	if a.store == nil {
		return fmt.Errorf("配置存储未初始化")
	}
	servers, err := a.loadMCPServers()
	if err != nil {
		return err
	}
	entry := persistedMCPServer{MCPServerConfig: MCPServerConfig{ID: req.ID, Transport: req.Transport, Endpoint: req.Endpoint, Command: req.Command, Args: append([]string(nil), req.Args...), AllowedTools: append([]string(nil), req.AllowedTools...), TargetIDs: append([]string(nil), req.TargetIDs...), Connected: true, HasEnv: len(req.Env) > 0, HasAuthToken: req.AuthToken != "", HasOAuthToken: req.OAuthAccessToken != ""}}
	if len(req.Env) > 0 {
		if a.vault == nil {
			return fmt.Errorf("MCP 环境变量需要加密保险箱")
		}
		raw, _ := json.Marshal(req.Env)
		entry.EnvCipher, err = a.vault.Encrypt(string(raw))
		if err != nil {
			return fmt.Errorf("保存 MCP 凭据失败: %w", err)
		}
	} else {
		for _, server := range servers {
			if server.ID == req.ID {
				entry.EnvCipher = server.EnvCipher
				entry.HasEnv = server.HasEnv
				break
			}
		}
	}
	if req.AuthToken != "" {
		if a.vault == nil {
			return fmt.Errorf("MCP 协议认证令牌需要加密保险箱")
		}
		entry.AuthTokenCipher, err = a.vault.Encrypt(req.AuthToken)
		if err != nil {
			return fmt.Errorf("保存 MCP 协议认证凭据失败: %w", err)
		}
	} else {
		for _, server := range servers {
			if server.ID == req.ID {
				entry.AuthTokenCipher = server.AuthTokenCipher
				entry.HasAuthToken = server.HasAuthToken
				break
			}
		}
	}
	if req.OAuthAccessToken != "" {
		if a.vault == nil {
			return fmt.Errorf("MCP OAuth token 需要加密保险箱")
		}
		entry.OAuthTokenCipher, err = a.vault.Encrypt(req.OAuthAccessToken)
		if err != nil {
			return fmt.Errorf("保存 MCP OAuth token 失败: %w", err)
		}
	} else {
		for _, server := range servers {
			if server.ID == req.ID {
				entry.OAuthTokenCipher = server.OAuthTokenCipher
				entry.HasOAuthToken = server.HasOAuthToken
				break
			}
		}
	}
	found := false
	for i := range servers {
		if servers[i].ID == req.ID {
			servers[i] = entry
			found = true
			break
		}
	}
	if !found {
		servers = append(servers, entry)
	}
	raw, _ := json.Marshal(servers)
	return a.store.SaveSetting("mcp.servers", string(raw))
}

func (a *App) AgentListMCPServers() (string, error) {
	servers, err := a.loadMCPServers()
	if err != nil {
		return "", err
	}
	a.mcpMu.Lock()
	connected := make(map[string]bool, len(a.mcpClients))
	for id := range a.mcpClients {
		connected[id] = true
	}
	a.mcpMu.Unlock()
	public := make([]MCPServerConfig, 0, len(servers))
	for _, server := range servers {
		server.Connected = connected[server.ID]
		public = append(public, server.MCPServerConfig)
	}
	raw, err := json.Marshal(public)
	return string(raw), err
}

func (a *App) AgentDeleteMCPServer(id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("MCP server id 不能为空")
	}
	if a.store == nil {
		return fmt.Errorf("配置存储未初始化")
	}
	_ = a.AgentDisconnectMCP(id)
	servers, err := a.loadMCPServers()
	if err != nil {
		return err
	}
	next := servers[:0]
	for _, server := range servers {
		if server.ID != id {
			next = append(next, server)
		}
	}
	raw, _ := json.Marshal(next)
	return a.store.SaveSetting("mcp.servers", string(raw))
}

func (a *App) AgentGetTask(taskID string) (*agent.Task, error) {
	task, err := a.ensureAgentRuntime().Get(taskID)
	if err != nil {
		return nil, err
	}
	return &task, nil
}

func (a *App) AgentListTasks(tabID string) (string, error) {
	tasks, err := a.ensureAgentRuntime().List(tabID)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(tasks)
	return string(raw), err
}

func sanitizeAgentOutput(output string) string {
	output = agentPrivateKeyPattern.ReplaceAllString(output, "[已脱敏私钥]")
	output = agentSecretPattern.ReplaceAllString(output, "$1=[已脱敏]")
	if len(output) > 16000 {
		return output[len(output)-16000:]
	}
	return output
}

func (a *App) LoadAIHistory() (string, error) {
	if a.store == nil {
		return "[]", nil
	}
	history, err := a.store.LoadSetting("ai.history")
	if err != nil || history == "" {
		return "[]", err
	}
	if len(history) > 1<<20 || !json.Valid([]byte(history)) {
		return "[]", nil
	}
	return history, nil
}

func (a *App) SaveAIHistory(history string) error {
	if a.store == nil {
		return fmt.Errorf("配置存储未初始化")
	}
	if len(history) > 1<<20 || !json.Valid([]byte(history)) {
		return fmt.Errorf("AI 历史记录格式无效或超过 1 MB")
	}
	var messages []struct {
		Role      string `json:"role"`
		Content   string `json:"content"`
		Timestamp int64  `json:"timestamp"`
	}
	if err := json.Unmarshal([]byte(history), &messages); err != nil || len(messages) > 100 {
		return fmt.Errorf("AI 历史记录格式无效")
	}
	return a.store.SaveSetting("ai.history", history)
}

func (a *App) SaveAIConfig(cfg config.AIProviderConfig) error {
	if a.store == nil {
		return fmt.Errorf("配置存储未初始化")
	}
	cfg.Provider = strings.TrimSpace(cfg.Provider)
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.EmbeddingModel = strings.TrimSpace(cfg.EmbeddingModel)
	cfg.RAGVectorBackend = strings.ToLower(strings.TrimSpace(cfg.RAGVectorBackend))
	cfg.RAGVectorEndpoint = strings.TrimRight(strings.TrimSpace(cfg.RAGVectorEndpoint), "/")
	cfg.RAGVectorCollection = strings.TrimSpace(cfg.RAGVectorCollection)
	if cfg.EmbeddingModel == "" {
		cfg.EmbeddingModel = "text-embedding-3-small"
	}
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.Provider == "" {
		return fmt.Errorf("AI 供应商不能为空")
	}
	if cfg.Model == "" {
		return fmt.Errorf("AI 模型不能为空")
	}
	switch ai.Provider(cfg.Provider) {
	case ai.ProviderDeepSeek, ai.ProviderOpenAI, ai.ProviderClaude, ai.ProviderQwen:
	default:
		if cfg.BaseURL == "" {
			return fmt.Errorf("自定义供应商必须填写 API 地址")
		}
	}
	if cfg.APIMode == "responses" && ai.Provider(cfg.Provider) != ai.ProviderOpenAI {
		cfg.APIMode = "chat"
	}
	if cfg.AgentMaxSteps == 0 {
		cfg.AgentMaxSteps = agent.DefaultMaxIterations
	}
	if cfg.AgentMaxSteps < 1 || cfg.AgentMaxSteps > agent.MaxAllowedIterations {
		return fmt.Errorf("Agent 最大迭代次数必须在 1 到 %d 之间", agent.MaxAllowedIterations)
	}
	plainAPIKey := cfg.APIKey
	plainRAGVectorAPIKey := cfg.RAGVectorAPIKey
	storedCfg := cfg
	existing, err := a.store.LoadAIConfig()
	if err != nil {
		return fmt.Errorf("读取已保存 AI 配置失败: %w", err)
	}
	if existing != nil && storedCfg.APIKey == "" {
		storedCfg.APIKey = existing.APIKey
		if a.vault != nil && existing.APIKey != "" {
			decrypted, err := a.vault.Decrypt(existing.APIKey)
			if err != nil {
				return fmt.Errorf("解密已保存 AI API Key 失败，请重新输入并保存: %w", err)
			}
			plainAPIKey = decrypted
		} else {
			plainAPIKey = existing.APIKey
		}
	}
	if existing != nil && storedCfg.RAGVectorAPIKey == "" {
		storedCfg.RAGVectorAPIKey = existing.RAGVectorAPIKey
		if a.vault != nil && existing.RAGVectorAPIKey != "" {
			decrypted, err := a.vault.Decrypt(existing.RAGVectorAPIKey)
			if err != nil {
				return fmt.Errorf("解密已保存 RAG 向量库 API Key 失败，请重新输入并保存: %w", err)
			}
			plainRAGVectorAPIKey = decrypted
		} else {
			plainRAGVectorAPIKey = existing.RAGVectorAPIKey
		}
	}
	if cfg.RAGVectorBackend != "" && cfg.RAGVectorBackend != "sqlite" && cfg.RAGVectorBackend != "local" {
		if cfg.RAGVectorBackend != "qdrant" {
			return fmt.Errorf("不支持的 RAG 向量后端: %s", cfg.RAGVectorBackend)
		}
		if cfg.RAGVectorEndpoint == "" || cfg.RAGVectorCollection == "" {
			return fmt.Errorf("Qdrant 需要 endpoint 和 collection")
		}
	}
	if strings.TrimSpace(plainAPIKey) == "" && requiresAPIKey(cfg.BaseURL) {
		return fmt.Errorf("AI API Key 未配置，请输入 API Key 后保存")
	}
	if a.vault != nil && storedCfg.APIKey != "" && !isEncryptedSecret(storedCfg.APIKey) {
		enc, err := a.vault.Encrypt(storedCfg.APIKey)
		if err != nil {
			return fmt.Errorf("加密 AI API Key 失败: %w", err)
		}
		storedCfg.APIKey = enc
	}
	if a.vault != nil && storedCfg.RAGVectorAPIKey != "" && !isEncryptedSecret(storedCfg.RAGVectorAPIKey) {
		enc, err := a.vault.Encrypt(storedCfg.RAGVectorAPIKey)
		if err != nil {
			return fmt.Errorf("加密 RAG 向量库 API Key 失败: %w", err)
		}
		storedCfg.RAGVectorAPIKey = enc
	}
	if err := a.store.SaveAIConfig(storedCfg); err != nil {
		return err
	}
	a.aiClient = ai.NewClient(ai.Config{
		Provider:       ai.Provider(cfg.Provider),
		Model:          cfg.Model,
		EmbeddingModel: cfg.EmbeddingModel,
		APIKey:         plainAPIKey,
		BaseURL:        cfg.BaseURL,
		APIMode:        cfg.APIMode,
		MaxTokens:      cfg.MaxTokens,
		Temperature:    cfg.Temperature,
	})
	a.configureRAGEmbedder(cfg.EmbeddingModel, cfg.BaseURL)
	cfg.RAGVectorAPIKey = plainRAGVectorAPIKey
	if err := a.configureRAGBackend(cfg); err != nil {
		return fmt.Errorf("配置 RAG 向量后端失败: %w", err)
	}
	runtime := a.ensureAgentRuntime()
	runtime.SetClient(a.aiClient)
	runtime.SetDefaultMaxSteps(cfg.AgentMaxSteps)
	a.ragEnabled = cfg.RAGEnabled
	return nil
}

func (a *App) LoadAIConfig() (*config.AIProviderConfig, error) {
	if a.store == nil {
		return nil, fmt.Errorf("配置存储未初始化")
	}
	cfg, err := a.store.LoadAIConfig()
	if err != nil || cfg == nil {
		return cfg, err
	}
	// Do not expose the encrypted or plaintext API key to the frontend.
	cfg.APIKey = ""
	cfg.RAGVectorAPIKey = ""
	return cfg, nil
}

func (a *App) ListConnections() (string, error) {
	if a.store == nil {
		return "[]", nil
	}
	conns, err := a.store.ListConnections()
	if err != nil {
		return "", err
	}
	for i := range conns {
		// Secrets are resolved only by SSHConnectByID.
		conns[i].Password = ""
		conns[i].PrivateKey = ""
		conns[i].Passphrase = ""
		conns[i].ProxyPassword = ""
	}
	b, _ := json.Marshal(conns)
	return string(b), nil
}

func (a *App) SaveConnection(conn config.ConnectionRecord) error {
	if a.store == nil {
		return fmt.Errorf("配置存储未初始化")
	}
	// ListConnections deliberately omits secrets. Preserve the stored values when
	// an edit submits empty secret fields, while still encrypting newly supplied values.
	if existing, err := a.store.FindConnection(conn.ID); err == nil && existing != nil {
		if conn.Password == "" {
			conn.Password = existing.Password
		}
		if conn.PrivateKey == "" {
			conn.PrivateKey = existing.PrivateKey
		}
		if conn.Passphrase == "" {
			conn.Passphrase = existing.Passphrase
		}
		if conn.ProxyPassword == "" {
			conn.ProxyPassword = existing.ProxyPassword
		}
	}
	if a.vault != nil {
		if conn.Password != "" && !isEncryptedSecret(conn.Password) {
			enc, err := a.vault.Encrypt(conn.Password)
			if err != nil {
				return fmt.Errorf("加密密码失败: %w", err)
			}
			conn.Password = enc
		}
		if conn.PrivateKey != "" && !isEncryptedSecret(conn.PrivateKey) {
			enc, err := a.vault.Encrypt(conn.PrivateKey)
			if err != nil {
				return fmt.Errorf("加密私钥失败: %w", err)
			}
			conn.PrivateKey = enc
		}
		if conn.Passphrase != "" && !isEncryptedSecret(conn.Passphrase) {
			enc, err := a.vault.Encrypt(conn.Passphrase)
			if err != nil {
				return fmt.Errorf("加密私钥口令失败: %w", err)
			}
			conn.Passphrase = enc
		}
		if conn.ProxyPassword != "" && !isEncryptedSecret(conn.ProxyPassword) {
			enc, err := a.vault.Encrypt(conn.ProxyPassword)
			if err != nil {
				return fmt.Errorf("加密代理口令失败: %w", err)
			}
			conn.ProxyPassword = enc
		}
	}
	return a.store.SaveConnection(conn)
}

func (a *App) SetConnectionTerminalTheme(connectionID, theme string) error {
	if a.store == nil {
		return fmt.Errorf("配置存储未初始化")
	}
	conn, err := a.store.FindConnection(connectionID)
	if err != nil {
		return err
	}
	if conn == nil {
		return fmt.Errorf("连接 %s 不存在", connectionID)
	}
	conn.TerminalTheme = strings.TrimSpace(theme)
	return a.SaveConnection(*conn)
}

func isEncryptedSecret(value string) bool {
	return len(value) > 3 && value[:3] == "v1:"
}

func requiresAPIKey(baseURL string) bool {
	if strings.TrimSpace(baseURL) == "" {
		return true
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return true
	}
	host := parsed.Hostname()
	return host != "localhost" && host != "127.0.0.1" && host != "::1"
}

func (a *App) DeleteConnection(id string) error {
	if a.store == nil {
		return fmt.Errorf("配置存储未初始化")
	}
	return a.store.DeleteConnection(id)
}

func (a *App) SetConnectionGroup(connectionID, groupID string) error {
	if a.store == nil {
		return fmt.Errorf("配置存储未初始化")
	}
	return a.store.SetConnectionGroup(connectionID, groupID)
}

func (a *App) ListGroups() (string, error) {
	if a.store == nil {
		return "[]", nil
	}
	groups, err := a.store.ListGroups()
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(groups)
	return string(b), nil
}

func (a *App) SaveGroup(g config.ConnectionGroup) error {
	if a.store == nil {
		return fmt.Errorf("配置存储未初始化")
	}
	return a.store.SaveGroup(g)
}

func (a *App) DeleteGroup(id string) error {
	if a.store == nil {
		return fmt.Errorf("配置存储未初始化")
	}
	return a.store.DeleteGroup(id)
}

func (a *App) ExportConfig() (string, error) {
	if a.store == nil {
		return "{}", nil
	}
	base, err := a.store.ExportAll()
	if err != nil {
		return "", err
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(base), &data); err != nil {
		return "", err
	}
	registry, err := a.ensureSkillRegistry()
	if err != nil {
		return "", err
	}
	userSkills, err := registry.ExportUser()
	if err != nil {
		return "", err
	}
	data["skills"] = userSkills
	raw, err := json.MarshalIndent(data, "", "  ")
	return string(raw), err
}

func (a *App) ImportConfig(jsonData string) error {
	if a.store == nil {
		return fmt.Errorf("配置存储未初始化")
	}
	var data struct {
		Groups      []config.ConnectionGroup  `json:"groups"`
		Connections []config.ConnectionRecord `json:"connections"`
		Skills      []skills.Manifest         `json:"skills"`
	}
	if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
		return fmt.Errorf("配置格式无效: %w", err)
	}
	if a.vault != nil {
		for i := range data.Connections {
			if err := a.encryptImportedSecret(&data.Connections[i].Password, "密码"); err != nil {
				return err
			}
			if err := a.encryptImportedSecret(&data.Connections[i].PrivateKey, "私钥"); err != nil {
				return err
			}
			if err := a.encryptImportedSecret(&data.Connections[i].Passphrase, "私钥口令"); err != nil {
				return err
			}
		}
	}
	prepared, err := json.Marshal(struct {
		Groups      []config.ConnectionGroup  `json:"groups"`
		Connections []config.ConnectionRecord `json:"connections"`
	}{Groups: data.Groups, Connections: data.Connections})
	if err != nil {
		return err
	}
	if err := a.store.ImportAll(string(prepared)); err != nil {
		return err
	}
	if len(data.Skills) == 0 {
		return nil
	}
	registry, err := a.ensureSkillRegistry()
	if err != nil {
		return err
	}
	return registry.ImportUser(data.Skills)
}

func (a *App) encryptImportedSecret(value *string, label string) error {
	if *value == "" || isEncryptedSecret(*value) {
		return nil
	}
	enc, err := a.vault.Encrypt(*value)
	if err != nil {
		return fmt.Errorf("加密导入的%s失败: %w", label, err)
	}
	*value = enc
	return nil
}

func (a *App) ExportConfigToFile() error {
	if a.store == nil || a.ctx == nil {
		return fmt.Errorf("配置存储未初始化")
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title: "导出 GoSSH 配置", DefaultFilename: "gossh-config.json",
		Filters: []runtime.FileFilter{{DisplayName: "JSON 配置", Pattern: "*.json"}},
	})
	if err != nil || path == "" {
		return err
	}
	data, err := a.ExportConfig()
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(data), 0600)
}

func (a *App) ImportConfigFromFile() error {
	if a.store == nil || a.ctx == nil {
		return fmt.Errorf("配置存储未初始化")
	}
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   "导入 GoSSH 配置",
		Filters: []runtime.FileFilter{{DisplayName: "JSON 配置", Pattern: "*.json"}},
	})
	if err != nil || path == "" {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) > 4<<20 {
		return fmt.Errorf("配置文件超过 4 MB")
	}
	return a.ImportConfig(string(data))
}

// ImportOpenSSHConfigFromFile imports concrete Host entries from an OpenSSH
// config file. Wildcard-only entries are intentionally not imported.
func (a *App) ImportOpenSSHConfigFromFile() (string, error) {
	if a.store == nil || a.ctx == nil {
		return "", fmt.Errorf("配置存储未初始化")
	}
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   "导入 OpenSSH 配置",
		Filters: []runtime.FileFilter{{DisplayName: "OpenSSH 配置", Pattern: "config;*.conf;*"}},
	})
	if err != nil || path == "" {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) > 2<<20 {
		return "", fmt.Errorf("SSH 配置文件超过 2 MB")
	}
	profiles, err := openssh.ParseFile(path)
	if err != nil {
		return "", fmt.Errorf("解析 OpenSSH 配置失败: %w", err)
	}
	if len(profiles) == 0 {
		return "", fmt.Errorf("未找到可导入的 Host 条目")
	}
	username := ""
	if currentUser, userErr := user.Current(); userErr == nil {
		username = currentUser.Username
	}
	if err := a.store.SaveGroup(config.ConnectionGroup{ID: "openssh", Name: "OpenSSH 导入"}); err != nil {
		return "", err
	}
	imported := 0
	for _, profile := range profiles {
		if profile.Alias == "" || profile.HostName == "" || strings.Contains(profile.HostName, "%") {
			continue
		}
		authMethod := string(sshmgr.AuthSSHAgent)
		if profile.IdentityFile != "" {
			authMethod = string(sshmgr.AuthPrivateKey)
		}
		if profile.UseAgent && profile.IdentityFile == "" {
			authMethod = string(sshmgr.AuthSSHAgent)
		}
		proxyType := ""
		if profile.ProxyCommand != "" {
			proxyType = "command"
		}
		if err := a.SaveConnection(config.ConnectionRecord{
			ID: fmt.Sprintf("openssh-%s", safeConnectionID(profile.Alias)), Name: profile.Alias,
			Host: profile.HostName, Port: profile.Port, Username: firstNonEmpty(profile.User, username),
			AuthMethod: authMethod, PrivateKeyPath: profile.IdentityFile, CertificatePath: profile.CertificateFile,
			JumpHost: profile.ProxyJump, ProxyType: proxyType, ProxyCommand: profile.ProxyCommand,
			Encoding: "utf-8", StartupCmd: profile.RemoteCommand, KeepAlive: profile.ServerAliveInterval,
			GroupID: "openssh",
		}); err != nil {
			return "", fmt.Errorf("保存 Host %s 失败: %w", profile.Alias, err)
		}
		connectionID := fmt.Sprintf("openssh-%s", safeConnectionID(profile.Alias))
		for _, forward := range profile.Forwards {
			if err := a.saveForwardProfile(connectionID, portForwardProfile{
				ID: forward.ID, Type: forward.Type,
				LocalHost: forward.LocalHost, LocalPort: forward.LocalPort,
				RemoteHost: forward.RemoteHost, RemotePort: forward.RemotePort,
			}); err != nil {
				return "", fmt.Errorf("保存 Host %s 的端口转发失败: %w", profile.Alias, err)
			}
		}
		imported++
	}
	if imported == 0 {
		return "", fmt.Errorf("没有可保存的 Host 条目")
	}
	return fmt.Sprintf("已导入 %d 个 OpenSSH Host", imported), nil
}

// ImportKnownHostsFromFile merges a selected known_hosts file into the
// standard OpenSSH trust store without overwriting existing host keys.
func (a *App) ImportKnownHostsFromFile() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("应用未初始化")
	}
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "导入 known_hosts", Filters: []runtime.FileFilter{{DisplayName: "known_hosts", Pattern: "known_hosts;*.hosts;*"}},
	})
	if err != nil || path == "" {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) > 2<<20 {
		return "", fmt.Errorf("known_hosts 文件超过 2 MB")
	}
	if _, err := knownhosts.New(path); err != nil {
		return "", fmt.Errorf("known_hosts 格式无效: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	target := filepath.Join(home, ".ssh", "known_hosts")
	if filepath.Clean(path) == filepath.Clean(target) {
		return "已使用当前 known_hosts 文件", nil
	}
	existing, err := os.ReadFile(target)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	merged, added := mergeKnownHostLines(string(existing), string(data))
	if added == 0 {
		return "没有新的主机密钥", nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return "", err
	}
	if err := os.WriteFile(target, []byte(merged), 0600); err != nil {
		return "", err
	}
	return fmt.Sprintf("已导入 %d 条主机密钥", added), nil
}

func mergeKnownHostLines(existing, incoming string) (string, int) {
	seen := make(map[string]struct{})
	lines := make([]string, 0)
	appendLines := func(content string, add bool) int {
		added := 0
		scanner := bufio.NewScanner(strings.NewReader(content))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if _, exists := seen[line]; exists {
				continue
			}
			seen[line] = struct{}{}
			lines = append(lines, line)
			if add {
				added++
			}
		}
		return added
	}
	appendLines(existing, false)
	added := appendLines(incoming, true)
	return strings.Join(lines, "\n") + "\n", added
}

func safeConnectionID(value string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(value) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			builder.WriteRune(r)
		} else {
			builder.WriteByte('-')
		}
	}
	return strings.Trim(builder.String(), "-")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return "root"
}

func (a *App) SFTPListDir(sessionID string, path string) (string, error) {
	sc, err := a.sftpClient(sessionID)
	if err != nil {
		return "", err
	}
	files, err := sc.ListDir(path)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(files)
	return string(b), nil
}

func (a *App) SFTPExtensions(sessionID string) (string, error) {
	sc, err := a.sftpClient(sessionID)
	if err != nil {
		return "", err
	}
	extensions, err := sc.Extensions()
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(extensions)
	return string(b), nil
}

func (a *App) SFTPDiskUsage(sessionID string, path string) (string, error) {
	sc, err := a.sftpClient(sessionID)
	if err != nil {
		return "", err
	}
	usage, err := sc.DiskUsage(path)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(usage)
	return string(b), nil
}

func (a *App) SFTPRealPath(sessionID string, path string) (string, error) {
	sc, err := a.sftpClient(sessionID)
	if err != nil {
		return "", err
	}
	return sc.RealPath(path)
}

func (a *App) SFTPUpload(sessionID string, localPath string, remotePath string) error {
	started := time.Now()
	sc, err := a.sftpClient(sessionID)
	if err != nil {
		a.recordObservation("sftp", "upload", "error", started, map[string]interface{}{"sessionId": sessionID})
		return err
	}
	transferID := fmt.Sprintf("upload-%d", time.Now().UnixNano())
	transferCtx, cancel := context.WithCancel(context.Background())
	a.sftpTransfersMu.Lock()
	a.sftpTransfers[transferID] = cancel
	a.sftpTransfersMu.Unlock()
	defer func() {
		cancel()
		a.sftpTransfersMu.Lock()
		delete(a.sftpTransfers, transferID)
		a.sftpTransfersMu.Unlock()
	}()
	progress := make(chan sftpmgr.TransferProgress, 8)
	done := make(chan error, 1)
	go func() {
		err := sc.UploadContext(transferCtx, localPath, remotePath, progress)
		close(progress)
		done <- err
	}()
	for update := range progress {
		update.ID = transferID
		a.emitSFTPProgress(update)
	}
	err = <-done
	final := sftpmgr.TransferProgress{ID: transferID, Type: "upload", FileName: localPath, Status: "completed", Percent: 100, Verified: err == nil}
	if err != nil {
		final.Status = "failed"
		if errors.Is(err, context.Canceled) {
			final.Status = "cancelled"
		}
	}
	status := "ok"
	if err != nil {
		status = final.Status
	}
	a.recordObservation("sftp", "upload", status, started, map[string]interface{}{
		"sessionId": sessionID, "localPath": localPath, "remotePath": remotePath,
	})
	a.emitSFTPProgress(final)
	return err
}

func (a *App) SFTPDownload(sessionID string, remotePath string, localPath string) error {
	started := time.Now()
	sc, err := a.sftpClient(sessionID)
	if err != nil {
		a.recordObservation("sftp", "download", "error", started, map[string]interface{}{"sessionId": sessionID})
		return err
	}
	transferID := fmt.Sprintf("download-%d", time.Now().UnixNano())
	transferCtx, cancel := context.WithCancel(context.Background())
	a.sftpTransfersMu.Lock()
	a.sftpTransfers[transferID] = cancel
	a.sftpTransfersMu.Unlock()
	defer func() {
		cancel()
		a.sftpTransfersMu.Lock()
		delete(a.sftpTransfers, transferID)
		a.sftpTransfersMu.Unlock()
	}()
	progress := make(chan sftpmgr.TransferProgress, 8)
	done := make(chan error, 1)
	go func() {
		err := sc.DownloadContext(transferCtx, remotePath, localPath, progress)
		close(progress)
		done <- err
	}()
	for update := range progress {
		update.ID = transferID
		a.emitSFTPProgress(update)
	}
	err = <-done
	final := sftpmgr.TransferProgress{ID: transferID, Type: "download", FileName: remotePath, Status: "completed", Percent: 100, Verified: err == nil}
	if err != nil {
		final.Status = "failed"
		if errors.Is(err, context.Canceled) {
			final.Status = "cancelled"
		}
	}
	status := "ok"
	if err != nil {
		status = final.Status
	}
	a.recordObservation("sftp", "download", status, started, map[string]interface{}{
		"sessionId": sessionID, "remotePath": remotePath, "localPath": localPath,
	})
	a.emitSFTPProgress(final)
	return err
}

func (a *App) emitSFTPProgress(progress sftpmgr.TransferProgress) {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "sftp:progress", progress)
	}
}

func (a *App) recordObservation(area, name, status string, started time.Time, fields map[string]interface{}) {
	if a.observability != nil {
		a.observability.Record(area, name, status, started, fields)
	}
}

type AppDiagnostics struct {
	GoVersion         string                 `json:"goVersion"`
	XCryptoVersion    string                 `json:"xCryptoVersion,omitempty"`
	SSHAgentAvailable bool                   `json:"sshAgentAvailable"`
	KnownHostsPath    string                 `json:"knownHostsPath"`
	KnownHostsExists  bool                   `json:"knownHostsExists"`
	Security          sshmgr.SecurityProfile `json:"security"`
	Observability     observability.Summary  `json:"observability"`
}

func (a *App) LoadDiagnostics() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	knownHostsPath := filepath.Join(home, ".ssh", "known_hosts")
	_, statErr := os.Stat(knownHostsPath)

	diagnostics := AppDiagnostics{
		GoVersion:         goruntime.Version(),
		SSHAgentAvailable: os.Getenv("SSH_AUTH_SOCK") != "",
		KnownHostsPath:    knownHostsPath,
		KnownHostsExists:  statErr == nil,
		Security:          sshmgr.ModernSecurityProfile(),
	}
	if buildInfo, ok := debug.ReadBuildInfo(); ok {
		for _, dependency := range buildInfo.Deps {
			if dependency.Path == "golang.org/x/crypto" {
				diagnostics.XCryptoVersion = dependency.Version
				break
			}
		}
	}
	if a.observability != nil {
		diagnostics.Observability = a.observability.Snapshot()
	}
	result, err := json.Marshal(diagnostics)
	return string(result), err
}

func (a *App) SFTPRename(sessionID string, oldPath string, newPath string) error {
	sc, err := a.sftpClient(sessionID)
	if err != nil {
		return err
	}
	return sc.Rename(oldPath, newPath)
}

func (a *App) SFTPCancelTransfer(transferID string) error {
	a.sftpTransfersMu.Lock()
	cancel, ok := a.sftpTransfers[transferID]
	a.sftpTransfersMu.Unlock()
	if !ok {
		return fmt.Errorf("传输不存在或已完成")
	}
	cancel()
	return nil
}

func (a *App) SFTPReadFile(sessionID string, path string) (string, error) {
	sc, err := a.sftpClient(sessionID)
	if err != nil {
		return "", err
	}
	return sc.ReadFile(path)
}

func (a *App) readSFTPFileRange(sessionID, path string, startLine, lineCount int) (sftpmgr.ReadRangeResult, error) {
	sc, err := a.sftpClient(sessionID)
	if err != nil {
		return sftpmgr.ReadRangeResult{}, err
	}
	return sc.ReadFileRange(path, startLine, lineCount)
}

func (a *App) SFTPWriteFile(sessionID string, path string, content string) error {
	sc, err := a.sftpClient(sessionID)
	if err != nil {
		return err
	}
	return sc.WriteFile(path, content)
}

func (a *App) SFTPMkdir(sessionID string, path string) error {
	sc, err := a.sftpClient(sessionID)
	if err != nil {
		return err
	}
	return sc.Mkdir(path)
}

func (a *App) SFTPRemove(sessionID string, path string) error {
	sc, err := a.sftpClient(sessionID)
	if err != nil {
		return err
	}
	return sc.Remove(path)
}

func (a *App) SFTPRemoveRecursive(sessionID string, path string) error {
	sc, err := a.sftpClient(sessionID)
	if err != nil {
		return err
	}
	return sc.RemoveRecursive(path)
}

func (a *App) SFTPChmod(sessionID string, path string, mode uint32) error {
	sc, err := a.sftpClient(sessionID)
	if err != nil {
		return err
	}
	return sc.Chmod(path, os.FileMode(mode))
}

func (a *App) SFTPSymlink(sessionID string, targetPath string, linkPath string) error {
	sc, err := a.sftpClient(sessionID)
	if err != nil {
		return err
	}
	return sc.Symlink(targetPath, linkPath)
}

func (a *App) sftpClient(sessionID string) (*sftpmgr.Client, error) {
	a.sftpClientsMu.Lock()
	defer a.sftpClientsMu.Unlock()
	if client := a.sftpClients[sessionID]; client != nil {
		return client, nil
	}
	sshClient, jumpClient, authCloser, err := a.sshManager.OpenAdditionalClient(sessionID)
	if err != nil {
		return nil, err
	}
	client := sftpmgr.NewDedicatedClient(sshClient, jumpClient, authCloser)
	a.sftpClients[sessionID] = client
	return client, nil
}

func (a *App) closeSFTPClient(sessionID string) {
	a.sftpClientsMu.Lock()
	client := a.sftpClients[sessionID]
	delete(a.sftpClients, sessionID)
	a.sftpClientsMu.Unlock()
	if client != nil {
		_ = client.Close()
	}
}

func (a *App) closeAllSFTPClients() {
	a.sftpClientsMu.Lock()
	clients := a.sftpClients
	a.sftpClients = make(map[string]*sftpmgr.Client)
	a.sftpClientsMu.Unlock()
	for _, client := range clients {
		_ = client.Close()
	}
}

type FileEntry struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"isDir"`
	Perm  string `json:"perm"`
}

func (a *App) SFTPLocalListDir(path string) (string, error) {
	if path == "" {
		home, _ := os.UserHomeDir()
		path = home
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return "", err
	}
	files := make([]FileEntry, 0, len(entries))
	for _, e := range entries {
		info, _ := e.Info()
		sz := int64(0)
		perm := "drwxr-xr-x"
		if info != nil {
			sz = info.Size()
			perm = info.Mode().String()
		}
		if !e.IsDir() && info != nil {
			sz = info.Size()
		}
		files = append(files, FileEntry{
			Name:  e.Name(),
			Size:  sz,
			IsDir: e.IsDir(),
			Perm:  perm,
		})
	}
	b, _ := json.Marshal(files)
	return string(b), nil
}

func (a *App) SFTPLocalHomeDir() (string, error) {
	return os.UserHomeDir()
}

func (a *App) RAGAddDocument(req RAGDocumentRequest) (string, error) {
	if a.ragStore == nil {
		return "", fmt.Errorf("知识库未初始化")
	}
	doc, err := a.ragStore.AddWithMetadata(req.Title, req.Content, req.Source, req.Tags)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(doc)
	return string(b), nil
}

func (a *App) RAGListDocuments() (string, error) {
	if a.ragStore == nil {
		return "[]", nil
	}
	docs, err := a.ragStore.List()
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(docs)
	return string(b), nil
}

func (a *App) RAGSearch(query string) (string, error) {
	return a.ragSearchWithLimit(query, 5)
}

func (a *App) ragSearchWithLimit(query string, limit int) (string, error) {
	if a.ragStore == nil {
		return "[]", nil
	}
	results, err := a.ragStore.Search(query, limit)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(results)
	return string(b), nil
}

func (a *App) RAGDeleteDocument(id string) error {
	if a.ragStore == nil {
		return fmt.Errorf("知识库未初始化")
	}
	return a.ragStore.Delete(id)
}

func (a *App) RAGReindex() (int, error) {
	if a.ragStore == nil {
		return 0, nil
	}
	return a.ragStore.Reindex(context.Background())
}

type PortForwardRequest struct {
	SessionID  string `json:"sessionId"`
	ID         string `json:"id"`
	Type       string `json:"type"` // local / remote / dynamic
	LocalHost  string `json:"localHost"`
	LocalPort  int    `json:"localPort"`
	RemoteHost string `json:"remoteHost"`
	RemotePort int    `json:"remotePort"`
}

type portForwardProfile struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	LocalHost  string `json:"localHost"`
	LocalPort  int    `json:"localPort"`
	RemoteHost string `json:"remoteHost"`
	RemotePort int    `json:"remotePort"`
}

func (a *App) rememberSessionConnection(sessionID, connectionID string) {
	if sessionID == "" || connectionID == "" {
		return
	}
	a.forwardsMu.Lock()
	a.sessionConnIDs[sessionID] = connectionID
	a.forwardsMu.Unlock()
}

func forwardProfileKey(connectionID string) string {
	return "portforward.rules." + connectionID
}

func (a *App) loadForwardProfiles(connectionID string) []portForwardProfile {
	a.forwardProfilesMu.Lock()
	defer a.forwardProfilesMu.Unlock()
	return a.loadForwardProfilesUnlocked(connectionID)
}

func (a *App) loadForwardProfilesUnlocked(connectionID string) []portForwardProfile {
	if a.store == nil || connectionID == "" {
		return nil
	}
	raw, err := a.store.LoadSetting(forwardProfileKey(connectionID))
	if err != nil || raw == "" || len(raw) > 1<<20 {
		return nil
	}
	var profiles []portForwardProfile
	if err := json.Unmarshal([]byte(raw), &profiles); err != nil || len(profiles) > 100 {
		return nil
	}
	return profiles
}

func (a *App) saveForwardProfile(connectionID string, profile portForwardProfile) error {
	a.forwardProfilesMu.Lock()
	defer a.forwardProfilesMu.Unlock()
	if a.store == nil || connectionID == "" {
		return nil
	}
	profiles := a.loadForwardProfilesUnlocked(connectionID)
	found := false
	for i := range profiles {
		if profiles[i].ID == profile.ID {
			profiles[i] = profile
			found = true
			break
		}
	}
	if !found {
		profiles = append(profiles, profile)
	}
	b, err := json.Marshal(profiles)
	if err != nil {
		return err
	}
	return a.store.SaveSetting(forwardProfileKey(connectionID), string(b))
}

func (a *App) removeForwardProfile(connectionID, id string) error {
	a.forwardProfilesMu.Lock()
	defer a.forwardProfilesMu.Unlock()
	if a.store == nil || connectionID == "" {
		return nil
	}
	profiles := a.loadForwardProfilesUnlocked(connectionID)
	filtered := profiles[:0]
	for _, profile := range profiles {
		if profile.ID != id {
			filtered = append(filtered, profile)
		}
	}
	b, err := json.Marshal(filtered)
	if err != nil {
		return err
	}
	return a.store.SaveSetting(forwardProfileKey(connectionID), string(b))
}

func (a *App) restoreForwardRules(sessionID, connectionID string) {
	for _, profile := range a.loadForwardProfiles(connectionID) {
		_, err := a.PortForwardStart(PortForwardRequest{
			SessionID: sessionID, ID: profile.ID, Type: profile.Type,
			LocalHost: profile.LocalHost, LocalPort: profile.LocalPort,
			RemoteHost: profile.RemoteHost, RemotePort: profile.RemotePort,
		})
		if err != nil {
			fmt.Printf("恢复端口转发 %s 失败: %v\n", profile.ID, err)
		}
	}
}

func (a *App) PortForwardStart(req PortForwardRequest) (string, error) {
	if req.ID == "" {
		req.ID = fmt.Sprintf("pf-%d", time.Now().UnixNano())
	}
	if req.LocalPort < 0 || req.LocalPort > 65535 || req.RemotePort < 0 || req.RemotePort > 65535 {
		return "", fmt.Errorf("端口范围无效")
	}
	if req.LocalHost == "" {
		req.LocalHost = "127.0.0.1"
	}
	if req.Type != "dynamic" && (req.RemoteHost == "" || req.RemotePort == 0) {
		return "", fmt.Errorf("远程主机和端口不能为空")
	}
	if req.Type != "local" && req.Type != "remote" && req.Type != "dynamic" {
		return "", fmt.Errorf("未知转发类型: %s", req.Type)
	}
	sshClient, err := a.sshManager.GetClient(req.SessionID)
	if err != nil {
		return "", err
	}
	a.forwardsMu.Lock()
	pf := a.forwards[req.SessionID]
	if pf == nil {
		pf = pfmgr.NewManager(sshClient)
		a.forwards[req.SessionID] = pf
	}
	a.forwardsMu.Unlock()
	localAddr := net.JoinHostPort(req.LocalHost, fmt.Sprintf("%d", req.LocalPort))
	remoteAddr := net.JoinHostPort(req.RemoteHost, fmt.Sprintf("%d", req.RemotePort))

	var rule *pfmgr.ForwardRule
	switch req.Type {
	case "local":
		rule, err = pf.AddLocal(req.ID, localAddr, remoteAddr)
	case "remote":
		rule, err = pf.AddRemote(req.ID, remoteAddr, localAddr)
	case "dynamic":
		rule, err = pf.AddDynamic(req.ID, localAddr)
	}
	if err != nil {
		return "", err
	}
	a.forwardsMu.Lock()
	a.forwardIDs[req.ID] = req.SessionID
	connectionID := a.sessionConnIDs[req.SessionID]
	a.forwardsMu.Unlock()
	if err := a.saveForwardProfile(connectionID, portForwardProfile{
		ID: req.ID, Type: req.Type, LocalHost: rule.LocalHost, LocalPort: rule.LocalPort,
		RemoteHost: rule.RemoteHost, RemotePort: rule.RemotePort,
	}); err != nil {
		fmt.Printf("保存端口转发配置 %s 失败: %v\n", req.ID, err)
	}
	b, _ := json.Marshal(rule)
	return string(b), nil
}

func (a *App) PortForwardStop(id string) error {
	a.forwardsMu.Lock()
	sessionID, ok := a.forwardIDs[id]
	pf := a.forwards[sessionID]
	connectionID := a.sessionConnIDs[sessionID]
	a.forwardsMu.Unlock()
	if ok && pf != nil {
		err := pf.Stop(id)
		if err == nil {
			a.forwardsMu.Lock()
			delete(a.forwardIDs, id)
			a.forwardsMu.Unlock()
			if saveErr := a.removeForwardProfile(connectionID, id); saveErr != nil {
				fmt.Printf("删除端口转发配置 %s 失败: %v\n", id, saveErr)
			}
		}
		return err
	}
	return fmt.Errorf("转发规则不存在")
}

func (a *App) PortForwardList(sessionID string) (string, error) {
	a.forwardsMu.Lock()
	pf := a.forwards[sessionID]
	a.forwardsMu.Unlock()
	rules := make([]pfmgr.ForwardRule, 0)
	if pf != nil {
		rules = append(rules, pf.ListRules()...)
	}
	b, _ := json.Marshal(rules)
	return string(b), nil
}
