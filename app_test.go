// owner: muswood | Email: mumu920@outlook.com
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"gossh/internal/tcp"
)

func TestDecodeSessionLogBytesPreservesRawBytes(t *testing.T) {
	want := []byte{0x00, 0x1b, 0xff, '\n', '\r'}
	got, err := decodeSessionLogBytes(base64.StdEncoding.EncodeToString(want))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("decoded bytes = %v, want %v", got, want)
	}
}

func TestDecodeSessionLogBytesRejectsMalformedBase64(t *testing.T) {
	if _, err := decodeSessionLogBytes("not-base64"); err == nil {
		t.Fatal("malformed base64 was accepted")
	}
}

func TestRunAgentTelnetTerminalWritesApprovedCommandAndReturnsOutput(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		if errors.Is(err, syscall.EPERM) {
			t.Skip("sandbox does not permit TCP listeners")
		}
		t.Fatal(err)
	}
	defer listener.Close()
	received := make(chan string, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		buffer := make([]byte, 64)
		n, _ := conn.Read(buffer)
		received <- string(buffer[:n])
		_, _ = conn.Write([]byte("command output\r\n"))
	}()

	manager := tcp.NewManager()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var portNumber int
	if _, err := fmt.Sscanf(port, "%d", &portNumber); err != nil {
		t.Fatal(err)
	}
	sessionID, err := manager.Connect("telnet", "127.0.0.1", portNumber, tcp.ProtocolTelnet)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Disconnect(sessionID)

	result := runAgentTelnetTerminal(context.Background(), sessionID, "show status", manager, nil)
	if result.Status != "ok" || result.Output != "command output\r\n" {
		t.Fatalf("unexpected Telnet command result: %#v", result)
	}
	select {
	case command := <-received:
		if command != "show status\r" {
			t.Fatalf("Telnet received %q, want approved command followed by carriage return", command)
		}
	case <-time.After(time.Second):
		t.Fatal("Telnet server did not receive command")
	}
}

type filterTestTool struct{ name string }

func (t filterTestTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name, Desc: "test"}, nil
}

func (t filterTestTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	return t.name, nil
}

func TestFilterMCPToolsAllowsAllToolsWhenOnlyTargetACLIsConfigured(t *testing.T) {
	filtered := filterMCPTools(context.Background(), []tool.BaseTool{
		filterTestTool{name: "alpha"},
		filterTestTool{name: "beta"},
	}, nil, []string{"target-a"})
	if len(filtered) != 2 {
		t.Fatalf("target-only ACL unexpectedly filtered tools: %d", len(filtered))
	}
	for _, candidate := range filtered {
		invokable, ok := candidate.(tool.InvokableTool)
		if !ok {
			t.Fatalf("filtered tool is not invokable: %T", candidate)
		}
		if _, err := invokable.InvokableRun(context.Background(), `{"targetId":"target-b"}`); err == nil {
			t.Fatal("target ACL allowed an unauthorized target")
		}
		if _, err := invokable.InvokableRun(context.Background(), `{"targetId":"target-a"}`); err != nil {
			t.Fatalf("target ACL rejected an authorized target: %v", err)
		}
	}
}

func TestFilterMCPToolsAppliesToolAllowlist(t *testing.T) {
	filtered := filterMCPTools(context.Background(), []tool.BaseTool{
		filterTestTool{name: "alpha"},
		filterTestTool{name: "beta"},
	}, []string{"beta"}, nil)
	if len(filtered) != 1 {
		t.Fatalf("tool allowlist returned %d tools, want 1", len(filtered))
	}
	info, err := filtered[0].Info(context.Background())
	if err != nil || info.Name != "beta" {
		t.Fatalf("unexpected allowlisted tool: %#v %v", info, err)
	}
}
