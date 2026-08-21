// owner: muswood | Email: mumu920@outlook.com
package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type ToolSet struct {
	Terminal           func(context.Context, string, string) ToolResult
	TerminalStream     func(context.Context, string, string, func(string)) ToolResult
	TerminalCancel     func(context.Context, string) error
	SSHSystemProbeDone func(string) bool
	MarkSSHSystemProbe func(string)
	SFTPListDir        func(context.Context, string, string) ToolResult
	SFTPReadFile       func(context.Context, string, string) ToolResult
	SFTPReadFileRange  func(context.Context, string, string, int, int) ToolResult
	RAGSearch          func(context.Context, string, int) ToolResult
	RAGSearchTarget    func(context.Context, string, string, int) ToolResult
	Diagnostics        func(context.Context) ToolResult
	LocalGoSSHConfig   func(context.Context) ToolResult
	LocalSessionLog    func(context.Context, string, string, int64, int) ToolResult
	LocalDocumentRead  func(context.Context, string, int64, int) ToolResult
	WebSearch          func(context.Context, string, int) ToolResult
	WebRead            func(context.Context, string, int) ToolResult
	MCP                []tool.BaseTool
}

type mcpApprovalTool struct {
	runtime *Runtime
	state   *taskState
	tool    tool.InvokableTool
	info    *schema.ToolInfo
}

// workflowGateTool makes declarative Skill workflow steps durable runtime
// constraints rather than prompt-only guidance.
type workflowGateTool struct {
	runtime *Runtime
	state   *taskState
	tool    tool.InvokableTool
	info    *schema.ToolInfo
}

func (t *workflowGateTool) Info(context.Context) (*schema.ToolInfo, error) { return t.info, nil }

func (t *workflowGateTool) InvokableRun(ctx context.Context, argumentsInJSON string, options ...tool.Option) (string, error) {
	if err := t.runtime.workflowBefore(t.state, t.info.Name); err != nil {
		return "", err
	}
	output, err := t.tool.InvokableRun(ctx, argumentsInJSON, options...)
	if err != nil {
		// Eino's ToolNode treats any returned error as a node failure. A
		// truncated model tool-call is recoverable: return it as a tool result
		// so the model gets a chance to retry with complete JSON.
		if isMalformedToolArgumentsError(err) {
			return invalidToolArgumentsResult(t.info.Name, false, err)
		}
		t.runtime.workflowFailure(t.state)
		return output, err
	}
	var result ToolResult
	if json.Unmarshal([]byte(output), &result) == nil && result.Error != "" {
		if !(result.ErrorKind == "validation" && isMalformedToolArgumentsError(errors.New(result.Error))) {
			t.runtime.workflowFailure(t.state)
		}
		return output, nil
	}
	t.runtime.workflowAfter(t.state, t.info.Name)
	return output, nil
}

// malformedArgumentsSafeTool protects tools that are implemented outside this
// package (for example MCP tools). Eino passes their raw arguments through to
// InvokableRun, so a tool that unmarshals directly must not turn a truncated
// model payload into a fatal ToolNode error.
type malformedArgumentsSafeTool struct {
	tool tool.InvokableTool
	info *schema.ToolInfo
}

func (t *malformedArgumentsSafeTool) Info(context.Context) (*schema.ToolInfo, error) {
	return t.info, nil
}

func (t *malformedArgumentsSafeTool) InvokableRun(ctx context.Context, argumentsInJSON string, options ...tool.Option) (string, error) {
	if isTruncatedToolArguments(argumentsInJSON) {
		return invalidToolArgumentsResult(t.info.Name, false, errors.New("unexpected end of JSON input"))
	}
	output, err := t.tool.InvokableRun(ctx, argumentsInJSON, options...)
	if err == nil || !isMalformedToolArgumentsError(err) {
		return output, err
	}
	return invalidToolArgumentsResult(t.info.Name, false, err)
}

func isTruncatedToolArguments(input string) bool {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" || trimmed[0] != '{' {
		return false
	}
	var value any
	err := json.Unmarshal([]byte(trimmed), &value)
	if err == nil {
		return false
	}
	return isMalformedToolArgumentsError(err)
}

func (t *mcpApprovalTool) Info(context.Context) (*schema.ToolInfo, error) {
	return t.info, nil
}

func (t *mcpApprovalTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	arguments := map[string]any{"arguments": json.RawMessage(argumentsInJSON)}
	step := newStep(t.state, t.info.Name, arguments, "调用外部 MCP 工具", "高风险：外部进程可能访问网络、文件或执行系统操作")
	if cached, ok := t.runtime.cachedStepResult(step); ok {
		return marshalToolResult(*cached)
	}
	t.runtime.saveStep(step)
	step.Status = StepWaitingApproval
	step.UpdatedAt = time.Now()
	t.runtime.saveStep(step)
	allowed, err := t.runtime.requestApproval(t.state, Approval{
		TaskID: t.state.task.ID, StepID: step.ID, ToolName: t.info.Name,
		Command: t.info.Name, Purpose: "调用外部 MCP 工具", Risk: "高风险：外部 MCP 工具的副作用由其 server 决定",
	})
	if err != nil {
		return "", err
	}
	step.Approved = &allowed
	if !allowed {
		step.Status = StepRejected
		step.UpdatedAt = time.Now()
		t.runtime.saveStep(step)
		return marshalToolResult(ToolResult{ToolName: t.info.Name, ExitCode: -1,
			Error: "用户拒绝执行 MCP 工具", ErrorKind: "approval", Status: "rejected", Redacted: true})
	}
	step.Status = StepApproved
	step.UpdatedAt = time.Now()
	t.runtime.saveStep(step)
	step = t.runtime.startStep(step)
	result := t.runtime.executeStep(ctx, step, func(runCtx context.Context) ToolResult {
		output, callErr := t.tool.InvokableRun(runCtx, argumentsInJSON)
		toolResult := ToolResult{ToolName: t.info.Name, Output: output, ExitCode: 0, Redacted: true, Status: "ok"}
		if callErr != nil {
			toolResult.ExitCode = -1
			toolResult.Error = callErr.Error()
			toolResult.ErrorKind = "mcp"
			toolResult.Status = "error"
		}
		return toolResult
	})
	t.runtime.finishStep(step, result)
	return marshalToolResult(result)
}

func targetSession(state *taskState, fallback, targetID string) (string, error) {
	if targetID == "" {
		if len(state.task.Targets) == 1 && state.task.Targets[0].SessionID != "" {
			return state.task.Targets[0].SessionID, nil
		}
		if fallback != "" {
			return fallback, nil
		}
		return "", errors.New("Agent 没有可用的 SSH 目标会话")
	}
	for _, target := range state.task.Targets {
		if target.ID == targetID && target.SessionID != "" {
			return target.SessionID, nil
		}
	}
	// 单终端任务没有 Targets，模型可能从旧上下文复用了失效的 targetId。
	if len(state.task.Targets) == 0 && fallback != "" {
		return fallback, nil
	}
	// 单目标任务只有一个有效会话，允许兼容旧的或过期的目标 ID。
	if len(state.task.Targets) == 1 && state.task.Targets[0].SessionID != "" {
		return state.task.Targets[0].SessionID, nil
	}
	return "", fmt.Errorf("Agent 目标不存在或未连接: %s", targetID)
}

func effectiveTargetID(state *taskState, targetID string) string {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		if len(state.task.Targets) == 1 {
			return state.task.Targets[0].ID
		}
		return ""
	}
	for _, target := range state.task.Targets {
		if target.ID == targetID {
			return targetID
		}
	}
	if len(state.task.Targets) == 0 || len(state.task.Targets) == 1 {
		return ""
	}
	return targetID
}

// resolveTargetText applies {{parameter}} placeholders from global Skill
// parameters and then target-specific values, with the target taking priority.
func resolveTargetText(state *taskState, targetID, value string) string {
	for key, candidate := range state.task.SkillParameters {
		value = strings.ReplaceAll(value, "{{"+key+"}}", fmt.Sprint(candidate))
	}
	for key, candidate := range state.task.TargetParameters[targetID] {
		value = strings.ReplaceAll(value, "{{"+key+"}}", fmt.Sprint(candidate))
	}
	return value
}

func newStep(state *taskState, toolName string, arguments map[string]any, purpose, risk string) Step {
	now := time.Now()
	raw, _ := json.Marshal(arguments)
	digest := sha256.Sum256(append([]byte(state.task.ID+":"+toolName+":"), raw...))
	timeoutMillis := int64(60_000)
	if toolName == "terminal_command" || toolName == "terminal_command_many" {
		// ponytail: fixed 30-minute ceiling; user can stop earlier with Ctrl-C in the terminal.
		timeoutMillis = 30 * 60 * 1000
	}
	return Step{
		ID: fmt.Sprintf("step-%x", digest[:12]), TaskID: state.task.ID,
		Kind: "tool", ToolName: toolName, Arguments: arguments, Purpose: purpose, Risk: risk,
		Status: StepCreated, CreatedAt: now, UpdatedAt: now,
		IdempotencyKey: hex.EncodeToString(digest[:]), TimeoutMillis: timeoutMillis,
	}
}

