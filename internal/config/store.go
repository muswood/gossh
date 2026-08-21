// owner: muswood | Email: mumu920@outlook.com
package config

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type ConnectionRecord struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	Protocol            string    `json:"protocol,omitempty"`
	Host                string    `json:"host"`
	Port                int       `json:"port"`
	Username            string    `json:"username"`
	AuthMethod          string    `json:"authMethod"`
	Password            string    `json:"password,omitempty"`
	PrivateKey          string    `json:"privateKey,omitempty"`
	PrivateKeyPath      string    `json:"privateKeyPath,omitempty"`
	CertificatePath     string    `json:"certificatePath,omitempty"`
	Passphrase          string    `json:"passphrase,omitempty"`
	JumpHost            string    `json:"jumpHost,omitempty"`
	ProxyType           string    `json:"proxyType,omitempty"`
	ProxyHost           string    `json:"proxyHost,omitempty"`
	ProxyUsername       string    `json:"proxyUsername,omitempty"`
	ProxyPassword       string    `json:"proxyPassword,omitempty"`
	ProxyCommand        string    `json:"proxyCommand,omitempty"`
	Encoding            string    `json:"encoding"`
	StartupCmd          string    `json:"startupCmd,omitempty"`
	KeepAlive           int       `json:"keepAliveSeconds"`
	TerminalTheme       string    `json:"terminalTheme,omitempty"`
	SerialBaudRate      int       `json:"serialBaudRate,omitempty"`
	SerialDataBits      int       `json:"serialDataBits,omitempty"`
	SerialStopBits      int       `json:"serialStopBits,omitempty"`
	SerialParity        string    `json:"serialParity,omitempty"`
	SerialAutoReconnect bool      `json:"serialAutoReconnect,omitempty"`
	GroupID             string    `json:"groupId"`
	Starred             bool      `json:"starred"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

type ConnectionGroup struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type AIProviderConfig struct {
	Provider            string  `json:"provider"`
	Model               string  `json:"model"`
	EmbeddingModel      string  `json:"embeddingModel,omitempty"`
	APIKey              string  `json:"apiKey"`
	BaseURL             string  `json:"baseURL"`
	APIMode             string  `json:"apiMode,omitempty"`
	RAGEnabled          bool    `json:"ragEnabled"`
	RAGVectorBackend    string  `json:"ragVectorBackend,omitempty"`
	RAGVectorEndpoint   string  `json:"ragVectorEndpoint,omitempty"`
	RAGVectorCollection string  `json:"ragVectorCollection,omitempty"`
	RAGVectorAPIKey     string  `json:"ragVectorApiKey,omitempty"`
	MaxTokens           int     `json:"maxTokens"`
	Temperature         float64 `json:"temperature"`
	AgentMaxSteps       int     `json:"agentMaxSteps"`
}

type Settings struct {
	TerminalTheme string `json:"terminalTheme"`
	UITheme       string `json:"uiTheme"`
	FontSize      int    `json:"fontSize"`
	ShowSidebar   bool   `json:"showSidebar"`
	ShowAI        bool   `json:"showAI"`
}

type Store struct {
	db  *sql.DB
	dir string
}

func NewStore() (*Store, error) {
	dir, err := getDataDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", filepath.Join(dir, "gossh.db"))
	if err != nil {
		return nil, err
	}

	s := &Store{db: db, dir: dir}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func getDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gossh"), nil
}

func (s *Store) migrate() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS connection_groups (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS connections (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			protocol TEXT DEFAULT 'ssh',
			host TEXT NOT NULL,
			port INTEGER DEFAULT 22,
			username TEXT NOT NULL,
			auth_method TEXT DEFAULT 'password',
			password TEXT,
			private_key TEXT,
			private_key_path TEXT,
			certificate_path TEXT,
			passphrase TEXT,
			jump_host TEXT,
			proxy_type TEXT,
			proxy_host TEXT,
			proxy_username TEXT,
			proxy_password TEXT,
			proxy_command TEXT,
			encoding TEXT DEFAULT 'utf-8',
			startup_cmd TEXT,
			keep_alive INTEGER DEFAULT 30,
			terminal_theme TEXT DEFAULT '',
			serial_baud_rate INTEGER DEFAULT 115200,
			serial_data_bits INTEGER DEFAULT 8,
			serial_stop_bits INTEGER DEFAULT 1,
			serial_parity TEXT DEFAULT 'none',
			serial_auto_reconnect INTEGER DEFAULT 1,
			group_id TEXT,
			starred INTEGER DEFAULT 0,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS ai_config (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			provider TEXT,
			model TEXT,
			api_key TEXT,
			base_url TEXT,
			api_mode TEXT DEFAULT 'chat',
			rag_enabled INTEGER DEFAULT 0,
			rag_vector_backend TEXT DEFAULT '',
			rag_vector_endpoint TEXT DEFAULT '',
			rag_vector_collection TEXT DEFAULT '',
			rag_vector_api_key TEXT DEFAULT '',
			max_tokens INTEGER DEFAULT 393216,
			temperature REAL DEFAULT 0.7,
			agent_max_steps INTEGER DEFAULT 16,
			embedding_model TEXT DEFAULT 'text-embedding-3-small'
		)`,
	}
	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("执行迁移失败: %w", err)
		}
	}
	// Upgrade the old implicit default. The settings UI does not expose a
	// user-selectable token limit, so 2000 is legacy state, not user intent.
	if _, err := s.db.Exec(`UPDATE ai_config SET max_tokens = 393216 WHERE max_tokens = 2000`); err != nil {
		return fmt.Errorf("迁移 AI 最大输出令牌配置失败: %w", err)
	}
	// Existing databases predate the optional IdentityFile path column.
	if _, err := s.db.Exec(`ALTER TABLE connections ADD COLUMN private_key_path TEXT`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("迁移私钥路径失败: %w", err)
	}
	if _, err := s.db.Exec(`ALTER TABLE connections ADD COLUMN certificate_path TEXT`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("迁移证书路径失败: %w", err)
	}
	if _, err := s.db.Exec(`ALTER TABLE connections ADD COLUMN terminal_theme TEXT DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("迁移终端主题失败: %w", err)
	}
	for _, column := range []string{"proxy_type TEXT", "proxy_host TEXT", "proxy_username TEXT", "proxy_password TEXT", "proxy_command TEXT"} {
		if _, err := s.db.Exec(`ALTER TABLE connections ADD COLUMN ` + column); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return fmt.Errorf("迁移代理配置失败: %w", err)
		}
	}
	if _, err := s.db.Exec(`ALTER TABLE connections ADD COLUMN protocol TEXT DEFAULT 'ssh'`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("迁移连接协议失败: %w", err)
	}
	for _, column := range []string{"serial_baud_rate INTEGER DEFAULT 115200", "serial_data_bits INTEGER DEFAULT 8", "serial_stop_bits INTEGER DEFAULT 1", "serial_parity TEXT DEFAULT 'none'", "serial_auto_reconnect INTEGER DEFAULT 1"} {
		if _, err := s.db.Exec(`ALTER TABLE connections ADD COLUMN ` + column); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return fmt.Errorf("迁移串口配置失败: %w", err)
		}
	}
	if _, err := s.db.Exec(`ALTER TABLE ai_config ADD COLUMN api_mode TEXT DEFAULT 'chat'`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("迁移 AI API 模式失败: %w", err)
	}
	if _, err := s.db.Exec(`ALTER TABLE ai_config ADD COLUMN rag_enabled INTEGER DEFAULT 0`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("迁移 AI 知识库设置失败: %w", err)
	}
	if _, err := s.db.Exec(`ALTER TABLE ai_config ADD COLUMN embedding_model TEXT DEFAULT 'text-embedding-3-small'`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("迁移 Embedding 模型配置失败: %w", err)
	}
	for _, column := range []string{"rag_vector_backend TEXT DEFAULT ''", "rag_vector_endpoint TEXT DEFAULT ''", "rag_vector_collection TEXT DEFAULT ''", "rag_vector_api_key TEXT DEFAULT ''"} {
		if _, err := s.db.Exec(`ALTER TABLE ai_config ADD COLUMN ` + column); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return fmt.Errorf("迁移 RAG 向量后端配置失败: %w", err)
		}
	}
	if _, err := s.db.Exec(`ALTER TABLE ai_config ADD COLUMN agent_max_steps INTEGER DEFAULT 16`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("迁移 Agent 迭代上限配置失败: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE ai_config SET agent_max_steps = 16 WHERE agent_max_steps IS NULL OR agent_max_steps < 1 OR agent_max_steps > 50`); err != nil {
		return fmt.Errorf("规范化 Agent 迭代上限配置失败: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) SaveConnection(conn ConnectionRecord) error {
	conn.UpdatedAt = time.Now()
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO connections
		(id, name, protocol, host, port, username, auth_method, password, private_key, private_key_path, certificate_path, passphrase,
		 jump_host, proxy_type, proxy_host, proxy_username, proxy_password, proxy_command, encoding, startup_cmd, keep_alive, terminal_theme,
		 serial_baud_rate, serial_data_bits, serial_stop_bits, serial_parity, serial_auto_reconnect, group_id, starred, updated_at)
		VALUES (
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		)`,
		conn.ID, conn.Name, firstNonEmptyProtocol(conn.Protocol), conn.Host, conn.Port, conn.Username,
		conn.AuthMethod, conn.Password, conn.PrivateKey, conn.PrivateKeyPath, conn.CertificatePath, conn.Passphrase,
		conn.JumpHost, conn.ProxyType, conn.ProxyHost, conn.ProxyUsername, conn.ProxyPassword, conn.ProxyCommand, conn.Encoding, conn.StartupCmd, conn.KeepAlive, conn.TerminalTheme,
		conn.SerialBaudRate, conn.SerialDataBits, conn.SerialStopBits, conn.SerialParity, conn.SerialAutoReconnect,
		conn.GroupID, conn.Starred, conn.UpdatedAt,
	)
	return err
}

func (s *Store) ListConnections() ([]ConnectionRecord, error) {
	rows, err := s.db.Query(`
		SELECT id, name, COALESCE(protocol, 'ssh'), host, port, username, auth_method,
		       COALESCE(password, ''), COALESCE(private_key, ''), COALESCE(private_key_path, ''), COALESCE(certificate_path, ''),
		       COALESCE(passphrase, ''), COALESCE(jump_host, ''), COALESCE(proxy_type, ''), COALESCE(proxy_host, ''), COALESCE(proxy_username, ''), COALESCE(proxy_password, ''), COALESCE(proxy_command, ''), COALESCE(encoding, 'utf-8'), COALESCE(startup_cmd, ''),
		       COALESCE(keep_alive, 30), COALESCE(terminal_theme, ''), COALESCE(serial_baud_rate, 115200), COALESCE(serial_data_bits, 8), COALESCE(serial_stop_bits, 1), COALESCE(serial_parity, 'none'), COALESCE(serial_auto_reconnect, 1), COALESCE(group_id, ''), starred, updated_at
		FROM connections ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	conns := make([]ConnectionRecord, 0)
	for rows.Next() {
		var c ConnectionRecord
		var starred int
		err := rows.Scan(&c.ID, &c.Name, &c.Protocol, &c.Host, &c.Port, &c.Username,
			&c.AuthMethod, &c.Password, &c.PrivateKey, &c.PrivateKeyPath, &c.CertificatePath, &c.Passphrase,
			&c.JumpHost, &c.ProxyType, &c.ProxyHost, &c.ProxyUsername, &c.ProxyPassword, &c.ProxyCommand, &c.Encoding, &c.StartupCmd, &c.KeepAlive, &c.TerminalTheme,
			&c.SerialBaudRate, &c.SerialDataBits, &c.SerialStopBits, &c.SerialParity, &c.SerialAutoReconnect,
			&c.GroupID, &starred, &c.UpdatedAt)
		if err != nil {
			return nil, err
		}
		c.Starred = starred != 0
		conns = append(conns, c)
	}
	return conns, nil
}

func (s *Store) FindConnection(id string) (*ConnectionRecord, error) {
	row := s.db.QueryRow(`
		SELECT id, name, COALESCE(protocol, 'ssh'), host, port, username, auth_method,
		       COALESCE(password, ''), COALESCE(private_key, ''), COALESCE(private_key_path, ''), COALESCE(certificate_path, ''),
		       COALESCE(passphrase, ''), COALESCE(jump_host, ''), COALESCE(proxy_type, ''), COALESCE(proxy_host, ''), COALESCE(proxy_username, ''), COALESCE(proxy_password, ''), COALESCE(proxy_command, ''), COALESCE(encoding, 'utf-8'), COALESCE(startup_cmd, ''),
		       COALESCE(keep_alive, 30), COALESCE(terminal_theme, ''), COALESCE(serial_baud_rate, 115200), COALESCE(serial_data_bits, 8), COALESCE(serial_stop_bits, 1), COALESCE(serial_parity, 'none'), COALESCE(serial_auto_reconnect, 1), COALESCE(group_id, ''), starred, updated_at
		FROM connections WHERE id = ?`, id)
	var c ConnectionRecord
	var starred int
	if err := row.Scan(&c.ID, &c.Name, &c.Protocol, &c.Host, &c.Port, &c.Username,
		&c.AuthMethod, &c.Password, &c.PrivateKey, &c.PrivateKeyPath, &c.CertificatePath, &c.Passphrase,
		&c.JumpHost, &c.ProxyType, &c.ProxyHost, &c.ProxyUsername, &c.ProxyPassword, &c.ProxyCommand, &c.Encoding, &c.StartupCmd, &c.KeepAlive, &c.TerminalTheme,
		&c.SerialBaudRate, &c.SerialDataBits, &c.SerialStopBits, &c.SerialParity, &c.SerialAutoReconnect,
		&c.GroupID, &starred, &c.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	c.Starred = starred != 0
	return &c, nil
}

func (s *Store) DeleteConnection(id string) error {
	_, err := s.db.Exec(`DELETE FROM connections WHERE id = ?`, id)
	return err
}

// SetConnectionGroup changes only the group relation, preserving all other
// connection fields including encrypted credentials.
func (s *Store) SetConnectionGroup(connectionID, groupID string) error {
	if strings.TrimSpace(connectionID) == "" {
		return fmt.Errorf("连接 ID 不能为空")
	}
	if strings.TrimSpace(groupID) == "ungrouped" {
		groupID = ""
	}
	result, err := s.db.Exec(`UPDATE connections SET group_id = ?, updated_at = ? WHERE id = ?`, groupID, time.Now(), connectionID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected == 0 {
		return fmt.Errorf("连接 %s 不存在", connectionID)
	}
	return nil
}

func (s *Store) SaveGroup(g ConnectionGroup) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO connection_groups (id, name) VALUES (?, ?)`, g.ID, g.Name)
	return err
}

func (s *Store) ListGroups() ([]ConnectionGroup, error) {
	rows, err := s.db.Query(`SELECT id, name FROM connection_groups`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := make([]ConnectionGroup, 0)
	for rows.Next() {
		var g ConnectionGroup
		if err := rows.Scan(&g.ID, &g.Name); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, nil
}

func (s *Store) DeleteGroup(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE connections SET group_id = '' WHERE group_id = ?`, id); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err = tx.Exec(`DELETE FROM connection_groups WHERE id = ?`, id); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Store) SaveAIConfig(cfg AIProviderConfig) error {
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO ai_config (id, provider, model, embedding_model, api_key, base_url, api_mode, rag_enabled, rag_vector_backend, rag_vector_endpoint, rag_vector_collection, rag_vector_api_key, max_tokens, temperature, agent_max_steps)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cfg.Provider, cfg.Model, cfg.EmbeddingModel, cfg.APIKey, cfg.BaseURL, cfg.APIMode, cfg.RAGEnabled, cfg.RAGVectorBackend, cfg.RAGVectorEndpoint, cfg.RAGVectorCollection, cfg.RAGVectorAPIKey, cfg.MaxTokens, cfg.Temperature, normalizedAgentMaxSteps(cfg.AgentMaxSteps))
	return err
}

