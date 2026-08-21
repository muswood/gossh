// owner: muswood | Email: mumu920@outlook.com
package agent

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

type SecurityConfig struct {
	WhitelistEnabled     bool     `json:"whitelistEnabled"`
	BlacklistEnabled     bool     `json:"blacklistEnabled"`
	MutationsEnabled     bool     `json:"mutationsEnabled"`
	DeletionsEnabled     bool     `json:"deletionsEnabled"`
	AdministratorEnabled bool     `json:"administratorEnabled"`
	ReadOnlyNoApproval   bool     `json:"readOnlyNoApproval"`
	CommandWhitelist     []string `json:"commandWhitelist"`
	CommandBlacklist     []string `json:"commandBlacklist"`
}

type PolicyDecision struct {
	Allowed       bool   `json:"allowed"`
	Reason        string `json:"reason,omitempty"`
	Risk          string `json:"risk,omitempty"`
	ReadOnly      bool   `json:"readOnly"`
	Mutating      bool   `json:"mutating"`
	Deleting      bool   `json:"deleting"`
	Administrator bool   `json:"administrator,omitempty"`
}

type shellAST struct {
	Pipeline []shellCommand
}

type shellCommand struct {
	Name string
	Args []string
}

// AssessCommand intentionally accepts a small command vocabulary. A semicolon
// may join multiple independently validated commands; other shell control
// operators are rejected before command-specific checks.
func AssessCommand(command string) PolicyDecision {
	return AssessCommandMode(command, false)
}

// AssessCommandMode applies the same shell grammar checks in read-only and
// explicitly authorized mutation mode. Mutation mode is never implicit.
func AssessCommandMode(command string, allowMutating bool) PolicyDecision {
	security := GetSecurityConfig()
	if security.AdministratorEnabled {
		return AssessAdministratorCommand(command)
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return blocked("命令不能为空", "empty")
	}
	ast, unsafe, err := parseShellAST(command)
	if err != nil {
		return blocked("命令语法无效: "+err.Error(), "syntax")
	}
	if unsafe {
		return blocked("命令包含不允许的 Shell 控制符、重定向或命令替换", "shell_syntax")
	}
	if len(ast.Pipeline) == 0 {
		return blocked("命令不能为空", "empty")
	}
	if isMutatingWord(strings.ToLower(ast.Pipeline[0].Name)) && !allowMutating {
		return blocked("命令包含可能修改系统或数据的操作", "mutation")
	}
	mutating := false
	deleting := false
	for _, segment := range ast.Pipeline {
		commandName := strings.ToLower(filepath.Base(segment.Name))
		segmentDeleting := isDeleteCommand(commandName, segment.Args)
		if security.BlacklistEnabled && containsCommand(security.CommandBlacklist, commandName) {
			return blocked("命令位于 GoSSH 命令黑名单中: "+commandName, "blacklisted")
		}
		if blockedCommand[commandName] {
			return blocked("命令可能绕过 GoSSH 安全策略", "mutation")
		}
		if segmentDeleting && !security.DeletionsEnabled {
			return blocked("删除操作未在 GoSSH 安全配置中启用", "mutation")
		}
		if segmentDeleting && !safeDeletionTarget(commandName, segment.Args) {
			return blocked("删除目标命中了受保护路径或通配符，操作被拒绝", "mutation")
		}
		segmentMutating := false
		if isMutatingCommand(commandName, segment.Args) {
			if !allowMutating || !security.MutationsEnabled || !allowedMutationCommand(commandName, segment.Args) {
				return blocked("命令不在 GoSSH 允许的命令白名单中: "+commandName, "not_allowlisted")
			}
			segmentMutating = true
			mutating = true
			deleting = deleting || segmentDeleting
		} else if security.WhitelistEnabled && !containsCommand(security.CommandWhitelist, commandName) {
			return blocked("命令不在 GoSSH 允许的命令白名单中: "+commandName, "not_allowlisted")
		} else if !security.WhitelistEnabled && !knownCommand(commandName) {
			return blocked("无法确认该命令的安全属性，请加入白名单后重试", "unknown_command")
		}
		validArgs := validSubcommand(commandName, segment.Args)
		if segmentMutating {
			if segmentDeleting {
				validArgs = validDeletionSubcommand(segment.Args)
			} else {
				validArgs = validMutationSubcommand(commandName, segment.Args)
			}
		}
		if !validArgs {
			return blocked("该命令的参数可能产生修改或执行副作用", "mutation")
		}
	}
	return PolicyDecision{Allowed: true, Risk: "approval_required", ReadOnly: !mutating, Mutating: mutating, Deleting: deleting}
}