func requireSSHSystemProbe(state *taskState, sessionID, command string, probeDone func(string) bool) error {
	if state == nil || state.task.Transport != "ssh" {
		return nil
	}
	if sessionID != "" && probeDone != nil && probeDone(sessionID) {
		return nil
	}
	for _, step := range state.task.Steps {
		if step.Status != StepCompleted || step.Result == nil || step.Result.Error != "" {
			continue
		}
		if step.ToolName != "terminal_command" && step.ToolName != "terminal_command_many" {
			continue
		}
		if probe, ok := step.Arguments["command"].(string); ok && strings.TrimSpace(probe) == "uname -a" {
			return nil
		}
	}
	if strings.TrimSpace(command) != "uname -a" {
		return errors.New("SSH Agent 必须先执行并完成 uname -a，以识别远端操作系统后才能执行其他终端命令")
	}
	return nil
}

func marshalToolResult(result ToolResult) (string, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// decodeToolArguments distinguishes a syntactically invalid model call from a
// harmlessly truncated JSON envelope. Repaired arguments must never be used to
// execute a tool: callers return a retryable ToolResult instead.
func decodeToolArguments(input string, destination any) (bool, error) {
	if err := json.Unmarshal([]byte(input), destination); err == nil {
		return false, nil
	} else if !strings.Contains(err.Error(), "unexpected end of JSON input") {
		return false, err
	}
	repaired, ok := repairTruncatedJSONObject(input)
	if !ok {
		return false, json.Unmarshal([]byte(input), destination)
	}
	if err := json.Unmarshal(repaired, destination); err != nil {
		return false, err
	}
	return true, nil
}

func invalidToolArgumentsResult(toolName string, repaired bool, err error) (string, error) {
	message := "工具参数 JSON 无效，GoSSH 未执行该操作，请使用完整 JSON 后重试"
	if repaired {
		message = "工具参数在响应末尾被截断；GoSSH 未执行该操作，请使用完整 JSON 重试"
	}
	if err != nil {
		message += ": " + err.Error()
	}
	return marshalToolResult(ToolResult{ToolName: toolName, ExitCode: -1, Error: message,
		ErrorKind: "validation", Status: "error", Redacted: true})
}

// plainCommandArgument accepts the two unstructured forms that can be safely
// interpreted as a terminal command. A payload that starts like a JSON object
// or array is never treated as a command when it is malformed; that is almost
// certainly a truncated structured tool call.
func plainCommandArgument(input string) (string, bool) {
	text := strings.TrimSpace(input)
	if text == "" || strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") {
		return "", false
	}
	if strings.HasPrefix(text, `"`) {
		var command string
		if err := json.Unmarshal([]byte(text), &command); err != nil || strings.TrimSpace(command) == "" {
			return "", false
		}
		return command, true
	}
	if json.Valid([]byte(text)) {
		return "", false
	}
	return text, true
}

func invokeReadOnlyTool(ctx context.Context, maxAttempts int, invoke func() ToolResult) ToolResult {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	var result ToolResult
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if ctx.Err() != nil {
			result.Error = ctx.Err().Error()
			result.ExitCode = -1
			result.Attempts = attempt - 1
			result.Status = "error"
			result.ErrorKind = "cancelled"
			result.Cancelled = true
			return result
		}
		result = invoke()
		result.Attempts = attempt
		if result.Error == "" {
			if result.Status == "" {
				result.Status = "ok"
			}
			return result
		}
		result.Status = "error"
		if result.ErrorKind == "" {
			result.ErrorKind = "execution"
		}
	}
	return result
}

func (r *Runtime) executeTerminal(ctx context.Context, sessionID, command, toolName string) ToolResult {
	r.mu.RLock()
	terminal := r.tools.Terminal
	stream := r.tools.TerminalStream
	r.mu.RUnlock()
	if terminal == nil && stream == nil {
		return ToolResult{ToolName: toolName, Command: command, ExitCode: -1,
			Error: "当前 Agent 没有关联可执行的终端会话", ErrorKind: "configuration", Status: "error", Redacted: true}
	}
	var result ToolResult
	if stream != nil {
		result = stream(ctx, sessionID, command, nil)
	} else {
		result = terminal(ctx, sessionID, command)
	}
	if result.ToolName == "" {
		result.ToolName = toolName
	}
	if result.Command == "" {
		result.Command = command
	}
	if result.Status == "" {
		result.Status = "ok"
	}
	return result
}

func (r *Runtime) startStep(step Step) Step {
	if step.TimeoutMillis <= 0 {
		step.TimeoutMillis = 60_000
	}
	step.Status = StepExecuting
	step.Attempt++
	step.StartedAt = time.Now()
	step.UpdatedAt = step.StartedAt
	step.HeartbeatAt = step.StartedAt
	step.LeaseOwner = r.ownerID
	step.LeaseUntil = step.StartedAt.Add(2 * time.Minute)
	r.saveStep(step)
	r.publish(step.TaskID, step.ID, EventToolStarted, step)
	return step
}

func (r *Runtime) finishStep(step Step, result ToolResult) {
	now := time.Now()
	step.FinishedAt = now
	step.UpdatedAt = now
	step.LeaseOwner = ""
	step.LeaseUntil = time.Time{}
	step.HeartbeatAt = now
	step.Result = &result
	if result.Error != "" {
		step.Status = StepFailed
		if result.Status == "" {
			result.Status = "error"
		}
	} else if result.Status == "submitted" {
		step.Status = StepSubmitted
	} else {
		step.Status = StepCompleted
		if result.Status == "" {
			result.Status = "ok"
		}
	}
	r.saveStep(step)
	r.publish(step.TaskID, step.ID, EventToolFinished, result)
}

func (r *Runtime) heartbeatStep(step Step) {
	now := time.Now()
	step.HeartbeatAt = now
	step.LeaseUntil = now.Add(2 * time.Minute)
	step.UpdatedAt = now
	r.saveStep(step)
}

func (r *Runtime) cachedStepResult(step Step) (*ToolResult, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state := r.tasks[step.TaskID]
	if state == nil {
		return nil, false
	}
	for _, existing := range state.task.Steps {
		if existing.IdempotencyKey != step.IdempotencyKey {
			continue
		}
		if existing.Status == StepCompleted && existing.Result != nil {
			result := *existing.Result
			return &result, true
		}
		if existing.Status == StepExecuting && existing.LeaseUntil.After(time.Now()) {
			return &ToolResult{ToolName: step.ToolName, ExitCode: -1, Error: "相同幂等键的工具步骤正在执行", ErrorKind: "in_progress", Status: "error"}, true
		}
	}
	return nil, false
}

func (r *Runtime) executeStep(ctx context.Context, step Step, invoke func(context.Context) ToolResult) ToolResult {
	r.mu.RLock()
	tracer := r.tracer
	r.mu.RUnlock()
	traceCtx, finishTrace := tracer.Start(ctx, "agent.tool", map[string]string{
		"agent.task.id":   step.TaskID,
		"agent.step.id":   step.ID,
		"agent.tool.name": step.ToolName,
	})
	stepCtx := ctx
	var cancel context.CancelFunc
	if step.TimeoutMillis > 0 {
		stepCtx, cancel = context.WithTimeout(traceCtx, time.Duration(step.TimeoutMillis)*time.Millisecond)
		defer cancel()
	} else {
		stepCtx = traceCtx
	}
	resultCh := make(chan ToolResult, 1)
	go func() { resultCh <- invoke(stepCtx) }()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case result := <-resultCh:
			if result.Attempts == 0 {
				result.Attempts = 1
			}
			if result.Error != "" {
				finishTrace(errors.New(result.Error))
			} else {
				finishTrace(nil)
			}
			return result
		case <-ticker.C:
			r.heartbeatStep(step)
		case <-stepCtx.Done():
			// Most GoSSH transports honor ctx and return immediately. A third-party
			// callback may not, so wait briefly before reporting an uncertain stop
			// instead of claiming that a potentially mutating operation ended.
			select {
			case result := <-resultCh:
				if result.Attempts == 0 {
					result.Attempts = step.Attempt
				}
				if result.Error != "" {
					finishTrace(errors.New(result.Error))
				} else {
					finishTrace(nil)
				}
				return result
			case <-time.After(2 * time.Second):
			}
			result := ToolResult{ToolName: step.ToolName, ExitCode: -1, Attempts: step.Attempt,
				Error: "工具执行超时，已发送取消信号但未确认远端操作停止；请人工核验远端状态", ErrorKind: "execution_uncertain", Cancelled: true}
			if errors.Is(stepCtx.Err(), context.DeadlineExceeded) {
				result.TimedOut = true
			}
			finishTrace(stepCtx.Err())
			return result
		}
	}
}

type terminalCommandTool struct {
	runtime   *Runtime
	state     *taskState
	sessionID string
}

const maxTargetConcurrency = 4

