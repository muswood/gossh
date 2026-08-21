// owner: muswood | Email: mumu920@outlook.com
package tcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"
)

var ansiSequencePattern = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\))`)

type Protocol string

const (
	ProtocolRaw    Protocol = "raw"
	ProtocolTelnet Protocol = "telnet"
)

type DeviceProfile string

const (
	DeviceGeneric DeviceProfile = "generic"
	DeviceCisco   DeviceProfile = "cisco_ios"
	DeviceHuawei  DeviceProfile = "huawei_vrp"
	DeviceH3C     DeviceProfile = "h3c_comware"
)

type CommandResult struct {
	Status        string `json:"status"`
	Output        string `json:"output,omitempty"`
	Prompt        string `json:"prompt,omitempty"`
	PagerDetected bool   `json:"pagerDetected,omitempty"`
	Truncated     bool   `json:"truncated,omitempty"`
	TimedOut      bool   `json:"timedOut,omitempty"`
	Complete      bool   `json:"complete"`
	Error         string `json:"error,omitempty"`
}

type Session struct {
	ID            string
	Protocol      Protocol
	conn          net.Conn
	output        chan []byte
	mu            sync.Mutex
	receiveMu     sync.Mutex
	telnetPending []byte
	telnetNAWS    bool
	telnetCols    uint16
	telnetRows    uint16
	closed        bool
	subscribers   map[uint64]chan []byte
	nextSubID     uint64
	commandMu     sync.Mutex
	prompt        string
	deviceProfile DeviceProfile
}

type Manager struct {
	sessions map[string]*Session
	mu       sync.RWMutex
	onData   func(string, []byte)
}

func NewManager() *Manager { return &Manager{sessions: make(map[string]*Session)} }

func (m *Manager) SetOnData(callback func(sessionID string, data []byte)) {
	m.mu.Lock()
	m.onData = callback
	m.mu.Unlock()
}

func (m *Manager) Connect(id, host string, port int, protocol Protocol) (string, error) {
	if protocol != ProtocolRaw && protocol != ProtocolTelnet {
		return "", fmt.Errorf("不支持的 TCP 协议: %s", protocol)
	}
	if host == "" || port < 1 || port > 65535 {
		return "", fmt.Errorf("TCP 主机或端口无效")
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprint(port)), 20*time.Second)
	if err != nil {
		return "", fmt.Errorf("连接 %s:%d 失败: %w", host, port, err)
	}
	sessionID := fmt.Sprintf("%s-%d", id, time.Now().UnixNano())
	session := &Session{ID: sessionID, Protocol: protocol, conn: conn, output: make(chan []byte, 1024), subscribers: make(map[uint64]chan []byte), telnetCols: 80, telnetRows: 24, deviceProfile: DeviceGeneric}
	m.mu.Lock()
	m.sessions[sessionID] = session
	m.mu.Unlock()
	go m.readLoop(session)
	return sessionID, nil
}

func (m *Manager) readLoop(session *Session) {
	buffer := make([]byte, 32*1024)
	for {
		n, err := session.conn.Read(buffer)
		if n > 0 {
			data := append([]byte(nil), buffer[:n]...)
			if session.Protocol == ProtocolTelnet {
				data = m.filterTelnet(session, data)
			}
			if len(data) > 0 {
				session.receiveMu.Lock()
				select {
				case session.output <- data:
				default:
					select {
					case <-session.output:
					default:
					}
					session.output <- data
				}
				m.publish(session, data)
				m.mu.RLock()
				onData := m.onData
				m.mu.RUnlock()
				if onData != nil {
					onData(session.ID, data)
				}
				session.receiveMu.Unlock()
			}
		}
		if err != nil {
			session.mu.Lock()
			session.closed = true
			session.mu.Unlock()
			return
		}
	}
}

func (m *Manager) Subscribe(id string) (<-chan []byte, func(), error) {
	m.mu.RLock()
	session := m.sessions[id]
	m.mu.RUnlock()
	if session == nil {
		return nil, nil, fmt.Errorf("TCP 会话不存在")
	}
	channel := make(chan []byte, 256)
	session.mu.Lock()
	if session.subscribers == nil {
		session.subscribers = make(map[uint64]chan []byte)
	}
	session.nextSubID++
	idValue := session.nextSubID
	session.subscribers[idValue] = channel
	session.mu.Unlock()
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			session.mu.Lock()
			delete(session.subscribers, idValue)
			close(channel)
			session.mu.Unlock()
		})
	}
	return channel, cancel, nil
}

func (m *Manager) publish(session *Session, data []byte) {
	session.mu.Lock()
	defer session.mu.Unlock()
	for _, subscriber := range session.subscribers {
		copyData := append([]byte(nil), data...)
		select {
		case subscriber <- copyData:
		default:
		}
	}
}

func (m *Manager) SetDeviceProfile(id string, profile DeviceProfile) error {
	m.mu.RLock()
	session := m.sessions[id]
	m.mu.RUnlock()
	if session == nil {
		return fmt.Errorf("TCP 会话不存在")
	}
	session.mu.Lock()
	session.deviceProfile = profile
	session.mu.Unlock()
	return nil
}

func (m *Manager) ExecuteCommand(ctx context.Context, id, command string) CommandResult {
	m.mu.RLock()
	session := m.sessions[id]
	m.mu.RUnlock()
	if session == nil {
		return CommandResult{Status: "error", Error: "TCP 会话不存在"}
	}
	if session.Protocol != ProtocolTelnet {
		return CommandResult{Status: "error", Error: "Raw TCP 不支持设备命令完成判定"}
	}
	session.commandMu.Lock()
	defer session.commandMu.Unlock()
	session.receiveMu.Lock()
	// Subscribe before draining the connection queue. This gives the command a
	// clean subscriber boundary: bytes already buffered (including a stale
	// prompt) are discarded before the command is written.
	outputCh, cancel, err := m.Subscribe(id)
	if err != nil {
		return CommandResult{Status: "error", Error: err.Error()}
	}
	defer cancel()
	var prior bytes.Buffer
	for {
		data, err := m.Read(id)
		if err != nil || len(data) == 0 {
			break
		}
		prior.Write(data)
	}
drainSubscriber:
	for {
		select {
		case <-outputCh:
			continue
		default:
			break drainSubscriber
		}
	}
	if prompt := detectPrompt(prior.String()); prompt != "" {
		session.mu.Lock()
		session.prompt = prompt
		session.mu.Unlock()
	}
	if err := m.Write(id, []byte(command+"\r")); err != nil {
		session.receiveMu.Unlock()
		return CommandResult{Status: "error", Error: err.Error()}
	}
	session.receiveMu.Unlock()
	var output bytes.Buffer
	pagerCount := 0
	pagerDetected := false
	for {
		select {
		case <-ctx.Done():
			_ = m.Write(id, []byte{3})
			return CommandResult{Status: "cancelled", Output: output.String(), Error: ctx.Err().Error()}
		case data, ok := <-outputCh:
			if !ok {
				return CommandResult{Status: "disconnected", Output: output.String(), Error: "输出订阅已关闭"}
			}
			output.Write(data)
			if output.Len() > 256*1024 {
				return CommandResult{Status: "truncated", Output: output.String()[:256*1024], Truncated: true}
			}
			if pager := detectPager(output.String()); pager && pagerCount < 64 {
				pagerDetected = true
				pagerCount++
				_ = m.Write(id, []byte(" "))
			}
			// The subscription is created after the pre-command buffer is drained,
			// so a prompt arriving now belongs to this command even when the device
			// suppresses command echo and returns no body (common on first login).
			if prompt := detectPrompt(output.String()); prompt != "" {
				session.mu.Lock()
				session.prompt = prompt
				session.mu.Unlock()
				return CommandResult{Status: "completed", Output: output.String(), Prompt: prompt, PagerDetected: pagerDetected, Complete: true}
			}
		}
	}
}

func hasDeviceBody(output, command string) bool {
	for _, raw := range strings.Split(strings.ReplaceAll(output, "\r", ""), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || line == strings.TrimSpace(command) || detectPrompt(line) != "" || detectPager(line) {
			continue
		}
		return true
	}
	return false
}

func (m *Manager) drain(id string) error {
	for {
		data, err := m.Read(id)
		if err != nil {
			return err
		}
		if len(data) == 0 {
			return nil
		}
	}
}

func detectPager(output string) bool {
	trimmed := strings.TrimSpace(output)
	return strings.HasSuffix(trimmed, "--More--") || strings.HasSuffix(trimmed, "---- More ----") || strings.HasSuffix(strings.ToLower(trimmed), "press any key to continue")
}

func detectPrompt(output string) string {
	output = ansiSequencePattern.ReplaceAllString(output, "")
	lines := strings.Split(strings.ReplaceAll(output, "\r", ""), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" || detectPager(line) {
			continue
		}
		if strings.HasSuffix(line, ">") || strings.HasSuffix(line, "#") || strings.HasSuffix(line, "]") || strings.HasSuffix(line, "$") || strings.HasSuffix(line, "%") {
			if !strings.ContainsAny(line, " \t") || strings.HasPrefix(line, "[") || strings.HasPrefix(line, "<") || strings.Contains(line, "@") || strings.Contains(line, ":") {
				return line
			}
		}
		break
	}
	return ""
}

// filterTelnet handles the common interactive Telnet options while removing
// protocol bytes from the terminal stream. Unknown options are declined.
func (m *Manager) filterTelnet(session *Session, data []byte) []byte {
	const (
		iac  = 255
		will = 251
		wont = 252
		do   = 253
		dont = 254
		sb   = 250
		se   = 240
		echo = 1
		sga  = 3
		term = 24
		naws = 31
		is   = 0
		send = 1
	)
	data = append(session.telnetPending, data...)
	session.telnetPending = nil
	plain := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		if data[i] != iac {
			plain = append(plain, data[i])
			continue
		}
		if i+1 >= len(data) {
			session.telnetPending = append(session.telnetPending[:0], data[i:]...)
			break
		}
		command := data[i+1]
		i++
		if command == iac {
			plain = append(plain, iac)
			continue
		}
		if command == sb {
			end, payload := findTelnetSubnegotiation(data, i+1)
			if end < 0 {
				session.telnetPending = append(session.telnetPending[:0], data[i-1:]...)
				break
			}
			if len(payload) >= 2 && payload[0] == term && payload[1] == send {
				session.mu.Lock()
				_, _ = session.conn.Write(telnetTerminalTypeResponse(term, is, "xterm-256color"))
				session.mu.Unlock()
			}
			i = end
			continue
		}
		if command == will || command == wont || command == do || command == dont {
			if i+1 >= len(data) {
				session.telnetPending = append(session.telnetPending[:0], data[i-1:]...)
				break
			}
			i++
			option := data[i]
			response, supported := telnetNegotiationResponse(command, option, echo, sga, term, naws, will, wont, do, dont)
			session.mu.Lock()
			_, _ = session.conn.Write([]byte{iac, response, option})
			if supported && command == do && option == naws {
				session.telnetNAWS = true
				_, _ = session.conn.Write(telnetNAWSResponse(session.telnetCols, session.telnetRows))
			}
			session.mu.Unlock()
		}
	}
	return plain
}

func findTelnetSubnegotiation(data []byte, start int) (int, []byte) {
	for j := start; j+1 < len(data); j++ {
		if data[j] != 255 || data[j+1] != 240 {
			continue
		}
		payload := append([]byte(nil), data[start:j]...)
		return j + 1, payload
	}
	return -1, nil
}

func telnetNegotiationResponse(command, option byte, echo, sga, term, naws, will, wont, do, dont byte) (byte, bool) {
	supported := option == sga || option == term || option == naws
	switch command {
	case do:
		if supported {
			return will, true
		}
		return wont, false
	case dont:
		return wont, false
	case will:
		if option == echo || option == sga {
			return do, true
		}
		return dont, false
	case wont:
		return dont, false
	default:
		return dont, false
	}
}

func telnetTerminalTypeResponse(term, is byte, name string) []byte {
	data := []byte{255, 250, term, is}
	data = append(data, []byte(name)...)
	return append(data, 255, 240)
}

func telnetNAWSResponse(cols, rows uint16) []byte {
	data := []byte{255, 250, 31}
	for _, value := range []byte{byte(cols >> 8), byte(cols), byte(rows >> 8), byte(rows)} {
		data = append(data, value)
		if value == 255 {
			data = append(data, value)
		}
	}
	return append(data, 255, 240)
}

// SetSize updates the Telnet window size and sends NAWS after the server has
// negotiated the option.
func (m *Manager) SetSize(id string, cols, rows uint16) error {
	if cols == 0 || rows == 0 {
		return fmt.Errorf("Telnet 窗口尺寸无效")
	}
	m.mu.RLock()
	session := m.sessions[id]
	m.mu.RUnlock()
	if session == nil {
		return fmt.Errorf("TCP 会话不存在")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	session.telnetCols, session.telnetRows = cols, rows
	if session.Protocol != ProtocolTelnet || !session.telnetNAWS {
		return nil
	}
	_, err := session.conn.Write(telnetNAWSResponse(cols, rows))
	return err
}

func (m *Manager) Write(id string, data []byte) error {
	m.mu.RLock()
	session := m.sessions[id]
	m.mu.RUnlock()
	if session == nil {
		return fmt.Errorf("TCP 会话不存在")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.Protocol == ProtocolTelnet {
		data = escapeTelnetData(data)
	}
	_, err := session.conn.Write(data)
	return err
}

func escapeTelnetData(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	const iac = byte(255)
	escaped := make([]byte, 0, len(data))
	for _, value := range data {
		escaped = append(escaped, value)
		if value == iac {
			escaped = append(escaped, iac)
		}
	}
	return escaped
}

func (m *Manager) Read(id string) ([]byte, error) {
	m.mu.RLock()
	session := m.sessions[id]
	m.mu.RUnlock()
	if session == nil {
		return nil, fmt.Errorf("TCP 会话不存在")
	}
	session.mu.Lock()
	closed := session.closed
	session.mu.Unlock()
	if closed {
		return nil, fmt.Errorf("TCP 连接已断开")
	}
	select {
	case data := <-session.output:
		return data, nil
	default:
		return []byte{}, nil
	}
}

func (m *Manager) HasSession(id string) bool {
	m.mu.RLock()
	_, ok := m.sessions[id]
	m.mu.RUnlock()
	return ok
}

func (m *Manager) Protocol(id string) (Protocol, bool) {
	m.mu.RLock()
	session := m.sessions[id]
	m.mu.RUnlock()
	if session == nil {
		return "", false
	}
	return session.Protocol, true
}

func (m *Manager) Disconnect(id string) error {
	m.mu.Lock()
	session := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if session == nil {
		return fmt.Errorf("TCP 会话不存在")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.conn.Close()
}

func (m *Manager) DisconnectAll() {
	m.mu.Lock()
	sessions := m.sessions
	m.sessions = make(map[string]*Session)
	m.mu.Unlock()
	for _, session := range sessions {
		_ = session.conn.Close()
	}
}
func (m *Manager) ListSessions() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	values := make(map[string]string, len(m.sessions))
	for id, s := range m.sessions {
		values[id] = string(s.Protocol)
	}
	result, _ := json.Marshal(values)
	return string(result)
}
