// owner: muswood | Email: mumu920@outlook.com
package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// WrapTargetACL prevents an MCP tool from selecting targets outside the
// server's configured scope. Calls without an explicit target are rejected
// when a scope is configured, so the caller cannot silently fall back to a
// broader default target.
func WrapTargetACL(candidate tool.BaseTool, allowed []string) tool.BaseTool {
	if len(allowed) == 0 {
		return candidate
	}
	invokable, ok := candidate.(tool.InvokableTool)
	if !ok {
		return candidate
	}
	wanted := make(map[string]bool, len(allowed))
	for _, id := range allowed {
		if id != "" {
			wanted[id] = true
		}
	}
	return &targetACLTool{tool: invokable, wanted: wanted}
}

type targetACLTool struct {
	tool   tool.InvokableTool
	wanted map[string]bool
}

func (t *targetACLTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.tool.Info(ctx)
}

func (t *targetACLTool) InvokableRun(ctx context.Context, argumentsInJSON string, options ...tool.Option) (string, error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("MCP 工具参数无效: %w", err)
	}
	ids := make([]string, 0, 1)
	if value, ok := args["targetId"].(string); ok && value != "" {
		ids = append(ids, value)
	}
	if values, ok := args["targetIds"].([]any); ok {
		for _, value := range values {
			if id, ok := value.(string); ok && id != "" {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("MCP 工具必须明确提供 targetId 或 targetIds")
	}
	for _, id := range ids {
		if !t.wanted[id] {
			return "", fmt.Errorf("MCP 工具目标未获授权: %s", id)
		}
	}
	return t.tool.InvokableRun(ctx, argumentsInJSON, options...)
}
