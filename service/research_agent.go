package service

import (
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
	defaultInstructionHeading        = "## AgentOpt Personal Instruction Pack"
	openAIInstructionSchemaName      = "agent_instruction_pack"
	openAIInstructionSchemaFieldName = "instruction_markdown"
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

var personalInstructionPatterns = []instructionPattern{
	{
		Key:         "repo_discovery",
		Label:       "repo discovery",
		Terms:       []string{"find", "inspect", "explore", "locate", "repo", "which file", "control flow", "summarize the current"},
		Instruction: "Before editing, identify the exact files involved and summarize the current control flow.",
	},
	{
		Key:         "root_cause",
		Label:       "root-cause analysis",
		Terms:       []string{"why", "root cause", "cause", "bug", "error", "failing", "regression"},
		Instruction: "State the likely root cause in one sentence before proposing a patch.",
	},
	{
		Key:         "minimal_patch",
		Label:       "minimal patching",
		Terms:       []string{"minimal", "smallest", "small", "least", "patch", "fix only", "without changing"},
		Instruction: "Prefer the smallest viable patch and call out any behavior that stays intentionally unchanged.",
	},
	{
		Key:         "verification",
		Label:       "targeted verification",
		Terms:       []string{"test", "verify", "verification", "regression", "repro", "run", "check"},
		Instruction: "List the exact verification steps or targeted tests immediately after each substantial edit.",
	},
	{
		Key:         "contract_review",
		Label:       "contract comparison",
		Terms:       []string{"compare", "same", "contract", "response", "shared", "similar"},
		Instruction: "Compare neighboring implementations before changing a shared API, route, or response contract.",
	},
}

type openAIInstructionResponse struct {
	InstructionMarkdown string `json:"instruction_markdown"`
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
	if len(rawQueries) == 0 {
		return nil
	}

	totalTokens := 0
	for _, session := range sessions {
		totalTokens += session.TokenIn + session.TokenOut
	}
	avgTokensPerQuery := safeDiv(float64(totalTokens), float64(maxInt(len(rawQueries), 1)))
	sampledQueries := sampleRawQueries(rawQueries, minInt(a.sampleSize, len(rawQueries)), a.randSource)
	contentPreview, generationMode := a.buildInstructionPreview(project, sampledQueries, rawQueries, avgTokensPerQuery)

	evidence := []string{
		fmt.Sprintf("sessions=%d", len(sessions)),
		fmt.Sprintf("raw_query_count=%d", len(rawQueries)),
		fmt.Sprintf("sampled_raw_queries=%d", len(sampledQueries)),
		fmt.Sprintf("avg_tokens_per_query=%.0f", avgTokensPerQuery),
		"selection=random",
		"target_file=AGENTS.md",
		"generation_mode=" + generationMode,
	}

	return []researchRecommendation{{
		Kind:            "instruction-custom-rules",
		Title:           instructionRecommendationTitle(project),
		Summary:         "Recent session queries were sampled and distilled into a reusable instruction block for the local coding agent.",
		Reason:          buildInstructionReason(sampledQueries, len(sessions)),
		Explanation:     "The research agent samples up to 10 raw queries, asks OpenAI Responses API for a reusable instruction pack, and leaves the actual file edit to the local Codex agent.",
		ExpectedBenefit: "Reduce repeated prompt boilerplate and make the first useful answer more consistent.",
		Risk:            "Low. The plan is a reviewable append to AGENTS.md.",
		ExpectedImpact:  "Lower setup churn and fewer repeated discovery prompts in later sessions.",
		Score:           instructionRecommendationScore(len(sampledQueries), avgTokensPerQuery),
		Evidence:        evidence,
		Steps: []ChangePlanStep{{
			Type:           "text_append",
			Action:         "append_block",
			TargetFile:     "AGENTS.md",
			Summary:        "Append an instruction block synthesized from sampled raw queries.",
			ContentPreview: contentPreview,
		}},
	}}
}

func instructionRecommendationTitle(project *Project) string {
	if project != nil && strings.TrimSpace(project.Name) != "" {
		return "Add a shared instruction block for " + project.Name
	}
	return "Add a shared instruction block"
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

func (a *CloudResearchAgent) buildInstructionPreview(project *Project, sampledQueries, rawQueries []string, avgTokensPerQuery float64) (string, string) {
	markdown, err := a.generateInstructionMarkdown(project, sampledQueries)
	if err == nil && strings.TrimSpace(markdown) != "" {
		return wrapInstructionMarkdown(markdown), "openai_responses_api"
	}
	return buildFallbackInstructionContent(rawQueries, avgTokensPerQuery), "local_fallback"
}

func (a *CloudResearchAgent) generateInstructionMarkdown(project *Project, sampledQueries []string) (string, error) {
	if strings.TrimSpace(a.apiKey) == "" {
		return "", fmt.Errorf("OPENAI_API_KEY is not configured")
	}
	if len(sampledQueries) == 0 {
		return "", fmt.Errorf("no sampled queries available")
	}

	format := responses.ResponseFormatTextConfigParamOfJSONSchema(openAIInstructionSchemaName, map[string]any{
		"type": "object",
		"properties": map[string]any{
			openAIInstructionSchemaFieldName: map[string]any{
				"type":        "string",
				"description": "Markdown bullet lines to append under an AGENTS.md heading.",
			},
		},
		"required":             []string{openAIInstructionSchemaFieldName},
		"additionalProperties": false,
	})
	if format.OfJSONSchema != nil {
		format.OfJSONSchema.Strict = openai.Bool(true)
		format.OfJSONSchema.Description = openai.String("Markdown bullet lines to append under an AGENTS.md heading.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultResearchRequestTimeout)
	defer cancel()

	prompt, err := buildInstructionPrompt(project, sampledQueries)
	if err != nil {
		return "", err
	}

	resp, err := a.client.Responses.New(ctx, responses.ResponseNewParams{
		Model: openai.ResponsesModel(a.Model),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(prompt),
		},
		Text: responses.ResponseTextConfigParam{
			Format: format,
		},
	})
	if err != nil {
		return "", err
	}

	var structured openAIInstructionResponse
	if err := json.Unmarshal([]byte(resp.OutputText()), &structured); err != nil {
		return "", fmt.Errorf("decode openai instruction payload: %w", err)
	}
	return normalizeInstructionMarkdown(structured.InstructionMarkdown), nil
}

func buildInstructionPrompt(project *Project, sampledQueries []string) (string, error) {
	return renderResearchAgentInstructionPrompt(project, sampledQueries)
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

func buildFallbackInstructionContent(rawQueries []string, avgTokensPerQuery float64) string {
	matches := topInstructionPatterns(matchInstructionPatterns(rawQueries), 3)
	lines := []string{
		"",
		defaultInstructionHeading,
		"- Before editing, restate the goal, affected files, and the success check you will use.",
	}
	for _, match := range matches {
		lines = append(lines, "- "+match.Pattern.Instruction)
	}
	if avgTokensPerQuery >= 2500 {
		lines = append(lines, "- Keep the first response compact and avoid reopening files without new evidence.")
	}
	return strings.Join(lines, "\n") + "\n"
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

func buildInstructionReason(sampledQueries []string, sessionCount int) string {
	if len(sampledQueries) == 0 {
		return "No sampled raw queries were available for instruction synthesis."
	}
	return fmt.Sprintf("Synthesized from %d randomly sampled raw queries across %d uploaded sessions.", len(sampledQueries), sessionCount)
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
