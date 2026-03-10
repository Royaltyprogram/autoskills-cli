package service

import (
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Royaltyprogram/aiops/configs"
)

func TestCloudResearchAgentAnalyzeProjectUsesOpenAIResponses(t *testing.T) {
	type responsesRequest struct {
		Model string `json:"model"`
		Input string `json:"input"`
		Text  struct {
			Format struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"format"`
		} `json:"text"`
	}

	var seen responsesRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/responses", r.URL.Path)
		require.Equal(t, "Bearer test-openai-key", r.Header.Get("Authorization"))

		require.NoError(t, json.NewDecoder(r.Body).Decode(&seen))
		require.Equal(t, "gpt-5.4", seen.Model)
		require.Equal(t, "json_schema", seen.Text.Format.Type)
		require.Equal(t, openAIInstructionSchemaName, seen.Text.Format.Name)
		require.Equal(t, 10, strings.Count(seen.Input, "sample_query_"))

		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{
  "output": [
    {
      "type": "message",
      "content": [
        {
          "type": "output_text",
          "text": "{\"instruction_markdown\":\"- Keep patches minimal and explicit.\\n- List the exact verification steps after each edit.\\n- Compare nearby implementations before changing shared contracts.\\n- State the likely root cause before proposing a fix.\"}"
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
	require.Contains(t, recs[0].Steps[0].ContentPreview, "## AgentOpt Personal Instruction Pack")
	require.Contains(t, recs[0].Steps[0].ContentPreview, "- Keep patches minimal and explicit.")
	require.Contains(t, recs[0].Steps[0].ContentPreview, "- State the likely root cause before proposing a fix.")
}

func TestBuildInstructionPromptLoadsMarkdownTemplate(t *testing.T) {
	prompt, err := buildInstructionPrompt(&Project{Name: "demo-workspace"}, []string{
		"Inspect the analytics route.",
		"List the exact verification steps.",
	})

	require.NoError(t, err)
	require.Contains(t, prompt, "writes reusable `AGENTS.md` instructions")
	require.Contains(t, prompt, "## Requirements")
	require.Contains(t, prompt, "## Project")
	require.Contains(t, prompt, "demo-workspace")
	require.Contains(t, prompt, "## Sampled Raw Queries (2)")
	require.Contains(t, prompt, "sample_query_1: Inspect the analytics route.")
	require.Contains(t, prompt, "sample_query_2: List the exact verification steps.")
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
