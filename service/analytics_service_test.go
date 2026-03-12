package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Royaltyprogram/aiops/configs"
	"github.com/Royaltyprogram/aiops/dto/request"
	"github.com/Royaltyprogram/aiops/dto/response"
)

func findRecommendationByKind(items []response.RecommendationResp, kind string) *response.RecommendationResp {
	for i := range items {
		if items[i].Kind == kind {
			return &items[i]
		}
	}
	return nil
}

func isBenignTestConnError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset by peer")
}

func waitForResearchStatus(
	t *testing.T,
	svc *AnalyticsService,
	ctx context.Context,
	orgID string,
	matcher func(*response.RecommendationResearchStatusResp) bool,
) *response.RecommendationResearchStatusResp {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		overview, err := svc.DashboardOverview(ctx, &request.DashboardOverviewReq{OrgID: orgID})
		require.NoError(t, err)
		if matcher(overview.ResearchStatus) {
			return overview.ResearchStatus
		}
		time.Sleep(20 * time.Millisecond)
	}

	overview, err := svc.DashboardOverview(ctx, &request.DashboardOverviewReq{OrgID: orgID})
	require.NoError(t, err)
	require.True(t, matcher(overview.ResearchStatus), "unexpected research status: %#v", overview.ResearchStatus)
	return overview.ResearchStatus
}

func waitForRecommendations(
	t *testing.T,
	svc *AnalyticsService,
	ctx context.Context,
	req *request.RecommendationListReq,
	matcher func([]response.RecommendationResp) bool,
) []response.RecommendationResp {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := svc.ListRecommendations(ctx, req)
		require.NoError(t, err)
		if matcher(resp.Items) {
			return resp.Items
		}
		time.Sleep(20 * time.Millisecond)
	}

	resp, err := svc.ListRecommendations(ctx, req)
	require.NoError(t, err)
	require.True(t, matcher(resp.Items), "unexpected recommendations: %#v", resp.Items)
	return resp.Items
}

func newResearchStubConfig(t *testing.T, responseBody string) *configs.Config {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/responses", r.URL.Path)
		require.Equal(t, "Bearer test-openai-key", r.Header.Get("Authorization"))
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			if isBenignTestConnError(err) {
				return
			}
			require.NoError(t, err)
		}
		input, _ := payload["input"].(string)
		w.Header().Set("Content-Type", "application/json")
		body := responseBody
		if strings.Contains(input, "Before Rollout Queries") {
			body = defaultEvaluationStubResponse(input)
		}
		_, err := w.Write([]byte(body))
		if !isBenignTestConnError(err) {
			require.NoError(t, err)
		}
	}))
	t.Cleanup(server.Close)

	return &configs.Config{
		OpenAI: configs.OpenAI{
			APIKey:         "test-openai-key",
			BaseURL:        server.URL + "/v1",
			ResponsesModel: "gpt-5.4",
		},
	}
}

func defaultResearchStubResponse() string {
	return `{
  "output": [
    {
      "type": "message",
      "content": [
        {
          "type": "output_text",
          "text": "{\"recommendations\":[{\"kind\":\"llm-workflow-review\",\"title\":\"Reduce repeated workflow recap before implementation\",\"summary\":\"The uploaded raw queries show the user spending too many turns on control-flow recap and verification setup before the actual patch work starts.\",\"reason\":\"Recent raw queries repeatedly ask for current behavior summaries, exact checks, and narrow patch scope before implementation begins.\",\"explanation\":\"A small instruction update can push the coding agent to locate the concrete files first, summarize only the relevant control flow, and propose a verification plan by default.\",\"expected_benefit\":\"Less repeated steering and faster transition from orientation to implementation.\",\"risk\":\"Low. Reviewable instruction update only.\",\"expected_impact\":\"Fewer exploratory turns and clearer first useful responses.\",\"score\":0.82,\"evidence\":[\"repeated control-flow recap\",\"repeated verification prompts\"],\"change_plan\":[{\"type\":\"text_append\",\"action\":\"append_block\",\"target_file\":\"~/.codex/AGENTS.md\",\"summary\":\"Add a reusable workflow-defaults block for Codex.\",\"content_preview\":\"## Workflow Defaults\\n- Locate the concrete files first.\\n- Summarize only the relevant control flow.\\n- List targeted verification before proposing the patch.\\n\"}]}]}"
        }
      ]
    }
  ]
}`
}

func defaultEvaluationStubResponse(input string) string {
	lower := strings.ToLower(input)
	decision := "observe"
	confidence := "low"
	summary := "The qualitative review does not show a strong enough workflow change yet, so the rollout should keep being observed."
	if strings.Contains(lower, "made the workflow slower") || strings.Contains(lower, "larger tool trace after the rollout") {
		decision = "rollback"
		confidence = "high"
		summary = "After the rollout the raw queries show worse workflow quality, including slowdown and rollback-oriented prompts."
	}
	return `{
  "output": [
    {
      "type": "message",
      "content": [
        {
          "type": "output_text",
          "text": "{\"decision\":\"` + decision + `\",\"confidence\":\"` + confidence + `\",\"summary\":\"` + summary + `\"}"
        }
      ]
    }
  ]
}`
}

