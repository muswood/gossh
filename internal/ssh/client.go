// owner: muswood | Email: mumu920@outlook.com
package ssh

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

type AuthMethod string

var terminalANSI = regexp.MustCompile(`\x1b(?:\][^\x07]*(?:\x07|\x1b\\)|\[[0-?]*[ -/]*[@-~])`)

// KeyboardInteractiveCallback supplies answers for server-side MFA prompts.
// The callback is intentionally kept out of JSON configuration because it is
// owned by the active UI connection attempt.
type KeyboardInteractiveCallback = ssh.KeyboardInteractiveChallenge

const (
	AuthPassword   AuthMethod = "password"
	AuthPrivateKey AuthMethod = "private_key"
	AuthSSHAgent   AuthMethod = "ssh_agent"
)

type ConnectionConfig struct {
	ID                  string                      `json:"id"`
	Name                string                      `json:"name"`
	Host                string                      `json:"host"`
	Port                int                         `json:"port"`
	Username            string                      `json:"username"`
	AuthMethod          AuthMethod                  `json:"authMethod"`
	Password            string                      `json:"password,omitempty"`
	PrivateKey          string                      `json:"privateKey,omitempty"`
	PrivateKeyPath      string                      `json:"privateKeyPath,omitempty"`
	CertificatePath     string                      `json:"certificatePath,omitempty"`
	Passphrase          string                      `json:"passphrase,omitempty"`
	JumpHost            string                      `json:"jumpHost,omitempty"`
	ProxyType           string                      `json:"proxyType,omitempty"`
	ProxyHost           string                      `json:"proxyHost,omitempty"`
	ProxyUsername       string                      `json:"proxyUsername,omitempty"`
	ProxyPassword       string                      `json:"proxyPassword,omitempty"`
	ProxyCommand        string                      `json:"proxyCommand,omitempty"`
	Encoding            string                      `json:"encoding"`
	StartupCmd          string                      `json:"startupCmd,omitempty"`
	KeepAlive           int                         `json:"keepAliveSeconds"`
	KeyboardInteractive KeyboardInteractiveCallback `json:"-"`
}

type Session struct {
	ID              string
	Config          *ConnectionConfig
	Client          *ssh.Client
	JumpClient      *ssh.Client
	Session         *ssh.Session
	Stdin           io.WriteCloser
	Stdout          io.Reader
	Stderr          io.Reader
	Cols, Rows      uint32
	Connected       bool
	mu              sync.Mutex
	cancel          context.CancelFunc
	outputBuf       chan []byte
	log             *SessionLog
	authCloser      io.Closer
	closed          bool
	systemProbeDone bool
}

type Manager struct {
	sessions        map[string]*Session
	mu              sync.RWMutex
	onData          func(sessionID string, data []byte)
	outputListeners map[string]map[uint64]func([]byte)
	nextListenerID  uint64
}

// InteractiveResult is the completion-aware result for a command sent to the
// existing PTY shell. It never reads the session log.
type InteractiveResult struct {
	Status     string
	Output     string
	Prompt     string
	ExitCode   int
	Completion string
	Error      string
	Complete   bool
	TimedOut   bool
	Truncated  bool
}

var knownHostsMu sync.Mutex

var errHostKeyCaptured = errors.New("SSH host key captured")

// UnknownHostKeyError is safe to show to an interactive user before they
// decide whether to trust a server's previously unseen host key.
type UnknownHostKeyError struct {
	Host        string
	KeyType     string
	Fingerprint string
}

func (e *UnknownHostKeyError) Error() string {
	return fmt.Sprintf("未知 SSH 主机密钥: %s (%s)，指纹: %s", e.Host, e.KeyType, e.Fingerprint)
}

func NewManager() *Manager {
	return &Manager{
		sessions:        make(map[string]*Session),
		outputListeners: make(map[string]map[uint64]func([]byte)),
	}
}

func (m *Manager) SetOnData(callback func(sessionID string, data []byte)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onData = callback
}

