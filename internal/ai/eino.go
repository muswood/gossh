// owner: muswood | Email: mumu920@outlook.com
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

var _ model.BaseChatModel = (*EinoChatModel)(nil)

// EinoChatModel adapts the existing provider client to Eino's chat model
// contract. The provider remains the single place that owns API wire formats.
type EinoChatModel struct {
	client *AIClient
}

// CallDiagnostics records protocol metadata needed to diagnose incomplete
// model tool calls. It intentionally excludes prompts and model output.
type CallDiagnostics struct {
	FinishReason  string `json:"finishReason,omitempty"`
	MaxTokens     int    `json:"maxTokens"`
	ToolCallBytes int    `json:"toolCallBytes"`
	HTTPStatus    int    `json:"httpStatus"`
	RequestID     string `json:"requestId,omitempty"`
	Forced        bool   `json:"forced,omitempty"`
	Retry         int    `json:"retry,omitempty"`
}

type DiagnosticsError struct {
	Diagnostics CallDiagnostics
	Err         error
}

func (e *DiagnosticsError) Error() string { return e.Err.Error() }
func (e *DiagnosticsError) Unwrap() error { return e.Err }

func DiagnosticsFromError(err error) (CallDiagnostics, bool) {
	var diagnosticErr *DiagnosticsError
	if errors.As(err, &diagnosticErr) {
		return diagnosticErr.Diagnostics, true
	}
	return CallDiagnostics{}, false
}

const DiagnosticsExtraKey = "gossh.ai.diagnostics"

func NewEinoChatModel(client *AIClient) *EinoChatModel {
	return &EinoChatModel{client: client}
}

// ForceToolCall performs a separate Chat Completions request that registers
// only the named tool. The caller validates that the model actually returned
// that tool call because providers differ in tool-call forcing support.
func (c *AIClient) ForceToolCall(ctx context.Context, messages []Message, tool Tool, toolName string) (ChatToolCall, CallDiagnostics, error) {
	if c == nil {
		return ChatToolCall{}, CallDiagnostics{Forced: true}, fmt.Errorf("AI 客户端未配置")
	}
	diagnostics := CallDiagnostics{MaxTokens: c.config.MaxTokens, Forced: true}
	if strings.TrimSpace(tool.Name) == "" || strings.TrimSpace(toolName) == "" {
		return ChatToolCall{}, diagnostics, fmt.Errorf("强制工具调用缺少工具名称")
	}
	if tool.Name != toolName {
		return ChatToolCall{}, diagnostics, fmt.Errorf("强制工具名称不匹配: %s != %s", tool.Name, toolName)
	}
	reqBody := ChatRequest{
		Model:    c.config.Model,
		Messages: messages,
		Tools: []ChatTool{{Type: "function", Function: ChatToolSchema{
			Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters,
		}}},
		MaxTokens: c.config.MaxTokens, Temperature: c.config.Temperature, Stream: false,
	}
	resp, err := c.doChatCompletion(ctx, reqBody)
	if err != nil {
		return ChatToolCall{}, diagnostics, c.requestError("强制 report 请求失败", err)
	}
	diagnostics.HTTPStatus = resp.StatusCode
	diagnostics.RequestID = responseRequestID(resp.Header)
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return ChatToolCall{}, diagnostics, readAPIError(resp)
	}
	defer resp.Body.Close()
	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return ChatToolCall{}, diagnostics, fmt.Errorf("强制 report 响应 JSON 无效: %w", err)
	}
	if len(chatResp.Choices) == 0 {
		return ChatToolCall{}, diagnostics, fmt.Errorf("强制 report 未返回任何回复")
	}
	choice := chatResp.Choices[0]
	diagnostics.FinishReason = choice.FinishReason
	diagnostics.ToolCallBytes = toolCallArgumentBytes(choice.Message.ToolCalls)
	for _, call := range choice.Message.ToolCalls {
		if call.Function.Name != toolName {
			continue
		}
		if strings.TrimSpace(call.ID) == "" {
			return ChatToolCall{}, diagnostics, fmt.Errorf("强制 report 工具调用缺少 ID")
		}
		if strings.TrimSpace(call.Function.Arguments) == "" {
			return ChatToolCall{}, diagnostics, fmt.Errorf("强制 report 工具调用缺少参数")
		}
		return call, diagnostics, nil
	}
	finishReason := choice.FinishReason
	if finishReason == "" {
		finishReason = "unknown"
	}
	return ChatToolCall{}, diagnostics, fmt.Errorf("强制 report 未返回 report 工具调用，finish_reason=%s", finishReason)
}