func TestAnalyticsServiceLifecycleAndOrdering(t *testing.T) {
	ctx := context.Background()
	conf := newResearchStubConfig(t, defaultResearchStubResponse())
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	svc := NewAnalyticsService(Options{
		Config:                    conf,
		AnalyticsStore:            store,
		RecommendationMinSessions: 1,
	})

	agentResp, err := svc.RegisterAgent(ctx, &request.RegisterAgentReq{
		OrgID:      "org-1",
		OrgName:    "Org 1",
		UserID:     "user-1",
		DeviceName: "macbook",
	})
	require.NoError(t, err)

	_, err = svc.RegisterProject(ctx, &request.RegisterProjectReq{
		OrgID:       "org-1",
		AgentID:     agentResp.AgentID,
		ProjectID:   "project-z",
		Name:        "zeta",
		RepoHash:    "zeta-hash",
		DefaultTool: "codex",
	})
	require.NoError(t, err)

	_, err = svc.RegisterProject(ctx, &request.RegisterProjectReq{
		OrgID:       "org-1",
		AgentID:     agentResp.AgentID,
		ProjectID:   "project-a",
		Name:        "alpha",
		RepoHash:    "alpha-hash",
		DefaultTool: "codex",
	})
	require.NoError(t, err)

	now := time.Now().UTC()
	_, err = svc.UploadSessionSummary(ctx, &request.SessionSummaryReq{
		ProjectID:             "project-z",
		SessionID:             "session-before",
		Tool:                  "codex",
		TokenIn:               1000,
		TokenOut:              400,
		CachedInputTokens:     320,
		ReasoningOutputTokens: 90,
		FunctionCallCount:     3,
		ToolErrorCount:        1,
		SessionDurationMS:     96000,
		ToolWallTimeMS:        1500,
		ToolCalls:             map[string]int{"shell": 2, "read_file": 1},
		ToolErrors:            map[string]int{"shell": 1},
		ToolWallTimesMS:       map[string]int{"shell": 1200, "read_file": 300},
		RawQueries: []string{
			"Inspect the route handler and summarize the current control flow.",
			"Find the smallest patch that fixes the failing analytics request path.",
			"List the exact tests to run after the patch.",
		},
		Models:                 []string{"gpt-5.4"},
		ModelProvider:          "openai",
		FirstResponseLatencyMS: 2400,
		AssistantResponses: []string{
			"The analytics route wires session upload, recommendation refresh, and dashboard summaries together.",
		},
		Timestamp: now.Add(-2 * time.Hour),
	})
	require.NoError(t, err)

	_, err = svc.UploadConfigSnapshot(ctx, &request.ConfigSnapshotReq{
		ProjectID:  "project-z",
		Tool:       "codex",
		ProfileID:  "baseline",
		Settings:   map[string]any{"instructions_pack": "baseline"},
		CapturedAt: now.Add(-90 * time.Minute),
	})
	require.NoError(t, err)

	projects, err := svc.ListProjects(ctx, &request.ProjectListReq{OrgID: "org-1"})
	require.NoError(t, err)
	require.Len(t, projects.Items, 1)
	require.Equal(t, "project-z", projects.Items[0].ID)
	require.Equal(t, "Shared workspace", projects.Items[0].Name)

	recommendationItems := waitForRecommendations(t, svc, ctx, &request.RecommendationListReq{ProjectID: "project-z"}, func(items []response.RecommendationResp) bool {
		return len(items) == 1
	})
	require.Equal(t, "llm-workflow-review", recommendationItems[0].Kind)
	require.Len(t, recommendationItems[0].ChangePlan, 1)
	require.Equal(t, defaultCodexInstructionTarget, recommendationItems[0].ChangePlan[0].TargetFile)
	require.Contains(t, recommendationItems[0].ChangePlan[0].ContentPreview, "## Workflow Defaults")
	require.Contains(t, recommendationItems[0].RawSuggestion, "\"kind\": \"llm-workflow-review\"")

	planOld, err := svc.CreateApplyPlan(ctx, &request.ApplyRecommendationReq{
		RecommendationID: recommendationItems[0].ID,
		RequestedBy:      "user-1",
		Scope:            "project",
	})
	require.NoError(t, err)
	require.Len(t, planOld.PatchPreview, len(recommendationItems[0].ChangePlan))
	require.Equal(t, "requires_review", planOld.PolicyMode)
	_, err = svc.ReviewChangePlan(ctx, &request.ReviewChangePlanReq{
		ApplyID:    planOld.ApplyID,
		Decision:   "approve",
		ReviewedBy: "reviewer-1",
	})
	require.NoError(t, err)

	oldAppliedAt := now.Add(-30 * time.Minute)
	oldResolvedAt := now.Add(-20 * time.Minute)
	store.mu.Lock()
	store.applyOperations[planOld.ApplyID].AppliedAt = &oldAppliedAt
	store.applyOperations[planOld.ApplyID].Status = "applied"
	store.experiments[planOld.ExperimentID].AppliedAt = &oldAppliedAt
	store.experiments[planOld.ExperimentID].Status = "completed"
	store.experiments[planOld.ExperimentID].Decision = "keep"
	store.experiments[planOld.ExperimentID].DecisionReason = "completed for ordering test"
	store.experiments[planOld.ExperimentID].ResolvedAt = &oldResolvedAt
	store.mu.Unlock()

	time.Sleep(10 * time.Millisecond)

	planNew, err := svc.CreateApplyPlan(ctx, &request.ApplyRecommendationReq{
		RecommendationID: recommendationItems[0].ID,
		RequestedBy:      "user-1",
		Scope:            "user",
	})
	require.NoError(t, err)
	_, err = svc.ReviewChangePlan(ctx, &request.ReviewChangePlanReq{
		ApplyID:    planNew.ApplyID,
		Decision:   "approve",
		ReviewedBy: "reviewer-1",
	})
	require.NoError(t, err)

	pending, err := svc.PendingApplies(ctx, &request.PendingApplyReq{ProjectID: "project-z", UserID: "user-1"})
	require.NoError(t, err)
	require.Len(t, pending.Items, 1)
	require.Equal(t, planNew.ApplyID, pending.Items[0].ApplyID)

	newAppliedAt := now.Add(-10 * time.Minute)
	newResolvedAt := now.Add(-6 * time.Minute)
	store.mu.Lock()
	store.applyOperations[planNew.ApplyID].AppliedAt = &newAppliedAt
	store.applyOperations[planNew.ApplyID].Status = "applied"
	store.experiments[planNew.ExperimentID].AppliedAt = &newAppliedAt
	store.experiments[planNew.ExperimentID].Status = "completed"
	store.experiments[planNew.ExperimentID].Decision = "keep"
	store.experiments[planNew.ExperimentID].DecisionReason = "completed for ordering test"
	store.experiments[planNew.ExperimentID].ResolvedAt = &newResolvedAt
	store.mu.Unlock()

	projectScopedPlan, err := svc.CreateApplyPlan(ctx, &request.ApplyRecommendationReq{
		RecommendationID: recommendationItems[0].ID,
		RequestedBy:      "another-user",
		Scope:            "project",
	})
	require.NoError(t, err)
	_, err = svc.ReviewChangePlan(ctx, &request.ReviewChangePlanReq{
		ApplyID:    projectScopedPlan.ApplyID,
		Decision:   "approve",
		ReviewedBy: "reviewer-2",
	})
	require.NoError(t, err)

	projectVisible, err := svc.PendingApplies(ctx, &request.PendingApplyReq{ProjectID: "project-z", UserID: "user-1"})
	require.NoError(t, err)
	require.Len(t, projectVisible.Items, 1)
	require.Equal(t, projectScopedPlan.ApplyID, projectVisible.Items[0].ApplyID)

	_, err = svc.UploadSessionSummary(ctx, &request.SessionSummaryReq{
		ProjectID:             "project-z",
		SessionID:             "session-after",
		Tool:                  "codex",
		TokenIn:               800,
		TokenOut:              300,
		CachedInputTokens:     100,
		ReasoningOutputTokens: 20,
		FunctionCallCount:     1,
		SessionDurationMS:     45000,
		ToolWallTimeMS:        150,
		ToolCalls:             map[string]int{"shell": 1},
		ToolErrors:            map[string]int{},
		ToolWallTimesMS:       map[string]int{"shell": 150},
		RawQueries: []string{
			"Compare the analytics and health controllers before changing the shared response contract.",
			"Keep the patch minimal and list targeted verification steps.",
		},
		Models:                 []string{"gpt-5.4"},
		ModelProvider:          "openai",
		FirstResponseLatencyMS: 1200,
		Timestamp:              now.Add(-5 * time.Minute),
	})
	require.NoError(t, err)

	history, err := svc.ApplyHistory(ctx, &request.ApplyHistoryReq{ProjectID: "project-z"})
	require.NoError(t, err)
	require.Len(t, history.Items, 3)
	require.Equal(t, projectScopedPlan.ApplyID, history.Items[0].ApplyID)
	require.Equal(t, planNew.ApplyID, history.Items[1].ApplyID)
	require.Equal(t, planOld.ApplyID, history.Items[2].ApplyID)

	snapshots, err := svc.ListConfigSnapshots(ctx, &request.ConfigSnapshotListReq{ProjectID: "project-z"})
	require.NoError(t, err)
	require.Len(t, snapshots.Items, 1)
	require.Equal(t, "baseline", snapshots.Items[0].ProfileID)

	sessions, err := svc.ListSessionSummaries(ctx, &request.SessionSummaryListReq{ProjectID: "project-z", Limit: 1})
	require.NoError(t, err)
	require.Len(t, sessions.Items, 1)
	require.Equal(t, "session-after", sessions.Items[0].ID)
	require.NotEmpty(t, sessions.Items[0].RawQueries)
	require.Equal(t, "openai", sessions.Items[0].ModelProvider)
	require.Equal(t, 1200, sessions.Items[0].FirstResponseLatencyMS)
	require.Equal(t, 100, sessions.Items[0].CachedInputTokens)
	require.Equal(t, 20, sessions.Items[0].ReasoningOutputTokens)
	require.Equal(t, 1, sessions.Items[0].FunctionCallCount)
	require.Zero(t, sessions.Items[0].ToolErrorCount)
	require.Equal(t, 45000, sessions.Items[0].SessionDurationMS)
	require.Equal(t, 150, sessions.Items[0].ToolWallTimeMS)
	require.Equal(t, map[string]int{"shell": 1}, sessions.Items[0].ToolCalls)
	require.Equal(t, map[string]int{}, sessions.Items[0].ToolErrors)
	require.Equal(t, map[string]int{"shell": 150}, sessions.Items[0].ToolWallTimesMS)

	impact, err := svc.ImpactSummary(ctx, &request.ImpactSummaryReq{ProjectID: "project-z"})
	require.NoError(t, err)
	require.Len(t, impact.Items, 2)
	require.Equal(t, planNew.ApplyID, impact.Items[0].ApplyID)
	require.Greater(t, impact.Items[0].SessionsAfter, 0)
	require.Equal(t, 333.33, impact.Items[0].AvgInputTokensPerQueryBefore)
	require.Equal(t, 400.0, impact.Items[0].AvgInputTokensPerQueryAfter)
	require.Equal(t, 133.33, impact.Items[0].AvgOutputTokensPerQueryBefore)
	require.Equal(t, 150.0, impact.Items[0].AvgOutputTokensPerQueryAfter)
	require.Equal(t, 66.67, impact.Items[0].InputTokensPerQueryDelta)
	require.Equal(t, 16.67, impact.Items[0].OutputTokensPerQueryDelta)

	overview, err := svc.DashboardOverview(ctx, &request.DashboardOverviewReq{OrgID: "org-1"})
	require.NoError(t, err)
	require.Greater(t, overview.AvgTokensPerQuery, 0.0)
	require.Equal(t, 1800, overview.TotalInputTokens)
	require.Equal(t, 700, overview.TotalOutputTokens)
	require.Equal(t, 360.0, overview.AvgInputTokensPerQuery)
	require.Equal(t, 140.0, overview.AvgOutputTokensPerQuery)
	require.Greater(t, overview.TotalTokens, 0)
	require.Equal(t, 2, overview.SuccessfulRolloutCount)
	require.NotEmpty(t, overview.ActionSummary)
	require.NotEmpty(t, overview.OutcomeSummary)

	insights, err := svc.DashboardProjectInsights(ctx, &request.DashboardProjectInsightsReq{ProjectID: "project-z"})
	require.NoError(t, err)
	require.NotEmpty(t, insights.Days)
	require.Equal(t, 2, insights.KnownModelSessions)
	require.Equal(t, 2, insights.KnownProviderSessions)
	require.Equal(t, 2, insights.KnownLatencySessions)
	require.Equal(t, 2, insights.KnownDurationSessions)
	require.Equal(t, 1800, insights.AvgFirstResponseLatencyMS)
	require.Equal(t, 70500, insights.AvgSessionDurationMS)
	require.Equal(t, 420, insights.TotalCachedInputTokens)
	require.Equal(t, 110, insights.TotalReasoningOutputTokens)
	require.Equal(t, 4, insights.TotalFunctionCalls)
	require.Equal(t, 1, insights.TotalToolErrors)
	require.Equal(t, 1650, insights.TotalToolWallTimeMS)
	require.Equal(t, 413, insights.AvgToolWallTimeMS)
	require.Equal(t, 2, insights.SessionsWithFunctionCalls)
	require.Equal(t, 1, insights.SessionsWithToolErrors)
	require.NotEmpty(t, insights.Tools)
	require.Equal(t, "shell", insights.Tools[0].Tool)
	require.Equal(t, 3, insights.Tools[0].CallCount)
	require.Equal(t, 1, insights.Tools[0].ErrorCount)
	require.Equal(t, 0.33, insights.Tools[0].ErrorRate)
	require.Equal(t, 1350, insights.Tools[0].WallTimeMS)
	require.Equal(t, 450, insights.Tools[0].AvgWallTimeMS)
	require.Equal(t, 2, insights.Tools[0].SessionCount)
	sumDayCachedInput := 0
	sumDayReasoning := 0
	sumDayCalls := 0
	sumDayErrors := 0
	sumDayToolWallTime := 0
	sumDayDurationSessions := 0
	for _, day := range insights.Days {
		sumDayCachedInput += day.CachedInputTokens
		sumDayReasoning += day.ReasoningOutputTokens
		sumDayCalls += day.FunctionCallCount
		sumDayErrors += day.ToolErrorCount
		sumDayToolWallTime += day.ToolWallTimeMS
		sumDayDurationSessions += day.DurationSessionCount
	}
	require.Equal(t, insights.TotalCachedInputTokens, sumDayCachedInput)
	require.Equal(t, insights.TotalReasoningOutputTokens, sumDayReasoning)
	require.Equal(t, insights.TotalFunctionCalls, sumDayCalls)
	require.Equal(t, insights.TotalToolErrors, sumDayErrors)
	require.Equal(t, insights.TotalToolWallTimeMS, sumDayToolWallTime)
	require.Equal(t, insights.KnownDurationSessions, sumDayDurationSessions)
	require.NotEmpty(t, insights.Providers)
	require.Equal(t, "openai", insights.Providers[0].Provider)

	audits, err := svc.AuditList(ctx, &request.AuditListReq{OrgID: "org-1", ProjectID: "project-z"})
	require.NoError(t, err)
	require.NotEmpty(t, audits.Items)
	require.Equal(t, "session.ingested", audits.Items[0].Type)
}