func (m *Manager) buildAuth(cfg *ConnectionConfig) ([]ssh.AuthMethod, io.Closer, error) {
	var methods []ssh.AuthMethod
	var authCloser io.Closer

	switch cfg.AuthMethod {
	case AuthPassword:
		if cfg.Password == "" {
			return nil, nil, fmt.Errorf("密码不能为空")
		}
		methods = append(methods, ssh.Password(cfg.Password))

	case AuthPrivateKey:
		key := []byte(cfg.PrivateKey)
		if len(key) == 0 && cfg.PrivateKeyPath != "" {
			path, err := expandHomePath(cfg.PrivateKeyPath)
			if err != nil {
				return nil, nil, err
			}
			key, err = os.ReadFile(path)
			if err != nil {
				return nil, nil, fmt.Errorf("读取私钥文件失败: %w", err)
			}
		}
		if len(key) == 0 {
			return nil, nil, fmt.Errorf("私钥内容或私钥文件路径不能为空")
		}
		var signer ssh.Signer
		var err error
		if cfg.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(cfg.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey(key)
		}
		if err != nil {
			return nil, nil, fmt.Errorf("解析私钥失败: %w", err)
		}
		if cfg.CertificatePath != "" {
			certPath, err := expandHomePath(cfg.CertificatePath)
			if err != nil {
				return nil, nil, err
			}
			certData, err := os.ReadFile(certPath)
			if err != nil {
				return nil, nil, fmt.Errorf("读取 SSH 证书失败: %w", err)
			}
			publicKey, _, _, _, err := ssh.ParseAuthorizedKey(certData)
			if err != nil {
				return nil, nil, fmt.Errorf("解析 SSH 证书失败: %w", err)
			}
			certificate, ok := publicKey.(*ssh.Certificate)
			if !ok {
				return nil, nil, fmt.Errorf("证书文件不是 OpenSSH 用户证书")
			}
			signer, err = ssh.NewCertSigner(certificate, signer)
			if err != nil {
				return nil, nil, fmt.Errorf("组合 SSH 证书和私钥失败: %w", err)
			}
		}
		methods = append(methods, ssh.PublicKeys(signer))

	case AuthSSHAgent:
		sock := os.Getenv("SSH_AUTH_SOCK")
		if sock == "" {
			return nil, nil, fmt.Errorf("SSH_AUTH_SOCK 未设置")
		}
		conn, err := net.Dial("unix", sock)
		if err != nil {
			return nil, nil, fmt.Errorf("连接 SSH Agent 失败: %w", err)
		}
		agentClient := agent.NewClient(conn)
		signers, err := agentClient.Signers()
		if err != nil {
			_ = conn.Close()
			return nil, nil, fmt.Errorf("读取 SSH Agent 密钥失败: %w", err)
		}
		if len(signers) == 0 {
			_ = conn.Close()
			return nil, nil, fmt.Errorf("SSH Agent 中没有可用密钥，请先使用 ssh-add 加载私钥，或改用密码/私钥认证")
		}
		authCloser = conn
		methods = append(methods, ssh.PublicKeys(signers...))
	default:
		return nil, nil, fmt.Errorf("未知认证方式: %s", cfg.AuthMethod)
	}
	if cfg.KeyboardInteractive != nil {
		methods = append(methods, ssh.KeyboardInteractive(cfg.KeyboardInteractive))
	}

	return methods, authCloser, nil
}

func expandHomePath(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("获取用户目录失败: %w", err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, path[2:]), nil
}

func knownHostsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("获取用户目录失败: %w", err)
	}
	return filepath.Join(home, ".ssh", "known_hosts"), nil
}

func appendKnownHost(khPath, hostname string, key ssh.PublicKey) error {
	knownHostsMu.Lock()
	defer knownHostsMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(khPath), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(khPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, knownhosts.Line([]string{hostname}, key))
	return err
}

