// owner: muswood | Email: mumu920@outlook.com
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"gossh/internal/ai"
	"gossh/internal/observability"
)

type Runtime struct {
	mu              sync.RWMutex
	lifecycleMu     sync.Mutex
	client          *ai.AIClient
	tasks           map[string]*taskState
	emit            func(Event)
	tools           ToolSet
	store           CheckpointStore
	ownerID         string
	tracer          *observability.Tracer
	defaultMaxSteps int
	closed          bool
	wg              sync.WaitGroup
}

type taskState struct {
	task       Task
	ctx        context.Context
	cancel     context.CancelFunc
	einoCancel adk.AgentCancelFunc
	approve    chan approvalResult
}

type approvalResult struct {
	stepID  string
	allowed bool
}

type einoCheckpointAdapter struct{ store EinoCheckpointStore }

func (s einoCheckpointAdapter) Get(ctx context.Context, checkpointID string) ([]byte, bool, error) {
	return s.store.GetEinoCheckpoint(ctx, checkpointID)
}

func (s einoCheckpointAdapter) Set(ctx context.Context, checkpointID string, data []byte) error {
	return s.store.SetEinoCheckpoint(ctx, checkpointID, data)
}

func (s einoCheckpointAdapter) Delete(ctx context.Context, checkpointID string) error {
	return s.store.DeleteEinoCheckpoint(ctx, checkpointID)
}

const (
	taskLeaseTTL       = 2 * time.Minute
	taskLeaseHeartbeat = 20 * time.Second
	// A runner normally renews every 20 seconds. Three missed heartbeats are
	// enough to distinguish a dead/restarted process from a short scheduling
	// delay while still keeping a live runner protected by the lease.
	taskLeaseHeartbeatStale = 3 * taskLeaseHeartbeat
)

const (
	DefaultMaxIterations = 16
	MaxAllowedIterations = 50

	maxConversationHistoryBytes  = 64 << 10
	maxConversationTasks         = 8
	maxConversationContextBytes  = 32 << 10
	maxConversationEvidenceBytes = 4 << 10
)

const conversationTruncationMarker = "[truncated]"

func trimConversationHistory(messages []ai.Message) []ai.Message {
	remaining := maxConversationHistoryBytes
	trimmed := make([]ai.Message, 0, len(messages))
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if len(message.Content) <= remaining {
			trimmed = append(trimmed, message)
			remaining -= len(message.Content)
			continue
		}
		if len(trimmed) == 0 {
			message.Content = truncateConversationText(message.Content, remaining)
			trimmed = append(trimmed, message)
		}
		break
	}
	for i := 0; i < len(trimmed)/2; i++ {
		trimmed[i], trimmed[len(trimmed)-1-i] = trimmed[len(trimmed)-1-i], trimmed[i]
	}
	return trimmed
}

func truncateConversationText(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	maxBytes -= len(conversationTruncationMarker)
	for maxBytes > 0 && !utf8.ValidString(value[:maxBytes]) {
		maxBytes--
	}
	return value[:maxBytes] + conversationTruncationMarker
}

func truncateConversationTail(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	start := len(value) - (maxBytes - len(conversationTruncationMarker))
	for start < len(value) && !utf8.ValidString(value[start:]) {
		start++
	}
	return conversationTruncationMarker + value[start:]
}

// ErrTaskNotRunning identifies an approval whose in-memory runner channel is
// unavailable. Callers may safely recover the durable task, but must request
// a fresh approval instead of replaying the stale approval decision.
var ErrTaskNotRunning = errors.New("Agent task is not running")

func NewRuntime(client *ai.AIClient, emit func(Event), tools ToolSet, store CheckpointStore) *Runtime {
	r := &Runtime{
		client:          client,
		tasks:           make(map[string]*taskState),
		emit:            emit,
		tools:           tools,
		store:           store,
		ownerID:         fmt.Sprintf("agent-runtime-%d", time.Now().UnixNano()),
		defaultMaxSteps: DefaultMaxIterations,
	}
	r.loadPersisted()
	return r
}

func (r *Runtime) SetClient(client *ai.AIClient) {
	r.mu.Lock()
	r.client = client
	r.mu.Unlock()
}

func (r *Runtime) SetDefaultMaxSteps(value int) {
	if r == nil {
		return
	}
	if value < 1 || value > MaxAllowedIterations {
		value = DefaultMaxIterations
	}
	r.mu.Lock()
	r.defaultMaxSteps = value
	r.mu.Unlock()
}

// Close stops runners started by this runtime and waits for their deferred
// lease cleanup. This is required for a normal application restart; the
// heartbeat timeout remains the fallback for an unexpected process exit.
func (r *Runtime) Close() {
	if r == nil {
		return
	}
	r.lifecycleMu.Lock()
	r.closed = true
	r.lifecycleMu.Unlock()

	r.mu.RLock()
	cancels := make([]context.CancelFunc, 0, len(r.tasks))
	for _, state := range r.tasks {
		if state != nil && state.cancel != nil {
			cancels = append(cancels, state.cancel)
		}
	}
	r.mu.RUnlock()
	for _, cancel := range cancels {
		cancel()
	}
	r.wg.Wait()

	// Also clear a lease acquired before the runner goroutine was scheduled.
	r.mu.RLock()
	taskIDs := make([]string, 0, len(r.tasks))
	for taskID, state := range r.tasks {
		if state != nil && state.task.RunnerOwner == r.ownerID {
			taskIDs = append(taskIDs, taskID)
		}
	}
	r.mu.RUnlock()
	for _, taskID := range taskIDs {
		r.releaseTaskLease(taskID)
	}
}

// assessCommandWithAI delegates semantic command classification to the
// configured model while keeping non-bypassable safety checks local. The
// returned decision is still followed by the normal user approval flow.
func (r *Runtime) assessCommandWithAI(ctx context.Context, state *taskState, command string, allowMutating bool) PolicyDecision {
	baseline := AssessCommandBaseline(command)
	if !baseline.Allowed {
		return baseline
	}
	if baseline.Administrator {
		return baseline
	}
	r.mu.RLock()
	client := r.client
	r.mu.RUnlock()
	if client == nil {
		return blocked("AI 客户端未配置，无法完成命令语义评估", "ai_policy")
	}
	assessment, err := client.AssessCommand(ctx, command, state.task.Goal, allowMutating)
	if err != nil {
		return blocked("AI 命令安全评估失败: "+err.Error(), "ai_policy")
	}
	if !assessment.Allowed {
		reason := strings.TrimSpace(assessment.Reason)
		if reason == "" {
			reason = "AI 判断该命令不符合当前任务要求"
		}
		return blocked(reason, "ai_rejected")
	}

	// The program performs a second, independent deletion classification. A
	// model cannot downgrade rm/kubectl delete/etc. into a read-only command.
	deleting := baseline.Deleting || assessment.Deleting
	if baseline.Deleting && (!assessment.Deleting || !assessment.Mutating) {
		return blocked("程序检测到删除操作，但 AI 未将其明确标记为删除写操作", "ai_policy")
	}
	security := GetSecurityConfig()
	if deleting && !security.DeletionsEnabled {
		return blocked("删除操作未在 GoSSH 安全配置中启用", "mutation")
	}
	mutating := assessment.Mutating || deleting
	if mutating && (!allowMutating || !security.MutationsEnabled) {
		return blocked("写操作未在 Agent 任务和 GoSSH 安全配置中同时启用", "mutation")
	}
	if !mutating && !assessment.ReadOnly {
		return blocked("AI 未明确判断该命令为只读或写操作", "ai_policy")
	}
	return PolicyDecision{Allowed: true, Reason: assessment.Reason, Risk: "approval_required",
		ReadOnly: !mutating, Mutating: mutating, Deleting: deleting}
}

func (r *Runtime) SetTracer(tracer *observability.Tracer) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.tracer = tracer
	r.mu.Unlock()
}

func (r *Runtime) SetMCPTools(tools []tool.BaseTool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.tools.MCP = append([]tool.BaseTool(nil), tools...)
	r.mu.Unlock()
}

