package service

import (
	"bytes"
	"context"
	"encoding/json"
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
	defaultOpenAIResponsesModel      = "gpt-5.4"
	defaultResearchSampleSize        = 10
	defaultResearchRequestTimeout    = 45 * time.Second
	defaultResearchEvidenceLimit     = 5
	defaultProjectInstructionTarget  = "AGENTS.md"
	defaultProjectSkillTarget        = ".codex/skills/agentopt-repo-discovery/SKILL.md"
	defaultProjectHarnessSkillTarget = ".codex/skills/agentopt-test-harness/SKILL.md"
	defaultProjectHarnessSpecTarget  = ".agentopt/harness/agentopt-default.json"
	defaultCodexInstructionTarget    = defaultProjectInstructionTarget
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
	HarnessSpec     *HarnessSpec
	Settings        map[string]any
	RawSuggestion   string
}

type researchRecommendationPayload struct {
	Recommendations []json.RawMessage `json:"recommendations"`
}

type researchRecommendationItemPayload struct {
	Kind            string                          `json:"kind"`
	Title           string                          `json:"title"`
	Summary         string                          `json:"summary"`
	Reason          string                          `json:"reason"`
	Explanation     string                          `json:"explanation"`
	ExpectedBenefit string                          `json:"expected_benefit"`
	Risk            string                          `json:"risk"`
	ExpectedImpact  string                          `json:"expected_impact"`
	Score           float64                         `json:"score"`
	Evidence        []string                        `json:"evidence"`
	ChangePlan      []researchChangePlanStepPayload `json:"change_plan"`
	HarnessSpec     *researchHarnessSpecPayload     `json:"harness_spec"`
}

type researchChangePlanStepPayload struct {
	Type            string         `json:"type"`
	Action          string         `json:"action"`
	TargetFile      string         `json:"target_file"`
	Summary         string         `json:"summary"`
	SettingsUpdates map[string]any `json:"settings_updates"`
	ContentPreview  string         `json:"content_preview"`
}

type researchHarnessSpecPayload struct {
	Version       int                               `json:"version"`
	Name          string                            `json:"name"`
	Goal          string                            `json:"goal"`
	TargetPaths   []string                          `json:"target_paths"`
	SetupCommands []string                          `json:"setup_commands"`
	TestCommands  []string                          `json:"test_commands"`
	Assertions    []researchHarnessAssertionPayload `json:"assertions"`
	AntiGoals     []string                          `json:"anti_goals"`
}

type researchHarnessAssertionPayload struct {
	Kind        string `json:"kind"`
	Equals      int    `json:"equals,omitempty"`
	Contains    string `json:"contains,omitempty"`
	NotContains string `json:"not_contains,omitempty"`
}

