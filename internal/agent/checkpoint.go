// owner: muswood | Email: mumu920@outlook.com
package agent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var checkpointSecretPattern = regexp.MustCompile(`(?i)\b(password|passwd|token|api[_-]?key|secret|authorization)\s*[:=]\s*[^\s,;]+`)

func redactCheckpointText(value string) string {
	return checkpointSecretPattern.ReplaceAllString(value, "$1=[REDACTED]")
}

func redactCheckpointValue(value any) any {
	switch item := value.(type) {
	case string:
		return redactCheckpointText(item)
	case map[string]any:
		out := make(map[string]any, len(item))
		for key, child := range item {
			if strings.Contains(strings.ToLower(key), "password") || strings.Contains(strings.ToLower(key), "token") || strings.Contains(strings.ToLower(key), "secret") || strings.Contains(strings.ToLower(key), "apikey") || strings.Contains(strings.ToLower(key), "authorization") {
				out[key] = "[REDACTED]"
			} else {
				out[key] = redactCheckpointValue(child)
			}
		}
		return out
	case []any:
		out := make([]any, len(item))
		for i, child := range item {
			out[i] = redactCheckpointValue(child)
		}
		return out
	default:
		return value
	}
}

func redactCheckpointJSON(raw []byte) []byte {
	var value any
	if json.Unmarshal(raw, &value) == nil {
		if encoded, err := json.Marshal(redactCheckpointValue(value)); err == nil {
			return encoded
		}
	}
	return []byte(redactCheckpointText(string(raw)))
}

type CheckpointStore interface {
	SaveTask(Task) error
	SaveStep(Step) error
	AppendEvent(Event) error
	SaveSnapshot(Task, []Step, []Event) error
	LoadTask(taskID string) (Task, error)
	ListTasks(tabID string) ([]Task, error)
	LoadEvents(taskID string) ([]Event, error)
}

// RecentTaskStore is the bounded read path used when task history crosses the
// model boundary. Full ListTasks remains available for UI and recovery.
type RecentTaskStore interface {
	ListRecentTasks(tabID string, limit int) ([]Task, error)
}

// TaskLeaseStore is implemented by durable stores that can atomically assign
// a task to one runtime. It prevents two application processes from replaying
// the same checkpoint concurrently. File checkpoints remain compatible but do
// not offer cross-process lease guarantees.
type TaskLeaseStore interface {
	GetTaskLease(taskID string) (TaskLease, bool, error)
	TryAcquireTaskLease(taskID, owner string, ttl time.Duration) (bool, error)
	RenewTaskLease(taskID, owner string, ttl time.Duration) (bool, error)
	ReleaseTaskLease(taskID, owner string) error
}

type EinoCheckpointStore interface {
	GetEinoCheckpoint(ctx context.Context, checkpointID string) ([]byte, bool, error)
	SetEinoCheckpoint(ctx context.Context, checkpointID string, data []byte) error
	DeleteEinoCheckpoint(ctx context.Context, checkpointID string) error
}

type TaskLease struct {
	Owner       string
	Until       time.Time
	HeartbeatAt time.Time
}

// SQLiteCheckpointStore stores the event log separately from the materialized
// task and step views. This keeps writes small and makes task listing indexed.
type SQLiteCheckpointStore struct {
	db *sql.DB
}