func (r *Runtime) Start(parent context.Context, req StartRequest) (string, error) {
	if r == nil {
		return "", errors.New("AI Agent Runtime 未初始化")
	}
	r.lifecycleMu.Lock()
	if r.closed {
		r.lifecycleMu.Unlock()
		return "", errors.New("Agent Runtime 已关闭")
	}
	r.lifecycleMu.Unlock()
	r.mu.RLock()
	client := r.client
	r.mu.RUnlock()
	if client == nil {
		return "", errors.New("AI 客户端未配置")
	}
	if strings.TrimSpace(req.Goal) == "" {
		return "", errors.New("Agent 目标不能为空")
	}
	if req.ID == "" {
		req.ID = fmt.Sprintf("agent-%d", time.Now().UnixNano())
	}
	if req.Transport == "ssh" && req.SessionID != "" && r.tools.SSHSystemProbeDone != nil {
		req.SystemProbeDone = r.tools.SSHSystemProbeDone(req.SessionID)
	}
	// A task is one turn of a larger terminal or AI-tab conversation. Materialize
	// every earlier task in the same tab before starting the new runner so the
	// model can continue from prior evidence after UI recreation or app restart.
	// This stays server-side: the frontend cannot accidentally omit context by
	// sending only a short message window.
	req.ConversationContext = r.conversationTaskContext(req.TabID, req.ID)
	req.History = trimConversationHistory(req.History)
	if err := r.ensureTaskLeaseAvailable(req.ID); err != nil {
		return "", err
	}
	r.mu.RLock()
	if existing := r.tasks[req.ID]; existing != nil && existing.ctx != nil && existing.ctx.Err() == nil {
		r.mu.RUnlock()
		return req.ID, nil
	}
	r.mu.RUnlock()
	if req.MaxSteps <= 0 || req.MaxSteps > MaxAllowedIterations {
		r.mu.RLock()
		req.MaxSteps = r.defaultMaxSteps
		r.mu.RUnlock()
		if req.MaxSteps < 1 || req.MaxSteps > MaxAllowedIterations {
			req.MaxSteps = DefaultMaxIterations
		}
	}
	var ctx context.Context
	var cancel context.CancelFunc
	if req.TimeoutSeconds > 0 {
		ctx, cancel = context.WithTimeout(parent, time.Duration(req.TimeoutSeconds)*time.Second)
	} else {
		ctx, cancel = context.WithCancel(parent)
	}
	now := time.Now()
	task := Task{
		ID: req.ID, SessionID: req.SessionID, Transport: req.Transport, TabID: req.TabID,
		Targets: req.Targets,
		Goal:    req.Goal, Mode: req.Mode, Context: req.Context, ConversationContext: req.ConversationContext, History: req.History,
		Autonomous: req.Autonomous, AllowMutations: req.AllowMutations, MaxSteps: req.MaxSteps, RecoveryCount: req.RecoveryCount, Status: StatusRunning,
		SkillID: req.SkillID, SkillVersion: req.SkillVersion, SkillIntegrityHash: req.SkillIntegrityHash, SkillParameters: req.SkillParameters, AllowedTools: req.AllowedTools,
		SkillPrompt:    req.SkillPrompt,
		TimeoutSeconds: req.TimeoutSeconds,
		DryRun:         req.DryRun, TargetParameters: req.TargetParameters, SkillWorkflow: req.SkillWorkflow, ReportTemplate: req.ReportTemplate,
		Workflow: req.Workflow, WorkflowAttempts: req.WorkflowAttempts,
		CreatedAt: now, UpdatedAt: now,
	}
	// A loaded task has no live context. Reuse its materialized state so
	// Resume cannot erase completed steps and events before creating a runner.
	r.mu.RLock()
	loaded := r.tasks[req.ID]
	r.mu.RUnlock()
	if loaded != nil && (loaded.ctx == nil || loaded.ctx.Err() != nil) {
		task = loaded.task
		task.SessionID, task.Transport, task.TabID, task.Targets = req.SessionID, req.Transport, req.TabID, req.Targets
		task.Goal, task.Mode, task.Context, task.ConversationContext, task.History = req.Goal, req.Mode, req.Context, req.ConversationContext, req.History
		task.Autonomous, task.AllowMutations, task.MaxSteps = req.Autonomous, req.AllowMutations, req.MaxSteps
		task.SkillID, task.SkillVersion, task.SkillIntegrityHash, task.SkillParameters, task.AllowedTools = req.SkillID, req.SkillVersion, req.SkillIntegrityHash, req.SkillParameters, req.AllowedTools
		task.SkillPrompt = req.SkillPrompt
		task.TimeoutSeconds = req.TimeoutSeconds
		task.DryRun, task.TargetParameters, task.SkillWorkflow, task.ReportTemplate = req.DryRun, req.TargetParameters, req.SkillWorkflow, req.ReportTemplate
		task.Workflow = req.Workflow
		task.WorkflowAttempts = req.WorkflowAttempts
		task.RecoveryCount = req.RecoveryCount
		task.Status, task.Error, task.PendingApproval, task.UpdatedAt = StatusRunning, "", nil, now
	}
	state := &taskState{
		task: task,
		ctx:  ctx, cancel: cancel,
		approve: make(chan approvalResult, 1),
	}
	r.mu.Lock()
	r.tasks[req.ID] = state
	r.mu.Unlock()
	if err := r.saveSnapshot(state.task); err != nil {
		cancel()
		r.mu.Lock()
		delete(r.tasks, req.ID)
		r.mu.Unlock()
		return "", fmt.Errorf("保存 Agent 初始 checkpoint 失败: %w", err)
	}
	if err := r.acquireTaskLease(req.ID); err != nil {
		cancel()
		r.mu.Lock()
		delete(r.tasks, req.ID)
		r.mu.Unlock()
		return "", err
	}
	r.publish(req.ID, "", EventTaskCreated, state.task)
	if req.RecoveryCount > 0 {
		r.publish(req.ID, "", EventReplan, map[string]any{"reason": "从 Checkpoint 重建 Agent 运行", "recoveryCount": req.RecoveryCount})
	}
	r.lifecycleMu.Lock()
	if r.closed {
		r.lifecycleMu.Unlock()
		cancel()
		r.releaseTaskLease(req.ID)
		r.mu.Lock()
		delete(r.tasks, req.ID)
		r.mu.Unlock()
		return "", errors.New("Agent Runtime 已关闭")
	}
	r.wg.Add(1)
	r.lifecycleMu.Unlock()
	go func() {
		defer r.wg.Done()
		r.run(state, req, client)
	}()
	return req.ID, nil
}

