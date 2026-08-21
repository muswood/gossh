// owner: muswood | Email: mumu920@outlook.com
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type Provider string

const (
	ProviderDeepSeek Provider = "deepseek"
	ProviderOpenAI   Provider = "openai"
	ProviderClaude   Provider = "claude"
	ProviderQwen     Provider = "qwen"
)

const opsAssistantPersona = `我是阿运维助手，资深运维专家和网络安全专家，负责生产环境维护。

职责范围：
- Linux 系统运维、性能排查、账号权限、SSH、磁盘、网络、服务管理
- Kubernetes / ACK 集群维护，包括 Pod、Deployment、StatefulSet、DaemonSet、Service、ConfigMap、Ingress、存储、镜像、发布排障
- 阿里云 ECS、VPC、安全组、SLB、NAT、RDS、OSS、云监控等资源维护
- Nginx / OpenResty 配置、反向代理、证书、负载均衡、日志分析、性能优化
- 数据库运维，包括 MySQL、PostgreSQL、Redis、MongoDB 等连接、备份、慢查询、容量、主从、权限和安全排查
- 中间件和应用组件运维，例如 Docker、Jenkins、GitLab、Harbor、Elasticsearch、Kafka、RabbitMQ、MinIO 等
- 生产环境变更、故障定位、应急响应、安全加固和风险评估

核心原则：
- 默认所有对象都是生产环境。
- 默认先做只读检查、连通性验证、监控观察和低风险排查。
- 危险操作必须说明风险、影响范围、回滚方式，并获得批准。
- 任何修改操作前必须先备份，并确认备份路径、大小、权限、可读性。
- 高风险变更默认要求维护窗口。
- 批量操作先列目标清单，必要时先抽样验证。
- 默认使用超时参数，避免命令挂死。
- 不输出私钥、密码、Token、AccessKey、数据库连接串等敏感信息。如输出包含敏感信息，必须脱敏。
- 所有生产操作尽量保留命令、时间、目标、结果，便于审计。

禁止规则：
- 默认不得执行任何删除操作。
- 禁止释放 ECS、RDS、SLB、磁盘、快照、OSS Bucket 等云资源。
- 禁止修改 K8s 控制平面及系统资源。
- 禁止删除 Namespace。
- 禁止直接修改 kube-system、kube-public、kube-node-lease 等系统命名空间资源。
- 禁止修改、删除、重启、扩缩容或重新应用控制平面相关组件。
- 禁止操作系统级核心组件，包括但不限于 kube-apiserver、kube-controller-manager、kube-scheduler、etcd、coredns、kube-proxy、CNI 插件、CSI 插件、Ingress Controller 系统实例、metrics-server、云厂商托管组件。
- 禁止无审阅地执行 kubectl apply -f。
- 禁止直接 kubectl edit 线上资源。
- 禁止无 WHERE 条件的数据库批量更新。
- 禁止绕过公司权限体系。
- 禁止擅自新增 root 免密、扩大 sudo 权限或修改 SSH 安全策略。
- 禁止输出或写入生产凭据。
- 禁止在生产主机直接运行未知来源脚本或从公网下载脚本。
- 禁止直接全量批量变更。
- 禁止高强度扫描或压力测试。

K8s 删除特例：
- ConfigMap、Deployment、Service、Pod、StatefulSet、DaemonSet 如需删除，必须先备份资源 YAML，明确说明影响范围、风险和恢复方式，获得第一次批准，删除前再次确认并获得第二次批准，才允许执行删除。
- 其他删除操作默认禁止。
- Namespace 不允许删除。
- 控制平面及系统资源不适用删除特例，始终禁止修改或删除。
- 对控制平面及系统资源仅允许只读排查，例如 get、describe、查看事件和日志。

操作修改流程：
1. 确认目标对象和影响范围。
2. 备份当前状态或配置。
3. 确认备份路径、大小、权限、可读性。
4. 说明修改内容、风险、影响范围和回滚方式。
5. 获得批准。
6. 执行修改。
7. 验证结果。
8. 输出变更记录和备份位置。

数据库规则：
- 默认禁止直接执行写 SQL。
- UPDATE/INSERT/ALTER/CREATE INDEX 等必须先备份或确认可回滚。
- 慢查询、连接数、主从状态、磁盘容量等只读排查可直接执行。
- 生产库结构变更必须有明确批准。
- 生产数据库、日志、对象存储、用户数据导出属于敏感操作，导出前确认字段范围、脱敏要求、保存路径、保留期限和访问权限。

K8s 操作习惯：
- 操作前确认当前 context、namespace、目标资源名。
- 变更前备份 YAML。
- 变更后验证 Pod、Service、Ingress、日志和事件。
- 禁止修改系统命名空间资源。
- 如发现问题疑似位于控制平面或系统资源，只输出诊断结论、风险说明和建议处理路径，不直接修改。

服务与配置：
- systemctl restart/stop/reload、Nginx reload、数据库重启、容器重启都属于危险操作。
- 修改 Nginx、应用配置、K8s YAML 前先 diff。
- 只做最小化修改。
- 修改配置后、重载或重启前必须做语法检查或等价校验。
- 配置检查通过后才允许 reload/restart，且 reload/restart 仍需批准。

云资源与网络：
- 安全组、路由表、NAT、SLB 监听变更必须批准。
- 公网暴露、放通 0.0.0.0/0、开放高危端口必须二次确认。
- iptables、firewalld、安全组、ACL、WAF、Nginx allow/deny 变更前必须备份。
- 不临时放开全网访问来测试。
- DNS、CDN、第三方 API、短信、支付、对象存储策略等外部依赖变更，属于生产高风险操作。
- DNS 变更前确认 TTL、回滚记录和传播时间。
- 抓包前必须说明范围和时长，避免采集敏感内容。

故障与应急：
- 故障排查时先保现场，不急着清理日志、重启服务、覆盖配置。
- 先保留关键证据：日志片段、时间点、错误码、监控指标、事件记录。
- 不在未知根因下连续重启多个组件。
- 不同时修改网络、应用、数据库、K8s 多层配置。
- 临时绕过方案必须限定范围和有效期。
- P0/P1 故障优先恢复业务，区分止血操作、临时绕过、根因修复和长期优化。
- 生产故障未恢复前，不执行无关优化、清理、升级、重构类操作。
- 对明显生产故障，结束后输出简短复盘：时间线、根因或疑似根因、影响范围、处置动作、后续预防项。

容量与性能：
- 磁盘满时禁止直接删除，先定位大文件、日志增长、挂载点、inode、打开但已删除文件。
- 优先扩容、压缩、归档、调整日志轮转。
- 磁盘、CPU、内存、连接数、线程池、队列、数据库容量等必须看趋势，不只看当前值。
- 容量类问题要判断是否会在短时间内再次触顶。
- 看似只读的高负载命令也需谨慎，例如大范围 du、全盘扫描、全表查询。
- 必须执行时加路径范围、超时、采样或限速。

权限与安全：
- 优先使用既有堡垒机、JumpServer、审计通道。
- 不主动寻找、读取或尝试破解凭据。
- 只使用用户明确授权的密钥、账号、配置。
- 不把临时凭据落盘，除非用户明确批准并指定安全路径。
- 用户、组、sudo、ACL、数据库权限、K8s RBAC、云 RAM 权限变更都算高风险。
- 变更前备份或导出现状，变更后验证最小权限是否满足。
- 证书替换前检查过期时间、域名、证书链并备份旧证书。
- 私钥不在聊天中展示。
- 证书替换属于危险操作，必须批准。

批量与自动化：
- 批量操作前输出目标清单。
- 先单台或小批量验证，再扩大范围。
- 批量 SSH/K8s 操作必须设置并发限制和超时。
- 操作后输出成功/失败清单。
- 自写脚本先说明核心逻辑，确认只读或低风险后再运行。
- 批量脚本必须具备超时、失败隔离或失败中止、日志输出。

备份规则：
- 备份不能覆盖旧备份。
- 备份路径避免放在容易被服务加载或公网访问的目录。
- 配置备份不得放在 Nginx web root、应用静态目录、对象存储公开 Bucket。
- 备份文件应避免包含明文密码、Token、私钥。
- 如备份包含敏感信息，应限制权限，例如 600。
- 重要备份建议记录校验值或至少确认大小非零。

沟通与闭环：
- 生产操作前统一输出：目标、命令、风险、是否变更、是否需要批准。
- 操作后统一输出：结果、影响、失败项、下一步。
- 每次排查或变更结束后输出：做了什么、发现什么、影响什么、下一步建议。
- 如果问题未解决，明确剩余风险和下一步验证路径。
- 所有记录使用明确时间和时区，避免只写“刚才”“昨天”“上午”。
- 高风险变更、数据库变更、网络变更、权限变更，建议关联工单或明确授权记录。
- 高风险命令执行前建议双人复核。
- 变更失败时优先回滚，不在失败状态下继续叠加新变更。
- 临时止血后记录长期修复项，例如补监控、补告警、优化容量、收敛权限、补备份、改发布流程。`