func (c *AIClient) doChatCompletion(ctx context.Context, request ChatRequest) (*http.Response, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	req, err := newAPIRequest(ctx, c.config.BaseURL+"/chat/completions", c.config.APIKey, body)
	if err != nil {
		return nil, err
	}
	return c.http.Do(req)
}

func (m *EinoChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	if m == nil || m.client == nil {
		return nil, fmt.Errorf("AI 客户端未配置")
	}
	options := model.GetCommonOptions(&model.Options{
		Model:     stringPtr(m.client.config.Model),
		MaxTokens: intPtr(m.client.config.MaxTokens),
	}, opts...)
	return m.client.generateEino(ctx, input, options)
}

func (m *EinoChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	message, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

func stringPtr(value string) *string {
	return &value
}

func intPtr(value int) *int {
	return &value
}

func newAPIRequest(ctx context.Context, endpoint, apiKey string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	return req, nil
}

func readAPIError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return fmt.Errorf("API 返回 %d: %s", resp.StatusCode, string(body))
}

func (c *AIClient) generateEino(ctx context.Context, input []*schema.Message, options *model.Options) (*schema.Message, error) {
	messages := make([]Message, 0, len(input))
	for _, item := range input {
		if item == nil {
			continue
		}
		message := Message{Role: string(item.Role), Content: item.Content}
		if item.Role == schema.Tool {
			if strings.TrimSpace(item.ToolCallID) == "" {
				return nil, fmt.Errorf("工具结果缺少 tool_call_id，无法继续调用模型")
			}
			message.ToolCallID = item.ToolCallID
		}
		if len(item.ToolCalls) > 0 {
			message.ToolCalls = make([]ChatToolCall, 0, len(item.ToolCalls))
			for _, call := range item.ToolCalls {
				if strings.TrimSpace(call.ID) == "" {
					return nil, fmt.Errorf("工具调用缺少 ID，无法继续调用模型")
				}
				callType := call.Type
				if callType == "" {
					callType = "function"
				}
				message.ToolCalls = append(message.ToolCalls, ChatToolCall{
					ID:   call.ID,
					Type: callType,
					Function: ChatToolCallFunction{
						Name:      call.Function.Name,
						Arguments: call.Function.Arguments,
					},
				})
			}
		}
		messages = append(messages, message)
	}

	if len(options.Tools) == 0 {
		content, err := c.ChatWithTools(ctx, messages, nil)
		if err != nil {
			return nil, err
		}
		return schema.AssistantMessage(content, nil), nil
	}

	tools := make([]ChatTool, 0, len(options.Tools))
	for _, item := range options.Tools {
		if item == nil {
			continue
		}
		var parameters map[string]interface{}
		if item.ParamsOneOf != nil {
			jsonSchema, err := item.ParamsOneOf.ToJSONSchema()
			if err != nil {
				return nil, fmt.Errorf("工具 %s 参数定义无效: %w", item.Name, err)
			}
			encoded, err := json.Marshal(jsonSchema)
			if err != nil {
				return nil, fmt.Errorf("序列化工具 %s 参数定义失败: %w", item.Name, err)
			}
			if err := json.Unmarshal(encoded, &parameters); err != nil {
				return nil, fmt.Errorf("解析工具 %s 参数定义失败: %w", item.Name, err)
			}
		}
		tools = append(tools, ChatTool{
			Type: "function",
			Function: ChatToolSchema{
				Name:        item.Name,
				Description: item.Desc,
				Parameters:  parameters,
			},
		})
	}

	if c.config.Provider == ProviderClaude || (c.config.Provider == ProviderOpenAI && c.config.APIMode == "responses") {
		return nil, fmt.Errorf("当前 API 模式不支持 Eino 工具调用，请切换到 Chat Completions 模式")
	}

	reqBody := ChatRequest{
		Model:       c.config.Model,
		Messages:    messages,
		Tools:       tools,
		MaxTokens:   c.config.MaxTokens,
		Temperature: c.config.Temperature,
		Stream:      false,
	}
	if options.Model != nil && *options.Model != "" {
		reqBody.Model = *options.Model
	}
	if options.MaxTokens != nil {
		reqBody.MaxTokens = *options.MaxTokens
	}
	if options.Temperature != nil {
		reqBody.Temperature = float64(*options.Temperature)
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := newAPIRequest(ctx, c.config.BaseURL+"/chat/completions", c.config.APIKey, body)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &DiagnosticsError{Diagnostics: CallDiagnostics{MaxTokens: reqBody.MaxTokens}, Err: c.requestError("API 请求失败", err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, &DiagnosticsError{Diagnostics: CallDiagnostics{MaxTokens: reqBody.MaxTokens, HTTPStatus: resp.StatusCode, RequestID: responseRequestID(resp.Header)}, Err: readAPIError(resp)}
	}
	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, &DiagnosticsError{Diagnostics: CallDiagnostics{MaxTokens: reqBody.MaxTokens, HTTPStatus: resp.StatusCode, RequestID: responseRequestID(resp.Header)}, Err: fmt.Errorf("响应 JSON 无效: %w", err)}
	}
	if len(chatResp.Choices) == 0 {
		return nil, &DiagnosticsError{Diagnostics: CallDiagnostics{MaxTokens: reqBody.MaxTokens, HTTPStatus: resp.StatusCode, RequestID: responseRequestID(resp.Header), FinishReason: "empty_choices"}, Err: fmt.Errorf("AI 未返回任何回复")}
	}
	choice := chatResp.Choices[0].Message
	calls := make([]schema.ToolCall, 0, len(choice.ToolCalls))
	for index, call := range choice.ToolCalls {
		callType := call.Type
		if callType == "" {
			callType = "function"
		}
		calls = append(calls, schema.ToolCall{
			Index: &index,
			ID:    call.ID,
			Type:  callType,
			Function: schema.FunctionCall{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
		})
	}
	message := schema.AssistantMessage(choice.Content, calls)
	diagnostics := CallDiagnostics{
		FinishReason: chatResp.Choices[0].FinishReason,
		MaxTokens:    reqBody.MaxTokens, ToolCallBytes: toolCallArgumentBytes(choice.ToolCalls),
		HTTPStatus: resp.StatusCode, RequestID: responseRequestID(resp.Header),
	}
	message.Extra = map[string]any{DiagnosticsExtraKey: diagnostics}
	if finishReason := diagnostics.FinishReason; finishReason != "" {
		message.ResponseMeta = &schema.ResponseMeta{FinishReason: finishReason}
	}
	return message, nil
}

func toolCallArgumentBytes(calls []ChatToolCall) int {
	bytes := 0
	for _, call := range calls {
		bytes += len(call.Function.Arguments)
	}
	return bytes
}

func responseRequestID(headers http.Header) string {
	for _, name := range []string{"x-request-id", "request-id", "openai-request-id"} {
		if value := strings.TrimSpace(headers.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

// MessageDiagnostics returns protocol metadata attached by EinoChatModel.
func MessageDiagnostics(message *schema.Message) (CallDiagnostics, bool) {
	if message == nil || message.Extra == nil {
		return CallDiagnostics{}, false
	}
	value, ok := message.Extra[DiagnosticsExtraKey]
	if !ok {
		return CallDiagnostics{}, false
	}
	diagnostics, ok := value.(CallDiagnostics)
	return diagnostics, ok
}