func (m *Manager) loadKnownHosts() (ssh.HostKeyCallback, error) {
	khPath, err := knownHostsPath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(khPath), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(khPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	cb, err := knownhosts.New(khPath)
	if err != nil {
		return nil, fmt.Errorf("加载 known_hosts 失败: %w", err)
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := cb(hostname, remote, key)
		if err == nil {
			return nil
		}
		var keyErr *knownhosts.KeyError
		if errors.As(err, &keyErr) && len(keyErr.Want) == 0 {
			return &UnknownHostKeyError{
				Host: hostname, KeyType: key.Type(), Fingerprint: ssh.FingerprintSHA256(key),
			}
		}
		return err
	}, nil
}

// TrustHostKey records a first-seen host key only after the caller has shown
// its fingerprint to the user and supplied that exact fingerprint back.
func (m *Manager) TrustHostKey(cfg *ConnectionConfig, expectedFingerprint string) error {
	if cfg.JumpHost != "" {
		return fmt.Errorf("请先将跳板机主机密钥加入 known_hosts")
	}
	port := cfg.Port
	if port == 0 {
		port = 22
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, 15*time.Second)
	if err != nil {
		return fmt.Errorf("连接 %s 失败: %w", addr, err)
	}
	defer conn.Close()

	var received ssh.PublicKey
	probeConfig := &ssh.ClientConfig{
		Config:            modernAlgorithmsConfig(),
		User:              cfg.Username,
		HostKeyAlgorithms: modernHostKeyAlgorithms(),
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			received = key
			return errHostKeyCaptured
		},
		Timeout: 15 * time.Second,
	}
	_, _, _, _ = ssh.NewClientConn(conn, addr, probeConfig)
	if received == nil {
		return fmt.Errorf("未能获取 %s 的 SSH 主机密钥", addr)
	}
	fingerprint := ssh.FingerprintSHA256(received)
	if expectedFingerprint == "" || fingerprint != expectedFingerprint {
		return fmt.Errorf("主机密钥指纹已变化，未写入 known_hosts")
	}
	khPath, err := knownHostsPath()
	if err != nil {
		return err
	}
	if err := appendKnownHost(khPath, addr, received); err != nil {
		return fmt.Errorf("写入 known_hosts 失败: %w", err)
	}
	return nil
}

