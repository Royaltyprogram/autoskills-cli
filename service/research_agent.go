package service

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Royaltyprogram/aiops/configs"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
)

const (
	defaultOpenAIResponsesModel   = "gpt-5.4"
	defaultResearchSampleSize     = 10
	defaultResearchRequestTimeout = 45 * time.Second
	defaultInstructionHeading     = "## AgentOpt Research Findings"
)

type CloudResearchAgent struct {
	Provider string
	Model    string
	Mode     string

	apiKey     string
	client     openai.Client
	sampleSize int
	randSource *rand.Rand
}

type researchRecommendation struct {
	Kind            string
	Title           string
	Summary         string
	Reason          string
	Explanation     string
	ExpectedBenefit string
	Risk            string
	ExpectedImpact  string
	Score           float64
	Evidence        []string
	Steps           []ChangePlanStep
	Settings        map[string]any
}

type instructionPattern struct {
	Key         string
	Label       string
	Terms       []string
	Instruction string
}

type matchedInstructionPattern struct {
	Pattern instructionPattern
	Count   int
}

type researchSessionSnapshot struct {
	TimestampLabel         string
	Tool                   string
	QueryCount             int
	InputTokens            int
	OutputTokens           int
	CachedInputTokens      int
	ReasoningOutputTokens  int
	FirstResponseLatencyMS int
	SessionDurationMS      int
	FunctionCallCount      int
	ToolErrorCount         int
	ToolWallTimeMS         int
}

type researchUsageSummary struct {
	SessionCount               int
	RawQueryCount              int
	TotalInputTokens           int
	TotalOutputTokens          int
	TotalCachedInputTokens     int
	TotalReasoningOutputTokens int
	AvgTokensPerQuery          int
	AvgFirstResponseLatencyMS  int
	AvgSessionDurationMS       int
	TotalFunctionCalls         int
	TotalToolErrors            int
	TotalToolWallTimeMS        int
	SessionsWithFunctionCalls  int
	SessionsWithToolErrors     int
	RecentSessions             []researchSessionSnapshot
}

var personalInstructionPatterns = []instructionPattern{
	{
		Key:         "repo_discovery",
		Label:       "repo discovery",
		Terms:       []string{"find", "inspect", "explore", "locate", "repo", "which file", "control flow", "summarize the current"},
		Instruction: "The user repeatedly spends turns on repo discovery and control-flow recap before real work begins, which suggests the default workflow starts without enough context.",
	},
	{
		Key:         "root_cause",
		Label:       "root-cause analysis",
		Terms:       []string{"why", "root cause", "cause", "bug", "error", "failing", "regression"},
		Instruction: "The user often has to explicitly ask for root-cause analysis, which suggests fixes are attempted before the diagnosis is stable.",
	},
	{
		Key:         "minimal_patch",
		Label:       "minimal patching",
		Terms:       []string{"minimal", "smallest", "small", "least", "patch", "fix only", "without changing"},
		Instruction: "The user repeatedly asks for smaller patches, which suggests the default response scope expands too aggressively without explicit pressure.",
	},
	{
		Key:         "verification",
		Label:       "targeted verification",
		Terms:       []string{"test", "verify", "verification", "regression", "repro", "run", "check"},
		Instruction: "The user repeatedly asks for exact verification steps, which suggests testing discipline is not being applied by default.",
	},
	{
		Key:         "contract_review",
		Label:       "contract comparison",
		Terms:       []string{"compare", "same", "contract", "response", "shared", "similar"},
		Instruction: "The user explicitly requests neighboring contract comparisons, which suggests shared interfaces are easy to change without enough compatibility checks.",
	},
}