type multiTargetCommandTool struct {
	runtime   *Runtime
	state     *taskState
	sessionID string
}

type multiTargetReadTool struct {
	runtime   *Runtime
	state     *taskState
	sessionID string
	kind      string
}

func (t *multiTargetReadTool) Info(context.Context) (*schema.ToolInfo, error) {
	params := map[string]*schema.ParameterInfo{
		"targetIds": {Type: schema.Array, Desc: "目标 ID 数组；为空时使用任务全部目标", Required: false},
	}
	switch t.kind {
	case "sftp_list_dir_many", "sftp_read_file_many":
		params["path"] = &schema.ParameterInfo{Type: schema.String, Desc: "所有目标上的远端路径", Required: true}
	case "rag_search_many":
		params["query"] = &schema.ParameterInfo{Type: schema.String, Desc: "检索问题", Required: true}
		params["limit"] = &schema.ParameterInfo{Type: schema.Integer, Desc: "每个目标返回的结果数", Required: false}
	case "gossh_diagnostics_many":
		// Diagnostics are local to GoSSH; remote shell diagnostics must use the
		// approved terminal_command_many tool instead.
	}
	desc := map[string]string{
		"sftp_list_dir_many":     "在多个 SSH 目标列出同一路径的远端目录，结果按 targetId 隔离。",
		"sftp_read_file_many":    "在多个 SSH 目标读取同一路径的远端文件，结果按 targetId 隔离。",
		"rag_search_many":        "为多个目标执行同一知识库检索，结果按 targetId 分组返回。",
		"gossh_diagnostics_many": "读取 GoSSH 本地诊断信息并按目标 ID 分组；不会向远端终端发送命令。",
	}[t.kind]
	return &schema.ToolInfo{Name: t.kind, Desc: desc, ParamsOneOf: schema.NewParamsOneOfByParams(params)}, nil
}

func (t *multiTargetReadTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Path      string   `json:"path"`
		Query     string   `json:"query"`
		Limit     int      `json:"limit"`
		TargetIDs []string `json:"targetIds"`
	}
	repaired, err := decodeToolArguments(argumentsInJSON, &args)
	if err != nil || repaired {
		return invalidToolArgumentsResult(t.kind, repaired, err)
	}
	targets, err := selectedTargets(t.state, args.TargetIDs)
	if err != nil {
		return "", err
	}
	purpose, risk := "执行多目标只读工具", "只读"
	if t.kind == "sftp_list_dir_many" || t.kind == "sftp_read_file_many" {
		purpose, risk = "访问多个远端主机的 SFTP 数据", "远端数据访问可能暴露敏感文件内容"
	}
	step := newStep(t.state, t.kind, map[string]any{"path": args.Path, "query": args.Query, "limit": args.Limit, "targetIds": args.TargetIDs}, purpose, risk)
	if cached, ok := t.runtime.cachedStepResult(step); ok {
		return marshalToolResult(*cached)
	}
	if t.kind == "sftp_list_dir_many" || t.kind == "sftp_read_file_many" {
		t.runtime.saveStep(step)
		step.Status = StepWaitingApproval
		step.UpdatedAt = time.Now()
		t.runtime.saveStep(step)
		allowed, approvalErr := t.runtime.requestApproval(t.state, Approval{TaskID: t.state.task.ID, StepID: step.ID,
			ToolName: t.kind, Command: "SFTP " + args.Path, Purpose: purpose, Risk: risk, ApprovalLevel: 1})
		if approvalErr != nil {
			return "", approvalErr
		}
		step.Approved = &allowed
		if !allowed {
			step.Status = StepRejected
			step.UpdatedAt = time.Now()
			t.runtime.saveStep(step)
			return marshalToolResult(ToolResult{ToolName: t.kind, ExitCode: -1, Error: "用户拒绝访问远端 SFTP 数据", ErrorKind: "approval", Status: "rejected", Redacted: true})
		}
		step.Status = StepApproved
		step.UpdatedAt = time.Now()
		t.runtime.saveStep(step)
	}
	step = t.runtime.startStep(step)
	result := t.runtime.executeStep(ctx, step, func(runCtx context.Context) ToolResult {
		return t.execute(runCtx, args.Path, args.Query, args.Limit, targets)
	})
	result.ToolName = t.kind
	t.runtime.finishStep(step, result)
	return marshalToolResult(result)
}

func (t *multiTargetReadTool) execute(ctx context.Context, path, query string, limit int, targets []Target) ToolResult {
	started := time.Now()
	results := make([]ToolResult, len(targets))
	sem := make(chan struct{}, maxTargetConcurrency)
	var wg sync.WaitGroup
	for i, target := range targets {
		i, target := i, target
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results[i] = ToolResult{ToolName: t.kind, TargetID: target.ID, ExitCode: -1, Error: ctx.Err().Error(), ErrorKind: "cancelled", Status: "error", Cancelled: true}
				return
			}
			defer func() { <-sem }()
			var result ToolResult
			targetPath := resolveTargetText(t.state, target.ID, path)
			targetQuery := resolveTargetText(t.state, target.ID, query)
			switch t.kind {
			case "sftp_list_dir_many":
				result = invokeReadOnlyTool(ctx, 2, func() ToolResult { return t.runtime.tools.SFTPListDir(ctx, target.SessionID, targetPath) })
			case "sftp_read_file_many":
				result = invokeReadOnlyTool(ctx, 2, func() ToolResult { return t.runtime.tools.SFTPReadFile(ctx, target.SessionID, targetPath) })
			case "rag_search_many":
				if t.runtime.tools.RAGSearchTarget != nil {
					result = invokeReadOnlyTool(ctx, 2, func() ToolResult { return t.runtime.tools.RAGSearchTarget(ctx, target.ID, targetQuery, limit) })
				} else if t.runtime.tools.RAGSearch != nil {
					result = invokeReadOnlyTool(ctx, 2, func() ToolResult { return t.runtime.tools.RAGSearch(ctx, targetQuery, limit) })
				} else {
					result = ToolResult{ToolName: "rag_search", ExitCode: -1, Error: "RAG 未配置目标级检索", ErrorKind: "configuration", Status: "error", Redacted: true}
				}
			case "gossh_diagnostics_many":
				if t.runtime.tools.Diagnostics != nil {
					result = invokeReadOnlyTool(ctx, 2, func() ToolResult { return t.runtime.tools.Diagnostics(ctx) })
				} else {
					result = ToolResult{ToolName: "gossh_diagnostics", ExitCode: -1, Error: "Diagnostics 未配置", ErrorKind: "configuration", Status: "error", Redacted: true}
				}
			}
			result.TargetID = target.ID
			if result.ToolName == "" {
				result.ToolName = t.kind
			}
			results[i] = result
		}()
	}
	wg.Wait()
	failed := 0
	for _, result := range results {
		if result.Error != "" || result.ExitCode != 0 {
			failed++
		}
	}
	status := "ok"
	if failed == len(results) {
		status = "error"
	} else if failed > 0 {
		status = "partial"
	}
	raw, _ := json.Marshal(results)
	result := ToolResult{ToolName: t.kind, Output: string(raw), ExitCode: 0, DurationMillis: time.Since(started).Milliseconds(), Status: status, Redacted: true,
		Metadata: map[string]any{"targetCount": len(results), "failedTargets": failed, "maxConcurrency": maxTargetConcurrency}}
	if failed > 0 {
		result.Error = fmt.Sprintf("%d/%d 个目标执行失败", failed, len(results))
		result.ErrorKind = "partial"
	}
	if len(results) == 0 {
		result.ExitCode = -1
		result.Error = "没有可用的 Agent 目标"
		result.ErrorKind = "configuration"
		result.Status = "error"
	}
	return result
}

func (t *multiTargetCommandTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "terminal_command_many",
		Desc: "在多个 SSH 目标并发执行同一条只读命令。每个目标独立返回 Tool Result，最多同时执行 4 个目标。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"command":   {Type: schema.String, Desc: "单条只读 shell 命令", Required: true},
			"purpose":   {Type: schema.String, Desc: "为什么需要这条命令", Required: true},
			"risk":      {Type: schema.String, Desc: "命令风险说明", Required: true},
			"targetIds": {Type: schema.Array, Desc: "目标 ID 数组；为空时使用任务全部目标", Required: false},
		}),
	}, nil
}

