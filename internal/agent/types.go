// owner: muswood | Email: mumu920@outlook.com
package agent

import (
	"time"

	"gossh/internal/ai"
)

const (
	StatusRunning         = "running"
	StatusWaitingApproval = "waiting_approval"
	StatusCompleted       = "completed"
	StatusFailed          = "failed"
	StatusCancelled       = "cancelled"
	StatusInterrupted     = "interrupted"
	StatusOwnedByOther    = "owned_by_other_process"
)

const (
	StepCreated         = "created"
	StepWaitingApproval = "waiting_approval"
	StepApproved        = "approved"
	StepRejected        = "rejected"
	StepExecuting       = "executing"
	StepSubmitted       = "submitted"
	StepCompleted       = "completed"
	StepFailed          = "failed"
)

type StartRequest struct {
	ID                  string                    `json:"id"`
	SessionID           string                    `json:"sessionId"`
	Transport           string                    `json:"transport,omitempty"`
	SystemProbeDone     bool                      `json:"-"`
	TabID               string                    `json:"tabId"`
	Targets             []Target                  `json:"targets,omitempty"`
	Goal                string                    `json:"goal"`
	Mode                string                    `json:"mode"`
	Context             string                    `json:"context"`
	ConversationContext string                    `json:"-"`
	History             []ai.Message              `json:"history"`
	Autonomous          bool                      `json:"autonomous"`
	AllowMutations      bool                      `json:"allowMutations"`
	MaxSteps            int                       `json:"maxSteps"`
	RecoveryCount       int                       `json:"recoveryCount,omitempty"`
	SkillID             string                    `json:"skillId,omitempty"`
	SkillVersion        string                    `json:"skillVersion,omitempty"`
	SkillIntegrityHash  string                    `json:"skillIntegrityHash,omitempty"`
	SkillPrompt         string                    `json:"skillPrompt,omitempty"`
	SkillParameters     map[string]any            `json:"skillParameters,omitempty"`
	AllowedTools        []string                  `json:"allowedTools,omitempty"`
	TimeoutSeconds      int                       `json:"timeoutSeconds,omitempty"`
	DryRun              bool                      `json:"dryRun,omitempty"`
	TargetParameters    map[string]map[string]any `json:"targetParameters,omitempty"`
	SkillWorkflow       string                    `json:"skillWorkflow,omitempty"`
	ReportTemplate      string                    `json:"reportTemplate,omitempty"`
	Workflow            []WorkflowStep            `json:"workflow,omitempty"`
	WorkflowAttempts    map[string]int            `json:"workflowAttempts,omitempty"`
}

type Target struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Name      string `json:"name,omitempty"`
	Host      string `json:"host,omitempty"`
}

type WorkflowStep struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	When         string   `json:"when,omitempty"`
	Prompt       string   `json:"prompt"`
	AllowedTools []string `json:"allowedTools,omitempty"`
	Repeat       int      `json:"repeat,omitempty"`
	MaxAttempts  int      `json:"maxAttempts,omitempty"`
}

type Task struct {
	ID                       string                    `json:"id"`
	SessionID                string                    `json:"sessionId"`
	Transport                string                    `json:"transport,omitempty"`
	TabID                    string                    `json:"tabId"`
	Targets                  []Target                  `json:"targets,omitempty"`
	Goal                     string                    `json:"goal"`
	Mode                     string                    `json:"mode"`
	Context                  string                    `json:"context,omitempty"`
	ConversationContext      string                    `json:"conversationContext,omitempty"`
	History                  []ai.Message              `json:"history,omitempty"`
	Autonomous               bool                      `json:"autonomous"`
	AllowMutations           bool                      `json:"allowMutations"`
	MaxSteps                 int                       `json:"maxSteps"`
	RecoveryCount            int                       `json:"recoveryCount,omitempty"`
	SkillID                  string                    `json:"skillId,omitempty"`
	SkillVersion             string                    `json:"skillVersion,omitempty"`
	SkillIntegrityHash       string                    `json:"skillIntegrityHash,omitempty"`
	SkillPrompt              string                    `json:"skillPrompt,omitempty"`
	SkillParameters          map[string]any            `json:"skillParameters,omitempty"`
	AllowedTools             []string                  `json:"allowedTools,omitempty"`
	TimeoutSeconds           int                       `json:"timeoutSeconds,omitempty"`
	DryRun                   bool                      `json:"dryRun,omitempty"`
	TargetParameters         map[string]map[string]any `json:"targetParameters,omitempty"`
	SkillWorkflow            string                    `json:"skillWorkflow,omitempty"`
	ReportTemplate           string                    `json:"reportTemplate,omitempty"`
	Workflow                 []WorkflowStep            `json:"workflow,omitempty"`
	WorkflowIndex            int                       `json:"workflowIndex,omitempty"`
	WorkflowAttempts         map[string]int            `json:"workflowAttempts,omitempty"`
	RunnerOwner              string                    `json:"runnerOwner,omitempty"`
	RunnerLeaseUntil         time.Time                 `json:"runnerLeaseUntil,omitempty"`
	RunnerHeartbeatAt        time.Time                 `json:"runnerHeartbeatAt,omitempty"`
	Status                   string                    `json:"status"`
	CurrentStep              int                       `json:"currentStep"`
	PendingApproval          *Approval                 `json:"pendingApproval,omitempty"`
	Result                   string                    `json:"result,omitempty"`
	Report                   *Report                   `json:"report,omitempty"`
	Error                    string                    `json:"error,omitempty"`
	PersistenceState         string                    `json:"persistenceState,omitempty"`
	PersistenceError         string                    `json:"persistenceError,omitempty"`
	PersistenceFailures      int                       `json:"persistenceFailures,omitempty"`
	PersistenceLastAttemptAt time.Time                 `json:"persistenceLastAttemptAt,omitempty"`
	Steps                    []Step                    `json:"steps,omitempty"`
	Events                   []Event                   `json:"events,omitempty"`
	RecoveryManifest         *RecoveryManifest         `json:"recoveryManifest,omitempty"`
	CreatedAt                time.Time                 `json:"createdAt"`
	UpdatedAt                time.Time                 `json:"updatedAt"`
}