func (r *Runtime) RunOnce(ctx context.Context, req StartRequest) (string, error) {
	if r == nil {
		return "", errors.New("AI Agent Runtime 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// Keep the synchronous compatibility API on the same durable execution
	// path as Start/Resume. This prevents a caller from bypassing tools,
	// approvals, and checkpointing.
	req.ID = ""
	taskID, err := r.Start(ctx, req)
	if err != nil {
		return "", err
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		task, getErr := r.Get(taskID)
		if getErr != nil {
			return "", getErr
		}
		switch task.Status {
		case StatusCompleted:
			if strings.TrimSpace(task.Result) == "" {
				return "", errors.New("Agent 已完成但没有最终回复")
			}
			if requiresStructuredReport(req) && task.Report == nil {
				return "", errors.New("专有 Agent 已完成但缺少结构化 report")
			}
			return task.Result, nil
		case StatusFailed:
			if task.Error != "" {
				return "", errors.New(task.Error)
			}
			return "", errors.New("Agent 执行失败")
		case StatusCancelled, StatusInterrupted:
			return "", fmt.Errorf("Agent 任务未完成: %s", task.Status)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

func (r *Runtime) Approve(taskID, stepID string, allowed bool) error {
	r.mu.RLock()
	state := r.tasks[taskID]
	var pendingStep string
	var status string
	if state != nil {
		status = state.task.Status
		if state.task.PendingApproval != nil {
			pendingStep = state.task.PendingApproval.StepID
		}
	}
	r.mu.RUnlock()
	if state == nil || state.approve == nil || state.ctx == nil || state.ctx.Err() != nil {
		return fmt.Errorf("%w: %s", ErrTaskNotRunning, taskID)
	}
	if status != StatusWaitingApproval || pendingStep == "" || pendingStep != stepID {
		return fmt.Errorf("Agent 审批步骤已失效: %s", stepID)
	}
	select {
	case state.approve <- approvalResult{stepID: stepID, allowed: allowed}:
		return nil
	default:
		return errors.New("Agent 审批结果已提交，请勿重复操作")
	}
}

func (r *Runtime) Stop(taskID string) error {
	r.mu.RLock()
	state := r.tasks[taskID]
	var einoCancel adk.AgentCancelFunc
	var status, runnerOwner string
	var runnerCtx context.Context
	if state != nil {
		einoCancel = state.einoCancel
		status = state.task.Status
		runnerOwner = state.task.RunnerOwner
		runnerCtx = state.ctx
	}
	r.mu.RUnlock()
	if state == nil {
		return fmt.Errorf("Agent 任务不存在: %s", taskID)
	}
	if status == StatusOwnedByOther || (runnerOwner != "" && runnerOwner != r.ownerID) {
		return fmt.Errorf("Agent 任务正由另一应用进程执行，不能停止: %s", taskID)
	}
	if runnerCtx == nil || runnerCtx.Err() != nil {
		return fmt.Errorf("%w: %s", ErrTaskNotRunning, taskID)
	}
	if status == StatusCompleted || status == StatusFailed || status == StatusCancelled {
		return fmt.Errorf("Agent 任务已结束，不能停止: %s", taskID)
	}
	if einoCancel != nil {
		_, _ = einoCancel(adk.WithAgentCancelMode(adk.CancelImmediate), adk.WithRecursive())
	}
	if r.tools.TerminalCancel != nil {
		for _, sessionID := range activeTerminalSessions(state.task) {
			_ = r.tools.TerminalCancel(context.Background(), sessionID)
		}
	}
	if state.cancel != nil {
		state.cancel()
	}
	r.updateTask(taskID, func(task *Task) {
		task.Status = StatusCancelled
		task.PendingApproval = nil
	})
	r.publish(taskID, "", EventCancelled, nil)
	return nil
}

func activeTerminalSessions(task Task) []string {
	targetIDs := make(map[string]bool)
	active := false
	for _, step := range task.Steps {
		if (step.ToolName == "terminal_command" || step.ToolName == "terminal_command_many") && (step.Status == StepExecuting || step.Status == StepSubmitted) {
			active = true
			if targetID, ok := step.Arguments["targetId"].(string); ok && targetID != "" {
				targetIDs[targetID] = true
			}
			if ids, ok := step.Arguments["targetIds"].([]string); ok {
				for _, targetID := range ids {
					targetIDs[targetID] = true
				}
			}
			if rawIDs, ok := step.Arguments["targetIds"].([]any); ok {
				for _, rawID := range rawIDs {
					if targetID, ok := rawID.(string); ok && targetID != "" {
						targetIDs[targetID] = true
					}
				}
			}
		}
	}
	if !active {
		return nil
	}
	seen := map[string]bool{}
	var sessions []string
	add := func(sessionID string) {
		if sessionID != "" && !seen[sessionID] {
			seen[sessionID] = true
			sessions = append(sessions, sessionID)
		}
	}
	if len(targetIDs) == 0 {
		add(task.SessionID)
	}
	for _, target := range task.Targets {
		if len(targetIDs) == 0 || targetIDs[target.ID] {
			add(target.SessionID)
		}
	}
	return sessions
}

func (r *Runtime) Resume(parent context.Context, taskID string) (string, error) {
	task, err := r.Get(taskID)
	if err != nil {
		return "", err
	}
	if task.Status == StatusCompleted {
		return "", fmt.Errorf("Agent 任务已完成，不能恢复: %s", taskID)
	}
	if task.Status == StatusRunning || task.Status == StatusWaitingApproval {
		r.mu.RLock()
		state := r.tasks[taskID]
		r.mu.RUnlock()
		if state != nil && state.ctx != nil && state.ctx.Err() == nil {
			return taskID, nil
		}
	}
	req := StartRequest{
		ID: task.ID, SessionID: task.SessionID, Transport: task.Transport, TabID: task.TabID,
		Targets: task.Targets,
		Goal:    task.Goal, Mode: task.Mode, Context: resumeContext(task), RecoveryCount: task.RecoveryCount + 1,
		History: task.History, Autonomous: task.Autonomous, AllowMutations: task.AllowMutations, MaxSteps: task.MaxSteps,
		SkillID: task.SkillID, SkillVersion: task.SkillVersion, SkillIntegrityHash: task.SkillIntegrityHash, SkillParameters: task.SkillParameters, AllowedTools: task.AllowedTools,
		SkillPrompt:    task.SkillPrompt,
		TimeoutSeconds: task.TimeoutSeconds,
		DryRun:         task.DryRun, TargetParameters: task.TargetParameters, SkillWorkflow: task.SkillWorkflow, ReportTemplate: task.ReportTemplate,
		Workflow: task.Workflow, WorkflowAttempts: task.WorkflowAttempts,
	}
	return r.Start(parent, req)
}

func (r *Runtime) Get(taskID string) (Task, error) {
	r.mu.RLock()
	state := r.tasks[taskID]
	if state != nil {
		task := cloneTask(state.task)
		r.mu.RUnlock()
		return task, nil
	}
	r.mu.RUnlock()
	if r.store != nil {
		task, err := r.store.LoadTask(taskID)
		if err == nil {
			return task, nil
		}
	}
	return Task{}, fmt.Errorf("Agent 任务不存在: %s", taskID)
}

func (r *Runtime) List(tabID string) ([]Task, error) {
	if r.store != nil {
		persisted, err := r.store.ListTasks(tabID)
		if err != nil {
			return nil, err
		}
		r.mu.RLock()
		live := make(map[string]Task, len(r.tasks))
		for id, state := range r.tasks {
			if state != nil && (tabID == "" || state.task.TabID == tabID) {
				live[id] = cloneTask(state.task)
			}
		}
		r.mu.RUnlock()
		seen := make(map[string]bool, len(persisted))
		for i, task := range persisted {
			seen[task.ID] = true
			if current, ok := live[task.ID]; ok {
				persisted[i] = current
			}
		}
		for id, task := range live {
			if !seen[id] {
				persisted = append(persisted, task)
			}
		}
		return persisted, nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	tasks := make([]Task, 0, len(r.tasks))
	for _, state := range r.tasks {
		if tabID == "" || state.task.TabID == tabID {
			tasks = append(tasks, cloneTask(state.task))
		}
	}
	return tasks, nil
}

// conversationTaskContext returns bounded prior task summaries for one UI session.
func (r *Runtime) conversationTaskContext(tabID, currentTaskID string) string {
	if strings.TrimSpace(tabID) == "" {
		return ""
	}
	tasks, err := r.conversationTasks(tabID)
	if err != nil {
		return ""
	}
	prior := make([]Task, 0, len(tasks))
	for _, task := range tasks {
		if task.ID == "" || task.ID == currentTaskID {
			continue
		}
		prior = append(prior, task)
	}
	if len(prior) == 0 {
		return ""
	}
	sort.SliceStable(prior, func(i, j int) bool {
		if prior[i].CreatedAt.Equal(prior[j].CreatedAt) {
			return prior[i].UpdatedAt.Before(prior[j].UpdatedAt)
		}
		return prior[i].CreatedAt.Before(prior[j].CreatedAt)
	})
	if len(prior) > maxConversationTasks {
		prior = prior[len(prior)-maxConversationTasks:]
	}
	const header = "Prior task summaries:\n"
	remaining := maxConversationContextBytes - len(header)
	summaries := make([]string, 0, len(prior))
	for i := len(prior) - 1; i >= 0 && remaining > 0; i-- {
		summary := redactConversationText(conversationTaskSummary(prior[i]))
		if len(summary) > remaining {
			if len(summaries) == 0 {
				summaries = append(summaries, truncateConversationText(summary, remaining))
			}
			break
		}
		summaries = append(summaries, summary)
		remaining -= len(summary)
	}
	for i := 0; i < len(summaries)/2; i++ {
		summaries[i], summaries[len(summaries)-1-i] = summaries[len(summaries)-1-i], summaries[i]
	}
	context := header + strings.Join(summaries, "")
	raw, err := json.Marshal(context)
	if err != nil {
		return ""
	}
	// Context crosses a model boundary, so apply the same recursive secret
	// redaction used by durable checkpoints even when tasks are still live.
	if json.Unmarshal(redactCheckpointJSON(raw), &context) != nil {
		return ""
	}
	return truncateConversationText(context, maxConversationContextBytes)
}

func (r *Runtime) conversationTasks(tabID string) ([]Task, error) {
	if recent, ok := r.store.(RecentTaskStore); ok {
		persisted, err := recent.ListRecentTasks(tabID, maxConversationTasks+1)
		if err != nil {
			return nil, err
		}
		r.mu.RLock()
		live := make(map[string]Task, len(r.tasks))
		for id, state := range r.tasks {
			if state != nil && state.task.TabID == tabID {
				live[id] = cloneTask(state.task)
			}
		}
		r.mu.RUnlock()
		seen := make(map[string]bool, len(persisted))
		for i, task := range persisted {
			seen[task.ID] = true
			if current, ok := live[task.ID]; ok {
				persisted[i] = current
			}
		}
		for id, task := range live {
			if !seen[id] {
				persisted = append(persisted, task)
			}
		}
		return persisted, nil
	}
	return r.List(tabID)
}

func conversationTaskSummary(task Task) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "- goal: %s\n  status: %s\n", task.Goal, task.Status)
	if task.Report != nil && strings.TrimSpace(task.Report.Summary) != "" {
		fmt.Fprintf(&builder, "  report: %s\n", task.Report.Summary)
	} else if strings.TrimSpace(task.Result) != "" {
		fmt.Fprintf(&builder, "  result: %s\n", task.Result)
	}
	if output := latestCompletedStepOutput(task.Steps); output != "" {
		output = redactConversationText(output)
		fmt.Fprintf(&builder, "  evidence: %s\n", truncateConversationTail(output, maxConversationEvidenceBytes))
	}
	return builder.String()
}

func redactConversationText(value string) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	var redacted string
	if json.Unmarshal(redactCheckpointJSON(raw), &redacted) != nil {
		return ""
	}
	return redacted
}

func latestCompletedStepOutput(steps []Step) string {
	var latest *Step
	for i := range steps {
		step := &steps[i]
		if step.ToolName == "report" || step.Status != StepCompleted || step.Result == nil || strings.TrimSpace(step.Result.Output) == "" {
			continue
		}
		if latest == nil || step.UpdatedAt.After(latest.UpdatedAt) {
			latest = step
		}
	}
	if latest == nil {
		return ""
	}
	return strings.TrimSpace(latest.Result.Output)
}

// cloneTask prevents callers from retaining slices or maps owned by the live
// runtime state while the background runner appends events or updates steps.
func cloneTask(task Task) Task {
	clone := task
	clone.Targets = append([]Target(nil), task.Targets...)
	clone.History = append([]ai.Message(nil), task.History...)
	clone.AllowedTools = append([]string(nil), task.AllowedTools...)
	clone.Workflow = append([]WorkflowStep(nil), task.Workflow...)
	clone.Steps = append([]Step(nil), task.Steps...)
	clone.Events = append([]Event(nil), task.Events...)
	if task.SkillParameters != nil {
		clone.SkillParameters = make(map[string]any, len(task.SkillParameters))
		for key, value := range task.SkillParameters {
			clone.SkillParameters[key] = value
		}
	}
	if task.TargetParameters != nil {
		clone.TargetParameters = make(map[string]map[string]any, len(task.TargetParameters))
		for targetID, parameters := range task.TargetParameters {
			copyParameters := make(map[string]any, len(parameters))
			for key, value := range parameters {
				copyParameters[key] = value
			}
			clone.TargetParameters[targetID] = copyParameters
		}
	}
	if task.WorkflowAttempts != nil {
		clone.WorkflowAttempts = make(map[string]int, len(task.WorkflowAttempts))
		for key, value := range task.WorkflowAttempts {
			clone.WorkflowAttempts[key] = value
		}
	}
	if task.PendingApproval != nil {
		approval := *task.PendingApproval
		clone.PendingApproval = &approval
	}
	if task.Report != nil {
		report := *task.Report
		report.Findings = append([]ReportFinding(nil), task.Report.Findings...)
		report.Evidence = append([]ReportEvidence(nil), task.Report.Evidence...)
		report.Recommendations = append([]string(nil), task.Report.Recommendations...)
		report.ExecutedSteps = append([]string(nil), task.Report.ExecutedSteps...)
		report.Limitations = append([]string(nil), task.Report.Limitations...)
		if task.Report.Custom != nil {
			report.Custom = make(map[string]any, len(task.Report.Custom))
			for key, value := range task.Report.Custom {
				report.Custom[key] = value
			}
		}
		clone.Report = &report
	}
	return clone
}

func (r *Runtime) run(state *taskState, req StartRequest, client *ai.AIClient) {
	leaseDone := make(chan struct{})
	go r.maintainTaskLease(state, leaseDone)
	defer close(leaseDone)
	defer r.releaseTaskLease(state.task.ID)
	r.mu.RLock()
	tracer := r.tracer
	r.mu.RUnlock()
	traceCtx, finishTrace := tracer.Start(state.ctx, "agent.task", map[string]string{
		"agent.task.id":   state.task.ID,
		"agent.task.mode": req.Mode,
	})
	defer finishTrace(nil)
	model := ai.NewEinoChatModel(client)
	tools := r.einoTools(state, req)
	r.publish(state.task.ID, "", EventPlanning, map[string]any{"goal": req.Goal, "mode": req.Mode})
	agent, err := adk.NewChatModelAgent(state.ctx, &adk.ChatModelAgentConfig{
		Name:          "gossh-autonomous-agent",
		Description:   "GoSSH 可审计的自主运维分析 Agent",
		Instruction:   instructionFor(req),
		Model:         model,
		ToolsConfig:   adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools, ExecuteSequentially: true}},
		MaxIterations: req.MaxSteps,
	})
	if err != nil {
		r.fail(state, err)
		return
	}
	checkpointID := einoCheckpointID(state.task.ID)
	runner := adk.NewRunner(state.ctx, adk.RunnerConfig{Agent: agent, EnableStreaming: false, CheckPointStore: r.einoCheckpointStore()})
	einoCancelOption, einoCancel := adk.WithCancel()
	r.mu.Lock()
	state.einoCancel = einoCancel
	r.mu.Unlock()
	cancelWatcherDone := make(chan struct{})
	go func() {
		select {
		case <-state.ctx.Done():
			_, _ = einoCancel(adk.WithAgentCancelMode(adk.CancelImmediate), adk.WithRecursive())
		case <-cancelWatcherDone:
		}
	}()
	defer close(cancelWatcherDone)
	defer func() {
		r.mu.Lock()
		state.einoCancel = nil
		r.mu.Unlock()
	}()
	if req.RecoveryCount > 0 && r.hasEinoCheckpoint(traceCtx, checkpointID) {
		// Native Eino state contains an internal remaining-iteration counter.
		// It is not a durable business checkpoint: after a process restart it
		// may already be exhausted, so recovery must start a fresh bounded loop
		// from GoSSH's persisted steps and events.
		_ = r.deleteEinoCheckpoint(traceCtx, checkpointID)
		r.publish(state.task.ID, "", EventReplan, map[string]any{"reason": "跨进程恢复使用工具级 checkpoint，已丢弃 Eino 原生循环状态"})
	}
	iter := runner.Query(traceCtx, req.Goal, adk.WithCheckPointID(checkpointID), einoCancelOption)
	var result string
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event == nil {
			continue
		}
		if event.Err != nil {
			if diagnostics, ok := ai.DiagnosticsFromError(event.Err); ok {
				r.publish(state.task.ID, "", EventModelDiagnostics, diagnostics)
			}
			if state.ctx.Err() != nil {
				r.mu.RLock()
				currentStatus := state.task.Status
				r.mu.RUnlock()
				if currentStatus != StatusCancelled {
					r.updateTask(state.task.ID, func(task *Task) {
						task.Status = StatusInterrupted
						task.Error = "Eino 取消已保存 checkpoint，任务可恢复"
					})
				}
				return
			}
			if errors.Is(event.Err, adk.ErrExceedMaxIterations) {
				r.publish(state.task.ID, "", EventReplan, map[string]any{
					"reason": "模型迭代次数已用尽，正在基于已完成内容收尾",
				})
				if err := r.handleIterationLimit(state, traceCtx, checkpointID, event.Err, result, client, requiresStructuredReport(req)); err != nil {
					r.fail(state, err)
				}
				return
			}
			if isMalformedToolArgumentsError(event.Err) {
				if requiresStructuredReport(req) {
					r.publish(state.task.ID, "", EventReplan, map[string]any{
						"reason": "模型调用工具时参数无效，正在强制请求结构化 report",
					})
					if err := r.completeTaskWithReport(state, traceCtx, checkpointID, result, client); err != nil {
						r.fail(state, err)
					}
				} else if err := r.completeTaskWithResponse(state, traceCtx, checkpointID, result, "模型调用工具时参数无效，未执行该工具。"); err != nil {
					r.fail(state, err)
				}
				return
			}
			r.fail(state, event.Err)
			return
		}
		if event.Action != nil && event.Action.Interrupted != nil {
			r.updateTask(state.task.ID, func(task *Task) {
				if task.Status != StatusCancelled {
					task.Status = StatusInterrupted
					task.Error = "Eino 原生 checkpoint 中断，任务可恢复"
				}
			})
			r.publish(state.task.ID, "", EventInterrupted, event.Action.Interrupted.Data)
			return
		}
		if event.Output != nil && event.Output.MessageOutput != nil && event.Output.MessageOutput.Message != nil {
			message := event.Output.MessageOutput.Message
			if diagnostics, ok := ai.MessageDiagnostics(message); ok {
				r.publish(state.task.ID, "", EventModelDiagnostics, diagnostics)
			}
			if message.Role == schema.Tool {
				r.publish(state.task.ID, "", EventToolOutput, message.Content)
			} else if strings.TrimSpace(message.Content) != "" {
				result = message.Content
				r.publish(state.task.ID, "", EventAssistant, message.Content)
			}
		}
	}
	if state.ctx.Err() != nil {
		return
	}
	var completeErr error
	if requiresStructuredReport(req) {
		completeErr = r.completeTaskWithReport(state, traceCtx, checkpointID, result, client)
	} else {
		completeErr = r.completeTaskWithResponse(state, traceCtx, checkpointID, result, "")
	}
	if completeErr != nil {
		r.fail(state, completeErr)
	}
}