func (t *multiTargetCommandTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Command   string   `json:"command"`
		Purpose   string   `json:"purpose"`
		Risk      string   `json:"risk"`
		TargetIDs []string `json:"targetIds"`
	}
	repaired, err := decodeToolArguments(argumentsInJSON, &args)
	if err != nil || repaired {
		return invalidToolArgumentsResult("terminal_command_many", repaired, err)
	}
	args.Command = strings.TrimSpace(args.Command)
	if strings.Contains(args.Command, "{{") || strings.Contains(args.Command, "}}") {
		return marshalToolResult(ToolResult{ToolName: "terminal_command_many", Command: args.Command, ExitCode: -1,
			Error: "多目标终端命令不能包含参数模板；请为每个目标分别提交并审批实际命令", ErrorKind: "policy", Status: "rejected", Redacted: true})
	}
	decision := t.runtime.assessCommandWithAI(ctx, t.state, args.Command, t.state.task.AllowMutations)
	if !decision.Allowed {
		return marshalToolResult(ToolResult{ToolName: "terminal_command_many", Command: args.Command, ExitCode: -1,
			Error: decision.Reason, ErrorKind: "policy", Status: "rejected", Redacted: true,
			Metadata: map[string]any{"risk": decision.Risk}})
	}
	if t.runtime.tools.Terminal == nil && t.runtime.tools.TerminalStream == nil {
		return marshalToolResult(ToolResult{ToolName: "terminal_command_many", Command: args.Command, ExitCode: -1,
			Error: "当前 Agent 没有关联可执行的终端会话", ErrorKind: "configuration", Status: "error", Redacted: true})
	}
	targets, err := selectedTargets(t.state, args.TargetIDs)
	if err != nil {
		return "", err
	}
	for _, target := range targets {
		if err := requireSSHSystemProbe(t.state, target.SessionID, args.Command, t.runtime.tools.SSHSystemProbeDone); err != nil {
			return marshalToolResult(ToolResult{ToolName: "terminal_command_many", Command: args.Command, ExitCode: -1, Error: err.Error(), ErrorKind: "prerequisite", Status: "rejected", Redacted: true})
		}
	}
	canonicalIDs := make([]string, 0, len(targets))
	for _, target := range targets {
		canonicalIDs = append(canonicalIDs, target.ID)
	}
	sort.Strings(canonicalIDs)
	step := newStep(t.state, "terminal_command_many", map[string]any{"command": args.Command, "targetIds": canonicalIDs}, args.Purpose, args.Risk)
	if cached, ok := t.runtime.cachedStepResult(step); ok {
		return marshalToolResult(*cached)
	}
	t.runtime.saveStep(step)
	step.Status = StepWaitingApproval
	step.UpdatedAt = time.Now()
	t.runtime.saveStep(step)
	allowed, err := t.runtime.requestApproval(t.state, Approval{TaskID: t.state.task.ID, StepID: step.ID,
		ToolName: "terminal_command_many", Command: args.Command, Purpose: args.Purpose, Risk: args.Risk, ApprovalLevel: 1})
	if err != nil {
		return "", err
	}
	if allowed && decision.Deleting {
		allowed, err = t.runtime.requestApproval(t.state, Approval{TaskID: t.state.task.ID, StepID: step.ID,
			ToolName: "terminal_command_many", Command: args.Command,
			Purpose: "删除操作二次确认：请再次核对命令、目标和路径，确认删除不可逆。\n" + args.Purpose,
			Risk:    "高风险：该命令将删除数据，必须再次明确确认", ApprovalLevel: 2})
		if err != nil {
			return "", err
		}
	}
	step.Approved = &allowed
	if !allowed {
		step.Status = StepRejected
		step.UpdatedAt = time.Now()
		t.runtime.saveStep(step)
		return marshalToolResult(ToolResult{ToolName: "terminal_command_many", Command: args.Command, ExitCode: -1,
			Error: "用户拒绝执行该多目标命令", ErrorKind: "approval", Status: "rejected", Redacted: true})
	}
	step.Status = StepApproved
	step.UpdatedAt = time.Now()
	t.runtime.saveStep(step)
	step = t.runtime.startStep(step)
	result := t.runtime.executeStep(ctx, step, func(runCtx context.Context) ToolResult {
		return t.executeTargets(runCtx, step, args.Command, targets)
	})
	if result.ToolName == "" {
		result.ToolName = "terminal_command_many"
	}
	result.Command = args.Command
	t.runtime.finishStep(step, result)
	return marshalToolResult(result)
}

func selectedTargets(state *taskState, ids []string) ([]Target, error) {
	if len(state.task.Targets) == 0 {
		if state.task.SessionID == "" {
			return nil, errors.New("Agent 没有可用的多目标 SSH 会话")
		}
		return []Target{{ID: state.task.SessionID, SessionID: state.task.SessionID}}, nil
	}
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	targets := make([]Target, 0, len(state.task.Targets))
	for _, target := range state.task.Targets {
		if len(ids) == 0 || wanted[target.ID] {
			if target.ID == "" || target.SessionID == "" {
				continue
			}
			targets = append(targets, target)
		}
	}
	if len(targets) == 0 {
		return nil, errors.New("没有匹配的已连接 Agent 目标")
	}
	return targets, nil
}

func (t *multiTargetCommandTool) executeTargets(ctx context.Context, step Step, command string, targets []Target) ToolResult {
	started := time.Now()
	results := make([]ToolResult, len(targets))
	sem := make(chan struct{}, maxTargetConcurrency)
	var wg sync.WaitGroup
	for i, target := range targets {
		i, target := i, target
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results[i] = ToolResult{ToolName: "terminal_command", Command: command, TargetID: target.ID, ExitCode: -1,
					Error: ctx.Err().Error(), ErrorKind: "cancelled", Status: "error", Cancelled: true}
				return
			}
			defer func() { <-sem }()
			targetCommand := resolveTargetText(t.state, target.ID, command)
			var result ToolResult
			if t.runtime.tools.TerminalStream != nil {
				result = t.runtime.tools.TerminalStream(ctx, target.SessionID, targetCommand, func(chunk string) {
					t.runtime.publish(step.TaskID, step.ID, EventToolOutput, map[string]any{"stream": true, "targetId": target.ID, "output": chunk})
				})
			} else {
				result = t.runtime.tools.Terminal(ctx, target.SessionID, targetCommand)
			}
			if result.ToolName == "" {
				result.ToolName = "terminal_command"
			}
			result.TargetID = target.ID
			result.Command = targetCommand
			if result.Status == "ok" && strings.TrimSpace(targetCommand) == "uname -a" && t.runtime.tools.MarkSSHSystemProbe != nil {
				t.runtime.tools.MarkSSHSystemProbe(target.SessionID)
			}
			results[i] = result
		}()
	}
	wg.Wait()
	failed := 0
	for _, result := range results {
		if result.Error != "" {
			failed++
		}
	}
	raw, _ := json.Marshal(results)
	status := "ok"
	resultError := ""
	if failed > 0 {
		status = "partial"
		resultError = fmt.Sprintf("%d/%d 个目标执行失败", failed, len(results))
	}
	return ToolResult{ToolName: "terminal_command_many", Command: command, Output: string(raw), ExitCode: 0,
		DurationMillis: time.Since(started).Milliseconds(), Error: resultError, ErrorKind: "partial", Status: status,
		Redacted: true, Metadata: map[string]any{"targetCount": len(targets), "failedTargets": failed, "maxConcurrency": maxTargetConcurrency}}
}

func (t *terminalCommandTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "terminal_command",
		Desc: "提出一个需要用户审批后才会在当前 SSH 终端执行的命令。每次只提交一个命令。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"command":             {Type: schema.String, Desc: "要执行的单条 shell 命令", Required: true},
			"purpose":             {Type: schema.String, Desc: "为什么需要这条命令", Required: true},
			"risk":                {Type: schema.String, Desc: "命令风险说明", Required: true},
			"targetId":            {Type: schema.String, Desc: "多目标任务中的目标 ID，可选", Required: false},
			"dryRun":              {Type: schema.Boolean, Desc: "只生成变更计划，不执行命令", Required: false},
			"idempotencyKey":      {Type: schema.String, Desc: "写操作必填的稳定幂等键", Required: false},
			"preconditionCommand": {Type: schema.String, Desc: "写操作执行前的只读前置条件检查命令", Required: false},
			"snapshotCommand":     {Type: schema.String, Desc: "写操作执行前保存状态的只读命令", Required: false},
			"verifyCommand":       {Type: schema.String, Desc: "写操作执行后验证结果的只读命令", Required: false},
			"rollbackCommand":     {Type: schema.String, Desc: "主命令或验证失败后执行的回滚命令", Required: false},
		}),
	}, nil
}

