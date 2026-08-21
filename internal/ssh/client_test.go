// owner: muswood | Email: mumu920@outlook.com
package ssh

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	xssh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func TestParseJumpHost(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		defaultUser string
		wantUser    string
		wantAddr    string
	}{
		{name: "explicit user and port", raw: "jump@bastion.example:2200", defaultUser: "target", wantUser: "jump", wantAddr: "bastion.example:2200"},
		{name: "default port", raw: "bastion.example", defaultUser: "target", wantUser: "target", wantAddr: "bastion.example:22"},
		{name: "ipv6", raw: "admin@[::1]:2222", defaultUser: "target", wantUser: "admin", wantAddr: "[::1]:2222"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user, addr := parseJumpHost(tt.raw, tt.defaultUser)
			if user != tt.wantUser || addr != tt.wantAddr {
				t.Fatalf("parseJumpHost(%q) = (%q, %q), want (%q, %q)", tt.raw, user, addr, tt.wantUser, tt.wantAddr)
			}
		})
	}
}

func TestBuildAuthIncludesKeyboardInteractiveForMFA(t *testing.T) {
	manager := NewManager()
	methods, closer, err := manager.buildAuth(&ConnectionConfig{
		AuthMethod: AuthPassword,
		Password:   "password",
		KeyboardInteractive: func(user, instruction string, questions []string, echos []bool) ([]string, error) {
			return []string{"123456"}, nil
		},
	})
	if err != nil {
		t.Fatalf("buildAuth returned error: %v", err)
	}
	if len(methods) != 2 {
		t.Fatalf("expected password and keyboard-interactive methods, got %d", len(methods))
	}
	if closer != nil {
		closer.Close()
	}
}

func TestBuildAuthReturnsAgentCloser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "operation not permitted") {
			t.Skipf("Unix socket unavailable in test environment: %v", err)
		}
		t.Fatal(err)
	}
	defer listener.Close()
	private, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: private}); err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			agent.ServeAgent(keyring, conn)
		}
		close(serverDone)
	}()
	t.Setenv("SSH_AUTH_SOCK", path)
	methods, closer, err := NewManager().buildAuth(&ConnectionConfig{AuthMethod: AuthSSHAgent})
	if err != nil {
		t.Fatal(err)
	}
	if len(methods) != 1 || closer == nil {
		t.Fatalf("buildAuth returned methods=%d closer=%T, want one method and closer", len(methods), closer)
	}
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("closing auth closer did not close the agent socket")
	}
}

func TestDisconnectClosesAuthCloser(t *testing.T) {
	closed := false
	manager := NewManager()
	manager.sessions["session"] = &Session{ID: "session", Connected: true, authCloser: closeFunc(func() error {
		closed = true
		return nil
	})}
	if err := manager.Disconnect("session"); err != nil {
		t.Fatal(err)
	}
	if !closed {
		t.Fatal("disconnect did not close session auth resource")
	}
}

func TestDisconnectAllClosesAuthClosers(t *testing.T) {
	closed := 0
	manager := NewManager()
	manager.sessions["session"] = &Session{ID: "session", Connected: true, authCloser: closeFunc(func() error {
		closed++
		return nil
	})}
	manager.DisconnectAll()
	if closed != 1 {
		t.Fatalf("disconnect all auth closer calls = %d, want 1", closed)
	}
}

type closeFunc func() error

func (f closeFunc) Close() error { return f() }

func TestRemoteExitCodeRejectsTransportErrors(t *testing.T) {
	if code, ok := RemoteExitCode(errors.New("connection reset")); ok || code != -1 {
		t.Fatalf("transport error returned remote exit status (%d, %v)", code, ok)
	}
}

func TestExecuteContextStreamsOutputAndPreservesExitCode(t *testing.T) {
	server, client := newTestSSHClient(t)
	defer server.Close()
	manager := NewManager()
	manager.sessions["test"] = &Session{ID: "test", Client: client, Connected: true}

	var chunks []string
	output, err := manager.ExecuteContext(context.Background(), "test", "exit-7", func(chunk []byte) {
		chunks = append(chunks, string(chunk))
	})
	if err == nil {
		t.Fatal("expected remote command failure")
	}
	code, ok := RemoteExitCode(err)
	if !ok || code != 7 {
		t.Fatalf("remote exit status = (%d, %v), want (7, true), error=%v", code, ok, err)
	}
	if !strings.Contains(output, "stdout") || !strings.Contains(output, "stderr") || len(chunks) < 2 {
		t.Fatalf("streamed output incomplete: output=%q chunks=%#v", output, chunks)
	}
}

func TestExecuteContextHonorsCancellation(t *testing.T) {
	server, client := newTestSSHClient(t)
	defer server.Close()
	manager := NewManager()
	manager.sessions["test"] = &Session{ID: "test", Client: client, Connected: true}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := manager.ExecuteContext(ctx, "test", "sleep", nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ExecuteContext error = %v, want deadline exceeded", err)
	}
}