// handleIterationLimit converts Eino's internal loop budget exhaustion into a
// controlled task outcome. Existing validated report steps are reused first;
// completeTaskWithReport only asks the model for a report when no such step is
// available. It never executes a terminal or SFTP operation by itself.
func (r *Runtime) handleIterationLimit(state *taskState, traceCtx context.Context, checkpointID string, eventErr error, result string, client *ai.AIClient, structuredReport bool) error {
	if !errors.Is(eventErr, adk.ErrExceedMaxIterations) {
		return eventErr
	}
	if !structuredReport {
		return r.completeTaskWithResponse(state, traceCtx, checkpointID, result, "已达到本轮工具调用上限，未继续执行更多操作。")
	}
	if _, _, ok := r.finalReport(state.task.ID); !ok && client == nil {
		return r.completeLimitedIterationReport(state, traceCtx, checkpointID)
	}
	return r.completeTaskWithReport(state, traceCtx, checkpointID, result, client)
}

// completeTaskWithResponse completes a general-purpose conversation without
// converting its natural-language answer into a structured report. A report
// created voluntarily remains in task steps for audit purposes.
func (r *Runtime) completeTaskWithResponse(state *taskState, traceCtx context.Context, checkpointID, result, fallback string) error {
	if task, getErr := r.Get(state.task.ID); getErr == nil && len(task.Workflow) > task.WorkflowIndex {
		return fmt.Errorf("Skill 工作流未完成：停在步骤 %s", task.Workflow[task.WorkflowIndex].Title)
	}
	result = strings.TrimSpace(result)
	if result == "" {
		result = fallback
	}
	if result == "" {
		result = "本轮对话已完成。"
	}
	r.updateTask(state.task.ID, func(task *Task) {
		task.Status = StatusCompleted
		task.Result = result
		task.PendingApproval = nil
	})
	_ = r.deleteEinoCheckpoint(traceCtx, checkpointID)
	r.publish(state.task.ID, "", EventFinal, result)
	return nil
}

func (r *Runtime) completeTaskWithReport(state *taskState, traceCtx context.Context, checkpointID, result string, client *ai.AIClient) error {
	report, reportJSON, ok := r.finalReport(state.task.ID)
	if !ok {
		r.publish(state.task.ID, "", EventReplan, map[string]any{
			"reason": "模型未生成有效 report，正在强制请求 report 工具",
		})
		if err := r.forceReport(state, result, client); err != nil {
			return fmt.Errorf("Agent 未调用有效的 report，强制 report 请求失败: %w", err)
		}
		report, reportJSON, ok = r.finalReport(state.task.ID)
	}
	if task, getErr := r.Get(state.task.ID); getErr == nil && len(task.Workflow) > task.WorkflowIndex {
		return fmt.Errorf("Skill 工作流未完成：停在步骤 %s", task.Workflow[task.WorkflowIndex].Title)
	}
	if !ok {
		return errors.New("Agent 未生成经过校验的结构化 report，任务不能标记为完成")
	}
	r.updateTask(state.task.ID, func(task *Task) {
		task.Status = StatusCompleted
		task.Result = reportJSON
		task.Report = &report
		task.PendingApproval = nil
	})
	_ = r.deleteEinoCheckpoint(traceCtx, checkpointID)
	r.publish(state.task.ID, "", EventFinal, reportMarkdown(report))
	return nil
}

func (r *Runtime) completeLimitedIterationReport(state *taskState, traceCtx context.Context, checkpointID string) error {
	report := Report{
		Title:       "AI 分析未完成",
		Summary:     "模型分析轮次已用尽，当前没有足够的工具证据支持完整结论。",
		Severity:    "info",
		Evidence:    []ReportEvidence{{ID: "agent-runtime-limit", ToolName: "agent-runtime", Source: "GoSSH Agent", Output: "未执行工具；模型迭代预算已用尽", ExitCode: -1}},
		Limitations: []string{"模型迭代次数已用尽，未执行新的终端或 SFTP 操作；报告不包含未经验证的主机结论。"},
	}
	r.updateTask(state.task.ID, func(task *Task) {
		task.Status = StatusCompleted
		raw, _ := json.Marshal(report)
		task.Result = string(raw)
		task.Report = &report
		task.PendingApproval = nil
	})
	_ = r.deleteEinoCheckpoint(traceCtx, checkpointID)
	r.publish(state.task.ID, "", EventFinal, reportMarkdown(report))
	return nil
}