func (t *terminalCommandTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Command             string `json:"command"`
		Purpose             string `json:"purpose"`
		Risk                string `json:"risk"`
		TargetID            string `json:"targetId"`
		DryRun              bool   `json:"dryRun"`
		IdempotencyKey      string `json:"idempotencyKey"`
		PreconditionCommand string `json:"preconditionCommand"`
		SnapshotCommand     string `json:"snapshotCommand"`
		VerifyCommand       string `json:"verifyCommand"`
		RollbackCommand     string `json:"rollbackCommand"`
	}
	repaired, err := decodeToolArguments(argumentsInJSON, &args)
	if err != nil {
		if command, ok := plainCommandArgument(argumentsInJSON); ok {
			args.Command = command
			args.Purpose = "模型未提供结构化参数，按命令字符串处理"
			args.Risk = "参数未结构化，仍需按当前安全策略和用户审批执行"
			err = nil
		} else {
			return invalidToolArgumentsResult("terminal_command", repaired, err)
		}
	}
	if repaired {
		return invalidToolArgumentsResult("terminal_command", repaired, err)
	}
	args.TargetID = effectiveTargetID(t.state, args.TargetID)
	args.Command = strings.TrimSpace(resolveTargetText(t.state, args.TargetID, args.Command))
	args.Purpose = resolveTargetText(t.state, args.TargetID, args.Purpose)
	args.Risk = resolveTargetText(t.state, args.TargetID, args.Risk)
	sessionID, err := targetSession(t.state, t.sessionID, args.TargetID)
	if err != nil {
		return "", err
	}
	if err := requireSSHSystemProbe(t.state, sessionID, args.Command, t.runtime.tools.SSHSystemProbeDone); err != nil {
		return marshalToolResult(ToolResult{ToolName: "terminal_command", Command: args.Command, ExitCode: -1, Error: err.Error(), ErrorKind: "prerequisite", Status: "rejected", Redacted: true})
	}
	// A task-level dry-run cannot be bypassed by omitting the tool argument.
	args.DryRun = args.DryRun || t.state.task.DryRun
	decision := t.runtime.assessCommandWithAI(ctx, t.state, args.Command, t.state.task.AllowMutations || args.DryRun)
	if !decision.Allowed {
		return marshalToolResult(ToolResult{ToolName: "terminal_command", Command: args.Command, ExitCode: -1, Error: decision.Reason, ErrorKind: "policy", Status: "rejected", Redacted: true, Metadata: map[string]any{"risk": decision.Risk}})
	}
	if args.DryRun {
		step := newStep(t.state, "terminal_command", map[string]any{"command": args.Command, "targetId": args.TargetID, "dryRun": true, "idempotencyKey": args.IdempotencyKey, "rollbackCommand": args.RollbackCommand}, args.Purpose, args.Risk)
		if args.IdempotencyKey != "" {
			step.IdempotencyKey = args.IdempotencyKey
		}
		result := ToolResult{ToolName: "terminal_command", Command: args.Command, ExitCode: 0, Status: "dry_run", Redacted: true,
			Metadata: map[string]any{"policy": decision, "idempotencyKey": args.IdempotencyKey, "rollbackCommand": args.RollbackCommand}}
		t.runtime.finishStep(step, result)
		return marshalToolResult(result)
	}
	if decision.Mutating && !decision.Administrator && strings.TrimSpace(args.IdempotencyKey) == "" {
		return marshalToolResult(ToolResult{ToolName: "terminal_command", Command: args.Command, ExitCode: -1,
			Error: "写操作必须提供 idempotencyKey；请先 dry-run 或补充幂等键", ErrorKind: "idempotency", Status: "rejected", Redacted: true})
	}
	plan := &MutationPlan{
		PreconditionCommand: strings.TrimSpace(args.PreconditionCommand),
		SnapshotCommand:     strings.TrimSpace(args.SnapshotCommand),
		VerifyCommand:       strings.TrimSpace(args.VerifyCommand),
		RollbackCommand:     strings.TrimSpace(args.RollbackCommand),
	}
	if decision.Mutating && !decision.Administrator {
		if err := validateMutationPlan(plan); err != nil {
			return marshalToolResult(ToolResult{ToolName: "terminal_command", Command: args.Command, ExitCode: -1,
				Error: err.Error(), ErrorKind: "safety_plan", Status: "rejected", Redacted: true})
		}
	}
	if t.runtime.tools.Terminal == nil && t.runtime.tools.TerminalStream == nil {
		return marshalToolResult(ToolResult{ToolName: "terminal_command", Command: args.Command, ExitCode: -1, Error: "当前 Agent 没有关联可执行的终端会话", ErrorKind: "configuration", Status: "error", Redacted: true})
	}
	step := newStep(t.state, "terminal_command", map[string]any{"command": args.Command, "targetId": args.TargetID, "idempotencyKey": args.IdempotencyKey, "mutationPlan": plan}, args.Purpose, args.Risk)
	if decision.Mutating && !decision.Administrator {
		step.MutationPlan = plan
	}
	if args.IdempotencyKey != "" {
		step.IdempotencyKey = args.IdempotencyKey
	}
	if cached, ok := t.runtime.cachedStepResult(step); ok {
		return marshalToolResult(*cached)
	}
	t.runtime.saveStep(step)
	allowed := true
	security := GetSecurityConfig()
	approvalPurpose := mutationApprovalPurpose(args.Purpose, plan, decision.Mutating && !decision.Administrator)
	if decision.Mutating || !security.ReadOnlyNoApproval {
		step.Status = StepWaitingApproval
		step.UpdatedAt = time.Now()
		t.runtime.saveStep(step)
		allowed, err = t.runtime.requestApproval(t.state, Approval{
			TaskID: t.state.task.ID, StepID: step.ID, ToolName: "terminal_command",
			Command: args.Command, Purpose: approvalPurpose, Risk: args.Risk, ApprovalLevel: 1,
		})
		if err != nil {
			return "", err
		}
	}
	if allowed && decision.Deleting {
		allowed, err = t.runtime.requestApproval(t.state, Approval{
			TaskID: t.state.task.ID, StepID: step.ID, ToolName: "terminal_command",
			Command: args.Command,
			Purpose: "删除操作二次确认：请再次核对命令和目标，确认删除不可逆。\n" + approvalPurpose,
			Risk:    "高风险：该命令将删除数据，必须再次明确确认", ApprovalLevel: 2,
		})
		if err != nil {
			return "", err
		}
	}
	step.Approved = &allowed
	if !allowed {
		step.Status = StepRejected
		step.UpdatedAt = time.Now()
		t.runtime.saveStep(step)
		return "用户拒绝执行该命令。请改用不需要这条命令的分析路径，或提出更小范围的只读检查。", nil
	}
	step.Status = StepApproved
	step.UpdatedAt = time.Now()
	t.runtime.saveStep(step)
	step = t.runtime.startStep(step)
	result := ToolResult{ToolName: "terminal_command", Command: args.Command, Status: "error", ExitCode: -1, Redacted: true}
	result = t.runtime.executeStep(ctx, step, func(runCtx context.Context) ToolResult {
		if t.runtime.tools.TerminalStream != nil {
			return t.runtime.tools.TerminalStream(runCtx, sessionID, args.Command, func(chunk string) {
				t.runtime.publish(step.TaskID, step.ID, EventToolOutput, map[string]any{"stream": true, "output": chunk})
			})
		}
		return t.runtime.tools.Terminal(runCtx, sessionID, args.Command)
	})
	if result.Status == "ok" && strings.TrimSpace(args.Command) == "uname -a" && t.runtime.tools.MarkSSHSystemProbe != nil {
		t.runtime.tools.MarkSSHSystemProbe(sessionID)
	}
	if result.Metadata == nil {
		result.Metadata = map[string]any{}
	}
	if decision.Mutating && !decision.Administrator {
		result.Metadata["safetyPlan"] = plan
		result.Metadata["safetyPlanNotice"] = "前置检查、快照、验证和回滚命令未自动执行；每条命令都需要单独审批。"
	}
	if result.ToolName == "" {
		result.ToolName = "terminal_command"
	}
	if result.Command == "" {
		result.Command = args.Command
	}
	result.TargetID = args.TargetID
	t.runtime.finishStep(step, result)
	if result.Error != "" {
		return marshalToolResult(result)
	}
	return marshalToolResult(result)
}

func mutationApprovalPurpose(purpose string, plan *MutationPlan, mutating bool) string {
	if !mutating || plan == nil {
		return purpose
	}
	return fmt.Sprintf("%s\n安全执行计划：前置检查=%s；状态快照=%s；执行后验证=%s；失败回滚=%s",
		purpose, plan.PreconditionCommand, plan.SnapshotCommand, plan.VerifyCommand, plan.RollbackCommand)
}

func validateMutationPlan(plan *MutationPlan) error {
	if plan == nil || plan.PreconditionCommand == "" || plan.SnapshotCommand == "" || plan.VerifyCommand == "" || plan.RollbackCommand == "" {
		return errors.New("写操作必须同时提供 preconditionCommand、snapshotCommand、verifyCommand 和 rollbackCommand")
	}
	for name, guard := range map[string]string{
		"precondition": plan.PreconditionCommand,
		"snapshot":     plan.SnapshotCommand,
		"verify":       plan.VerifyCommand,
	} {
		decision := AssessCommand(guard)
		if !decision.Allowed || !decision.ReadOnly {
			return fmt.Errorf("%sCommand 必须是允许的只读命令: %s", name, decision.Reason)
		}
	}
	rollbackDecision := AssessCommandMode(plan.RollbackCommand, true)
	if !rollbackDecision.Allowed || !rollbackDecision.Mutating {
		return errors.New("rollbackCommand 必须是经过显式授权的可执行回滚命令")
	}
	return nil
}

type sftpListDirTool struct {
	runtime   *Runtime
	state     *taskState
	sessionID string
}