func TestAnalyticsServiceWaitsForMinimumSessionsBeforeGeneratingRecommendations(t *testing.T) {
	ctx := context.Background()
	conf := newResearchStubConfig(t, defaultResearchStubResponse())
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	svc := NewAnalyticsService(Options{
		Config:                    conf,
		AnalyticsStore:            store,
		RecommendationMinSessions: 3,
	})

	agentResp, err := svc.RegisterAgent(ctx, &request.RegisterAgentReq{
		OrgID:      "org-threshold",
		UserID:     "user-threshold",
		DeviceName: "threshold-device",
	})
	require.NoError(t, err)

	projectResp, err := svc.RegisterProject(ctx, &request.RegisterProjectReq{
		OrgID:       "org-threshold",
		AgentID:     agentResp.AgentID,
		ProjectID:   "project-threshold",
		Name:        "threshold-workspace",
		RepoHash:    "threshold-hash",
		DefaultTool: "codex",
	})
	require.NoError(t, err)

	for i := 1; i <= 2; i++ {
		ingestResp, err := svc.UploadSessionSummary(ctx, &request.SessionSummaryReq{
			ProjectID: projectResp.ProjectID,
			SessionID: fmt.Sprintf("threshold-session-%d", i),
			Tool:      "codex",
			RawQueries: []string{
				"Inspect the current flow before editing.",
				"List the exact verification steps after the patch.",
			},
			AssistantResponses: []string{
				"I will inspect the flow first and then propose a change.",
			},
			Timestamp: time.Now().UTC().Add(time.Duration(i) * time.Minute),
		})
		require.NoError(t, err)
		require.Zero(t, ingestResp.RecommendationCount)
		require.Empty(t, ingestResp.LatestRecommendationIDs)
		require.NotNil(t, ingestResp.ResearchStatus)
		require.Equal(t, "waiting_for_min_sessions", ingestResp.ResearchStatus.State)
	}

	ingestResp, err := svc.UploadSessionSummary(ctx, &request.SessionSummaryReq{
		ProjectID: projectResp.ProjectID,
		SessionID: "threshold-session-3",
		Tool:      "codex",
		RawQueries: []string{
			"Summarize the current flow before proposing changes.",
			"Reduce repeated steering around verification planning.",
		},
		AssistantResponses: []string{
			"I will inspect the current path and then suggest a minimal workflow change.",
		},
		Timestamp: time.Now().UTC().Add(3 * time.Minute),
	})
	require.NoError(t, err)
	require.Zero(t, ingestResp.RecommendationCount)
	require.Empty(t, ingestResp.LatestRecommendationIDs)
	require.NotNil(t, ingestResp.ResearchStatus)
	require.Equal(t, "running", ingestResp.ResearchStatus.State)

	status := waitForResearchStatus(t, svc, ctx, "org-threshold", func(item *response.RecommendationResearchStatusResp) bool {
		return item != nil && item.State == "succeeded"
	})
	require.Equal(t, 1, status.RecommendationCount)
	require.NotNil(t, status.CompletedAt)

	recommendations, err := svc.ListRecommendations(ctx, &request.RecommendationListReq{ProjectID: projectResp.ProjectID})
	require.NoError(t, err)
	require.Len(t, recommendations.Items, 1)
}

