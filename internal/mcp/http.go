// owner: muswood | Email: mumu920@outlook.com
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
)

// HTTPConfig configures the MCP Streamable HTTP transport. OAuth is supplied
// as a previously issued access token; interactive authorization belongs to
// the host application's browser/account flow.
type HTTPConfig struct {
	Endpoint         string
	OAuthAccessToken string
	Timeout          time.Duration
	HTTPClient       *http.Client
}

type HTTPClient struct {
	endpoint    string
	accessToken string
	http        *http.Client
	mu          sync.Mutex
	sessionID   string
	closed      bool
	nextID      int64
}

type ProtectedResourceMetadata struct {
	Resource             string   `json:"resource,omitempty"`
	AuthorizationServers []string `json:"authorization_servers,omitempty"`
}

type AuthorizationServerMetadata struct {
	Issuer                string   `json:"issuer,omitempty"`
	AuthorizationEndpoint string   `json:"authorization_endpoint,omitempty"`
	TokenEndpoint         string   `json:"token_endpoint,omitempty"`
	CodeChallengeMethods  []string `json:"code_challenge_methods_supported,omitempty"`
}

func NewHTTPClient(ctx context.Context, cfg HTTPConfig) (*HTTPClient, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("MCP HTTP endpoint 必须是 http 或 https URL")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: cfg.Timeout}
	}
	client := &HTTPClient{endpoint: endpoint, accessToken: strings.TrimSpace(cfg.OAuthAccessToken), http: httpClient}
	if _, err := client.request(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "gossh", "version": "1"},
	}); err != nil {
		return nil, err
	}
	if err := client.notify(ctx, "notifications/initialized", nil); err != nil {
		return nil, err
	}
	return client, nil
}

func (c *HTTPClient) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("MCP HTTP client 已关闭")
	}
	c.nextID++
	id := c.nextID
	sessionID := c.sessionID
	c.mu.Unlock()
	payload, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.accessToken)
	}
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("MCP HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()
	if value := resp.Header.Get("Mcp-Session-Id"); value != "" {
		c.mu.Lock()
		c.sessionID = value
		c.mu.Unlock()
	}
	if resp.StatusCode == http.StatusUnauthorized {
		metadata, metadataErr := DiscoverOAuth(ctx, c.endpoint, c.http)
		if metadataErr != nil {
			return nil, fmt.Errorf("MCP HTTP 认证失败 (401)，且无法发现 OAuth 元数据: %w", metadataErr)
		}
		return nil, fmt.Errorf("MCP HTTP 认证失败 (401)，请使用 OAuth access token；authorization servers=%s", strings.Join(metadata.AuthorizationServers, ","))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		return nil, fmt.Errorf("MCP HTTP 返回 %d: %s", resp.StatusCode, string(data))
	}
	data, err := readHTTPRPCResponse(resp)
	if err != nil {
		return nil, err
	}
	var response rpcResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("解析 MCP HTTP JSON-RPC 响应失败: %w", err)
	}
	if response.ID != id {
		return nil, fmt.Errorf("MCP HTTP 响应 ID 不匹配: got %d want %d", response.ID, id)
	}
	if response.Error != nil {
		return nil, fmt.Errorf("MCP HTTP %s 失败 (%d): %s", method, response.Error.Code, response.Error.Message)
	}
	return response.Result, nil
}

func (c *HTTPClient) notify(ctx context.Context, method string, params any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	payload, err := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("MCP HTTP client 已关闭")
	}
	sessionID := c.sessionID
	c.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.accessToken)
	}
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("MCP HTTP notification 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return errors.New("MCP HTTP notification 未通过 OAuth 认证")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("MCP HTTP notification 返回 %d: %s", resp.StatusCode, string(data))
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return nil
}

func (c *HTTPClient) ListTools(ctx context.Context) ([]tool.BaseTool, error) {
	result, err := c.request(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var listed listToolsResult
	if err := json.Unmarshal(result, &listed); err != nil {
		return nil, fmt.Errorf("解析 MCP HTTP tools/list 失败: %w", err)
	}
	tools := make([]tool.BaseTool, 0, len(listed.Tools))
	for _, description := range listed.Tools {
		if description.Name != "" {
			tools = append(tools, &Tool{client: c, description: description})
		}
	}
	return tools, nil
}

func (c *HTTPClient) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return nil
}

func readHTTPRPCResponse(resp *http.Response) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("读取 MCP HTTP 响应失败: %w", err)
	}
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		var last []byte
		scanner := bufio.NewScanner(bytes.NewReader(data))
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data:") {
				last = []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		if len(last) == 0 {
			return nil, errors.New("MCP HTTP SSE 响应没有 data")
		}
		return last, nil
	}
	return data, nil
}

func DiscoverOAuth(ctx context.Context, endpoint string, client *http.Client) (*ProtectedResourceMetadata, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	metadataURL := *parsed
	metadataURL.Path = "/.well-known/oauth-protected-resource"
	metadataURL.RawQuery = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("protected resource metadata 返回 %d", resp.StatusCode)
	}
	var metadata ProtectedResourceMetadata
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&metadata); err != nil {
		return nil, err
	}
	if len(metadata.AuthorizationServers) == 0 {
		return nil, errors.New("OAuth 元数据缺少 authorization_servers")
	}
	return &metadata, nil
}

func DiscoverAuthorizationServer(ctx context.Context, issuer string, client *http.Client) (*AuthorizationServerMetadata, error) {
	parsed, err := url.Parse(issuer)
	if err != nil {
		return nil, err
	}
	metadataURL := *parsed
	metadataURL.Path = strings.TrimRight(parsed.Path, "/") + "/.well-known/oauth-authorization-server"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("authorization server metadata 返回 %d", resp.StatusCode)
	}
	var metadata AuthorizationServerMetadata
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&metadata); err != nil {
		return nil, err
	}
	return &metadata, nil
}