func (s *Store) LoadAIConfig() (*AIProviderConfig, error) {
	row := s.db.QueryRow(`SELECT provider, model, COALESCE(embedding_model, 'text-embedding-3-small'), api_key, base_url, COALESCE(api_mode, 'chat'), COALESCE(rag_enabled, 0), COALESCE(rag_vector_backend, ''), COALESCE(rag_vector_endpoint, ''), COALESCE(rag_vector_collection, ''), COALESCE(rag_vector_api_key, ''), max_tokens, temperature, COALESCE(agent_max_steps, 16) FROM ai_config WHERE id = 1`)
	var cfg AIProviderConfig
	var ragEnabled int
	err := row.Scan(&cfg.Provider, &cfg.Model, &cfg.EmbeddingModel, &cfg.APIKey, &cfg.BaseURL, &cfg.APIMode, &ragEnabled, &cfg.RAGVectorBackend, &cfg.RAGVectorEndpoint, &cfg.RAGVectorCollection, &cfg.RAGVectorAPIKey, &cfg.MaxTokens, &cfg.Temperature, &cfg.AgentMaxSteps)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	cfg.RAGEnabled = ragEnabled != 0
	cfg.AgentMaxSteps = normalizedAgentMaxSteps(cfg.AgentMaxSteps)
	return &cfg, err
}