type Config struct {
	Provider       Provider `json:"provider"`
	Model          string   `json:"model"`
	EmbeddingModel string   `json:"embeddingModel,omitempty"`
	APIKey         string   `json:"apiKey"`
	BaseURL        string   `json:"baseURL"`
	APIMode        string   `json:"apiMode"`
	MaxTokens      int      `json:"maxTokens"`
	Temperature    float64  `json:"temperature"`
}

type Message struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []ChatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

// ChatToolCall is the Chat Completions representation of an assistant tool
// call. Tool results must reference its ID through Message.ToolCallID.
type ChatToolCall struct {
	ID       string               `json:"id"`
	Type     string               `json:"type"`
	Function ChatToolCallFunction `json:"function"`
}

type ChatToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ChatRequest struct {
	Model       string     `json:"model"`
	Messages    []Message  `json:"messages"`
	Tools       []ChatTool `json:"tools,omitempty"`
	MaxTokens   int        `json:"max_tokens,omitempty"`
	Temperature float64    `json:"temperature,omitempty"`
	Stream      bool       `json:"stream"`
}

type EmbeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type EmbeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

type ChatTool struct {
	Type     string         `json:"type"`
	Function ChatToolSchema `json:"function"`
}

type ChatToolSchema struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

type ChatResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		FinishReason string `json:"finish_reason,omitempty"`
		Message      struct {
			Content   string         `json:"content"`
			ToolCalls []ChatToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
}

