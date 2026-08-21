// owner: muswood | Email: mumu920@outlook.com
package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type httpRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn httpRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func httpResponse(status int, contentType, body string, headers http.Header) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}
	headers.Set("Content-Type", contentType)
	return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body))}
}

func TestHTTPClientUsesBearerSessionAndSSE(t *testing.T) {
	client, err := NewHTTPClient(context.Background(), HTTPConfig{Endpoint: "https://mcp.example.test/mcp", OAuthAccessToken: "access-token", HTTPClient: &http.Client{Transport: httpRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer access-token" {
			return httpResponse(http.StatusUnauthorized, "application/json", `{"error":"unauthorized"}`, nil), nil
		}
		var message rpcRequest
		if err := json.NewDecoder(request.Body).Decode(&message); err != nil {
			return nil, err
		}
		headers := http.Header{"Mcp-Session-Id": []string{"session-1"}}
		switch message.Method {
		case "initialize":
			response := map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": map[string]any{"protocolVersion": "2024-11-05"}}
			data, _ := json.Marshal(response)
			return httpResponse(http.StatusOK, "application/json", string(data), headers), nil
		case "notifications/initialized":
			return httpResponse(http.StatusAccepted, "", "", headers), nil
		case "tools/list":
			response := map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": map[string]any{"tools": []any{map[string]any{"name": "echo", "description": "echo", "inputSchema": map[string]any{"type": "object"}}}}}
			data, _ := json.Marshal(response)
			return httpResponse(http.StatusOK, "text/event-stream", "event: message\ndata: "+string(data)+"\n\n", headers), nil
		default:
			return httpResponse(http.StatusNotFound, "", "", nil), nil
		}
	})}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	tools, err := client.ListTools(context.Background())
	if err != nil || len(tools) != 1 {
		t.Fatalf("HTTP tools/list failed: %v %#v", err, tools)
	}
	if got := client.sessionID; got != "session-1" {
		t.Fatalf("session ID = %q, want session-1", got)
	}
}

func TestHTTPClientDiscoversOAuthMetadataOnUnauthorized(t *testing.T) {
	_, err := NewHTTPClient(context.Background(), HTTPConfig{Endpoint: "https://mcp.example.test/mcp", HTTPClient: &http.Client{Transport: httpRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/mcp" {
			return httpResponse(http.StatusUnauthorized, "", "", nil), nil
		}
		return httpResponse(http.StatusOK, "application/json", `{"resource":"resource","authorization_servers":["https://issuer.example"]}`, nil), nil
	})}})
	if err == nil || !strings.Contains(err.Error(), "authorization servers=https://issuer.example") {
		t.Fatalf("unauthorized HTTP MCP did not expose OAuth discovery: %v", err)
	}
}

func TestDiscoverAuthorizationServer(t *testing.T) {
	metadata, err := DiscoverAuthorizationServer(context.Background(), "https://issuer.example", &http.Client{Transport: httpRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return httpResponse(http.StatusOK, "application/json", `{"issuer":"issuer","authorization_endpoint":"https://issuer.example/authorize","token_endpoint":"https://issuer.example/token"}`, nil), nil
	})})
	if err != nil || metadata.TokenEndpoint != "https://issuer.example/token" {
		t.Fatalf("authorization metadata discovery failed: %#v %v", metadata, err)
	}
}
