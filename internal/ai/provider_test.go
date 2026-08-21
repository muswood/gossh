// owner: muswood | Email: mumu920@outlook.com
package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestQwenDefaults(t *testing.T) {
	client := NewClient(Config{Provider: ProviderQwen})
	if client.config.Model != "qwen-plus" {
		t.Fatalf("unexpected model: %s", client.config.Model)
	}
	if client.config.BaseURL != "https://dashscope.aliyuncs.com/compatible-mode/v1" {
		t.Fatalf("unexpected base URL: %s", client.config.BaseURL)
	}
	if client.http.Timeout != 180*time.Second {
		t.Fatalf("unexpected HTTP timeout: %s", client.http.Timeout)
	}
}

func TestCustomProviderUsesOpenAICompatibleDefaults(t *testing.T) {
	client := NewClient(Config{Provider: Provider("ollama"), Model: "qwen2.5:7b", BaseURL: "http://localhost:11434/v1"})
	if client.config.Provider != Provider("ollama") {
		t.Fatalf("unexpected provider: %s", client.config.Provider)
	}
	if client.config.Model != "qwen2.5:7b" {
		t.Fatalf("unexpected model: %s", client.config.Model)
	}
	if client.config.BaseURL != "http://localhost:11434/v1" {
		t.Fatalf("unexpected base URL: %s", client.config.BaseURL)
	}
	if client.config.APIMode != "chat" {
		t.Fatalf("unexpected API mode: %s", client.config.APIMode)
	}
}

func TestNewClientUpgradesLegacyMaxTokensDefault(t *testing.T) {
	client := NewClient(Config{Provider: ProviderDeepSeek, MaxTokens: 2000})
	if client.config.MaxTokens != 393216 {
		t.Fatalf("legacy max tokens was not upgraded: %d", client.config.MaxTokens)
	}
}

func TestParseCommandAssessment(t *testing.T) {
	assessment, err := parseCommandAssessment("```json\n{\"allowed\":true,\"readOnly\":true,\"mutating\":false,\"deleting\":false,\"risk\":\"low\",\"reason\":\"只读状态查询\"}\n```")
	if err != nil {
		t.Fatalf("parse command assessment: %v", err)
	}
	if !assessment.Allowed || !assessment.ReadOnly || assessment.Mutating || assessment.Deleting {
		t.Fatalf("unexpected command assessment: %#v", assessment)
	}
}

func TestParseCommandAssessmentRejectsContradictoryDeletion(t *testing.T) {
	_, err := parseCommandAssessment(`{"allowed":true,"readOnly":true,"mutating":false,"deleting":true,"risk":"high","reason":"delete"}`)
	if err == nil {
		t.Fatal("contradictory deletion assessment was accepted")
	}
}

func TestCustomProviderUsesChatCompletions(t *testing.T) {
	client := NewClient(Config{
		Provider: "local-gateway",
		Model:    "local-model",
		APIKey:   "test-key",
		BaseURL:  "http://example.test/v1",
	})
	client.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("unexpected authorization header: %s", r.Header.Get("Authorization"))
		}
		var request ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Model != "local-model" {
			t.Fatalf("unexpected model: %s", request.Model)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"custom provider works"}}]}`)),
			Header:     make(http.Header),
		}, nil
	})}

	reply, err := client.Chat(context.Background(), []Message{{Role: "user", Content: "hello"}})
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}
	if reply != "custom provider works" {
		t.Fatalf("unexpected reply: %q", reply)
	}
}

func TestOpenAICompatibleEmbeddings(t *testing.T) {
	client := NewClient(Config{Provider: "local-gateway", Model: "local-model", EmbeddingModel: "embed-model", APIKey: "test-key", BaseURL: "http://example.test/v1"})
	client.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/embeddings" {
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("unexpected authorization header: %s", r.Header.Get("Authorization"))
		}
		var request EmbeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != "embed-model" || len(request.Input) != 2 {
			t.Fatalf("unexpected embedding request: %#v", request)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":[{"index":1,"embedding":[0.3,0.4]},{"index":0,"embedding":[0.1,0.2]}]}`)), Header: make(http.Header)}, nil
	})}
	vectors, err := client.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 2 || vectors[0][0] != 0.1 || vectors[1][1] != 0.4 {
		t.Fatalf("unexpected embedding vectors: %#v", vectors)
	}
}

func TestResponseTextExtractsOutputText(t *testing.T) {
	text := responseText(&responsesResponse{Output: []struct {
		Type      string `json:"type"`
		Role      string `json:"role"`
		Name      string `json:"name"`
		CallID    string `json:"call_id"`
		Arguments string `json:"arguments"`
		Content   []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}{
		{Type: "message", Content: []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{{Type: "output_text", Text: "hello"}}},
	}})
	if text != "hello" {
		t.Fatalf("unexpected response text: %q", text)
	}
}