func (t *sftpListDirTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "sftp_list_dir",
		Desc: "通过当前 SSH 会话的 SFTP 子系统列出远端目录。访问前必须获得用户审批。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"path":     {Type: schema.String, Desc: "远端目录路径", Required: true},
			"targetId": {Type: schema.String, Desc: "多目标任务中的目标 ID，可选", Required: false},
		}),
	}, nil
}

func (t *sftpListDirTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Path     string `json:"path"`
		TargetID string `json:"targetId"`
	}
	repaired, err := decodeToolArguments(argumentsInJSON, &args)
	if err != nil || repaired {
		return invalidToolArgumentsResult("sftp_list_dir", repaired, err)
	}
	args.TargetID = effectiveTargetID(t.state, args.TargetID)
	args.Path = resolveTargetText(t.state, args.TargetID, args.Path)
	sessionID, err := targetSession(t.state, t.sessionID, args.TargetID)
	if err != nil {
		return "", err
	}
	step := newStep(t.state, "sftp_list_dir", map[string]any{"path": args.Path, "targetId": args.TargetID}, "访问远端 SFTP 目录", "远端数据访问可能暴露敏感文件名和元数据")
	if cached, ok := t.runtime.cachedStepResult(step); ok {
		return marshalToolResult(*cached)
	}
	t.runtime.saveStep(step)
	step.Status = StepWaitingApproval
	step.UpdatedAt = time.Now()
	t.runtime.saveStep(step)
	allowed, approvalErr := t.runtime.requestApproval(t.state, Approval{TaskID: t.state.task.ID, StepID: step.ID,
		ToolName: "sftp_list_dir", Command: "SFTP LIST " + args.Path, Purpose: step.Purpose, Risk: step.Risk, ApprovalLevel: 1})
	if approvalErr != nil {
		return "", approvalErr
	}
	step.Approved = &allowed
	if !allowed {
		step.Status = StepRejected
		step.UpdatedAt = time.Now()
		t.runtime.saveStep(step)
		return marshalToolResult(ToolResult{ToolName: "sftp_list_dir", ExitCode: -1, Error: "用户拒绝访问远端 SFTP 目录", ErrorKind: "approval", Status: "rejected", Redacted: true})
	}
	step.Status = StepApproved
	step.UpdatedAt = time.Now()
	t.runtime.saveStep(step)
	step = t.runtime.startStep(step)
	result := t.runtime.executeStep(ctx, step, func(runCtx context.Context) ToolResult {
		return invokeReadOnlyTool(runCtx, 2, func() ToolResult {
			return t.runtime.tools.SFTPListDir(runCtx, sessionID, args.Path)
		})
	})
	if result.ToolName == "" {
		result.ToolName = "sftp_list_dir"
	}
	result.TargetID = args.TargetID
	t.runtime.finishStep(step, result)
	return marshalToolResult(result)
}

type sftpReadFileTool struct {
	runtime   *Runtime
	state     *taskState
	sessionID string
}

func (t *sftpReadFileTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "sftp_read_file",
		Desc: "通过当前 SSH 会话的 SFTP 子系统分段读取远端文件内容。访问前必须获得用户审批。默认读取 200 行；如果结果 metadata.hasMore=true，使用 metadata.nextStartLine 继续读取。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"path":      {Type: schema.String, Desc: "远端文件路径", Required: true},
			"startLine": {Type: schema.Integer, Desc: "起始行号，从 1 开始；默认 1", Required: false},
			"lineCount": {Type: schema.Integer, Desc: "读取行数，默认 200，最大 1000", Required: false},
			"targetId":  {Type: schema.String, Desc: "多目标任务中的目标 ID，可选", Required: false},
		}),
	}, nil
}

func (t *sftpReadFileTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Path      string `json:"path"`
		StartLine int    `json:"startLine"`
		LineCount int    `json:"lineCount"`
		TargetID  string `json:"targetId"`
	}
	repaired, err := decodeToolArguments(argumentsInJSON, &args)
	if err != nil || repaired {
		return invalidToolArgumentsResult("sftp_read_file", repaired, err)
	}
	args.TargetID = effectiveTargetID(t.state, args.TargetID)
	args.Path = resolveTargetText(t.state, args.TargetID, args.Path)
	if args.StartLine <= 0 {
		args.StartLine = 1
	}
	if args.LineCount <= 0 {
		args.LineCount = 200
	}
	if args.LineCount > 1000 {
		return marshalToolResult(ToolResult{ToolName: "sftp_read_file", ExitCode: -1, Error: "lineCount 不能超过 1000", ErrorKind: "validation", Status: "error", Redacted: true})
	}
	sessionID, err := targetSession(t.state, t.sessionID, args.TargetID)
	if err != nil {
		return "", err
	}
	step := newStep(t.state, "sftp_read_file", map[string]any{"path": args.Path, "startLine": args.StartLine, "lineCount": args.LineCount, "targetId": args.TargetID}, "访问远端 SFTP 文件", "远端数据访问可能包含敏感信息")
	if cached, ok := t.runtime.cachedStepResult(step); ok {
		return marshalToolResult(*cached)
	}
	t.runtime.saveStep(step)
	step.Status = StepWaitingApproval
	step.UpdatedAt = time.Now()
	t.runtime.saveStep(step)
	allowed, approvalErr := t.runtime.requestApproval(t.state, Approval{TaskID: t.state.task.ID, StepID: step.ID,
		ToolName: "sftp_read_file", Command: "SFTP READ " + args.Path, Purpose: step.Purpose, Risk: step.Risk, ApprovalLevel: 1})
	if approvalErr != nil {
		return "", approvalErr
	}
	step.Approved = &allowed
	if !allowed {
		step.Status = StepRejected
		step.UpdatedAt = time.Now()
		t.runtime.saveStep(step)
		return marshalToolResult(ToolResult{ToolName: "sftp_read_file", ExitCode: -1, Error: "用户拒绝访问远端 SFTP 文件", ErrorKind: "approval", Status: "rejected", Redacted: true})
	}
	step.Status = StepApproved
	step.UpdatedAt = time.Now()
	t.runtime.saveStep(step)
	step = t.runtime.startStep(step)
	result := t.runtime.executeStep(ctx, step, func(runCtx context.Context) ToolResult {
		return invokeReadOnlyTool(runCtx, 2, func() ToolResult {
			if t.runtime.tools.SFTPReadFileRange != nil {
				return t.runtime.tools.SFTPReadFileRange(runCtx, sessionID, args.Path, args.StartLine, args.LineCount)
			}
			return t.runtime.tools.SFTPReadFile(runCtx, sessionID, args.Path)
		})
	})
	if result.ToolName == "" {
		result.ToolName = "sftp_read_file"
	}
	result.TargetID = args.TargetID
	t.runtime.finishStep(step, result)
	return marshalToolResult(result)
}

type ragSearchTool struct {
	runtime *Runtime
	state   *taskState
}

func (t *ragSearchTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "rag_search",
		Desc: "检索 GoSSH 本地私有知识库，返回相关片段。只读工具。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"query": {Type: schema.String, Desc: "检索关键词或问题", Required: true},
			"limit": {Type: schema.Integer, Desc: "返回条数，默认 5", Required: false},
		}),
	}, nil
}

func (t *ragSearchTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	repaired, err := decodeToolArguments(argumentsInJSON, &args)
	if err != nil || repaired {
		return invalidToolArgumentsResult("rag_search", repaired, err)
	}
	step := newStep(t.state, "rag_search", map[string]any{"query": args.Query, "limit": args.Limit}, "检索本地知识库", "只读")
	if cached, ok := t.runtime.cachedStepResult(step); ok {
		return marshalToolResult(*cached)
	}
	step = t.runtime.startStep(step)
	result := t.runtime.executeStep(ctx, step, func(runCtx context.Context) ToolResult {
		return invokeReadOnlyTool(runCtx, 2, func() ToolResult {
			return t.runtime.tools.RAGSearch(runCtx, args.Query, args.Limit)
		})
	})
	if result.ToolName == "" {
		result.ToolName = "rag_search"
	}
	t.runtime.finishStep(step, result)
	return marshalToolResult(result)
}

type diagnosticsTool struct {
	runtime *Runtime
	state   *taskState
}

// localReadTool exposes explicitly scoped local and internet sources. It never
// has a target session, so these operations cannot be confused with SSH/SFTP.
// Each source access is approved individually because logs, documents, and
// web requests may contain sensitive information.
type localReadTool struct {
	runtime *Runtime
	state   *taskState
	kind    string
}