func TestRegisterProjectReusesExistingProjectAndPreservesSignals(t *testing.T) {
	ctx := context.Background()
	conf := newResearchStubConfig(t, defaultResearchStubResponse())
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	svc := NewAnalyticsService(Options{
		Config:                    conf,
		AnalyticsStore:            store,
		RecommendationMinSessions: 1,
	})

	agentResp, err := svc.RegisterAgent(ctx, &request.RegisterAgentReq{
		OrgID:      "org-1",
		UserID:     "user-1",
		DeviceName: "macbook",
	})
	require.NoError(t, err)

	firstConnect, err := svc.RegisterProject(ctx, &request.RegisterProjectReq{
		OrgID:       "org-1",
		AgentID:     agentResp.AgentID,
		ProjectID:   "project-1",
		Name:        "demo-repo",
		RepoHash:    "demo-repo-hash",
		RepoPath:    ".",
		DefaultTool: "codex",
	})
	require.NoError(t, err)
	require.Equal(t, "project-1", firstConnect.ProjectID)

	_, err = svc.UploadConfigSnapshot(ctx, &request.ConfigSnapshotReq{
		ProjectID:  "project-1",
		Tool:       "codex",
		ProfileID:  "baseline",
		Settings:   map[string]any{"instructions_pack": "baseline"},
		CapturedAt: time.Now().UTC().Add(-time.Minute),
	})
	require.NoError(t, err)

	_, err = svc.UploadSessionSummary(ctx, &request.SessionSummaryReq{
		ProjectID: "project-1",
		SessionID: "session-1",
		Tool:      "codex",
		TokenIn:   1200,
		TokenOut:  320,
		RawQueries: []string{
			"Inspect the approval flow and summarize the current behavior.",
		},
		Timestamp: time.Now().UTC(),
	})
	require.NoError(t, err)

	secondConnect, err := svc.RegisterProject(ctx, &request.RegisterProjectReq{
		OrgID:       "org-1",
		AgentID:     agentResp.AgentID,
		Name:        "demo-repo",
		RepoHash:    "demo-repo-hash",
		RepoPath:    "/tmp/demo-repo",
		DefaultTool: "codex",
	})
	require.NoError(t, err)
	require.Equal(t, "project-1", secondConnect.ProjectID)

	projects, err := svc.ListProjects(ctx, &request.ProjectListReq{OrgID: "org-1"})
	require.NoError(t, err)
	require.Len(t, projects.Items, 1)
	require.Equal(t, "project-1", projects.Items[0].ID)
	require.Equal(t, "Shared workspace", projects.Items[0].Name)
	require.Equal(t, "/tmp/demo-repo", projects.Items[0].RepoPath)
	require.Equal(t, "baseline", projects.Items[0].LastProfileID)
	require.NotNil(t, projects.Items[0].LastIngestedAt)
}

func TestAnalyticsServiceAuthWorkflow(t *testing.T) {
	ctx := context.Background()
	conf := newResearchStubConfig(t, defaultResearchStubResponse())
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	svc := NewAnalyticsService(Options{
		Config:                    conf,
		AnalyticsStore:            store,
		RecommendationMinSessions: 1,
	})

	loginResp, err := svc.Login(ctx, &request.LoginReq{
		Email:    "demo@example.com",
		Password: "demo1234",
	})
	require.NoError(t, err)
	require.NotEmpty(t, loginResp.SessionToken)
	require.Equal(t, "demo-user", loginResp.User.ID)

	sessionIdentity, ok := store.ValidateAccessToken(loginResp.SessionToken)
	require.True(t, ok)

	sessionCtx := WithAuthIdentity(ctx, AuthIdentity{
		TokenID:   sessionIdentity.TokenID,
		TokenKind: TokenKindWebSession,
		OrgID:     loginResp.Organization.ID,
		UserID:    loginResp.User.ID,
	})

	sessionResp, err := svc.CurrentSession(sessionCtx)
	require.NoError(t, err)
	require.Equal(t, "demo@example.com", sessionResp.User.Email)

	cliTokenResp, err := svc.IssueCLIToken(sessionCtx, &request.IssueCLITokenReq{
		Label: "Test laptop",
	})
	require.NoError(t, err)
	require.NotEmpty(t, cliTokenResp.Token)

	tokenListResp, err := svc.ListCLITokens(sessionCtx)
	require.NoError(t, err)
	require.Len(t, tokenListResp.Items, 1)
	require.Equal(t, "active", tokenListResp.Items[0].Status)

	identity, ok := store.ValidateAccessToken(cliTokenResp.Token)
	require.True(t, ok)
	require.Equal(t, TokenKindCLI, identity.TokenKind)

	cliCtx := WithAuthIdentity(ctx, *identity)
	cliLoginResp, err := svc.AuthenticateCLI(cliCtx, &request.CLILoginReq{
		DeviceName: "test-laptop",
		Hostname:   "test-laptop.local",
		Platform:   "darwin/arm64",
		Tools:      []string{"codex"},
	})
	require.NoError(t, err)
	require.Equal(t, "registered", cliLoginResp.Status)
	require.Equal(t, "demo-org", cliLoginResp.OrgID)
	require.Equal(t, "demo-user", cliLoginResp.UserID)

	projectResp, err := svc.RegisterProject(cliCtx, &request.RegisterProjectReq{
		OrgID:       cliLoginResp.OrgID,
		AgentID:     cliLoginResp.AgentID,
		Name:        "demo-repo",
		RepoHash:    "demo-repo-hash",
		RepoPath:    "/tmp/demo-repo",
		DefaultTool: "codex",
	})
	require.NoError(t, err)
	require.NotEmpty(t, projectResp.ProjectID)

	_, err = svc.UploadSessionSummary(cliCtx, &request.SessionSummaryReq{
		Tool:     "codex",
		TokenIn:  900,
		TokenOut: 120,
		RawQueries: []string{
			"Inspect the shared workspace flow after login.",
		},
	})
	require.NoError(t, err)

	recommendationItems := waitForRecommendations(t, svc, cliCtx, &request.RecommendationListReq{}, func(items []response.RecommendationResp) bool {
		return len(items) > 0
	})
	require.NotEmpty(t, recommendationItems)

	tokenListResp, err = svc.ListCLITokens(sessionCtx)
	require.NoError(t, err)
	require.Len(t, tokenListResp.Items, 1)
	require.NotNil(t, tokenListResp.Items[0].LastUsedAt)

	revokeResp, err := svc.RevokeCLIToken(sessionCtx, &request.RevokeCLITokenReq{
		TokenID: cliTokenResp.TokenID,
	})
	require.NoError(t, err)
	require.Equal(t, "revoked", revokeResp.Status)

	_, ok = store.ValidateAccessToken(cliTokenResp.Token)
	require.False(t, ok)

	logoutResp, err := svc.Logout(sessionCtx)
	require.NoError(t, err)
	require.Equal(t, "signed_out", logoutResp.Status)
}

func TestAnalyticsServiceProdBootstrapAuthDisablesDefaultDemoUser(t *testing.T) {
	ctx := context.Background()
	conf := &configs.Config{}
	conf.App.Mode = "prod"
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")
	conf.Auth.BootstrapUsers = []configs.BootstrapUser{{
		ID:       "beta-user",
		OrgID:    "beta-org",
		OrgName:  "Beta Org",
		Email:    "beta@example.com",
		Name:     "Beta User",
		Password: "beta-pass",
	}}

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	svc := NewAnalyticsService(Options{
		Config:                    conf,
		AnalyticsStore:            store,
		RecommendationMinSessions: 1,
	})

	_, err = svc.Login(ctx, &request.LoginReq{
		Email:    "demo@example.com",
		Password: "demo1234",
	})
	require.Error(t, err)

	loginResp, err := svc.Login(ctx, &request.LoginReq{
		Email:    "beta@example.com",
		Password: "beta-pass",
	})
	require.NoError(t, err)
	require.Equal(t, "beta-user", loginResp.User.ID)
	require.Equal(t, "beta-org", loginResp.Organization.ID)
}

func TestCreateApplyPlanRequiresReviewForInstructionAppend(t *testing.T) {
	ctx := context.Background()
	conf := newResearchStubConfig(t, defaultResearchStubResponse())
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	svc := NewAnalyticsService(Options{
		Config:                    conf,
		AnalyticsStore:            store,
		RecommendationMinSessions: 1,
	})

	agentResp, err := svc.RegisterAgent(ctx, &request.RegisterAgentReq{
		OrgID:      "org-auto",
		UserID:     "user-auto",
		DeviceName: "macbook",
	})
	require.NoError(t, err)

	_, err = svc.RegisterProject(ctx, &request.RegisterProjectReq{
		OrgID:       "org-auto",
		AgentID:     agentResp.AgentID,
		ProjectID:   "project-auto",
		Name:        "auto",
		RepoHash:    "auto-hash",
		DefaultTool: "codex",
	})
	require.NoError(t, err)

	_, err = svc.UploadSessionSummary(ctx, &request.SessionSummaryReq{
		ProjectID: "project-auto",
		SessionID: "session-auto",
		Tool:      "codex",
		TokenIn:   300,
		TokenOut:  120,
		RawQueries: []string{
			"Inspect the docs command and summarize the current behavior before editing.",
			"Suggest the smallest documentation patch and the exact checks to run.",
		},
		Timestamp: time.Now().UTC(),
	})
	require.NoError(t, err)

	recommendationItems := waitForRecommendations(t, svc, ctx, &request.RecommendationListReq{ProjectID: "project-auto"}, func(items []response.RecommendationResp) bool {
		return len(items) == 1
	})
	require.Equal(t, "llm-workflow-review", recommendationItems[0].Kind)

	plan, err := svc.CreateApplyPlan(ctx, &request.ApplyRecommendationReq{
		RecommendationID: recommendationItems[0].ID,
		RequestedBy:      "user-auto",
		Scope:            "user",
	})
	require.NoError(t, err)
	require.Equal(t, "requires_review", plan.PolicyMode)
	require.Equal(t, "awaiting_review", plan.Status)
	require.Equal(t, "awaiting_review", plan.ApprovalStatus)
	require.Equal(t, "pending", plan.Decision)
	require.Empty(t, plan.ReviewedBy)
}