// AssessCommandBaseline performs only the local checks that must not be
// delegated to a model. Semantic read/write classification is performed by
// the AI layer, while shell injection, interpreter bypasses, and unsafe delete
// targets remain program-enforced.
func AssessCommandBaseline(command string) PolicyDecision {
	if GetSecurityConfig().AdministratorEnabled {
		return AssessAdministratorCommand(command)
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return blocked("命令不能为空", "empty")
	}
	ast, unsafe, err := parseShellAST(command)
	if err != nil {
		return blocked("命令语法无效: "+err.Error(), "syntax")
	}
	if unsafe {
		return blocked("命令包含不允许的 Shell 控制符、重定向或命令替换", "shell_syntax")
	}
	if len(ast.Pipeline) == 0 {
		return blocked("命令不能为空", "empty")
	}
	deleting := false
	for _, segment := range ast.Pipeline {
		commandName := strings.ToLower(filepath.Base(segment.Name))
		if blockedCommand[commandName] {
			return blocked("命令可能绕过 GoSSH 安全策略", "mutation")
		}
		if isDeleteCommand(commandName, segment.Args) {
			deleting = true
			if !safeDeletionTarget(commandName, segment.Args) {
				return blocked("删除目标命中了受保护路径或通配符，操作被拒绝", "mutation")
			}
		}
	}
	return PolicyDecision{Allowed: true, Risk: "ai_review_required", ReadOnly: !deleting, Mutating: deleting, Deleting: deleting}
}

// AssessAdministratorCommand deliberately bypasses all command-policy checks.
// The result still requires the normal user approval; recognised destructive
// commands are marked so the caller can require the separate delete approval.
func AssessAdministratorCommand(command string) PolicyDecision {
	command = strings.TrimSpace(command)
	if command == "" {
		return blocked("命令不能为空", "empty")
	}
	return PolicyDecision{
		Allowed:       true,
		Risk:          "administrator_approval_required",
		Mutating:      true,
		Deleting:      containsDeleteOperation(command),
		Administrator: true,
	}
}

var administratorDeletePattern = regexp.MustCompile(`(?i)\b(?:sudo\s+)?(?:rm|rmdir|unlink|shred)\b|\b(?:kubectl|oc)\s+delete\b|\bhelm\s+(?:delete|uninstall)\b|\b(?:docker|podman|nerdctl|crictl|ctr)\s+(?:(?:container|image|volume|network)\s+)?(?:rm|rmi|remove|prune)\b|\b(?:apt|apt-get|yum|dnf|zypper|apk)\s+(?:remove|purge|autoremove|erase)\b|\bgit\s+rm\b|\bfind\b.*(?:-delete|-exec(?:dir)?\s+rm\b)|\b(?:drop|truncate)\s+(?:database|table)\b|\bdelete\s+from\b|\b(?:os|fs|shutil)\.(?:remove|unlink|rmdir|rmtree|rm)\b|\b(?:remove|unlink|rmdir|rmtree)\s*\(`)

func containsDeleteOperation(command string) bool {
	if ast, _, err := parseShellAST(command); err == nil {
		for _, segment := range ast.Pipeline {
			if isDeleteCommand(strings.ToLower(filepath.Base(segment.Name)), segment.Args) {
				return true
			}
		}
	}
	return administratorDeletePattern.MatchString(command)
}

