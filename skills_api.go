// owner: muswood | Email: mumu920@outlook.com
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"gossh/internal/agent"
	"gossh/internal/skills"
)

type SkillRunRequest struct {
	SkillID    string             `json:"skillId"`
	Parameters map[string]any     `json:"parameters,omitempty"`
	Request    agent.StartRequest `json:"request"`
}

func (a *App) ensureSkillRegistry() (*skills.Registry, error) {
	if a.skillRegistry == nil {
		registry, err := skills.NewRegistry("")
		if err != nil {
			return nil, err
		}
		a.skillRegistry = registry
	}
	return a.skillRegistry, nil
}

func (a *App) SkillList() (string, error) {
	registry, err := a.ensureSkillRegistry()
	if err != nil {
		return "", err
	}
	items, err := registry.List()
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(items)
	return string(raw), err
}

func (a *App) SkillGet(id string) (string, error) {
	registry, err := a.ensureSkillRegistry()
	if err != nil {
		return "", err
	}
	item, err := registry.Get(strings.TrimSpace(id))
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(item)
	return string(raw), err
}

func (a *App) SkillSearch(query string) (string, error) {
	registry, err := a.ensureSkillRegistry()
	if err != nil {
		return "", err
	}
	items, err := registry.Search(query)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(items)
	return string(raw), err
}

func (a *App) SkillHistory(id string) (string, error) {
	registry, err := a.ensureSkillRegistry()
	if err != nil {
		return "", err
	}
	items, err := registry.History(strings.TrimSpace(id))
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(items)
	return string(raw), err
}

func (a *App) SkillRestore(id, version string) error {
	registry, err := a.ensureSkillRegistry()
	if err != nil {
		return err
	}
	return registry.Restore(strings.TrimSpace(id), strings.TrimSpace(version))
}

func (a *App) SkillTrustKey(signer, publicKey string) error {
	registry, err := a.ensureSkillRegistry()
	if err != nil {
		return err
	}
	return registry.TrustKey(strings.TrimSpace(signer), strings.TrimSpace(publicKey))
}

func (a *App) SkillRevokeKey(signer string) error {
	registry, err := a.ensureSkillRegistry()
	if err != nil {
		return err
	}
	return registry.RevokeKey(strings.TrimSpace(signer))
}

func (a *App) SkillGenerateSigningKey() (string, error) {
	publicKey, privateKey, err := skills.GenerateSigningKey()
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(map[string]string{"publicKey": publicKey, "privateKey": privateKey})
	return string(raw), err
}

func (a *App) SkillSign(document, signer, privateKey string) (string, error) {
	manifest, err := skills.ParseMarkdown([]byte(document))
	if err != nil {
		return "", err
	}
	signed, err := skills.Sign(manifest, strings.TrimSpace(signer), strings.TrimSpace(privateKey))
	if err != nil {
		return "", err
	}
	raw, err := skills.MarshalMarkdown(signed)
	return string(raw), err
}

func (a *App) SkillRotateKey(signer, publicKey string) error {
	return a.SkillTrustKey(signer, publicKey)
}

func (a *App) SkillCheck(id string) (string, error) {
	registry, err := a.ensureSkillRegistry()
	if err != nil {
		return "", err
	}
	manifest, err := registry.Get(strings.TrimSpace(id))
	if err != nil {
		return "", err
	}
	missing := a.skillMissingDependencies(manifest)
	result := map[string]any{"id": manifest.ID, "version": manifest.Version, "trustStatus": manifest.TrustStatus, "ok": len(missing) == 0, "missing": missing}
	raw, marshalErr := json.Marshal(result)
	return string(raw), marshalErr
}