func (m *Manager) Connect(cfg *ConnectionConfig, cols, rows uint32) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	auth, authCloser, err := m.buildAuth(cfg)
	if err != nil {
		return "", err
	}
	keepAuthCloser := false
	defer func() {
		if !keepAuthCloser && authCloser != nil {
			_ = authCloser.Close()
		}
	}()

	if cfg.Port == 0 {
		cfg.Port = 22
	}

	hostCallback, err := m.loadKnownHosts()
	if err != nil {
		return "", err
	}

	sshCfg := &ssh.ClientConfig{
		Config:            modernAlgorithmsConfig(),
		User:              cfg.Username,
		Auth:              auth,
		HostKeyCallback:   hostCallback,
		HostKeyAlgorithms: modernHostKeyAlgorithms(),
		Timeout:           30 * time.Second,
	}

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	client, jumpClient, err := connectClient(cfg, addr, sshCfg)
	if err != nil {
		return "", err
	}

	session, err := client.NewSession()
	if err != nil {
		client.Close()
		if jumpClient != nil {
			_ = jumpClient.Close()
		}
		return "", fmt.Errorf("创建会话失败: %w", err)
	}

	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty("xterm-256color", int(rows), int(cols), modes); err != nil {
		session.Close()
		client.Close()
		if jumpClient != nil {
			_ = jumpClient.Close()
		}
		return "", fmt.Errorf("请求 PTY 失败: %w", err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()
		client.Close()
		if jumpClient != nil {
			_ = jumpClient.Close()
		}
		return "", fmt.Errorf("stdin 管道失败: %w", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		client.Close()
		if jumpClient != nil {
			_ = jumpClient.Close()
		}
		return "", fmt.Errorf("stdout 管道失败: %w", err)
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		session.Close()
		client.Close()
		if jumpClient != nil {
			_ = jumpClient.Close()
		}
		return "", fmt.Errorf("stderr 管道失败: %w", err)
	}

	if err := session.Shell(); err != nil {
		session.Close()
		client.Close()
		if jumpClient != nil {
			_ = jumpClient.Close()
		}
		return "", fmt.Errorf("启动 Shell 失败: %w", err)
	}

	sessionID := fmt.Sprintf("%s-%d", cfg.ID, time.Now().UnixNano())
	logRoot, err := defaultSessionLogRoot()
	if err != nil {
		session.Close()
		client.Close()
		if jumpClient != nil {
			_ = jumpClient.Close()
		}
		return "", err
	}
	sessionLog, err := openSessionLog(logRoot, sessionID, time.Now())
	if err != nil {
		session.Close()
		client.Close()
		if jumpClient != nil {
			_ = jumpClient.Close()
		}
		return "", err
	}
	ctx, cancel := context.WithCancel(context.Background())

	sess := &Session{
		ID:         sessionID,
		Config:     cfg,
		Client:     client,
		JumpClient: jumpClient,
		Session:    session,
		Stdin:      stdin,
		Stdout:     stdout,
		Stderr:     stderr,
		Cols:       cols,
		Rows:       rows,
		Connected:  true,
		authCloser: authCloser,
		cancel:     cancel,
		outputBuf:  make(chan []byte, 1024),
		log:        sessionLog,
	}

	m.sessions[sessionID] = sess
	keepAuthCloser = true

	// Read both SSH data streams. Interactive shells normally use stdout, but
	// login diagnostics and some server-side prompts are sent as stderr.
	go m.readOutput(ctx, sess, stdout)
	go m.readOutput(ctx, sess, stderr)

	if cfg.KeepAlive > 0 {
		go m.keepAlive(ctx, sess)
	}

	if cfg.StartupCmd != "" {
		go func() {
			time.Sleep(500 * time.Millisecond)
			if _, err := sess.Stdin.Write([]byte(cfg.StartupCmd + "\n")); err != nil {
				fmt.Printf("执行启动命令失败: %v\n", err)
			}
		}()
	}

	return sessionID, nil
}

// connectClient handles direct, proxied and chained ProxyJump transports.
// Every hop uses the connection's selected authentication method, matching
// OpenSSH's practical default when separate Host entries are not configured.
func connectClient(cfg *ConnectionConfig, addr string, targetCfg *ssh.ClientConfig) (*ssh.Client, *ssh.Client, error) {
	newClient := func(conn net.Conn, name string, sshCfg *ssh.ClientConfig) (*ssh.Client, error) {
		ncc, chans, reqs, err := ssh.NewClientConn(conn, name, sshCfg)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		return ssh.NewClient(ncc, chans, reqs), nil
	}
	if strings.TrimSpace(cfg.JumpHost) == "" {
		conn, err := dialTransport(cfg, addr)
		if err != nil {
			return nil, nil, fmt.Errorf("连接 %s 失败: %w", addr, err)
		}
		client, err := newClient(conn, addr, targetCfg)
		if err != nil {
			return nil, nil, fmt.Errorf("新建客户端连接失败: %w", err)
		}
		return client, nil, nil
	}

	hops := strings.Split(cfg.JumpHost, ",")
	var current *ssh.Client
	var root *ssh.Client
	for index, raw := range hops {
		jumpUser, jumpAddr := parseJumpHost(raw, cfg.Username)
		jumpCfg := *targetCfg
		jumpCfg.User = jumpUser
		var conn net.Conn
		var err error
		if current == nil {
			conn, err = dialTransport(cfg, jumpAddr)
		} else {
			conn, err = current.Dial("tcp", jumpAddr)
		}
		if err != nil {
			if root != nil {
				_ = root.Close()
			}
			return nil, nil, fmt.Errorf("第 %d 个跳板机连接失败: %w", index+1, err)
		}
		next, err := newClient(conn, jumpAddr, &jumpCfg)
		if err != nil {
			if root != nil {
				_ = root.Close()
			}
			return nil, nil, fmt.Errorf("第 %d 个跳板机 SSH 握手失败: %w", index+1, err)
		}
		if root == nil {
			root = next
		}
		current = next
	}
	conn, err := current.Dial("tcp", addr)
	if err != nil {
		_ = root.Close()
		return nil, nil, fmt.Errorf("通过跳板机拨号失败: %w", err)
	}
	client, err := newClient(conn, addr, targetCfg)
	if err != nil {
		_ = root.Close()
		return nil, nil, fmt.Errorf("新建客户端连接失败: %w", err)
	}
	return client, root, nil
}

func parseJumpHost(raw, defaultUser string) (string, string) {
	value := strings.TrimSpace(raw)
	user := defaultUser
	if at := strings.LastIndex(value, "@"); at >= 0 {
		if candidate := strings.TrimSpace(value[:at]); candidate != "" {
			user = candidate
		}
		value = strings.TrimSpace(value[at+1:])
	}
	if _, _, err := net.SplitHostPort(value); err != nil {
		value = net.JoinHostPort(value, "22")
	}
	return user, value
}

func (m *Manager) readOutput(ctx context.Context, sess *Session, reader io.Reader) {
	buf := make([]byte, 32*1024)
	for {
		select {
		case <-ctx.Done():
			return
		default:
			n, err := reader.Read(buf)
			if err != nil {
				if err != io.EOF {
					fmt.Printf("读取 SSH 输出失败: %v\n", err)
				}
				sess.mu.Lock()
				sess.closed = true
				sess.Connected = false
				sess.mu.Unlock()
				return
			}
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				select {
				case sess.outputBuf <- data:
				default:
					// buffer full, drop oldest
					select {
					case <-sess.outputBuf:
					default:
					}
					sess.outputBuf <- data
				}
				m.mu.RLock()
				onData := m.onData
				listeners := make([]func([]byte), 0)
				for _, listener := range m.outputListeners[sess.ID] {
					listeners = append(listeners, listener)
				}
				m.mu.RUnlock()
				if onData != nil {
					onData(sess.ID, data)
				}
				for _, listener := range listeners {
					listener(data)
				}
			}
		}
	}
}

