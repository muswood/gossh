// owner: muswood | Email: mumu920@outlook.com
package tcp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestRejectsInvalidTCPConnection(t *testing.T) {
	m := NewManager()
	if _, err := m.Connect("test", "", 23, ProtocolTelnet); err == nil {
		t.Fatal("expected invalid host error")
	}
	if _, err := m.Connect("test", "example.com", 23, Protocol("smtp")); err == nil {
		t.Fatal("expected unsupported protocol error")
	}
}

func TestManagerProtocolReturnsActualSessionProtocol(t *testing.T) {
	manager := NewManager()
	manager.sessions["raw"] = &Session{ID: "raw", Protocol: ProtocolRaw}
	manager.sessions["telnet"] = &Session{ID: "telnet", Protocol: ProtocolTelnet}

	if protocol, ok := manager.Protocol("raw"); !ok || protocol != ProtocolRaw {
		t.Fatalf("raw protocol = %q, found=%v", protocol, ok)
	}
	if protocol, ok := manager.Protocol("telnet"); !ok || protocol != ProtocolTelnet {
		t.Fatalf("telnet protocol = %q, found=%v", protocol, ok)
	}
	if _, ok := manager.Protocol("missing"); ok {
		t.Fatal("missing session reported a protocol")
	}
}

func TestDetectPromptIgnoresANSISequences(t *testing.T) {
	if got := detectPrompt("\x1b[32mRouter#\x1b[0m"); got != "Router#" {
		t.Fatalf("ANSI prompt = %q, want Router#", got)
	}
}

func TestTelnetFilterHandlesSplitNegotiationAndSubnegotiation(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	manager := NewManager()
	session := &Session{Protocol: ProtocolTelnet, conn: client}
	responseCh := make(chan []byte, 1)
	go func() {
		response := make([]byte, 23)
		_, _ = io.ReadFull(server, response)
		responseCh <- response
	}()

	if got := manager.filterTelnet(session, []byte{255}); len(got) != 0 {
		t.Fatalf("split IAC leaked into terminal output: %v", got)
	}
	if got := manager.filterTelnet(session, []byte{253, 1, 'o', 'k', 255, 250, 24}); string(got) != "ok" {
		t.Fatalf("unexpected telnet output before split subnegotiation: %q", got)
	}
	if got := manager.filterTelnet(session, []byte{1, 255, 240, '!'}); string(got) != "!" {
		t.Fatalf("unexpected telnet output after split subnegotiation: %q", got)
	}
	response := <-responseCh
	if len(response) < 6 || !bytes.Equal(response[:3], []byte{255, 252, 1}) || !bytes.Equal(response[3:6], []byte{255, 250, 24}) || !bytes.Equal(response[len(response)-2:], []byte{255, 240}) {
		t.Fatalf("unexpected telnet negotiation response: %v", response)
	}
}

func TestTelnetWriteEscapesIAC(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	manager := NewManager()
	manager.sessions["telnet"] = &Session{ID: "telnet", Protocol: ProtocolTelnet, conn: client}
	writeDone := make(chan error, 1)
	go func() { writeDone <- manager.Write("telnet", []byte{'a', 255, 'b'}) }()
	got := make([]byte, 4)
	if _, err := server.Read(got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte{'a', 255, 255, 'b'}) {
		t.Fatalf("IAC was not escaped: %v", got)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
}

func TestSubscribeBroadcastsOutputToIndependentConsumers(t *testing.T) {
	manager := NewManager()
	session := &Session{ID: "broadcast", Protocol: ProtocolTelnet, conn: nil, output: make(chan []byte, 4), subscribers: make(map[uint64]chan []byte)}
	manager.sessions[session.ID] = session
	first, cancelFirst, err := manager.Subscribe(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer cancelFirst()
	second, cancelSecond, err := manager.Subscribe(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer cancelSecond()
	manager.publish(session, []byte("same output"))
	for name, channel := range map[string]<-chan []byte{"first": first, "second": second} {
		select {
		case got := <-channel:
			if string(got) != "same output" {
				t.Fatalf("%s subscriber got %q", name, got)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s subscriber did not receive output", name)
		}
	}
}

func TestExecuteCommandWaitsForPromptAndHandlesPager(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	manager := NewManager()
	session := &Session{ID: "device", Protocol: ProtocolTelnet, conn: client, output: make(chan []byte, 16), subscribers: make(map[uint64]chan []byte), prompt: "Router#", deviceProfile: DeviceCisco}
	manager.sessions[session.ID] = session
	go manager.readLoop(session)
	serverDone := make(chan error, 1)
	go func() {
		command := make([]byte, len("show version\r"))
		if _, err := io.ReadFull(server, command); err != nil {
			serverDone <- err
			return
		}
		if string(command) != "show version\r" {
			serverDone <- fmt.Errorf("unexpected command %q", command)
			return
		}
		if _, err := server.Write([]byte("Router# show version\r\n--More--")); err != nil {
			serverDone <- err
			return
		}
		pagerResponse := make([]byte, 1)
		if _, err := io.ReadFull(server, pagerResponse); err != nil {
			serverDone <- err
			return
		}
		if pagerResponse[0] != ' ' {
			serverDone <- fmt.Errorf("pager response = %q, want space", pagerResponse)
			return
		}
		_, err := server.Write([]byte("\r\nVersion 15\r\nRouter#"))
		serverDone <- err
	}()

	result := manager.ExecuteCommand(context.Background(), session.ID, "show version")
	if result.Status != "completed" || !result.Complete || result.Prompt != "Router#" || !strings.Contains(result.Output, "Version 15") {
		t.Fatalf("unexpected command result: %#v", result)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestExecuteCommandCompletesWhenDeviceDoesNotEchoCommand(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	manager := NewManager()
	session := &Session{ID: "no-echo", Protocol: ProtocolTelnet, conn: client, output: make(chan []byte, 16), subscribers: make(map[uint64]chan []byte), deviceProfile: DeviceCisco}
	manager.sessions[session.ID] = session
	go manager.readLoop(session)
	go func() {
		command := make([]byte, len("show version\r"))
		_, _ = io.ReadFull(server, command)
		_, _ = server.Write([]byte("Router#"))
	}()
	result := manager.ExecuteCommand(context.Background(), session.ID, "show version")
	if result.Status != "completed" || result.Prompt != "Router#" {
		t.Fatalf("no-echo command did not complete: %#v", result)
	}
}

func TestExecuteCommandDiscardsStalePromptBeforeWrite(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	manager := NewManager()
	session := &Session{ID: "stale-prompt", Protocol: ProtocolTelnet, conn: client, output: make(chan []byte, 16), subscribers: make(map[uint64]chan []byte)}
	manager.sessions[session.ID] = session
	go manager.readLoop(session)
	if _, err := server.Write([]byte("Router#")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	go func() {
		command := make([]byte, len("show clock\r"))
		_, _ = io.ReadFull(server, command)
		_, _ = server.Write([]byte("12:00:00\r\nRouter#"))
	}()
	result := manager.ExecuteCommand(context.Background(), session.ID, "show clock")
	if result.Status != "completed" || !strings.Contains(result.Output, "12:00:00") {
		t.Fatalf("stale prompt completed command early: %#v", result)
	}
}