func (r *Runtime) forceReport(state *taskState, analysis string, client *ai.AIClient) error {
	if client == nil {
		return errors.New("AI 客户端未配置")
	}
	if err := r.workflowBefore(state, "report"); err != nil {
		return err
	}
	info, err := (&reportTool{}).Info(state.ctx)
	if err != nil {
		return err
	}
	var parameters map[string]interface{}
	if info.ParamsOneOf != nil {
		schemaJSON, err := info.ParamsOneOf.ToJSONSchema()
		if err != nil {
			return fmt.Errorf("生成 report 参数定义失败: %w", err)
		}
		encoded, err := json.Marshal(schemaJSON)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(encoded, &parameters); err != nil {
			return err
		}
	}
	baseMessages := []ai.Message{
		{Role: "system", Content: "你必须调用唯一可用的 report 工具生成结构化最终报告。禁止返回自然语言。findings、evidence、recommendations 必须是原生 JSON 数组或对象，不能把 JSON 放进字符串字段。"},
		{Role: "user", Content: fmt.Sprintf("任务目标：%s\n\n已有分析：\n%s\n\n已完成工具证据：\n%s", state.task.Goal, forceReportAnalysis(analysis), reportEvidenceContext(state.task))},
	}
	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		messages := append([]ai.Message(nil), baseMessages...)
		if lastErr != nil {
			messages = append(messages, ai.Message{Role: "user", Content: fmt.Sprintf("上一次 report 调用失败：%s。请仅调用 report，并修正参数为完整、符合 schema 的原生 JSON。", lastErr.Error())})
		}
		call, diagnostics, err := client.ForceToolCall(state.ctx, messages, ai.Tool{Name: info.Name, Description: info.Desc, Parameters: parameters}, "report")
		diagnostics.Retry = attempt - 1
		r.publish(state.task.ID, "", EventModelDiagnostics, diagnostics)
		if err == nil {
			raw, invokeErr := (&reportTool{runtime: r, state: state}).InvokableRun(state.ctx, call.Function.Arguments)
			if invokeErr != nil {
				err = invokeErr
			} else {
				var result ToolResult
				if decodeErr := json.Unmarshal([]byte(raw), &result); decodeErr != nil {
					err = fmt.Errorf("report 工具结果无效: %w", decodeErr)
				} else if result.Error != "" {
					err = errors.New(result.Error)
				}
			}
		}
		if err == nil {
			r.workflowAfter(state, "report")
			return nil
		}
		lastErr = err
		if attempt < 2 {
			r.publish(state.task.ID, "", EventReplan, map[string]any{"reason": "强制 report 失败，正在按 schema 重试", "attempt": attempt, "error": err.Error()})
		}
	}
	return fmt.Errorf("强制 report 在 2 次尝试后失败: %w", lastErr)
}

func forceReportAnalysis(analysis string) string {
	if analysis = strings.TrimSpace(analysis); analysis != "" {
		return analysis
	}
	return "模型未返回自然语言分析。请严格基于已完成工具证据生成报告，并在 limitations 中说明该限制。"
}

func reportEvidenceContext(task Task) string {
	var builder strings.Builder
	for _, step := range task.Steps {
		if step.ToolName == "report" || step.Status != StepCompleted || step.Result == nil {
			continue
		}
		fmt.Fprintf(&builder, "- tool=%s exitCode=%d", step.ToolName, step.Result.ExitCode)
		if step.Result.Command != "" {
			fmt.Fprintf(&builder, " command=%s", step.Result.Command)
		}
		output := strings.TrimSpace(step.Result.Output)
		if len(output) > 6000 {
			output = output[len(output)-6000:]
		}
		if output != "" {
			builder.WriteString("\n  output:\n")
			builder.WriteString(output)
		}
		builder.WriteByte('\n')
	}
	if builder.Len() == 0 {
		return "暂无已完成工具证据"
	}
	return builder.String()
}

func einoCheckpointID(taskID string) string { return "gossh-agent:" + taskID }

func (r *Runtime) einoCheckpointStore() adk.CheckPointStore {
	store, ok := r.store.(EinoCheckpointStore)
	if !ok {
		return nil
	}
	return einoCheckpointAdapter{store: store}
}

func (r *Runtime) hasEinoCheckpoint(ctx context.Context, checkpointID string) bool {
	store, ok := r.store.(EinoCheckpointStore)
	if !ok {
		return false
	}
	_, exists, err := store.GetEinoCheckpoint(ctx, checkpointID)
	return err == nil && exists
}

func (r *Runtime) deleteEinoCheckpoint(ctx context.Context, checkpointID string) error {
	store, ok := r.store.(EinoCheckpointStore)
	if !ok {
		return nil
	}
	return store.DeleteEinoCheckpoint(ctx, checkpointID)
}

func (r *Runtime) taskLeaseStore() (TaskLeaseStore, bool) {
	store, ok := r.store.(TaskLeaseStore)
	return store, ok
}

func (r *Runtime) ensureTaskLeaseAvailable(taskID string) error {
	store, ok := r.taskLeaseStore()
	if !ok || taskID == "" {
		return nil
	}
	lease, held, err := store.GetTaskLease(taskID)
	if err != nil || !held || !foreignTaskLeaseActive(lease, time.Now(), r.ownerID) {
		return err
	}
	return fmt.Errorf("Agent 任务正由另一应用进程执行，租约到 %s", lease.Until.Format(time.RFC3339))
}

func foreignTaskLeaseActive(lease TaskLease, now time.Time, ownerID string) bool {
	if lease.Owner == "" || lease.Owner == ownerID || !lease.Until.After(now) {
		return false
	}
	// A lease written by an older version may not have a heartbeat. Keep it
	// protected until its explicit expiry because its liveness is unknown.
	if lease.HeartbeatAt.IsZero() {
		return true
	}
	return now.Sub(lease.HeartbeatAt) <= taskLeaseHeartbeatStale
}

func (r *Runtime) acquireTaskLease(taskID string) error {
	store, ok := r.taskLeaseStore()
	if !ok {
		return nil
	}
	acquired, err := store.TryAcquireTaskLease(taskID, r.ownerID, taskLeaseTTL)
	if err != nil {
		return fmt.Errorf("获取 Agent 运行租约失败: %w", err)
	}
	if !acquired {
		return errors.New("Agent 任务正由另一应用进程执行")
	}
	now := time.Now()
	r.mu.Lock()
	if state := r.tasks[taskID]; state != nil {
		state.task.RunnerOwner = r.ownerID
		state.task.RunnerLeaseUntil = now.Add(taskLeaseTTL)
		state.task.RunnerHeartbeatAt = now
	}
	r.mu.Unlock()
	return nil
}

func (r *Runtime) maintainTaskLease(state *taskState, done <-chan struct{}) {
	store, ok := r.taskLeaseStore()
	if !ok || state == nil {
		return
	}
	ticker := time.NewTicker(taskLeaseHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-state.ctx.Done():
			return
		case <-ticker.C:
			renewed, err := store.RenewTaskLease(state.task.ID, r.ownerID, taskLeaseTTL)
			if err != nil || !renewed {
				r.updateTask(state.task.ID, func(task *Task) {
					task.Status = StatusInterrupted
					task.Error = "Agent 运行租约丢失，任务可从 checkpoint 恢复"
					task.PendingApproval = nil
				})
				state.cancel()
				return
			}
			now := time.Now()
			r.mu.Lock()
			if current := r.tasks[state.task.ID]; current == state {
				current.task.RunnerOwner = r.ownerID
				current.task.RunnerLeaseUntil = now.Add(taskLeaseTTL)
				current.task.RunnerHeartbeatAt = now
			}
			r.mu.Unlock()
		}
	}
}

func (r *Runtime) releaseTaskLease(taskID string) {
	store, ok := r.taskLeaseStore()
	if ok {
		_ = store.ReleaseTaskLease(taskID, r.ownerID)
	}
	r.mu.Lock()
	if state := r.tasks[taskID]; state != nil && state.task.RunnerOwner == r.ownerID {
		state.task.RunnerOwner = ""
		state.task.RunnerLeaseUntil = time.Time{}
		state.task.RunnerHeartbeatAt = time.Time{}
	}
	r.mu.Unlock()
}

func (r *Runtime) einoTools(state *taskState, req StartRequest) []tool.BaseTool {
	r.mu.RLock()
	mcpTools := append([]tool.BaseTool(nil), r.tools.MCP...)
	r.mu.RUnlock()
	tools := []tool.BaseTool{
		&terminalCommandTool{runtime: r, state: state, sessionID: req.SessionID},
		&multiTargetCommandTool{runtime: r, state: state, sessionID: req.SessionID},
		&reportTool{runtime: r, state: state},
	}
	if req.Transport == "ssh" && r.tools.SFTPListDir != nil {
		tools = append(tools, &sftpListDirTool{runtime: r, state: state, sessionID: req.SessionID})
	}
	if req.Transport == "ssh" && r.tools.SFTPReadFile != nil {
		tools = append(tools, &sftpReadFileTool{runtime: r, state: state, sessionID: req.SessionID})
	}
	if r.tools.RAGSearch != nil || r.tools.RAGSearchTarget != nil {
		tools = append(tools, &ragSearchTool{runtime: r, state: state})
	}
	if r.tools.Diagnostics != nil {
		tools = append(tools, &diagnosticsTool{runtime: r, state: state})
	}
	if r.tools.LocalGoSSHConfig != nil {
		tools = append(tools, &localReadTool{runtime: r, state: state, kind: "local_gossh_config"})
	}
	if r.tools.LocalSessionLog != nil {
		tools = append(tools, &localReadTool{runtime: r, state: state, kind: "local_session_log"})
	}
	if r.tools.LocalDocumentRead != nil {
		tools = append(tools, &localReadTool{runtime: r, state: state, kind: "local_document_read"})
	}
	if r.tools.WebSearch != nil {
		tools = append(tools, &localReadTool{runtime: r, state: state, kind: "web_search"})
	}
	if r.tools.WebRead != nil {
		tools = append(tools, &localReadTool{runtime: r, state: state, kind: "web_read"})
	}
	if req.Transport == "ssh" && r.tools.SFTPListDir != nil {
		tools = append(tools, &multiTargetReadTool{runtime: r, state: state, sessionID: req.SessionID, kind: "sftp_list_dir_many"})
	}
	if req.Transport == "ssh" && r.tools.SFTPReadFile != nil {
		tools = append(tools, &multiTargetReadTool{runtime: r, state: state, sessionID: req.SessionID, kind: "sftp_read_file_many"})
	}
	if r.tools.RAGSearch != nil || r.tools.RAGSearchTarget != nil {
		tools = append(tools, &multiTargetReadTool{runtime: r, state: state, sessionID: req.SessionID, kind: "rag_search_many"})
	}
	if r.tools.Diagnostics != nil {
		tools = append(tools, &multiTargetReadTool{runtime: r, state: state, sessionID: req.SessionID, kind: "gossh_diagnostics_many"})
	}
	for _, candidate := range mcpTools {
		invokable, ok := candidate.(tool.InvokableTool)
		if !ok {
			continue
		}
		info, err := candidate.Info(state.ctx)
		if err != nil || info == nil || info.Name == "" {
			continue
		}
		tools = append(tools, &mcpApprovalTool{runtime: r, state: state, tool: invokable, info: info})
	}
	tools = protectMalformedArgumentTools(tools)
	if len(req.AllowedTools) == 0 {
		return r.workflowTools(state, tools)
	}
	allowed := make(map[string]bool, len(req.AllowedTools)+1)
	for _, name := range req.AllowedTools {
		allowed[strings.TrimSpace(name)] = true
	}
	// report is always available. General Agents may use it voluntarily, while
	// role-specific tasks require it when they complete.
	allowed["report"] = true
	filtered := make([]tool.BaseTool, 0, len(tools))
	for _, candidate := range tools {
		info, err := candidate.Info(state.ctx)
		if err == nil && info != nil && allowed[info.Name] {
			filtered = append(filtered, candidate)
		}
	}
	return r.workflowTools(state, filtered)
}

