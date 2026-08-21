// owner: muswood | Email: mumu920@outlook.com
package portforward

import (
	"net"
	"testing"
	"time"
)

func TestSOCKS5RejectsUnsupportedAuthentication(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()

	done := make(chan struct{})
	go func() {
		(&Manager{}).handleSOCKS5(server)
		close(done)
	}()

	if _, err := client.Write([]byte{5, 1, 2}); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 2)
	if _, err := client.Read(response); err != nil {
		t.Fatal(err)
	}
	if response[0] != 5 || response[1] != 0xff {
		t.Fatalf("unexpected method response: %v", response)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SOCKS5 handler did not finish")
	}
}

func TestSOCKS5ReplyEncodesAddresses(t *testing.T) {
	reply := socks5Reply(0, "127.0.0.1", 8080)
	expected := []byte{5, 0, 0, 1, 127, 0, 0, 1, 0x1f, 0x90}
	if string(reply) != string(expected) {
		t.Fatalf("unexpected IPv4 reply: %v", reply)
	}

	reply = socks5Reply(0, "example.test", 443)
	expected = []byte{5, 0, 0, 3, 12, 'e', 'x', 'a', 'm', 'p', 'l', 'e', '.', 't', 'e', 's', 't', 1, 0xbb}
	if string(reply) != string(expected) {
		t.Fatalf("unexpected domain reply: %v", reply)
	}
}

func TestParseForwardAddress(t *testing.T) {
	tests := []struct {
		name             string
		address          string
		defaultHost      string
		allowDynamicPort bool
		wantHost         string
		wantPort         int
		wantErr          bool
	}{
		{name: "IPv4 address", address: "127.0.0.1:8080", defaultHost: "127.0.0.1", wantHost: "127.0.0.1", wantPort: 8080},
		{name: "IPv6 address", address: "[::1]:8080", defaultHost: "127.0.0.1", wantHost: "::1", wantPort: 8080},
		{name: "empty host uses default", address: ":0", defaultHost: "127.0.0.1", allowDynamicPort: true, wantHost: "127.0.0.1", wantPort: 0},
		{name: "missing port", address: "127.0.0.1", defaultHost: "127.0.0.1", wantErr: true},
		{name: "invalid port", address: "127.0.0.1:not-a-port", defaultHost: "127.0.0.1", wantErr: true},
		{name: "remote port cannot be zero", address: "target.example:0", wantErr: true},
		{name: "out of range port", address: "target.example:65536", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, port, err := parseForwardAddress(tt.address, tt.defaultHost, tt.allowDynamicPort)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseForwardAddress(%q) succeeded unexpectedly", tt.address)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseForwardAddress(%q) returned error: %v", tt.address, err)
			}
			if host != tt.wantHost || port != tt.wantPort {
				t.Fatalf("parseForwardAddress(%q) = (%q, %d), want (%q, %d)", tt.address, host, port, tt.wantHost, tt.wantPort)
			}
		})
	}
}
