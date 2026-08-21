// owner: muswood | Email: mumu920@outlook.com
// Package skills provides declarative, auditable Agent workflow templates.
package skills

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const currentVersion = "1.0.0"

type Parameter struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Default     any      `json:"default,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

type Dependency struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Required bool   `json:"required,omitempty"`
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

type Signature struct {
	Signer string `json:"signer"`
	Value  string `json:"value"`
}

type Manifest struct {
	ID               string               `json:"id"`
	Name             string               `json:"name"`
	Version          string               `json:"version"`
	Description      string               `json:"description"`
	Mode             string               `json:"mode,omitempty"`
	AllowedTools     []string             `json:"allowedTools"`
	MaxSteps         int                  `json:"maxSteps,omitempty"`
	TimeoutSeconds   int                  `json:"timeoutSeconds,omitempty"`
	RequiresApproval bool                 `json:"requiresApproval,omitempty"`
	Prompt           string               `json:"prompt"`
	Parameters       map[string]Parameter `json:"parameters,omitempty"`
	Category         string               `json:"category,omitempty"`
	Tags             []string             `json:"tags,omitempty"`
	Dependencies     []Dependency         `json:"dependencies,omitempty"`
	Workflow         []WorkflowStep       `json:"workflow,omitempty"`
	ReportTemplate   string               `json:"reportTemplate,omitempty"`
	Signature        *Signature           `json:"signature,omitempty"`
	IntegrityHash    string               `json:"integrityHash,omitempty"`
	TrustStatus      string               `json:"trustStatus,omitempty"`
	Source           string               `json:"source,omitempty"`
	Enabled          bool                 `json:"enabled"`
	Builtin          bool                 `json:"builtin,omitempty"`
}

type Registry struct {
	dir      string
	builtins map[string]Manifest
}

func NewRegistry(dir string) (*Registry, error) {
	if strings.TrimSpace(dir) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(home, ".gossh", "skills")
	}
	r := &Registry{dir: dir, builtins: make(map[string]Manifest)}
	for _, manifest := range builtinManifests() {
		if err := Validate(manifest); err != nil {
			return nil, fmt.Errorf("内置 Skill %s 无效: %w", manifest.ID, err)
		}
		manifest.Builtin = true
		manifest.Enabled = true
		manifest.TrustStatus = "builtin"
		r.builtins[manifest.ID] = manifest
	}
	return r, nil
}

func Validate(manifest Manifest) error {
	manifest.ID = strings.TrimSpace(manifest.ID)
	manifest.Name = strings.TrimSpace(manifest.Name)
	manifest.Version = strings.TrimSpace(manifest.Version)
	manifest.Prompt = strings.TrimSpace(manifest.Prompt)
	if !validStandardSkillName(manifest.ID) {
		return errors.New("Skill name 必须是 1-64 个小写字母、数字或连字符")
	}
	if manifest.Name == "" || len(manifest.Name) > 120 {
		return errors.New("name 不能为空且不能超过 120 个字符")
	}
	if manifest.Version == "" {
		return errors.New("version 不能为空")
	}
	if manifest.Prompt == "" || len(manifest.Prompt) > 12000 {
		return errors.New("prompt 不能为空且不能超过 12000 个字符")
	}
	if len(manifest.Tags) > 30 || len(manifest.Dependencies) > 30 || len(manifest.Workflow) > 20 {
		return errors.New("tags、dependencies 或 workflow 超过允许数量")
	}
	if len(manifest.ReportTemplate) > 12000 {
		return errors.New("reportTemplate 不能超过 12000 个字符")
	}
	if manifest.MaxSteps < 0 || manifest.MaxSteps > 50 {
		return errors.New("maxSteps 必须在 0-50 之间")
	}
	if manifest.TimeoutSeconds < 0 || manifest.TimeoutSeconds > 3600 {
		return errors.New("timeoutSeconds 必须在 0-3600 之间")
	}
	for i, name := range manifest.AllowedTools {
		if strings.TrimSpace(name) == "" || len(name) > 120 {
			return fmt.Errorf("allowedTools[%d] 无效", i)
		}
	}
	for name, param := range manifest.Parameters {
		if !validID(name) {
			return fmt.Errorf("参数名无效: %s", name)
		}
		switch strings.ToLower(param.Type) {
		case "string", "integer", "number", "boolean":
		default:
			return fmt.Errorf("参数 %s 的 type 不支持: %s", name, param.Type)
		}
		if len(param.Enum) > 100 {
			return fmt.Errorf("参数 %s 的 enum 过长", name)
		}
	}
	for _, dependency := range manifest.Dependencies {
		if dependency.Name == "" || (dependency.Kind != "tool" && dependency.Kind != "mcp" && dependency.Kind != "rag") {
			return fmt.Errorf("依赖定义无效: %s/%s", dependency.Kind, dependency.Name)
		}
	}
	for _, step := range manifest.Workflow {
		if !validID(step.ID) || strings.TrimSpace(step.Title) == "" || strings.TrimSpace(step.Prompt) == "" {
			return fmt.Errorf("工作流步骤无效: %s", step.ID)
		}
		if step.Repeat < 0 || step.Repeat > 20 || step.MaxAttempts < 0 || step.MaxAttempts > 5 {
			return fmt.Errorf("工作流步骤重试或重复次数无效: %s", step.ID)
		}
	}
	return nil
}

func validID(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func (r *Registry) skillDir(id string) string { return filepath.Join(r.dir, id) }

func (r *Registry) skillPath(id string) string {
	return filepath.Join(r.skillDir(id), standardSkillFile)
}

func (r *Registry) List() ([]Manifest, error) {
	items := make(map[string]Manifest, len(r.builtins))
	for id, manifest := range r.builtins {
		items[id] = manifest
	}
	entries, err := os.ReadDir(r.dir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		manifest, loadErr := r.loadFile(filepath.Join(r.dir, entry.Name(), standardSkillFile))
		if loadErr != nil {
			continue
		}
		manifest.Builtin = false
		items[manifest.ID] = manifest
	}
	result := make([]Manifest, 0, len(items))
	for _, manifest := range items {
		result = append(result, manifest)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (r *Registry) Get(id string) (Manifest, error) {
	if manifest, ok := r.builtins[id]; ok {
		return manifest, nil
	}
	if !validStandardSkillName(id) {
		return Manifest{}, errors.New("Skill ID 无效")
	}
	return r.loadFile(r.skillPath(id))
}

func (r *Registry) Save(manifest Manifest) error {
	if err := Validate(manifest); err != nil {
		return err
	}
	if _, builtin := r.builtins[manifest.ID]; builtin {
		return errors.New("内置 Skill 不能覆盖")
	}
	if existing, err := r.Get(manifest.ID); err == nil && !existing.Builtin {
		if err := r.saveHistory(existing); err != nil {
			return err
		}
	}
	manifest = normalizeStandardManifest(manifest)
	if err := Validate(manifest); err != nil {
		return err
	}
	if err := os.MkdirAll(r.skillDir(manifest.ID), 0700); err != nil {
		return err
	}
	manifest.Builtin = false
	if manifest.Source == "" {
		manifest.Source = "local"
	}
	manifest.IntegrityHash = IntegrityHash(manifest)
	manifest.TrustStatus = r.trustStatus(manifest)
	raw, err := MarshalMarkdown(manifest)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(r.skillDir(manifest.ID), ".skill-*.md")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, r.skillPath(manifest.ID)); err != nil {
		return err
	}
	return nil
}

func IntegrityHash(manifest Manifest) string {
	manifest.Signature = nil
	manifest.IntegrityHash = ""
	manifest.TrustStatus = ""
	manifest.Builtin = false
	raw, _ := json.Marshal(manifest)
	digest := sha256.Sum256(raw)
	return fmt.Sprintf("sha256:%x", digest[:])
}

func (r *Registry) Search(query string) ([]Manifest, error) {
	items, err := r.List()
	if err != nil || strings.TrimSpace(query) == "" {
		return items, err
	}
	needle := strings.ToLower(strings.TrimSpace(query))
	filtered := make([]Manifest, 0, len(items))
	for _, item := range items {
		haystack := strings.ToLower(strings.Join(append([]string{item.ID, item.Name, item.Description, item.Category}, item.Tags...), " "))
		if strings.Contains(haystack, needle) {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func (r *Registry) History(id string) ([]Manifest, error) {
	if !validStandardSkillName(id) {
		return nil, errors.New("Skill ID 无效")
	}
	dir := filepath.Join(r.dir, ".history", id)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []Manifest{}, nil
	}
	if err != nil {
		return nil, err
	}
	items := make([]Manifest, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		manifest, loadErr := r.loadManifest(filepath.Join(dir, entry.Name()), false)
		if loadErr == nil {
			manifest.Version = strings.TrimSuffix(entry.Name(), ".md")
			items = append(items, manifest)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Version > items[j].Version })
	return items, nil
}

func (r *Registry) Restore(id, version string) error {
	history, err := r.History(id)
	if err != nil {
		return err
	}
	for _, item := range history {
		if item.Version == version {
			if item.Signature == nil {
				item.Source = "restored"
			}
			return r.Save(item)
		}
	}
	return fmt.Errorf("未找到 Skill 历史版本: %s", version)
}

func (r *Registry) TrustKey(signer, publicKeyBase64 string) error {
	if !validID(signer) {
		return errors.New("签名者标识无效")
	}
	key, err := base64.StdEncoding.DecodeString(publicKeyBase64)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return errors.New("Ed25519 公钥无效")
	}
	keys, err := r.trustedKeys()
	if err != nil {
		return err
	}
	keys[signer] = publicKeyBase64
	raw, _ := json.MarshalIndent(keys, "", "  ")
	if err := os.MkdirAll(r.dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.dir, "trusted-keys.json"), raw, 0600)
}

func (r *Registry) RevokeKey(signer string) error {
	if !validID(signer) {
		return errors.New("签名者标识无效")
	}
	keys, err := r.trustedKeys()
	if err != nil {
		return err
	}
	delete(keys, signer)
	raw, _ := json.MarshalIndent(keys, "", "  ")
	if err := os.MkdirAll(r.dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.dir, "trusted-keys.json"), raw, 0600)
}

func GenerateSigningKey() (string, string, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(publicKey), base64.StdEncoding.EncodeToString(privateKey), nil
}

func Sign(manifest Manifest, signer, privateKeyBase64 string) (Manifest, error) {
	if !validID(signer) {
		return Manifest{}, errors.New("签名者标识无效")
	}
	privateKey, err := base64.StdEncoding.DecodeString(privateKeyBase64)
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		return Manifest{}, errors.New("Ed25519 私钥无效")
	}
	manifest = normalizeStandardManifest(manifest)
	if err := Validate(manifest); err != nil {
		return Manifest{}, err
	}
	if manifest.Source == "" {
		manifest.Source = "local"
	}
	manifest.Builtin = false
	manifest.Signature = &Signature{Signer: signer}
	manifest.IntegrityHash, manifest.TrustStatus = "", ""
	payload := manifest
	payload.Signature = nil
	raw, err := json.Marshal(payload)
	if err != nil {
		return Manifest{}, err
	}
	manifest.Signature.Value = base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.PrivateKey(privateKey), raw))
	manifest.IntegrityHash = IntegrityHash(manifest)
	return manifest, nil
}

func (r *Registry) saveHistory(manifest Manifest) error {
	dir := filepath.Join(r.dir, ".history", manifest.ID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	raw, err := MarshalMarkdown(manifest)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%s-%d.md", strings.ReplaceAll(manifest.Version, "/", "-"), time.Now().UnixNano())
	return os.WriteFile(filepath.Join(dir, name), raw, 0600)
}

func (r *Registry) Delete(id string) error {
	if _, builtin := r.builtins[id]; builtin {
		return errors.New("内置 Skill 不能删除")
	}
	if !validStandardSkillName(id) {
		return errors.New("Skill ID 无效")
	}
	err := os.RemoveAll(r.skillDir(id))
	if os.IsNotExist(err) {
		return r.clearEnabledState(id)
	}
	if err != nil {
		return err
	}
	return r.clearEnabledState(id)
}

func (r *Registry) Enable(id string, enabled bool) error {
	manifest, err := r.Get(id)
	if err != nil {
		return err
	}
	if manifest.Builtin {
		return nil
	}
	return r.setEnabledState(id, enabled)
}

func (r *Registry) ExportUser() ([]Manifest, error) {
	result := make([]Manifest, 0)
	items, err := r.List()
	if err != nil {
		return nil, err
	}
	for _, manifest := range items {
		if !manifest.Builtin {
			result = append(result, manifest)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

// Export returns the persisted manifest rather than the effective runtime
// state. Local enablement overrides must not be written into signed content.
func (r *Registry) Export(id string) (Manifest, error) {
	if manifest, ok := r.builtins[id]; ok {
		return manifest, nil
	}
	if !validStandardSkillName(id) {
		return Manifest{}, errors.New("Skill ID 无效")
	}
	return r.loadManifest(r.skillPath(id), true)
}

// Document returns the canonical standard SKILL.md source used by the editor
// and file export. Built-ins are rendered on demand and remain read-only.
func (r *Registry) Document(id string) (string, error) {
	if manifest, ok := r.builtins[id]; ok {
		raw, err := MarshalMarkdown(manifest)
		return string(raw), err
	}
	if !validStandardSkillName(id) {
		return "", errors.New("Skill ID 无效")
	}
	raw, err := os.ReadFile(r.skillPath(id))
	if err != nil {
		return "", err
	}
	if _, err := ParseMarkdown(raw); err != nil {
		return "", err
	}
	return string(raw), nil
}

func (r *Registry) ImportUser(items []Manifest) error {
	for _, item := range items {
		item.Builtin = false
		if err := r.Save(item); err != nil {
			return err
		}
	}
	return nil
}

func (r *Registry) loadFile(path string) (Manifest, error) {
	manifest, err := r.loadManifest(path, true)
	if err != nil {
		return Manifest{}, err
	}
	enabled, present, err := r.enabledState(manifest.ID)
	if err != nil {
		return Manifest{}, err
	}
	if present {
		// Runtime state is deliberately applied after signature verification;
		// enabling a skill must not mutate its signed document.
		manifest.Enabled = enabled
	}
	return manifest, nil
}

func (r *Registry) enabledState(id string) (bool, bool, error) {
	raw, err := os.ReadFile(filepath.Join(r.dir, ".state", "enabled.json"))
	if os.IsNotExist(err) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	var states map[string]bool
	if err := json.Unmarshal(raw, &states); err != nil {
		return false, false, fmt.Errorf("Skill 启用状态文件无效: %w", err)
	}
	enabled, present := states[id]
	return enabled, present, nil
}

func (r *Registry) setEnabledState(id string, enabled bool) error {
	path := filepath.Join(r.dir, ".state", "enabled.json")
	states := make(map[string]bool)
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &states); err != nil {
			return fmt.Errorf("Skill 启用状态文件无效: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	states[id] = enabled
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(states, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".enabled-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func (r *Registry) clearEnabledState(id string) error {
	path := filepath.Join(r.dir, ".state", "enabled.json")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var states map[string]bool
	if err := json.Unmarshal(raw, &states); err != nil {
		return fmt.Errorf("Skill 启用状态文件无效: %w", err)
	}
	if _, present := states[id]; !present {
		return nil
	}
	delete(states, id)
	if len(states) == 0 {
		return os.Remove(path)
	}
	updated, err := json.MarshalIndent(states, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, updated, 0600)
}

func (r *Registry) loadManifest(path string, requireMatchingName bool) (Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	manifest, err := ParseMarkdown(raw)
	if err != nil {
		return Manifest{}, err
	}
	if err := Validate(manifest); err != nil {
		return Manifest{}, err
	}
	if requireMatchingName && (filepath.Base(path) != standardSkillFile || filepath.Base(filepath.Dir(path)) != manifest.ID) {
		return Manifest{}, errors.New("Skill 文件名与 id 不匹配")
	}
	manifest.IntegrityHash = IntegrityHash(manifest)
	manifest.TrustStatus = r.trustStatus(manifest)
	return manifest, nil
}

func (r *Registry) trustStatus(manifest Manifest) string {
	if manifest.Builtin {
		return "builtin"
	}
	if manifest.Signature == nil || manifest.Signature.Signer == "" || manifest.Signature.Value == "" {
		return "unverified"
	}
	keys, err := r.trustedKeys()
	if err != nil {
		return "invalid"
	}
	encodedKey := keys[manifest.Signature.Signer]
	publicKey, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return "untrusted"
	}
	signature, err := base64.StdEncoding.DecodeString(manifest.Signature.Value)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return "invalid"
	}
	payload := manifest
	payload.Signature = nil
	payload.IntegrityHash = ""
	payload.TrustStatus = ""
	payload.Builtin = false
	raw, _ := json.Marshal(payload)
	if ed25519.Verify(ed25519.PublicKey(publicKey), raw, signature) {
		return "verified"
	}
	return "invalid"
}

func (r *Registry) trustedKeys() (map[string]string, error) {
	keys := make(map[string]string)
	raw, err := os.ReadFile(filepath.Join(r.dir, "trusted-keys.json"))
	if os.IsNotExist(err) {
		return keys, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil, fmt.Errorf("可信公钥文件无效: %w", err)
	}
	return keys, nil
}

func builtinManifests() []Manifest {
	return []Manifest{
		{ID: "linux-health-check", Name: "Linux 健康检查", Version: currentVersion, Description: "检查 CPU、内存、磁盘、负载和关键服务。", Mode: "diagnose", AllowedTools: []string{"terminal_command", "gossh_diagnostics", "report"}, MaxSteps: 16, TimeoutSeconds: 0, Prompt: "以只读方式检查 Linux 主机健康状态，重点覆盖 CPU、内存、磁盘、负载和关键服务。根据实际工具证据给出分级结论。"},
		{ID: "service-diagnosis", Name: "服务故障诊断", Version: currentVersion, Description: "围绕指定服务收集状态和日志证据。", Mode: "diagnose", AllowedTools: []string{"terminal_command", "rag_search", "report"}, MaxSteps: 16, TimeoutSeconds: 0, Prompt: "诊断指定服务的故障。先收集服务状态、最近日志和相关知识库证据，只读检查，不修改系统。", Parameters: map[string]Parameter{"service": {Type: "string", Description: "服务名称，例如 nginx 或 sshd", Required: true}}},
		{ID: "remote-file-audit", Name: "远端文件审计", Version: currentVersion, Description: "读取并审计远端配置文件，输出风险和建议。", Mode: "file_analysis", AllowedTools: []string{"sftp_read_file", "report"}, MaxSteps: 12, TimeoutSeconds: 300, Prompt: "读取指定远端文件，审计配置、敏感信息暴露和生产风险。内容可能已脱敏，禁止猜测凭据，不执行修改。", Parameters: map[string]Parameter{"path": {Type: "string", Description: "远端文件路径", Required: true}}},
	}
}
