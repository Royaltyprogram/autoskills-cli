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
	defaultEvaluationSampleSize     = 8
	defaultEvaluationRequestTimeout = 45 * time.Second
)

type CloudEvaluationAgent struct {
	Provider string
	Model    string
	Mode     string

	apiKey     string
	client     openai.Client
	sampleSize int
	randSource *rand.Rand
}

type experimentEvaluation struct {
	Decision   string
	Confidence string
	Summary    string
	Provider   string
	Model      string
	Mode       string
}

type evaluationAgentResponse struct {
	Decision   string `json:"decision"`
	Confidence string `json:"confidence"`
	Summary    string `json:"summary"`
}

func NewCloudEvaluationAgent(conf *configs.Config) *CloudEvaluationAgent {
	var openAIConf configs.OpenAI
	if conf != nil {
		openAIConf = conf.OpenAI
	}
	apiKey := strings.TrimSpace(openAIConf.APIKey)
	model := firstNonEmptyString(strings.TrimSpace(openAIConf.ResponsesModel), defaultOpenAIResponsesModel)
	provider := "openai"
	mode := "responses-api"
	clientOptions := []option.RequestOption{}
	if apiKey == "" {
		provider = "local"
		mode = "disabled"
		model = "qualitative-fallback"
	} else {
		clientOptions = append(clientOptions, option.WithAPIKey(apiKey))
		clientOptions = append(clientOptions, option.WithHTTPClient(&http.Client{Timeout: defaultEvaluationRequestTimeout}))
		if baseURL := strings.TrimSpace(openAIConf.BaseURL); baseURL != "" {
			clientOptions = append(clientOptions, option.WithBaseURL(baseURL))
		}
	}

	return &CloudEvaluationAgent{
		Provider:   provider,
		Model:      model,
		Mode:       mode,
		apiKey:     apiKey,
		client:     openai.NewClient(clientOptions...),
		sampleSize: defaultEvaluationSampleSize,
		randSource: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (a *CloudEvaluationAgent) ReviewExperiment(project *Project, before, after []*SessionSummary, beforeStats, afterStats sessionSummaryStats) experimentEvaluation {
	beforeQueries := sampleQueriesForEvaluation(before, a.sampleSize, a.randSource)
	afterQueries := sampleQueriesForEvaluation(after, a.sampleSize, a.randSource)
	if len(beforeQueries) == 0 && len(afterQueries) == 0 {
		return experimentEvaluation{
			Decision:   "observe",
			Confidence: "low",
			Summary:    "Qualitative evaluation skipped because no raw queries were available around the rollout.",
			Provider:   a.Provider,
			Model:      a.Model,
			Mode:       "no-query-context",
		}
	}
	if strings.TrimSpace(a.apiKey) == "" {
		return a.reviewExperimentFallback(beforeQueries, afterQueries)
	}

	prompt, err := renderEvaluationAgentPrompt(
		project,
		buildEvaluationNumericSummary(beforeStats, afterStats, len(before), len(after)),
		formatEvaluationQueries(beforeQueries),
		formatEvaluationQueries(afterQueries),
	)
	if err != nil {
		return experimentEvaluation{
			Decision:   "observe",
			Confidence: "low",
			Summary:    fmt.Sprintf("Qualitative evaluation prompt failed: %v", err),
			Provider:   a.Provider,
			Model:      a.Model,
			Mode:       "prompt-error",
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultEvaluationRequestTimeout)
	defer cancel()

	resp, err := a.client.Responses.New(ctx, responses.ResponseNewParams{
		Model: openai.ResponsesModel(a.Model),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(prompt),
		},
	})
	if err != nil {
		return experimentEvaluation{
			Decision:   "observe",
			Confidence: "low",
			Summary:    fmt.Sprintf("Qualitative evaluation failed: %v", err),
			Provider:   a.Provider,
			Model:      a.Model,
			Mode:       "openai-error",
		}
	}

	parsed, err := parseEvaluationResponse(resp.OutputText())
	if err != nil {
		return experimentEvaluation{
			Decision:   "observe",
			Confidence: "low",
			Summary:    fmt.Sprintf("Qualitative evaluation returned invalid JSON: %v", err),
			Provider:   a.Provider,
			Model:      a.Model,
			Mode:       "parse-error",
		}
	}
	return experimentEvaluation{
		Decision:   parsed.Decision,
		Confidence: parsed.Confidence,
		Summary:    parsed.Summary,
		Provider:   a.Provider,
		Model:      a.Model,
		Mode:       a.Mode,
	}
}

func (a *CloudEvaluationAgent) reviewExperimentFallback(beforeQueries, afterQueries []evaluationQuerySample) experimentEvaluation {
	beforeTexts := flattenEvaluationQueries(beforeQueries)
	afterTexts := flattenEvaluationQueries(afterQueries)

	beforeCounts := countInstructionPatternMatches(beforeTexts)
	afterCounts := countInstructionPatternMatches(afterTexts)

	negativeTerms := []string{"slower", "slow", "rollback", "revert", "worse", "regression", "rediscovery", "larger trace"}
	positiveTerms := []string{"minimal", "targeted", "exact", "verification", "smaller", "focused"}

	negativeBefore := countMatchingTerms(beforeTexts, negativeTerms)
	negativeAfter := countMatchingTerms(afterTexts, negativeTerms)
	positiveBefore := countMatchingTerms(beforeTexts, positiveTerms)
	positiveAfter := countMatchingTerms(afterTexts, positiveTerms)

	workflowLoadBefore := beforeCounts["repo_discovery"] + beforeCounts["root_cause"] + beforeCounts["verification"]
	workflowLoadAfter := afterCounts["repo_discovery"] + afterCounts["root_cause"] + afterCounts["verification"]

	switch {
	case negativeAfter > negativeBefore:
		return experimentEvaluation{
			Decision:   "rollback",
			Confidence: "medium",
			Summary:    "After the rollout the raw queries show more slowdown, rollback, or rediscovery language, which suggests the workflow got worse.",
			Provider:   "local",
			Model:      "qualitative-fallback",
			Mode:       "local-heuristic",
		}
	case workflowLoadAfter > workflowLoadBefore+1:
		return experimentEvaluation{
			Decision:   "rollback",
			Confidence: "medium",
			Summary:    "After the rollout the user spends more queries on discovery, diagnosis, or verification overhead, which suggests the new workflow added friction.",
			Provider:   "local",
			Model:      "qualitative-fallback",
			Mode:       "local-heuristic",
		}
	case workflowLoadAfter < workflowLoadBefore && positiveAfter >= positiveBefore:
		return experimentEvaluation{
			Decision:   "keep",
			Confidence: "medium",
			Summary:    "After the rollout the raw queries look more focused and require less discovery overhead, which suggests the workflow improved.",
			Provider:   "local",
			Model:      "qualitative-fallback",
			Mode:       "local-heuristic",
		}
	default:
		return experimentEvaluation{
			Decision:   "observe",
			Confidence: "low",
			Summary:    "The raw-query review does not show a strong enough workflow change yet, so the rollout should keep being observed.",
			Provider:   "local",
			Model:      "qualitative-fallback",
			Mode:       "local-heuristic",
		}
	}
}

func parseEvaluationResponse(raw string) (evaluationAgentResponse, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	var parsed evaluationAgentResponse
	if err := json.Unmarshal([]byte(cleaned), &parsed); err != nil {
		return evaluationAgentResponse{}, err
	}
	parsed.Decision = normalizeEvaluationDecision(parsed.Decision)
	parsed.Confidence = normalizeEvaluationConfidence(parsed.Confidence)
	parsed.Summary = strings.TrimSpace(parsed.Summary)
	if parsed.Summary == "" {
		return evaluationAgentResponse{}, fmt.Errorf("empty summary")
	}
	return parsed, nil
}

func normalizeEvaluationDecision(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "keep":
		return "keep"
	case "rollback":
		return "rollback"
	default:
		return "observe"
	}
}

func normalizeEvaluationConfidence(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "high":
		return "high"
	case "medium":
		return "medium"
	default:
		return "low"
	}
}

