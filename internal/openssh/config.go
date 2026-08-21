// owner: muswood | Email: mumu920@outlook.com
package openssh

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type ForwardSpec struct {
	ID         string
	Type       string
	LocalHost  string
	LocalPort  int
	RemoteHost string
	RemotePort int
}

// Profile is the useful, connection-oriented subset of an OpenSSH Host block.
type Profile struct {
	Alias               string
	HostName            string
	User                string
	Port                int
	IdentityFile        string
	CertificateFile     string
	ProxyJump           string
	ProxyCommand        string
	ServerAliveInterval int
	UseAgent            bool
	ForwardAgent        bool
	RequestTTY          string
	RemoteCommand       string
	Forwards            []ForwardSpec
}

// ParseConfig reads common OpenSSH client configuration. Pattern-only blocks
// are ignored because they cannot become a standalone saved connection.
func ParseConfig(data string) ([]Profile, error) {
	profiles := make([]Profile, 0)
	var current []int

	apply := func(key, value string) error {
		if len(current) == 0 {
			return nil
		}
		for _, index := range current {
			profile := &profiles[index]
			switch strings.ToLower(key) {
			case "hostname":
				profile.HostName = value
			case "user":
				profile.User = value
			case "port":
				var port int
				if _, err := fmt.Sscanf(value, "%d", &port); err != nil || port < 1 || port > 65535 {
					return fmt.Errorf("Host %s 的端口无效: %q", profile.Alias, value)
				}
				profile.Port = port
			case "identityfile":
				profile.IdentityFile = expandHome(value)
			case "certificatefile":
				profile.CertificateFile = expandHome(value)
			case "proxyjump":
				profile.ProxyJump = value
			case "proxycommand":
				profile.ProxyCommand = value
			case "serveraliveinterval":
				var interval int
				if _, err := fmt.Sscanf(value, "%d", &interval); err != nil || interval < 0 {
					return fmt.Errorf("Host %s 的心跳间隔无效: %q", profile.Alias, value)
				}
				profile.ServerAliveInterval = interval
			case "identityagent":
				profile.UseAgent = value != "none"
			case "forwardagent":
				profile.ForwardAgent = parseBool(value)
			case "requesttty":
				profile.RequestTTY = value
			case "remotecommand":
				profile.RemoteCommand = value
			case "localforward":
				spec, err := parseForward(value, "local", profile.Alias, len(profile.Forwards))
				if err != nil {
					return err
				}
				profile.Forwards = append(profile.Forwards, spec)
			case "remoteforward":
				spec, err := parseForward(value, "remote", profile.Alias, len(profile.Forwards))
				if err != nil {
					return err
				}
				profile.Forwards = append(profile.Forwards, spec)
			case "dynamicforward":
				spec, err := parseForward(value, "dynamic", profile.Alias, len(profile.Forwards))
				if err != nil {
					return err
				}
				profile.Forwards = append(profile.Forwards, spec)
			}
		}
		return nil
	}

	scanner := bufio.NewScanner(strings.NewReader(data))
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(stripComment(scanner.Text()))
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			return nil, fmt.Errorf("第 %d 行格式无效", lineNumber)
		}
		key := strings.ToLower(parts[0])
		value := strings.TrimSpace(strings.TrimPrefix(line[len(parts[0]):], "="))
		value = strings.Trim(strings.TrimSpace(value), "\"")
		if key == "host" {
			current = current[:0]
			for _, alias := range strings.Fields(value) {
				if strings.ContainsAny(alias, "*!?") {
					continue
				}
				profiles = append(profiles, Profile{Alias: alias, HostName: alias, Port: 22})
				current = append(current, len(profiles)-1)
			}
			continue
		}
		if err := apply(key, value); err != nil {
			return nil, err
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return profiles, nil
}

