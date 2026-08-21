// owner: muswood | Email: mumu920@outlook.com
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

type truncatedArgumentsTool struct{}

type executionTrackingTool struct{ executed *atomic.Int32 }

func (truncatedArgumentsTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "external_tool"}, nil
}

func (truncatedArgumentsTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	return "", errors.New("工具参数无效: unexpected end of JSON input")
}

func (t executionTrackingTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "external_tool"}, nil
}

func (t executionTrackingTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	t.executed.Add(1)
	return `{"status":"ok"}`, nil
}

func TestToolNodeConvertsTruncatedArgumentsIntoRetryableToolResult(t *testing.T) {
	protected := protectMalformedArgumentTools([]tool.BaseTool{truncatedArgumentsTool{}})
	node, err := compose.NewToolNode(context.Background(), &compose.ToolsNodeConfig{
		Tools:               protected,
		ExecuteSequentially: true,
	})
	if err != nil {
		t.Fatalf("create tool node: %v", err)
	}
	out, err := node.Invoke(context.Background(), schema.AssistantMessage("", []schema.ToolCall{{
		ID: "call-truncated",
		Function: schema.FunctionCall{
			Name:      "external_tool",
			Arguments: `{"command":"systemctl status chrony"`,
		},
	}}))
	if err != nil {
		t.Fatalf("truncated tool call escaped as ToolNode error: %v", err)
	}
	if len(out) != 1 || out[0].Role != schema.Tool {
		t.Fatalf("unexpected tool node output: %#v", out)
	}
	var result ToolResult
	if err := json.Unmarshal([]byte(out[0].Content), &result); err != nil {
		t.Fatalf("tool result is not JSON: %v", err)
	}
	if result.ErrorKind != "validation" || !strings.Contains(result.Error, "未执行") {
		t.Fatalf("unexpected retryable result: %#v", result)
	}
}

func TestMalformedWrapperRejectsTruncatedArgumentsBeforeExecution(t *testing.T) {
	executed := &atomic.Int32{}
	protected := protectMalformedArgumentTools([]tool.BaseTool{executionTrackingTool{executed: executed}})
	output, err := protected[0].(tool.InvokableTool).InvokableRun(context.Background(), `{"command":"id"`)
	if err != nil {
		t.Fatalf("wrapper returned an invocation error: %v", err)
	}
	var result ToolResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("wrapper result is not JSON: %v", err)
	}
	if executed.Load() != 0 || result.ErrorKind != "validation" {
		t.Fatalf("truncated arguments reached tool: executed=%d result=%#v", executed.Load(), result)
	}
}

func TestPlainCommandArgumentDoesNotConfuseTruncatedJSONWithACommand(t *testing.T) {
	tests := []struct {
		input string
		want  string
		ok    bool
	}{
		{input: `systemctl status chrony --no-pager`, want: "systemctl status chrony --no-pager", ok: true},
		{input: `"systemctl --failed --no-pager"`, want: "systemctl --failed --no-pager", ok: true},
		{input: `{"command":"systemctl status chrony"`, ok: false},
		{input: `{"command":"systemctl status chrony"}`, ok: false},
		{input: ``, ok: false},
	}
	for _, test := range tests {
		got, ok := plainCommandArgument(test.input)
		if ok != test.ok || got != test.want {
			t.Errorf("plainCommandArgument(%q) = %q, %v; want %q, %v", test.input, got, ok, test.want, test.ok)
		}
	}
}

func TestNewStepUsesStableIdempotencyKey(t *testing.T) {
	state := &taskState{task: Task{ID: "task-1"}}
	a := newStep(state, "terminal_command", map[string]any{"command": "df -h"}, "check", "read-only")
	b := newStep(state, "terminal_command", map[string]any{"command": "df -h"}, "check", "read-only")
	if a.ID != b.ID || a.IdempotencyKey != b.IdempotencyKey {
		t.Fatalf("step identity is not stable: %#v %#v", a, b)
	}
	if a.TimeoutMillis != 30*60*1000 {
		t.Fatalf("terminal command timeout = %d ms, want 30 minutes", a.TimeoutMillis)
	}
}

