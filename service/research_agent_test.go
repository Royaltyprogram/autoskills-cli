package service

import (
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Royaltyprogram/aiops/configs"
)

func TestCloudResearchAgentAnalyzeProjectUsesOpenAIResponses(t *testing.T) {
	type responsesRequest struct {
		Model string `json:"model"`
		Input string `json:"input"`
	}

	var seen responsesRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/responses", r.URL.Path)
		require.Equal(t, "Bearer test-openai-key", r.Header.Get("Authorization"))

		require.NoError(t, json.NewDecoder(r.Body).Decode(&seen))
		require.Equal(t, "gpt-5.4", seen.Model)
		require.Contains(t, seen.Input, "sample_query_1:")
		require.Contains(t, seen.Input, "sample_query_10:")

		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{
  "output": [
    {
      "type": "message",
      "content": [
        {
          "type": "output_text",
          "text": "- The user repeatedly has to ask for explicit verification, which suggests testing discipline is not being applied by default.\n- Discovery and control-flow recap consume enough turns that the workflow likely starts without enough repo context.\n- The sampled sessions show enough manual steering that default patch scope and diagnosis habits are still too weak."
        }
      ]
    }
  ]
}`))
		require.NoError(t, err)
	}))
	defer server.Close()

	agent := NewCloudResearchAgent(&configs.Config{
		OpenAI: configs.OpenAI{
			APIKey:         "test-openai-key",
			BaseURL:        server.URL + "/v1",
			ResponsesModel: "gpt-5.4",
		},
	})
	recs := agent.AnalyzeProject(&Project{Name: "demo-workspace"}, []*SessionSummary{{
		TokenIn:  1200,
		TokenOut: 400,
		RawQueries: []string{
			"Inspect the current analytics flow before editing it.",
			"Find the smallest patch for the failing route.",
			"List the exact tests to run after the patch.",
			"Compare this response contract with the health controller.",
			"State the likely root cause before editing.",
			"Keep the change minimal and do not refactor unrelated files.",
			"Check whether the workspace sync path already handles this case.",
			"Summarize the current control flow before proposing a fix.",
			"Verify whether rollback covers this scenario.",
			"Locate the files involved in the approval flow.",
			"Check if there is already a helper for this behavior.",
			"Explain why the regression appears only after sync.",
		},
	}}, nil)

	require.Len(t, recs, 1)
	require.Equal(t, "instruction-custom-rules", recs[0].Kind)
	require.Contains(t, recs[0].Evidence, "sampled_raw_queries=10")
	require.Contains(t, recs[0].Evidence, "generation_mode=openai_responses_api")
	require.Len(t, recs[0].Steps, 1)
	require.Equal(t, "AGENTS.md", recs[0].Steps[0].TargetFile)
	require.Contains(t, recs[0].Steps[0].ContentPreview, "## AgentOpt Research Findings")
	require.Contains(t, recs[0].Steps[0].ContentPreview, "- The user repeatedly has to ask for explicit verification")
	require.Contains(t, recs[0].Summary, "highlight repeated inefficiencies")
}

func TestBuildInstructionPromptLoadsMarkdownTemplate(t *testing.T) {
	prompt, err := buildInstructionPrompt(&Project{Name: "demo-workspace"}, []string{
		"Inspect the analytics route.",
		"List the exact verification steps.",
	}, researchUsageSummary{
		SessionCount:               2,
		RawQueryCount:              2,
		TotalInputTokens:           2200,
		TotalOutputTokens:          500,
		TotalCachedInputTokens:     700,
		TotalReasoningOutputTokens: 120,
		AvgTokensPerQuery:          1350,
		AvgFirstResponseLatencyMS:  1800,
		AvgSessionDurationMS:       75000,
		TotalFunctionCalls:         4,
		TotalToolErrors:            1,
		TotalToolWallTimeMS:        1600,
		SessionsWithFunctionCalls:  2,
		SessionsWithToolErrors:     1,
		RecentSessions: []researchSessionSnapshot{{
			TimestampLabel:         "2026-03-10T08:00:00Z",
			Tool:                   "codex",
			QueryCount:             2,
			InputTokens:            1200,
			OutputTokens:           280,
			CachedInputTokens:      300,
			ReasoningOutputTokens:  70,
			FirstResponseLatencyMS: 1900,
			SessionDurationMS:      81000,
			FunctionCallCount:      3,
			ToolErrorCount:         1,
			ToolWallTimeMS:         900,
		}},
	})

	require.NoError(t, err)
	require.Contains(t, prompt, "reviews a user's real coding-agent usage history")
	require.Contains(t, prompt, "## Requirements")
	require.Contains(t, prompt, "## Project")
	require.Contains(t, prompt, "demo-workspace")
	require.Contains(t, prompt, "## Usage Summary")
	require.Contains(t, prompt, "- avg_first_response_latency_ms=1800")
	require.Contains(t, prompt, "## Recent Session Metrics")
	require.Contains(t, prompt, "tool=codex")
	require.Contains(t, prompt, "## Sampled Raw Queries (2)")
	require.Contains(t, prompt, "sample_query_1: Inspect the analytics route.")
	require.Contains(t, prompt, "sample_query_2: List the exact verification steps.")
}

func TestNormalizeQueriesForResearchPromptStripsBoilerplate(t *testing.T) {
	queries := normalizeQueriesForResearchPrompt([]string{
		`# AGENTS.md instructions for /tmp/demo

<INSTRUCTIONS>
Only follow safe steps.
</INSTRUCTIONS>

# Context from my IDE setup:

## Open tabs:
- foo.go

## My request for Codex:
Inspect the analytics route and summarize the current control flow.`,
		`<environment_context>
  <cwd>/tmp/demo</cwd>
</environment_context>

List the exact tests to run after the patch.`,
	})

	require.Equal(t, []string{
		"Inspect the analytics route and summarize the current control flow.",
		"List the exact tests to run after the patch.",
	}, queries)
}

func TestSampleRawQueriesRespectsLimit(t *testing.T) {
	rng := deterministicRand()
	queries := []string{"q1", "q2", "q3", "q4", "q5", "q6", "q7", "q8", "q9", "q10", "q11", "q12"}

	sampled := sampleRawQueries(queries, 10, rng)

	require.Len(t, sampled, 10)
	seen := map[string]struct{}{}
	for _, item := range sampled {
		seen[item] = struct{}{}
	}
	require.Len(t, seen, 10)
}

func deterministicRand() *rand.Rand {
	return rand.New(rand.NewSource(1))
}
