// owner: muswood | Email: mumu920@outlook.com
package serial

import (
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	goserial "go.bug.st/serial"
)

type Config struct {
	PortName      string `json:"portName"`
	BaudRate      int    `json:"baudRate"`
	DataBits      int    `json:"dataBits"`
	StopBits      int    `json:"stopBits"`
	Parity        string `json:"parity"`
	HexMode       bool   `json:"hexMode"`
	AutoReconnect bool   `json:"autoReconnect"`
	Encoding      string `json:"encoding"`
}

type Client struct {
	port         goserial.Port
	config       Config
	mu           sync.Mutex
	reconnecting bool
	stopCh       chan struct{}
	onData       func(data []byte)
}

func NewClient() *Client {
	return &Client{stopCh: make(chan struct{})}
}

func (c *Client) SetOnData(cb func(data []byte)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onData = cb
}

func (c *Client) ListPorts() ([]string, error) {
	ports, err := goserial.GetPortsList()
	if err != nil {
		return nil, fmt.Errorf("获取串口列表失败: %w", err)
	}
	return ports, nil
}

func (c *Client) Connect(cfg Config) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopCh != nil {
		close(c.stopCh)
	}
	if c.port != nil {
		_ = c.port.Close()
		c.port = nil
	}
	c.config = cfg
	c.stopCh = make(chan struct{})
	c.reconnecting = false
	if err := c.openLocked(); err != nil {
		if cfg.AutoReconnect {
			c.startReconnectLocked(c.stopCh)
			return nil
		}
		return err
	}
	return nil
}

func (c *Client) openLocked() error {
	cfg := c.config
	if cfg.BaudRate == 0 {
		cfg.BaudRate = 115200
	}
	if cfg.DataBits == 0 {
		cfg.DataBits = 8
	}
	if cfg.StopBits == 0 {
		cfg.StopBits = 1
	}

	var parity goserial.Parity
	switch cfg.Parity {
	case "even":
		parity = goserial.EvenParity
	case "odd":
		parity = goserial.OddParity
	case "mark":
		parity = goserial.MarkParity
	case "space":
		parity = goserial.SpaceParity
	default:
		parity = goserial.NoParity
	}

	mode := &goserial.Mode{
		BaudRate: cfg.BaudRate,
		Parity:   parity,
		DataBits: cfg.DataBits,
		StopBits: goserial.StopBits(cfg.StopBits),
	}

	port, err := goserial.Open(cfg.PortName, mode)
	if err != nil {
		return fmt.Errorf("打开串口 %s 失败: %w", cfg.PortName, err)
	}

	if err := port.SetReadTimeout(100 * time.Millisecond); err != nil {
		port.Close()
		return fmt.Errorf("设置读超时失败: %w", err)
	}

	c.port = port
	c.config = cfg
	return nil
}

func (c *Client) startReconnectLocked(stopCh chan struct{}) {
	if c.reconnecting {
		return
	}
	c.reconnecting = true
	go c.reconnectLoop(stopCh)
}

func (c *Client) reconnectLoop(stopCh chan struct{}) {
	defer func() {
		c.mu.Lock()
		if c.stopCh == stopCh {
			c.reconnecting = false
		}
		c.mu.Unlock()
	}()

	for {
		select {
		case <-stopCh:
			return
		default:
		}

		c.mu.Lock()
		if c.stopCh != stopCh {
			c.mu.Unlock()
			return
		}
		err := c.openLocked()
		connected := c.port != nil
		c.mu.Unlock()
		if err == nil && connected {
			return
		}

		select {
		case <-stopCh:
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func (c *Client) Write(data []byte) (int, error) {
	c.mu.Lock()
	port := c.port
	c.mu.Unlock()
	if port == nil {
		return 0, fmt.Errorf("串口未连接")
	}
	return port.Write(data)
}

func (c *Client) WriteString(s string) (int, error) {
	return c.Write([]byte(s))
}

func (c *Client) WriteHex(hexStr string) (int, error) {
	raw, err := hex.DecodeString(hexStr)
	if err != nil {
		return 0, fmt.Errorf("无效的十六进制数据: %w", err)
	}
	return c.Write(raw)
}

func (c *Client) Read(buf []byte) (int, error) {
	c.mu.Lock()
	port := c.port
	cfg := c.config
	stopCh := c.stopCh
	c.mu.Unlock()
	if port == nil {
		return 0, fmt.Errorf("串口未连接")
	}
	n, err := port.Read(buf)
	if err != nil {
		c.mu.Lock()
		if c.port == port {
			_ = port.Close()
			c.port = nil
			if cfg.AutoReconnect && stopCh != nil {
				c.startReconnectLocked(stopCh)
			}
		}
		c.mu.Unlock()
	}
	return n, err
}

func (c *Client) ReadString(maxLen int) (string, error) {
	buf := make([]byte, maxLen)
	n, err := c.Read(buf)
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "", nil
	}
	if c.GetConfig().HexMode {
		return hex.EncodeToString(buf[:n]), nil
	}
	return string(buf[:n]), nil
}

func (c *Client) ReadHex(maxBytes int) (string, error) {
	buf := make([]byte, maxBytes)
	n, err := c.Read(buf)
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "", nil
	}
	return hex.EncodeToString(buf[:n]), nil
}

func (c *Client) ReadLoop() {
	c.mu.Lock()
	stopCh := c.stopCh
	c.mu.Unlock()
	if stopCh == nil {
		return
	}
	go func() {
		buf := make([]byte, 256)
		for {
			select {
			case <-stopCh:
				return
			default:
			}
			n, err := c.Read(buf)
			if err != nil || n == 0 {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			c.mu.Lock()
			cb := c.onData
			c.mu.Unlock()
			if cb != nil {
				data := make([]byte, n)
				copy(data, buf[:n])
				cb(data)
			}
		}
	}()
}

func (c *Client) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopCh != nil {
		close(c.stopCh)
		c.stopCh = nil
	}
	c.reconnecting = false
	if c.port != nil {
		err := c.port.Close()
		c.port = nil
		return err
	}
	return nil
}

func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.port != nil
}

func (c *Client) GetConfig() Config {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.config
}