func TestSSHRequiresSystemProbeBeforeOtherTerminalCommands(t *testing.T) {
	state := &taskState{task: Task{Transport: "ssh"}}
	if err := requireSSHSystemProbe(state, "s1", "df -h", nil); err == nil {
		t.Fatal("SSH task accepted an OS-specific command before system detection")
	}
	if err := requireSSHSystemProbe(state, "s1", "uname -a", nil); err != nil {
		t.Fatalf("SSH task rejected the system probe: %v", err)
	}
	state.task.Steps = []Step{{
		ToolName: "terminal_command", Status: StepCompleted,
		Arguments: map[string]any{"command": "uname -a"}, Result: &ToolResult{Status: "ok"},
	}}
	if err := requireSSHSystemProbe(state, "s1", "df -h", nil); err != nil {
		t.Fatalf("SSH task rejected command after system detection: %v", err)
	}
}

func TestSSHSystemProbeIsPerSession(t *testing.T) {
	state := &taskState{task: Task{Transport: "ssh"}}
	done := func(id string) bool { return id == "session-a" }
	if err := requireSSHSystemProbe(state, "session-a", "df -h", done); err != nil {
		t.Fatalf("same session rejected: %v", err)
	}
	if err := requireSSHSystemProbe(state, "session-b", "df -h", done); err == nil {
		t.Fatal("new session accepted before probe")
	}
}