func (m *Manager) subscribeOutput(sessionID string, listener func([]byte)) func() {
	if listener == nil {
		return func() {}
	}
	m.mu.Lock()
	if m.outputListeners == nil {
		m.outputListeners = make(map[string]map[uint64]func([]byte))
	}
	m.nextListenerID++
	id := m.nextListenerID
	if m.outputListeners[sessionID] == nil {
		m.outputListeners[sessionID] = make(map[uint64]func([]byte))
	}
	m.outputListeners[sessionID][id] = listener
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if listeners := m.outputListeners[sessionID]; listeners != nil {
			delete(listeners, id)
			if len(listeners) == 0 {
				delete(m.outputListeners, sessionID)
			}
		}
	}
}

// Subscribe returns an independent live stream fed by the session's single
// background reader. The returned cancel function is idempotent.
func (m *Manager) Subscribe(sessionID string) (<-chan []byte, func(), error) {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok || sess == nil || !sess.Connected {
		return nil, nil, fmt.Errorf("SSH 会话未连接")
	}
	stream := make(chan []byte, 256)
	cancel := m.subscribeOutput(sessionID, func(data []byte) {
		copyData := append([]byte(nil), data...)
		select {
		case stream <- copyData:
		default:
			// ponytail: bounded subscriber buffers avoid a slow consumer
			// blocking the reader; the terminal remains available via Read.
		}
	})
	return stream, func() {
		cancel()
	}, nil
}

func (m *Manager) keepAlive(ctx context.Context, sess *Session) {
	t := time.NewTicker(time.Duration(sess.Config.KeepAlive) * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sess.mu.Lock()
			if sess.Client != nil {
				sess.Client.SendRequest("keepalive@openssh.com", true, nil)
			}
			sess.mu.Unlock()
		}
	}
}

func (m *Manager) Write(sessionID string, data []byte) error {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("会话 %s 不存在", sessionID)
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if !sess.Connected || sess.Stdin == nil {
		return fmt.Errorf("会话未连接")
	}
	_, err := sess.Stdin.Write(data)
	return err
}

func (m *Manager) Read(sessionID string) ([]byte, error) {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("会话 %s 不存在", sessionID)
	}
	sess.mu.Lock()
	closed := sess.closed
	sess.mu.Unlock()
	if closed {
		return nil, fmt.Errorf("SSH 连接已断开")
	}

	select {
	case data := <-sess.outputBuf:
		return data, nil
	default:
		return []byte{}, nil
	}
}

