// owner: muswood | Email: mumu920@outlook.com
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gossh/internal/agent"
	"gossh/internal/skills"
)

func TestSkillSaveAndDocumentUseStandardMarkdown(t *testing.T) {
	dir := t.TempDir()
	registry, err := skills.NewRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{skillRegistry: registry}
	document := "---\nname: host-health\ndescription: Analyze a host when a user requests a health check.\n---\n\n# Host health\n\nCollect read-only evidence and summarize the result.\n"
	if err := app.SkillSave(document); err != nil {
		t.Fatal(err)
	}
	stored, err := app.SkillDocument("host-health")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored, "name: host-health") || !strings.Contains(stored, "# Host health") {
		t.Fatalf("unexpected stored document: %s", stored)
	}
	if _, err := os.Stat(filepath.Join(dir, "host-health", "SKILL.md")); err != nil {
		t.Fatalf("standard skill path was not created: %v", err)
	}
	if err := app.SkillSave(`{"id":"legacy"}`); err == nil {
		t.Fatal("JSON skill document was accepted")
	}
}

func TestSkillParameterAndWorkflowRendering(t *testing.T) {
	manifest := skills.Manifest{
		ID: "svc", Name: "service", Version: "1", Prompt: "inspect {{service}}", AllowedTools: []string{"report"},
		Parameters: map[string]skills.Parameter{"service": {Type: "string", Required: true}},
		Workflow:   []skills.WorkflowStep{{ID: "collect", Title: "Collect", Prompt: "check {{service}}", When: "service is set"}},
	}
	params, err := validateSkillParameters(manifest, map[string]any{"service": "nginx"})
	if err != nil {
		t.Fatal(err)
	}
	if got := renderSkillPrompt(manifest.Prompt, params); got != "inspect nginx" {
		t.Fatalf("unexpected prompt: %s", got)
	}
	if got := renderWorkflow(manifest.Workflow, params); got == "" || got[:1] != "1" || !containsText(got, "nginx") {
		t.Fatalf("unexpected workflow: %s", got)
	}
	if _, err := validateSkillParameters(manifest, map[string]any{"service": ""}); err == nil {
		t.Fatal("empty required parameter was accepted")
	}
}

func containsText(value, needle string) bool {
	for i := 0; i+len(needle) <= len(value); i++ {
		if value[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestValidateSkillResumeRejectsDisabledOrChangedSkill(t *testing.T) {
	registry, err := skills.NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manifest := skills.Manifest{ID: "resume-check", Name: "Resume check", Version: "1.0.0", Prompt: "check", AllowedTools: []string{"report"}, Enabled: true}
	if err := registry.Save(manifest); err != nil {
		t.Fatal(err)
	}
	stored, err := registry.Get(manifest.ID)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{skillRegistry: registry}
	task := agent.Task{SkillID: stored.ID, SkillVersion: stored.Version, SkillIntegrityHash: stored.IntegrityHash}
	if err := app.validateSkillResume(task); err != nil {
		t.Fatalf("valid skill could not resume: %v", err)
	}
	if err := registry.Enable(manifest.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := app.validateSkillResume(task); err == nil {
		t.Fatal("disabled skill was allowed to resume")
	}
	if err := registry.Enable(manifest.ID, true); err != nil {
		t.Fatal(err)
	}
	manifest.Description = "changed"
	if err := registry.Save(manifest); err != nil {
		t.Fatal(err)
	}
	if err := app.validateSkillResume(task); err == nil {
		t.Fatal("changed skill was allowed to resume")
	}
}