// ParseFile also resolves Include directives relative to the selected config
// file. The parser keeps includes intentionally bounded and ignores patterns
// that do not match a local file, as OpenSSH does.
func ParseFile(path string) ([]Profile, error) {
	seen := make(map[string]bool)
	var read func(string) (string, error)
	read = func(name string) (string, error) {
		absolute, err := filepath.Abs(name)
		if err != nil {
			return "", err
		}
		if seen[absolute] {
			return "", nil
		}
		seen[absolute] = true
		data, err := os.ReadFile(absolute)
		if err != nil {
			return "", err
		}
		lines := make([]string, 0)
		for _, line := range strings.Split(string(data), "\n") {
			parts := strings.Fields(strings.TrimSpace(stripComment(line)))
			if len(parts) >= 2 && strings.EqualFold(parts[0], "include") {
				for _, pattern := range parts[1:] {
					pattern = expandHome(pattern)
					if !filepath.IsAbs(pattern) {
						pattern = filepath.Join(filepath.Dir(absolute), pattern)
					}
					matches, _ := filepath.Glob(pattern)
					for _, match := range matches {
						included, includeErr := read(match)
						if includeErr != nil {
							return "", includeErr
						}
						lines = append(lines, included)
					}
				}
				continue
			}
			lines = append(lines, line)
		}
		return strings.Join(lines, "\n"), nil
	}
	data, err := read(path)
	if err != nil {
		return nil, err
	}
	return ParseConfig(data)
}

func stripComment(line string) string {
	inQuote := false
	for i, r := range line {
		if r == '"' {
			inQuote = !inQuote
		} else if r == '#' && !inQuote {
			return line[:i]
		}
	}
	return line
}

func expandHome(path string) string {
	if path == "~" {
		return "~"
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join("~", path[2:])
	}
	return path
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "yes", "true", "on", "1":
		return true
	default:
		return false
	}
}

func parseForward(value, kind, alias string, index int) (ForwardSpec, error) {
	fields := strings.Fields(value)
	if kind == "dynamic" {
		if len(fields) != 1 {
			return ForwardSpec{}, fmt.Errorf("Host %s 的 DynamicForward 无效: %q", alias, value)
		}
		host, port, err := parseListen(fields[0], "127.0.0.1")
		if err != nil {
			return ForwardSpec{}, fmt.Errorf("Host %s 的 DynamicForward 无效: %w", alias, err)
		}
		return ForwardSpec{ID: forwardID(alias, kind, index), Type: kind, LocalHost: host, LocalPort: port}, nil
	}
	if len(fields) != 2 {
		return ForwardSpec{}, fmt.Errorf("Host %s 的 %s 无效: %q", alias, kind, value)
	}
	listenHost, listenPort, err := parseListen(fields[0], "127.0.0.1")
	if err != nil {
		return ForwardSpec{}, fmt.Errorf("Host %s 的 %s 监听地址无效: %w", alias, kind, err)
	}
	targetHost, targetPort, err := parseTarget(fields[1])
	if err != nil {
		return ForwardSpec{}, fmt.Errorf("Host %s 的 %s 目标地址无效: %w", alias, kind, err)
	}
	if kind == "remote" {
		return ForwardSpec{
			ID: forwardID(alias, kind, index), Type: kind,
			LocalHost: targetHost, LocalPort: targetPort, RemoteHost: listenHost, RemotePort: listenPort,
		}, nil
	}
	return ForwardSpec{
		ID: forwardID(alias, kind, index), Type: kind,
		LocalHost: listenHost, LocalPort: listenPort, RemoteHost: targetHost, RemotePort: targetPort,
	}, nil
}

func parseListen(value, defaultHost string) (string, int, error) {
	host, port, err := splitHostPort(value, defaultHost)
	if err != nil {
		return "", 0, err
	}
	return firstNonEmpty(host, defaultHost), port, nil
}

func parseTarget(value string) (string, int, error) {
	return splitHostPort(value, "")
}

func splitHostPort(value, defaultHost string) (string, int, error) {
	if host, portText, err := net.SplitHostPort(value); err == nil {
		port, err := parsePort(portText)
		return host, port, err
	}
	if strings.Count(value, ":") == 0 {
		port, err := parsePort(value)
		return defaultHost, port, err
	}
	at := strings.LastIndex(value, ":")
	if at <= 0 || at == len(value)-1 {
		return "", 0, fmt.Errorf("地址格式无效: %q", value)
	}
	port, err := parsePort(value[at+1:])
	if err != nil {
		return "", 0, err
	}
	return value[:at], port, nil
}

func parsePort(value string) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("端口无效: %q", value)
	}
	return port, nil
}

func forwardID(alias, kind string, index int) string {
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, alias)
	return fmt.Sprintf("openssh-%s-%s-%d", name, kind, index+1)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