// RecoveryManifest records the durable replay boundary for crashes where an
// Eino iterator cannot be restored. Completed steps are evidence only; only
// the listed incomplete steps may be replayed by a recovered runner.
type RecoveryManifest struct {
	Generation        int       `json:"generation"`
	CapturedAt        time.Time `json:"capturedAt"`
	LastEventID       string    `json:"lastEventId,omitempty"`
	CompletedStepIDs  []string  `json:"completedStepIds,omitempty"`
	ReplayStepIDs     []string  `json:"replayStepIds,omitempty"`
	ReplayIdempotency []string  `json:"replayIdempotencyKeys,omitempty"`
	Reason            string    `json:"reason,omitempty"`
}

type Step struct {
	ID             string         `json:"id"`
	TaskID         string         `json:"taskId"`
	Kind           string         `json:"kind"`
	ToolName       string         `json:"toolName,omitempty"`
	Arguments      map[string]any `json:"arguments,omitempty"`
	Purpose        string         `json:"purpose,omitempty"`
	Risk           string         `json:"risk,omitempty"`
	Status         string         `json:"status"`
	Approved       *bool          `json:"approved,omitempty"`
	Result         *ToolResult    `json:"result,omitempty"`
	StartedAt      time.Time      `json:"startedAt,omitempty"`
	FinishedAt     time.Time      `json:"finishedAt,omitempty"`
	CreatedAt      time.Time      `json:"createdAt"`
	UpdatedAt      time.Time      `json:"updatedAt"`
	IdempotencyKey string         `json:"idempotencyKey,omitempty"`
	LeaseOwner     string         `json:"leaseOwner,omitempty"`
	LeaseUntil     time.Time      `json:"leaseUntil,omitempty"`
	HeartbeatAt    time.Time      `json:"heartbeatAt,omitempty"`
	Attempt        int            `json:"attempt,omitempty"`
	TimeoutMillis  int64          `json:"timeoutMillis,omitempty"`
	MutationPlan   *MutationPlan  `json:"mutationPlan,omitempty"`
}

// MutationPlan records optional safety commands for the user to review. The
// runtime never sends them automatically: each terminal write needs its own
// approval card.
type MutationPlan struct {
	PreconditionCommand string `json:"preconditionCommand"`
	SnapshotCommand     string `json:"snapshotCommand"`
	VerifyCommand       string `json:"verifyCommand"`
	RollbackCommand     string `json:"rollbackCommand"`
}

type Approval struct {
	TaskID        string    `json:"taskId"`
	StepID        string    `json:"stepId"`
	ToolName      string    `json:"toolName"`
	Command       string    `json:"command"`
	Purpose       string    `json:"purpose"`
	Risk          string    `json:"risk"`
	ApprovalLevel int       `json:"approvalLevel,omitempty"`
	ExpiresAt     time.Time `json:"expiresAt,omitempty"`
	RequestedAt   time.Time `json:"requestedAt,omitempty"`
	RequestedBy   string    `json:"requestedBy,omitempty"`
}

type ToolResult struct {
	ToolName       string         `json:"toolName"`
	Command        string         `json:"command,omitempty"`
	Output         string         `json:"output,omitempty"`
	ExitCode       int            `json:"exitCode"`
	DurationMillis int64          `json:"durationMillis"`
	Attempts       int            `json:"attempts,omitempty"`
	Error          string         `json:"error,omitempty"`
	Redacted       bool           `json:"redacted"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	Status         string         `json:"status,omitempty"`
	ErrorKind      string         `json:"errorKind,omitempty"`
	TimedOut       bool           `json:"timedOut,omitempty"`
	Cancelled      bool           `json:"cancelled,omitempty"`
	TargetID       string         `json:"targetId,omitempty"`
}

type Report struct {
	Title           string           `json:"title"`
	Summary         string           `json:"summary"`
	Severity        string           `json:"severity"`
	Findings        []ReportFinding  `json:"findings,omitempty"`
	Evidence        []ReportEvidence `json:"evidence,omitempty"`
	Recommendations []string         `json:"recommendations,omitempty"`
	ExecutedSteps   []string         `json:"executedSteps,omitempty"`
	Limitations     []string         `json:"limitations,omitempty"`
	Custom          map[string]any   `json:"custom,omitempty"`
	GeneratedAt     time.Time        `json:"generatedAt"`
}

type ReportFinding struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Severity    string   `json:"severity,omitempty"`
	EvidenceIDs []string `json:"evidenceIds,omitempty"`
}

type ReportEvidence struct {
	ID       string `json:"id"`
	ToolName string `json:"toolName,omitempty"`
	StepID   string `json:"stepId,omitempty"`
	TargetID string `json:"targetId,omitempty"`
	Command  string `json:"command,omitempty"`
	Source   string `json:"source,omitempty"`
	Output   string `json:"output,omitempty"`
	ExitCode int    `json:"exitCode,omitempty"`
}
