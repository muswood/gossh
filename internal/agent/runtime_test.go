// owner: muswood | Email: mumu920@outlook.com
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"gossh/internal/ai"
)

type flakyCheckpointStore struct {
	err   error
	calls int
}

type recentContextStore struct {
	tasks     []Task
	tabID     string
	limit     int
	listCalls int
}

func (s *recentContextStore) SaveTask(Task) error                      { return nil }
func (s *recentContextStore) SaveStep(Step) error                      { return nil }
func (s *recentContextStore) AppendEvent(Event) error                  { return nil }
func (s *recentContextStore) SaveSnapshot(Task, []Step, []Event) error { return nil }
func (s *recentContextStore) LoadTask(string) (Task, error)            { return Task{}, errors.New("not found") }
func (s *recentContextStore) ListTasks(string) ([]Task, error) {
	s.listCalls++
	return s.tasks, nil
}
func (s *recentContextStore) LoadEvents(string) ([]Event, error) { panic("events must not be loaded") }
func (s *recentContextStore) ListRecentTasks(tabID string, limit int) ([]Task, error) {
	s.tabID, s.limit = tabID, limit
	return s.tasks, nil
}

func (s *flakyCheckpointStore) SaveTask(Task) error                { return nil }
func (s *flakyCheckpointStore) SaveStep(Step) error                { return nil }
func (s *flakyCheckpointStore) AppendEvent(Event) error            { return nil }
func (s *flakyCheckpointStore) LoadTask(string) (Task, error)      { return Task{}, errors.New("not found") }
func (s *flakyCheckpointStore) ListTasks(string) ([]Task, error)   { return nil, nil }
func (s *flakyCheckpointStore) LoadEvents(string) ([]Event, error) { return nil, nil }
func (s *flakyCheckpointStore) SaveSnapshot(Task, []Step, []Event) error {
	s.calls++
	return s.err
}

func TestRuntimeStartRequiresConfiguredClient(t *testing.T) {
	runtime := NewRuntime(nil, nil, ToolSet{}, nil)
	_, err := runtime.Start(context.Background(), StartRequest{Goal: "check disk"})
	if err == nil {
		t.Fatal("expected missing client error")
	}
}

func TestOperationalPersonaUsesRemoteTimeoutOnlyWhenNecessary(t *testing.T) {
	if strings.Contains(operationalPersona, "默认使用超时参数") || strings.Contains(operationalPersona, "限定路径、超时、采样或限速") {
		t.Fatal("operational persona must not ask the model to add remote timeout by default")
	}
	if !strings.Contains(operationalPersona, "非必要不得在终端命令前添加 timeout") {
		t.Fatal("operational persona must require remote timeout only when necessary")
	}
}

func TestRuntimeAcceptsConfiguredMaxStepsThroughFifty(t *testing.T) {
	runtime := NewRuntime(ai.NewClient(ai.Config{Provider: "test", Model: "test", BaseURL: "http://example.test/v1"}), nil, ToolSet{}, nil)
	if _, err := runtime.Start(context.Background(), StartRequest{ID: "max-steps-50", Goal: "check disk", MaxSteps: 50}); err != nil {
		t.Fatal(err)
	}
	task, err := runtime.Get("max-steps-50")
	if err != nil || task.MaxSteps != 50 {
		t.Fatalf("task max steps = %d, err=%v, want 50", task.MaxSteps, err)
	}
	_ = runtime.Stop("max-steps-50")
}

func TestRuntimeFallsBackForMaxStepsAboveFifty(t *testing.T) {
	runtime := NewRuntime(ai.NewClient(ai.Config{Provider: "test", Model: "test", BaseURL: "http://example.test/v1"}), nil, ToolSet{}, nil)
	if _, err := runtime.Start(context.Background(), StartRequest{ID: "too-many", Goal: "check disk", MaxSteps: 51}); err != nil {
		t.Fatal(err)
	}
	task, err := runtime.Get("too-many")
	if err != nil || task.MaxSteps != DefaultMaxIterations {
		t.Fatalf("task max steps = %d, err=%v, want default %d", task.MaxSteps, err, DefaultMaxIterations)
	}
	_ = runtime.Stop("too-many")
}

func TestRuntimeUsesConfiguredDefaultMaxSteps(t *testing.T) {
	runtime := NewRuntime(ai.NewClient(ai.Config{Provider: "test", Model: "test", BaseURL: "http://example.test/v1"}), nil, ToolSet{}, nil)
	runtime.SetDefaultMaxSteps(50)
	if _, err := runtime.Start(context.Background(), StartRequest{ID: "configured-default", Goal: "check disk"}); err != nil {
		t.Fatal(err)
	}
	task, err := runtime.Get("configured-default")
	if err != nil || task.MaxSteps != 50 {
		t.Fatalf("task max steps = %d, err=%v, want 50", task.MaxSteps, err)
	}
	_ = runtime.Stop("configured-default")
}