func NewSQLiteCheckpointStore(path string) (*SQLiteCheckpointStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	// SQLite permits many readers but only one writer. Agent snapshots, Eino
	// checkpoints, and lease heartbeats all share this store, so keeping one
	// connection per process avoids self-inflicted writer contention. The DSN
	// pragma is applied to every connection as well, including connections
	// opened by database/sql after the schema migration.
	db, err := sql.Open("sqlite", sqliteCheckpointDSN(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := &SQLiteCheckpointStore{db: db}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func sqliteCheckpointDSN(path string) string {
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	return path + separator + "_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)"
}

func (s *SQLiteCheckpointStore) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS agent_schema (name TEXT PRIMARY KEY, version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("初始化 Agent SQLite schema 版本失败: %w", err)
	}
	queries := []string{
		`PRAGMA busy_timeout = 30000`,
		`PRAGMA journal_mode = WAL`,
		`CREATE TABLE IF NOT EXISTS agent_tasks (
			id TEXT PRIMARY KEY,
			tab_id TEXT NOT NULL DEFAULT '',
			updated_at DATETIME NOT NULL,
			lease_owner TEXT NOT NULL DEFAULT '',
			lease_until INTEGER NOT NULL DEFAULT 0,
			heartbeat_at INTEGER NOT NULL DEFAULT 0,
			payload TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_tasks_tab_updated ON agent_tasks(tab_id, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS agent_steps (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			idempotency_key TEXT NOT NULL DEFAULT '',
			updated_at DATETIME NOT NULL,
			payload TEXT NOT NULL,
			UNIQUE(task_id, idempotency_key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_steps_task ON agent_steps(task_id, updated_at)`,
		`CREATE TABLE IF NOT EXISTS agent_events (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			event_id TEXT NOT NULL DEFAULT '',
			task_id TEXT NOT NULL,
			step_id TEXT NOT NULL DEFAULT '',
			timestamp DATETIME NOT NULL,
			type TEXT NOT NULL,
			payload TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_events_task ON agent_events(task_id, seq)`,
		`CREATE TABLE IF NOT EXISTS agent_eino_checkpoints (
			checkpoint_id TEXT PRIMARY KEY,
			updated_at DATETIME NOT NULL,
			payload BLOB NOT NULL
		)`,
	}
	for _, query := range queries {
		if _, err := s.db.Exec(query); err != nil {
			return fmt.Errorf("初始化 Agent SQLite checkpoint 失败: %w", err)
		}
	}
	for _, column := range []string{
		"lease_owner TEXT NOT NULL DEFAULT ''",
		"lease_until INTEGER NOT NULL DEFAULT 0",
		"heartbeat_at INTEGER NOT NULL DEFAULT 0",
	} {
		if _, err := s.db.Exec(`ALTER TABLE agent_tasks ADD COLUMN ` + column); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return fmt.Errorf("升级 Agent task lease schema 失败: %w", err)
		}
	}
	var hasEventID bool
	rows, err := s.db.Query(`PRAGMA table_info(agent_events)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			_ = rows.Close()
			return err
		}
		if name == "event_id" {
			hasEventID = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if !hasEventID {
		if _, err := s.db.Exec(`ALTER TABLE agent_events ADD COLUMN event_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("升级 Agent event schema 失败: %w", err)
		}
	}
	if err := s.backfillEventIDs(); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_events_event_id ON agent_events(event_id)`); err != nil {
		return fmt.Errorf("创建 Agent event 去重索引失败: %w", err)
	}
	if _, err := s.db.Exec(`INSERT INTO agent_schema(name, version) VALUES('checkpoint', 3)
		ON CONFLICT(name) DO UPDATE SET version=excluded.version`); err != nil {
		return err
	}
	return nil
}

func (s *SQLiteCheckpointStore) backfillEventIDs() error {
	rows, err := s.db.Query(`SELECT seq, event_id, task_id, step_id, timestamp, type, payload FROM agent_events ORDER BY seq`)
	if err != nil {
		return err
	}
	// Read and close the result set before issuing UPDATEs. The store uses one
	// database connection, so keeping rows open while calling s.db.Exec would
	// otherwise wait forever for the same connection.
	type eventIDUpdate struct {
		id  string
		seq int64
	}
	updates := make([]eventIDUpdate, 0)
	seen := make(map[string]struct{})
	for rows.Next() {
		var seq int64
		var id, taskID, stepID, typ, payload string
		var timestamp time.Time
		if err := rows.Scan(&seq, &id, &taskID, &stepID, &timestamp, &typ, &payload); err != nil {
			_ = rows.Close()
			return err
		}
		if id == "" {
			id = stableEventID(Event{TaskID: taskID, StepID: stepID, Timestamp: timestamp, Type: typ, Payload: json.RawMessage(payload)})
		}
		if _, exists := seen[id]; exists {
			id = fmt.Sprintf("%s-%d", id, seq)
		}
		seen[id] = struct{}{}
		updates = append(updates, eventIDUpdate{id: id, seq: seq})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, update := range updates {
		if _, err := s.db.Exec(`UPDATE agent_events SET event_id = ? WHERE seq = ?`, update.id, update.seq); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteCheckpointStore) SaveTask(task Task) error {
	payload, err := json.Marshal(taskWithoutRelations(task))
	payload = redactCheckpointJSON(payload)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO agent_tasks(id, tab_id, updated_at, lease_owner, lease_until, heartbeat_at, payload) VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET tab_id=excluded.tab_id, updated_at=excluded.updated_at, lease_owner=excluded.lease_owner, lease_until=excluded.lease_until, heartbeat_at=excluded.heartbeat_at, payload=excluded.payload
		WHERE excluded.updated_at >= agent_tasks.updated_at`,
		task.ID, task.TabID, task.UpdatedAt, task.RunnerOwner, unixMillis(task.RunnerLeaseUntil), unixMillis(task.RunnerHeartbeatAt), string(payload))
	return err
}

