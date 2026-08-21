// owner: muswood | Email: mumu920@outlook.com
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

func TestMCPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request rpcRequest
		if json.Unmarshal(scanner.Bytes(), &request) != nil || request.ID == 0 {
			continue
		}
		var result any
		switch request.Method {
		case "gossh/authenticate":
			var params struct {
				BearerToken string `json:"bearerToken"`
			}
			raw, _ := json.Marshal(request.Params)
			_ = json.Unmarshal(raw, &params)
			result = map[string]any{"authenticated": params.BearerToken != "" && params.BearerToken == os.Getenv("GO_MCP_AUTH_TOKEN")}
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05"}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{
				"name": "echo", "description": "Echo input", "inputSchema": map[string]any{
					"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string", "required": true}}, "required": []string{"text"},
				},
			}}}
		case "tools/call":
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "echoed"}}}
		default:
			result = map[string]any{}
		}
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}
		data, _ := json.Marshal(response)
		fmt.Printf("%s\n", data)
	}
}

func TestClientAuthenticatesConfiguredTokenBeforeToolDiscovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := NewClient(ctx, Config{Command: os.Args[0], Args: []string{"-test.run=TestMCPHelperProcess", "--"}, Env: []string{"GO_WANT_MCP_HELPER=1", "GO_MCP_AUTH_TOKEN=shared-secret"}, AuthToken: "shared-secret"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.ListTools(ctx); err != nil {
		t.Fatalf("authenticated client could not list tools: %v", err)
	}
}

func TestClientRejectsInvalidAuthenticationToken(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := NewClient(ctx, Config{Command: os.Args[0], Args: []string{"-test.run=TestMCPHelperProcess", "--"}, Env: []string{"GO_WANT_MCP_HELPER=1", "GO_MCP_AUTH_TOKEN=shared-secret"}, AuthToken: "wrong-secret"})
	if err == nil {
		t.Fatal("client accepted an invalid MCP authentication token")
	}
}

func TestClientListsAndCallsTools(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := NewClient(ctx, Config{Command: os.Args[0], Args: []string{"-test.run=TestMCPHelperProcess", "--"}, Env: []string{"GO_WANT_MCP_HELPER=1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	tools, err := client.ListTools(ctx)
	if err != nil || len(tools) != 1 {
		t.Fatalf("list tools: %v %#v", err, tools)
	}
	info, err := tools[0].Info(ctx)
	if err != nil || info.Name != "mcp_echo" {
		t.Fatalf("tool info: %v %#v", err, info)
	}
	invokable, ok := tools[0].(tool.InvokableTool)
	if !ok {
		t.Fatal("MCP tool is not invokable")
	}
	output, err := invokable.InvokableRun(ctx, `{"text":"hello"}`)
	if err != nil || output == "" {
		t.Fatalf("tool call: %v %s", err, output)
	}
}

type aclTestTool struct{}

func (aclTestTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "targeted", Desc: "test"}, nil
}

func (aclTestTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	return "ok", nil
}

func TestTargetACL(t *testing.T) {
	wrapped := WrapTargetACL(aclTestTool{}, []string{"target-a"}).(tool.InvokableTool)
	if _, err := wrapped.InvokableRun(context.Background(), `{"targetId":"target-b"}`); err == nil {
		t.Fatal("unauthorized target was accepted")
	}
	if _, err := wrapped.InvokableRun(context.Background(), `{"targetId":"target-a"}`); err != nil {
		t.Fatalf("authorized target rejected: %v", err)
	}
}