// AppendSessionLog stores bytes the active frontend has determined are
// visible terminal content. It intentionally does not receive raw SSH output
// because binary transfer protocols share that stream.
func (m *Manager) AppendSessionLog(sessionID string, data []byte) error {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("会话 %s 不存在", sessionID)
	}
	sess.mu.Lock()
	log := sess.log
	sess.mu.Unlock()
	if log == nil {
		return fmt.Errorf("会话 %s 没有终端日志", sessionID)
	}
	return log.Write(data)
}

func (m *Manager) ReadSessionLog(sessionID string, offset int64, maxBytes int) ([]byte, int64, bool, error) {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok {
		return nil, offset, false, fmt.Errorf("会话 %s 不存在", sessionID)
	}
	sess.mu.Lock()
	log := sess.log
	sess.mu.Unlock()
	if log == nil {
		return nil, offset, false, fmt.Errorf("会话 %s 没有终端日志", sessionID)
	}
	return log.Read(offset, maxBytes)
}

func (m *Manager) SessionLogSize(sessionID string) (int64, error) {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok {
		return 0, fmt.Errorf("会话 %s 不存在", sessionID)
	}
	sess.mu.Lock()
	log := sess.log
	sess.mu.Unlock()
	if log == nil {
		return 0, fmt.Errorf("会话 %s 没有终端日志", sessionID)
	}
	return log.Size()
}

func (m *Manager) Resize(sessionID string, cols, rows uint32) error {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("会话 %s 不存在", sessionID)
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.Cols, sess.Rows = cols, rows
	return sess.Session.WindowChange(int(rows), int(cols))
}

func (m *Manager) Disconnect(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, ok := m.sessions[sessionID]
	if !ok {
		return fmt.Errorf("会话 %s 不存在", sessionID)
	}

	sess.mu.Lock()
	defer sess.mu.Unlock()

	sess.Connected = false
	if sess.cancel != nil {
		sess.cancel()
	}
	if sess.Session != nil {
		sess.Session.Close()
	}
	if sess.Client != nil {
		sess.Client.Close()
	}
	if sess.JumpClient != nil {
		sess.JumpClient.Close()
	}
	if sess.authCloser != nil {
		_ = sess.authCloser.Close()
		sess.authCloser = nil
	}
	if sess.log != nil {
		_ = sess.log.Close()
	}
	delete(m.sessions, sessionID)
	return nil
}

func (m *Manager) Execute(sessionID, command string) (string, error) {
	return m.ExecuteContext(context.Background(), sessionID, command, nil)
}

// ExecuteInteractiveContext sends one command to the active PTY and waits for
// the next shell/device prompt. Output is obtained only from the live reader.
func (m *Manager) ExecuteInteractiveContext(ctx context.Context, sessionID, command string, onChunk func([]byte)) InteractiveResult {
	stream, cancel, err := m.Subscribe(sessionID)
	if err != nil {
		return InteractiveResult{Status: "error", Error: err.Error()}
	}
	defer cancel()
	marker, err := newCompletionMarker()
	if err != nil {
		return InteractiveResult{Status: "error", Error: err.Error()}
	}
	// Keep the wrapper on one shell input line. A newline can be interpreted as
	// an Enter boundary by bracketed-paste/shell integration, causing the prompt
	// to reappear before rc/printf runs and making the legacy prompt fallback
	// report a false completion.
	wrapped := command + "; rc=$?; printf '\\n" + marker + ":%s\\n' \"$rc\""
	if err := m.Write(sessionID, []byte(wrapped+"\r")); err != nil {
		return InteractiveResult{Status: "error", Error: err.Error()}
	}
	const maxOutput = 256 * 1024
	var output bytes.Buffer
	for {
		select {
		case <-ctx.Done():
			// Stop a long-running foreground command before allowing the Agent
			// step to finish, so the next command cannot overlap it in the PTY.
			_ = m.Write(sessionID, []byte{3})
			return InteractiveResult{Status: "cancelled", Output: output.String(), Error: ctx.Err().Error()}
		case data, ok := <-stream:
			if !ok {
				return InteractiveResult{Status: "disconnected", Output: output.String(), Error: "输出订阅已关闭"}
			}
			if output.Len()+len(data) > maxOutput {
				remaining := maxOutput - output.Len()
				if remaining > 0 {
					_, _ = output.Write(data[:remaining])
				}
				return InteractiveResult{Status: "truncated", Output: output.String(), Error: "终端输出超过 256 KiB", Truncated: true}
			}
			_, _ = output.Write(data)
			if onChunk != nil {
				onChunk(data)
			}
			if exitCode, clean, ok := parseCompletionMarker(output.String(), marker); ok {
				return InteractiveResult{Status: "completed", Output: clean, ExitCode: exitCode, Completion: "remote_marker", Complete: true}
			}
			if prompt := interactivePrompt(output.String()); prompt != "" && !strings.HasSuffix(strings.TrimSpace(output.String()), wrapped) {
				// The shell prompt may already be in flight when a command starts.
				// Require a second prompt or an echoed command before declaring done.
				if countInteractivePrompts(output.String()) > 1 || strings.Contains(output.String(), command) || interactiveHasBody(output.String()) {
					return InteractiveResult{Status: "completed", Output: output.String(), Prompt: prompt, Completion: "prompt", Complete: true}
				}
			}
		}
	}
}