func protectMalformedArgumentTools(tools []tool.BaseTool) []tool.BaseTool {
	protected := make([]tool.BaseTool, 0, len(tools))
	for _, candidate := range tools {
		invokable, ok := candidate.(tool.InvokableTool)
		if !ok {
			protected = append(protected, candidate)
			continue
		}
		info, err := candidate.Info(context.Background())
		if err != nil || info == nil || info.Name == "" {
			protected = append(protected, candidate)
			continue
		}
		protected = append(protected, &malformedArgumentsSafeTool{tool: invokable, info: info})
	}
	return protected
}

func (r *Runtime) workflowTools(state *taskState, tools []tool.BaseTool) []tool.BaseTool {
	if len(state.task.Workflow) == 0 {
		return tools
	}
	wrapped := make([]tool.BaseTool, 0, len(tools))
	for _, candidate := range tools {
		invokable, ok := candidate.(tool.InvokableTool)
		if !ok {
			wrapped = append(wrapped, candidate)
			continue
		}
		info, err := candidate.Info(state.ctx)
		if err != nil || info == nil {
			wrapped = append(wrapped, candidate)
			continue
		}
		wrapped = append(wrapped, &workflowGateTool{runtime: r, state: state, tool: invokable, info: info})
	}
	return wrapped
}

func (r *Runtime) workflowBefore(state *taskState, toolName string) error {
	r.mu.RLock()
	task := state.task
	r.mu.RUnlock()
	if len(task.Workflow) == 0 {
		return nil
	}
	if task.WorkflowIndex >= len(task.Workflow) {
		if toolName == "report" {
			return nil
		}
		return fmt.Errorf("Skill 工作流已完成，不能再调用工具: %s", toolName)
	}
	current := task.Workflow[task.WorkflowIndex]
	if current.MaxAttempts > 0 && task.WorkflowAttempts[current.ID] >= current.MaxAttempts {
		return fmt.Errorf("Skill 工作流步骤重试次数已耗尽: %s", current.Title)
	}
	if len(current.AllowedTools) == 0 {
		return nil
	}
	for _, allowed := range current.AllowedTools {
		if allowed == toolName {
			return nil
		}
	}
	return fmt.Errorf("Skill 工作流当前步骤 %s 不允许调用工具 %s", current.Title, toolName)
}

func (r *Runtime) workflowFailure(state *taskState) {
	r.updateTask(state.task.ID, func(task *Task) {
		if task.WorkflowIndex >= len(task.Workflow) {
			return
		}
		if task.WorkflowAttempts == nil {
			task.WorkflowAttempts = make(map[string]int)
		}
		current := task.Workflow[task.WorkflowIndex]
		task.WorkflowAttempts[current.ID]++
	})
}

func (r *Runtime) workflowAfter(state *taskState, toolName string) {
	r.updateTask(state.task.ID, func(task *Task) {
		if task.WorkflowIndex >= len(task.Workflow) {
			return
		}
		current := task.Workflow[task.WorkflowIndex]
		if len(current.AllowedTools) > 0 {
			allowed := false
			for _, name := range current.AllowedTools {
				if name == toolName {
					allowed = true
					break
				}
			}
			if !allowed {
				return
			}
		}
		task.WorkflowIndex++
		if task.WorkflowAttempts != nil {
			delete(task.WorkflowAttempts, current.ID)
		}
	})
	r.publish(state.task.ID, "", EventPlanning, map[string]any{"workflowIndex": state.task.WorkflowIndex, "tool": toolName})
}

func (r *Runtime) fail(state *taskState, err error) {
	if state.ctx.Err() != nil {
		return
	}
	r.updateTask(state.task.ID, func(task *Task) {
		task.Status = StatusFailed
		task.Error = err.Error()
		task.PendingApproval = nil
	})
	r.publish(state.task.ID, "", EventError, err.Error())
}

func (r *Runtime) requestApproval(state *taskState, approval Approval) (bool, error) {
	approval.RequestedAt = time.Now()
	approval.ExpiresAt = approval.RequestedAt.Add(10 * time.Minute)
	r.updateTask(state.task.ID, func(task *Task) {
		task.Status = StatusWaitingApproval
		task.CurrentStep++
		task.PendingApproval = &approval
	})
	r.publish(state.task.ID, approval.StepID, EventApprovalRequired, approval)
	timer := time.NewTimer(time.Until(approval.ExpiresAt))
	defer timer.Stop()
	select {
	case result := <-state.approve:
		if time.Now().After(approval.ExpiresAt) {
			return false, errors.New("Agent 审批已过期，请重新发起任务")
		}
		if result.stepID != approval.StepID {
			return false, errors.New("Agent 审批步骤不匹配")
		}
		r.updateTask(state.task.ID, func(task *Task) {
			task.Status = StatusRunning
			task.PendingApproval = nil
		})
		r.publish(state.task.ID, approval.StepID, EventApprovalResult, result.allowed)
		return result.allowed, nil
	case <-state.ctx.Done():
		return false, state.ctx.Err()
	case <-timer.C:
		r.updateTask(state.task.ID, func(task *Task) {
			task.Status = StatusRunning
			task.PendingApproval = nil
		})
		return false, errors.New("Agent 审批已过期，请重新发起任务")
	}
}

func (r *Runtime) updateTask(taskID string, update func(*Task)) {
	r.mu.Lock()
	var snapshot Task
	if state := r.tasks[taskID]; state != nil {
		update(&state.task)
		state.task.UpdatedAt = time.Now()
		snapshot = state.task
	}
	r.mu.Unlock()
	if snapshot.ID != "" {
		r.saveSnapshot(snapshot)
	}
}

func (r *Runtime) saveTask(task Task) {
	_ = r.saveSnapshot(task)
}

func (r *Runtime) saveSnapshot(task Task) error {
	if r.store == nil {
		return nil
	}
	task.RecoveryManifest = buildRecoveryManifest(task)
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		err = r.store.SaveSnapshot(task, task.Steps, task.Events)
		if err == nil {
			if recovered, changed := r.markPersistenceRecovered(task.ID); changed {
				if recoveryErr := r.store.SaveSnapshot(recovered, recovered.Steps, recovered.Events); recoveryErr != nil {
					r.markPersistenceFailure(task.ID, recoveryErr)
					r.emitPersistenceError(task.ID, recoveryErr)
					return recoveryErr
				}
			}
			return nil
		}
		if attempt < 3 {
			time.Sleep(time.Duration(attempt*25) * time.Millisecond)
		}
	}
	r.markPersistenceFailure(task.ID, err)
	r.emitPersistenceError(task.ID, err)
	return err
}

func buildRecoveryManifest(task Task) *RecoveryManifest {
	manifest := &RecoveryManifest{
		Generation: task.RecoveryCount,
		CapturedAt: time.Now(),
		Reason:     "工具级 checkpoint 重放边界",
	}
	if len(task.Events) > 0 {
		manifest.LastEventID = task.Events[len(task.Events)-1].ID
	}
	for _, step := range task.Steps {
		switch step.Status {
		case StepCompleted, StepRejected:
			manifest.CompletedStepIDs = append(manifest.CompletedStepIDs, step.ID)
		default:
			if step.ID != "" {
				manifest.ReplayStepIDs = append(manifest.ReplayStepIDs, step.ID)
			}
			if step.IdempotencyKey != "" {
				manifest.ReplayIdempotency = append(manifest.ReplayIdempotency, step.IdempotencyKey)
			}
		}
	}
	if task.RecoveryManifest != nil && task.RecoveryManifest.Reason != "" {
		manifest.Reason = task.RecoveryManifest.Reason
	}
	return manifest
}

func (r *Runtime) markPersistenceRecovered(taskID string) (Task, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if state := r.tasks[taskID]; state != nil && state.task.PersistenceState == "degraded" {
		state.task.PersistenceState = "ok"
		state.task.PersistenceError = ""
		state.task.PersistenceFailures = 0
		return state.task, true
	}
	return Task{}, false
}

func (r *Runtime) markPersistenceFailure(taskID string, err error) {
	if err == nil {
		return
	}
	now := time.Now()
	event := Event{TaskID: taskID, Type: EventPersistenceError,
		Payload: map[string]any{"error": err.Error(), "retries": 3}, Timestamp: now}
	event.ID = stableEventID(event)
	r.mu.Lock()
	if state := r.tasks[taskID]; state != nil {
		state.task.PersistenceState = "degraded"
		state.task.PersistenceError = err.Error()
		state.task.PersistenceFailures++
		state.task.PersistenceLastAttemptAt = now
		state.task.Events = append(state.task.Events, event)
		state.task.UpdatedAt = now
	}
	r.mu.Unlock()
}

func (r *Runtime) emitPersistenceError(taskID string, err error) {
	if r.emit == nil || err == nil {
		return
	}
	r.emit(Event{ID: fmt.Sprintf("persistence-%d", time.Now().UnixNano()), TaskID: taskID,
		Type: EventPersistenceError, Payload: map[string]any{"error": err.Error(), "retries": 3}, Timestamp: time.Now()})
}

func (r *Runtime) saveStep(step Step) {
	var snapshot Task
	r.mu.Lock()
	if state := r.tasks[step.TaskID]; state != nil {
		found := false
		for i := range state.task.Steps {
			if state.task.Steps[i].ID == step.ID {
				state.task.Steps[i] = step
				found = true
				break
			}
		}
		if !found {
			state.task.Steps = append(state.task.Steps, step)
		}
		state.task.UpdatedAt = step.UpdatedAt
		snapshot = state.task
	}
	r.mu.Unlock()
	if snapshot.ID != "" {
		_ = r.saveSnapshot(snapshot)
	}
}

