// owner: muswood | Email: mumu920@outlook.com
package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func TestGenerateEinoPreservesToolCallProtocol(t *testing.T) {
	client := NewClient(Config{
		Provider: ProviderOpenAI,
		Model:    "test-model",
		APIKey:   "test-key",
		BaseURL:  "http://example.test/v1",
	})
	client.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var request ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(request.Messages) != 3 {
			t.Fatalf("unexpected message count: %d", len(request.Messages))
		}
		call := request.Messages[1].ToolCalls
		if len(call) != 1 || call[0].ID != "call_123" || call[0].Type != "function" {
			t.Fatalf("assistant tool call was not preserved: %#v", call)
		}
		if call[0].Function.Name != "inspect_host" || call[0].Function.Arguments != `{"host":"web-01"}` {
			t.Fatalf("unexpected tool call payload: %#v", call[0].Function)
		}
		result := request.Messages[2]
		if result.Role != "tool" || result.ToolCallID != "call_123" || result.Content != "host is healthy" {
			t.Fatalf("tool result is missing its call ID: %#v", result)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"stop","message":{"content":"analysis complete"}}]}`)),
			Header:     make(http.Header),
		}, nil
	})}

	result, err := client.generateEino(context.Background(), []*schema.Message{
		schema.UserMessage("inspect web-01"),
		schema.AssistantMessage("", []schema.ToolCall{{
			ID:   "call_123",
			Type: "function",
			Function: schema.FunctionCall{
				Name:      "inspect_host",
				Arguments: `{"host":"web-01"}`,
			},
		}}),
		schema.ToolMessage("host is healthy", "call_123", schema.WithToolName("inspect_host")),
	}, &model.Options{Tools: []*schema.ToolInfo{{Name: "inspect_host"}}})
	if err != nil {
		t.Fatalf("generate eino: %v", err)
	}
	if result.Content != "analysis complete" {
		t.Fatalf("unexpected result: %q", result.Content)
	}
	if result.ResponseMeta == nil || result.ResponseMeta.FinishReason != "stop" {
		t.Fatalf("finish reason was not preserved: %#v", result.ResponseMeta)
	}
}

func TestGenerateEinoPreservesLengthFinishReason(t *testing.T) {
	client := NewClient(Config{Provider: ProviderOpenAI, Model: "test-model", APIKey: "test-key", BaseURL: "http://example.test/v1"})
	client.http = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"length","message":{"content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"terminal_command","arguments":"{\"command\":\"uptime\""}}]}}]}`)),
			Header:     make(http.Header),
		}, nil
	})}
	result, err := client.generateEino(context.Background(), []*schema.Message{schema.UserMessage("check")}, &model.Options{
		Tools: []*schema.ToolInfo{{Name: "terminal_command"}},
	})
	if err != nil {
		t.Fatalf("generate eino: %v", err)
	}
	if result.ResponseMeta == nil || result.ResponseMeta.FinishReason != "length" {
		t.Fatalf("length finish reason was not preserved: %#v", result.ResponseMeta)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Function.Arguments == "" {
		t.Fatalf("tool call was not preserved: %#v", result.ToolCalls)
	}
}

func TestForceToolCallRequiresTheRequestedTool(t *testing.T) {
	client := NewClient(Config{Provider: ProviderOpenAI, Model: "test-model", APIKey: "test-key", BaseURL: "http://example.test/v1"})
	client.http = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var body ChatRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(body.Tools) != 1 || body.Tools[0].Function.Name != "report" {
			t.Fatalf("unexpected tools: %#v", body.Tools)
		}
		return &http.Response{StatusCode: http.StatusOK,
			Body:   io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"tool_calls","message":{"tool_calls":[{"id":"report-call","type":"function","function":{"name":"report","arguments":"{\"title\":\"check\"}"}}]}}]}`)),
			Header: http.Header{"X-Request-Id": []string{"gw-123"}}}, nil
	})}
	call, diagnostics, err := client.ForceToolCall(context.Background(), []Message{{Role: "user", Content: "check"}}, Tool{Name: "report", Parameters: map[string]interface{}{}}, "report")
	if err != nil {
		t.Fatalf("force tool call: %v", err)
	}
	if call.ID != "report-call" || call.Function.Name != "report" || call.Function.Arguments != `{"title":"check"}` {
		t.Fatalf("unexpected report call: %#v", call)
	}
	if diagnostics.FinishReason != "tool_calls" || diagnostics.ToolCallBytes == 0 || diagnostics.HTTPStatus != http.StatusOK || diagnostics.RequestID != "gw-123" {
		t.Fatalf("unexpected diagnostics: %#v", diagnostics)
	}
}

func TestGenerateEinoRejectsToolResultWithoutCallID(t *testing.T) {
	client := NewClient(Config{Provider: ProviderOpenAI, Model: "test-model", BaseURL: "http://example.test/v1"})
	_, err := client.generateEino(context.Background(), []*schema.Message{
		{Role: schema.Tool, Content: "tool output"},
	}, &model.Options{Tools: []*schema.ToolInfo{{Name: "inspect_host"}}})
	if err == nil || !strings.Contains(err.Error(), "tool_call_id") {
		t.Fatalf("expected missing tool_call_id error, got %v", err)
	}
}
