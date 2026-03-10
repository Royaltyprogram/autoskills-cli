package service

import (
	"bytes"
	"embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed prompts/research_agent_instruction_prompt.md
var researchAgentPromptFS embed.FS

var researchAgentInstructionPromptTemplate = template.Must(template.New("research_agent_instruction_prompt.md").ParseFS(
	researchAgentPromptFS,
	"prompts/research_agent_instruction_prompt.md",
))

type researchAgentInstructionPromptData struct {
	ProjectName          string
	SampledQueryCount    int
	SampledQueriesPrompt string
}

func renderResearchAgentInstructionPrompt(project *Project, sampledQueries []string) (string, error) {
	data := researchAgentInstructionPromptData{
		SampledQueryCount:    len(sampledQueries),
		SampledQueriesPrompt: formatSampledQueriesForPrompt(sampledQueries),
	}
	if project != nil {
		data.ProjectName = strings.TrimSpace(project.Name)
	}

	var buf bytes.Buffer
	if err := researchAgentInstructionPromptTemplate.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render research agent prompt: %w", err)
	}
	return strings.TrimSpace(buf.String()), nil
}

func formatSampledQueriesForPrompt(sampledQueries []string) string {
	if len(sampledQueries) == 0 {
		return "- none"
	}
	lines := make([]string, 0, len(sampledQueries))
	for idx, query := range sampledQueries {
		lines = append(lines, fmt.Sprintf("sample_query_%d: %s", idx+1, strings.TrimSpace(query)))
	}
	return strings.Join(lines, "\n")
}