func (t *localReadTool) Info(context.Context) (*schema.ToolInfo, error) {
	params := map[string]*schema.ParameterInfo{}
	desc := ""
	switch t.kind {
	case "local_gossh_config":
		desc = "读取本机 GoSSH 配置摘要（连接密码、私钥和 API Key 会被排除）。访问前必须获得用户审批。"
	case "local_session_log":
		desc = "读取本机 ~/.gossh/logs/YYYY-MM-DD/<session-id>.log 中指定 SSH 会话日志的一个字节分页。不会连接远端主机，访问前必须获得用户审批。"
		params["date"] = &schema.ParameterInfo{Type: schema.String, Desc: "日志日期，格式 YYYY-MM-DD", Required: true}
		params["sessionId"] = &schema.ParameterInfo{Type: schema.String, Desc: "SSH session ID", Required: true}
		params["offset"] = &schema.ParameterInfo{Type: schema.Integer, Desc: "字节偏移，默认 0", Required: false}
		params["maxBytes"] = &schema.ParameterInfo{Type: schema.Integer, Desc: "读取字节数，默认 65536，最大 65536", Required: false}
	case "local_document_read":
		desc = "读取用户明确指定的本机文档的一个字节分页。不会扫描目录或连接远端主机，访问前必须获得用户审批。"
		params["path"] = &schema.ParameterInfo{Type: schema.String, Desc: "用户指定的本地文件绝对路径", Required: true}
		params["offset"] = &schema.ParameterInfo{Type: schema.Integer, Desc: "字节偏移，默认 0", Required: false}
		params["maxBytes"] = &schema.ParameterInfo{Type: schema.Integer, Desc: "读取字节数，默认 65536，最大 65536", Required: false}
	case "web_search":
		desc = "通过互联网搜索公开资料。不会连接 SSH/SFTP，访问前必须获得用户审批。"
		params["query"] = &schema.ParameterInfo{Type: schema.String, Desc: "搜索关键词或问题", Required: true}
		params["limit"] = &schema.ParameterInfo{Type: schema.Integer, Desc: "结果数，默认 5，最大 10", Required: false}
	case "web_read":
		desc = "读取指定公开网页并提取文本。不会连接 SSH/SFTP，访问前必须获得用户审批。"
		params["url"] = &schema.ParameterInfo{Type: schema.String, Desc: "公开网页 URL", Required: true}
		params["maxBytes"] = &schema.ParameterInfo{Type: schema.Integer, Desc: "读取字节数，默认 65536，最大 131072", Required: false}
	default:
		return nil, fmt.Errorf("未知本地读取工具: %s", t.kind)
	}
	return &schema.ToolInfo{Name: t.kind, Desc: desc, ParamsOneOf: schema.NewParamsOneOfByParams(params)}, nil
}

func (t *localReadTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Date      string `json:"date"`
		SessionID string `json:"sessionId"`
		Path      string `json:"path"`
		Query     string `json:"query"`
		URL       string `json:"url"`
		Offset    int64  `json:"offset"`
		MaxBytes  int    `json:"maxBytes"`
		Limit     int    `json:"limit"`
	}
	repaired, err := decodeToolArguments(argumentsInJSON, &args)
	if err != nil || repaired {
		return invalidToolArgumentsResult(t.kind, repaired, err)
	}
	if args.Offset < 0 {
		return invalidToolArgumentsResult(t.kind, false, errors.New("offset 不能为负数"))
	}
	purpose, risk, command := "读取本地只读数据", "本地数据可能包含敏感信息", t.kind
	switch t.kind {
	case "local_gossh_config":
		purpose, risk, command = "读取本机 GoSSH 配置摘要", "配置元数据可能暴露主机和账号信息；秘密字段会被排除", "LOCAL GOSSH CONFIG"
	case "local_session_log":
		if strings.TrimSpace(args.Date) == "" || strings.TrimSpace(args.SessionID) == "" {
			return invalidToolArgumentsResult(t.kind, false, errors.New("date 和 sessionId 不能为空"))
		}
		args.MaxBytes = normalizeReadLimit(args.MaxBytes, 64*1024)
		purpose, risk, command = "读取本机 GoSSH 会话日志", "终端日志可能包含敏感命令输出", fmt.Sprintf("LOCAL GOSSH LOG %s/%s", args.Date, args.SessionID)
	case "local_document_read":
		if strings.TrimSpace(args.Path) == "" {
			return invalidToolArgumentsResult(t.kind, false, errors.New("path 不能为空"))
		}
		args.MaxBytes = normalizeReadLimit(args.MaxBytes, 64*1024)
		purpose, risk, command = "读取用户指定的本地文档", "文档内容可能包含敏感信息", "LOCAL FILE "+args.Path
	case "web_search":
		if strings.TrimSpace(args.Query) == "" {
			return invalidToolArgumentsResult(t.kind, false, errors.New("query 不能为空"))
		}
		if args.Limit <= 0 {
			args.Limit = 5
		}
		if args.Limit > 10 {
			return invalidToolArgumentsResult(t.kind, false, errors.New("limit 不能超过 10"))
		}
		purpose, risk, command = "搜索互联网公开资料", "搜索关键词会发送给外部搜索服务", "WEB SEARCH "+args.Query
	case "web_read":
		if strings.TrimSpace(args.URL) == "" {
			return invalidToolArgumentsResult(t.kind, false, errors.New("url 不能为空"))
		}
		args.MaxBytes = normalizeReadLimit(args.MaxBytes, 128*1024)
		purpose, risk, command = "读取互联网公开网页", "网页地址与请求元数据会发送给外部站点", "WEB READ "+args.URL
	}
	step := newStep(t.state, t.kind, map[string]any{"date": args.Date, "sessionId": args.SessionID, "path": args.Path, "query": args.Query, "url": args.URL, "offset": args.Offset, "maxBytes": args.MaxBytes, "limit": args.Limit}, purpose, risk)
	if cached, ok := t.runtime.cachedStepResult(step); ok {
		return marshalToolResult(*cached)
	}
	t.runtime.saveStep(step)
	step.Status = StepWaitingApproval
	step.UpdatedAt = time.Now()
	t.runtime.saveStep(step)
	allowed, approvalErr := t.runtime.requestApproval(t.state, Approval{TaskID: t.state.task.ID, StepID: step.ID, ToolName: t.kind, Command: command, Purpose: purpose, Risk: risk, ApprovalLevel: 1})
	if approvalErr != nil {
		return "", approvalErr
	}
	step.Approved = &allowed
	if !allowed {
		step.Status = StepRejected
		step.UpdatedAt = time.Now()
		t.runtime.saveStep(step)
		return marshalToolResult(ToolResult{ToolName: t.kind, ExitCode: -1, Error: "用户拒绝该只读数据访问", ErrorKind: "approval", Status: "rejected", Redacted: true})
	}
	step.Status = StepApproved
	step.UpdatedAt = time.Now()
	t.runtime.saveStep(step)
	step = t.runtime.startStep(step)
	result := t.runtime.executeStep(ctx, step, func(runCtx context.Context) ToolResult {
		return invokeReadOnlyTool(runCtx, 2, func() ToolResult {
			switch t.kind {
			case "local_gossh_config":
				return t.runtime.tools.LocalGoSSHConfig(runCtx)
			case "local_session_log":
				return t.runtime.tools.LocalSessionLog(runCtx, args.Date, args.SessionID, args.Offset, args.MaxBytes)
			case "local_document_read":
				return t.runtime.tools.LocalDocumentRead(runCtx, args.Path, args.Offset, args.MaxBytes)
			case "web_search":
				return t.runtime.tools.WebSearch(runCtx, args.Query, args.Limit)
			default:
				return t.runtime.tools.WebRead(runCtx, args.URL, args.MaxBytes)
			}
		})
	})
	if result.ToolName == "" {
		result.ToolName = t.kind
	}
	t.runtime.finishStep(step, result)
	return marshalToolResult(result)
}

func normalizeReadLimit(value, maximum int) int {
	if value <= 0 {
		return min(64*1024, maximum)
	}
	if value > maximum {
		return maximum
	}
	return value
}

func (t *diagnosticsTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name:        "gossh_diagnostics",
		Desc:        "读取 GoSSH 当前运行诊断、SSH 安全算法能力、SSH Agent、known_hosts 和最近 SSH/SFTP 操作统计。只读工具。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{}),
	}, nil
}

func (t *diagnosticsTool) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	step := newStep(t.state, "gossh_diagnostics", nil, "读取 GoSSH 诊断信息", "只读")
	if cached, ok := t.runtime.cachedStepResult(step); ok {
		return marshalToolResult(*cached)
	}
	step = t.runtime.startStep(step)
	result := t.runtime.executeStep(ctx, step, func(runCtx context.Context) ToolResult {
		return invokeReadOnlyTool(runCtx, 2, func() ToolResult {
			return t.runtime.tools.Diagnostics(runCtx)
		})
	})
	if result.ToolName == "" {
		result.ToolName = "gossh_diagnostics"
	}
	t.runtime.finishStep(step, result)
	return marshalToolResult(result)
}

type reportTool struct {
	runtime *Runtime
	state   *taskState
}

type reportArguments struct {
	Title           string           `json:"title"`
	Summary         string           `json:"summary"`
	Severity        string           `json:"severity"`
	Findings        []ReportFinding  `json:"findings"`
	Evidence        []ReportEvidence `json:"evidence"`
	Recommendations []string         `json:"recommendations"`
	ExecutedSteps   []string         `json:"executedSteps"`
	Limitations     []string         `json:"limitations"`
	Custom          map[string]any   `json:"custom"`
}

