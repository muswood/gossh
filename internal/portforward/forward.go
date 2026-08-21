// owner: muswood | Email: mumu920@outlook.com
package portforward

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"

	"golang.org/x/crypto/ssh"
)

type ForwardType string

const (
	TypeLocal   ForwardType = "local"   // -L: listen local, forward to remote
	TypeRemote  ForwardType = "remote"  // -R: listen remote, forward to local
	TypeDynamic ForwardType = "dynamic" // -D: SOCKS5 proxy
)

type ForwardRule struct {
	ID         string      `json:"id"`
	Type       ForwardType `json:"type"`
	LocalHost  string      `json:"localHost"`
	LocalPort  int         `json:"localPort"`
	RemoteHost string      `json:"remoteHost"`
	RemotePort int         `json:"remotePort"`
	Active     bool        `json:"active"`
	listener   net.Listener
	cancel     chan struct{}
}

type Manager struct {
	client *ssh.Client
	rules  map[string]*ForwardRule
	mu     sync.Mutex
}

func NewManager(client *ssh.Client) *Manager {
	return &Manager{client: client, rules: make(map[string]*ForwardRule)}
}

func (m *Manager) AddLocal(id string, localAddr string, remoteAddr string) (*ForwardRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.rules[id]; ok {
		return nil, fmt.Errorf("转发规则 %s 已存在", id)
	}

	localHost, localPort, err := parseForwardAddress(localAddr, "0.0.0.0", true)
	if err != nil {
		return nil, fmt.Errorf("本地地址无效: %w", err)
	}
	remoteHost, remotePort, err := parseForwardAddress(remoteAddr, "", false)
	if err != nil {
		return nil, fmt.Errorf("远程地址无效: %w", err)
	}

	listener, err := net.Listen("tcp", net.JoinHostPort(localHost, strconv.Itoa(localPort)))
	if err != nil {
		return nil, fmt.Errorf("本地监听失败: %w", err)
	}

	rule := &ForwardRule{
		ID:         id,
		Type:       TypeLocal,
		LocalHost:  localHost,
		LocalPort:  listener.Addr().(*net.TCPAddr).Port,
		RemoteHost: remoteHost,
		RemotePort: remotePort,
		Active:     true,
		listener:   listener,
		cancel:     make(chan struct{}),
	}
	m.rules[id] = rule

	go m.serveLocal(rule)
	return rule, nil
}

func (m *Manager) serveLocal(rule *ForwardRule) {
	for {
		select {
		case <-rule.cancel:
			return
		default:
		}
		localConn, err := rule.listener.Accept()
		if err != nil {
			select {
			case <-rule.cancel:
				return
			default:
			}
			continue
		}
		go func() {
			defer localConn.Close()
			remoteConn, err := m.client.Dial("tcp", net.JoinHostPort(rule.RemoteHost, fmt.Sprint(rule.RemotePort)))
			if err != nil {
				return
			}
			defer remoteConn.Close()
			go io.Copy(remoteConn, localConn)
			io.Copy(localConn, remoteConn)
		}()
	}
}

func (m *Manager) AddRemote(id string, remoteAddr string, localAddr string) (*ForwardRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.rules[id]; ok {
		return nil, fmt.Errorf("转发规则 %s 已存在", id)
	}

	remoteHost, remotePort, err := parseForwardAddress(remoteAddr, "", false)
	if err != nil {
		return nil, fmt.Errorf("远程地址无效: %w", err)
	}
	localHost, localPort, err := parseForwardAddress(localAddr, "127.0.0.1", true)
	if err != nil {
		return nil, fmt.Errorf("本地地址无效: %w", err)
	}

	listener, err := m.client.Listen("tcp", net.JoinHostPort(remoteHost, strconv.Itoa(remotePort)))
	if err != nil {
		return nil, fmt.Errorf("远程监听失败: %w", err)
	}

	rule := &ForwardRule{
		ID:         id,
		Type:       TypeRemote,
		LocalHost:  localHost,
		LocalPort:  localPort,
		RemoteHost: remoteHost,
		RemotePort: remotePort,
		Active:     true,
		listener:   listener,
		cancel:     make(chan struct{}),
	}
	m.rules[id] = rule

	go func() {
		for {
			select {
			case <-rule.cancel:
				return
			default:
			}
			remoteConn, err := listener.Accept()
			if err != nil {
				select {
				case <-rule.cancel:
					return
				default:
				}
				continue
			}
			go func() {
				defer remoteConn.Close()
				localConn, err := net.Dial("tcp", net.JoinHostPort(rule.LocalHost, fmt.Sprint(rule.LocalPort)))
				if err != nil {
					return
				}
				defer localConn.Close()
				go io.Copy(localConn, remoteConn)
				io.Copy(remoteConn, localConn)
			}()
		}
	}()

	return rule, nil
}