func normalizedAgentMaxSteps(value int) int {
	if value < 1 || value > 50 {
		return 16
	}
	return value
}

func (s *Store) SaveSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)`, key, value)
	return err
}

func (s *Store) LoadSetting(key string) (string, error) {
	row := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key)
	var value string
	err := row.Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func (s *Store) ExportAll() (string, error) {
	groups, err := s.ListGroups()
	if err != nil {
		return "", err
	}
	conns, err := s.ListConnections()
	if err != nil {
		return "", err
	}
	data := map[string]interface{}{"groups": groups, "connections": conns}
	b, err := json.MarshalIndent(data, "", "  ")
	return string(b), err
}

func (s *Store) ImportAll(jsonData string) error {
	var data struct {
		Groups      []ConnectionGroup  `json:"groups"`
		Connections []ConnectionRecord `json:"connections"`
	}
	if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	rollback := func(e error) error {
		_ = tx.Rollback()
		return e
	}
	for _, g := range data.Groups {
		if g.ID == "" || g.Name == "" {
			return rollback(fmt.Errorf("导入分组缺少 id 或名称"))
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO connection_groups (id, name) VALUES (?, ?)`, g.ID, g.Name); err != nil {
			return rollback(err)
		}
	}
	for _, c := range data.Connections {
		if c.ID == "" || c.Host == "" {
			return rollback(fmt.Errorf("导入连接缺少 id 或主机地址"))
		}
		if _, err := tx.Exec(`
			INSERT OR REPLACE INTO connections
			(id, name, protocol, host, port, username, auth_method, password, private_key, private_key_path, certificate_path, passphrase,
			 jump_host, proxy_type, proxy_host, proxy_username, proxy_password, proxy_command, encoding, startup_cmd, keep_alive, group_id, starred, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			c.ID, c.Name, firstNonEmptyProtocol(c.Protocol), c.Host, c.Port, c.Username, c.AuthMethod, c.Password,
			c.PrivateKey, c.PrivateKeyPath, c.CertificatePath, c.Passphrase, c.JumpHost, c.ProxyType, c.ProxyHost, c.ProxyUsername, c.ProxyPassword, c.ProxyCommand, c.Encoding, c.StartupCmd,
			c.KeepAlive, c.GroupID, c.Starred, c.UpdatedAt); err != nil {
			return rollback(err)
		}
	}
	return tx.Commit()
}

func firstNonEmptyProtocol(value string) string {
	if strings.TrimSpace(value) == "" {
		return "ssh"
	}
	return value
}
