package service

import (
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
		require.Contains(t, seen.Input, "assistant_response_1:")
		require.Contains(t, seen.Input, "reasoning_summary_1:")
		require.Contains(t, seen.Input, `"schema_version": "report-feedback.v1"`)
		require.Contains(t, seen.Input, "\"reports\"")

		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{
 "output": [
    {
      "type": "message",
      "content": [
        {
          "type": "output_text",
          "text": "{\"schema_version\":\"report-feedback.v1\",\"reports\":[{\"kind\":\"repo-orientation-defaults\",\"title\":\"Reduce repeated repo orientation before work starts\",\"summary\":\"Recent sessions spend too many early turns on repo discovery and control-flow recap before the real task begins.\",\"user_intent\":\"The user wants a small, well-scoped fix with explicit verification before any broader changes.\",\"model_interpretation\":\"The model appears to read the request as a need to re-orient on repo structure and control flow before proposing a patch.\",\"reason\":\"The uploaded raw queries repeatedly ask for control-flow summaries, file discovery, and verification planning before any implementation work starts.\",\"explanation\":\"The workflow appears to require too much manual orientation on each task, so the user needs clearer repo-entry habits when starting a new task.\",\"expected_benefit\":\"Less repeated repo discovery and faster first useful responses.\",\"expected_impact\":\"Fewer exploratory turns and less prompt steering at the start of each task.\",\"confidence\":\"high\",\"strengths\":[\"asks for minimal patch scope\"],\"frictions\":[\"repeated control-flow recap\"],\"next_steps\":[\"start each task with the concrete files involved\"],\"score\":0.86,\"evidence\":[\"repeated control-flow recap\",\"repeated verification prompts\"]}]}"
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
	reports, err := agent.AnalyzeProject(&Project{Name: "demo-workspace"}, []*SessionSummary{{
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
			"Verify whether the report should call out this scenario.",
			"Locate the files involved in the approval flow.",
			"Check if there is already a helper for this behavior.",
			"Explain why the regression appears only after sync.",
		},
		AssistantResponses: []string{
			"I will inspect the route flow first, then propose a minimal patch and verification plan.",
			"The approval flow spans the analytics service and dashboard renderer.",
		},
		ReasoningSummaries: []string{
			"Checking repo structure before proposing the minimal patch.",
		},
	}}, nil)
	require.NoError(t, err)

	require.Len(t, reports, 1)
	require.Equal(t, "repo-orientation-defaults", reports[0].Kind)
	require.Equal(t, "Reduce repeated repo orientation before work starts", reports[0].Title)
	require.Contains(t, reports[0].UserIntent, "small, well-scoped fix")
	require.Contains(t, reports[0].ModelInterpretation, "re-orient on repo structure")
	require.Contains(t, reports[0].Evidence, "repeated control-flow recap")
	require.Contains(t, reports[0].RawSuggestion, "\"kind\": \"repo-orientation-defaults\"")
	require.Equal(t, "high", reports[0].Confidence)
	require.Contains(t, reports[0].Frictions, "repeated control-flow recap")
	require.Contains(t, reports[0].NextSteps, "start each task with the concrete files involved")
	require.Contains(t, reports[0].Summary, "repo discovery")
}

func TestCloudResearchAgentAnalyzeProjectRequiresOpenAIForReports(t *testing.T) {
	agent := NewCloudResearchAgent(&configs.Config{})
	reports, err := agent.AnalyzeProject(&Project{Name: "demo-workspace"}, []*SessionSummary{{
		RawQueries: []string{
			"Inspect the analytics flow before editing it.",
			"List the exact tests to run after the patch.",
		},
	}}, nil)
	require.NoError(t, err)

	require.Nil(t, reports)
}

func TestBuildReportsPromptLoadsTemplate(t *testing.T) {
	prompt, err := buildReportsPrompt("", &Project{Name: "demo-workspace"}, []string{
		"Inspect the analytics route.",
		"List the exact verification steps.",
	}, []researchInteractionSample{{
		TimestampLabel: "2026-03-10T08:00:00Z",
		Tool:           "codex",
		Queries: []string{
			"Inspect the analytics route.",
			"List the exact verification steps.",
		},
		AssistantResponses: []string{
			"I will inspect the route flow before proposing changes.",
		},
		ReasoningSummaries: []string{
			"Checking whether the user wants a control-flow recap before patching.",
		},
	}}, researchUsageSummary{
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
	require.Contains(t, prompt, "analyzing a user's real Codex coding-agent sessions")
	require.Contains(t, prompt, "You are not the coding agent.")
	require.Contains(t, prompt, "## Requirements")
	require.Contains(t, prompt, "report-feedback.v1")
	require.Contains(t, prompt, "## Project")
	require.Contains(t, prompt, "demo-workspace")
	require.Contains(t, prompt, "## Usage Summary")
	require.Contains(t, prompt, "- avg_first_response_latency_ms=1800")
	require.Contains(t, prompt, "## Recent Session Metrics")
	require.Contains(t, prompt, "tool=codex")
	require.Contains(t, prompt, "## Query-Response Interaction Evidence")
	require.Contains(t, prompt, "assistant_response_1: I will inspect the route flow before proposing changes.")
	require.Contains(t, prompt, "reasoning_summary_1: Checking whether the user wants a control-flow recap before patching.")
	require.Contains(t, prompt, "## Raw Queries (2)")
	require.Contains(t, prompt, "sample_query_1: Inspect the analytics route.")
	require.Contains(t, prompt, "sample_query_2: List the exact verification steps.")
}

func TestBuildReportsPromptLoadsKoreanTestTemplate(t *testing.T) {
	prompt, err := buildReportsPrompt("ko-test", &Project{Name: "demo-workspace"}, []string{
		"Inspect the analytics route.",
	}, []researchInteractionSample{{
		TimestampLabel: "2026-03-10T08:00:00Z",
		Tool:           "codex",
		Queries: []string{
			"Inspect the analytics route.",
		},
		AssistantResponses: []string{
			"I will inspect the route flow before proposing changes.",
		},
	}}, researchUsageSummary{
		SessionCount:      1,
		RawQueryCount:     1,
		TotalInputTokens:  1200,
		TotalOutputTokens: 280,
		RecentSessions: []researchSessionSnapshot{{
			TimestampLabel: "2026-03-10T08:00:00Z",
			Tool:           "codex",
			QueryCount:     1,
			InputTokens:    1200,
			OutputTokens:   280,
		}},
	})

	require.NoError(t, err)
	require.Contains(t, prompt, "produce clear analysis reports in Korean")
	require.Contains(t, prompt, "Write every user-facing narrative field in Korean")
	require.Contains(t, prompt, "## Language Rules")
	require.Contains(t, prompt, "sample_query_1: Inspect the analytics route.")
}

func TestParseResearchReportsRejectsInvalidEntries(t *testing.T) {
	reports, err := parseResearchReports(`{
  "schema_version": "report-feedback.v1",
  "reports": [
    {
      "kind": "empty-report",
      "title": "Missing summary",
      "summary": ""
    },
    {
      "kind": "valid",
      "title": "Keep verification local",
      "summary": "The user keeps asking for exact checks, so the default workflow should include them.",
      "user_intent": "The user wants the patch path to stay narrow and verifiable.",
      "model_interpretation": "The model seems to read the request as verification-first before implementation.",
      "reason": "Repeated verification prompts appear in raw queries.",
      "explanation": "The user needs a clearer checklist for how to drive verification prompts.",
      "expected_benefit": "Less repeated prompting about checks.",
      "expected_impact": "Faster convergence on the final patch.",
      "confidence": "medium",
      "strengths": ["asks for exact checks"],
      "frictions": ["repeated verification prompts"],
      "next_steps": ["state the target verification first"],
      "score": 1.2,
      "evidence": ["repeated verification prompts"]
    }
  ]
}`)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	require.Equal(t, "valid", reports[0].Kind)
	require.Contains(t, reports[0].UserIntent, "stay narrow and verifiable")
	require.Contains(t, reports[0].ModelInterpretation, "verification-first")
	require.Equal(t, 1.0, reports[0].Score)
	require.Equal(t, "medium", reports[0].Confidence)
	require.Contains(t, reports[0].RawSuggestion, "\"title\": \"Keep verification local\"")
}

func TestParseResearchReportsRejectsUnsupportedSchemaVersion(t *testing.T) {
	_, err := parseResearchReports(`{
  "schema_version": "report-feedback.v999",
  "reports": [
    {
      "kind": "valid",
      "title": "Keep verification local",
      "summary": "The user keeps asking for exact checks."
    }
  ]
}`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported report feedback schema_version")
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

func TestBuildResearchInteractionSamplesFiltersLowSignalReasoningSummaries(t *testing.T) {
	items := buildResearchInteractionSamples([]*SessionSummary{{
		Tool:      "codex",
		Timestamp: mustParseResearchTime(t, "2026-03-10T08:00:00Z"),
		RawQueries: []string{
			"Inspect the analytics route and keep the patch minimal.",
		},
		AssistantResponses: []string{
			"I will inspect the route flow first.",
		},
		ReasoningSummaries: []string{
			"Checking for AGENTS instructions",
			"Need to include a preamble before tool calls",
			"Checking route flow before proposing the minimal patch",
		},
	}}, 5)

	require.Len(t, items, 1)
	require.Equal(t, []string{
		"Checking route flow before proposing the minimal patch",
	}, items[0].ReasoningSummaries)
}

func TestIsLowSignalReasoningSummary(t *testing.T) {
	require.True(t, isLowSignalReasoningSummary("**Checking for AGENTS instructions**"))
	require.True(t, isLowSignalReasoningSummary("Need to include a preamble before tool calls"))
	require.False(t, isLowSignalReasoningSummary("Checking route flow before proposing the minimal patch"))
}

func deterministicRand() *rand.Rand {
	return rand.New(rand.NewSource(1))
}

func mustParseResearchTime(t *testing.T, raw string) time.Time {
	t.Helper()

	ts, err := time.Parse(time.RFC3339, raw)
	require.NoError(t, err)
	return ts.UTC()
}