func (s *SQLiteCheckpointStore) SaveStep(step Step) error {
	if step.IdempotencyKey == "" {
		step.IdempotencyKey = step.ID
	}
	payload, err := json.Marshal(step)
	payload = redactCheckpointJSON(payload)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO agent_steps(id, task_id, idempotency_key, updated_at, payload) VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET task_id=excluded.task_id, idempotency_key=excluded.idempotency_key,
		updated_at=excluded.updated_at, payload=excluded.payload
		WHERE excluded.updated_at >= agent_steps.updated_at`,
		step.ID, step.TaskID, step.IdempotencyKey, step.UpdatedAt, string(payload))
	return err
}

func (s *SQLiteCheckpointStore) AppendEvent(event Event) error {
	if event.ID == "" {
		event.ID = stableEventID(event)
	}
	payload, err := json.Marshal(event.Payload)
	payload = redactCheckpointJSON(payload)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO agent_events(event_id, task_id, step_id, timestamp, type, payload) VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(event_id) DO NOTHING`, event.ID,
		event.TaskID, event.StepID, event.Timestamp, event.Type, string(payload))
	return err
}

// SaveSnapshot persists the materialized task, all of its steps, and its
// event log in one SQLite transaction. Event rows are immutable and deduped by
// their stable event ID, while steps are replaced as a single snapshot.
func (s *SQLiteCheckpointStore) SaveSnapshot(task Task, steps []Step, events []Event) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	rollback := func(err error) error { _ = tx.Rollback(); return err }
	payload, err := json.Marshal(taskWithoutRelations(task))
	payload = redactCheckpointJSON(payload)
	if err != nil {
		return rollback(err)
	}
	result, err := tx.Exec(`INSERT INTO agent_tasks(id, tab_id, updated_at, lease_owner, lease_until, heartbeat_at, payload) VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET tab_id=excluded.tab_id, updated_at=excluded.updated_at, lease_owner=excluded.lease_owner, lease_until=excluded.lease_until, heartbeat_at=excluded.heartbeat_at, payload=excluded.payload
		WHERE excluded.updated_at >= agent_tasks.updated_at`,
		task.ID, task.TabID, task.UpdatedAt, task.RunnerOwner, unixMillis(task.RunnerLeaseUntil), unixMillis(task.RunnerHeartbeatAt), string(payload))
	if err != nil {
		return rollback(err)
	}
	if updated, err := result.RowsAffected(); err != nil {
		return rollback(err)
	} else if updated == 0 {
		return tx.Commit()
	}
	if _, err = tx.Exec(`DELETE FROM agent_steps WHERE task_id = ?`, task.ID); err != nil {
		return rollback(err)
	}
	for _, step := range steps {
		if step.IdempotencyKey == "" {
			step.IdempotencyKey = step.ID
		}
		payload, err = json.Marshal(step)
		payload = redactCheckpointJSON(payload)
		if err != nil {
			return rollback(err)
		}
		if _, err = tx.Exec(`INSERT INTO agent_steps(id, task_id, idempotency_key, updated_at, payload) VALUES(?, ?, ?, ?, ?)`,
			step.ID, step.TaskID, step.IdempotencyKey, step.UpdatedAt, string(payload)); err != nil {
			return rollback(err)
		}
	}
	for _, event := range events {
		if event.ID == "" {
			event.ID = stableEventID(event)
		}
		payload, err = json.Marshal(event.Payload)
		payload = redactCheckpointJSON(payload)
		if err != nil {
			return rollback(err)
		}
		if _, err = tx.Exec(`INSERT INTO agent_events(event_id, task_id, step_id, timestamp, type, payload) VALUES(?, ?, ?, ?, ?, ?)
			ON CONFLICT(event_id) DO NOTHING`, event.ID, event.TaskID, event.StepID, event.Timestamp, event.Type, string(payload)); err != nil {
			return rollback(err)
		}
	}
	return tx.Commit()
}