func TestAnalyzeProjectAddsConfigAndMCPRecommendationsFromSnapshot(t *testing.T) {
	ctx := context.Background()
	conf := newResearchStubConfig(t, `{
  "output": [
    {
      "type": "message",
      "content": [
        {
          "type": "output_text",
          "text": "{\"recommendations\":[{\"kind\":\"config-defaults\",\"title\":\"Load workspace instructions by default\",\"summary\":\"The uploaded sessions suggest the agent is missing reusable workspace defaults.\",\"reason\":\"The raw queries keep asking for discovery and verification framing before edits begin.\",\"explanation\":\"A small config merge can load the instruction files automatically for future sessions.\",\"expected_benefit\":\"Less repeated setup prompting.\",\"risk\":\"Low. Single-file config merge only.\",\"expected_impact\":\"Faster task startup and less manual steering.\",\"score\":0.84,\"evidence\":[\"repeated discovery prompts\"],\"change_plan\":[{\"type\":\"config_merge\",\"action\":\"merge_patch\",\"target_file\":\".codex/config.json\",\"summary\":\"Load workspace instruction files by default.\",\"settings_updates\":{\"instruction_files\":[\"AGENTS.md\",\"~/.codex/AGENTS.md\"]}}]},{\"kind\":\"instruction-review\",\"title\":\"Add workflow defaults to Codex instructions\",\"summary\":\"The uploaded sessions suggest the agent should provide workflow defaults before patching.\",\"reason\":\"The user keeps asking for exact verification steps and narrow repo summaries.\",\"explanation\":\"A reusable instruction block can reduce repeated steering.\",\"expected_benefit\":\"Less repeated prompt steering.\",\"risk\":\"Low. Reviewable instruction append.\",\"expected_impact\":\"Clearer first useful responses.\",\"score\":0.72,\"evidence\":[\"repeated verification prompts\"],\"change_plan\":[{\"type\":\"text_append\",\"action\":\"append_block\",\"target_file\":\"~/.codex/AGENTS.md\",\"summary\":\"Add workflow defaults to Codex instructions.\",\"content_preview\":\"## Workflow Defaults\\n- Locate the concrete files first.\\n- List targeted verification before proposing the patch.\\n\"}]}]}"
        }
      ]
    }
  ]
}`)
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	svc := NewAnalyticsService(Options{
		Config:                    conf,
		AnalyticsStore:            store,
		RecommendationMinSessions: 1,
	})

	agentResp, err := svc.RegisterAgent(ctx, &request.RegisterAgentReq{
		OrgID:      "org-config",
		UserID:     "user-config",
		DeviceName: "macbook",
	})
	require.NoError(t, err)

	_, err = svc.RegisterProject(ctx, &request.RegisterProjectReq{
		OrgID:       "org-config",
		AgentID:     agentResp.AgentID,
		ProjectID:   "project-config",
		Name:        "config",
		RepoHash:    "config-hash",
		DefaultTool: "codex",
	})
	require.NoError(t, err)

	_, err = svc.UploadConfigSnapshot(ctx, &request.ConfigSnapshotReq{
		ProjectID:        "project-config",
		Tool:             "codex",
		ProfileID:        "baseline",
		Settings:         map[string]any{"mcp_servers": []any{"filesystem"}},
		EnabledMCPCount:  1,
		InstructionFiles: []string{"AGENTS.md"},
		CapturedAt:       time.Now().UTC().Add(-time.Hour),
	})
	require.NoError(t, err)

	_, err = svc.UploadSessionSummary(ctx, &request.SessionSummaryReq{
		ProjectID:              "project-config",
		SessionID:              "session-config",
		Tool:                   "codex",
		TokenIn:                1300,
		TokenOut:               320,
		ToolWallTimeMS:         2100,
		FirstResponseLatencyMS: 2200,
		RawQueries: []string{
			"Inspect the current analytics flow before editing it.",
			"Locate the files involved in the approval flow.",
			"Compare this response contract with the health controller.",
			"List the exact tests to run after the patch.",
		},
		Timestamp: time.Now().UTC(),
	})
	require.NoError(t, err)

	recommendationItems := waitForRecommendations(t, svc, ctx, &request.RecommendationListReq{ProjectID: "project-config"}, func(items []response.RecommendationResp) bool {
		return len(items) == 2
	})

	configRecommendation := findRecommendationByKind(recommendationItems, "config-defaults")
	require.NotNil(t, configRecommendation)
	require.Equal(t, ".codex/config.json", configRecommendation.ChangePlan[0].TargetFile)
	require.Equal(t, "merge_patch", configRecommendation.ChangePlan[0].Action)
	require.Equal(t, []any{"AGENTS.md", defaultCodexInstructionTarget}, configRecommendation.ChangePlan[0].SettingsUpdates["instruction_files"])

	instructionRecommendation := findRecommendationByKind(recommendationItems, "instruction-review")
	require.NotNil(t, instructionRecommendation)
	require.Equal(t, defaultCodexInstructionTarget, instructionRecommendation.ChangePlan[0].TargetFile)
	require.Equal(t, "append_block", instructionRecommendation.ChangePlan[0].Action)
	require.Contains(t, instructionRecommendation.ChangePlan[0].ContentPreview, "## Workflow Defaults")

	plan, err := svc.CreateApplyPlan(ctx, &request.ApplyRecommendationReq{
		RecommendationID: configRecommendation.ID,
		RequestedBy:      "user-config",
		Scope:            "user",
	})
	require.NoError(t, err)
	require.Equal(t, "auto_approved", plan.PolicyMode)
	require.Equal(t, "approved_for_local_apply", plan.Status)

	_, err = svc.ReportApplyResult(ctx, &request.ApplyResultReq{
		ApplyID: plan.ApplyID,
		Success: true,
		Note:    "config recommendation applied in test",
	})
	require.NoError(t, err)

	_, err = svc.ReportApplyResult(ctx, &request.ApplyResultReq{
		ApplyID:    plan.ApplyID,
		Success:    true,
		Note:       "config recommendation rolled back in test",
		RolledBack: true,
	})
	require.NoError(t, err)

	instructionPlan, err := svc.CreateApplyPlan(ctx, &request.ApplyRecommendationReq{
		RecommendationID: instructionRecommendation.ID,
		RequestedBy:      "user-config",
		Scope:            "user",
	})
	require.NoError(t, err)
	require.Equal(t, "requires_review", instructionPlan.PolicyMode)
	require.Equal(t, "awaiting_review", instructionPlan.Status)
}