func NewCloudResearchAgent(conf *configs.Config) *CloudResearchAgent {
	var openAIConf configs.OpenAI
	if conf != nil {
		openAIConf = conf.OpenAI
	}
	apiKey := strings.TrimSpace(openAIConf.APIKey)
	model := firstNonEmptyString(strings.TrimSpace(openAIConf.ResponsesModel), defaultOpenAIResponsesModel)
	provider := "openai"
	mode := "responses-api"
	if apiKey == "" {
		provider = "local"
		mode = "instruction-fallback"
		model = "personal-usage-mvp"
	}
	clientOptions := []option.RequestOption{}
	if apiKey != "" {
		clientOptions = append(clientOptions, option.WithAPIKey(apiKey))
		clientOptions = append(clientOptions, option.WithHTTPClient(&http.Client{Timeout: defaultResearchRequestTimeout}))
		if baseURL := strings.TrimSpace(openAIConf.BaseURL); baseURL != "" {
			clientOptions = append(clientOptions, option.WithBaseURL(baseURL))
		}
	}
	return &CloudResearchAgent{
		Provider:   provider,
		Model:      model,
		Mode:       mode,
		apiKey:     apiKey,
		client:     openai.NewClient(clientOptions...),
		sampleSize: defaultResearchSampleSize,
		randSource: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func NewCloudResearchAgentPlaceholder(conf *configs.Config) *CloudResearchAgent {
	return NewCloudResearchAgent(conf)
}

func (a *CloudResearchAgent) AnalyzeProject(project *Project, sessions []*SessionSummary, snapshots []*ConfigSnapshot) []researchRecommendation {
	_ = snapshots

	rawQueries := collectRawQueries(sessions)
	rawQueries = normalizeQueriesForResearchPrompt(rawQueries)
	if len(rawQueries) == 0 {
		return nil
	}

	usageSummary := buildResearchUsageSummary(sessions, rawQueries)
	sampledQueries := sampleRawQueries(rawQueries, minInt(a.sampleSize, len(rawQueries)), a.randSource)
	contentPreview, generationMode := a.buildInstructionPreview(project, sampledQueries, rawQueries, usageSummary)

	evidence := []string{
		fmt.Sprintf("sessions=%d", len(sessions)),
		fmt.Sprintf("raw_query_count=%d", len(rawQueries)),
		fmt.Sprintf("sampled_raw_queries=%d", len(sampledQueries)),
		fmt.Sprintf("avg_tokens_per_query=%d", usageSummary.AvgTokensPerQuery),
		fmt.Sprintf("avg_first_response_latency_ms=%d", usageSummary.AvgFirstResponseLatencyMS),
		fmt.Sprintf("total_function_calls=%d", usageSummary.TotalFunctionCalls),
		fmt.Sprintf("total_tool_errors=%d", usageSummary.TotalToolErrors),
		"selection=random",
		"target_file=AGENTS.md",
		"generation_mode=" + generationMode,
	}

	return []researchRecommendation{{
		Kind:            "instruction-custom-rules",
		Title:           instructionRecommendationTitle(project),
		Summary:         "Recent usage history was analyzed to highlight repeated inefficiencies before the local coding agent decides what instruction to add.",
		Reason:          buildInstructionReason(sampledQueries, usageSummary),
		Explanation:     "The research agent samples recent raw queries, adds latency and token context, asks OpenAI for abstract workflow findings, and leaves the final instruction edit to the local Codex agent.",
		ExpectedBenefit: "Surface high-friction defaults without forcing the research agent to author the final AGENTS.md wording.",
		Risk:            "Low. The plan is a reviewable append to AGENTS.md.",
		ExpectedImpact:  "Lower setup churn, less repeated prompt steering, and clearer evidence about where the workflow wastes time.",
		Score:           instructionRecommendationScore(len(sampledQueries), float64(usageSummary.AvgTokensPerQuery)),
		Evidence:        evidence,
		Steps: []ChangePlanStep{{
			Type:           "text_append",
			Action:         "append_block",
			TargetFile:     "AGENTS.md",
			Summary:        "Append a research findings block distilled from usage history and sampled queries.",
			ContentPreview: contentPreview,
		}},
	}}
}

func instructionRecommendationTitle(project *Project) string {
	if project != nil && strings.TrimSpace(project.Name) != "" {
		return "Highlight workflow inefficiencies for " + project.Name
	}
	return "Highlight workflow inefficiencies"
}

func collectRawQueries(sessions []*SessionSummary) []string {
	out := make([]string, 0)
	for _, session := range sessions {
		for _, query := range session.RawQueries {
			query = strings.TrimSpace(query)
			if query == "" {
				continue
			}
			out = append(out, query)
		}
	}
	return out
}

func (a *CloudResearchAgent) buildInstructionPreview(project *Project, sampledQueries, rawQueries []string, usageSummary researchUsageSummary) (string, string) {
	markdown, err := a.generateInstructionMarkdown(project, sampledQueries, usageSummary)
	if err == nil && strings.TrimSpace(markdown) != "" {
		return wrapInstructionMarkdown(markdown), "openai_responses_api"
	}
	return buildFallbackInstructionContent(rawQueries, usageSummary), "local_fallback"
}

func (a *CloudResearchAgent) generateInstructionMarkdown(project *Project, sampledQueries []string, usageSummary researchUsageSummary) (string, error) {
	if strings.TrimSpace(a.apiKey) == "" {
		return "", fmt.Errorf("OPENAI_API_KEY is not configured")
	}
	if len(sampledQueries) == 0 {
		return "", fmt.Errorf("no sampled queries available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultResearchRequestTimeout)
	defer cancel()

	prompt, err := buildInstructionPrompt(project, sampledQueries, usageSummary)
	if err != nil {
		return "", err
	}

	resp, err := a.client.Responses.New(ctx, responses.ResponseNewParams{
		Model: openai.ResponsesModel(a.Model),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(prompt),
		},
	})
	if err != nil {
		return "", err
	}
	return normalizeInstructionMarkdown(resp.OutputText()), nil
}

func buildInstructionPrompt(project *Project, sampledQueries []string, usageSummary researchUsageSummary) (string, error) {
	return renderResearchAgentInstructionPrompt(project, sampledQueries, usageSummary)
}

func sampleRawQueries(queries []string, limit int, rng *rand.Rand) []string {
	if limit <= 0 || len(queries) == 0 {
		return nil
	}
	if len(queries) <= limit {
		return append([]string(nil), queries...)
	}
	pool := append([]string(nil), queries...)
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	rng.Shuffle(len(pool), func(i, j int) {
		pool[i], pool[j] = pool[j], pool[i]
	})
	return append([]string(nil), pool[:limit]...)
}

func wrapInstructionMarkdown(markdown string) string {
	lines := []string{
		"",
		defaultInstructionHeading,
	}
	if trimmed := strings.TrimSpace(markdown); trimmed != "" {
		lines = append(lines, strings.Split(trimmed, "\n")...)
	}
	return strings.Join(lines, "\n") + "\n"
}

func normalizeInstructionMarkdown(markdown string) string {
	rawLines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimPrefix(line, defaultInstructionHeading)
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "- ") {
			line = "- " + strings.TrimLeft(line, "-* ")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func buildFallbackInstructionContent(rawQueries []string, usageSummary researchUsageSummary) string {
	matches := topInstructionPatterns(matchInstructionPatterns(rawQueries), 3)
	lines := []string{
		"",
		defaultInstructionHeading,
		"- Repeated prompt steering suggests the workflow still depends on manual setup instead of strong defaults.",
	}
	for _, match := range matches {
		lines = append(lines, "- "+match.Pattern.Instruction)
	}
	if usageSummary.AvgTokensPerQuery >= 2500 {
		lines = append(lines, "- Token usage per query is high enough that too much context is likely being rebuilt instead of reused.")
	}
	if usageSummary.AvgFirstResponseLatencyMS >= 2000 {
		lines = append(lines, "- First-response latency is high enough to suggest too much discovery happens before the first useful answer.")
	}
	if usageSummary.TotalToolErrors > 0 {
		lines = append(lines, "- Tool-call errors are recurring, which suggests execution steps are being attempted without enough preflight or constraint awareness.")
	}
	return strings.Join(lines, "\n") + "\n"
}

func normalizeQueriesForResearchPrompt(queries []string) []string {
	seen := make(map[string]struct{}, len(queries))
	out := make([]string, 0, len(queries))
	for _, query := range queries {
		normalized := normalizeResearchPromptQuery(query)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func normalizeResearchPromptQuery(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	for _, marker := range []string{"## My request for Codex:", "## My request for Codex", "My request for Codex:"} {
		if strings.Contains(raw, marker) {
			raw = raw[strings.Index(raw, marker)+len(marker):]
			break
		}
	}
	raw = stripTaggedBlock(raw, "<environment_context>", "</environment_context>")
	raw = stripTaggedBlock(raw, "<INSTRUCTIONS>", "</INSTRUCTIONS>")
	raw = strings.ReplaceAll(raw, "\r\n", "\n")

	lines := strings.Split(raw, "\n")
	cleaned := make([]string, 0, len(lines))
	skipOpenTabs := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "# AGENTS.md instructions"):
			continue
		case strings.EqualFold(line, "# Context from my IDE setup:"),
			strings.EqualFold(line, "# Context from my IDE setup"):
			continue
		case strings.EqualFold(line, "## Open tabs:"),
			strings.EqualFold(line, "## Open tabs"):
			skipOpenTabs = true
			continue
		case strings.HasPrefix(line, "## My request for Codex"):
			skipOpenTabs = false
			continue
		case skipOpenTabs:
			if strings.HasPrefix(line, "## ") {
				skipOpenTabs = false
			} else {
				continue
			}
		}
		if strings.EqualFold(line, "<image>") || strings.EqualFold(line, "</image>") {
			continue
		}
		cleaned = append(cleaned, line)
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}

func stripTaggedBlock(raw, openTag, closeTag string) string {
	for {
		start := strings.Index(raw, openTag)
		if start < 0 {
			return raw
		}
		end := strings.Index(raw[start+len(openTag):], closeTag)
		if end < 0 {
			return strings.TrimSpace(raw[:start])
		}
		end += start + len(openTag) + len(closeTag)
		raw = raw[:start] + raw[end:]
	}
}

func matchInstructionPatterns(queries []string) []matchedInstructionPattern {
	out := make([]matchedInstructionPattern, 0, len(personalInstructionPatterns))
	for _, pattern := range personalInstructionPatterns {
		count := 0
		for _, query := range queries {
			if queryMatchesPattern(query, pattern.Terms) {
				count++
			}
		}
		if count == 0 {
			continue
		}
		out = append(out, matchedInstructionPattern{
			Pattern: pattern,
			Count:   count,
		})
	}
	return out
}

func queryMatchesPattern(query string, terms []string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	for _, term := range terms {
		if strings.Contains(query, term) {
			return true
		}
	}
	return false
}

func topInstructionPatterns(items []matchedInstructionPattern, limit int) []matchedInstructionPattern {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count == items[j].Count {
			return items[i].Pattern.Label < items[j].Pattern.Label
		}
		return items[i].Count > items[j].Count
	})
	if limit > 0 && len(items) > limit {
		return append([]matchedInstructionPattern(nil), items[:limit]...)
	}
	return append([]matchedInstructionPattern(nil), items...)
}

func buildInstructionReason(sampledQueries []string, usageSummary researchUsageSummary) string {
	if len(sampledQueries) == 0 {
		return "No sampled raw queries were available for instruction synthesis."
	}
	return fmt.Sprintf(
		"Synthesized from %d randomly sampled raw queries across %d uploaded sessions, with %d ms average first-response latency and %d average tokens per query.",
		len(sampledQueries),
		usageSummary.SessionCount,
		usageSummary.AvgFirstResponseLatencyMS,
		usageSummary.AvgTokensPerQuery,
	)
}

func instructionRecommendationScore(sampleCount int, avgTokensPerQuery float64) float64 {
	score := 0.68 + 0.01*float64(sampleCount)
	if avgTokensPerQuery >= 2500 {
		score += 0.07
	}
	if score > 0.93 {
		score = 0.93
	}
	return round(score)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func buildResearchUsageSummary(sessions []*SessionSummary, rawQueries []string) researchUsageSummary {
	summary := researchUsageSummary{
		SessionCount:  len(sessions),
		RawQueryCount: len(rawQueries),
	}

	totalTokens := 0
	totalLatencyMS := 0
	knownLatencySessions := 0
	totalDurationMS := 0
	knownDurationSessions := 0
	recentSessions := make([]researchSessionSnapshot, 0, len(sessions))

	for _, session := range sessions {
		summary.TotalInputTokens += session.TokenIn
		summary.TotalOutputTokens += session.TokenOut
		summary.TotalCachedInputTokens += session.CachedInputTokens
		summary.TotalReasoningOutputTokens += session.ReasoningOutputTokens
		summary.TotalFunctionCalls += session.FunctionCallCount
		summary.TotalToolErrors += session.ToolErrorCount
		summary.TotalToolWallTimeMS += session.ToolWallTimeMS
		totalTokens += session.TokenIn + session.TokenOut
		if session.FunctionCallCount > 0 {
			summary.SessionsWithFunctionCalls++
		}
		if session.ToolErrorCount > 0 {
			summary.SessionsWithToolErrors++
		}
		if session.FirstResponseLatencyMS > 0 {
			totalLatencyMS += session.FirstResponseLatencyMS
			knownLatencySessions++
		}
		if session.SessionDurationMS > 0 {
			totalDurationMS += session.SessionDurationMS
			knownDurationSessions++
		}
		recentSessions = append(recentSessions, researchSessionSnapshot{
			TimestampLabel:         session.Timestamp.UTC().Format(time.RFC3339),
			Tool:                   firstNonEmptyString(strings.TrimSpace(session.Tool), "unknown"),
			QueryCount:             len(session.RawQueries),
			InputTokens:            session.TokenIn,
			OutputTokens:           session.TokenOut,
			CachedInputTokens:      session.CachedInputTokens,
			ReasoningOutputTokens:  session.ReasoningOutputTokens,
			FirstResponseLatencyMS: session.FirstResponseLatencyMS,
			SessionDurationMS:      session.SessionDurationMS,
			FunctionCallCount:      session.FunctionCallCount,
			ToolErrorCount:         session.ToolErrorCount,
			ToolWallTimeMS:         session.ToolWallTimeMS,
		})
	}

	sort.Slice(recentSessions, func(i, j int) bool {
		return recentSessions[i].TimestampLabel > recentSessions[j].TimestampLabel
	})
	if len(recentSessions) > 5 {
		recentSessions = recentSessions[:5]
	}
	summary.RecentSessions = recentSessions
	summary.AvgTokensPerQuery = int(round(safeDiv(float64(totalTokens), float64(maxInt(len(rawQueries), 1)))))
	summary.AvgFirstResponseLatencyMS = int(round(safeDiv(float64(totalLatencyMS), float64(maxInt(knownLatencySessions, 1)))))
	summary.AvgSessionDurationMS = int(round(safeDiv(float64(totalDurationMS), float64(maxInt(knownDurationSessions, 1)))))
	return summary
}