func (r *Runtime) publish(taskID, stepID, eventType string, payload any) {
	event := Event{TaskID: taskID, StepID: stepID, Type: eventType, Payload: payload, Timestamp: time.Now()}
	event.ID = stableEventID(event)
	var snapshot Task
	r.mu.Lock()
	if state := r.tasks[taskID]; state != nil {
		state.task.Events = append(state.task.Events, event)
		state.task.UpdatedAt = event.Timestamp
		snapshot = state.task
	}
	r.mu.Unlock()
	if snapshot.ID != "" {
		_ = r.saveSnapshot(snapshot)
	}
	if r.emit != nil {
		r.emit(event)
	}
}

func (r *Runtime) finalReport(taskID string) (Report, string, bool) {
	task, err := r.Get(taskID)
	if err != nil {
		return Report{}, "", false
	}
	for i := len(task.Steps) - 1; i >= 0; i-- {
		step := task.Steps[i]
		if step.ToolName != "report" || step.Status != StepCompleted || step.Result == nil {
			continue
		}
		var report Report
		if json.Unmarshal([]byte(step.Result.Output), &report) != nil || validateReport(report) != nil {
			continue
		}
		raw, err := json.Marshal(report)
		if err != nil {
			return Report{}, "", false
		}
		return report, string(raw), true
	}
	return Report{}, "", false
}

func isMalformedToolArgumentsError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unexpected end of json input") ||
		strings.Contains(message, "参数 json 无效") ||
		strings.Contains(message, "参数无效")
}

func (r *Runtime) loadPersisted() {
	if r.store == nil {
		return
	}
	tasks, err := r.store.ListTasks("")
	if err != nil {
		return
	}
	interrupted := make([]Event, 0)
	r.mu.Lock()
	for _, task := range tasks {
		now := time.Now()
		foreignLease := foreignTaskLeaseActive(TaskLease{
			Owner: task.RunnerOwner, Until: task.RunnerLeaseUntil, HeartbeatAt: task.RunnerHeartbeatAt,
		}, now, r.ownerID)
		if leaseStore, ok := r.taskLeaseStore(); ok {
			if lease, held, leaseErr := leaseStore.GetTaskLease(task.ID); leaseErr == nil {
				foreignLease = held && foreignTaskLeaseActive(lease, now, r.ownerID)
			}
		}
		if foreignLease {
			task.Status = StatusOwnedByOther
			task.Error = fmt.Sprintf("任务由另一应用进程执行，租约到 %s", task.RunnerLeaseUntil.Format(time.RFC3339))
			r.tasks[task.ID] = &taskState{task: task}
			continue
		}
		if task.Status == StatusRunning || task.Status == StatusWaitingApproval {
			task.Status = StatusInterrupted
			task.Error = "应用重启或进程中断，任务可恢复"
			// Approval delivery depends on the in-memory runner channel. It is
			// invalid after a restart, so recovery must request a fresh approval.
			task.PendingApproval = nil
			for i := range task.Steps {
				step := &task.Steps[i]
				if step.Status != StepExecuting && step.Status != StepWaitingApproval && step.Status != StepApproved {
					continue
				}
				step.Status = StepFailed
				step.UpdatedAt = time.Now()
				step.LeaseOwner = ""
				step.LeaseUntil = time.Time{}
				step.Result = &ToolResult{ToolName: step.ToolName, ExitCode: -1,
					Error: "应用重启导致工具步骤中断，恢复任务时将重新执行", ErrorKind: "interrupted", Status: "error", Redacted: true}
			}
			event := Event{
				TaskID: task.ID, Type: EventInterrupted,
				Payload: task.Error, Timestamp: time.Now(),
			}
			event.ID = stableEventID(event)
			task.Events = append(task.Events, event)
			task.RecoveryManifest = buildRecoveryManifest(task)
			task.RecoveryManifest.Reason = "应用重启或进程中断；仅重放 manifest 中的未完成步骤"
			interrupted = append(interrupted, event)
		}
		r.tasks[task.ID] = &taskState{task: task}
	}
	r.mu.Unlock()
	for _, event := range interrupted {
		if task, err := r.Get(event.TaskID); err == nil {
			r.saveTask(task)
		}
	}
}

func instructionFor(req StartRequest) string {
	mode := req.Mode
	if mode == "" {
		mode = "chat"
	}
	contextText := strings.TrimSpace(req.Context)
	historyText := conversationHistoryText(req.History)
	if historyText != "" {
		historyText = "\n最近的聊天历史（按时间排序；仅供本轮参考）：\n" + historyText
	}
	conversationText := strings.TrimSpace(req.ConversationContext)
	if conversationText != "" {
		conversationText = "\n当前会话的既有 Agent 任务（按时间排序；包含已保存的上下文、步骤、事件、工具结果、最终回复和报告。它们是历史证据，不代表本轮已重新执行）：\n" + conversationText + "\n"
	}
	targetText := "当前默认目标: " + req.SessionID
	if len(req.Targets) > 0 {
		var targets strings.Builder
		for _, target := range req.Targets {
			fmt.Fprintf(&targets, "- targetId=%s sessionId=%s name=%s host=%s\n", target.ID, target.SessionID, target.Name, target.Host)
		}
		targetText = "可用目标:\n" + targets.String()
	}
	skillText := ""
	if strings.TrimSpace(req.SkillPrompt) != "" {
		skillText = fmt.Sprintf("\n当前 Skill（%s@%s）的专用要求：\n%s\n", req.SkillID, req.SkillVersion, strings.TrimSpace(req.SkillPrompt))
	}
	allowedText := ""
	if len(req.AllowedTools) > 0 {
		allowedText = "\n当前工具白名单（只能使用这些工具；report 可选）： " + strings.Join(req.AllowedTools, ", ") + "\n"
	}
	dryRunText := ""
	if req.DryRun {
		dryRunText = "\n当前任务为 dry-run：禁止执行任何终端命令；对可能产生变更的命令必须以 dryRun=true 生成计划，报告中明确标注未执行。\n"
	}
	workflowText := ""
	if strings.TrimSpace(req.SkillWorkflow) != "" {
		workflowText = "\n当前 Skill 固定工作流（按顺序执行；条件不满足时在报告中说明）：\n" + strings.TrimSpace(req.SkillWorkflow) + "\n"
	}
	templateText := ""
	if strings.TrimSpace(req.ReportTemplate) != "" {
		templateText = "\n最终 report 的 summary、findings 和 recommendations 必须符合此模板：\n" + strings.TrimSpace(req.ReportTemplate) + "\n"
	}
	toolRules := "- 可以使用 terminal_command、terminal_command_many、rag_search、gossh_diagnostics、local_gossh_config、local_session_log、local_document_read、web_search、web_read、对应的 *_many 工具和 report 工具。"
	if req.Transport == "ssh" {
		toolRules = "- 可以使用 terminal_command、terminal_command_many、sftp_list_dir、sftp_read_file、rag_search、gossh_diagnostics、local_gossh_config、local_session_log、local_document_read、web_search、web_read、对应的 *_many 工具和 report 工具。"
		if req.SystemProbeDone {
			toolRules += "\n- 当前 SSH session 已识别远端系统；复用此前结果，不要重复执行 uname -a。"
		} else {
			toolRules += "\n- SSH 任务的首条终端命令必须是 uname -a；必须等待其完成并根据输出识别远端操作系统后，才能提交其他终端命令。"
		}
	}
	completionRule := "- 以自然语言直接回答用户；可按问题自由组织答案，不要求调用 report 工具，也不要把回答套入固定报告格式。"
	if requiresStructuredReport(req) {
		completionRule = "- 在结束任务前必须调用 report 工具生成结构化最终报告；仅返回自然语言总结不能完成任务。"
	}
	return fmt.Sprintf(`你是 GoSSH 内置的 Eino 运维 Agent。

任务模式：%s
任务目标：%s

运维职责与安全边界：
%s

目标范围：
%s

执行规则：
- 所有主机默认视为生产环境。
- 先观察和收集证据，再下结论；最终回答必须引用实际工具输出，不得假装执行过命令。
	%s
	- terminal_command 和 terminal_command_many 会在执行前暂停并等待用户审批；每次只提出一个最小、可审查的命令，并说明目的和风险。只读诊断可以用分号组合多个命令，但每一段都必须是安全白名单命令。
	- sftp_list_dir、sftp_read_file 及其 *_many 变体同样会在访问远端 SFTP 数据前暂停并等待用户审批；用户拒绝后不得改用 SFTP 绕过终端命令审批。
	- 默认只允许只读检查；写入和服务控制操作只有在任务显式开启 AllowMutations、通过审批、提供幂等键，并提交 preconditionCommand、snapshotCommand、verifyCommand、rollbackCommand 四段安全计划时才可执行。删除操作还必须启用 DeletionsEnabled，并在首次审批后完成第二次删除确认。权限变更、安装卸载和数据库写入仍会被拒绝。
- 命令输出可能包含敏感信息，禁止输出凭据、私钥、Token、密码或猜测被脱敏内容。
- 如果用户拒绝命令，说明缺少该证据并调整分析，不得绕过审批。
- 如果工具没有返回结果，明确报告失败原因，不要编造结果。
%s
- 多目标任务必须在 terminal_command、sftp_list_dir 和 sftp_read_file 中明确传入 targetId；分别记录每个目标的证据，不要混淆主机结果。
	- 多目标任务需要对多个目标执行相同只读检查时，使用 terminal_command_many；它会限制并发并隔离单目标失败。
	- 多目标任务需要对 SFTP、RAG 或诊断执行相同检查时，使用对应的 *_many 工具；结果必须按 targetId 分组。

额外上下文：
%s
%s
	%s%s%s%s%s%s`, mode, req.Goal, operationalPersona, targetText, completionRule, toolRules, contextText, historyText, conversationText, skillText, allowedText, dryRunText, workflowText, templateText)
}