func TestCreateApplyPlanBlocksWhenActiveExperimentExists(t *testing.T) {
	ctx := context.Background()
	conf := newResearchStubConfig(t, defaultResearchStubResponse())
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	svc := NewAnalyticsService(Options{
		Config:                    conf,
		AnalyticsStore:            store,
		RecommendationMinSessions: 1,
	})

	agentResp, err := svc.RegisterAgent(ctx, &request.RegisterAgentReq{
		OrgID:      "org-active",
		UserID:     "user-active",
		DeviceName: "macbook",
	})
	require.NoError(t, err)

	_, err = svc.RegisterProject(ctx, &request.RegisterProjectReq{
		OrgID:       "org-active",
		AgentID:     agentResp.AgentID,
		ProjectID:   "project-active",
		Name:        "active",
		RepoHash:    "active-hash",
		DefaultTool: "codex",
	})
	require.NoError(t, err)

	_, err = svc.UploadSessionSummary(ctx, &request.SessionSummaryReq{
		ProjectID: "project-active",
		SessionID: "session-active",
		Tool:      "codex",
		TokenIn:   700,
		TokenOut:  180,
		RawQueries: []string{
			"Inspect the current apply flow before editing.",
			"List the exact verification steps after the patch.",
		},
		Timestamp: time.Now().UTC().Add(-time.Hour),
	})
	require.NoError(t, err)

	recommendationItems := waitForRecommendations(t, svc, ctx, &request.RecommendationListReq{ProjectID: "project-active"}, func(items []response.RecommendationResp) bool {
		return len(items) == 1
	})

	firstPlan, err := svc.CreateApplyPlan(ctx, &request.ApplyRecommendationReq{
		RecommendationID: recommendationItems[0].ID,
		RequestedBy:      "user-active",
		Scope:            "project",
	})
	require.NoError(t, err)
	require.NotEmpty(t, firstPlan.ExperimentID)

	_, err = svc.CreateApplyPlan(ctx, &request.ApplyRecommendationReq{
		RecommendationID: recommendationItems[0].ID,
		RequestedBy:      "user-active",
		Scope:            "project",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "active experiment")
}

func TestUploadSessionSummaryRequestsRollbackAfterSevereRegression(t *testing.T) {
	ctx := context.Background()
	conf := newResearchStubConfig(t, defaultResearchStubResponse())
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	svc := NewAnalyticsService(Options{
		Config:                    conf,
		AnalyticsStore:            store,
		RecommendationMinSessions: 1,
	})

	agentResp, err := svc.RegisterAgent(ctx, &request.RegisterAgentReq{
		OrgID:      "org-rollback",
		UserID:     "user-rollback",
		DeviceName: "macbook",
	})
	require.NoError(t, err)

	_, err = svc.RegisterProject(ctx, &request.RegisterProjectReq{
		OrgID:       "org-rollback",
		AgentID:     agentResp.AgentID,
		ProjectID:   "project-rollback",
		Name:        "rollback",
		RepoHash:    "rollback-hash",
		DefaultTool: "codex",
	})
	require.NoError(t, err)

	baselineAt := time.Now().UTC().Add(-2 * time.Hour)
	_, err = svc.UploadSessionSummary(ctx, &request.SessionSummaryReq{
		ProjectID:              "project-rollback",
		SessionID:              "session-rollback-before",
		Tool:                   "codex",
		TokenIn:                400,
		TokenOut:               100,
		FirstResponseLatencyMS: 900,
		RawQueries: []string{
			"Inspect the current rollout flow before proposing changes.",
			"List the exact verification steps after the edit.",
		},
		Timestamp: baselineAt,
	})
	require.NoError(t, err)

	recommendationItems := waitForRecommendations(t, svc, ctx, &request.RecommendationListReq{ProjectID: "project-rollback"}, func(items []response.RecommendationResp) bool {
		return len(items) == 1
	})

	plan, err := svc.CreateApplyPlan(ctx, &request.ApplyRecommendationReq{
		RecommendationID: recommendationItems[0].ID,
		RequestedBy:      "user-rollback",
		Scope:            "project",
	})
	require.NoError(t, err)

	_, err = svc.ReviewChangePlan(ctx, &request.ReviewChangePlanReq{
		ApplyID:    plan.ApplyID,
		Decision:   "approve",
		ReviewedBy: "user-rollback",
	})
	require.NoError(t, err)

	_, err = svc.ReportApplyResult(ctx, &request.ApplyResultReq{
		ApplyID:     plan.ApplyID,
		Success:     true,
		Note:        "applied for rollback regression test",
		AppliedFile: "AGENTS.md",
		AppliedText: "AgentOpt guidance",
	})
	require.NoError(t, err)

	postOne := time.Now().UTC().Add(time.Second)
	_, err = svc.UploadSessionSummary(ctx, &request.SessionSummaryReq{
		ProjectID:              "project-rollback",
		SessionID:              "session-rollback-after-1",
		Tool:                   "codex",
		TokenIn:                2400,
		TokenOut:               600,
		FirstResponseLatencyMS: 3200,
		ToolErrorCount:         1,
		RawQueries: []string{
			"Summarize the workflow changes after the rollout.",
			"Suggest the next patch after checking the changed config.",
		},
		Timestamp: postOne,
	})
	require.NoError(t, err)

	experiments, err := svc.ListExperiments(ctx, &request.ExperimentListReq{ProjectID: "project-rollback"})
	require.NoError(t, err)
	require.Len(t, experiments.Items, 1)
	require.Equal(t, "measuring", experiments.Items[0].Status)
	require.Equal(t, 1, experiments.Items[0].PostApplySessions)

	postTwo := postOne.Add(time.Second)
	_, err = svc.UploadSessionSummary(ctx, &request.SessionSummaryReq{
		ProjectID:              "project-rollback",
		SessionID:              "session-rollback-after-2",
		Tool:                   "codex",
		TokenIn:                2600,
		TokenOut:               700,
		FirstResponseLatencyMS: 3600,
		ToolErrorCount:         1,
		RawQueries: []string{
			"Inspect the larger tool trace after the rollout.",
			"Explain why the new settings made the workflow slower.",
		},
		Timestamp: postTwo,
	})
	require.NoError(t, err)

	experiments, err = svc.ListExperiments(ctx, &request.ExperimentListReq{ProjectID: "project-rollback"})
	require.NoError(t, err)
	require.Len(t, experiments.Items, 1)
	require.Equal(t, "rollback_requested", experiments.Items[0].Status)
	require.Equal(t, "rollback", experiments.Items[0].Decision)
	require.Equal(t, 2, experiments.Items[0].PostApplySessions)
	require.Contains(t, experiments.Items[0].DecisionReason, "workflow")

	pending, err := svc.PendingApplies(ctx, &request.PendingApplyReq{ProjectID: "project-rollback", UserID: "user-rollback"})
	require.NoError(t, err)
	require.Len(t, pending.Items, 1)
	require.Equal(t, "rollback", pending.Items[0].Action)
	require.Equal(t, "rollback_requested", pending.Items[0].Status)
	require.Equal(t, plan.ExperimentID, pending.Items[0].ExperimentID)
	require.Contains(t, pending.Items[0].Note, "workflow")

	overview, err := svc.DashboardOverview(ctx, &request.DashboardOverviewReq{OrgID: "org-rollback"})
	require.NoError(t, err)
	require.Equal(t, 1, overview.ActiveExperimentCount)
}

func TestUploadSessionSummaryUsesQualitativeEvaluationAgentForRollback(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/responses", r.URL.Path)

		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			if isBenignTestConnError(err) {
				return
			}
			require.NoError(t, err)
		}
		input, _ := payload["input"].(string)
		w.Header().Set("Content-Type", "application/json")
		if !strings.Contains(input, "Before Rollout Queries") {
			_, err := w.Write([]byte(`{
  "output": [
    {
      "type": "message",
      "content": [
        {
          "type": "output_text",
          "text": "{\"recommendations\":[{\"kind\":\"research-review\",\"title\":\"Reduce repeated approval-flow rediscovery\",\"summary\":\"The uploaded queries still spend too many early turns on approval-flow discovery before editing begins.\",\"reason\":\"The session repeatedly asks for approval-flow summaries and exact checks before implementation work starts.\",\"explanation\":\"A small instruction update can push the agent to front-load repo context and verification planning by default.\",\"expected_benefit\":\"Less repeated setup prompting.\",\"risk\":\"Low. Reviewable instruction append.\",\"expected_impact\":\"Faster first useful responses.\",\"score\":0.79,\"evidence\":[\"approval-flow discovery prompts\"],\"change_plan\":[{\"type\":\"text_append\",\"action\":\"append_block\",\"target_file\":\"~/.codex/AGENTS.md\",\"summary\":\"Add approval-flow workflow defaults.\",\"content_preview\":\"## Workflow Defaults\\n- Locate approval-flow files first.\\n- List targeted verification before patching.\\n\"}]}]}"
        }
      ]
    }
  ]
}`))
			if !isBenignTestConnError(err) {
				require.NoError(t, err)
			}
			return
		}
		require.Contains(t, input, "After Rollout Queries")
		require.Contains(t, input, "Inspect the approval flow before editing.")
		require.Contains(t, input, "Explain why the rollout now needs more repo rediscovery.")

		_, err := w.Write([]byte(`{
  "output": [
    {
      "type": "message",
      "content": [
        {
          "type": "output_text",
          "text": "{\"decision\":\"rollback\",\"confidence\":\"high\",\"summary\":\"After the rollout the user repeatedly asks for repo rediscovery and slowdown diagnosis, which indicates worse workflow quality despite stable metrics.\"}"
        }
      ]
    }
  ]
}`))
		if !isBenignTestConnError(err) {
			require.NoError(t, err)
		}
	}))
	defer server.Close()

	conf := &configs.Config{
		App: configs.App{
			StorePath: filepath.Join(t.TempDir(), "agentopt-store.json"),
		},
		OpenAI: configs.OpenAI{
			APIKey:         "test-openai-key",
			BaseURL:        server.URL + "/v1",
			ResponsesModel: "gpt-5.4",
		},
	}

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	svc := NewAnalyticsService(Options{
		Config:                    conf,
		AnalyticsStore:            store,
		RecommendationMinSessions: 1,
	})

	agentResp, err := svc.RegisterAgent(ctx, &request.RegisterAgentReq{
		OrgID:      "org-qual",
		UserID:     "user-qual",
		DeviceName: "macbook",
	})
	require.NoError(t, err)

	_, err = svc.RegisterProject(ctx, &request.RegisterProjectReq{
		OrgID:       "org-qual",
		AgentID:     agentResp.AgentID,
		ProjectID:   "project-qual",
		Name:        "qual",
		RepoHash:    "qual-hash",
		DefaultTool: "codex",
	})
	require.NoError(t, err)

	baselineAt := time.Now().UTC().Add(-2 * time.Hour)
	_, err = svc.UploadSessionSummary(ctx, &request.SessionSummaryReq{
		ProjectID:              "project-qual",
		SessionID:              "session-qual-before",
		Tool:                   "codex",
		TokenIn:                1000,
		TokenOut:               250,
		FirstResponseLatencyMS: 1200,
		RawQueries: []string{
			"Inspect the approval flow before editing.",
			"List the exact verification steps after the patch.",
		},
		Timestamp: baselineAt,
	})
	require.NoError(t, err)

	recommendationItems := waitForRecommendations(t, svc, ctx, &request.RecommendationListReq{ProjectID: "project-qual"}, func(items []response.RecommendationResp) bool {
		return len(items) > 0
	})

	plan, err := svc.CreateApplyPlan(ctx, &request.ApplyRecommendationReq{
		RecommendationID: recommendationItems[0].ID,
		RequestedBy:      "user-qual",
		Scope:            "project",
	})
	require.NoError(t, err)

	_, err = svc.ReviewChangePlan(ctx, &request.ReviewChangePlanReq{
		ApplyID:    plan.ApplyID,
		Decision:   "approve",
		ReviewedBy: "user-qual",
	})
	require.NoError(t, err)

	_, err = svc.ReportApplyResult(ctx, &request.ApplyResultReq{
		ApplyID:     plan.ApplyID,
		Success:     true,
		Note:        "applied for qualitative review test",
		AppliedFile: "AGENTS.md",
		AppliedText: "AgentOpt guidance",
	})
	require.NoError(t, err)

	postOne := time.Now().UTC().Add(time.Second)
	_, err = svc.UploadSessionSummary(ctx, &request.SessionSummaryReq{
		ProjectID:              "project-qual",
		SessionID:              "session-qual-after-1",
		Tool:                   "codex",
		TokenIn:                980,
		TokenOut:               240,
		FirstResponseLatencyMS: 1250,
		RawQueries: []string{
			"Explain why the rollout now needs more repo rediscovery.",
			"Inspect the larger trace after the rollout.",
		},
		Timestamp: postOne,
	})
	require.NoError(t, err)

	postTwo := postOne.Add(time.Second)
	_, err = svc.UploadSessionSummary(ctx, &request.SessionSummaryReq{
		ProjectID:              "project-qual",
		SessionID:              "session-qual-after-2",
		Tool:                   "codex",
		TokenIn:                990,
		TokenOut:               245,
		FirstResponseLatencyMS: 1280,
		RawQueries: []string{
			"Summarize why the new settings slowed the workflow down.",
			"Check whether the rollout should be reverted.",
		},
		Timestamp: postTwo,
	})
	require.NoError(t, err)

	experiments, err := svc.ListExperiments(ctx, &request.ExperimentListReq{ProjectID: "project-qual"})
	require.NoError(t, err)
	require.Len(t, experiments.Items, 1)
	require.Equal(t, "rollback_requested", experiments.Items[0].Status)
	require.Equal(t, "rollback", experiments.Items[0].Decision)
	require.Equal(t, "responses-api", experiments.Items[0].EvaluationMode)
	require.Equal(t, "rollback", experiments.Items[0].EvaluationDecision)
	require.Equal(t, "high", experiments.Items[0].EvaluationConfidence)
	require.Contains(t, experiments.Items[0].EvaluationSummary, "worse workflow quality")
	require.Contains(t, experiments.Items[0].DecisionReason, "worse workflow quality")
}

