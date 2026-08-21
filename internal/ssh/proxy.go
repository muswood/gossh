// owner: muswood | Email: mumu920@outlook.com
package ssh

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const proxyDialTimeout = 30 * time.Second

// dialTransport opens the first SSH transport. ProxyCommand is intentionally
// user-configured and follows OpenSSH's %h/%p token expansion.
func dialTransport(cfg *ConnectionConfig, target string) (net.Conn, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.ProxyType)) {
	case "", "none", "direct":
		return net.DialTimeout("tcp", target, proxyDialTimeout)
	case "http", "http_connect":
		return dialHTTPConnect(cfg, target)
	case "socks5", "socks":
		return dialSOCKS5(cfg, target)
	case "command", "proxycommand":
		return dialProxyCommand(cfg.ProxyCommand, cfg.Host, cfg.Port)
	default:
		return nil, fmt.Errorf("不支持的代理类型: %s", cfg.ProxyType)
	}
}

func proxyAddress(host string) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "", fmt.Errorf("代理地址不能为空")
	}
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(host, "1080")
	}
	return host, nil
}

func dialHTTPConnect(cfg *ConnectionConfig, target string) (net.Conn, error) {
	addr, err := proxyAddress(cfg.ProxyHost)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTimeout("tcp", addr, proxyDialTimeout)
	if err != nil {
		return nil, fmt.Errorf("连接 HTTP 代理失败: %w", err)
	}
	request := "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n"
	if cfg.ProxyUsername != "" {
		token := base64.StdEncoding.EncodeToString([]byte(cfg.ProxyUsername + ":" + cfg.ProxyPassword))
		request += "Proxy-Authorization: Basic " + token + "\r\n"
	}
	request += "Connection: keep-alive\r\n\r\n"
	if _, err := io.WriteString(conn, request); err != nil {
		conn.Close()
		return nil, err
	}
	reader := bufio.NewReader(conn)
	response, err := reader.ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("读取 HTTP 代理响应失败: %w", err)
	}
	if !strings.Contains(response, " 200 ") {
		conn.Close()
		return nil, fmt.Errorf("HTTP CONNECT 被代理拒绝: %s", strings.TrimSpace(response))
	}
	// Consume headers. bufio.Reader cannot be discarded because it may already
	// hold tunneled bytes, so return a connection that reads from it.
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			conn.Close()
			return nil, fmt.Errorf("读取 HTTP 代理响应头失败: %w", readErr)
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}
	return &bufferedConn{Conn: conn, reader: reader}, nil
}

func dialSOCKS5(cfg *ConnectionConfig, target string) (net.Conn, error) {
	addr, err := proxyAddress(cfg.ProxyHost)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTimeout("tcp", addr, proxyDialTimeout)
	if err != nil {
		return nil, fmt.Errorf("连接 SOCKS5 代理失败: %w", err)
	}
	methods := []byte{0x00}
	if cfg.ProxyUsername != "" {
		methods = append(methods, 0x02)
	}
	if _, err := conn.Write(append([]byte{0x05, byte(len(methods))}, methods...)); err != nil {
		conn.Close()
		return nil, err
	}
	response := make([]byte, 2)
	if _, err := io.ReadFull(conn, response); err != nil || response[0] != 0x05 || response[1] == 0xff {
		conn.Close()
		return nil, fmt.Errorf("SOCKS5 认证协商失败")
	}
	if response[1] == 0x02 {
		user, pass := []byte(cfg.ProxyUsername), []byte(cfg.ProxyPassword)
		if len(user) == 0 || len(user) > 255 || len(pass) > 255 {
			conn.Close()
			return nil, fmt.Errorf("SOCKS5 用户名或口令无效")
		}
		packet := append([]byte{0x01, byte(len(user))}, user...)
		packet = append(packet, byte(len(pass)))
		packet = append(packet, pass...)
		if _, err := conn.Write(packet); err != nil {
			conn.Close()
			return nil, err
		}
		if _, err := io.ReadFull(conn, response); err != nil || response[1] != 0x00 {
			conn.Close()
			return nil, fmt.Errorf("SOCKS5 用户名或口令被拒绝")
		}
	}
	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		conn.Close()
		return nil, err
	}
	port, err := net.LookupPort("tcp", portText)
	if err != nil {
		conn.Close()
		return nil, err
	}
	packet := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			packet = append(packet, 0x01)
			packet = append(packet, ip4...)
		} else {
			packet = append(packet, 0x04)
			packet = append(packet, ip...)
		}
	} else {
		if len(host) == 0 || len(host) > 255 {
			conn.Close()
			return nil, fmt.Errorf("SOCKS5 目标域名无效")
		}
		packet = append(packet, 0x03, byte(len(host)))
		packet = append(packet, host...)
	}
	packet = append(packet, byte(port>>8), byte(port))
	if _, err := conn.Write(packet); err != nil {
		conn.Close()
		return nil, err
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil || header[0] != 0x05 || header[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("SOCKS5 CONNECT 被拒绝")
	}
	length := 0
	switch header[3] {
	case 0x01:
		length = 4
	case 0x04:
		length = 16
	case 0x03:
		b := []byte{0}
		if _, err := io.ReadFull(conn, b); err != nil {
			conn.Close()
			return nil, err
		}
		length = int(b[0])
	default:
		conn.Close()
		return nil, fmt.Errorf("SOCKS5 响应地址类型无效")
	}
	if _, err := io.ReadFull(conn, make([]byte, length+2)); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

type proxyCommandConn struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	cmd    *exec.Cmd
	once   sync.Once
}

func (c *proxyCommandConn) Read(p []byte) (int, error)  { return c.stdout.Read(p) }
func (c *proxyCommandConn) Write(p []byte) (int, error) { return c.stdin.Write(p) }
func (c *proxyCommandConn) Close() error {
	var err error
	c.once.Do(func() { _ = c.stdin.Close(); _ = c.stdout.Close(); err = c.cmd.Process.Kill(); _ = c.cmd.Wait() })
	return err
}
func (c *proxyCommandConn) LocalAddr() net.Addr              { return proxyAddr("proxycommand") }
func (c *proxyCommandConn) RemoteAddr() net.Addr             { return proxyAddr("proxycommand") }
func (c *proxyCommandConn) SetDeadline(time.Time) error      { return nil }
func (c *proxyCommandConn) SetReadDeadline(time.Time) error  { return nil }
func (c *proxyCommandConn) SetWriteDeadline(time.Time) error { return nil }

type proxyAddr string

func (a proxyAddr) Network() string { return string(a) }
func (a proxyAddr) String() string  { return string(a) }

func dialProxyCommand(command, host string, port int) (net.Conn, error) {
	if strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("ProxyCommand 不能为空")
	}
	command = strings.ReplaceAll(command, "%%", "%")
	command = strings.ReplaceAll(command, "%h", host)
	command = strings.ReplaceAll(command, "%p", fmt.Sprint(port))
	cmd := exec.Command("sh", "-c", command)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 ProxyCommand 失败: %w", err)
	}
	return &proxyCommandConn{stdin: stdin, stdout: stdout, cmd: cmd}, nil
}