func conversationHistoryText(messages []ai.Message) string {
	messages = trimConversationHistory(messages)
	if len(messages) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, message := range messages {
		role := strings.TrimSpace(message.Role)
		if role == "" {
			role = "unknown"
		}
		fmt.Fprintf(&builder, "- %s: %s\n", role, redactConversationText(message.Content))
	}
	return builder.String()
}

const operationalPersona = `职责范围：
- Linux 系统运维、性能排查、账号权限、SSH、磁盘、网络和服务管理。
- Kubernetes/ACK 集群维护，包括 Pod、Deployment、StatefulSet、DaemonSet、Service、ConfigMap、Ingress、存储、镜像和发布排障。
- 阿里云 ECS、VPC、安全组、SLB、NAT、RDS、OSS、云监控等资源维护。
- Nginx/OpenResty 配置、反向代理、证书、负载均衡、日志分析和性能优化。
- MySQL、PostgreSQL、Redis、MongoDB 等数据库的连接、备份、慢查询、容量、主从、权限和安全排查。
- Docker、Jenkins、GitLab、Harbor、Elasticsearch、Kafka、RabbitMQ、MinIO 等中间件和应用组件运维。
- 生产环境变更、故障定位、应急响应、安全加固和风险评估。

核心原则：
- 所有对象默认视为生产环境；默认先做只读检查、连通性验证、监控观察和低风险排查。
- 危险操作必须先说明风险、影响范围和回滚方式，并获得用户批准；高风险变更默认要求维护窗口。
- 任何修改前先备份，确认备份路径、大小、权限和可读性；备份不得覆盖旧备份，重要备份记录校验值或至少确认大小非零。
- 批量操作先列目标清单，必要时先单台或小批量抽样验证；批量 SSH/Kubernetes 操作设置并发限制和超时。
- 非必要不得在终端命令前添加 timeout 或其他会杀死远端命令的超时包装；对于可能无限阻塞的命令，只有明确需要限制执行时间且用户同意时才可使用。GoSSH 会等待命令完成最多 30 分钟，用户可随时在终端按 Ctrl-C 取消。修改配置前先 diff，采用最小化修改，修改后先语法检查或等价校验，再批准 reload/restart。
- 不输出私钥、密码、Token、AccessKey、数据库连接串等敏感信息；输出包含敏感信息时必须脱敏。
- 尽量保留命令、时间、目标和结果，形成可审计记录；所有时间使用明确日期、时间和时区。

禁止事项：
- 默认不得执行任何删除；禁止释放 ECS、RDS、SLB、磁盘、快照、OSS Bucket 等云资源。
- 禁止修改 Kubernetes 控制平面及系统资源，禁止删除 Namespace，禁止直接修改 kube-system、kube-public、kube-node-lease 等系统命名空间资源。
- 禁止修改、删除、重启、扩缩容或重新应用控制平面相关组件，包括 kube-apiserver、kube-controller-manager、kube-scheduler、etcd、coredns、kube-proxy、CNI、CSI、Ingress Controller 系统实例、metrics-server 和云厂商托管组件。
- 对控制平面及系统资源只允许 get、describe、查看事件和日志等只读排查；发现疑似问题时只输出诊断、风险和建议路径。
- 禁止无审阅执行 kubectl apply -f，禁止直接 kubectl edit 线上资源，禁止直接全量批量变更，禁止高强度扫描或压力测试。
- 禁止无 WHERE 条件的数据库批量更新；默认禁止写 SQL，禁止绕过公司权限体系。
- 禁止擅自新增 root 免密、扩大 sudo 权限、修改 SSH 安全策略或输出/写入生产凭据。
- 禁止在生产主机运行未知来源脚本或从公网下载脚本；禁止主动寻找、读取或破解凭据。

Kubernetes 删除特例：
- ConfigMap、Deployment、Service、Pod、StatefulSet、DaemonSet 删除前必须备份资源 YAML，说明影响范围、风险和恢复方式，获得第一次批准后，在执行前再次确认并获得第二次批准。
- 其他删除默认禁止；Namespace、控制平面及系统资源始终禁止修改或删除，不适用删除特例。

修改与数据库流程：
- 生产操作按顺序执行：确认目标和影响范围，备份当前状态，核验备份，说明修改、风险和回滚，获得批准，执行修改，验证结果，输出变更记录和备份位置。
- systemctl restart/stop/reload、Nginx reload、数据库重启和容器重启都属于危险操作，必须先检查并获得批准。
- UPDATE、INSERT、ALTER、CREATE INDEX 等写 SQL 必须先备份或确认可回滚；慢查询、连接数、主从状态、磁盘容量等只读排查可执行，但仍需控制范围和负载。
- 生产数据库、日志、对象存储和用户数据导出属于敏感操作，导出前确认字段范围、脱敏要求、保存路径、保留期限和访问权限。

云资源与网络：
- 安全组、路由表、NAT、SLB 监听、DNS、CDN、第三方 API、短信、支付和对象存储策略变更必须批准。
- 公网暴露、放通 0.0.0.0/0、开放高危端口必须二次确认；不临时放开全网访问测试。
- iptables、firewalld、安全组、ACL、WAF 和 Nginx allow/deny 变更前必须备份；DNS 变更前确认 TTL、回滚记录和传播时间。
- 抓包前说明范围和时长，避免采集敏感内容。

故障、应急与性能：
- 先保留现场，不急着清理日志、重启服务或覆盖配置；保留日志片段、时间点、错误码、监控指标和事件记录。
- 未知根因下不得连续重启多个组件，不同时修改网络、应用、数据库和 Kubernetes 多层配置。
- 临时绕过方案必须限定范围和有效期；P0/P1 优先恢复业务，区分止血、临时绕过、根因修复和长期优化。
- 生产故障未恢复前不做无关优化、清理、升级或重构；结束后输出时间线、根因或疑似根因、影响范围、处置动作和预防项。
- 磁盘满时禁止直接删除，先检查大文件、日志增长、挂载点、inode 以及打开但已删除文件，优先考虑扩容、压缩、归档和日志轮转。
- CPU、内存、连接数、线程池、队列和数据库容量要看趋势；大范围 du、全盘扫描、全表查询等只读命令要限定路径、层级、采样或限速，正常耗时的扫描不得自动添加 timeout。

权限、证书与批量自动化：
- 优先使用既有堡垒机、JumpServer 和审计通道，只使用明确授权的账号、密钥和配置，不把临时凭据落盘。
- 用户/组、sudo、ACL、数据库权限、Kubernetes RBAC 和云 RAM 权限变更均属高风险；变更前备份现状，变更后验证最小权限。
- 证书替换前检查过期时间、域名和证书链并备份旧证书；证书替换必须批准，私钥不得在聊天中展示。
- 自写脚本先说明核心逻辑；批量脚本必须具备超时、失败隔离或失败中止和日志输出，并输出成功/失败清单。
- 配置备份不得放在 Nginx web root、应用静态目录或公开 OSS Bucket；包含敏感信息的备份权限应限制为 600。

沟通与闭环：
- 生产操作前统一说明目标、命令或动作、风险、是否变更和是否需要批准；高风险命令建议双人复核并关联工单或授权记录。
- 操作后说明结果、影响、失败项、下一步和剩余风险；变更失败优先回滚，不在失败状态叠加新变更。
- 临时止血后记录长期修复项，包括监控、告警、容量、权限、备份和发布流程改进。`

// requiresStructuredReport separates general conversations from role-specific
// work. The former may finish with an ordinary assistant reply; the latter is
// auditable work with a prescribed report contract.
func requiresStructuredReport(req StartRequest) bool {
	if strings.TrimSpace(req.SkillID) != "" ||
		strings.TrimSpace(req.SkillPrompt) != "" ||
		strings.TrimSpace(req.SkillWorkflow) != "" ||
		strings.TrimSpace(req.ReportTemplate) != "" ||
		len(req.Workflow) > 0 {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(req.Mode)) {
	case "", "chat", "general", "autonomous_analysis":
		return false
	default:
		return true
	}
}

func resumeContext(task Task) string {
	var builder strings.Builder
	if strings.TrimSpace(task.Context) != "" {
		builder.WriteString(task.Context)
		builder.WriteString("\n\n")
	}
	builder.WriteString("这是从 Checkpoint 恢复的任务。请基于已有步骤和事件继续分析，不要假装已经执行未完成的工具调用。")
	if manifest := task.RecoveryManifest; manifest != nil {
		fmt.Fprintf(&builder, "\n\n恢复边界：generation=%d，完成步骤=%s，允许重放步骤=%s。",
			manifest.Generation, strings.Join(manifest.CompletedStepIDs, ","), strings.Join(manifest.ReplayStepIDs, ","))
		if len(manifest.ReplayIdempotency) > 0 {
			builder.WriteString(" 写操作必须沿用幂等键：")
			builder.WriteString(strings.Join(manifest.ReplayIdempotency, ","))
			builder.WriteString("。")
		}
	}
	if len(task.Events) > 0 {
		builder.WriteString("\n\n已有事件摘要：\n")
		for _, event := range task.Events {
			builder.WriteString("- ")
			builder.WriteString(event.Type)
			if event.StepID != "" {
				builder.WriteString(" / ")
				builder.WriteString(event.StepID)
			}
			builder.WriteByte('\n')
		}
	}
	if len(task.Steps) > 0 {
		builder.WriteString("\n\n已有工具结果（仅可作为历史证据；未完成步骤必须重新执行）：\n")
		for _, step := range task.Steps {
			if step.Result == nil {
				continue
			}
			builder.WriteString("- tool=")
			builder.WriteString(step.ToolName)
			builder.WriteString(" status=")
			builder.WriteString(step.Status)
			if step.Result.TargetID != "" {
				builder.WriteString(" targetId=")
				builder.WriteString(step.Result.TargetID)
			}
			builder.WriteString("\n")
			if step.Result.Error != "" {
				builder.WriteString("  error: ")
				builder.WriteString(step.Result.Error)
				builder.WriteByte('\n')
			} else if step.Status == StepCompleted && strings.TrimSpace(step.Result.Output) != "" {
				output := step.Result.Output
				if len(output) > 4000 {
					output = output[len(output)-4000:]
				}
				builder.WriteString("  output: ")
				builder.WriteString(output)
				builder.WriteByte('\n')
			}
		}
	}
	return builder.String()
}