func newCompletionMarker() (string, error) {
	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", fmt.Errorf("生成命令完成标记失败: %w", err)
	}
	return "__GOSSH_DONE_" + fmt.Sprintf("%x", id) + "__", nil
}

func parseCompletionMarker(output, marker string) (int, string, bool) {
	needle := marker + ":"
	start := strings.LastIndex(output, needle)
	if start < 0 {
		return 0, output, false
	}
	end := strings.IndexByte(output[start+len(needle):], '\n')
	if end < 0 {
		return 0, output, false
	}
	value := strings.TrimSpace(output[start+len(needle) : start+len(needle)+end])
	exitCode, err := strconv.Atoi(value)
	if err != nil {
		return 0, output, false
	}
	clean := strings.TrimRight(output[:start], "\r\n")
	return exitCode, clean, true
}

func interactivePrompt(output string) string {
	output = terminalANSI.ReplaceAllString(output, "")
	lines := strings.Split(strings.ReplaceAll(output, "\r", ""), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if strings.HasSuffix(line, ">") || strings.HasSuffix(line, "#") || strings.HasSuffix(line, "]") || strings.HasSuffix(line, "$") {
			if !strings.ContainsAny(line, " \t") || strings.Contains(line, "@") || strings.Contains(line, ":") {
				return line
			}
		}
		break
	}
	return ""
}

func countInteractivePrompts(output string) int {
	output = terminalANSI.ReplaceAllString(output, "")
	count := 0
	lines := strings.Split(strings.ReplaceAll(output, "\r", ""), "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if (strings.HasSuffix(line, ">") || strings.HasSuffix(line, "#") || strings.HasSuffix(line, "]") || strings.HasSuffix(line, "$") || strings.HasSuffix(line, "%")) && (!strings.ContainsAny(line, " \t") || strings.Contains(line, "@") || strings.Contains(line, ":")) {
			count++
		}
	}
	return count
}

func interactiveHasBody(output string) bool {
	output = terminalANSI.ReplaceAllString(output, "")
	lines := strings.Split(strings.ReplaceAll(output, "\r", ""), "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || interactivePrompt(line) != "" {
			continue
		}
		return true
	}
	return false
}

// RemoteExitCode extracts the exit status from a command that reached the
// remote host but exited unsuccessfully. A false result means the command did
// not produce a reliable remote exit status, for example due to transport
// failure or local cancellation.
func RemoteExitCode(err error) (int, bool) {
	var exitErr *ssh.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitStatus(), true
	}
	return -1, false
}

// ExecuteContext runs a non-interactive command while preserving output
// chunks. Closing the SSH channel on cancellation also interrupts long-lived
// remote commands instead of merely abandoning a local goroutine.
func (m *Manager) ExecuteContext(ctx context.Context, sessionID, command string, onChunk func([]byte)) (string, error) {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok || !sess.Connected {
		return "", fmt.Errorf("会话未连接")
	}

	execSession, err := sess.Client.NewSession()
	if err != nil {
		return "", err
	}
	defer execSession.Close()
	writer := &streamWriter{onChunk: onChunk}
	execSession.Stdout = writer
	execSession.Stderr = writer
	if err := execSession.Start(command); err != nil {
		return writer.String(), err
	}
	waitCh := make(chan error, 1)
	go func() { waitCh <- execSession.Wait() }()
	select {
	case err := <-waitCh:
		return writer.String(), err
	case <-ctx.Done():
		_ = execSession.Close()
		<-waitCh
		return writer.String(), ctx.Err()
	}
}

