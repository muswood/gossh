// owner: muswood | Email: mumu920@outlook.com
package agent

import (
	"fmt"
	"strings"
)

// reportMarkdown is the user-facing representation of a validated Agent report.
// The persisted task result remains JSON so it can be queried by the backend.
func reportMarkdown(report Report) string {
	var out strings.Builder
	fmt.Fprintf(&out, "# %s\n\n", strings.TrimSpace(report.Title))
	fmt.Fprintf(&out, "**严重性：** %s\n\n", strings.TrimSpace(report.Severity))
	out.WriteString("## 摘要\n\n")
	out.WriteString(strings.TrimSpace(report.Summary))
	out.WriteString("\n")

	if len(report.Findings) > 0 {
		out.WriteString("\n## 发现\n")
		for _, finding := range report.Findings {
			fmt.Fprintf(&out, "\n### %s\n\n%s\n", strings.TrimSpace(finding.Title), strings.TrimSpace(finding.Description))
			if finding.Severity != "" {
				fmt.Fprintf(&out, "\n**严重性：** %s\n", strings.TrimSpace(finding.Severity))
			}
			if len(finding.EvidenceIDs) > 0 {
				ids := make([]string, 0, len(finding.EvidenceIDs))
				for _, id := range finding.EvidenceIDs {
					ids = append(ids, "`"+strings.TrimSpace(id)+"`")
				}
				fmt.Fprintf(&out, "\n**证据：** %s\n", strings.Join(ids, "、"))
			}
		}
	}

	writeMarkdownList(&out, "建议", report.Recommendations)
	writeMarkdownList(&out, "已执行步骤", report.ExecutedSteps)
	writeMarkdownList(&out, "限制", report.Limitations)
	return strings.TrimSpace(out.String())
}

func writeMarkdownList(out *strings.Builder, title string, items []string) {
	if len(items) == 0 {
		return
	}
	out.WriteString("\n\n## " + title + "\n\n")
	for _, item := range items {
		fmt.Fprintf(out, "- %s\n", strings.TrimSpace(item))
	}
}
