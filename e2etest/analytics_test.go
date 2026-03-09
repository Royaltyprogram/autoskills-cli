package e2etest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/liushuangls/go-server-template/dto/request"
	"github.com/liushuangls/go-server-template/dto/response"
)

func (s *APISuite) TestAnalyticsLifecycle_ApplyAndRollback() {
	now := time.Now().UTC()
	suffix := fmt.Sprintf("%d", now.UnixNano())
	orgID := "org-e2e-" + suffix
	userID := "user-e2e-" + suffix
	projectName := "workspace-" + suffix
	projectHash := "hash-" + suffix

	agentResp := postAPIJSON[response.AgentRegistrationResp](s.T(), s, http.MethodPost, "/api/v1/agents/register", request.RegisterAgentReq{
		OrgID:      orgID,
		UserID:     userID,
		DeviceName: "e2e-device",
	})
	require.Equal(s.T(), "registered", agentResp.Status)

	projectResp := postAPIJSON[response.ProjectRegistrationResp](s.T(), s, http.MethodPost, "/api/v1/projects/register", request.RegisterProjectReq{
		OrgID:       orgID,
		AgentID:     agentResp.AgentID,
		Name:        projectName,
		RepoHash:    projectHash,
		DefaultTool: "codex",
	})
	require.Equal(s.T(), "connected", projectResp.Status)

	snapshotResp := postAPIJSON[response.ConfigSnapshotResp](s.T(), s, http.MethodPost, "/api/v1/config-snapshots", request.ConfigSnapshotReq{
		ProjectID:  projectResp.ProjectID,
		Tool:       "codex",
		ProfileID:  "baseline",
		Settings:   map[string]any{"instructions_pack": "baseline"},
		CapturedAt: now.Add(-90 * time.Minute),
	})
	require.Equal(s.T(), "baseline", snapshotResp.ProfileID)

	sessionResp := postAPIJSON[response.SessionIngestResp](s.T(), s, http.MethodPost, "/api/v1/session-summaries", request.SessionSummaryReq{
		ProjectID: projectResp.ProjectID,
		SessionID: "session-before-" + suffix,
		Tool:      "codex",
		TokenIn:   1000,
		TokenOut:  240,
		RawQueries: []string{
			"Inspect the route handler and summarize the current control flow.",
			"Find the smallest patch that fixes the failing analytics path.",
			"List the exact tests to run after the patch.",
		},
		Timestamp: now.Add(-2 * time.Hour),
	})
	require.NotEmpty(s.T(), sessionResp.LatestRecommendationIDs)

	recommendations := getAPIJSON[response.RecommendationListResp](s.T(), s, "/api/v1/recommendations", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.NotEmpty(s.T(), recommendations.Items)

	applyResp := postAPIJSON[response.ApplyPlanResp](s.T(), s, http.MethodPost, "/api/v1/recommendations/apply", request.ApplyRecommendationReq{
		RecommendationID: recommendations.Items[0].ID,
		RequestedBy:      userID,
		Scope:            "project",
	})
	require.Equal(s.T(), "awaiting_review", applyResp.Status)

	reviewResp := postAPIJSON[response.ChangePlanReviewResp](s.T(), s, http.MethodPost, "/api/v1/change-plans/review", request.ReviewChangePlanReq{
		ApplyID:    applyResp.ApplyID,
		Decision:   "approve",
		ReviewedBy: userID,
	})
	require.Equal(s.T(), "approved_for_local_apply", reviewResp.Status)

	pendingResp := getAPIJSON[response.PendingApplyResp](s.T(), s, "/api/v1/applies/pending", url.Values{
		"project_id": []string{projectResp.ProjectID},
		"user_id":    []string{userID},
	})
	require.Len(s.T(), pendingResp.Items, 1)
	require.Equal(s.T(), applyResp.ApplyID, pendingResp.Items[0].ApplyID)

	applyResult := postAPIJSON[response.ApplyResultResp](s.T(), s, http.MethodPost, "/api/v1/applies/result", request.ApplyResultReq{
		ApplyID:     applyResp.ApplyID,
		Success:     true,
		Note:        "applied by e2e",
		AppliedFile: "AGENTS.md",
		AppliedText: "AgentOpt Personal Instruction Pack",
	})
	require.Equal(s.T(), "applied", applyResult.Status)
	require.False(s.T(), applyResult.RolledBack)

	pendingAfterApply := getAPIJSON[response.PendingApplyResp](s.T(), s, "/api/v1/applies/pending", url.Values{
		"project_id": []string{projectResp.ProjectID},
		"user_id":    []string{userID},
	})
	require.Empty(s.T(), pendingAfterApply.Items)

	historyAfterApply := getAPIJSON[response.ApplyHistoryResp](s.T(), s, "/api/v1/applies", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.NotEmpty(s.T(), historyAfterApply.Items)
	require.Equal(s.T(), "applied", historyAfterApply.Items[0].Status)

	postAPIJSON[response.SessionIngestResp](s.T(), s, http.MethodPost, "/api/v1/session-summaries", request.SessionSummaryReq{
		ProjectID: projectResp.ProjectID,
		SessionID: "session-after-" + suffix,
		Tool:      "codex",
		TokenIn:   700,
		TokenOut:  180,
		RawQueries: []string{
			"Compare the analytics and health controllers before editing the shared response contract.",
			"Keep the patch minimal and list the targeted verification steps.",
		},
		Timestamp: now.Add(2 * time.Hour),
	})

	impactResp := getAPIJSON[response.ImpactSummaryResp](s.T(), s, "/api/v1/impact", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.NotEmpty(s.T(), impactResp.Items)
	require.Equal(s.T(), applyResp.ApplyID, impactResp.Items[0].ApplyID)
	require.Greater(s.T(), impactResp.Items[0].SessionsAfter, 0)

	overviewAfterApply := getAPIJSON[response.DashboardOverviewResp](s.T(), s, "/api/v1/dashboard/overview", url.Values{
		"org_id": []string{orgID},
	})
	require.Greater(s.T(), overviewAfterApply.AvgTokensPerQuery, 0.0)
	require.Greater(s.T(), overviewAfterApply.TotalTokens, 0)
	require.Equal(s.T(), 1, overviewAfterApply.SuccessfulRolloutCount)
	require.Equal(s.T(), 0, overviewAfterApply.FailedExecutionCount)
	require.NotEmpty(s.T(), overviewAfterApply.ActionSummary)
	require.NotEmpty(s.T(), overviewAfterApply.OutcomeSummary)

	rollbackResp := postAPIJSON[response.ApplyResultResp](s.T(), s, http.MethodPost, "/api/v1/applies/result", request.ApplyResultReq{
		ApplyID:     applyResp.ApplyID,
		Success:     true,
		Note:        "rolled back by e2e",
		AppliedFile: "AGENTS.md",
		RolledBack:  true,
	})
	require.Equal(s.T(), "rollback_confirmed", rollbackResp.Status)
	require.True(s.T(), rollbackResp.RolledBack)

	historyAfterRollback := getAPIJSON[response.ApplyHistoryResp](s.T(), s, "/api/v1/applies", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.NotEmpty(s.T(), historyAfterRollback.Items)
	require.Equal(s.T(), "rollback_confirmed", historyAfterRollback.Items[0].Status)
	require.True(s.T(), historyAfterRollback.Items[0].RolledBack)

	overviewAfterRollback := getAPIJSON[response.DashboardOverviewResp](s.T(), s, "/api/v1/dashboard/overview", url.Values{
		"org_id": []string{orgID},
	})
	require.Greater(s.T(), overviewAfterRollback.AvgQueriesPerSession, 0.0)
	require.Greater(s.T(), overviewAfterRollback.TotalTokens, 0)
	require.Equal(s.T(), 0, overviewAfterRollback.SuccessfulRolloutCount)
	require.Equal(s.T(), 0, overviewAfterRollback.FailedExecutionCount)
	require.NotEmpty(s.T(), overviewAfterRollback.ActionSummary)
	require.NotEmpty(s.T(), overviewAfterRollback.OutcomeSummary)

	auditResp := getAPIJSON[response.AuditListResp](s.T(), s, "/api/v1/audits", url.Values{
		"org_id":     []string{orgID},
		"project_id": []string{projectResp.ProjectID},
	})
	require.NotEmpty(s.T(), auditResp.Items)
}

func postAPIJSON[T any](t *testing.T, s *APISuite, method, path string, payload any) T {
	t.Helper()

	body, err := json.Marshal(payload)
	require.NoError(t, err)

	fullURL, err := s.c.buildURL(path, nil)
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(s.ctx, method, fullURL, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AgentOpt-Token", apiToken())

	resp, err := s.c.HTTP.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	env, data, err := decodeEnvelope[T](rawBody)
	require.NoError(t, err)
	require.Equal(t, 0, env.Code, env.Message)
	require.NotNil(t, data)

	return *data
}

func getAPIJSON[T any](t *testing.T, s *APISuite, path string, query url.Values) T {
	t.Helper()

	fullURL, err := s.c.buildURL(path, query)
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, fullURL, nil)
	require.NoError(t, err)
	req.Header.Set("X-AgentOpt-Token", apiToken())

	resp, err := s.c.HTTP.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	env, data, err := decodeEnvelope[T](rawBody)
	require.NoError(t, err)
	require.Equal(t, 0, env.Code, env.Message)
	require.NotNil(t, data)

	return *data
}

func apiToken() string {
	token, ok := os.LookupEnv("E2E_API_TOKEN")
	if !ok || token == "" {
		return "agentopt-dev-token"
	}
	return token
}
