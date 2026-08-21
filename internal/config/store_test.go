// owner: muswood | Email: mumu920@outlook.com
package config

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestImportAllRollsBackOnInvalidConnection(t *testing.T) {
	s := newTestStore(t)
	err := s.ImportAll(`{"groups":[{"id":"ops","name":"运维"}],"connections":[{"id":"bad"}]}`)
	if err == nil {
		t.Fatal("expected invalid import to fail")
	}
	groups, err := s.ListGroups()
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 0 {
		t.Fatalf("transaction left %d groups after failed import", len(groups))
	}
}

func TestImportAllPersistsValidData(t *testing.T) {
	s := newTestStore(t)
	err := s.ImportAll(`{"groups":[{"id":"ops","name":"运维"}],"connections":[{"id":"srv-1","name":"server","host":"127.0.0.1","port":22,"username":"root","authMethod":"password","groupId":"ops"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := s.FindConnection("srv-1")
	if err != nil {
		t.Fatal(err)
	}
	if conn == nil || conn.Host != "127.0.0.1" || conn.GroupID != "ops" {
		t.Fatalf("unexpected imported connection: %+v", conn)
	}
}

func TestConnectionTerminalThemePersists(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveConnection(ConnectionRecord{
		ID: "theme-1", Name: "theme", Host: "127.0.0.1", Port: 22,
		Username: "root", AuthMethod: "password", TerminalTheme: "catppuccinMocha",
	}); err != nil {
		t.Fatal(err)
	}
	conn, err := s.FindConnection("theme-1")
	if err != nil {
		t.Fatal(err)
	}
	if conn == nil || conn.TerminalTheme != "catppuccinMocha" {
		t.Fatalf("unexpected terminal theme: %+v", conn)
	}
}

func TestSetConnectionGroupPreservesConnectionFields(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveConnection(ConnectionRecord{ID: "group-1", Name: "host", Host: "127.0.0.1", Port: 22, Username: "root", AuthMethod: "password", Password: "encrypted", GroupID: "old"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetConnectionGroup("group-1", "new"); err != nil {
		t.Fatal(err)
	}
	conn, err := s.FindConnection("group-1")
	if err != nil {
		t.Fatal(err)
	}
	if conn == nil || conn.GroupID != "new" || conn.Password != "encrypted" || conn.Host != "127.0.0.1" {
		t.Fatalf("group update changed connection fields: %+v", conn)
	}
	if err := s.SetConnectionGroup("group-1", "ungrouped"); err != nil {
		t.Fatal(err)
	}
	conn, err = s.FindConnection("group-1")
	if err != nil || conn.GroupID != "" {
		t.Fatalf("ungrouped update = %+v, err=%v", conn, err)
	}
}

func TestMigrationReadsLegacyRowsWithNullNewColumns(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Store{db: db}
	t.Cleanup(func() { _ = s.Close() })

	if _, err := db.Exec(`
		CREATE TABLE connections (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			host TEXT NOT NULL,
			port INTEGER DEFAULT 22,
			username TEXT NOT NULL,
			auth_method TEXT DEFAULT 'password',
			password TEXT,
			private_key TEXT,
			passphrase TEXT,
			jump_host TEXT,
			encoding TEXT DEFAULT 'utf-8',
			startup_cmd TEXT,
			keep_alive INTEGER DEFAULT 30,
			group_id TEXT,
			starred INTEGER DEFAULT 0,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO connections (id, name, host, username) VALUES ('legacy', 'legacy', '127.0.0.1', 'root')`); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}

	conn, err := s.FindConnection("legacy")
	if err != nil {
		t.Fatal(err)
	}
	if conn == nil || conn.PrivateKeyPath != "" || conn.CertificatePath != "" {
		t.Fatalf("unexpected legacy connection: %+v", conn)
	}
	conns, err := s.ListConnections()
	if err != nil {
		t.Fatal(err)
	}
	if len(conns) != 1 || conns[0].ID != "legacy" {
		t.Fatalf("unexpected connection list: %+v", conns)
	}
}

func TestAIConfigPersistsAPIModeAndRAG(t *testing.T) {
	s := newTestStore(t)
	err := s.SaveAIConfig(AIProviderConfig{
		Provider: "openai", Model: "gpt-4o", APIKey: "key", BaseURL: "https://api.openai.com/v1",
		APIMode: "responses", RAGEnabled: true, RAGVectorBackend: "qdrant", RAGVectorEndpoint: "https://qdrant.example.test", RAGVectorCollection: "docs", RAGVectorAPIKey: "vector-key", MaxTokens: 1000, Temperature: 0.2, AgentMaxSteps: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := s.LoadAIConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil || cfg.APIMode != "responses" || !cfg.RAGEnabled || cfg.RAGVectorBackend != "qdrant" || cfg.RAGVectorCollection != "docs" || cfg.RAGVectorAPIKey != "vector-key" || cfg.AgentMaxSteps != 50 {
		t.Fatalf("unexpected AI config: %+v", cfg)
	}
}

func TestAIConfigDefaultsAgentMaxSteps(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveAIConfig(AIProviderConfig{Provider: "test", Model: "test", MaxTokens: 1000}); err != nil {
		t.Fatal(err)
	}
	cfg, err := s.LoadAIConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil || cfg.AgentMaxSteps != 16 {
		t.Fatalf("agent max steps = %d, want default 16", cfg.AgentMaxSteps)
	}
}

func TestMigrationAddsEmbeddingModelToLegacyAIConfig(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy-ai.db"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Store{db: db}
	t.Cleanup(func() { _ = s.Close() })

	if _, err := db.Exec(`
		CREATE TABLE ai_config (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			provider TEXT,
			model TEXT,
			api_key TEXT,
			base_url TEXT,
			max_tokens INTEGER DEFAULT 2000,
			temperature REAL DEFAULT 0.7
		)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO ai_config (id, provider, model, api_key, base_url) VALUES (1, 'openai', 'gpt-4o', 'key', 'https://example.test/v1')`); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}

	cfg, err := s.LoadAIConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil || cfg.EmbeddingModel != "text-embedding-3-small" || cfg.APIMode != "chat" || cfg.RAGEnabled || cfg.MaxTokens != 393216 {
		t.Fatalf("legacy AI config migration produced unexpected config: %+v", cfg)
	}
}
