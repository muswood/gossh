// owner: muswood | Email: mumu920@outlook.com
package agent

import "time"

const (
	EventTaskCreated      = "task-created"
	EventPlanning         = "planning"
	EventPlanCreated      = "plan-created"
	EventApprovalRequired = "approval-required"
	EventApprovalResult   = "approval-result"
	EventToolStarted      = "tool-started"
	EventToolOutput       = "tool-output"
	EventToolFinished     = "tool-finished"
	EventReplan           = "replan"
	EventAssistant        = "assistant"
	EventModelDiagnostics = "model-diagnostics"
	EventFinal            = "final"
	EventError            = "error"
	EventCancelled        = "cancelled"
	EventInterrupted      = "interrupted"
	EventPersistenceError = "persistence-error"
)

type Event struct {
	ID        string    `json:"id,omitempty"`
	TaskID    string    `json:"taskId"`
	StepID    string    `json:"stepId,omitempty"`
	Type      string    `json:"type"`
	Payload   any       `json:"payload,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}
