// owner: muswood | Email: mumu920@outlook.com
package ssh

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestLiveSSHIntegration is intentionally opt-in because it needs a real SSH
// server and credentials. It exercises the public manager path, including
// host-key trust, PTY input/output, resize, command execution, and transport
// failure handling.
func TestLiveSSHIntegration(t *testing.T) {
	if os.Getenv("GOSSH_SSH_INTEGRATION") != "1" {
		t.Skip("set GOSSH_SSH_INTEGRATION=1 to run against a real SSH server")
	}
	host := requiredSSHEnv(t, "GOSSH_SSH_HOST")
	user := requiredSSHEnv(t, "GOSSH_SSH_USER")
	password := requiredSSHEnv(t, "GOSSH_SSH_PASSWORD")
	port := 22
	if raw := os.Getenv("GOSSH_SSH_PORT"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 65535 {
			t.Fatalf("invalid GOSSH_SSH_PORT %q", raw)
		}
		port = parsed
	}

	// Keep the trust decision isolated from the developer's actual known_hosts.
	t.Setenv("HOME", t.TempDir())
	manager := NewManager()
	cfg := &ConnectionConfig{ID: "live-integration", Name: "live integration", Host: host, Port: port, Username: user, AuthMethod: AuthPassword, Password: password}
	sessionID, err := manager.Connect(cfg, 80, 24)
	if err != nil {
		var unknown *UnknownHostKeyError
		if !errors.As(err, &unknown) {
			t.Fatalf("initial SSH connection failed: %v", err)
		}
		if trustErr := manager.TrustHostKey(cfg, unknown.Fingerprint); trustErr != nil {
			t.Fatalf("trusting advertised host key failed: %v", trustErr)
		}
		sessionID, err = manager.Connect(cfg, 80, 24)
		if err != nil {
			t.Fatalf("SSH connection after host-key trust failed: %v", err)
		}
	}
	defer manager.Disconnect(sessionID)

	if err := manager.Resize(sessionID, 120, 40); err != nil {
		t.Fatalf("PTY resize failed: %v", err)
	}
	if err := manager.Write(sessionID, []byte("printf 'gossh-live-pty\\n'\n")); err != nil {
		t.Fatalf("PTY input failed: %v", err)
	}
	output := readUntil(t, manager, sessionID, "gossh-live-pty", 10*time.Second)
	if !strings.Contains(output, "gossh-live-pty") {
		t.Fatalf("PTY output did not contain marker: %q", output)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	execOutput, err := manager.ExecuteContext(ctx, sessionID, "printf 'gossh-live-exec\\n'", nil)
	if err != nil || !strings.Contains(execOutput, "gossh-live-exec") {
		t.Fatalf("non-interactive command failed: output=%q err=%v", execOutput, err)
	}

	client, err := manager.GetClient(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ExecuteContext(context.Background(), sessionID, "printf disconnected", nil); err == nil {
		t.Fatal("command unexpectedly succeeded after SSH transport disconnect")
	}
}

func requiredSSHEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required when GOSSH_SSH_INTEGRATION=1", name)
	}
	return value
}

func readUntil(t *testing.T, manager *Manager, sessionID, marker string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var output strings.Builder
	for time.Now().Before(deadline) {
		chunk, err := manager.Read(sessionID)
		if err != nil {
			t.Fatalf("reading SSH output failed: %v", err)
		}
		output.Write(chunk)
		if strings.Contains(output.String(), marker) {
			return output.String()
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for SSH output marker %q; output=%q", marker, output.String())
	return ""
}