func TestTargetSessionFallsBackForSingleTargetTasks(t *testing.T) {
	tests := []struct {
		name     string
		targets  []Target
		fallback string
		targetID string
		want     string
		wantErr  bool
	}{
		{name: "current session without target list", fallback: "session-current", targetID: "conn-old", want: "session-current"},
		{name: "unique target with stale id", targets: []Target{{ID: "target-current", SessionID: "session-current"}}, targetID: "conn-old", want: "session-current"},
		{name: "known target", targets: []Target{{ID: "target-a", SessionID: "session-a"}, {ID: "target-b", SessionID: "session-b"}}, targetID: "target-b", want: "session-b"},
		{name: "unknown target in multi target task", targets: []Target{{ID: "target-a", SessionID: "session-a"}, {ID: "target-b", SessionID: "session-b"}}, targetID: "conn-old", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := &taskState{task: Task{Targets: test.targets}}
			got, err := targetSession(state, test.fallback, test.targetID)
			if test.wantErr {
				if err == nil {
					t.Fatalf("expected target resolution error, got session %q", got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("target session = %q, err = %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestEffectiveTargetIDNormalizesStaleSingleTargetIDs(t *testing.T) {
	state := &taskState{task: Task{Targets: []Target{{ID: "target-only", SessionID: "session-only"}}}}
	if got := effectiveTargetID(state, "conn-old"); got != "" {
		t.Fatalf("stale single-target ID was retained: %q", got)
	}
	if got := effectiveTargetID(state, "target-only"); got != "target-only" {
		t.Fatalf("known target ID was not retained: %q", got)
	}

	state.task.Targets = []Target{{ID: "target-a", SessionID: "session-a"}, {ID: "target-b", SessionID: "session-b"}}
	if got := effectiveTargetID(state, "conn-old"); got != "conn-old" {
		t.Fatalf("unknown multi-target ID was normalized unexpectedly: %q", got)
	}
}

func TestStructuredReportValidation(t *testing.T) {
	evidence := []ReportEvidence{{ID: "e1", ToolName: "terminal_command", Output: "ok"}}
	valid := Report{Title: "title", Summary: "summary", Severity: "high", Evidence: evidence,
		Findings: []ReportFinding{{Title: "finding", Description: "description", Severity: "high", EvidenceIDs: []string{"e1"}}}}
	if err := validateReport(valid); err != nil {
		t.Fatalf("valid report rejected: %v", err)
	}
	invalid := valid
	invalid.Findings = []ReportFinding{{Title: "finding", Description: "description", EvidenceIDs: []string{"missing"}}}
	if err := validateReport(invalid); err == nil {
		t.Fatal("report with unknown evidence reference was accepted")
	}
	if _, err := parseSeverity("urgent"); err == nil {
		t.Fatal("invalid severity was accepted")
	}
}

func TestReportMarkdownIsUserFacingEventFormat(t *testing.T) {
	markdown := reportMarkdown(Report{
		Title:           "Health",
		Summary:         "系统状态良好",
		Severity:        "low",
		Findings:        []ReportFinding{{Title: "内存", Description: "使用率正常", Severity: "info", EvidenceIDs: []string{"E1"}}},
		Recommendations: []string{"持续观察内存趋势"},
	})
	for _, expected := range []string{"# Health", "## 摘要", "### 内存", "**证据：** `E1`", "## 建议", "- 持续观察内存趋势"} {
		if !strings.Contains(markdown, expected) {
			t.Fatalf("report markdown missing %q: %s", expected, markdown)
		}
	}
	if strings.HasPrefix(strings.TrimSpace(markdown), "{") {
		t.Fatalf("final report is still JSON: %s", markdown)
	}
}

func TestReportToolReturnsRetryableResultForMalformedArguments(t *testing.T) {
	raw, err := (&reportTool{}).InvokableRun(context.Background(), `{"title":`)
	if err != nil {
		t.Fatalf("malformed report arguments returned an invocation error: %v", err)
	}
	var result ToolResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("malformed report result is not JSON: %v", err)
	}
	if result.Status != "error" || result.ErrorKind != "validation" || result.Error == "" {
		t.Fatalf("unexpected malformed report result: %#v", result)
	}
}

func TestReportToolRejectsTruncatedOrStringifiedArguments(t *testing.T) {
	for _, input := range []string{
		`{"title":"系统检查","summary":"状态正常","evidence":[{"id":"e1"}`,
		`{"title":"系统检查","summary":"状态正常","evidence":"[{\"id\":\"e1\"}]"}`,
	} {
		raw, err := (&reportTool{}).InvokableRun(context.Background(), input)
		if err != nil {
			t.Fatalf("report arguments returned an invocation error: %v", err)
		}
		var result ToolResult
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatalf("report result is not JSON: %v", err)
		}
		if result.Status != "error" || result.ErrorKind != "validation" {
			t.Fatalf("invalid report was accepted: %#v", result)
		}
	}
}

func TestReportToolAcceptsNativeStructuredArguments(t *testing.T) {
	runtime := NewRuntime(nil, nil, ToolSet{}, nil)
	state := &taskState{task: Task{ID: "native-report", Goal: "系统检查"}}
	runtime.mu.Lock()
	runtime.tasks[state.task.ID] = state
	runtime.mu.Unlock()
	raw, err := (&reportTool{runtime: runtime, state: state}).InvokableRun(context.Background(), `{"title":"系统检查","summary":"状态正常","findings":[{"title":"服务","description":"运行正常","severity":"info","evidenceIds":["e1"]}],"evidence":[{"id":"e1","toolName":"terminal_command","output":"ok","exitCode":0}],"recommendations":["持续观察"]}`)
	if err != nil {
		t.Fatalf("native report invocation failed: %v", err)
	}
	var result ToolResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil || result.Status == "error" {
		t.Fatalf("native report was rejected: result=%#v err=%v", result, err)
	}
}

func TestTerminalToolDoesNotExecuteRepairedArguments(t *testing.T) {
	executed := atomic.Int32{}
	runtime := NewRuntime(nil, nil, ToolSet{Terminal: func(context.Context, string, string) ToolResult {
		executed.Add(1)
		return ToolResult{ToolName: "terminal_command", ExitCode: 0, Status: "ok"}
	}}, nil)
	state := &taskState{task: Task{ID: "truncated-terminal", SessionID: "session-1"}, approve: make(chan approvalResult, 1)}
	runtime.mu.Lock()
	runtime.tasks[state.task.ID] = state
	runtime.mu.Unlock()

	raw, err := (&terminalCommandTool{runtime: runtime, state: state, sessionID: "session-1"}).InvokableRun(context.Background(), `{"command":"uname -a"`)
	if err != nil {
		t.Fatalf("truncated terminal arguments returned invocation error: %v", err)
	}
	var result ToolResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("terminal retry result is not JSON: %v", err)
	}
	if result.ErrorKind != "validation" || executed.Load() != 0 {
		t.Fatalf("truncated terminal call was not safely rejected: result=%#v executed=%d", result, executed.Load())
	}
}

func TestTerminalCommandMarksResolvedTargetSystemProbe(t *testing.T) {
	previousSecurity := GetSecurityConfig()
	security := DefaultSecurityConfig()
	security.AdministratorEnabled = true
	SetSecurityConfig(security)
	defer SetSecurityConfig(previousSecurity)

	var marked string
	runtime := NewRuntime(nil, nil, ToolSet{
		Terminal: func(_ context.Context, sessionID, command string) ToolResult {
			if sessionID != "session-b" || command != "uname -a" {
				t.Fatalf("terminal ran on %q with %q", sessionID, command)
			}
			return ToolResult{ToolName: "terminal_command", ExitCode: 0, Status: "ok"}
		},
		MarkSSHSystemProbe: func(sessionID string) { marked = sessionID },
	}, nil)
	state := &taskState{task: Task{
		ID: "target-probe", Transport: "ssh", SessionID: "session-a",
		Targets: []Target{{ID: "target-a", SessionID: "session-a"}, {ID: "target-b", SessionID: "session-b"}},
	}, ctx: context.Background(), approve: make(chan approvalResult, 1)}
	runtime.mu.Lock()
	runtime.tasks[state.task.ID] = state
	runtime.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		_, err := (&terminalCommandTool{runtime: runtime, state: state, sessionID: "session-a"}).InvokableRun(context.Background(), `{"command":"uname -a","purpose":"detect system","risk":"read-only","targetId":"target-b"}`)
		done <- err
	}()
	if err := runtime.Approve(state.task.ID, waitForApproval(t, runtime, state), true); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if marked != "session-b" {
		t.Fatalf("system probe marked %q, want resolved target session-b", marked)
	}
}

func TestInvokeReadOnlyToolRetriesAndRecordsAttempts(t *testing.T) {
	attempts := 0
	result := invokeReadOnlyTool(context.Background(), 2, func() ToolResult {
		attempts++
		if attempts == 1 {
			return ToolResult{ToolName: "test", ExitCode: -1, Error: "temporary failure"}
		}
		return ToolResult{ToolName: "test", ExitCode: 0, Output: "ok"}
	})
	if attempts != 2 || result.Attempts != 2 || result.Error != "" {
		t.Fatalf("unexpected retry result: attempts=%d result=%#v", attempts, result)
	}
}

func TestInvokeReadOnlyToolStopsAtAttemptLimit(t *testing.T) {
	attempts := 0
	result := invokeReadOnlyTool(context.Background(), 2, func() ToolResult {
		attempts++
		return ToolResult{ToolName: "test", ExitCode: -1, Error: "persistent failure"}
	})
	if attempts != 2 || result.Attempts != 2 || result.Error != "persistent failure" {
		t.Fatalf("unexpected limited retry result: attempts=%d result=%#v", attempts, result)
	}
}

func TestSFTPReadFileDoesNotAccessRemoteDataWhenRejected(t *testing.T) {
	called := atomic.Int32{}
	runtime := NewRuntime(nil, nil, ToolSet{SFTPReadFile: func(context.Context, string, string) ToolResult {
		called.Add(1)
		return ToolResult{ToolName: "sftp_read_file", Output: "secret", ExitCode: 0, Status: "ok"}
	}}, nil)
	state := &taskState{task: Task{ID: "reject-sftp", SessionID: "session-1"}, ctx: context.Background(), approve: make(chan approvalResult, 1)}
	runtime.mu.Lock()
	runtime.tasks[state.task.ID] = state
	runtime.mu.Unlock()

	done := make(chan string, 1)
	go func() {
		raw, err := (&sftpReadFileTool{runtime: runtime, state: state, sessionID: "session-1"}).InvokableRun(context.Background(), `{"path":"/etc/passwd"}`)
		if err != nil {
			done <- err.Error()
			return
		}
		done <- raw
	}()
	stepID := waitForApproval(t, runtime, state)
	if err := runtime.Approve(state.task.ID, stepID, false); err != nil {
		t.Fatal(err)
	}
	raw := <-done
	var result ToolResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "rejected" || result.ErrorKind != "approval" || called.Load() != 0 {
		t.Fatalf("rejected SFTP access invoked callback: result=%#v calls=%d", result, called.Load())
	}
}

func TestMutationPlanRequiresReadOnlyGuardsAndAuthorizedRollback(t *testing.T) {
	plan := &MutationPlan{PreconditionCommand: "stat /tmp/state", SnapshotCommand: "cat /tmp/state", VerifyCommand: "stat /tmp/state", RollbackCommand: "mv /tmp/state.bak /tmp/state"}
	if err := validateMutationPlan(plan); err != nil {
		t.Fatalf("valid mutation plan rejected: %v", err)
	}
	plan.RollbackCommand = "rm -f /tmp/state"
	if err := validateMutationPlan(plan); err == nil {
		t.Fatal("destructive rollback was accepted")
	}
	plan.RollbackCommand = ""
	if err := validateMutationPlan(plan); err == nil {
		t.Fatal("incomplete mutation plan was accepted")
	}
}

func TestMultiTargetCommandUsesBoundedConcurrencyAndIsolatesFailures(t *testing.T) {
	var running, maxRunning atomic.Int32
	runtime := NewRuntime(nil, nil, ToolSet{Terminal: func(context.Context, string, string) ToolResult {
		current := running.Add(1)
		for {
			old := maxRunning.Load()
			if current <= old || maxRunning.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		running.Add(-1)
		return ToolResult{ToolName: "terminal_command", Output: "ok", ExitCode: 0, Status: "ok"}
	}}, nil)
	state := &taskState{task: Task{ID: "multi-task"}}
	for i := 0; i < 7; i++ {
		state.task.Targets = append(state.task.Targets, Target{ID: "target-" + string(rune('a'+i)), SessionID: "session"})
	}
	tool := &multiTargetCommandTool{runtime: runtime, state: state}
	result := tool.executeTargets(context.Background(), Step{TaskID: state.task.ID, ID: "step-1"}, "df -h", state.task.Targets)
	if result.Status != "ok" || maxRunning.Load() > maxTargetConcurrency {
		t.Fatalf("unexpected fan-out result: %#v, max concurrency=%d", result, maxRunning.Load())
	}
	var perTarget []ToolResult
	if err := json.Unmarshal([]byte(result.Output), &perTarget); err != nil || len(perTarget) != len(state.task.Targets) {
		t.Fatalf("per-target results missing: %v %#v", err, perTarget)
	}
}

func TestMultiTargetReadToolKeepsTargetResultsSeparate(t *testing.T) {
	runtime := NewRuntime(nil, nil, ToolSet{SFTPListDir: func(_ context.Context, sessionID, path string) ToolResult {
		return ToolResult{ToolName: "sftp_list_dir", Output: sessionID + ":" + path, ExitCode: 0, Status: "ok"}
	}}, nil)
	state := &taskState{task: Task{ID: "multi-read", Targets: []Target{{ID: "a", SessionID: "session-a"}, {ID: "b", SessionID: "session-b"}}}, ctx: context.Background(), approve: make(chan approvalResult, 1)}
	runtime.mu.Lock()
	runtime.tasks[state.task.ID] = state
	runtime.mu.Unlock()
	tool := &multiTargetReadTool{runtime: runtime, state: state, kind: "sftp_list_dir_many"}
	done := make(chan struct {
		raw string
		err error
	}, 1)
	go func() {
		raw, err := tool.InvokableRun(context.Background(), `{"path":"/var/log"}`)
		done <- struct {
			raw string
			err error
		}{raw, err}
	}()
	stepID := waitForApproval(t, runtime, state)
	if err := runtime.Approve(state.task.ID, stepID, true); err != nil {
		t.Fatal(err)
	}
	completed := <-done
	if completed.err != nil {
		t.Fatal(completed.err)
	}
	raw := completed.raw
	var result ToolResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	var perTarget []ToolResult
	if err := json.Unmarshal([]byte(result.Output), &perTarget); err != nil || len(perTarget) != 2 {
		t.Fatalf("unexpected fan-out output: %v %#v", err, result)
	}
	if perTarget[0].TargetID != "a" || perTarget[1].TargetID != "b" {
		t.Fatalf("target identity was not preserved: %#v", perTarget)
	}
}

func waitForApproval(t *testing.T, runtime *Runtime, state *taskState) string {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		runtime.mu.RLock()
		var stepID string
		if state.task.PendingApproval != nil {
			stepID = state.task.PendingApproval.StepID
		}
		runtime.mu.RUnlock()
		if stepID != "" {
			return stepID
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("SFTP approval was not requested")
	return ""
}

func TestMultiTargetReadToolFansOutRAGAndDiagnostics(t *testing.T) {
	runtime := NewRuntime(nil, nil, ToolSet{
		RAGSearchTarget: func(_ context.Context, targetID, query string, limit int) ToolResult {
			return ToolResult{ToolName: "rag_search", Output: targetID + ":" + query, ExitCode: 0, Status: "ok", Metadata: map[string]any{"limit": limit}}
		},
		Diagnostics: func(_ context.Context) ToolResult {
			return ToolResult{ToolName: "gossh_diagnostics", Output: "local diagnostics", ExitCode: 0, Status: "ok"}
		},
	}, nil)
	state := &taskState{task: Task{ID: "multi-read-scoped", Targets: []Target{{ID: "a", SessionID: "session-a"}, {ID: "b", SessionID: "session-b"}}}}

	for _, test := range []struct {
		kind string
		args string
	}{
		{kind: "rag_search_many", args: `{"query":"certificate","limit":3}`},
		{kind: "gossh_diagnostics_many", args: `{}`},
	} {
		t.Run(test.kind, func(t *testing.T) {
			tool := &multiTargetReadTool{runtime: runtime, state: state, kind: test.kind}
			raw, err := tool.InvokableRun(context.Background(), test.args)
			if err != nil {
				t.Fatal(err)
			}
			var result ToolResult
			if err := json.Unmarshal([]byte(raw), &result); err != nil {
				t.Fatal(err)
			}
			var perTarget []ToolResult
			if err := json.Unmarshal([]byte(result.Output), &perTarget); err != nil || len(perTarget) != 2 {
				t.Fatalf("unexpected fan-out output: %v %#v", err, result)
			}
			byTarget := make(map[string]ToolResult, len(perTarget))
			for _, item := range perTarget {
				byTarget[item.TargetID] = item
			}
			for _, targetID := range []string{"a", "b"} {
				item, ok := byTarget[targetID]
				if !ok || item.Error != "" || item.ExitCode != 0 {
					t.Fatalf("target %s was not isolated: %#v", targetID, perTarget)
				}
			}
		})
	}
}

func TestMultiTargetCommandRejectsUnresolvedTargetTemplate(t *testing.T) {
	runtime := NewRuntime(nil, nil, ToolSet{Terminal: func(context.Context, string, string) ToolResult {
		t.Fatal("unapproved multi-target command reached terminal")
		return ToolResult{}
	}}, nil)
	state := &taskState{task: Task{ID: "template-command", Targets: []Target{{ID: "a", SessionID: "session-a"}}, TargetParameters: map[string]map[string]any{"a": {"path": "/tmp"}}}}
	tool := &multiTargetCommandTool{runtime: runtime, state: state}
	raw, err := tool.InvokableRun(context.Background(), `{"command":"ls {{path}}","purpose":"inspect","risk":"read-only"}`)
	if err != nil {
		t.Fatal(err)
	}
	var result ToolResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if result.ErrorKind != "policy" || result.Status != "rejected" {
		t.Fatalf("unresolved template was not rejected: %#v", result)
	}
}

func TestMultiTargetDiagnosticsDoesNotExecuteRemoteCommandWithoutApproval(t *testing.T) {
	called := 0
	runtime := NewRuntime(nil, nil, ToolSet{Diagnostics: func(context.Context) ToolResult {
		called++
		return ToolResult{Status: "ok"}
	}}, nil)
	state := &taskState{task: Task{ID: "diagnostics-command", Targets: []Target{{ID: "a", SessionID: "session-a"}}}}
	tool := &multiTargetReadTool{runtime: runtime, state: state, kind: "gossh_diagnostics_many"}
	if _, err := tool.InvokableRun(context.Background(), `{}`); err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatalf("local diagnostics call count = %d, want 1", called)
	}
}
