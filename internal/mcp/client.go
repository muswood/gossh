// owner: muswood | Email: mumu920@outlook.com
// Package mcp provides a small, explicitly configured MCP stdio client.
// It intentionally does not discover or launch servers from model input.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type Config struct {
	Command   string
	Args      []string
	Env       []string
	AuthToken string
	Timeout   time.Duration
}

type Client struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	mu      sync.Mutex
	nextID  int64
	closed  bool
	timeout time.Duration
}

type rpcRequester interface {
	request(context.Context, string, any) (json.RawMessage, error)
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolDescription struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type listToolsResult struct {
	Tools []toolDescription `json:"tools"`
}

type callToolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
		Data any    `json:"data,omitempty"`
	} `json:"content,omitempty"`
	IsError bool `json:"isError,omitempty"`
}

func NewClient(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Command == "" {
		return nil, errors.New("MCP command 不能为空")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	cmd.Env = append(os.Environ(), cfg.Env...)
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("创建 MCP stdin 失败: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("创建 MCP stdout 失败: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("启动 MCP server 失败: %w", err)
	}
	c := &Client{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout), timeout: cfg.Timeout}
	initCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	if cfg.AuthToken != "" {
		if err := c.authenticate(initCtx, cfg.AuthToken); err != nil {
			_ = c.Close()
			return nil, err
		}
	}
	if _, err := c.request(initCtx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "gossh", "version": "1"},
	}); err != nil {
		_ = c.Close()
		return nil, err
	}
	if err := c.notify("notifications/initialized", nil); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// authenticate is a fail-closed GoSSH stdio extension. MCP's standard
// authorization flow targets HTTP transports; a child stdio process has no
// equivalent challenge endpoint. A configured token therefore requires this
// explicit handshake before initialize or any tool discovery/call is allowed.
func (c *Client) authenticate(ctx context.Context, token string) error {
	result, err := c.request(ctx, "gossh/authenticate", map[string]any{"bearerToken": token})
	if err != nil {
		return fmt.Errorf("MCP 协议认证失败: %w", err)
	}
	var response struct {
		Authenticated bool `json:"authenticated"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		return fmt.Errorf("解析 MCP 认证响应失败: %w", err)
	}
	if !response.Authenticated {
		return errors.New("MCP 协议认证被拒绝")
	}
	return nil
}

func (c *Client) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("MCP client 已关闭")
	}
	c.nextID++
	req := rpcRequest{JSONRPC: "2.0", ID: c.nextID, Method: method, Params: params}
	if err := writeJSON(c.stdin, req); err != nil {
		return nil, err
	}
	for {
		lineCh := make(chan []byte, 1)
		errCh := make(chan error, 1)
		go func() {
			line, err := c.stdout.ReadBytes('\n')
			if err != nil {
				errCh <- err
				return
			}
			lineCh <- line
		}()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case err := <-errCh:
			return nil, fmt.Errorf("读取 MCP response 失败: %w", err)
		case line := <-lineCh:
			var response rpcResponse
			if err := json.Unmarshal(line, &response); err != nil {
				continue
			}
			if response.ID != req.ID {
				continue
			}
			if response.Error != nil {
				return nil, fmt.Errorf("MCP %s 失败 (%d): %s", method, response.Error.Code, response.Error.Message)
			}
			return response.Result, nil
		}
	}
}

func (c *Client) notify(method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("MCP client 已关闭")
	}
	return writeJSON(c.stdin, rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
}

func writeJSON(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

func (c *Client) ListTools(ctx context.Context) ([]tool.BaseTool, error) {
	result, err := c.request(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var listed listToolsResult
	if err := json.Unmarshal(result, &listed); err != nil {
		return nil, fmt.Errorf("解析 MCP tools/list 失败: %w", err)
	}
	tools := make([]tool.BaseTool, 0, len(listed.Tools))
	for _, description := range listed.Tools {
		if description.Name == "" {
			continue
		}
		tools = append(tools, &Tool{client: c, description: description})
	}
	return tools, nil
}

func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	_ = c.stdin.Close()
	c.mu.Unlock()
	return c.cmd.Wait()
}

type Tool struct {
	client      rpcRequester
	description toolDescription
}

func (t *Tool) Info(context.Context) (*schema.ToolInfo, error) {
	params := schemaParams(t.description.InputSchema)
	return &schema.ToolInfo{Name: exposedName(t.description.Name), Desc: t.description.Description,
		ParamsOneOf: schema.NewParamsOneOfByParams(params)}, nil
}

func (t *Tool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var arguments map[string]any
	if argumentsInJSON == "" {
		arguments = map[string]any{}
	} else if err := json.Unmarshal([]byte(argumentsInJSON), &arguments); err != nil {
		return "", fmt.Errorf("MCP 工具参数无效: %w", err)
	}
	result, err := t.client.request(ctx, "tools/call", map[string]any{"name": t.description.Name, "arguments": arguments})
	if err != nil {
		return "", err
	}
	var called callToolResult
	if err := json.Unmarshal(result, &called); err != nil {
		return "", fmt.Errorf("解析 MCP tools/call 失败: %w", err)
	}
	parts := make([]string, 0, len(called.Content))
	for _, item := range called.Content {
		if item.Text != "" {
			parts = append(parts, item.Text)
		} else if item.Data != nil {
			raw, _ := json.Marshal(item.Data)
			parts = append(parts, string(raw))
		}
	}
	output := string(mustJSON(map[string]any{"content": parts, "isError": called.IsError}))
	if called.IsError {
		return output, fmt.Errorf("MCP 工具 %s 返回错误", exposedName(t.description.Name))
	}
	return output, nil
}

func exposedName(remoteName string) string {
	return "mcp_" + remoteName
}

func schemaParams(input map[string]any) map[string]*schema.ParameterInfo {
	params := make(map[string]*schema.ParameterInfo)
	properties, _ := input["properties"].(map[string]any)
	required := make(map[string]bool)
	if values, ok := input["required"].([]any); ok {
		for _, value := range values {
			if name, ok := value.(string); ok {
				required[name] = true
			}
		}
	}
	for name, raw := range properties {
		property, _ := raw.(map[string]any)
		params[name] = schemaParameter(property, required[name])
	}
	return params
}

func schemaParameter(property map[string]any, required bool) *schema.ParameterInfo {
	dataType, _ := property["type"].(string)
	if dataType == "" {
		dataType = "string"
	}
	parameter := &schema.ParameterInfo{Type: schema.DataType(dataType), Required: required}
	parameter.Desc, _ = property["description"].(string)
	if values, ok := property["enum"].([]any); ok {
		for _, value := range values {
			if item, ok := value.(string); ok {
				parameter.Enum = append(parameter.Enum, item)
			}
		}
	}
	if dataType == "array" {
		items, _ := property["items"].(map[string]any)
		parameter.ElemInfo = schemaParameter(items, false)
	}
	return parameter
}

func mustJSON(value any) []byte {
	raw, _ := json.Marshal(value)
	return raw
}