type evaluationQuerySample struct {
	Timestamp string
	Query     string
}

func sampleQueriesForEvaluation(sessions []*SessionSummary, limit int, rng *rand.Rand) []evaluationQuerySample {
	if limit <= 0 || len(sessions) == 0 {
		return nil
	}

	type candidate struct {
		timestamp time.Time
		query     string
	}
	pool := make([]candidate, 0)
	for _, session := range sessions {
		if session == nil {
			continue
		}
		for _, query := range normalizeQueriesForResearchPrompt(session.RawQueries) {
			if strings.TrimSpace(query) == "" {
				continue
			}
			pool = append(pool, candidate{
				timestamp: session.Timestamp.UTC(),
				query:     query,
			})
		}
	}
	if len(pool) == 0 {
		return nil
	}
	sort.Slice(pool, func(i, j int) bool {
		if pool[i].timestamp.Equal(pool[j].timestamp) {
			return pool[i].query < pool[j].query
		}
		return pool[i].timestamp.Before(pool[j].timestamp)
	})
	if len(pool) > limit {
		trimmed := append([]candidate(nil), pool...)
		if rng == nil {
			rng = rand.New(rand.NewSource(time.Now().UnixNano()))
		}
		rng.Shuffle(len(trimmed), func(i, j int) {
			trimmed[i], trimmed[j] = trimmed[j], trimmed[i]
		})
		trimmed = trimmed[:limit]
		sort.Slice(trimmed, func(i, j int) bool {
			if trimmed[i].timestamp.Equal(trimmed[j].timestamp) {
				return trimmed[i].query < trimmed[j].query
			}
			return trimmed[i].timestamp.Before(trimmed[j].timestamp)
		})
		pool = trimmed
	}

	out := make([]evaluationQuerySample, 0, len(pool))
	for _, item := range pool {
		out = append(out, evaluationQuerySample{
			Timestamp: item.timestamp.Format(time.RFC3339),
			Query:     item.query,
		})
	}
	return out
}