func TestUploadSessionSummaryReplacesExistingSessionByID(t *testing.T) {
	ctx := context.Background()
	conf := newResearchStubConfig(t, defaultResearchStubResponse())
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	svc := NewAnalyticsService(Options{
		Config:                    conf,
		AnalyticsStore:            store,
		RecommendationMinSessions: 1,
	})

	agentResp, err := svc.RegisterAgent(ctx, &request.RegisterAgentReq{
		OrgID:      "org-replace",
		UserID:     "user-replace",
		DeviceName: "macbook",
	})
	require.NoError(t, err)

	_, err = svc.RegisterProject(ctx, &request.RegisterProjectReq{
		OrgID:       "org-replace",
		AgentID:     agentResp.AgentID,
		ProjectID:   "project-replace",
		Name:        "replace",
		RepoHash:    "replace-hash",
		DefaultTool: "codex",
	})
	require.NoError(t, err)

	firstTimestamp := time.Date(2026, 3, 9, 9, 0, 0, 0, time.UTC)
	secondTimestamp := firstTimestamp.Add(5 * time.Minute)

	_, err = svc.UploadSessionSummary(ctx, &request.SessionSummaryReq{
		ProjectID: "project-replace",
		SessionID: "codex-session-1",
		Tool:      "codex",
		TokenIn:   500,
		TokenOut:  120,
		RawQueries: []string{
			"Inspect the analytics route before editing.",
		},
		Models:                 []string{"gpt-5.4"},
		ModelProvider:          "openai",
		FirstResponseLatencyMS: 2100,
		AssistantResponses: []string{
			"I inspected the analytics route before editing.",
		},
		Timestamp: firstTimestamp,
	})
	require.NoError(t, err)

	_, err = svc.UploadSessionSummary(ctx, &request.SessionSummaryReq{
		ProjectID: "project-replace",
		SessionID: "codex-session-1",
		Tool:      "codex",
		TokenIn:   900,
		TokenOut:  240,
		RawQueries: []string{
			"Inspect the analytics route before editing.",
			"List the exact tests to run after the patch.",
		},
		Models:                 []string{"gpt-5.4"},
		ModelProvider:          "openai",
		FirstResponseLatencyMS: 1100,
		AssistantResponses: []string{
			"The analytics route registers auth, ingestion, and dashboard handlers in one place.",
		},
		Timestamp: secondTimestamp,
	})
	require.NoError(t, err)

	sessions, err := svc.ListSessionSummaries(ctx, &request.SessionSummaryListReq{ProjectID: "project-replace"})
	require.NoError(t, err)
	require.Len(t, sessions.Items, 1)
	require.Equal(t, "codex-session-1", sessions.Items[0].ID)
	require.Equal(t, 900, sessions.Items[0].TokenIn)
	require.Equal(t, 240, sessions.Items[0].TokenOut)
	require.Equal(t, []string{
		"Inspect the analytics route before editing.",
		"List the exact tests to run after the patch.",
	}, sessions.Items[0].RawQueries)
	require.Equal(t, []string{"gpt-5.4"}, sessions.Items[0].Models)
	require.Equal(t, "openai", sessions.Items[0].ModelProvider)
	require.Equal(t, 1100, sessions.Items[0].FirstResponseLatencyMS)
	require.Equal(t, []string{"The analytics route registers auth, ingestion, and dashboard handlers in one place."}, sessions.Items[0].AssistantResponses)
	require.Equal(t, secondTimestamp, sessions.Items[0].Timestamp)
}

