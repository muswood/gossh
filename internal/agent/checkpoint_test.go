// owner: muswood | Email: mumu920@outlook.com
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gossh/internal/ai"
)

func TestSQLiteCheckpointStorePersistsTaskStepAndEvent(t *testing.T) {
	store, err := NewSQLiteCheckpointStore(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task := Task{ID: "sqlite-task", TabID: "tab-1", Goal: "check disk", Status: StatusCompleted, CreatedAt: now, UpdatedAt: now}
	step := Step{ID: "sqlite-step", TaskID: task.ID, ToolName: "terminal_command", Status: StepCompleted, Result: &ToolResult{ToolName: "terminal_command", Output: "ok", ExitCode: 0}, CreatedAt: now, UpdatedAt: now}
	event := Event{TaskID: task.ID, StepID: step.ID, Type: EventToolFinished, Payload: map[string]any{"ok": true}, Timestamp: now}
	if err := store.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveStep(step); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(event); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != task.ID || len(got.Steps) != 1 || len(got.Events) != 1 || got.Steps[0].Result.Output != "ok" {
		t.Fatalf("unexpected SQLite task: %#v", got)
	}
}

func TestSQLiteTaskLeasePreventsConcurrentRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	first, err := NewSQLiteCheckpointStore(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewSQLiteCheckpointStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := first.SaveTask(Task{ID: "leased-task", Goal: "recover", Status: StatusRunning, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	acquired, err := first.TryAcquireTaskLease("leased-task", "runtime-a", time.Minute)
	if err != nil || !acquired {
		t.Fatalf("first runtime could not acquire lease: %v %v", acquired, err)
	}
	acquired, err = second.TryAcquireTaskLease("leased-task", "runtime-b", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if acquired {
		t.Fatal("second runtime acquired an active task lease")
	}
	lease, held, err := second.GetTaskLease("leased-task")
	if err != nil || !held || lease.Owner != "runtime-a" {
		t.Fatalf("unexpected active lease: %#v %v %v", lease, held, err)
	}
	if err := first.ReleaseTaskLease("leased-task", "runtime-a"); err != nil {
		t.Fatal(err)
	}
	acquired, err = second.TryAcquireTaskLease("leased-task", "runtime-b", time.Minute)
	if err != nil || !acquired {
		t.Fatalf("lease was not transferable after release: %v %v", acquired, err)
	}

	runtime := NewRuntime(ai.NewClient(ai.Config{Provider: "test", Model: "test", BaseURL: "http://example.test/v1"}), nil, ToolSet{}, second)
	if _, err := runtime.Resume(context.Background(), "leased-task"); err == nil {
		t.Fatal("runtime resumed a task owned by another process")
	}
}

func TestSQLiteTaskLeaseAllowsTakeoverAfterStaleHeartbeat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	first, err := NewSQLiteCheckpointStore(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewSQLiteCheckpointStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := first.SaveTask(Task{ID: "stale-leased-task", Goal: "recover", Status: StatusRunning, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	acquired, err := first.TryAcquireTaskLease("stale-leased-task", "runtime-a", time.Minute)
	if err != nil || !acquired {
		t.Fatalf("first runtime could not acquire lease: %v %v", acquired, err)
	}
	oldHeartbeat := time.Now().Add(-(taskLeaseHeartbeatStale + time.Second))
	if _, err := first.db.Exec(`UPDATE agent_tasks SET heartbeat_at = ? WHERE id = ?`, unixMillis(oldHeartbeat), "stale-leased-task"); err != nil {
		t.Fatal(err)
	}
	acquired, err = second.TryAcquireTaskLease("stale-leased-task", "runtime-b", time.Minute)
	if err != nil || !acquired {
		t.Fatalf("stale lease was not transferable: %v %v", acquired, err)
	}
	lease, held, err := second.GetTaskLease("stale-leased-task")
	if err != nil || !held || lease.Owner != "runtime-b" {
		t.Fatalf("unexpected takeover lease: %#v %v %v", lease, held, err)
	}
}

func TestRuntimeCloseReleasesTaskLease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	store, err := NewSQLiteCheckpointStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := store.SaveTask(Task{ID: "close-task", Goal: "close", Status: StatusRunning, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if acquired, err := store.TryAcquireTaskLease("close-task", "runtime-a", time.Minute); err != nil || !acquired {
		t.Fatalf("could not seed task lease: %v %v", acquired, err)
	}

	// A runtime with a different owner must not be able to clear another
	// process's lease, even when it is closed.
	runtime := NewRuntime(nil, nil, ToolSet{}, store)
	runtime.Close()
	lease, held, err := store.GetTaskLease("close-task")
	if err != nil || !held || lease.Owner != "runtime-a" {
		t.Fatalf("close cleared a foreign lease: %#v %v %v", lease, held, err)
	}

	if err := store.ReleaseTaskLease("close-task", "runtime-a"); err != nil {
		t.Fatal(err)
	}
	if acquired, err := store.TryAcquireTaskLease("close-task", runtime.ownerID, time.Minute); err != nil || !acquired {
		t.Fatalf("could not acquire lease for runtime: %v %v", acquired, err)
	}
	runtime.mu.Lock()
	runtime.tasks["close-task"] = &taskState{task: Task{ID: "close-task", RunnerOwner: runtime.ownerID}}
	runtime.mu.Unlock()
	runtime.Close()
	lease, held, err = store.GetTaskLease("close-task")
	if err != nil || held || lease.Owner != "" {
		t.Fatalf("close did not release its lease: %#v %v %v", lease, held, err)
	}
}

func TestSQLiteCheckpointRejectsOlderSnapshot(t *testing.T) {
	store, err := NewSQLiteCheckpointStore(filepath.Join(t.TempDir(), "checkpoint.db"))
	if err != nil {
		t.Fatal(err)
	}
	newer := Task{ID: "ordered-snapshot", Goal: "new", Status: StatusRunning, CreatedAt: time.Now(), UpdatedAt: time.Now().Add(time.Second)}
	older := newer
	older.Goal = "old"
	older.UpdatedAt = newer.UpdatedAt.Add(-time.Second)
	newerStep := Step{ID: "newer-step", TaskID: newer.ID, ToolName: "terminal_command", Status: StepCompleted, UpdatedAt: newer.UpdatedAt}
	olderStep := Step{ID: "older-step", TaskID: older.ID, ToolName: "terminal_command", Status: StepFailed, UpdatedAt: older.UpdatedAt}
	if err := store.SaveSnapshot(newer, []Step{newerStep}, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSnapshot(older, []Step{olderStep}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadTask(newer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Goal != "new" {
		t.Fatalf("older snapshot overwrote newer task: %#v", got)
	}
	if len(got.Steps) != 1 || got.Steps[0].ID != newerStep.ID {
		t.Fatalf("older snapshot replaced newer steps: %#v", got.Steps)
	}
}

func TestSQLiteCheckpointRedactsSensitiveValues(t *testing.T) {
	store, err := NewSQLiteCheckpointStore(filepath.Join(t.TempDir(), "checkpoint.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	task := Task{ID: "redacted-task", Goal: "password=secret-value", Status: StatusCompleted, CreatedAt: now, UpdatedAt: now}
	step := Step{ID: "redacted-step", TaskID: task.ID, ToolName: "terminal_command", Status: StepCompleted,
		Result: &ToolResult{Output: "token=secret-token", ExitCode: 0}, CreatedAt: now, UpdatedAt: now}
	event := Event{ID: "redacted-event", TaskID: task.ID, Type: EventToolOutput, Timestamp: now, Payload: map[string]any{"apiKey": "secret-key"}}
	if err := store.SaveSnapshot(task, []Step{step}, []Event{event}); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(got)
	for _, secret := range []string{"secret-value", "secret-token", "secret-key"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("checkpoint returned unredacted secret %q: %s", secret, encoded)
		}
	}
}

func TestSQLiteStoresEinoNativeCheckpointBytes(t *testing.T) {
	store, err := NewSQLiteCheckpointStore(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0, 1, 2, 255}
	if err := store.SetEinoCheckpoint(context.Background(), "eino-task", want); err != nil {
		t.Fatal(err)
	}
	got, exists, err := store.GetEinoCheckpoint(context.Background(), "eino-task")
	if err != nil || !exists || string(got) != string(want) {
		t.Fatalf("Eino checkpoint mismatch: exists=%v got=%v err=%v", exists, got, err)
	}
	if err := store.DeleteEinoCheckpoint(context.Background(), "eino-task"); err != nil {
		t.Fatal(err)
	}
	_, exists, err = store.GetEinoCheckpoint(context.Background(), "eino-task")
	if err != nil || exists {
		t.Fatalf("Eino checkpoint was not deleted: exists=%v err=%v", exists, err)
	}
}

func TestSQLiteCheckpointStoreDeduplicatesEventsAndSavesSnapshot(t *testing.T) {
	store, err := NewSQLiteCheckpointStore(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task := Task{ID: "snapshot-task", Goal: "snapshot", Status: StatusRunning, CreatedAt: now, UpdatedAt: now}
	step := Step{ID: "snapshot-step", TaskID: task.ID, IdempotencyKey: "idem-1", Status: StepCompleted, CreatedAt: now, UpdatedAt: now}
	event := Event{ID: "event-1", TaskID: task.ID, Type: EventToolFinished, Timestamp: now, Payload: map[string]any{"ok": true}}
	if err := store.SaveSnapshot(task, []Step{step}, []Event{event}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(event); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Steps) != 1 || len(got.Events) != 1 || got.Events[0].ID != event.ID {
		t.Fatalf("snapshot or event deduplication failed: %#v", got)
	}
}

func TestSQLiteCheckpointStoreSerializesConcurrentSnapshots(t *testing.T) {
	store, err := NewSQLiteCheckpointStore(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task := Task{ID: "concurrent-snapshot-task", Goal: "concurrent writes", Status: StatusRunning, CreatedAt: now, UpdatedAt: now}
	const writes = 32
	errs := make(chan error, writes)
	var group sync.WaitGroup
	for i := 0; i < writes; i++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			event := Event{
				ID: fmt.Sprintf("concurrent-event-%d", index), TaskID: task.ID,
				Type: EventToolOutput, Timestamp: now.Add(time.Duration(index) * time.Millisecond),
				Payload: map[string]any{"index": index},
			}
			errs <- store.SaveSnapshot(task, nil, []Event{event})
		}(i)
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent snapshot failed: %v", err)
		}
	}
	events, err := store.LoadEvents(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != writes {
		t.Fatalf("concurrent snapshots lost events: got %d, want %d", len(events), writes)
	}
}

func TestSQLiteCheckpointImportsLegacyJSON(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "agent-checkpoints.json")
	legacy := NewFileCheckpointStore(legacyPath)
	now := time.Now()
	if err := legacy.SaveTask(Task{ID: "legacy-task", Goal: "migrate", Status: StatusFailed, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	store, err := NewSQLiteCheckpointStore(filepath.Join(dir, "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ImportFileCheckpoint(legacyPath); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadTask("legacy-task")
	if err != nil || got.ID != "legacy-task" {
		t.Fatalf("legacy task not imported: %#v %v", got, err)
	}
}

func TestFileCheckpointStorePersistsTaskStepAndEvent(t *testing.T) {
	store := NewFileCheckpointStore(filepath.Join(t.TempDir(), "agent-checkpoints.json"))
	now := time.Now()
	task := Task{ID: "task-1", TabID: "tab-1", Goal: "diagnose", Status: StatusRunning, CreatedAt: now, UpdatedAt: now}
	step := Step{ID: "step-1", TaskID: task.ID, ToolName: "terminal_command", Status: StepCompleted, CreatedAt: now, UpdatedAt: now}
	event := Event{TaskID: task.ID, StepID: step.ID, Type: EventToolFinished, Payload: "ok", Timestamp: now}

	if err := store.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveStep(step); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(event); err != nil {
		t.Fatal(err)
	}

	got, err := store.LoadTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != task.ID || len(got.Steps) != 1 || len(got.Events) != 1 {
		t.Fatalf("unexpected loaded task: %#v", got)
	}

	tasks, err := store.ListTasks("tab-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != task.ID {
		t.Fatalf("unexpected task list: %#v", tasks)
	}
}

func TestSQLiteCheckpointStoreListsRecentTasks(t *testing.T) {
	store, err := NewSQLiteCheckpointStore(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	testRecentTaskStore(t, store)
}

func TestFileCheckpointStoreListsRecentTasks(t *testing.T) {
	store := NewFileCheckpointStore(filepath.Join(t.TempDir(), "agent-checkpoints.json"))
	testRecentTaskStore(t, store)
}

func testRecentTaskStore(t *testing.T, store interface {
	SaveSnapshot(Task, []Step, []Event) error
	ListRecentTasks(string, int) ([]Task, error)
}) {
	t.Helper()
	now := time.Now().UTC()
	for i, tabID := range []string{"tab-1", "tab-1", "tab-1", "other-tab"} {
		id := fmt.Sprintf("task-%d", i)
		task := Task{ID: id, TabID: tabID, Goal: id, Status: StatusCompleted,
			CreatedAt: now.Add(time.Duration(i) * time.Minute), UpdatedAt: now.Add(time.Duration(i) * time.Minute)}
		step := Step{ID: id + "-step", TaskID: id, ToolName: "terminal_command", Status: StepCompleted,
			Result: &ToolResult{Output: id + "-step-output"}, CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt}
		event := Event{ID: id + "-event", TaskID: id, Type: EventToolFinished, Payload: id + "-event-payload", Timestamp: task.UpdatedAt}
		if err := store.SaveSnapshot(task, []Step{step}, []Event{event}); err != nil {
			t.Fatal(err)
		}
	}
	tasks, err := store.ListRecentTasks("tab-1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0].ID != "task-2" || tasks[1].ID != "task-1" {
		t.Fatalf("recent tasks = %#v, want task-2 and task-1", tasks)
	}
	for _, task := range tasks {
		if len(task.Steps) != 1 || task.Steps[0].Result.Output != task.ID+"-step-output" {
			t.Fatalf("task %s missing recent steps: %#v", task.ID, task.Steps)
		}
		if len(task.Events) != 0 {
			t.Fatalf("task %s loaded events into recent summaries: %#v", task.ID, task.Events)
		}
	}
}