func TestExecuteContextReportsTransportDisconnect(t *testing.T) {
	server, client := newTestSSHClient(t)
	manager := NewManager()
	manager.sessions["test"] = &Session{ID: "test", Client: client, Connected: true}
	_ = server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := manager.ExecuteContext(ctx, "test", "echo disconnected", nil); err == nil {
		t.Fatal("transport disconnect was reported as success")
	}
	_ = client.Close()
}

func TestPTYShellAndWindowChange(t *testing.T) {
	server, client := newPTYTestSSHClient(t)
	defer server.Close()
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	stdout, err := session.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.RequestPty("xterm-256color", 24, 80, xssh.TerminalModes{xssh.ECHO: 1}); err != nil {
		t.Fatal(err)
	}
	if err := session.Shell(); err != nil {
		t.Fatal(err)
	}
	initial := make([]byte, len("ready> "))
	if _, err := io.ReadFull(stdout, initial); err != nil || string(initial) != "ready> " {
		t.Fatalf("initial PTY prompt = %q, err=%v", initial, err)
	}
	if err := session.WindowChange(40, 120); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Write([]byte("echo hi\n")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len("hi\r\nready> "))
	if _, err := io.ReadFull(stdout, response); err != nil || !bytes.Contains(response, []byte("hi")) {
		t.Fatalf("PTY response = %q, err=%v", response, err)
	}
}

func TestExecuteInteractiveUsesLiveOutputWithoutSessionLog(t *testing.T) {
	server, client := newPTYTestSSHClient(t)
	defer server.Close()
	defer client.Close()
	channel, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer channel.Close()
	stdout, err := channel.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := channel.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := channel.RequestPty("xterm-256color", 24, 80, xssh.TerminalModes{xssh.ECHO: 1}); err != nil {
		t.Fatal(err)
	}
	if err := channel.Shell(); err != nil {
		t.Fatal(err)
	}
	manager := NewManager()
	manager.sessions["interactive"] = &Session{ID: "interactive", Client: client, Session: channel, Stdin: stdin, Stdout: stdout, Connected: true, outputBuf: make(chan []byte, 8)}
	go manager.readOutput(context.Background(), manager.sessions["interactive"], stdout)
	result := manager.ExecuteInteractiveContext(context.Background(), "interactive", "echo hi", nil)
	if result.Status != "completed" || result.Prompt != "ready>" || !strings.Contains(result.Output, "hi") {
		t.Fatalf("unexpected interactive result: %#v", result)
	}
}

func TestExecuteInteractiveWaitsPastLegacy15SecondTimeout(t *testing.T) {
	server, client := newDelayedPTYTestSSHClient(t, 16*time.Second)
	defer server.Close()
	defer client.Close()
	channel, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer channel.Close()
	stdout, err := channel.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := channel.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := channel.RequestPty("xterm-256color", 24, 80, xssh.TerminalModes{xssh.ECHO: 1}); err != nil {
		t.Fatal(err)
	}
	if err := channel.Shell(); err != nil {
		t.Fatal(err)
	}
	manager := NewManager()
	manager.sessions["slow"] = &Session{ID: "slow", Client: client, Session: channel, Stdin: stdin, Stdout: stdout, Connected: true, outputBuf: make(chan []byte, 8)}
	go manager.readOutput(context.Background(), manager.sessions["slow"], stdout)

	result := manager.ExecuteInteractiveContext(context.Background(), "slow", "slow", nil)
	if result.Status != "completed" || !strings.Contains(result.Output, "slow-done") {
		t.Fatalf("long-running interactive command completed incorrectly: %#v", result)
	}
}

func TestParseCompletionMarker(t *testing.T) {
	marker := "__GOSSH_DONE_deadbeef__"
	output := "echo hi\r\nhi\r\n" + marker + ":7\r\nuser@host:~$ "
	exitCode, clean, ok := parseCompletionMarker(output, marker)
	if !ok || exitCode != 7 || strings.Contains(clean, marker) || !strings.Contains(clean, "hi") {
		t.Fatalf("unexpected marker parse: ok=%v exit=%d clean=%q", ok, exitCode, clean)
	}
}

func TestParseCompletionMarkerAcrossChunks(t *testing.T) {
	marker := "__GOSSH_DONE_deadbeef__"
	for _, output := range []string{"prefix " + marker + ":0\n", "prefix " + marker + ":0\r\n"} {
		if exitCode, _, ok := parseCompletionMarker(output, marker); !ok || exitCode != 0 {
			t.Fatalf("marker not parsed: %q", output)
		}
	}
	if _, _, ok := parseCompletionMarker("prefix "+marker+":", marker); ok {
		t.Fatal("partial marker must not complete")
	}
}