func (s *SQLiteCheckpointStore) LoadTask(taskID string) (Task, error) {
	var payload, owner string
	var until, heartbeat int64
	err := s.db.QueryRow(`SELECT payload, lease_owner, lease_until, heartbeat_at FROM agent_tasks WHERE id = ?`, taskID).Scan(&payload, &owner, &until, &heartbeat)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, os.ErrNotExist
	}
	if err != nil {
		return Task{}, err
	}
	var task Task
	if err := json.Unmarshal([]byte(payload), &task); err != nil {
		return Task{}, fmt.Errorf("读取 Agent task 失败: %w", err)
	}
	task.RunnerOwner, task.RunnerLeaseUntil, task.RunnerHeartbeatAt = owner, fromUnixMillis(until), fromUnixMillis(heartbeat)
	steps, err := s.loadSteps(taskID)
	if err != nil {
		return Task{}, err
	}
	events, err := s.LoadEvents(taskID)
	if err != nil {
		return Task{}, err
	}
	task.Steps, task.Events = steps, events
	return task, nil
}

func (s *SQLiteCheckpointStore) ListTasks(tabID string) ([]Task, error) {
	query := `SELECT payload, lease_owner, lease_until, heartbeat_at FROM agent_tasks ORDER BY updated_at DESC`
	args := []any{}
	if tabID != "" {
		query = `SELECT payload, lease_owner, lease_until, heartbeat_at FROM agent_tasks WHERE tab_id = ? ORDER BY updated_at DESC`
		args = append(args, tabID)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	type taskRow struct {
		payload   string
		owner     string
		until     int64
		heartbeat int64
	}
	rowsData := make([]taskRow, 0)
	for rows.Next() {
		var payload, owner string
		var until, heartbeat int64
		if err := rows.Scan(&payload, &owner, &until, &heartbeat); err != nil {
			_ = rows.Close()
			return nil, err
		}
		rowsData = append(rowsData, taskRow{payload: payload, owner: owner, until: until, heartbeat: heartbeat})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	tasks := make([]Task, 0, len(rowsData))
	for _, row := range rowsData {
		payload, owner, until, heartbeat := row.payload, row.owner, row.until, row.heartbeat
		var task Task
		if err := json.Unmarshal([]byte(payload), &task); err != nil {
			return nil, err
		}
		task.RunnerOwner, task.RunnerLeaseUntil, task.RunnerHeartbeatAt = owner, fromUnixMillis(until), fromUnixMillis(heartbeat)
		task.Steps, err = s.loadSteps(task.ID)
		if err != nil {
			return nil, err
		}
		task.Events, err = s.LoadEvents(task.ID)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func (s *SQLiteCheckpointStore) ListRecentTasks(tabID string, limit int) ([]Task, error) {
	if limit <= 0 {
		return []Task{}, nil
	}
	query := `SELECT payload, lease_owner, lease_until, heartbeat_at FROM agent_tasks`
	args := []any{}
	if tabID != "" {
		query += ` WHERE tab_id = ?`
		args = append(args, tabID)
	}
	query += ` ORDER BY updated_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	type taskRow struct {
		payload   string
		owner     string
		until     int64
		heartbeat int64
	}
	rowsData := make([]taskRow, 0, limit)
	for rows.Next() {
		var payload, owner string
		var until, heartbeat int64
		if err := rows.Scan(&payload, &owner, &until, &heartbeat); err != nil {
			_ = rows.Close()
			return nil, err
		}
		rowsData = append(rowsData, taskRow{payload: payload, owner: owner, until: until, heartbeat: heartbeat})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	tasks := make([]Task, 0, len(rowsData))
	for _, row := range rowsData {
		var task Task
		if err := json.Unmarshal([]byte(row.payload), &task); err != nil {
			return nil, err
		}
		task.RunnerOwner, task.RunnerLeaseUntil, task.RunnerHeartbeatAt = row.owner, fromUnixMillis(row.until), fromUnixMillis(row.heartbeat)
		task.Steps, err = s.loadSteps(task.ID)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func unixMillis(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UnixMilli()
}

func fromUnixMillis(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(value)
}

func (s *SQLiteCheckpointStore) GetTaskLease(taskID string) (TaskLease, bool, error) {
	var owner string
	var until, heartbeat int64
	err := s.db.QueryRow(`SELECT lease_owner, lease_until, heartbeat_at FROM agent_tasks WHERE id = ?`, taskID).Scan(&owner, &until, &heartbeat)
	if errors.Is(err, sql.ErrNoRows) {
		return TaskLease{}, false, nil
	}
	if err != nil {
		return TaskLease{}, false, err
	}
	return TaskLease{Owner: owner, Until: fromUnixMillis(until), HeartbeatAt: fromUnixMillis(heartbeat)}, owner != "" && until > 0, nil
}

func (s *SQLiteCheckpointStore) TryAcquireTaskLease(taskID, owner string, ttl time.Duration) (bool, error) {
	if taskID == "" || owner == "" {
		return false, errors.New("task lease id 和 owner 不能为空")
	}
	now := time.Now()
	staleHeartbeatBefore := unixMillis(now.Add(-taskLeaseHeartbeatStale))
	result, err := s.db.Exec(`UPDATE agent_tasks SET lease_owner = ?, lease_until = ?, heartbeat_at = ?
		WHERE id = ? AND (lease_owner = '' OR lease_owner = ? OR lease_until <= ?
			OR (heartbeat_at > 0 AND heartbeat_at <= ?))`, owner, unixMillis(now.Add(ttl)), unixMillis(now), taskID, owner, unixMillis(now), staleHeartbeatBefore)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed == 1, err
}

func (s *SQLiteCheckpointStore) RenewTaskLease(taskID, owner string, ttl time.Duration) (bool, error) {
	now := time.Now()
	result, err := s.db.Exec(`UPDATE agent_tasks SET lease_until = ?, heartbeat_at = ? WHERE id = ? AND lease_owner = ?`, unixMillis(now.Add(ttl)), unixMillis(now), taskID, owner)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed == 1, err
}

func (s *SQLiteCheckpointStore) ReleaseTaskLease(taskID, owner string) error {
	_, err := s.db.Exec(`UPDATE agent_tasks SET lease_owner = '', lease_until = 0, heartbeat_at = 0 WHERE id = ? AND lease_owner = ?`, taskID, owner)
	return err
}

func (s *SQLiteCheckpointStore) GetEinoCheckpoint(ctx context.Context, checkpointID string) ([]byte, bool, error) {
	var payload []byte
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM agent_eino_checkpoints WHERE checkpoint_id = ?`, checkpointID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return append([]byte(nil), payload...), true, nil
}

func (s *SQLiteCheckpointStore) SetEinoCheckpoint(ctx context.Context, checkpointID string, data []byte) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_eino_checkpoints(checkpoint_id, updated_at, payload) VALUES(?, ?, ?)
		ON CONFLICT(checkpoint_id) DO UPDATE SET updated_at=excluded.updated_at, payload=excluded.payload`, checkpointID, time.Now(), data)
	return err
}

func (s *SQLiteCheckpointStore) DeleteEinoCheckpoint(ctx context.Context, checkpointID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM agent_eino_checkpoints WHERE checkpoint_id = ?`, checkpointID)
	return err
}

func (s *SQLiteCheckpointStore) LoadEvents(taskID string) ([]Event, error) {
	rows, err := s.db.Query(`SELECT event_id, step_id, timestamp, type, payload FROM agent_events WHERE task_id = ? ORDER BY seq`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]Event, 0)
	for rows.Next() {
		var event Event
		var payload string
		if err := rows.Scan(&event.ID, &event.StepID, &event.Timestamp, &event.Type, &payload); err != nil {
			return nil, err
		}
		event.TaskID = taskID
		if payload != "null" && payload != "" {
			if err := json.Unmarshal([]byte(payload), &event.Payload); err != nil {
				return nil, err
			}
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *SQLiteCheckpointStore) loadSteps(taskID string) ([]Step, error) {
	rows, err := s.db.Query(`SELECT payload FROM agent_steps WHERE task_id = ? ORDER BY updated_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	steps := make([]Step, 0)
	for rows.Next() {
		var payload string
		var step Step
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(payload), &step); err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	return steps, rows.Err()
}

// ImportFileCheckpoint imports legacy JSON data without deleting the source.
// Keeping the source allows rollback if an older application version is used.
func (s *SQLiteCheckpointStore) ImportFileCheckpoint(path string) error {
	legacy := NewFileCheckpointStore(path)
	tasks, err := legacy.ListTasks("")
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if err := s.SaveSnapshot(task, task.Steps, task.Events); err != nil {
			return err
		}
	}
	return nil
}

func stableEventID(event Event) string {
	payload, _ := json.Marshal(event.Payload)
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s", event.TaskID, event.StepID, event.Timestamp.UTC().Format(time.RFC3339Nano), event.Type, payload)
	return hex.EncodeToString(h.Sum(nil))
}

func taskWithoutRelations(task Task) Task {
	task.Steps = nil
	task.Events = nil
	return task
}

// FileCheckpointStore remains available for compatibility and isolated tests.
type checkpointData struct {
	Tasks  map[string]Task    `json:"tasks"`
	Steps  map[string][]Step  `json:"steps"`
	Events map[string][]Event `json:"events"`
}

type FileCheckpointStore struct {
	path string
	mu   sync.Mutex
}

func NewFileCheckpointStore(path string) *FileCheckpointStore {
	return &FileCheckpointStore{path: path}
}

func (s *FileCheckpointStore) SaveTask(task Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadLocked()
	if err != nil {
		return err
	}
	task.Steps, task.Events = data.Steps[task.ID], data.Events[task.ID]
	data.Tasks[task.ID] = task
	return s.saveLocked(data)
}

func (s *FileCheckpointStore) SaveStep(step Step) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadLocked()
	if err != nil {
		return err
	}
	steps, found := data.Steps[step.TaskID], false
	for i := range steps {
		if steps[i].ID == step.ID {
			steps[i], found = step, true
			break
		}
	}
	if !found {
		steps = append(steps, step)
	}
	data.Steps[step.TaskID] = steps
	if task, ok := data.Tasks[step.TaskID]; ok {
		task.Steps = steps
		data.Tasks[step.TaskID] = task
	}
	return s.saveLocked(data)
}

func (s *FileCheckpointStore) AppendEvent(event Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadLocked()
	if err != nil {
		return err
	}
	data.Events[event.TaskID] = append(data.Events[event.TaskID], event)
	if task, ok := data.Tasks[event.TaskID]; ok {
		task.Events = data.Events[event.TaskID]
		data.Tasks[event.TaskID] = task
	}
	return s.saveLocked(data)
}

func (s *FileCheckpointStore) SaveSnapshot(task Task, steps []Step, events []Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadLocked()
	if err != nil {
		return err
	}
	for i := range steps {
		if steps[i].IdempotencyKey == "" {
			steps[i].IdempotencyKey = steps[i].ID
		}
	}
	for i := range events {
		if events[i].ID == "" {
			events[i].ID = stableEventID(events[i])
		}
	}
	if existing, ok := data.Tasks[task.ID]; ok && existing.UpdatedAt.After(task.UpdatedAt) {
		return nil
	}
	redactedTask := taskWithoutRelations(task)
	if raw, err := json.Marshal(redactedTask); err == nil {
		if json.Unmarshal(redactCheckpointJSON(raw), &redactedTask); err != nil {
			return err
		}
	}
	data.Tasks[task.ID] = redactedTask
	data.Steps[task.ID] = append([]Step(nil), steps...)
	data.Events[task.ID] = append([]Event(nil), events...)
	return s.saveLocked(data)
}

func (s *FileCheckpointStore) LoadTask(taskID string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadLocked()
	if err != nil {
		return Task{}, err
	}
	task, ok := data.Tasks[taskID]
	if !ok {
		return Task{}, os.ErrNotExist
	}
	task.Steps, task.Events = data.Steps[taskID], data.Events[taskID]
	return task, nil
}

func (s *FileCheckpointStore) ListTasks(tabID string) ([]Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	tasks := make([]Task, 0, len(data.Tasks))
	for id, task := range data.Tasks {
		if tabID != "" && task.TabID != tabID {
			continue
		}
		task.Steps, task.Events = data.Steps[id], data.Events[id]
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].UpdatedAt.After(tasks[j].UpdatedAt) })
	return tasks, nil
}

func (s *FileCheckpointStore) ListRecentTasks(tabID string, limit int) ([]Task, error) {
	if limit <= 0 {
		return []Task{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	tasks := make([]Task, 0, limit)
	for _, task := range data.Tasks {
		if tabID != "" && task.TabID != tabID {
			continue
		}
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].UpdatedAt.After(tasks[j].UpdatedAt) })
	if len(tasks) > limit {
		tasks = tasks[:limit]
	}
	for i := range tasks {
		tasks[i].Steps = append([]Step(nil), data.Steps[tasks[i].ID]...)
	}
	return tasks, nil
}

func (s *FileCheckpointStore) LoadEvents(taskID string) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	return append([]Event(nil), data.Events[taskID]...), nil
}

func (s *FileCheckpointStore) loadLocked() (checkpointData, error) {
	data := checkpointData{Tasks: map[string]Task{}, Steps: map[string][]Step{}, Events: map[string][]Event{}}
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return data, nil
	}
	if err != nil {
		return data, err
	}
	if len(raw) == 0 {
		return data, nil
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return data, err
	}
	if data.Tasks == nil {
		data.Tasks = map[string]Task{}
	}
	if data.Steps == nil {
		data.Steps = map[string][]Step{}
	}
	if data.Events == nil {
		data.Events = map[string][]Event{}
	}
	return data, nil
}

func (s *FileCheckpointStore) saveLocked(data checkpointData) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, raw, 0600)
}