var readOnlyCommand = map[string]bool{
	"cat": true, "cut": true, "df": true, "du": true, "echo": true, "env": true,
	"file": true, "find": true, "free": true, "getent": true, "grep": true,
	"head": true, "hostname": true, "id": true, "ip": true, "journalctl": true,
	"kubectl": true, "last": true, "ls": true, "lsof": true, "mount": true,
	"lscpu": true, "netstat": true, "nproc": true, "ps": true, "pwd": true, "rg": true, "sed": true,
	"ss": true, "stat": true, "systemctl": true, "tail": true, "top": true,
	"uname": true, "uptime": true, "who": true, "whoami": true,
	"docker": true, "helm": true, "git": true,
}

var mutationCommand = map[string]bool{
	"mkdir": true, "touch": true, "cp": true, "mv": true,
}

// mutationSubcommand allows explicit operational changes for commands that
// are otherwise read-only. Destructive data operations remain outside this
// list even when mutations are enabled.
var mutationSubcommand = map[string]map[string]bool{
	"systemctl": {
		"disable": true, "enable": true, "reload": true, "restart": true,
		"start": true, "stop": true,
	},
}

var blockedCommand = map[string]bool{
	"bash": true, "cmd": true, "csh": true, "dash": true, "fish": true,
	"ksh": true, "node": true, "perl": true, "powershell": true, "pwsh": true,
	"python": true, "python3": true, "ruby": true, "sh": true, "sudo": true,
	"su": true, "tcsh": true, "tee": true, "xargs": true, "zsh": true,
}

var (
	securityConfigMu sync.RWMutex
	securityConfig   = defaultSecurityConfig()
)

func defaultSecurityConfig() SecurityConfig {
	commands := make([]string, 0, len(readOnlyCommand))
	for command := range readOnlyCommand {
		commands = append(commands, command)
	}
	sort.Strings(commands)
	return SecurityConfig{
		WhitelistEnabled:   true,
		BlacklistEnabled:   true,
		MutationsEnabled:   true,
		DeletionsEnabled:   false,
		ReadOnlyNoApproval: false,
		CommandWhitelist:   commands,
		CommandBlacklist:   []string{},
	}
}

func DefaultSecurityConfig() SecurityConfig {
	return normalizeSecurityConfig(defaultSecurityConfig())
}

func GetSecurityConfig() SecurityConfig {
	securityConfigMu.RLock()
	defer securityConfigMu.RUnlock()
	return cloneSecurityConfig(securityConfig)
}

func SetSecurityConfig(config SecurityConfig) {
	securityConfigMu.Lock()
	securityConfig = normalizeSecurityConfig(config)
	securityConfigMu.Unlock()
}

func normalizeSecurityConfig(config SecurityConfig) SecurityConfig {
	if config.CommandWhitelist == nil {
		config.CommandWhitelist = defaultSecurityConfig().CommandWhitelist
	}
	config.CommandWhitelist = normalizeCommands(config.CommandWhitelist)
	config.CommandBlacklist = normalizeCommands(config.CommandBlacklist)
	return config
}

func cloneSecurityConfig(config SecurityConfig) SecurityConfig {
	config.CommandWhitelist = append([]string(nil), config.CommandWhitelist...)
	config.CommandBlacklist = append([]string(nil), config.CommandBlacklist...)
	return config
}