type streamWriter struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	onChunk func([]byte)
}

func (w *streamWriter) Write(p []byte) (int, error) {
	copyOfChunk := append([]byte(nil), p...)
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err := w.buffer.Write(copyOfChunk)
	if w.onChunk != nil && len(copyOfChunk) > 0 {
		w.onChunk(copyOfChunk)
	}
	return len(p), err
}

func (w *streamWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.String()
}

func (m *Manager) ListSessions() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	list := make(map[string]bool)
	for id, s := range m.sessions {
		list[id] = s.Connected
	}
	data, _ := json.Marshal(list)
	return string(data)
}

func (m *Manager) HasSession(id string) bool {
	m.mu.RLock()
	sess, ok := m.sessions[id]
	m.mu.RUnlock()
	return ok && sess != nil && sess.Connected
}

func (m *Manager) HasSystemProbe(id string) bool {
	m.mu.RLock()
	sess, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok || sess == nil {
		return false
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.Connected && sess.systemProbeDone
}

func (m *Manager) MarkSystemProbe(id string) {
	m.mu.RLock()
	sess, ok := m.sessions[id]
	m.mu.RUnlock()
	if ok && sess != nil {
		sess.mu.Lock()
		sess.systemProbeDone = true
		sess.mu.Unlock()
	}
}

func (m *Manager) GetClient(sessionID string) (*ssh.Client, error) {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok || !sess.Connected {
		return nil, fmt.Errorf("会话未连接")
	}
	return sess.Client, nil
}

// OpenAdditionalClient creates a separate SSH transport for services such as
// SFTP. Some servers only permit one channel on an SSH connection, which is
// already occupied by the interactive terminal session.
func (m *Manager) OpenAdditionalClient(sessionID string) (*ssh.Client, *ssh.Client, io.Closer, error) {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	if !ok || !sess.Connected || sess.Config == nil {
		m.mu.RUnlock()
		return nil, nil, nil, fmt.Errorf("会话未连接")
	}
	cfg := *sess.Config
	m.mu.RUnlock()

	auth, authCloser, err := m.buildAuth(&cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	closeAuth := true
	defer func() {
		if closeAuth && authCloser != nil {
			_ = authCloser.Close()
		}
	}()
	port := cfg.Port
	if port == 0 {
		port = 22
	}
	hostCallback, err := m.loadKnownHosts()
	if err != nil {
		return nil, nil, nil, err
	}
	sshCfg := &ssh.ClientConfig{
		Config:            modernAlgorithmsConfig(),
		User:              cfg.Username,
		Auth:              auth,
		HostKeyCallback:   hostCallback,
		HostKeyAlgorithms: modernHostKeyAlgorithms(),
		Timeout:           30 * time.Second,
	}
	addr := fmt.Sprintf("%s:%d", cfg.Host, port)
	client, jumpClient, err := connectClient(&cfg, addr, sshCfg)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("连接 SFTP 专用 SSH 通道失败: %w", err)
	}
	closeAuth = false
	return client, jumpClient, authCloser, nil
}

func (m *Manager) DisconnectAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, sess := range m.sessions {
		sess.mu.Lock()
		sess.Connected = false
		if sess.cancel != nil {
			sess.cancel()
		}
		if sess.Session != nil {
			sess.Session.Close()
		}
		if sess.Client != nil {
			sess.Client.Close()
		}
		if sess.JumpClient != nil {
			sess.JumpClient.Close()
		}
		if sess.authCloser != nil {
			_ = sess.authCloser.Close()
			sess.authCloser = nil
		}
		if sess.log != nil {
			_ = sess.log.Close()
		}
		delete(m.sessions, id)
		sess.mu.Unlock()
	}
}