func (a *App) skillMissingDependencies(manifest skills.Manifest) []string {
	available := map[string]bool{"report": true, "terminal_command": true, "terminal_command_many": true}
	if a.ragStore != nil {
		available["rag_search"] = true
		available["rag_search_many"] = true
	}
	if a.sshManager != nil {
		available["sftp_list_dir"] = true
		available["sftp_read_file"] = true
		available["sftp_list_dir_many"] = true
		available["sftp_read_file_many"] = true
		available["gossh_diagnostics"] = true
		available["gossh_diagnostics_many"] = true
	}
	a.mcpMu.Lock()
	for _, candidates := range a.mcpToolSets {
		for _, candidate := range candidates {
			if info, infoErr := candidate.Info(a.ctx); infoErr == nil && info != nil {
				available[info.Name] = true
			}
		}
	}
	a.mcpMu.Unlock()
	missing := make([]string, 0)
	for _, name := range manifest.AllowedTools {
		if !available[name] {
			missing = append(missing, "tool:"+name)
		}
	}
	for _, dependency := range manifest.Dependencies {
		if dependency.Kind == "rag" && a.ragStore == nil {
			missing = append(missing, "rag:"+dependency.Name)
		}
		if dependency.Kind == "tool" && !available[dependency.Name] {
			missing = append(missing, "tool:"+dependency.Name)
		}
		if dependency.Kind == "mcp" && !available[dependency.Name] {
			missing = append(missing, "mcp:"+dependency.Name)
		}
	}
	return missing
}

func (a *App) SkillSave(document string) error {
	registry, err := a.ensureSkillRegistry()
	if err != nil {
		return err
	}
	manifest, err := skills.ParseMarkdown([]byte(document))
	if err != nil {
		return err
	}
	return registry.Save(manifest)
}

func (a *App) SkillDocument(id string) (string, error) {
	registry, err := a.ensureSkillRegistry()
	if err != nil {
		return "", err
	}
	return registry.Document(strings.TrimSpace(id))
}

func (a *App) SkillExportToFile(id string) error {
	if a.ctx == nil {
		return errors.New("应用上下文未初始化")
	}
	registry, err := a.ensureSkillRegistry()
	if err != nil {
		return err
	}
	manifest, err := registry.Export(strings.TrimSpace(id))
	if err != nil {
		return err
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{Title: "导出 Skill", DefaultFilename: standardSkillExportName(manifest.ID), Filters: []runtime.FileFilter{{DisplayName: "Agent Skill Markdown", Pattern: "*.md"}}})
	if err != nil || path == "" {
		return err
	}
	raw, err := registry.Document(manifest.ID)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(raw), 0600)
}

func (a *App) SkillImportFromFile() error {
	if a.ctx == nil {
		return errors.New("应用上下文未初始化")
	}
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: "导入 Skill", Filters: []runtime.FileFilter{{DisplayName: "Agent Skill Markdown", Pattern: "*.md"}}})
	if err != nil || path == "" {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	manifest, err := skills.ParseMarkdown(raw)
	if err != nil {
		return err
	}
	if manifest.Signature == nil {
		manifest.Source = "imported"
	}
	manifest.Builtin = false
	registry, err := a.ensureSkillRegistry()
	if err != nil {
		return err
	}
	return registry.Save(manifest)
}

func standardSkillExportName(id string) string {
	if strings.TrimSpace(id) == "" {
		return "SKILL.md"
	}
	return id + "-SKILL.md"
}

func (a *App) SkillDelete(id string) error {
	registry, err := a.ensureSkillRegistry()
	if err != nil {
		return err
	}
	return registry.Delete(strings.TrimSpace(id))
}

func (a *App) SkillEnable(id string, enabled bool) error {
	registry, err := a.ensureSkillRegistry()
	if err != nil {
		return err
	}
	return registry.Enable(strings.TrimSpace(id), enabled)
}

func (a *App) SkillRun(req agent.StartRequest) (string, error) {
	return a.AgentStart(req)
}

