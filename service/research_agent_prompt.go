package service

import (
	"bytes"
	"embed"
	"fmt"
	"strings"
	"text/template"
)

const (
	researchReportPromptVariantDefault = ""
	researchReportPromptVariantKOTest  = "ko-test"
)

//go:embed prompts/research_agent_reports_prompt.md prompts/research_agent_reports_prompt_ko_test.md
var researchAgentPromptFS embed.FS

var researchAgentReportsPromptTemplate = template.Must(template.New("research_agent_reports_prompt.md").ParseFS(
	researchAgentPromptFS,
	"prompts/research_agent_reports_prompt.md",
))

var researchAgentReportsPromptKOTestTemplate = template.Must(template.New("research_agent_reports_prompt_ko_test.md").ParseFS(
	researchAgentPromptFS,
	"prompts/research_agent_reports_prompt_ko_test.md",
))

type researchAgentReportPromptData struct {
	ProjectName               string
	SampledQueryCount         int
	SampledQueriesPrompt      string
	InteractionEvidencePrompt string
	UsageSummaryPrompt        string
	RecentSessionsPrompt      string
}

func renderResearchAgentReportsPrompt(variant string, project *Project, sampledQueries []string, interactionSamples []researchInteractionSample, usageSummary researchUsageSummary) (string, error) {
	data := researchAgentReportPromptData{
		SampledQueryCount:         len(sampledQueries),
		SampledQueriesPrompt:      formatSampledQueriesForPrompt(sampledQueries),
		InteractionEvidencePrompt: formatInteractionEvidenceForPrompt(interactionSamples),
		UsageSummaryPrompt:        formatUsageSummaryForPrompt(usageSummary),
		RecentSessionsPrompt:      formatRecentSessionsForPrompt(usageSummary.RecentSessions),
	}
	if project != nil {
		data.ProjectName = strings.TrimSpace(project.Name)
	}

	promptTemplate := researchAgentReportsPromptTemplate
	switch normalizeResearchReportPromptVariant(variant) {
	case researchReportPromptVariantKOTest:
		promptTemplate = researchAgentReportsPromptKOTestTemplate
	}

	var buf bytes.Buffer
	if err := promptTemplate.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render research agent prompt: %w", err)
	}
	return strings.TrimSpace(buf.String()), nil
}

func normalizeResearchReportPromptVariant(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
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

func formatUsageSummaryForPrompt(usageSummary researchUsageSummary) string {
	lines := []string{
		fmt.Sprintf("- sessions=%d", usageSummary.SessionCount),
		fmt.Sprintf("- raw_queries=%d", usageSummary.RawQueryCount),
		fmt.Sprintf("- total_input_tokens=%d", usageSummary.TotalInputTokens),
		fmt.Sprintf("- total_output_tokens=%d", usageSummary.TotalOutputTokens),
		fmt.Sprintf("- total_cached_input_tokens=%d", usageSummary.TotalCachedInputTokens),
		fmt.Sprintf("- total_reasoning_output_tokens=%d", usageSummary.TotalReasoningOutputTokens),
		fmt.Sprintf("- avg_tokens_per_query=%d", usageSummary.AvgTokensPerQuery),
		fmt.Sprintf("- avg_first_response_latency_ms=%d", usageSummary.AvgFirstResponseLatencyMS),
		fmt.Sprintf("- avg_session_duration_ms=%d", usageSummary.AvgSessionDurationMS),
		fmt.Sprintf("- total_function_calls=%d", usageSummary.TotalFunctionCalls),
		fmt.Sprintf("- total_tool_errors=%d", usageSummary.TotalToolErrors),
		fmt.Sprintf("- total_tool_wall_time_ms=%d", usageSummary.TotalToolWallTimeMS),
		fmt.Sprintf("- sessions_with_function_calls=%d", usageSummary.SessionsWithFunctionCalls),
		fmt.Sprintf("- sessions_with_tool_errors=%d", usageSummary.SessionsWithToolErrors),
	}
	return strings.Join(lines, "\n")
}

func formatRecentSessionsForPrompt(recentSessions []researchSessionSnapshot) string {
	if len(recentSessions) == 0 {
		return "- none"
	}
	lines := make([]string, 0, len(recentSessions))
	for _, session := range recentSessions {
		lines = append(lines, fmt.Sprintf(
			"- %s | tool=%s | queries=%d | input_tokens=%d | output_tokens=%d | cached_input_tokens=%d | reasoning_output_tokens=%d | first_response_latency_ms=%d | session_duration_ms=%d | function_calls=%d | tool_errors=%d | tool_wall_time_ms=%d",
			session.TimestampLabel,
			session.Tool,
			session.QueryCount,
			session.InputTokens,
			session.OutputTokens,
			session.CachedInputTokens,
			session.ReasoningOutputTokens,
			session.FirstResponseLatencyMS,
			session.SessionDurationMS,
			session.FunctionCallCount,
			session.ToolErrorCount,
			session.ToolWallTimeMS,
		))
	}
	return strings.Join(lines, "\n")
}

func formatInteractionEvidenceForPrompt(samples []researchInteractionSample) string {
	if len(samples) == 0 {
		return "- none"
	}

	lines := make([]string, 0, len(samples)*6)
	for idx, sample := range samples {
		lines = append(lines, fmt.Sprintf("interaction_%d: %s | tool=%s", idx+1, sample.TimestampLabel, sample.Tool))
		if len(sample.Queries) == 0 {
			lines = append(lines, "  user_queries: none")
		} else {
			for queryIdx, query := range sample.Queries {
				lines = append(lines, fmt.Sprintf("  user_query_%d: %s", queryIdx+1, strings.TrimSpace(query)))
			}
		}
		if len(sample.AssistantResponses) == 0 {
			lines = append(lines, "  assistant_responses: none")
		} else {
			for responseIdx, response := range sample.AssistantResponses {
				lines = append(lines, fmt.Sprintf("  assistant_response_%d: %s", responseIdx+1, formatPromptEvidenceText(response)))
			}
		}
		if len(sample.ReasoningSummaries) == 0 {
			lines = append(lines, "  reasoning_summaries: none")
		} else {
			for summaryIdx, summary := range sample.ReasoningSummaries {
				lines = append(lines, fmt.Sprintf("  reasoning_summary_%d: %s", summaryIdx+1, formatPromptEvidenceText(summary)))
			}
		}
	}
	return strings.Join(lines, "\n")
}

func formatPromptEvidenceText(raw string) string {
	raw = strings.ReplaceAll(strings.TrimSpace(raw), "\r\n", "\n")
	raw = strings.Join(strings.Fields(raw), " ")
	return truncatePromptEvidenceText(raw, 360)
}

func truncatePromptEvidenceText(raw string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(raw)
	if len(runes) <= limit {
		return raw
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}