func formatEvaluationQueries(samples []evaluationQuerySample) string {
	if len(samples) == 0 {
		return "- none"
	}
	lines := make([]string, 0, len(samples))
	for _, sample := range samples {
		lines = append(lines, fmt.Sprintf("- %s | %s", sample.Timestamp, sample.Query))
	}
	return strings.Join(lines, "\n")
}

func buildEvaluationNumericSummary(before, after sessionSummaryStats, beforeSessions, afterSessions int) string {
	lines := []string{
		fmt.Sprintf("- before_sessions=%d", beforeSessions),
		fmt.Sprintf("- after_sessions=%d", afterSessions),
		fmt.Sprintf("- before_queries=%d", before.QueryCount),
		fmt.Sprintf("- after_queries=%d", after.QueryCount),
		fmt.Sprintf("- before_avg_tokens_per_query=%.2f", before.AvgTokensPerQuery),
		fmt.Sprintf("- after_avg_tokens_per_query=%.2f", after.AvgTokensPerQuery),
		fmt.Sprintf("- before_avg_first_response_latency_ms=%.2f", before.AvgFirstResponseLatencyMS),
		fmt.Sprintf("- after_avg_first_response_latency_ms=%.2f", after.AvgFirstResponseLatencyMS),
		fmt.Sprintf("- before_avg_tool_errors_per_session=%.2f", before.AvgToolErrorsPerSession),
		fmt.Sprintf("- after_avg_tool_errors_per_session=%.2f", after.AvgToolErrorsPerSession),
	}
	return strings.Join(lines, "\n")
}

func flattenEvaluationQueries(samples []evaluationQuerySample) []string {
	out := make([]string, 0, len(samples))
	for _, sample := range samples {
		if strings.TrimSpace(sample.Query) == "" {
			continue
		}
		out = append(out, sample.Query)
	}
	return out
}

func countMatchingTerms(queries, terms []string) int {
	count := 0
	for _, query := range queries {
		normalized := strings.ToLower(strings.TrimSpace(query))
		if normalized == "" {
			continue
		}
		for _, term := range terms {
			if strings.Contains(normalized, term) {
				count++
				break
			}
		}
	}
	return count
}