type workflowPattern struct {
	Key   string
	Terms []string
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

type researchInteractionSample struct {
	TimestampLabel     string
	Tool               string
	Queries            []string
	AssistantResponses []string
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

var workflowPatterns = []workflowPattern{
	{
		Key:   "repo_discovery",
		Terms: []string{"find", "inspect", "explore", "locate", "repo", "which file", "control flow", "summarize the current"},
	},
	{
		Key:   "root_cause",
		Terms: []string{"why", "root cause", "cause", "bug", "error", "failing", "regression"},
	},
	{
		Key:   "verification",
		Terms: []string{"test", "verify", "verification", "regression", "repro", "run", "check"},
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
	clientOptions := []option.RequestOption{}
	if apiKey != "" {
		clientOptions = append(clientOptions, option.WithAPIKey(apiKey))
		clientOptions = append(clientOptions, option.WithHTTPClient(&http.Client{Timeout: defaultResearchRequestTimeout}))
		if baseURL := strings.TrimSpace(openAIConf.BaseURL); baseURL != "" {
			clientOptions = append(clientOptions, option.WithBaseURL(baseURL))
		}
	} else {
		provider = "disabled"
		mode = "disabled"
		model = ""
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

func (a *CloudResearchAgent) AnalyzeProject(project *Project, sessions []*SessionSummary, snapshots []*ConfigSnapshot) ([]researchRecommendation, error) {
	_ = snapshots

	rawQueries := collectRawQueries(sessions)
	rawQueries = normalizeQueriesForResearchPrompt(rawQueries)
	if len(rawQueries) == 0 {
		return nil, nil
	}
	if strings.TrimSpace(a.apiKey) == "" {
		return nil, nil
	}

	usageSummary := buildResearchUsageSummary(sessions, rawQueries)
	sampledQueries := sampleRawQueries(rawQueries, minInt(a.sampleSize, len(rawQueries)), a.randSource)
	interactionSamples := buildResearchInteractionSamples(sessions, defaultResearchEvidenceLimit)
	recommendations, err := a.generateRecommendations(project, sampledQueries, interactionSamples, usageSummary)
	if err != nil {
		return nil, err
	}
	recommendations = localizeResearchRecommendations(project, recommendations)
	sort.Slice(recommendations, func(i, j int) bool {
		if recommendations[i].Score == recommendations[j].Score {
			return recommendations[i].Title < recommendations[j].Title
		}
		return recommendations[i].Score > recommendations[j].Score
	})
	return recommendations, nil
}

func localizeResearchRecommendations(project *Project, items []researchRecommendation) []researchRecommendation {
	if len(items) == 0 {
		return items
	}
	out := make([]researchRecommendation, 0, len(items))
	for _, item := range items {
		clone := item
		clone.HarnessSpec = cloneHarnessSpec(item.HarnessSpec)
		clone.Steps = make([]ChangePlanStep, 0, len(item.Steps))
		for _, step := range item.Steps {
			clone.Steps = append(clone.Steps, ChangePlanStep{
				Type:            step.Type,
				Action:          step.Action,
				TargetFile:      localizeResearchTargetFile(project, step.TargetFile),
				Summary:         step.Summary,
				SettingsUpdates: localizeInstructionFilesSettings(step.SettingsUpdates),
				ContentPreview:  step.ContentPreview,
			})
		}
		clone.Settings = localizeInstructionFilesSettings(item.Settings)
		out = append(out, clone)
	}
	return out
}

func localizeResearchTargetFile(project *Project, target string) string {
	target = strings.TrimSpace(target)
	switch target {
	case "", "~/.codex/AGENTS.md":
		return defaultProjectInstructionTarget
	case "~/.codex/skills/agentopt-repo-discovery/SKILL.md":
		return defaultProjectSkillTarget
	case "~/.codex/skills/agentopt-test-harness/SKILL.md":
		return defaultProjectHarnessSkillTarget
	case ".agentopt/harness/default.json":
		return defaultProjectHarnessSpecTarget
	default:
		if strings.HasPrefix(target, "~/.codex/skills/agentopt-") && strings.HasSuffix(target, "/SKILL.md") {
			return strings.TrimPrefix(target, "~/")
		}
		return target
	}
}

func localizeInstructionFilesSettings(settings map[string]any) map[string]any {
	if len(settings) == 0 {
		return cloneAnyMap(settings)
	}
	cloned := cloneAnyMap(settings)
	raw, ok := cloned["instruction_files"]
	if !ok {
		return cloned
	}
	items, ok := raw.([]any)
	if !ok {
		return cloned
	}
	out := make([]any, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			continue
		}
		text = strings.TrimSpace(text)
		switch text {
		case "~/.codex/AGENTS.md":
			text = defaultProjectInstructionTarget
		case "~/.codex/skills/agentopt-repo-discovery/SKILL.md":
			text = defaultProjectSkillTarget
		}
		if text == "" {
			continue
		}
		if _, exists := seen[text]; exists {
			continue
		}
		seen[text] = struct{}{}
		out = append(out, text)
	}
	if len(out) > 0 {
		cloned["instruction_files"] = out
	}
	return cloned
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

func (a *CloudResearchAgent) generateRecommendations(project *Project, sampledQueries []string, interactionSamples []researchInteractionSample, usageSummary researchUsageSummary) ([]researchRecommendation, error) {
	if len(sampledQueries) == 0 {
		return nil, fmt.Errorf("no sampled queries available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultResearchRequestTimeout)
	defer cancel()

	prompt, err := buildRecommendationsPrompt(project, sampledQueries, interactionSamples, usageSummary)
	if err != nil {
		return nil, err
	}

	resp, err := a.client.Responses.New(ctx, responses.ResponseNewParams{
		Model: openai.ResponsesModel(a.Model),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(prompt),
		},
	})
	if err != nil {
		return nil, err
	}
	return parseResearchRecommendations(resp.OutputText())
}

func buildRecommendationsPrompt(project *Project, sampledQueries []string, interactionSamples []researchInteractionSample, usageSummary researchUsageSummary) (string, error) {
	return renderResearchAgentRecommendationsPrompt(project, sampledQueries, interactionSamples, usageSummary)
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

func parseResearchRecommendations(raw string) ([]researchRecommendation, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	var payload researchRecommendationPayload
	if err := json.Unmarshal([]byte(cleaned), &payload); err != nil {
		return nil, err
	}

	items := make([]researchRecommendation, 0, len(payload.Recommendations))
	for _, rawItem := range payload.Recommendations {
		var item researchRecommendationItemPayload
		if err := json.Unmarshal(rawItem, &item); err != nil {
			continue
		}
		rec, ok := sanitizeResearchRecommendation(item, formatResearchSuggestion(rawItem))
		if ok {
			items = append(items, rec)
		}
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("no valid recommendations returned")
	}
	return items, nil
}

func sanitizeResearchRecommendation(item researchRecommendationItemPayload, rawSuggestion string) (researchRecommendation, bool) {
	steps := make([]ChangePlanStep, 0, len(item.ChangePlan))
	for _, step := range item.ChangePlan {
		if strings.TrimSpace(step.Action) == "" || strings.TrimSpace(step.TargetFile) == "" {
			continue
		}
		steps = append(steps, ChangePlanStep{
			Type:            strings.TrimSpace(step.Type),
			Action:          strings.TrimSpace(step.Action),
			TargetFile:      strings.TrimSpace(step.TargetFile),
			Summary:         strings.TrimSpace(step.Summary),
			SettingsUpdates: cloneAnyMap(step.SettingsUpdates),
			ContentPreview:  strings.TrimSpace(step.ContentPreview),
		})
	}
	if strings.TrimSpace(item.Title) == "" || strings.TrimSpace(item.Summary) == "" || len(steps) == 0 {
		return researchRecommendation{}, false
	}

	kind := sanitizeResearchRecommendationID(strings.TrimSpace(item.Kind))
	if kind == "" {
		kind = sanitizeResearchRecommendationID(strings.TrimSpace(item.Title))
	}
	if kind == "" {
		kind = "llm-generated-recommendation"
	}

	score := item.Score
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}

	evidence := make([]string, 0, len(item.Evidence))
	for _, entry := range item.Evidence {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		evidence = append(evidence, entry)
	}

	return researchRecommendation{
		Kind:            kind,
		Title:           strings.TrimSpace(item.Title),
		Summary:         strings.TrimSpace(item.Summary),
		Reason:          strings.TrimSpace(item.Reason),
		Explanation:     strings.TrimSpace(item.Explanation),
		ExpectedBenefit: strings.TrimSpace(item.ExpectedBenefit),
		Risk:            strings.TrimSpace(item.Risk),
		ExpectedImpact:  strings.TrimSpace(item.ExpectedImpact),
		Score:           round(score),
		Evidence:        evidence,
		Steps:           steps,
		HarnessSpec:     sanitizeResearchHarnessSpec(item.HarnessSpec),
		RawSuggestion:   strings.TrimSpace(rawSuggestion),
	}, true
}

func sanitizeResearchHarnessSpec(item *researchHarnessSpecPayload) *HarnessSpec {
	if item == nil {
		return nil
	}
	testCommands := make([]string, 0, len(item.TestCommands))
	for _, command := range item.TestCommands {
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}
		testCommands = append(testCommands, command)
	}
	if len(testCommands) == 0 {
		return nil
	}
	setupCommands := make([]string, 0, len(item.SetupCommands))
	for _, command := range item.SetupCommands {
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}
		setupCommands = append(setupCommands, command)
	}
	assertions := make([]HarnessAssertion, 0, len(item.Assertions))
	for _, assertion := range item.Assertions {
		kind := strings.TrimSpace(assertion.Kind)
		if kind == "" {
			continue
		}
		assertions = append(assertions, HarnessAssertion{
			Kind:        kind,
			Equals:      assertion.Equals,
			Contains:    strings.TrimSpace(assertion.Contains),
			NotContains: strings.TrimSpace(assertion.NotContains),
		})
	}
	version := item.Version
	if version <= 0 {
		version = 1
	}
	return &HarnessSpec{
		Version:       version,
		Name:          strings.TrimSpace(item.Name),
		Goal:          strings.TrimSpace(item.Goal),
		TargetPaths:   normalizeResearchStringSlice(item.TargetPaths),
		SetupCommands: setupCommands,
		TestCommands:  testCommands,
		Assertions:    assertions,
		AntiGoals:     normalizeResearchStringSlice(item.AntiGoals),
	}
}

func normalizeResearchStringSlice(input []string) []string {
	if len(input) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(input))
	for _, entry := range input {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func formatResearchSuggestion(raw json.RawMessage) string {
	cleaned := strings.TrimSpace(string(raw))
	if cleaned == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(cleaned), "", "  "); err != nil {
		return cleaned
	}
	return buf.String()
}

func sanitizeResearchRecommendationID(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
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

func countWorkflowPatternMatches(queries []string) map[string]int {
	counts := make(map[string]int, len(workflowPatterns))
	for _, query := range queries {
		normalized := strings.ToLower(strings.TrimSpace(query))
		if normalized == "" {
			continue
		}
		for _, pattern := range workflowPatterns {
			if queryMatchesAnyTerm(normalized, pattern.Terms) {
				counts[pattern.Key]++
			}
		}
	}
	return counts
}

func queryMatchesAnyTerm(query string, terms []string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	for _, term := range terms {
		if strings.Contains(query, term) {
			return true
		}
	}
	return false
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

func buildResearchInteractionSamples(sessions []*SessionSummary, limit int) []researchInteractionSample {
	if limit <= 0 || len(sessions) == 0 {
		return nil
	}

	pool := append([]*SessionSummary(nil), sessions...)
	sort.Slice(pool, func(i, j int) bool {
		return pool[i].Timestamp.After(pool[j].Timestamp)
	})

	items := make([]researchInteractionSample, 0, minInt(limit, len(pool)))
	for _, session := range pool {
		if session == nil {
			continue
		}
		queries := normalizeQueriesForResearchPrompt(session.RawQueries)
		if len(queries) > 3 {
			queries = append([]string(nil), queries[:3]...)
		}

		responses := make([]string, 0, len(session.AssistantResponses))
		for _, response := range session.AssistantResponses {
			response = strings.TrimSpace(response)
			if response == "" {
				continue
			}
			responses = append(responses, response)
			if len(responses) >= 2 {
				break
			}
		}

		if len(queries) == 0 && len(responses) == 0 {
			continue
		}

		items = append(items, researchInteractionSample{
			TimestampLabel:     session.Timestamp.UTC().Format(time.RFC3339),
			Tool:               firstNonEmptyString(strings.TrimSpace(session.Tool), "unknown"),
			Queries:            append([]string(nil), queries...),
			AssistantResponses: responses,
		})
		if len(items) >= limit {
			break
		}
	}
	return items
}