func TestReportApplyResultTracksApplyAndRollbackLifecycle(t *testing.T) {
	ctx := context.Background()
	conf := newResearchStubConfig(t, defaultResearchStubResponse())
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	svc := NewAnalyticsService(Options{
		Config:                    conf,
		AnalyticsStore:            store,
		RecommendationMinSessions: 1,
	})

	agentResp, err := svc.RegisterAgent(ctx, &request.RegisterAgentReq{
		OrgID:      "org-exec",
		UserID:     "user-exec",
		DeviceName: "macbook",
	})
	require.NoError(t, err)

	_, err = svc.RegisterProject(ctx, &request.RegisterProjectReq{
		OrgID:       "org-exec",
		AgentID:     agentResp.AgentID,
		ProjectID:   "project-exec",
		Name:        "exec",
		RepoHash:    "exec-hash",
		DefaultTool: "codex",
	})
	require.NoError(t, err)

	now := time.Now().UTC()
	_, err = svc.UploadSessionSummary(ctx, &request.SessionSummaryReq{
		ProjectID: "project-exec",
		SessionID: "session-before-exec",
		Tool:      "codex",
		TokenIn:   800,
		TokenOut:  220,
		RawQueries: []string{
			"Inspect the failing execution flow and summarize the current behavior.",
			"State the likely root cause before proposing a patch.",
			"List the exact verification steps after the edit.",
		},
		Timestamp: now.Add(-2 * time.Hour),
	})
	require.NoError(t, err)

	recommendationItems := waitForRecommendations(t, svc, ctx, &request.RecommendationListReq{ProjectID: "project-exec"}, func(items []response.RecommendationResp) bool {
		return len(items) > 0
	})

	plan, err := svc.CreateApplyPlan(ctx, &request.ApplyRecommendationReq{
		RecommendationID: recommendationItems[0].ID,
		RequestedBy:      "user-exec",
		Scope:            "project",
	})
	require.NoError(t, err)
	require.Equal(t, "awaiting_review", plan.Status)
	require.NotEmpty(t, plan.ExperimentID)

	experiments, err := svc.ListExperiments(ctx, &request.ExperimentListReq{ProjectID: "project-exec"})
	require.NoError(t, err)
	require.Len(t, experiments.Items, 1)
	require.Equal(t, plan.ExperimentID, experiments.Items[0].ExperimentID)
	require.Equal(t, "awaiting_review", experiments.Items[0].Status)
	require.Equal(t, 1, experiments.Items[0].BaselineSessions)
	require.Equal(t, 3, experiments.Items[0].BaselineQueries)

	_, err = svc.ReviewChangePlan(ctx, &request.ReviewChangePlanReq{
		ApplyID:    plan.ApplyID,
		Decision:   "approve",
		ReviewedBy: "user-exec",
	})
	require.NoError(t, err)

	experiments, err = svc.ListExperiments(ctx, &request.ExperimentListReq{ProjectID: "project-exec"})
	require.NoError(t, err)
	require.Equal(t, "queued_for_apply", experiments.Items[0].Status)
	require.Equal(t, "approved", experiments.Items[0].Decision)
	require.NotNil(t, experiments.Items[0].ApprovedAt)

	applyResult, err := svc.ReportApplyResult(ctx, &request.ApplyResultReq{
		ApplyID:     plan.ApplyID,
		Success:     true,
		Note:        "applied by lifecycle test",
		AppliedFile: "AGENTS.md",
		AppliedText: "AgentOpt Research Findings",
	})
	require.NoError(t, err)
	require.Equal(t, "applied", applyResult.Status)
	require.False(t, applyResult.RolledBack)

	experiments, err = svc.ListExperiments(ctx, &request.ExperimentListReq{ProjectID: "project-exec"})
	require.NoError(t, err)
	require.Equal(t, "measuring", experiments.Items[0].Status)
	require.Equal(t, "observe", experiments.Items[0].Decision)
	require.NotNil(t, experiments.Items[0].AppliedAt)
	firstAppliedAt := *experiments.Items[0].AppliedAt

	pending, err := svc.PendingApplies(ctx, &request.PendingApplyReq{ProjectID: "project-exec", UserID: "user-exec"})
	require.NoError(t, err)
	require.Empty(t, pending.Items)

	history, err := svc.ApplyHistory(ctx, &request.ApplyHistoryReq{ProjectID: "project-exec"})
	require.NoError(t, err)
	require.Len(t, history.Items, 1)
	require.Equal(t, "applied", history.Items[0].Status)
	require.Equal(t, "AGENTS.md", history.Items[0].AppliedFile)

	_, err = svc.UploadSessionSummary(ctx, &request.SessionSummaryReq{
		ProjectID: "project-exec",
		SessionID: "session-after-exec",
		Tool:      "codex",
		TokenIn:   600,
		TokenOut:  180,
		RawQueries: []string{
			"Keep the patch minimal and compare the shared contract before editing.",
			"Run the targeted verification steps after the change.",
		},
		Timestamp: now.Add(2 * time.Hour),
	})
	require.NoError(t, err)

	impact, err := svc.ImpactSummary(ctx, &request.ImpactSummaryReq{ProjectID: "project-exec"})
	require.NoError(t, err)
	require.Len(t, impact.Items, 1)
	require.Equal(t, plan.ApplyID, impact.Items[0].ApplyID)
	require.Equal(t, plan.ExperimentID, impact.Items[0].ExperimentID)
	require.Greater(t, impact.Items[0].SessionsAfter, 0)
	require.Equal(t, 33.33, impact.Items[0].InputTokensPerQueryDelta)
	require.Equal(t, 16.67, impact.Items[0].OutputTokensPerQueryDelta)

	experiments, err = svc.ListExperiments(ctx, &request.ExperimentListReq{ProjectID: "project-exec"})
	require.NoError(t, err)
	require.Equal(t, 1, experiments.Items[0].PostApplySessions)
	require.Equal(t, 2, experiments.Items[0].PostApplyQueries)

	rollbackResult, err := svc.ReportApplyResult(ctx, &request.ApplyResultReq{
		ApplyID:     plan.ApplyID,
		Success:     true,
		Note:        "rolled back by lifecycle test",
		AppliedFile: "AGENTS.md",
		RolledBack:  true,
	})
	require.NoError(t, err)
	require.Equal(t, "rollback_confirmed", rollbackResult.Status)
	require.True(t, rollbackResult.RolledBack)

	historyAfterRollback, err := svc.ApplyHistory(ctx, &request.ApplyHistoryReq{ProjectID: "project-exec"})
	require.NoError(t, err)
	require.Len(t, historyAfterRollback.Items, 1)
	require.Equal(t, "rollback_confirmed", historyAfterRollback.Items[0].Status)
	require.True(t, historyAfterRollback.Items[0].RolledBack)
	require.Equal(t, plan.ExperimentID, historyAfterRollback.Items[0].ExperimentID)
	require.Equal(t, firstAppliedAt, *historyAfterRollback.Items[0].AppliedAt)
	require.NotNil(t, historyAfterRollback.Items[0].RolledBackAt)

	experiments, err = svc.ListExperiments(ctx, &request.ExperimentListReq{ProjectID: "project-exec"})
	require.NoError(t, err)
	require.Equal(t, "rolled_back", experiments.Items[0].Status)
	require.Equal(t, "rollback", experiments.Items[0].Decision)
	require.NotNil(t, experiments.Items[0].ResolvedAt)
	require.NotNil(t, experiments.Items[0].AppliedAt)
	require.Equal(t, firstAppliedAt, *experiments.Items[0].AppliedAt)

	overview, err := svc.DashboardOverview(ctx, &request.DashboardOverviewReq{OrgID: "org-exec"})
	require.NoError(t, err)
	require.Greater(t, overview.AvgQueriesPerSession, 0.0)
	require.Equal(t, 1400, overview.TotalInputTokens)
	require.Equal(t, 400, overview.TotalOutputTokens)
	require.Equal(t, 280.0, overview.AvgInputTokensPerQuery)
	require.Equal(t, 80.0, overview.AvgOutputTokensPerQuery)
	require.Greater(t, overview.TotalTokens, 0)
	require.Equal(t, 0, overview.SuccessfulRolloutCount)
	require.Equal(t, 0, overview.FailedExecutionCount)
	require.Equal(t, 0, overview.ActiveExperimentCount)
	require.NotEmpty(t, overview.ActionSummary)
	require.NotEmpty(t, overview.OutcomeSummary)
}