func (a *App) applySkill(req *agent.StartRequest) error {
	if strings.TrimSpace(req.SkillID) == "" {
		return nil
	}
	registry, err := a.ensureSkillRegistry()
	if err != nil {
		return err
	}
	manifest, err := registry.Get(strings.TrimSpace(req.SkillID))
	if err != nil {
		return fmt.Errorf("加载 Skill 失败: %w", err)
	}
	if !manifest.Builtin && !manifest.Enabled {
		return fmt.Errorf("Skill 已禁用: %s", manifest.ID)
	}
	if manifest.TrustStatus == "invalid" || manifest.TrustStatus == "untrusted" {
		return fmt.Errorf("Skill 签名未获信任: %s", manifest.TrustStatus)
	}
	if missing := a.skillMissingDependencies(manifest); len(missing) > 0 {
		return fmt.Errorf("Skill 依赖未满足: %s", strings.Join(missing, ", "))
	}
	params, err := validateSkillParameters(manifest, req.SkillParameters)
	if err != nil {
		return err
	}
	for targetID := range req.TargetParameters {
		found := false
		for _, target := range req.Targets {
			if target.ID == targetID {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("多目标参数引用了不存在的目标: %s", targetID)
		}
	}
	if strings.TrimSpace(req.Goal) == "" {
		req.Goal = manifest.Description
	}
	if strings.TrimSpace(req.Mode) == "" {
		req.Mode = manifest.Mode
	}
	if req.MaxSteps <= 0 || (manifest.MaxSteps > 0 && req.MaxSteps > manifest.MaxSteps) {
		req.MaxSteps = manifest.MaxSteps
	}
	if manifest.TimeoutSeconds > 0 {
		req.TimeoutSeconds = manifest.TimeoutSeconds
	}
	req.SkillVersion = manifest.Version
	req.SkillIntegrityHash = manifest.IntegrityHash
	req.SkillParameters = params
	req.AllowedTools = append([]string(nil), manifest.AllowedTools...)
	req.SkillPrompt = renderSkillPrompt(manifest.Prompt, params)
	req.SkillWorkflow = renderWorkflow(manifest.Workflow, params)
	req.ReportTemplate = renderSkillPrompt(manifest.ReportTemplate, params)
	req.Workflow = make([]agent.WorkflowStep, 0, len(manifest.Workflow))
	for _, step := range manifest.Workflow {
		when := renderSkillPrompt(step.When, params)
		matched, matchErr := workflowConditionMatches(when, params)
		if matchErr != nil {
			return fmt.Errorf("Skill 工作流条件无效（%s）: %w", step.ID, matchErr)
		}
		if !matched {
			continue
		}
		repeat := step.Repeat
		if repeat <= 0 {
			repeat = 1
		}
		for index := 0; index < repeat; index++ {
			id := step.ID
			if repeat > 1 {
				id = fmt.Sprintf("%s-%d", step.ID, index+1)
			}
			req.Workflow = append(req.Workflow, agent.WorkflowStep{ID: id, Title: step.Title, When: when, Prompt: renderSkillPrompt(step.Prompt, params), AllowedTools: append([]string(nil), step.AllowedTools...), MaxAttempts: step.MaxAttempts})
		}
	}
	req.Context = strings.TrimSpace(req.Context + "\n\nSkill 参数:\n" + formatSkillParameters(params) + "\n多目标参数:\n" + formatTargetParameters(req.TargetParameters))
	return nil
}

func (a *App) validateSkillResume(task agent.Task) error {
	if strings.TrimSpace(task.SkillID) == "" {
		return nil
	}
	registry, err := a.ensureSkillRegistry()
	if err != nil {
		return err
	}
	manifest, err := registry.Get(strings.TrimSpace(task.SkillID))
	if err != nil {
		return fmt.Errorf("恢复任务的 Skill 不可用: %w", err)
	}
	if !manifest.Builtin && !manifest.Enabled {
		return fmt.Errorf("恢复任务被拒绝：Skill 已禁用: %s", manifest.ID)
	}
	if manifest.TrustStatus == "invalid" || manifest.TrustStatus == "untrusted" {
		return fmt.Errorf("恢复任务被拒绝：Skill 签名未获信任: %s", manifest.TrustStatus)
	}
	if manifest.Version != task.SkillVersion {
		return fmt.Errorf("恢复任务被拒绝：Skill 版本已变化（任务 %s，当前 %s）", task.SkillVersion, manifest.Version)
	}
	if task.SkillIntegrityHash != "" && manifest.IntegrityHash != task.SkillIntegrityHash {
		return errors.New("恢复任务被拒绝：Skill 内容已变化，请新建任务")
	}
	if missing := a.skillMissingDependencies(manifest); len(missing) > 0 {
		return fmt.Errorf("恢复任务被拒绝：Skill 依赖未满足: %s", strings.Join(missing, ", "))
	}
	if _, err := validateSkillParameters(manifest, task.SkillParameters); err != nil {
		return fmt.Errorf("恢复任务的 Skill 参数无效: %w", err)
	}
	return nil
}

func validateSkillParameters(manifest skills.Manifest, supplied map[string]any) (map[string]any, error) {
	result := make(map[string]any, len(manifest.Parameters))
	for name, spec := range manifest.Parameters {
		value, ok := supplied[name]
		if !ok {
			if spec.Default != nil {
				value, ok = spec.Default, true
			} else if spec.Required {
				return nil, fmt.Errorf("Skill 参数缺少: %s", name)
			}
		}
		if !ok {
			continue
		}
		if err := validateParameterValue(name, spec, value); err != nil {
			return nil, err
		}
		result[name] = value
	}
	for name := range supplied {
		if _, ok := manifest.Parameters[name]; !ok {
			return nil, fmt.Errorf("Skill 不支持参数: %s", name)
		}
	}
	return result, nil
}

func validateParameterValue(name string, spec skills.Parameter, value any) error {
	valid := false
	switch strings.ToLower(spec.Type) {
	case "string":
		text, ok := value.(string)
		valid = ok && (!spec.Required || strings.TrimSpace(text) != "")
	case "boolean":
		_, valid = value.(bool)
	case "integer":
		switch value.(type) {
		case int, int32, int64, float64, json.Number:
			valid = true
		}
	case "number":
		switch value.(type) {
		case int, int32, int64, float32, float64, json.Number:
			valid = true
		}
	}
	if !valid {
		return fmt.Errorf("Skill 参数 %s 类型错误，应为 %s", name, spec.Type)
	}
	if len(spec.Enum) > 0 {
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("Skill 参数 %s 只能使用枚举字符串", name)
		}
		found := false
		for _, item := range spec.Enum {
			if item == text {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("Skill 参数 %s 的值不在允许范围内", name)
		}
	}
	return nil
}

func renderSkillPrompt(prompt string, params map[string]any) string {
	result := prompt
	for name, value := range params {
		result = strings.ReplaceAll(result, "{{"+name+"}}", fmt.Sprint(value))
	}
	return result
}

func renderWorkflow(steps []skills.WorkflowStep, params map[string]any) string {
	if len(steps) == 0 {
		return ""
	}
	lines := make([]string, 0, len(steps))
	for index, step := range steps {
		prompt := renderSkillPrompt(step.Prompt, params)
		line := fmt.Sprintf("%d. %s: %s", index+1, step.Title, prompt)
		if strings.TrimSpace(step.When) != "" {
			line += "（条件：" + renderSkillPrompt(step.When, params) + "）"
		}
		if len(step.AllowedTools) > 0 {
			line += " [工具：" + strings.Join(step.AllowedTools, ", ") + "]"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// Workflow conditions intentionally use a small deterministic grammar:
// true, false, param:name==value, and param:name!=value.
func workflowConditionMatches(condition string, params map[string]any) (bool, error) {
	condition = strings.TrimSpace(condition)
	if condition == "" || condition == "true" || condition == "always" {
		return true, nil
	}
	if condition == "false" || condition == "never" {
		return false, nil
	}
	if strings.HasPrefix(condition, "param:") {
		expression := strings.TrimPrefix(condition, "param:")
		operator := "=="
		if strings.Contains(expression, "!=") {
			operator = "!="
		}
		parts := strings.SplitN(expression, operator, 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
			return false, errors.New("条件格式应为 param:name==value")
		}
		actual := fmt.Sprint(params[strings.TrimSpace(parts[0])])
		matched := actual == strings.TrimSpace(parts[1])
		if operator == "!=" {
			matched = !matched
		}
		return matched, nil
	}
	return false, errors.New("仅支持 true、false 或 param:name==value")
}

func formatSkillParameters(params map[string]any) string {
	if len(params) == 0 {
		return "无"
	}
	raw, _ := json.Marshal(params)
	return string(raw)
}

func formatTargetParameters(params map[string]map[string]any) string {
	if len(params) == 0 {
		return "无"
	}
	raw, _ := json.Marshal(params)
	return string(raw)
}
