// owner: muswood | Email: mumu920@outlook.com
package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gossh/internal/ai"
)

const agentRecoveryHelperEnv = "GOSSH_AGENT_PROCESS_HELPER"

// TestAgentProcessRestartRecovery launches the same test binary as a helper
// process. The helper exits immediately after SQLite commits its checkpoint,
// so the parent validates recovery from a different process boundary.
func TestAgentProcessRestartRecovery(t *testing.T) {
	if mode := os.Getenv(agentRecoveryHelperEnv); mode != "" {
		runAgentRecoveryHelper(t, mode)
		return
	}

	t.Run("expired lease is replayable", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "agent.db")
		runAgentRecoveryHelperProcess(t, dbPath, "expired")

		store, err := NewSQLiteCheckpointStore(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		runtime := NewRuntime(nil, nil, ToolSet{}, store)
		got, err := runtime.Get("process-recovery-task")
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != StatusInterrupted {
			t.Fatalf("status = %q, want interrupted", got.Status)
		}
		if len(got.Steps) != 2 {
			t.Fatalf("step count = %d, want 2: %#v", len(got.Steps), got.Steps)
		}
		if got.Steps[0].ID != "completed-before-crash" || got.Steps[0].Status != StepCompleted {
			t.Fatalf("completed step was changed or duplicated: %#v", got.Steps)
		}
		replayed := got.Steps[1]
		if replayed.ID != "in-flight-before-crash" || replayed.Status != StepFailed || replayed.Result == nil || replayed.Result.ErrorKind != "interrupted" {
			t.Fatalf("in-flight step was not marked for replay: %#v", replayed)
		}
		manifest := got.RecoveryManifest
		if manifest == nil || !containsString(manifest.CompletedStepIDs, "completed-before-crash") || !containsString(manifest.ReplayStepIDs, "in-flight-before-crash") || !containsString(manifest.ReplayIdempotency, "replay-idempotency") {
			t.Fatalf("invalid recovery manifest: %#v", manifest)
		}
		if countEvents(got.Events, EventInterrupted) != 1 {
			t.Fatalf("interrupted event count = %d, want 1: %#v", countEvents(got.Events, EventInterrupted), got.Events)
		}
	})

	t.Run("active lease blocks takeover", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "agent.db")
		runAgentRecoveryHelperProcess(t, dbPath, "active")

		store, err := NewSQLiteCheckpointStore(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		runtime := NewRuntime(ai.NewClient(ai.Config{Provider: "test", Model: "test", BaseURL: "http://example.test/v1"}), nil, ToolSet{}, store)
		got, err := runtime.Get("process-recovery-task")
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != StatusOwnedByOther {
			t.Fatalf("active-lease task status = %q, want %q: %#v", got.Status, StatusOwnedByOther, got)
		}
		if !strings.Contains(got.Error, "另一应用进程") {
			t.Fatalf("active-lease task did not expose ownership error: %#v", got)
		}
		listed, err := runtime.List("")
		if err != nil || len(listed) != 1 || listed[0].Status != StatusOwnedByOther {
			t.Fatalf("task list did not preserve foreign ownership state: %#v, %v", listed, err)
		}
		if _, err := runtime.Resume(context.Background(), got.ID); err == nil || !strings.Contains(err.Error(), "另一") {
			t.Fatalf("active lease takeover was not rejected: %v", err)
		}
	})

	t.Run("future lease with stale heartbeat is replayable", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "agent.db")
		runAgentRecoveryHelperProcess(t, dbPath, "stale")

		store, err := NewSQLiteCheckpointStore(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		runtime := NewRuntime(ai.NewClient(ai.Config{Provider: "test", Model: "test", BaseURL: "http://example.test/v1"}), nil, ToolSet{}, store)
		got, err := runtime.Get("process-recovery-task")
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != StatusInterrupted {
			t.Fatalf("stale-heartbeat task was not interrupted for recovery: %#v", got)
		}
		if err := runtime.ensureTaskLeaseAvailable(got.ID); err != nil {
			t.Fatalf("stale heartbeat still blocked recovery: %v", err)
		}
	})
}

func runAgentRecoveryHelperProcess(t *testing.T, dbPath, mode string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestAgentProcessRestartRecovery$")
	cmd.Env = append(os.Environ(), agentRecoveryHelperEnv+"="+mode, "GOSSH_AGENT_PROCESS_DB="+dbPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("recovery helper failed: %v\n%s", err, output)
	}
}

func runAgentRecoveryHelper(t *testing.T, mode string) {
	t.Helper()
	dbPath := os.Getenv("GOSSH_AGENT_PROCESS_DB")
	if dbPath == "" {
		t.Fatal("GOSSH_AGENT_PROCESS_DB is required")
	}
	store, err := NewSQLiteCheckpointStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task := Task{
		ID: "process-recovery-task", Goal: "verify process recovery", Status: StatusRunning,
		CreatedAt: now, UpdatedAt: now,
		Steps: []Step{
			{ID: "completed-before-crash", TaskID: "process-recovery-task", ToolName: "terminal_command", IdempotencyKey: "completed-idempotency", Status: StepCompleted,
				Result: &ToolResult{ToolName: "terminal_command", Output: "already collected", ExitCode: 0, Status: "ok"}, CreatedAt: now, UpdatedAt: now},
			{ID: "in-flight-before-crash", TaskID: "process-recovery-task", ToolName: "terminal_command", IdempotencyKey: "replay-idempotency", Status: StepExecuting,
				LeaseOwner: "crashed-process", LeaseUntil: now.Add(time.Minute), CreatedAt: now, UpdatedAt: now},
		},
	}
	if err := store.SaveSnapshot(task, task.Steps, nil); err != nil {
		t.Fatal(err)
	}
	if mode == "active" || mode == "stale" {
		acquired, acquireErr := store.TryAcquireTaskLease(task.ID, "crashed-process", time.Minute)
		if acquireErr != nil || !acquired {
			t.Fatalf("helper could not acquire active lease: %v %v", acquired, acquireErr)
		}
		if mode == "stale" {
			oldHeartbeat := now.Add(-(taskLeaseHeartbeatStale + time.Second))
			if _, updateErr := store.db.Exec(`UPDATE agent_tasks SET heartbeat_at = ? WHERE id = ?`, unixMillis(oldHeartbeat), task.ID); updateErr != nil {
				t.Fatal(updateErr)
			}
		}
	} else if mode != "expired" {
		t.Fatalf("unknown helper mode %q", mode)
	}
	// os.Exit models a process that disappears without running application
	// shutdown. SQLite and the OS close the database handles on exit.
	os.Exit(0)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func countEvents(events []Event, typ string) int {
	count := 0
	for _, event := range events {
		if event.Type == typ {
			count++
		}
	}
	return count
}
