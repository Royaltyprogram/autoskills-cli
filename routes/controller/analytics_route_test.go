package controller_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/liushuangls/go-server-template/configs"
	"github.com/liushuangls/go-server-template/dto/request"
	"github.com/liushuangls/go-server-template/dto/response"
	"github.com/liushuangls/go-server-template/routes"
	"github.com/liushuangls/go-server-template/routes/controller"
	"github.com/liushuangls/go-server-template/service"
)

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"msg"`
	Data    json.RawMessage `json:"data"`
}

func TestAnalyticsRouteLifecycle(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := service.NewAnalyticsStore(conf)
	require.NoError(t, err)

	analyticsSvc := service.NewAnalyticsService(service.Options{
		Config:         conf,
		AnalyticsStore: store,
	})

	echo, err := routes.NewEcho(conf, nil)
	require.NoError(t, err)

	route := controller.NewAnalyticsRoute(controller.Options{
		AnalyticsService: analyticsSvc,
	})
	route.RegisterRoute(echo.Group(""))

	agentResp := postJSON[response.AgentRegistrationResp](t, echo, http.MethodPost, "/api/v1/agents/register", request.RegisterAgentReq{
		OrgID:      "org-route",
		UserID:     "user-route",
		DeviceName: "mbp",
	})
	require.Equal(t, "registered", agentResp.Status)

	projectResp := postJSON[response.ProjectRegistrationResp](t, echo, http.MethodPost, "/api/v1/projects/register", request.RegisterProjectReq{
		OrgID:       "org-route",
		AgentID:     agentResp.AgentID,
		Name:        "route-project",
		RepoHash:    "route-project-hash",
		DefaultTool: "codex",
	})
	require.Equal(t, "connected", projectResp.Status)

	snapshotResp := postJSON[response.ConfigSnapshotResp](t, echo, http.MethodPost, "/api/v1/config-snapshots", request.ConfigSnapshotReq{
		ProjectID: projectResp.ProjectID,
		Tool:      "codex",
		ProfileID: "baseline",
		Settings:  map[string]any{"instructions_pack": "baseline"},
	})
	require.Equal(t, "baseline", snapshotResp.ProfileID)

	ingestResp := postJSON[response.SessionIngestResp](t, echo, http.MethodPost, "/api/v1/session-summaries", request.SessionSummaryReq{
		ProjectID:             projectResp.ProjectID,
		Tool:                  "codex",
		TaskType:              "bugfix",
		ProjectHash:           "route-project-hash",
		LanguageMix:           map[string]float64{"go": 1},
		TotalPromptsCount:     12,
		TotalToolCalls:        24,
		BashCallsCount:        6,
		ReadOps:               10,
		EditOps:               8,
		WriteOps:              2,
		MCPUsageCount:         1,
		PermissionRejectCount: 2,
		RetryCount:            1,
		TokenIn:               1000,
		TokenOut:              200,
		EstimatedCost:         0.4,
		RepoSizeBucket:        "large",
		ConfigProfileID:       "baseline",
	})
	require.NotEmpty(t, ingestResp.LatestRecommendationIDs)

	snapshotList := getJSON[response.ConfigSnapshotListResp](t, echo, "/api/v1/config-snapshots", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.NotEmpty(t, snapshotList.Items)

	sessionList := getJSON[response.SessionSummaryListResp](t, echo, "/api/v1/session-summaries", url.Values{
		"project_id": []string{projectResp.ProjectID},
		"limit":      []string{"5"},
	})
	require.NotEmpty(t, sessionList.Items)

	recResp := getJSON[response.RecommendationListResp](t, echo, "/api/v1/recommendations", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.NotEmpty(t, recResp.Items)

	applyResp := postJSON[response.ApplyPlanResp](t, echo, http.MethodPost, "/api/v1/recommendations/apply", request.ApplyRecommendationReq{
		RecommendationID: recResp.Items[0].ID,
		RequestedBy:      "user-route",
	})
	require.Equal(t, "awaiting_review", applyResp.Status)

	reviewResp := postJSON[response.ChangePlanReviewResp](t, echo, http.MethodPost, "/api/v1/change-plans/review", request.ReviewChangePlanReq{
		ApplyID:    applyResp.ApplyID,
		Decision:   "approve",
		ReviewedBy: "user-route",
	})
	require.Equal(t, "approved_for_local_apply", reviewResp.Status)

	pendingResp := getJSON[response.PendingApplyResp](t, echo, "/api/v1/applies/pending", url.Values{
		"project_id": []string{projectResp.ProjectID},
		"user_id":    []string{"user-route"},
	})
	require.Len(t, pendingResp.Items, 1)
	require.Equal(t, applyResp.ApplyID, pendingResp.Items[0].ApplyID)

	applyResult := postJSON[response.ApplyResultResp](t, echo, http.MethodPost, "/api/v1/applies/result", request.ApplyResultReq{
		ApplyID:         applyResp.ApplyID,
		Success:         true,
		Note:            "applied by route test",
		AppliedFile:     "AGENTS.md, .codex/config.json",
		AppliedSettings: map[string]any{"instructions_pack": "repo-research"},
		AppliedText:     "AgentOpt Research Pack",
	})
	require.Equal(t, "applied", applyResult.Status)
	require.False(t, applyResult.RolledBack)

	pendingAfterApply := getJSON[response.PendingApplyResp](t, echo, "/api/v1/applies/pending", url.Values{
		"project_id": []string{projectResp.ProjectID},
		"user_id":    []string{"user-route"},
	})
	require.Empty(t, pendingAfterApply.Items)

	applyHistory := getJSON[response.ApplyHistoryResp](t, echo, "/api/v1/applies", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.NotEmpty(t, applyHistory.Items)
	require.Equal(t, "applied", applyHistory.Items[0].Status)
	require.Equal(t, "AGENTS.md, .codex/config.json", applyHistory.Items[0].AppliedFile)

	postApplySession := postJSON[response.SessionIngestResp](t, echo, http.MethodPost, "/api/v1/session-summaries", request.SessionSummaryReq{
		ProjectID:                projectResp.ProjectID,
		Tool:                     "codex",
		TaskType:                 "bugfix",
		ProjectHash:              "route-project-hash",
		LanguageMix:              map[string]float64{"go": 1},
		TotalPromptsCount:        8,
		TotalToolCalls:           16,
		BashCallsCount:           3,
		ReadOps:                  9,
		EditOps:                  6,
		WriteOps:                 2,
		MCPUsageCount:            1,
		PermissionRejectCount:    1,
		RetryCount:               0,
		TokenIn:                  700,
		TokenOut:                 180,
		EstimatedCost:            0.25,
		RepoSizeBucket:           "large",
		ConfigProfileID:          "repo-research",
		RepoExplorationIntensity: 0.4,
		AcceptanceProxy:          0.9,
		Timestamp:                time.Now().UTC().Add(2 * time.Hour),
	})
	require.NotEmpty(t, postApplySession.SessionID)

	impactResp := getJSON[response.ImpactSummaryResp](t, echo, "/api/v1/impact", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.NotEmpty(t, impactResp.Items)
	require.Equal(t, applyResp.ApplyID, impactResp.Items[0].ApplyID)
	require.Greater(t, impactResp.Items[0].SessionsAfter, 0)

	rollbackResp := postJSON[response.ApplyResultResp](t, echo, http.MethodPost, "/api/v1/applies/result", request.ApplyResultReq{
		ApplyID:     applyResp.ApplyID,
		Success:     true,
		Note:        "rolled back by route test",
		AppliedFile: "AGENTS.md, .codex/config.json",
		RolledBack:  true,
	})
	require.Equal(t, "rollback_confirmed", rollbackResp.Status)
	require.True(t, rollbackResp.RolledBack)

	applyHistoryAfterRollback := getJSON[response.ApplyHistoryResp](t, echo, "/api/v1/applies", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.NotEmpty(t, applyHistoryAfterRollback.Items)
	require.Equal(t, "rollback_confirmed", applyHistoryAfterRollback.Items[0].Status)
	require.True(t, applyHistoryAfterRollback.Items[0].RolledBack)

	auditResp := getJSON[response.AuditListResp](t, echo, "/api/v1/audits", url.Values{
		"org_id": []string{"org-route"},
	})
	require.NotEmpty(t, auditResp.Items)
}

func postJSON[T any](t *testing.T, handler http.Handler, method, path string, payload any) T {
	t.Helper()

	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req = req.WithContext(context.Background())
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var env envelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	require.Equal(t, 0, env.Code, env.Message)

	var data T
	require.NoError(t, json.Unmarshal(env.Data, &data))
	return data
}

func getJSON[T any](t *testing.T, handler http.Handler, path string, query url.Values) T {
	t.Helper()

	target := path
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req = req.WithContext(context.Background())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var env envelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	require.Equal(t, 0, env.Code, env.Message)

	var data T
	require.NoError(t, json.Unmarshal(env.Data, &data))
	return data
}