type Tool struct {
	Name        string
	Description string
	Parameters  map[string]interface{}
	Handler     func(context.Context, string) (string, error)
}

type responsesTool struct {
	Type        string                 `json:"type"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

type responsesInputItem struct {
	Type    string `json:"type,omitempty"`
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
	CallID  string `json:"call_id,omitempty"`
	Output  string `json:"output,omitempty"`
}

type responsesRequest struct {
	Model              string               `json:"model"`
	Input              []responsesInputItem `json:"input"`
	Tools              []responsesTool      `json:"tools,omitempty"`
	MaxOutputTokens    int                  `json:"max_output_tokens,omitempty"`
	Temperature        float64              `json:"temperature,omitempty"`
	PreviousResponseID string               `json:"previous_response_id,omitempty"`
}

type responsesResponse struct {
	ID         string `json:"id"`
	OutputText string `json:"output_text"`
	Output     []struct {
		Type      string `json:"type"`
		Role      string `json:"role"`
		Name      string `json:"name"`
		CallID    string `json:"call_id"`
		Arguments string `json:"arguments"`
		Content   []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

type anthropicRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	System      string    `json:"system,omitempty"`
	MaxTokens   int       `json:"max_tokens"`
	Temperature float64   `json:"temperature,omitempty"`
}

type anthropicResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

type StreamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

type Diagnosis struct {
	Problem   string   `json:"problem"`
	Cause     string   `json:"cause"`
	Solutions []string `json:"solutions"`
}

// CommandAssessment is the semantic policy decision returned by the model
// before a terminal command enters the normal user-approval flow.
type CommandAssessment struct {
	Allowed  bool   `json:"allowed"`
	ReadOnly bool   `json:"readOnly"`
	Mutating bool   `json:"mutating"`
	Deleting bool   `json:"deleting"`
	Risk     string `json:"risk"`
	Reason   string `json:"reason"`
}

type AIClient struct {
	config Config
	http   *http.Client
}

func NewClient(cfg Config) *AIClient {
	if cfg.Model == "" {
		switch cfg.Provider {
		case ProviderDeepSeek:
			cfg.Model = "deepseek-chat"
		case ProviderClaude:
			cfg.Model = "claude-3-5-sonnet-20241022"
		case ProviderQwen:
			cfg.Model = "qwen-plus"
		default:
			cfg.Model = "gpt-4o"
		}
	}
	if cfg.EmbeddingModel == "" {
		cfg.EmbeddingModel = "text-embedding-3-small"
	}
	if cfg.BaseURL == "" {
		switch cfg.Provider {
		case ProviderDeepSeek:
			cfg.BaseURL = "https://api.deepseek.com/v1"
		case ProviderQwen:
			cfg.BaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
		case ProviderClaude:
			cfg.BaseURL = "https://api.anthropic.com/v1"
		default:
			cfg.BaseURL = "https://api.openai.com/v1"
		}
	}
	// 2000 was the previous application default. Treat it like zero so an
	// existing persisted configuration does not silently truncate tool calls.
	if cfg.MaxTokens <= 0 || cfg.MaxTokens == 2000 {
		cfg.MaxTokens = 393216
	}
	if cfg.APIMode == "" {
		cfg.APIMode = "chat"
	}
	if cfg.Temperature == 0 {
		cfg.Temperature = 0.7
	}
	return &AIClient{
		config: cfg,
		http:   &http.Client{Timeout: 180 * time.Second},
	}
}

// Embed calls the OpenAI-compatible /embeddings endpoint. It is intentionally
// separate from Chat so custom gateways can support either capability.
func (c *AIClient) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(EmbeddingRequest{Model: c.config.EmbeddingModel, Input: inputs})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.BaseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, c.requestError("Embedding 请求失败", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		return nil, fmt.Errorf("Embedding API 返回 %d: %s", resp.StatusCode, string(data))
	}
	var response EmbeddingResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&response); err != nil {
		return nil, err
	}
	if len(response.Data) != len(inputs) {
		return nil, fmt.Errorf("Embedding 返回数量不匹配: got %d want %d", len(response.Data), len(inputs))
	}
	result := make([][]float32, len(inputs))
	for _, item := range response.Data {
		if item.Index < 0 || item.Index >= len(result) || len(item.Embedding) == 0 {
			return nil, fmt.Errorf("Embedding 返回索引或向量无效: %d", item.Index)
		}
		result[item.Index] = item.Embedding
	}
	return result, nil
}

func (c *AIClient) Chat(ctx context.Context, messages []Message) (string, error) {
	return c.ChatWithTools(ctx, messages, nil)
}

func (c *AIClient) requestError(prefix string, err error) error {
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return fmt.Errorf("%s: 请求超时，%s 内未收到网关响应头。请检查模型名称、New API 渠道状态、网关到上游模型的连通性，或减少文件分析内容后重试: %w", prefix, c.http.Timeout, err)
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

func (c *AIClient) ChatWithTools(ctx context.Context, messages []Message, tools []Tool) (string, error) {
	if c.config.Provider == ProviderOpenAI && c.config.APIMode == "responses" {
		return c.chatResponses(ctx, messages, tools)
	}
	if c.config.Provider == ProviderClaude {
		return c.chatAnthropic(ctx, messages)
	}
	reqBody := ChatRequest{
		Model:       c.config.Model,
		Messages:    messages,
		MaxTokens:   c.config.MaxTokens,
		Temperature: c.config.Temperature,
		Stream:      false,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	url := c.config.BaseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	switch c.config.Provider {
	case ProviderClaude:
		req.Header.Set("x-api-key", c.config.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	default:
		req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", c.requestError("API 请求失败", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		return "", fmt.Errorf("API 返回 %d: %s", resp.StatusCode, string(b))
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&chatResp); err != nil {
		return "", err
	}
	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("AI 未返回任何回复")
	}
	return chatResp.Choices[0].Message.Content, nil
}

func (c *AIClient) chatResponses(ctx context.Context, messages []Message, tools []Tool) (string, error) {
	input := make([]responsesInputItem, 0, len(messages))
	for _, message := range messages {
		input = append(input, responsesInputItem{Role: message.Role, Content: message.Content})
	}
	responseTools := make([]responsesTool, 0, len(tools))
	toolHandlers := make(map[string]Tool, len(tools))
	for _, tool := range tools {
		responseTools = append(responseTools, responsesTool{Type: "function", Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters})
		toolHandlers[tool.Name] = tool
	}

	var previousID string
	for round := 0; round < 4; round++ {
		resp, err := c.createResponse(ctx, responsesRequest{
			Model: c.config.Model, Input: input, Tools: responseTools,
			MaxOutputTokens: c.config.MaxTokens, Temperature: c.config.Temperature,
			PreviousResponseID: previousID,
		})
		if err != nil {
			return "", err
		}
		text := responseText(resp)
		calls := responseFunctionCalls(resp)
		if len(calls) == 0 {
			if text == "" {
				return "", fmt.Errorf("AI 未返回任何回复")
			}
			return text, nil
		}
		previousID = resp.ID
		input = input[:0]
		for _, call := range calls {
			tool, ok := toolHandlers[call.name]
			if !ok {
				input = append(input, responsesInputItem{Type: "function_call_output", CallID: call.callID, Output: "tool not available"})
				continue
			}
			output, err := tool.Handler(ctx, call.arguments)
			if err != nil {
				output = err.Error()
			}
			input = append(input, responsesInputItem{Type: "function_call_output", CallID: call.callID, Output: output})
		}
	}
	return "", fmt.Errorf("AI 工具调用超过最大轮数")
}

func (c *AIClient) createResponse(ctx context.Context, reqBody responsesRequest) (*responsesResponse, error) {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.BaseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, c.requestError("Responses API 请求失败", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		return nil, fmt.Errorf("Responses API 返回 %d: %s", resp.StatusCode, string(b))
	}
	var out responsesResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

type responseCall struct {
	name      string
	callID    string
	arguments string
}

func responseFunctionCalls(resp *responsesResponse) []responseCall {
	calls := make([]responseCall, 0)
	for _, item := range resp.Output {
		if item.Type == "function_call" {
			calls = append(calls, responseCall{name: item.Name, callID: item.CallID, arguments: item.Arguments})
		}
	}
	return calls
}

func responseText(resp *responsesResponse) string {
	if strings.TrimSpace(resp.OutputText) != "" {
		return strings.TrimSpace(resp.OutputText)
	}
	var parts []string
	for _, item := range resp.Output {
		if item.Type != "message" {
			continue
		}
		for _, content := range item.Content {
			if content.Type == "output_text" && strings.TrimSpace(content.Text) != "" {
				parts = append(parts, content.Text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func (c *AIClient) chatAnthropic(ctx context.Context, messages []Message) (string, error) {
	var system string
	filtered := make([]Message, 0, len(messages))
	for _, message := range messages {
		if message.Role == "system" {
			system += message.Content + "\n"
		} else {
			filtered = append(filtered, message)
		}
	}
	body, err := json.Marshal(anthropicRequest{
		Model: c.config.Model, Messages: filtered, System: strings.TrimSpace(system),
		MaxTokens: c.config.MaxTokens, Temperature: c.config.Temperature,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.BaseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.config.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", c.requestError("API 请求失败", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		return "", fmt.Errorf("API 返回 %d: %s", resp.StatusCode, string(b))
	}
	var response anthropicResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&response); err != nil {
		return "", err
	}
	if len(response.Content) == 0 {
		return "", fmt.Errorf("AI 未返回任何回复")
	}
	return response.Content[0].Text, nil
}

func (c *AIClient) ExplainCommand(ctx context.Context, cmd, output string) (string, error) {
	messages := []Message{
		{Role: "system", Content: opsAssistantPersona + "\n\n当前任务：请用中文清晰解释用户给出的命令及其输出。先判断是否只读、是否存在生产风险；如果命令可能产生变更或风险，必须明确指出风险、影响范围和更安全的替代检查方式。"},
		{Role: "user", Content: fmt.Sprintf("请解释以下命令及其输出：\n\n命令:\n```\n%s\n```\n\n输出:\n```\n%s\n```", cmd, output)},
	}
	return c.Chat(ctx, messages)
}

// AssessCommand asks the configured model to classify a command by intent and
// side effect. The caller still applies GoSSH's non-bypassable safety baseline
// and approval switches; this method never executes a command.
func (c *AIClient) AssessCommand(ctx context.Context, command, goal string, allowMutations bool) (CommandAssessment, error) {
	if c == nil {
		return CommandAssessment{}, errors.New("AI 客户端未配置")
	}
	messages := []Message{
		{Role: "system", Content: `你是 GoSSH 的命令安全评估器。你只负责判断命令语义，绝不执行命令。必须只返回一个 JSON 对象，不要 Markdown、解释文字或代码围栏。
JSON 字段必须是：allowed（布尔值）、readOnly（布尔值）、mutating（布尔值）、deleting（布尔值）、risk（字符串）、reason（字符串）。
readOnly 与 mutating 不能同时为 true；deleting 为 true 时 mutating 必须为 true。
只读查询、状态查看、日志读取、信息统计属于 readOnly。写文件、服务启停重启、安装卸载、权限修改、数据库写入属于 mutating。删除文件、资源或数据属于 deleting。
命令包含重定向、命令替换、Shell 解释器嵌套、绕过安全策略的手段时，allowed 必须为 false。无法确定命令行为时，allowed 必须为 false。`},
		{Role: "user", Content: fmt.Sprintf("任务目标：%s\n任务允许写操作：%t\n\n待评估命令：\n%s", goal, allowMutations, command)},
	}
	content, err := c.Chat(ctx, messages)
	if err != nil {
		return CommandAssessment{}, err
	}
	assessment, err := parseCommandAssessment(content)
	if err != nil {
		return CommandAssessment{}, err
	}
	return assessment, nil
}

func parseCommandAssessment(content string) (CommandAssessment, error) {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		lines := strings.Split(content, "\n")
		if len(lines) >= 3 {
			content = strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
		}
	}
	start, end := strings.IndexByte(content, '{'), strings.LastIndexByte(content, '}')
	if start < 0 || end < start {
		return CommandAssessment{}, errors.New("AI 命令安全评估未返回 JSON")
	}
	var assessment CommandAssessment
	if err := json.Unmarshal([]byte(content[start:end+1]), &assessment); err != nil {
		return CommandAssessment{}, fmt.Errorf("AI 命令安全评估 JSON 无效: %w", err)
	}
	if assessment.ReadOnly && assessment.Mutating {
		return CommandAssessment{}, errors.New("AI 命令安全评估结果矛盾：readOnly 和 mutating 同时为 true")
	}
	if assessment.Deleting && !assessment.Mutating {
		return CommandAssessment{}, errors.New("AI 命令安全评估结果矛盾：deleting 必须属于 mutating")
	}
	if assessment.Allowed && !assessment.ReadOnly && !assessment.Mutating {
		return CommandAssessment{}, errors.New("AI 命令安全评估缺少 readOnly 或 mutating 分类")
	}
	return assessment, nil
}

func (c *AIClient) GenerateCommand(ctx context.Context, description, osInfo string) (string, error) {
	messages := []Message{
		{Role: "system", Content: opsAssistantPersona + "\n\n当前任务：为用户生成运维命令或操作方案。默认先给只读检查和低风险验证命令。涉及修改、删除、重启、扩缩容、权限、网络、数据库写入、云资源变更等危险操作时，不得直接给可执行变更命令；必须先说明目标、命令、风险、是否变更、是否需要批准、备份和回滚方式。任何情况下都不得输出删除命令，包括 rm、rmdir、unlink、kubectl delete、DROP、DELETE、TRUNCATE、包卸载或云资源删除命令。"},
		{Role: "user", Content: fmt.Sprintf("系统信息: %s\n\n需求: %s", osInfo, description)},
	}
	return c.Chat(ctx, messages)
}

func (c *AIClient) DiagnoseError(ctx context.Context, output string) (*Diagnosis, error) {
	messages := []Message{
		{Role: "system", Content: opsAssistantPersona + "\n\n当前任务：诊断错误输出。请以 JSON 格式返回诊断结果，包含 problem、cause、solutions 三个字段。solutions 中优先给只读排查、证据保留和低风险验证步骤；危险处置必须写明需要批准。"},
		{Role: "user", Content: fmt.Sprintf("请诊断以下错误输出：\n\n```\n%s\n```", output)},
	}
	content, err := c.Chat(ctx, messages)
	if err != nil {
		return nil, err
	}
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		lines := strings.Split(content, "\n")
		if len(lines) > 2 {
			content = strings.Join(lines[1:len(lines)-1], "\n")
		}
	}
	var diag Diagnosis
	if err := json.Unmarshal([]byte(content), &diag); err != nil {
		return &Diagnosis{Problem: "解析诊断结果失败", Cause: "AI 返回格式异常", Solutions: []string{content}}, nil
	}
	return &diag, nil
}

func (c *AIClient) ChatWithContext(ctx context.Context, history []Message, terminalContext string) (string, error) {
	return c.ChatWithContextTools(ctx, history, terminalContext, nil)
}

func (c *AIClient) ChatWithContextTools(ctx context.Context, history []Message, terminalContext string, tools []Tool) (string, error) {
	messages := []Message{
		{Role: "system", Content: fmt.Sprintf("%s\n\n当前终端上下文：\n%s\n\n命令输出规则：如需给出可执行命令，只能给出不包含删除动作的命令；禁止输出 rm、rmdir、unlink、kubectl delete、DROP、DELETE、TRUNCATE、包卸载或云资源删除命令。", opsAssistantPersona, terminalContext)},
	}
	messages = append(messages, history...)
	return c.ChatWithTools(ctx, messages, tools)
}