func (m *Manager) AddDynamic(id string, localAddr string) (*ForwardRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.rules[id]; ok {
		return nil, fmt.Errorf("转发规则 %s 已存在", id)
	}

	localHost, localPort, err := parseForwardAddress(localAddr, "127.0.0.1", true)
	if err != nil {
		return nil, fmt.Errorf("本地地址无效: %w", err)
	}

	listener, err := net.Listen("tcp", net.JoinHostPort(localHost, strconv.Itoa(localPort)))
	if err != nil {
		return nil, fmt.Errorf("本地监听失败: %w", err)
	}

	rule := &ForwardRule{
		ID:        id,
		Type:      TypeDynamic,
		LocalHost: localHost,
		LocalPort: listener.Addr().(*net.TCPAddr).Port,
		Active:    true,
		listener:  listener,
		cancel:    make(chan struct{}),
	}
	m.rules[id] = rule

	go m.serveDynamic(rule)
	return rule, nil
}

func (m *Manager) serveDynamic(rule *ForwardRule) {
	for {
		select {
		case <-rule.cancel:
			return
		default:
		}
		localConn, err := rule.listener.Accept()
		if err != nil {
			select {
			case <-rule.cancel:
				return
			default:
			}
			continue
		}
		go func() {
			defer localConn.Close()
			m.handleSOCKS5(localConn)
		}()
	}
}

func (m *Manager) handleSOCKS5(conn net.Conn) {
	var header [2]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil || header[0] != 5 {
		return
	}
	if header[1] == 0 {
		return
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}
	noAuth := false
	for _, method := range methods {
		if method == 0 {
			noAuth = true
			break
		}
	}
	if !noAuth {
		_, _ = conn.Write([]byte{5, 0xff})
		return
	}
	if _, err := conn.Write([]byte{5, 0}); err != nil { // no authentication
		return
	}

	var request [4]byte
	if _, err := io.ReadFull(conn, request[:]); err != nil || request[0] != 5 || request[1] != 1 {
		_, _ = conn.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	host, err := readSOCKS5Address(conn, request[3])
	if err != nil {
		_, _ = conn.Write([]byte{5, 8, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	var portBytes [2]byte
	if _, err := io.ReadFull(conn, portBytes[:]); err != nil {
		return
	}
	port := int(portBytes[0])<<8 | int(portBytes[1])
	remoteConn, err := m.client.Dial("tcp", net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		_, _ = conn.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer remoteConn.Close()
	boundHost, boundPort := socks5BoundAddress(remoteConn.LocalAddr())
	if _, err := conn.Write(socks5Reply(0, boundHost, boundPort)); err != nil {
		return
	}
	go func() { _, _ = io.Copy(remoteConn, conn) }()
	_, _ = io.Copy(conn, remoteConn)
}

func socks5BoundAddress(addr net.Addr) (string, int) {
	if addr == nil {
		return "0.0.0.0", 0
	}
	host, portText, err := net.SplitHostPort(addr.String())
	if err != nil {
		return "0.0.0.0", 0
	}
	return host, atoi(portText)
}

func socks5Reply(code byte, host string, port int) []byte {
	ip := net.ParseIP(host)
	if ip4 := ip.To4(); ip4 != nil {
		return []byte{5, code, 0, 1, ip4[0], ip4[1], ip4[2], ip4[3], byte(port >> 8), byte(port)}
	}
	if ip16 := ip.To16(); ip16 != nil {
		return append([]byte{5, code, 0, 4}, append(ip16, byte(port>>8), byte(port))...)
	}
	if len(host) > 255 {
		host = ""
	}
	return append([]byte{5, code, 0, 3, byte(len(host))}, append([]byte(host), byte(port>>8), byte(port))...)
}

func readSOCKS5Address(r io.Reader, atyp byte) (string, error) {
	switch atyp {
	case 1:
		buf := make([]byte, 4)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", err
		}
		return net.IP(buf).String(), nil
	case 3:
		var length [1]byte
		if _, err := io.ReadFull(r, length[:]); err != nil {
			return "", err
		}
		if length[0] == 0 {
			return "", fmt.Errorf("SOCKS5 域名为空")
		}
		buf := make([]byte, int(length[0]))
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", err
		}
		return string(buf), nil
	case 4:
		buf := make([]byte, 16)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", err
		}
		return net.IP(buf).String(), nil
	default:
		return "", fmt.Errorf("不支持的 SOCKS5 地址类型: %d", atyp)
	}
}

func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rule, ok := m.rules[id]
	if !ok {
		return fmt.Errorf("规则 %s 不存在", id)
	}
	close(rule.cancel)
	if rule.listener != nil {
		rule.listener.Close()
	}
	rule.Active = false
	delete(m.rules, id)
	return nil
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, rule := range m.rules {
		close(rule.cancel)
		if rule.listener != nil {
			rule.listener.Close()
		}
		rule.Active = false
		delete(m.rules, id)
	}
}

func (m *Manager) ListRules() []ForwardRule {
	m.mu.Lock()
	defer m.mu.Unlock()
	rules := make([]ForwardRule, 0, len(m.rules))
	for _, r := range m.rules {
		rules = append(rules, *r)
	}
	return rules
}

func atoi(s string) int {
	var n int
	fmt.Sscanf(s, "%d", &n)
	return n
}

func parseForwardAddress(address, defaultHost string, allowDynamicPort bool) (string, int, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, err
	}
	if host == "" {
		host = defaultHost
	}
	if host == "" {
		return "", 0, fmt.Errorf("主机不能为空")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 0 || port > 65535 || (!allowDynamicPort && port == 0) {
		return "", 0, fmt.Errorf("端口无效: %q", portText)
	}
	return host, port, nil
}