func TestEinoToolsHideSFTPForNonSSHTask(t *testing.T) {
	runtime := NewRuntime(nil, nil, ToolSet{
		SFTPListDir:  func(context.Context, string, string) ToolResult { return ToolResult{} },
		SFTPReadFile: func(context.Context, string, string) ToolResult { return ToolResult{} },
	}, nil)
	state := &taskState{ctx: context.Background(), task: Task{ID: "telnet-task"}}
	tools := runtime.einoTools(state, StartRequest{SessionID: "telnet-session", Transport: "telnet"})
	for _, candidate := range tools {
		info, err := candidate.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(info.Name, "sftp_") {
			t.Fatalf("non-SSH task exposed SFTP tool %q", info.Name)
		}
	}
}

func TestEinoToolsExposeSFTPForSSHTask(t *testing.T) {
	runtime := NewRuntime(nil, nil, ToolSet{
		SFTPListDir:  func(context.Context, string, string) ToolResult { return ToolResult{} },
		SFTPReadFile: func(context.Context, string, string) ToolResult { return ToolResult{} },
	}, nil)
	state := &taskState{ctx: context.Background(), task: Task{ID: "ssh-task"}}
	tools := runtime.einoTools(state, StartRequest{SessionID: "ssh-session", Transport: "ssh"})
	seen := false
	for _, candidate := range tools {
		info, err := candidate.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		seen = seen || info.Name == "sftp_read_file"
	}
	if !seen {
		t.Fatal("SSH task did not expose sftp_read_file")
	}
}

func TestSSHInstructionRequiresSystemProbeFirst(t *testing.T) {
	instruction := instructionFor(StartRequest{Transport: "ssh", SessionID: "ssh-session", Goal: "检查磁盘"})
	if !strings.Contains(instruction, "首条终端命令必须是 uname -a") {
		t.Fatalf("SSH instruction does not require the system probe first: %s", instruction)
	}
}

func TestSSHInstructionReusesCompletedSessionProbe(t *testing.T) {
	instruction := instructionFor(StartRequest{Transport: "ssh", SessionID: "ssh-session", SystemProbeDone: true, Goal: "检查磁盘"})
	if strings.Contains(instruction, "首条终端命令必须是 uname -a") {
		t.Fatalf("probed SSH session still requires uname -a: %s", instruction)
	}
	if !strings.Contains(instruction, "已识别远端系统") {
		t.Fatalf("probed SSH instruction does not reuse system information: %s", instruction)
	}
}

func TestApproveIdentifiesMissingRunnerChannel(t *testing.T) {
	runtime := NewRuntime(nil, nil, ToolSet{}, nil)
	if err := runtime.Approve("missing-task", "step-1", true); !errors.Is(err, ErrTaskNotRunning) {
		t.Fatalf("Approve error = %v, want ErrTaskNotRunning", err)
	}
}