func newTestSSHClient(t *testing.T) (net.Conn, *xssh.Client) {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostKey, err := xssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	config := &xssh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(hostKey)
	serverWire, clientWire := newAsyncConn(net.Pipe())
	go func() {
		serverConn, channels, requests, err := xssh.NewServerConn(serverWire, config)
		if err != nil {
			_ = serverWire.Close()
			return
		}
		go xssh.DiscardRequests(requests)
		go func() {
			for newChannel := range channels {
				if newChannel.ChannelType() != "session" {
					_ = newChannel.Reject(xssh.UnknownChannelType, "session only")
					continue
				}
				channel, requests, err := newChannel.Accept()
				if err != nil {
					continue
				}
				go func() {
					defer channel.Close()
					for request := range requests {
						if request.Type != "exec" || len(request.Payload) < 4 {
							_ = request.Reply(false, nil)
							continue
						}
						_ = request.Reply(true, nil)
						commandLength := int(binary.BigEndian.Uint32(request.Payload[:4]))
						if commandLength > len(request.Payload)-4 {
							return
						}
						command := string(request.Payload[4 : 4+commandLength])
						if command == "sleep" {
							time.Sleep(2 * time.Second)
							return
						}
						_, _ = channel.Write([]byte("stdout\n"))
						_, _ = channel.Stderr().Write([]byte("stderr\n"))
						status := make([]byte, 4)
						binary.BigEndian.PutUint32(status, 7)
						_, _ = channel.SendRequest("exit-status", false, status)
						return
					}
				}()
			}
		}()
		_ = serverConn.Wait()
	}()
	clientConn, channels, requests, err := xssh.NewClientConn(clientWire, "test", &xssh.ClientConfig{
		User: "test", HostKeyCallback: xssh.InsecureIgnoreHostKey(), Timeout: time.Second,
	})
	if err != nil {
		_ = serverWire.Close()
		t.Fatal(err)
	}
	return serverWire, xssh.NewClient(clientConn, channels, requests)
}

func newPTYTestSSHClient(t *testing.T) (net.Conn, *xssh.Client) {
	return newDelayedPTYTestSSHClient(t, 0)
}

func newDelayedPTYTestSSHClient(t *testing.T, delay time.Duration) (net.Conn, *xssh.Client) {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostKey, err := xssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	config := &xssh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(hostKey)
	serverWire, clientWire := newAsyncConn(net.Pipe())
	go func() {
		serverConn, channels, requests, err := xssh.NewServerConn(serverWire, config)
		if err != nil {
			_ = serverWire.Close()
			return
		}
		go xssh.DiscardRequests(requests)
		go func() {
			for newChannel := range channels {
				channel, requests, err := newChannel.Accept()
				if err != nil {
					continue
				}
				go func() {
					defer channel.Close()
					for request := range requests {
						switch request.Type {
						case "pty-req", "window-change":
							_ = request.Reply(true, nil)
						case "shell":
							_ = request.Reply(true, nil)
							_, _ = channel.Write([]byte("ready> "))
							go func() {
								buffer := make([]byte, 64)
								n, _ := channel.Read(buffer)
								if bytes.Contains(buffer[:n], []byte("slow")) {
									time.Sleep(delay)
									_, _ = channel.Write([]byte("slow-done\r\nready> "))
								} else if bytes.Contains(buffer[:n], []byte("echo hi")) {
									_, _ = channel.Write([]byte("hi\r\nready> "))
								}
							}()
						}
					}
				}()
			}
		}()
		_ = serverConn.Wait()
	}()
	clientConn, channels, requests, err := xssh.NewClientConn(clientWire, "test", &xssh.ClientConfig{User: "test", HostKeyCallback: xssh.InsecureIgnoreHostKey(), Timeout: time.Second})
	if err != nil {
		_ = serverWire.Close()
		t.Fatal(err)
	}
	return serverWire, xssh.NewClient(clientConn, channels, requests)
}

// net.Pipe writes synchronously, while both SSH peers send their version
// before reading the peer version. Buffering writes keeps this test fully
// in-memory without introducing a listening TCP socket.
type asyncConn struct {
	net.Conn
	writes chan []byte
	done   chan struct{}
	once   sync.Once
}

func newAsyncConn(connA, connB net.Conn) (*asyncConn, *asyncConn) {
	return newAsyncConnOne(connA), newAsyncConnOne(connB)
}

func newAsyncConnOne(conn net.Conn) *asyncConn {
	connWrapper := &asyncConn{Conn: conn, writes: make(chan []byte, 32), done: make(chan struct{})}
	go func() {
		for {
			select {
			case payload := <-connWrapper.writes:
				if _, err := conn.Write(payload); err != nil {
					return
				}
			case <-connWrapper.done:
				return
			}
		}
	}()
	return connWrapper
}

func (c *asyncConn) Write(payload []byte) (int, error) {
	copyOfPayload := append([]byte(nil), payload...)
	select {
	case c.writes <- copyOfPayload:
		return len(payload), nil
	case <-c.done:
		return 0, net.ErrClosed
	}
}

func (c *asyncConn) Close() error {
	c.once.Do(func() { close(c.done) })
	return c.Conn.Close()
}