func (t *reportTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "report",
		Desc: "生成结构化阶段报告或最终报告。报告必须基于已获得的工具结果。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"title":    {Type: schema.String, Desc: "报告标题", Required: true},
			"summary":  {Type: schema.String, Desc: "结论摘要", Required: true},
			"severity": {Type: schema.String, Desc: "严重性: info/low/medium/high/critical", Required: false},
			"findings": {Type: schema.Array, Desc: "发现项数组", ElemInfo: &schema.ParameterInfo{Type: schema.Object, SubParams: map[string]*schema.ParameterInfo{
				"title":       {Type: schema.String, Desc: "发现标题", Required: true},
				"description": {Type: schema.String, Desc: "发现说明", Required: true},
				"severity":    {Type: schema.String, Desc: "严重性", Required: false},
				"evidenceIds": {Type: schema.Array, Desc: "关联证据 ID", ElemInfo: &schema.ParameterInfo{Type: schema.String}},
			}}},
			"evidence": {Type: schema.Array, Desc: "证据数组", Required: true, ElemInfo: &schema.ParameterInfo{Type: schema.Object, SubParams: map[string]*schema.ParameterInfo{
				"id":       {Type: schema.String, Desc: "唯一证据 ID", Required: true},
				"toolName": {Type: schema.String, Desc: "工具名称"}, "stepId": {Type: schema.String, Desc: "步骤 ID"},
				"targetId": {Type: schema.String, Desc: "目标 ID"}, "command": {Type: schema.String, Desc: "命令"},
				"source": {Type: schema.String, Desc: "来源"}, "output": {Type: schema.String, Desc: "输出摘要"},
				"exitCode": {Type: schema.Integer, Desc: "退出码"},
			}}},
			"recommendations": {Type: schema.Array, Desc: "建议数组", ElemInfo: &schema.ParameterInfo{Type: schema.String}},
			"executedSteps":   {Type: schema.Array, Desc: "已执行步骤数组", ElemInfo: &schema.ParameterInfo{Type: schema.String}},
			"limitations":     {Type: schema.Array, Desc: "限制数组", ElemInfo: &schema.ParameterInfo{Type: schema.String}},
			"custom":          {Type: schema.Object, Desc: "Skill 报告模板定义的自定义字段对象"},
		}),
	}, nil
}

func (t *reportTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	args, err := decodeReportArguments(argumentsInJSON)
	if err != nil {
		return marshalToolResult(ToolResult{
			ToolName:  "report",
			ExitCode:  -1,
			Error:     fmt.Sprintf("report 参数 JSON 无效，请使用完整 JSON 后重试: %v", err),
			ErrorKind: "validation",
			Status:    "error",
			Redacted:  true,
		})
	}
	severity, err := parseSeverity(args.Severity)
	if err != nil {
		return invalidToolArgumentsResult("report", false, err)
	}
	report := Report{
		Title: strings.TrimSpace(args.Title), Summary: strings.TrimSpace(args.Summary),
		Severity: severity, GeneratedAt: time.Now(),
		Findings: args.Findings, Evidence: args.Evidence,
		Recommendations: args.Recommendations, ExecutedSteps: args.ExecutedSteps,
		Limitations: args.Limitations, Custom: args.Custom,
	}
	if err := validateReport(report); err != nil {
		return invalidToolArgumentsResult("report", false, err)
	}
	if t.state != nil && t.state.task.Mode == "terminal_output" && (len(report.Findings) == 0 || len(report.Recommendations) == 0) {
		return invalidToolArgumentsResult("report", false, errors.New("终端输出分析报告必须包含 findings 和 recommendations"))
	}
	if t.state != nil {
		if err := validateReportTemplate(report, t.state.task.ReportTemplate); err != nil {
			return invalidToolArgumentsResult("report", false, err)
		}
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return "", err
	}
	step := newStep(t.state, "report", map[string]any{"title": report.Title, "summary": report.Summary, "evidence": report.Evidence}, "生成结构化报告", "只读")
	if cached, ok := t.runtime.cachedStepResult(step); ok {
		return marshalToolResult(*cached)
	}
	step = t.runtime.startStep(step)
	result := ToolResult{ToolName: "report", Output: string(raw), ExitCode: 0, Metadata: map[string]any{"title": report.Title, "schema": "gossh.agent.report.v1"}}
	t.runtime.finishStep(step, result)
	return marshalToolResult(result)
}

// decodeReportArguments requires complete JSON. A truncated report is a
// retryable validation result and is never silently repaired or executed.
func decodeReportArguments(input string) (reportArguments, error) {
	var args reportArguments
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return args, err
	}
	return args, nil
}

// Non-report wrappers use this only to classify malformed arguments without
// executing them. Report arguments are never repaired.
func repairTruncatedJSONObject(input string) ([]byte, bool) {
	text := strings.TrimSpace(input)
	if text == "" || text[0] != '{' {
		return nil, false
	}
	stack := make([]byte, 0, 4)
	inString, escaped := false, false
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch ch {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{', '[':
			stack = append(stack, ch)
		case '}', ']':
			if len(stack) == 0 || (ch == '}' && stack[len(stack)-1] != '{') || (ch == ']' && stack[len(stack)-1] != '[') {
				return nil, false
			}
			stack = stack[:len(stack)-1]
		}
	}
	if escaped || (!inString && len(stack) == 0) {
		return nil, false
	}
	if inString {
		text += `"`
	}
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == '{' {
			text += "}"
		} else {
			text += "]"
		}
	}
	if !json.Valid([]byte(text)) {
		return nil, false
	}
	return []byte(text), true
}

func parseSeverity(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "info", nil
	}
	switch value {
	case "info", "critical", "high", "medium", "low":
		return value, nil
	default:
		return "", fmt.Errorf("报告 severity 无效: %s", value)
	}
}

// validateReportTemplate supports a compact JSON schema for Skill reports.
// Plain-text templates remain prompt guidance for backward compatibility.
func validateReportTemplate(report Report, template string) error {
	template = strings.TrimSpace(template)
	if !strings.HasPrefix(template, "{") {
		return nil
	}
	var schema struct {
		RequiredFields       []string `json:"requiredFields"`
		AllowedSeverities    []string `json:"allowedSeverities"`
		TitlePrefix          string   `json:"titlePrefix"`
		RequiredCustomFields []string `json:"requiredCustomFields"`
	}
	if err := json.Unmarshal([]byte(template), &schema); err != nil {
		return fmt.Errorf("Skill reportTemplate JSON 无效: %w", err)
	}
	for _, field := range schema.RequiredFields {
		present := map[string]bool{
			"title": report.Title != "", "summary": report.Summary != "", "severity": report.Severity != "",
			"findings": len(report.Findings) > 0, "evidence": len(report.Evidence) > 0,
			"recommendations": len(report.Recommendations) > 0, "executedSteps": len(report.ExecutedSteps) > 0, "limitations": len(report.Limitations) > 0,
		}[field]
		if !present {
			return fmt.Errorf("Skill 报告缺少模板要求字段: %s", field)
		}
	}
	if schema.TitlePrefix != "" && !strings.HasPrefix(report.Title, schema.TitlePrefix) {
		return fmt.Errorf("Skill 报告标题必须以 %q 开头", schema.TitlePrefix)
	}
	if len(schema.AllowedSeverities) > 0 {
		allowed := false
		for _, severity := range schema.AllowedSeverities {
			if severity == report.Severity {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("Skill 报告 severity 不在模板允许范围内: %s", report.Severity)
		}
	}
	for _, key := range schema.RequiredCustomFields {
		if _, ok := report.Custom[key]; !ok {
			return fmt.Errorf("Skill 报告缺少自定义字段: %s", key)
		}
	}
	return nil
}

func validateReport(report Report) error {
	if report.Title == "" || report.Summary == "" || len(report.Evidence) == 0 {
		return errors.New("结构化报告必须包含 title、summary 和至少一条 evidence")
	}
	evidenceIDs := make(map[string]struct{}, len(report.Evidence))
	for _, evidence := range report.Evidence {
		if strings.TrimSpace(evidence.ID) == "" {
			return errors.New("结构化报告的每条 evidence 都必须包含唯一 id")
		}
		if _, exists := evidenceIDs[evidence.ID]; exists {
			return fmt.Errorf("结构化报告 evidence id 重复: %s", evidence.ID)
		}
		evidenceIDs[evidence.ID] = struct{}{}
	}
	if _, err := parseSeverity(report.Severity); err != nil {
		return err
	}
	for _, finding := range report.Findings {
		if strings.TrimSpace(finding.Title) == "" || strings.TrimSpace(finding.Description) == "" {
			return errors.New("结构化报告的 finding 必须包含 title 和 description")
		}
		if _, err := parseSeverity(finding.Severity); err != nil {
			return err
		}
		for _, id := range finding.EvidenceIDs {
			if _, exists := evidenceIDs[id]; !exists {
				return fmt.Errorf("finding 引用了不存在的 evidence: %s", id)
			}
		}
	}
	return nil
}