func TestApproveCancelledRunnerRequiresRecovery(t *testing.T) {
	runtime := NewRuntime(nil, nil, ToolSet{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runtime.tasks["cancelled-task"] = &taskState{
		task: Task{ID: "cancelled-task", Status: StatusWaitingApproval,
			PendingApproval: &Approval{TaskID: "cancelled-task", StepID: "step-1"}},
		ctx:     ctx,
		approve: make(chan approvalResult, 1),
	}
	if err := runtime.Approve("cancelled-task", "step-1", true); !errors.Is(err, ErrTaskNotRunning) {
		t.Fatalf("Approve error = %v, want ErrTaskNotRunning", err)
	}
}

func TestApproveRejectsStaleAndDuplicateDecisions(t *testing.T) {
	runtime := NewRuntime(nil, nil, ToolSet{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	state := &taskState{task: Task{ID: "approval-task", Status: StatusWaitingApproval,
		PendingApproval: &Approval{TaskID: "approval-task", StepID: "step-current"}}, ctx: ctx, approve: make(chan approvalResult, 1)}
	runtime.mu.Lock()
	runtime.tasks[state.task.ID] = state
	runtime.mu.Unlock()

	if err := runtime.Approve(state.task.ID, "step-stale", true); err == nil {
		t.Fatal("stale approval was accepted")
	}
	if err := runtime.Approve(state.task.ID, "step-current", true); err != nil {
		t.Fatalf("current approval rejected: %v", err)
	}
	if err := runtime.Approve(state.task.ID, "step-current", true); err == nil {
		t.Fatal("duplicate approval was accepted")
	}
}

func TestWorkflowEnforcesToolOrderAndCompletion(t *testing.T) {
	runtime := NewRuntime(nil, nil, ToolSet{}, nil)
	state := &taskState{task: Task{ID: "workflow-task", Workflow: []WorkflowStep{
		{ID: "collect", Title: "collect", AllowedTools: []string{"terminal_command"}},
		{ID: "report", Title: "report", AllowedTools: []string{"report"}},
	}}}
	runtime.mu.Lock()
	runtime.tasks[state.task.ID] = state
	runtime.mu.Unlock()
	if err := runtime.workflowBefore(state, "sftp_read_file"); err == nil {
		t.Fatal("workflow allowed an out-of-order tool")
	}
	if err := runtime.workflowBefore(state, "terminal_command"); err != nil {
		t.Fatal(err)
	}
	runtime.workflowAfter(state, "terminal_command")
	if got := state.task.WorkflowIndex; got != 1 {
		t.Fatalf("workflow did not advance: %d", got)
	}
	if err := runtime.workflowBefore(state, "report"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.workflowBefore(state, "terminal_command"); err == nil {
		t.Fatal("workflow allowed a tool after completion")
	}
}

func TestReportTemplateValidation(t *testing.T) {
	report := Report{Title: "Health", Summary: "ok", Severity: "low", Evidence: []ReportEvidence{{ID: "e1"}}, Custom: map[string]any{"owner": "ops"}}
	template := `{"requiredFields":["title","summary","evidence"],"allowedSeverities":["low"],"requiredCustomFields":["owner"]}`
	if err := validateReportTemplate(report, template); err != nil {
		t.Fatal(err)
	}
	report.Custom = nil
	if err := validateReportTemplate(report, template); err == nil {
		t.Fatal("missing custom report field was accepted")
	}
}

func TestMalformedToolArgumentsErrorClassification(t *testing.T) {
	if !isMalformedToolArgumentsError(errors.New("terminal_command 参数无效: unexpected end of JSON input")) {
		t.Fatal("truncated tool arguments were not classified")
	}
	if isMalformedToolArgumentsError(errors.New("API 请求失败: connection reset")) {
		t.Fatal("API failure must not be classified as malformed tool arguments")
	}
}

func TestIterationLimitCompletesFromExistingReportAndDeletesNativeCheckpoint(t *testing.T) {
	store, err := NewSQLiteCheckpointStore(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(nil, nil, ToolSet{}, store)
	now := time.Now()
	report := Report{Title: "Iteration limit report", Summary: "Collected evidence before the model iteration limit.", Severity: "info",
		Evidence: []ReportEvidence{{ID: "evidence-1", ToolName: "sftp_list_dir", Source: "/etc", Output: "hosts", ExitCode: 0}}}
	rawReport, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	state := &taskState{task: Task{ID: "iteration-limit", Goal: "inspect /etc", Status: StatusRunning,
		Steps: []Step{{ID: "report-step", TaskID: "iteration-limit", ToolName: "report", Status: StepCompleted,
			Result: &ToolResult{ToolName: "report", Output: string(rawReport), ExitCode: 0}, CreatedAt: now, UpdatedAt: now}},
		CreatedAt: now, UpdatedAt: now}}
	runtime.tasks[state.task.ID] = state
	checkpointID := einoCheckpointID(state.task.ID)
	if err := store.SetEinoCheckpoint(context.Background(), checkpointID, []byte("depleted checkpoint")); err != nil {
		t.Fatal(err)
	}

	if err := runtime.handleIterationLimit(state, context.Background(), checkpointID, adk.ErrExceedMaxIterations, "", nil, true); err != nil {
		t.Fatalf("handleIterationLimit returned error: %v", err)
	}
	task, err := runtime.Get(state.task.ID)
	if err != nil || task.Status != StatusCompleted || task.Report == nil || task.Report.Title != report.Title {
		t.Fatalf("task = %#v, err=%v", task, err)
	}
	_, exists, err := store.GetEinoCheckpoint(context.Background(), checkpointID)
	if err != nil || exists {
		t.Fatalf("native checkpoint exists=%v err=%v, want deleted", exists, err)
	}
}

func TestResumeDropsNativeCheckpointBeforeStartingFreshLoop(t *testing.T) {
	store, err := NewSQLiteCheckpointStore(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	task := Task{ID: "fresh-recovery", Goal: "inspect evidence", Status: StatusInterrupted, RecoveryCount: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	checkpointID := einoCheckpointID(task.ID)
	if err := store.SetEinoCheckpoint(context.Background(), checkpointID, []byte("stale native state")); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(nil, nil, ToolSet{}, store)
	runtime.SetClient(ai.NewClient(ai.Config{Provider: "test", Model: "test", BaseURL: "http://example.test/v1"}))
	if _, err := runtime.Resume(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, exists, checkpointErr := store.GetEinoCheckpoint(context.Background(), checkpointID)
		if checkpointErr != nil {
			t.Fatal(checkpointErr)
		}
		if !exists {
			_ = runtime.Stop(task.ID)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("native checkpoint was not deleted before fresh loop")
}

func TestIterationLimitWithoutEvidenceProducesHonestLimitedReport(t *testing.T) {
	runtime := NewRuntime(nil, nil, ToolSet{}, nil)
	now := time.Now()
	state := &taskState{task: Task{ID: "iteration-limit-empty", Goal: "inspect host", Status: StatusRunning, CreatedAt: now, UpdatedAt: now}}
	runtime.tasks[state.task.ID] = state

	if err := runtime.handleIterationLimit(state, context.Background(), einoCheckpointID(state.task.ID), adk.ErrExceedMaxIterations, "", nil, true); err != nil {
		t.Fatalf("handleIterationLimit returned error: %v", err)
	}
	task, err := runtime.Get(state.task.ID)
	if err != nil || task.Status != StatusCompleted || task.Report == nil {
		t.Fatalf("task = %#v, err=%v", task, err)
	}
	if len(task.Report.Evidence) != 1 || task.Report.Evidence[0].ToolName != "agent-runtime" {
		t.Fatalf("fallback evidence = %#v", task.Report.Evidence)
	}
	if len(task.Report.Limitations) == 0 || !strings.Contains(task.Report.Limitations[0], "迭代") {
		t.Fatalf("fallback limitations = %#v", task.Report.Limitations)
	}
}

func TestGeneralAgentDoesNotRequireStructuredReport(t *testing.T) {
	generalRequests := []StartRequest{
		{},
		{Mode: "chat"},
		{Mode: "general"},
		{Mode: "autonomous_analysis"},
	}
	for _, req := range generalRequests {
		if requiresStructuredReport(req) {
			t.Fatalf("general request %#v unexpectedly requires report", req)
		}
		instruction := instructionFor(req)
		if !strings.Contains(instruction, "不要求调用 report 工具") {
			t.Fatalf("general instruction requires report: %s", instruction)
		}
	}
	runtime := NewRuntime(nil, nil, ToolSet{}, nil)
	state := &taskState{task: Task{ID: "general-response", Status: StatusRunning}}
	runtime.tasks[state.task.ID] = state
	if err := runtime.completeTaskWithResponse(state, context.Background(), einoCheckpointID(state.task.ID), "这是自然语言回复。", ""); err != nil {
		t.Fatal(err)
	}
	task, err := runtime.Get(state.task.ID)
	if err != nil || task.Status != StatusCompleted || task.Result != "这是自然语言回复。" || task.Report != nil {
		t.Fatalf("general completion = %#v, err=%v", task, err)
	}
}

func TestInstructionIncludesOperationalPersona(t *testing.T) {
	instruction := instructionFor(StartRequest{Mode: "chat", Goal: "排查服务"})
	for _, expected := range []string{
		"Linux 系统运维",
		"Kubernetes/ACK",
		"阿里云 ECS",
		"Nginx/OpenResty",
		"MySQL、PostgreSQL、Redis、MongoDB",
		"默认不得执行任何删除",
		"禁止删除 Namespace",
		"kubectl apply -f",
		"任何修改前先备份",
		"输出变更记录和备份位置",
	} {
		if !strings.Contains(instruction, expected) {
			t.Fatalf("operational persona is missing %q", expected)
		}
	}
}

func TestInstructionIncludesBoundedChronologicalConversationHistory(t *testing.T) {
	history := []ai.Message{
		{Role: "user", Content: strings.Repeat("old", maxConversationHistoryBytes)},
		{Role: "assistant", Content: "retained assistant reply"},
		{Role: "user", Content: "latest user question"},
	}

	instruction := instructionFor(StartRequest{Goal: "answer the latest question", History: history})
	for _, expected := range []string{"assistant: retained assistant reply", "user: latest user question"} {
		if !strings.Contains(instruction, expected) {
			t.Fatalf("instruction missing history entry %q: %s", expected, instruction)
		}
	}
	if strings.Contains(instruction, "oldoldold") {
		t.Fatalf("instruction included history outside the budget: %s", instruction)
	}
	if strings.Index(instruction, "assistant: retained assistant reply") > strings.Index(instruction, "user: latest user question") {
		t.Fatalf("instruction reordered history: %s", instruction)
	}
}

func TestSpecializedAgentRequiresStructuredReport(t *testing.T) {
	specializedRequests := []StartRequest{
		{Mode: "diagnose"},
		{Mode: "terminal_output"},
		{SkillID: "linux-health-check"},
		{ReportTemplate: `{"requiredFields":["summary"]}`},
		{SkillWorkflow: "collect then report"},
		{Workflow: []WorkflowStep{{ID: "report", Title: "Report"}}},
	}
	for _, req := range specializedRequests {
		if !requiresStructuredReport(req) {
			t.Fatalf("specialized request %#v did not require report", req)
		}
		instruction := instructionFor(req)
		if !strings.Contains(instruction, "必须调用 report 工具") {
			t.Fatalf("specialized instruction did not require report: %s", instruction)
		}
	}
}

func TestRuntimeLoadsPersistedTasks(t *testing.T) {
	store := NewFileCheckpointStore(filepath.Join(t.TempDir(), "agent-checkpoints.json"))
	now := time.Now()
	task := Task{ID: "task-1", TabID: "tab-1", Goal: "check disk", Status: StatusFailed, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveTask(task); err != nil {
		t.Fatal(err)
	}

	runtime := NewRuntime(nil, nil, ToolSet{}, store)
	tasks, err := runtime.List("tab-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != task.ID {
		t.Fatalf("unexpected tasks: %#v", tasks)
	}
}

func TestRuntimeMarksStaleTasksInterrupted(t *testing.T) {
	store := NewFileCheckpointStore(filepath.Join(t.TempDir(), "agent-checkpoints.json"))
	now := time.Now()
	task := Task{ID: "stale-task", Goal: "check disk", Status: StatusWaitingApproval,
		PendingApproval: &Approval{TaskID: "stale-task", StepID: "old-step", Command: "systemctl restart nginx"},
		CreatedAt:       now, UpdatedAt: now}
	if err := store.SaveTask(task); err != nil {
		t.Fatal(err)
	}

	runtime := NewRuntime(nil, nil, ToolSet{}, store)
	got, err := runtime.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusInterrupted || got.PendingApproval != nil || len(got.Events) != 1 || got.Events[0].Type != EventInterrupted {
		t.Fatalf("unexpected interrupted task: %#v", got)
	}
	persisted, err := store.LoadTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != StatusInterrupted || persisted.PendingApproval != nil || len(persisted.Events) != 1 {
		t.Fatalf("unexpected persisted task: %#v", persisted)
	}
}

func TestRuntimeMarksInFlightStepsForReplay(t *testing.T) {
	store := NewFileCheckpointStore(filepath.Join(t.TempDir(), "agent-checkpoints.json"))
	now := time.Now()
	task := Task{ID: "stale-step-task", Goal: "check disk", Status: StatusRunning, CreatedAt: now, UpdatedAt: now,
		Steps: []Step{{ID: "step-1", TaskID: "stale-step-task", ToolName: "terminal_command", Status: StepExecuting, LeaseOwner: "old", LeaseUntil: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}}}
	if err := store.SaveSnapshot(task, task.Steps, nil); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(nil, nil, ToolSet{}, store)
	got, err := runtime.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusInterrupted || got.Steps[0].Status != StepFailed || got.Steps[0].Result == nil || got.Steps[0].Result.ErrorKind != "interrupted" {
		t.Fatalf("in-flight step was not marked for replay: %#v", got)
	}
	if got.RecoveryManifest == nil || len(got.RecoveryManifest.ReplayStepIDs) != 1 || got.RecoveryManifest.ReplayStepIDs[0] != "step-1" {
		t.Fatalf("recovery replay boundary was not persisted: %#v", got.RecoveryManifest)
	}
}

func TestResumeContextPreservesCompletedEvidenceAndMarksReplayBoundary(t *testing.T) {
	task := Task{
		ID:      "resume-context",
		Context: "operator context",
		Events:  []Event{{Type: EventToolFinished, StepID: "done-1"}},
		Steps: []Step{
			{ID: "done-1", ToolName: "terminal_command", Status: StepCompleted, Result: &ToolResult{
				ToolName: "terminal_command", Output: "disk usage: 42%", TargetID: "target-a",
			}},
			{ID: "retry-1", ToolName: "terminal_command", Status: StepFailed, Result: &ToolResult{
				ToolName: "terminal_command", Error: "application interrupted", ErrorKind: "interrupted",
			}},
		},
	}
	contextText := resumeContext(task)
	for _, expected := range []string{
		"operator context",
		"tool=terminal_command status=completed targetId=target-a",
		"disk usage: 42%",
		"application interrupted",
		"未完成步骤必须重新执行",
	} {
		if !strings.Contains(contextText, expected) {
			t.Fatalf("resume context does not contain %q: %s", expected, contextText)
		}
	}
}

func TestTrimConversationHistoryKeepsNewestWithinBudget(t *testing.T) {
	history := []ai.Message{
		{Role: "user", Content: strings.Repeat("old", 30000)},
		{Role: "assistant", Content: "recent answer"},
		{Role: "user", Content: "current question"},
	}
	trimmed := trimConversationHistory(history)
	if len(trimmed) != 2 || trimmed[0].Content != "recent answer" || trimmed[1].Content != "current question" {
		t.Fatalf("history did not keep newest complete messages: %#v", trimmed)
	}
}

func TestTrimConversationHistoryTruncatesNewestOversizedMessage(t *testing.T) {
	trimmed := trimConversationHistory([]ai.Message{{Role: "user", Content: strings.Repeat("x", maxConversationHistoryBytes+1)}})
	if len(trimmed) != 1 || len(trimmed[0].Content) > maxConversationHistoryBytes {
		t.Fatalf("oversized history was not bounded: %#v", trimmed)
	}
	if !strings.Contains(trimmed[0].Content, "[truncated]") {
		t.Fatalf("oversized history is missing truncation marker: %q", trimmed[0].Content)
	}
}

func TestConversationTaskContextKeepsRecentSummariesForSameTab(t *testing.T) {
	store, err := NewSQLiteCheckpointStore(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(-10 * time.Minute)
	for i := 0; i < 9; i++ {
		createdAt := now.Add(time.Duration(i) * time.Minute)
		task := Task{
			ID: fmt.Sprintf("task-%d", i), TabID: "terminal-1", Goal: fmt.Sprintf("goal-%d", i), Mode: "chat",
			Context: fmt.Sprintf("context-%d", i), History: []ai.Message{{Role: "user", Content: fmt.Sprintf("history-%d", i)}},
			Status: StatusCompleted, CreatedAt: createdAt, UpdatedAt: createdAt,
			Steps: []Step{{ID: fmt.Sprintf("step-%d", i), TaskID: fmt.Sprintf("task-%d", i), ToolName: "terminal_command", Status: StepCompleted,
				Result: &ToolResult{ToolName: "terminal_command", Output: fmt.Sprintf("tool-output-%d", i)}, CreatedAt: createdAt, UpdatedAt: createdAt}},
			Events: []Event{{ID: fmt.Sprintf("event-%d", i), TaskID: fmt.Sprintf("task-%d", i), Type: EventFinal, Payload: fmt.Sprintf("event-%d", i), Timestamp: createdAt}},
		}
		if i%2 == 0 {
			task.Report = &Report{Summary: fmt.Sprintf("report-%d", i)}
		} else {
			task.Result = fmt.Sprintf("result-%d", i)
		}
		if err := store.SaveSnapshot(task, task.Steps, task.Events); err != nil {
			t.Fatal(err)
		}
	}
	other := Task{ID: "other-tab", TabID: "terminal-2", Goal: "不应泄露", Context: "other-tab-secret-context", Status: StatusCompleted, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveTask(other); err != nil {
		t.Fatal(err)
	}

	runtime := NewRuntime(nil, nil, ToolSet{}, store)
	context := runtime.conversationTaskContext("terminal-1", "next-task")
	for i := 1; i < 9; i++ {
		expected := fmt.Sprintf("goal-%d", i)
		if !strings.Contains(context, expected) {
			t.Fatalf("conversation context missing %q: %s", expected, context)
		}
	}
	for _, expected := range []string{"result-1", "report-2", "result-3", "report-4", "result-5", "report-6", "result-7", "report-8", "tool-output-8"} {
		if !strings.Contains(context, expected) {
			t.Fatalf("conversation context missing %q: %s", expected, context)
		}
	}
	for _, absent := range []string{"goal-0", "history-0", "event-0", "context-1", "history-1", "event-1"} {
		if strings.Contains(context, absent) {
			t.Fatalf("conversation context contains excluded material %q: %s", absent, context)
		}
	}
	if strings.Contains(context, "other-tab-secret-context") || strings.Contains(context, "other-tab") {
		t.Fatalf("conversation context leaked another tab: %s", context)
	}
}

func TestConversationTaskContextUsesRecentTaskStore(t *testing.T) {
	now := time.Now().UTC()
	store := &recentContextStore{tasks: []Task{{ID: "prior", TabID: "terminal-1", Goal: "bounded", Status: StatusCompleted,
		Result: "done", CreatedAt: now, UpdatedAt: now}}}
	runtime := NewRuntime(nil, nil, ToolSet{}, store)
	context := runtime.conversationTaskContext("terminal-1", "next")
	if !strings.Contains(context, "bounded") || !strings.Contains(context, "done") {
		t.Fatalf("recent task context missing bounded task: %s", context)
	}
	if store.tabID != "terminal-1" || store.limit != maxConversationTasks+1 {
		t.Fatalf("recent task request = (%q, %d), want terminal-1 and %d", store.tabID, store.limit, maxConversationTasks+1)
	}
	if store.listCalls != 1 {
		t.Fatalf("conversation context performed full task listings after runtime load: %d", store.listCalls)
	}
}

func TestConversationTaskContextRedactsPersistedSecrets(t *testing.T) {
	runtime := NewRuntime(nil, nil, ToolSet{}, nil)
	now := time.Now()
	runtime.tasks["prior"] = &taskState{task: Task{
		ID: "prior", TabID: "terminal-1", Status: StatusCompleted,
		Result: "token=top-secret-value", CreatedAt: now, UpdatedAt: now,
	}}
	context := runtime.conversationTaskContext("terminal-1", "next")
	if strings.Contains(context, "top-secret-value") {
		t.Fatalf("conversation context contains secret: %s", context)
	}
	if !strings.Contains(context, "token=[REDACTED]") {
		t.Fatalf("conversation context was not redacted: %s", context)
	}
}

func TestConversationTaskContextRedactsEvidenceBeforeTailTruncation(t *testing.T) {
	runtime := NewRuntime(nil, nil, ToolSet{}, nil)
	now := time.Now()
	runtime.tasks["prior"] = &taskState{task: Task{
		ID: "prior", TabID: "terminal-1", Goal: "redact evidence", Status: StatusCompleted, CreatedAt: now, UpdatedAt: now,
		Steps: []Step{{Status: StepCompleted, UpdatedAt: now, Result: &ToolResult{
			Output: "token=" + strings.Repeat("x", maxConversationEvidenceBytes) + "-leaked-secret-tail",
		}}},
	}}
	context := runtime.conversationTaskContext("terminal-1", "next")
	if strings.Contains(context, "leaked-secret-tail") {
		t.Fatalf("context leaked an evidence tail after its secret prefix was truncated: %s", context)
	}
	if !strings.Contains(context, "token=[REDACTED]") {
		t.Fatalf("context did not redact evidence before bounding it: %s", context)
	}
}

func TestConversationTaskContextBoundsEvidenceTail(t *testing.T) {
	now := time.Now()
	runtime := NewRuntime(nil, nil, ToolSet{}, nil)
	runtime.tasks["prior"] = &taskState{task: Task{
		ID: "prior", TabID: "terminal-1", Goal: "evidence cap", Status: StatusCompleted, CreatedAt: now, UpdatedAt: now,
		Steps: []Step{{Status: StepCompleted, UpdatedAt: now, Result: &ToolResult{
			Output: strings.Repeat("x", maxConversationEvidenceBytes+100) + " newest-evidence-tail",
		}}},
	}}
	context := runtime.conversationTaskContext("terminal-1", "next")
	start := strings.Index(context, "  evidence: ")
	if start < 0 {
		t.Fatalf("context missing evidence: %s", context)
	}
	evidence := context[start+len("  evidence: "):]
	if end := strings.IndexByte(evidence, '\n'); end >= 0 {
		evidence = evidence[:end]
	}
	if len(evidence) > maxConversationEvidenceBytes || !strings.Contains(evidence, "newest-evidence-tail") {
		t.Fatalf("evidence tail was not bounded to the newest output: %q", evidence)
	}
}

func TestConversationTaskContextSkipsReportStepEvidence(t *testing.T) {
	runtime := NewRuntime(nil, nil, ToolSet{}, nil)
	now := time.Now()
	runtime.tasks["prior"] = &taskState{task: Task{
		ID: "prior", TabID: "terminal-1", Goal: "use real evidence", Status: StatusCompleted, CreatedAt: now, UpdatedAt: now,
		Steps: []Step{
			{ToolName: "terminal_command", Status: StepCompleted, UpdatedAt: now, Result: &ToolResult{Output: "terminal-evidence"}},
			{ToolName: "report", Status: StepCompleted, UpdatedAt: now.Add(time.Second), Result: &ToolResult{Output: "report-payload-must-not-be-evidence"}},
		},
	}}
	context := runtime.conversationTaskContext("terminal-1", "next")
	if !strings.Contains(context, "terminal-evidence") || strings.Contains(context, "report-payload-must-not-be-evidence") {
		t.Fatalf("context selected report output as prior evidence: %s", context)
	}
}

func TestConversationTaskContextBoundsAggregateAndKeepsNewestTask(t *testing.T) {
	now := time.Now()
	runtime := NewRuntime(nil, nil, ToolSet{}, nil)
	for i := 0; i < maxConversationTasks; i++ {
		createdAt := now.Add(time.Duration(i) * time.Minute)
		runtime.tasks[fmt.Sprintf("task-%d", i)] = &taskState{task: Task{
			ID: fmt.Sprintf("task-%d", i), TabID: "terminal-1", Goal: fmt.Sprintf("task-marker-%d", i), Status: StatusCompleted,
			Result: strings.Repeat("x", maxConversationContextBytes/3) + fmt.Sprintf(" result-marker-%d", i), CreatedAt: createdAt, UpdatedAt: createdAt,
		}}
	}
	context := runtime.conversationTaskContext("terminal-1", "next")
	if len(context) > maxConversationContextBytes {
		t.Fatalf("context exceeds aggregate cap: %d", len(context))
	}
	if !strings.Contains(context, "task-marker-6") || !strings.Contains(context, "task-marker-7") {
		t.Fatalf("context dropped newest tasks: %s", context)
	}
	if strings.Index(context, "task-marker-6") > strings.Index(context, "task-marker-7") {
		t.Fatalf("context is not chronological: %s", context)
	}
}

func TestResumeIncrementsRecoveryGenerationAndPublishesReplan(t *testing.T) {
	store := NewFileCheckpointStore(filepath.Join(t.TempDir(), "agent-checkpoints.json"))
	now := time.Now()
	task := Task{ID: "recover-generation", Goal: "check disk", Status: StatusInterrupted, RecoveryCount: 2,
		CreatedAt: now, UpdatedAt: now, Steps: []Step{{ID: "done", TaskID: "recover-generation", ToolName: "terminal_command",
			Status: StepCompleted, Result: &ToolResult{ToolName: "terminal_command", Output: "ok"}, CreatedAt: now, UpdatedAt: now}}}
	if err := store.SaveSnapshot(task, task.Steps, task.Events); err != nil {
		t.Fatal(err)
	}
	var emitted []Event
	runtime := NewRuntime(nil, func(event Event) { emitted = append(emitted, event) }, ToolSet{}, store)
	runtime.SetClient(ai.NewClient(ai.Config{Provider: "test", Model: "test", BaseURL: "http://example.test/v1"}))
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runtime.Resume(parent, task.ID); err != nil {
		t.Fatal(err)
	}
	got, err := runtime.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RecoveryCount != 3 {
		t.Fatalf("recovery generation = %d, want 3", got.RecoveryCount)
	}
	if len(got.Steps) != 1 || got.Steps[0].ID != "done" {
		t.Fatalf("resume discarded checkpointed steps: %#v", got.Steps)
	}
	var replan *Event
	for i := range emitted {
		if emitted[i].Type == EventReplan {
			replan = &emitted[i]
			break
		}
	}
	if replan == nil {
		t.Fatalf("resume did not publish replan event: %#v", emitted)
	}
	payload, ok := replan.Payload.(map[string]any)
	if !ok || payload["recoveryCount"] != 3 {
		t.Fatalf("unexpected replan payload: %#v", replan.Payload)
	}
}

func TestResumeAfterCurrentProcessCancellationRetainsCheckpoint(t *testing.T) {
	store := NewFileCheckpointStore(filepath.Join(t.TempDir(), "agent-checkpoints.json"))
	now := time.Now()
	task := Task{ID: "cancelled-resume", Goal: "check disk", Status: StatusCancelled, RecoveryCount: 1,
		CreatedAt: now, UpdatedAt: now, Steps: []Step{{ID: "done", TaskID: "cancelled-resume", ToolName: "terminal_command",
			Status: StepCompleted, Result: &ToolResult{ToolName: "terminal_command", Output: "kept"}, CreatedAt: now, UpdatedAt: now}}}
	if err := store.SaveSnapshot(task, task.Steps, task.Events); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(nil, nil, ToolSet{}, store)
	runtime.SetClient(ai.NewClient(ai.Config{Provider: "test", Model: "test", BaseURL: "http://example.test/v1"}))
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runtime.Resume(parent, task.ID); err != nil {
		t.Fatal(err)
	}
	got, err := runtime.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RecoveryCount != 2 || len(got.Steps) != 1 || got.Steps[0].Result == nil || got.Steps[0].Result.Output != "kept" {
		t.Fatalf("cancelled task checkpoint was not retained: %#v", got)
	}
}

func TestRuntimeMarksPersistenceDegradedAndRecovers(t *testing.T) {
	store := &flakyCheckpointStore{err: errors.New("disk full")}
	var emitted []Event
	runtime := NewRuntime(nil, func(event Event) { emitted = append(emitted, event) }, ToolSet{}, store)
	now := time.Now()
	runtime.tasks["task-1"] = &taskState{task: Task{ID: "task-1", Status: StatusRunning, CreatedAt: now, UpdatedAt: now}}

	if err := runtime.saveSnapshot(runtime.tasks["task-1"].task); err == nil {
		t.Fatal("expected checkpoint failure")
	}
	got := runtime.tasks["task-1"].task
	if store.calls != 3 || got.PersistenceState != "degraded" || got.PersistenceFailures != 1 || got.PersistenceError == "" {
		t.Fatalf("persistence degradation not recorded: calls=%d task=%#v", store.calls, got)
	}
	if len(emitted) != 1 || emitted[0].Type != EventPersistenceError {
		t.Fatalf("missing persistence error event: %#v", emitted)
	}

	store.err = nil
	if err := runtime.saveSnapshot(got); err != nil {
		t.Fatal(err)
	}
	got = runtime.tasks["task-1"].task
	if got.PersistenceState != "ok" || got.PersistenceError != "" || got.PersistenceFailures != 0 {
		t.Fatalf("persistence recovery not recorded: %#v", got)
	}
}

func TestStopRequestsEinoCheckpointBeforeCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := false
	runtime := NewRuntime(nil, nil, ToolSet{}, nil)
	runtime.tasks["stop-checkpoint"] = &taskState{
		task: Task{ID: "stop-checkpoint", Status: StatusRunning},
		ctx:  ctx, cancel: cancel,
		einoCancel: func(...adk.AgentCancelOption) (*adk.CancelHandle, bool) {
			called = true
			return nil, true
		},
	}
	if err := runtime.Stop("stop-checkpoint"); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("AgentStop did not request Eino checkpoint-aware cancellation")
	}
	got, err := runtime.Get("stop-checkpoint")
	if err != nil || got.Status != StatusCancelled {
		t.Fatalf("unexpected stopped task: %#v, err=%v", got, err)
	}
}

func TestStopCancelsActiveTerminalSession(t *testing.T) {
	var cancelled []string
	runtime := NewRuntime(nil, nil, ToolSet{
		TerminalCancel: func(_ context.Context, sessionID string) error {
			cancelled = append(cancelled, sessionID)
			return nil
		},
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime.tasks["stop-terminal"] = &taskState{
		task: Task{ID: "stop-terminal", SessionID: "session-1", Status: StatusRunning,
			Targets: []Target{{ID: "target-a", SessionID: "session-a"}, {ID: "target-b", SessionID: "session-b"}},
			Steps:   []Step{{ToolName: "terminal_command_many", Status: StepSubmitted, Arguments: map[string]any{"targetIds": []string{"target-a", "target-b"}}}}},
		ctx: ctx, cancel: cancel,
	}
	if err := runtime.Stop("stop-terminal"); err != nil {
		t.Fatal(err)
	}
	if len(cancelled) != 2 || cancelled[0] != "session-a" || cancelled[1] != "session-b" {
		t.Fatalf("terminal cancellation sessions = %#v, want participating target sessions", cancelled)
	}
}

func TestStopRejectsForeignOwnedTask(t *testing.T) {
	runtime := NewRuntime(nil, nil, ToolSet{}, nil)
	runtime.tasks["foreign-stop"] = &taskState{task: Task{ID: "foreign-stop", Status: StatusOwnedByOther, RunnerOwner: "other-process"}}
	if err := runtime.Stop("foreign-stop"); err == nil {
		t.Fatal("Stop accepted a task owned by another process")
	}
}

func TestFinishStepPreservesSubmittedAsUnconfirmed(t *testing.T) {
	runtime := NewRuntime(nil, nil, ToolSet{}, nil)
	state := &taskState{task: Task{ID: "submitted-task"}}
	runtime.tasks[state.task.ID] = state
	runtime.finishStep(Step{ID: "submitted-step", TaskID: state.task.ID, ToolName: "terminal_command"}, ToolResult{
		ToolName: "terminal_command", Status: "submitted", ExitCode: 0,
		Metadata: map[string]any{"completion": "terminal_output"},
	})
	if got := state.task.Steps[0].Status; got != StepSubmitted {
		t.Fatalf("submitted terminal step status = %q, want %q", got, StepSubmitted)
	}
}
