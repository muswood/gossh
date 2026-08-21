// owner: muswood | Email: mumu920@outlook.com
package skills

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistrySaveListAndEnable(t *testing.T) {
	registry, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{ID: "custom-check", Name: "Custom check", Version: "1.0.0", Prompt: "check {{service}}", AllowedTools: []string{"terminal_command"}, Parameters: map[string]Parameter{"service": {Type: "string", Required: true}}, Enabled: true}
	if err := registry.Save(manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(registry.dir, manifest.ID, standardSkillFile)); err != nil {
		t.Fatalf("standard SKILL.md was not saved: %v", err)
	}
	got, err := registry.Get("custom-check")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != manifest.Name || !got.Enabled || got.Builtin {
		t.Fatalf("unexpected manifest: %#v", got)
	}
	if err := registry.Enable("custom-check", false); err != nil {
		t.Fatal(err)
	}
	got, err = registry.Get("custom-check")
	if err != nil || got.Enabled {
		t.Fatalf("enable state not persisted: %#v, %v", got, err)
	}
	if err := registry.Delete("custom-check"); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Get("custom-check"); err == nil {
		t.Fatal("deleted skill was still returned")
	}
}

func TestBuiltinTerminalSkillsDoNotUndercutCommandTimeout(t *testing.T) {
	for _, manifest := range builtinManifests() {
		usesTerminal := false
		for _, toolName := range manifest.AllowedTools {
			usesTerminal = usesTerminal || toolName == "terminal_command" || toolName == "terminal_command_many"
		}
		if usesTerminal && manifest.TimeoutSeconds != 0 {
			t.Fatalf("terminal skill %s task timeout = %d seconds, want no task-level timeout", manifest.ID, manifest.TimeoutSeconds)
		}
	}
}

func TestRegistryCreatesHistoryAndVerifiesTrustedSignature(t *testing.T) {
	registry, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{ID: "signed-check", Name: "Signed check", Version: "1.0.0", Prompt: "check", AllowedTools: []string{"report"}, Enabled: true}
	if err := registry.Save(manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = "1.1.0"
	if err := registry.Save(manifest); err != nil {
		t.Fatal(err)
	}
	history, err := registry.History(manifest.ID)
	if err != nil || len(history) != 1 || !strings.HasPrefix(history[0].Version, standardSkillVersion+"-") {
		t.Fatalf("unexpected history: %#v, %v", history, err)
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.TrustKey("ops-team", base64.StdEncoding.EncodeToString(publicKey)); err != nil {
		t.Fatal(err)
	}
	signed := Manifest{ID: "verified-check", Name: "Verified check", Version: "1.0.0", Prompt: "check", AllowedTools: []string{"report"}, Enabled: true, Source: "local"}
	signed, err = Sign(signed, "ops-team", base64.StdEncoding.EncodeToString(privateKey))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Save(signed); err != nil {
		t.Fatal(err)
	}
	got, err := registry.Get(signed.ID)
	if err != nil || got.TrustStatus != "verified" {
		t.Fatalf("signature was not verified: %#v, %v", got, err)
	}
	if err := registry.Enable(signed.ID, false); err != nil {
		t.Fatal(err)
	}
	got, err = registry.Get(signed.ID)
	if err != nil || got.Enabled || got.TrustStatus != "verified" {
		t.Fatalf("disabling signed skill changed trust: %#v, %v", got, err)
	}
	exported, err := registry.Export(signed.ID)
	if err != nil || !exported.Enabled || exported.TrustStatus != "verified" {
		t.Fatalf("exported signed skill included runtime state: %#v, %v", exported, err)
	}
	if err := registry.Enable(signed.ID, true); err != nil {
		t.Fatal(err)
	}
	got, err = registry.Get(signed.ID)
	if err != nil || !got.Enabled || got.TrustStatus != "verified" {
		t.Fatalf("re-enabling signed skill changed trust: %#v, %v", got, err)
	}

	second, err := Sign(Manifest{ID: signed.ID, Name: signed.Name, Version: "1.1.0", Prompt: signed.Prompt, AllowedTools: signed.AllowedTools, Enabled: true, Source: signed.Source}, "ops-team", base64.StdEncoding.EncodeToString(privateKey))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Save(second); err != nil {
		t.Fatal(err)
	}
	history, err = registry.History(signed.ID)
	if err != nil || len(history) == 0 {
		t.Fatalf("signed skill history unavailable: %#v, %v", history, err)
	}
	if err := registry.Restore(signed.ID, history[0].Version); err != nil {
		t.Fatal(err)
	}
	got, err = registry.Get(signed.ID)
	if err != nil || got.Version != signed.Version || got.TrustStatus != "verified" {
		t.Fatalf("restoring signed skill changed trust: %#v, %v", got, err)
	}
}

func TestValidateRejectsUnsafeManifestShape(t *testing.T) {
	err := Validate(Manifest{ID: "bad id", Name: "bad", Version: "1", Prompt: "x", AllowedTools: []string{"terminal_command"}})
	if err == nil {
		t.Fatal("expected invalid id to be rejected")
	}
	err = Validate(Manifest{ID: "bad_skill", Name: "bad", Version: "1", Prompt: "x"})
	if err == nil {
		t.Fatal("expected non-standard skill name to be rejected")
	}
}

func TestParseMarkdownUsesStandardFrontMatterOnly(t *testing.T) {
	raw := []byte("---\nname: linux-health\ndescription: Inspect a Linux host for common health issues.\n---\n\n# Linux health\n\nCollect read-only evidence before reporting findings.\n")
	manifest, err := ParseMarkdown(raw)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ID != "linux-health" || manifest.Name != "Linux health" || manifest.Prompt == "" || len(manifest.AllowedTools) != 0 {
		t.Fatalf("unexpected standard skill: %#v", manifest)
	}
	if _, err := ParseMarkdown([]byte("---\nname: linux-health\ndescription: test\nversion: 1\n---\n\nbody")); err == nil {
		t.Fatal("non-standard front matter field was accepted")
	}
}