func normalizeCommands(commands []string) []string {
	seen := make(map[string]struct{}, len(commands))
	result := make([]string, 0, len(commands))
	for _, command := range commands {
		name := strings.ToLower(strings.TrimSpace(filepath.Base(command)))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func containsCommand(commands []string, command string) bool {
	command = strings.ToLower(filepath.Base(strings.TrimSpace(command)))
	for _, allowed := range commands {
		if allowed == command {
			return true
		}
	}
	return false
}

func validMutationSubcommand(name string, args []string) bool {
	if len(args) == 0 {
		return false
	}
	for _, arg := range args {
		if arg == "-r" || arg == "-R" || arg == "--recursive" || strings.ContainsAny(arg, ";&|<>`$") {
			return false
		}
	}
	return true
}

func validDeletionSubcommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	for _, arg := range args {
		if strings.ContainsAny(arg, ";&|<>`$") {
			return false
		}
	}
	return true
}

func knownCommand(name string) bool {
	if readOnlyCommand[name] || mutationCommand[name] || blockedCommand[name] || isDeleteCommand(name, nil) {
		return true
	}
	if _, ok := mutationSubcommand[name]; ok {
		return true
	}
	return false
}

func safeDeletionTarget(name string, args []string) bool {
	// Non-filesystem deletion targets (Kubernetes, Helm and container tools)
	// are still protected by their explicit subcommand and second approval.
	if name == "kubectl" || name == "oc" || name == "helm" || name == "docker" || name == "podman" || name == "nerdctl" || name == "crictl" || name == "ctr" {
		return true
	}
	protected := map[string]bool{
		"/": true, "/bin": true, "/boot": true, "/dev": true, "/etc": true,
		"/home": true, "/lib": true, "/lib64": true, "/proc": true,
		"/root": true, "/run": true, "/sbin": true, "/sys": true,
		"/usr": true, "/var": true,
	}
	for index, arg := range args {
		if arg == "--" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if name == "find" && index > 0 {
			break
		}
		if strings.ContainsAny(arg, "*?[]{}") {
			return false
		}
		clean := filepath.Clean(arg)
		if protected[clean] {
			return false
		}
		if name != "find" {
			// Check every filesystem target for rm, mv-style and git commands.
			continue
		}
		break
	}
	return true
}

func isMutatingCommand(name string, args []string) bool {
	if isMutatingWord(name) || mutationCommand[name] || isDeleteCommand(name, args) {
		return true
	}
	first := firstNonFlagArg(args)
	return mutationSubcommand[name][first]
}

func allowedMutationCommand(name string, args []string) bool {
	if mutationCommand[name] || isDeleteCommand(name, args) {
		return true
	}
	first := firstNonFlagArg(args)
	return mutationSubcommand[name][first]
}

func isDeleteCommand(name string, args []string) bool {
	first := firstNonFlagArg(args)
	switch name {
	case "rm", "rmdir", "unlink", "shred":
		return true
	case "find":
		for index, arg := range args {
			if arg == "-delete" || arg == "--delete" {
				return true
			}
			if (arg == "-exec" || arg == "-execdir") && index+1 < len(args) && filepath.Base(args[index+1]) == "rm" {
				return true
			}
		}
	case "git":
		return first == "rm"
	case "kubectl", "oc":
		return first == "delete"
	case "helm":
		return first == "delete" || first == "uninstall"
	case "docker", "podman", "nerdctl", "crictl", "ctr":
		if first == "rm" || first == "rmi" {
			return true
		}
		for index, arg := range args {
			if (arg == "container" || arg == "image" || arg == "volume" || arg == "network") && index+1 < len(args) && (args[index+1] == "rm" || args[index+1] == "remove" || args[index+1] == "prune") {
				return true
			}
		}
	case "apt", "apt-get", "yum", "dnf", "zypper", "apk":
		return first == "remove" || first == "purge" || first == "autoremove" || first == "erase"
	}
	return false
}

func firstNonFlagArg(args []string) string {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return strings.ToLower(arg)
		}
	}
	return ""
}

