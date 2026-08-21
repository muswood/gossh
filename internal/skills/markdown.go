// owner: muswood | Email: mumu920@outlook.com
package skills

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	standardSkillFile    = "SKILL.md"
	standardSkillVersion = "standard"
	signatureCommentHead = "<!-- gossh-signature: "
)

type skillFrontMatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// ParseMarkdown reads the standard Agent Skill format: a SKILL.md file with
// only name and description in YAML front matter and Markdown instructions.
func ParseMarkdown(raw []byte) (Manifest, error) {
	if len(raw) > 256<<10 {
		return Manifest{}, errors.New("Skill 文件超过 256 KB")
	}
	text := strings.TrimPrefix(string(raw), "\ufeff")
	frontMatter, body, err := splitFrontMatter(text)
	if err != nil {
		return Manifest{}, err
	}
	decoder := yaml.NewDecoder(strings.NewReader(frontMatter))
	decoder.KnownFields(true)
	var metadata skillFrontMatter
	if err := decoder.Decode(&metadata); err != nil {
		return Manifest{}, fmt.Errorf("Skill Front Matter 无效: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Manifest{}, errors.New("Skill Front Matter 只能包含一个 YAML 文档")
	}
	metadata.Name = strings.TrimSpace(metadata.Name)
	metadata.Description = strings.TrimSpace(metadata.Description)
	if !validStandardSkillName(metadata.Name) {
		return Manifest{}, errors.New("Skill name 必须为 1-64 个小写字母、数字或连字符")
	}
	if metadata.Description == "" {
		return Manifest{}, errors.New("Skill description 不能为空")
	}
	signature, body, err := extractSignature(body)
	if err != nil {
		return Manifest{}, err
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return Manifest{}, errors.New("Skill Markdown 正文不能为空")
	}
	return Manifest{
		ID: metadata.Name, Name: markdownTitle(body, metadata.Name), Version: standardSkillVersion,
		Description: metadata.Description, Prompt: body, Signature: signature,
		Enabled: true, Source: "local",
	}, nil
}

// MarshalMarkdown writes a standard SKILL.md document. Runtime-only state is
// deliberately excluded from front matter; it lives in the registry state.
func MarshalMarkdown(manifest Manifest) ([]byte, error) {
	manifest = normalizeStandardManifest(manifest)
	if err := Validate(manifest); err != nil {
		return nil, err
	}
	metadata, err := yaml.Marshal(skillFrontMatter{Name: manifest.ID, Description: manifest.Description})
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.WriteString("---\n")
	out.Write(metadata)
	out.WriteString("---\n\n")
	if manifest.Signature != nil {
		raw, err := json.Marshal(manifest.Signature)
		if err != nil {
			return nil, err
		}
		out.WriteString(signatureCommentHead)
		out.WriteString(base64.StdEncoding.EncodeToString(raw))
		out.WriteString(" -->\n\n")
	}
	out.WriteString(strings.TrimSpace(manifest.Prompt))
	out.WriteByte('\n')
	return out.Bytes(), nil
}

func splitFrontMatter(text string) (string, string, error) {
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return "", "", errors.New("Skill 必须以标准 YAML Front Matter 开始")
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for index := 1; index < len(lines); index++ {
		if lines[index] != "---" {
			continue
		}
		return strings.Join(lines[1:index], "\n"), strings.Join(lines[index+1:], "\n"), nil
	}
	return "", "", errors.New("Skill Front Matter 未闭合")
}

func extractSignature(body string) (*Signature, string, error) {
	body = strings.TrimLeft(body, "\n\r \t")
	if !strings.HasPrefix(body, signatureCommentHead) {
		return nil, body, nil
	}
	end := strings.Index(body, "-->")
	if end < 0 {
		return nil, "", errors.New("Skill 签名注释未闭合")
	}
	encoded := strings.TrimSpace(strings.TrimPrefix(body[:end], signatureCommentHead))
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, "", errors.New("Skill 签名编码无效")
	}
	var signature Signature
	if err := json.Unmarshal(raw, &signature); err != nil || signature.Signer == "" || signature.Value == "" {
		return nil, "", errors.New("Skill 签名无效")
	}
	return &signature, strings.TrimLeft(body[end+3:], "\n\r \t"), nil
}

func markdownTitle(body, fallback string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") && strings.TrimSpace(strings.TrimPrefix(line, "# ")) != "" {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
		if line != "" {
			break
		}
	}
	return fallback
}

func validStandardSkillName(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' {
			continue
		}
		return false
	}
	return true
}

func normalizeStandardManifest(manifest Manifest) Manifest {
	manifest.ID = strings.TrimSpace(manifest.ID)
	if manifest.Description == "" {
		manifest.Description = manifest.Name
	}
	manifest.Description = strings.TrimSpace(manifest.Description)
	if manifest.Name == "" {
		manifest.Name = manifest.ID
	}
	if !strings.HasPrefix(strings.TrimSpace(manifest.Prompt), "# ") && manifest.Name != manifest.ID {
		manifest.Prompt = "# " + manifest.Name + "\n\n" + strings.TrimSpace(manifest.Prompt)
	}
	manifest.Version = standardSkillVersion
	manifest.Mode = ""
	manifest.AllowedTools = nil
	manifest.MaxSteps = 0
	manifest.TimeoutSeconds = 0
	manifest.RequiresApproval = false
	manifest.Parameters = nil
	manifest.Category = ""
	manifest.Tags = nil
	manifest.Dependencies = nil
	manifest.Workflow = nil
	manifest.ReportTemplate = ""
	manifest.Builtin = false
	manifest.TrustStatus = ""
	manifest.IntegrityHash = ""
	return manifest
}