func validSubcommand(name string, args []string) bool {
	for _, arg := range args {
		if ((arg == "-i" || strings.HasPrefix(arg, "-i=")) && name != "df") || arg == "--exec" || arg == "--delete" || arg == "-delete" {
			return false
		}
	}
	first := firstNonFlagArg(args)
	switch name {
	case "systemctl":
		if first == "status" || first == "is-active" || first == "is-enabled" || first == "show" {
			return true
		}
		for _, arg := range args {
			if arg == "--failed" {
				return first == ""
			}
		}
		return false
	case "kubectl":
		return first == "get" || first == "describe" || first == "logs" || first == "top" || first == "version" || first == "explain"
	case "helm":
		return first == "list" || first == "status" || first == "get" || first == "history"
	case "docker":
		return first == "ps" || first == "images" || first == "inspect" || first == "version" || first == "info"
	case "git":
		return first == "status" || first == "log" || first == "diff" || first == "show" || first == "branch"
	case "find":
		for _, arg := range args {
			if arg == "-exec" || arg == "-execdir" || arg == "-delete" || arg == "-ok" || arg == "-okdir" {
				return false
			}
		}
	case "journalctl":
		for _, arg := range args {
			if arg == "--vacuum-time" || arg == "--vacuum-size" {
				return false
			}
		}
	case "env":
		for _, arg := range args {
			if strings.HasPrefix(arg, "-") || strings.Contains(arg, "=") {
				continue
			}
			return false
		}
	}
	return true
}

func isMutatingWord(word string) bool {
	for _, fragment := range []string{"rm", "rmdir", "unlink", "reboot", "shutdown", "poweroff", "halt", "mkfs", "dd", "chmod", "chown", "usermod", "groupmod", "passwd", "visudo", "install", "remove", "erase", "purge", "upgrade", "restart", "reload", "disable", "enable", "delete", "apply", "edit", "patch", "replace", "scale", "cordon", "drain", "uninstall", "rollback", "drop", "truncate", "alter", "insert", "update", "exec", "run", "kill", "tee", "xargs"} {
		if word == fragment || strings.HasPrefix(word, fragment+"=") {
			return true
		}
	}
	return false
}

func splitShellSegments(tokens []string) [][]string {
	segments := make([][]string, 1)
	for _, token := range tokens {
		if token == "|" || token == ";" {
			segments = append(segments, nil)
			continue
		}
		segments[len(segments)-1] = append(segments[len(segments)-1], token)
	}
	return segments
}

func parseShellAST(command string) (*shellAST, bool, error) {
	tokens, unsafe, err := shellTokens(command)
	if err != nil {
		return nil, unsafe, err
	}
	segments := splitShellSegments(tokens)
	ast := &shellAST{Pipeline: make([]shellCommand, 0, len(segments))}
	for _, segment := range segments {
		if len(segment) == 0 {
			return nil, unsafe, &syntaxError{"命令分隔符两侧必须是完整命令"}
		}
		ast.Pipeline = append(ast.Pipeline, shellCommand{Name: segment[0], Args: append([]string(nil), segment[1:]...)})
	}
	return ast, unsafe, nil
}

func shellTokens(command string) ([]string, bool, error) {
	var tokens []string
	var current strings.Builder
	quoted := rune(0)
	escaped := false
	unsafe := false
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}
	for _, char := range command {
		if escaped {
			current.WriteRune(char)
			escaped = false
			continue
		}
		if quoted != 0 {
			if char == quoted {
				quoted = 0
			} else {
				current.WriteRune(char)
			}
			continue
		}
		switch char {
		case '\\':
			escaped = true
		case '\'', '"':
			quoted = char
		case ' ', '\t', '\r':
			flush()
		case '|':
			flush()
			tokens = append(tokens, "|")
		case ';':
			flush()
			tokens = append(tokens, ";")
		case '&', '<', '>', '`', '\n', '$':
			unsafe = true
			current.WriteRune(char)
		default:
			current.WriteRune(char)
		}
	}
	if escaped || quoted != 0 {
		return nil, unsafe, &syntaxError{"未闭合的引号或转义符"}
	}
	flush()
	return tokens, unsafe, nil
}

type syntaxError struct{ message string }

func (e *syntaxError) Error() string { return e.message }

func blocked(reason, risk string) PolicyDecision {
	return PolicyDecision{Allowed: false, Reason: "命令被 GoSSH 安全策略拦截: " + reason, Risk: risk, Mutating: risk == "mutation"}
}

func IsBlockedCommand(command string) bool { return !AssessCommand(command).Allowed }
