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
	conf.App.APIToken = "route-token"
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := service.NewAnalyticsStore(conf)
	require.NoError(t, err)

	analyticsSvc := service.NewAnalyticsService(service.Options{
		Config:         conf,
		AnalyticsStore: store,
	})

	echo, err := routes.NewEcho(conf, nil, store)
	require.NoError(t, err)

	route := controller.NewAnalyticsRoute(controller.Options{
		AnalyticsService: analyticsSvc,
	})
	route.RegisterRoute(echo.Group(""))

	agentResp := postJSON[response.AgentRegistrationResp](t, echo, conf.App.APIToken, http.MethodPost, "/api/v1/agents/register", request.RegisterAgentReq{
		OrgID:      "org-route",
		UserID:     "user-route",
		DeviceName: "mbp",
	})
	require.Equal(t, "registered", agentResp.Status)

	projectResp := postJSON[response.ProjectRegistrationResp](t, echo, conf.App.APIToken, http.MethodPost, "/api/v1/projects/register", request.RegisterProjectReq{
		OrgID:       "org-route",
		AgentID:     agentResp.AgentID,
		Name:        "route-project",
		RepoHash:    "route-project-hash",
		DefaultTool: "codex",
	})
	require.Equal(t, "connected", projectResp.Status)

	snapshotResp := postJSON[response.ConfigSnapshotResp](t, echo, conf.App.APIToken, http.MethodPost, "/api/v1/config-snapshots", request.ConfigSnapshotReq{
		ProjectID: projectResp.ProjectID,
		Tool:      "codex",
		ProfileID: "baseline",
		Settings:  map[string]any{"instructions_pack": "baseline"},
	})
	require.Equal(t, "baseline", snapshotResp.ProfileID)

	ingestResp := postJSON[response.SessionIngestResp](t, echo, conf.App.APIToken, http.MethodPost, "/api/v1/session-summaries", request.SessionSummaryReq{
		ProjectID: projectResp.ProjectID,
		Tool:      "codex",
		TokenIn:   1000,
		TokenOut:  200,
		RawQueries: []string{
			"Inspect the route handler and summarize the current control flow.",
			"Find the smallest patch that fixes the analytics route regression.",
			"List the exact tests to run after the patch.",
		},
	})
	require.NotEmpty(t, ingestResp.LatestRecommendationIDs)

	snapshotList := getJSON[response.ConfigSnapshotListResp](t, echo, conf.App.APIToken, "/api/v1/config-snapshots", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.NotEmpty(t, snapshotList.Items)

	sessionList := getJSON[response.SessionSummaryListResp](t, echo, conf.App.APIToken, "/api/v1/session-summaries", url.Values{
		"project_id": []string{projectResp.ProjectID},
		"limit":      []string{"5"},
	})
	require.NotEmpty(t, sessionList.Items)

	recResp := getJSON[response.RecommendationListResp](t, echo, conf.App.APIToken, "/api/v1/recommendations", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.NotEmpty(t, recResp.Items)

	applyResp := postJSON[response.ApplyPlanResp](t, echo, conf.App.APIToken, http.MethodPost, "/api/v1/recommendations/apply", request.ApplyRecommendationReq{
		RecommendationID: recResp.Items[0].ID,
		RequestedBy:      "user-route",
	})
	require.Equal(t, "awaiting_review", applyResp.Status)

	reviewResp := postJSON[response.ChangePlanReviewResp](t, echo, conf.App.APIToken, http.MethodPost, "/api/v1/change-plans/review", request.ReviewChangePlanReq{
		ApplyID:    applyResp.ApplyID,
		Decision:   "approve",
		ReviewedBy: "user-route",
	})
	require.Equal(t, "approved_for_local_apply", reviewResp.Status)

	pendingResp := getJSON[response.PendingApplyResp](t, echo, conf.App.APIToken, "/api/v1/applies/pending", url.Values{
		"project_id": []string{projectResp.ProjectID},
		"user_id":    []string{"user-route"},
	})
	require.Len(t, pendingResp.Items, 1)
	require.Equal(t, applyResp.ApplyID, pendingResp.Items[0].ApplyID)

	applyResult := postJSON[response.ApplyResultResp](t, echo, conf.App.APIToken, http.MethodPost, "/api/v1/applies/result", request.ApplyResultReq{
		ApplyID:     applyResp.ApplyID,
		Success:     true,
		Note:        "applied by route test",
		AppliedFile: "AGENTS.md",
		AppliedText: "AgentOpt Personal Instruction Pack",
	})
	require.Equal(t, "applied", applyResult.Status)
	require.False(t, applyResult.RolledBack)

	pendingAfterApply := getJSON[response.PendingApplyResp](t, echo, conf.App.APIToken, "/api/v1/applies/pending", url.Values{
		"project_id": []string{projectResp.ProjectID},
		"user_id":    []string{"user-route"},
	})
	require.Empty(t, pendingAfterApply.Items)

	applyHistory := getJSON[response.ApplyHistoryResp](t, echo, conf.App.APIToken, "/api/v1/applies", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.NotEmpty(t, applyHistory.Items)
	require.Equal(t, "applied", applyHistory.Items[0].Status)
	require.Equal(t, "AGENTS.md", applyHistory.Items[0].AppliedFile)

	postApplySession := postJSON[response.SessionIngestResp](t, echo, conf.App.APIToken, http.MethodPost, "/api/v1/session-summaries", request.SessionSummaryReq{
		ProjectID: projectResp.ProjectID,
		Tool:      "codex",
		TokenIn:   700,
		TokenOut:  180,
		RawQueries: []string{
			"Compare the analytics and health controllers before editing the shared response contract.",
			"Keep the patch minimal and list the targeted verification steps.",
		},
		Timestamp: time.Now().UTC().Add(2 * time.Hour),
	})
	require.NotEmpty(t, postApplySession.SessionID)

	impactResp := getJSON[response.ImpactSummaryResp](t, echo, conf.App.APIToken, "/api/v1/impact", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.NotEmpty(t, impactResp.Items)
	require.Equal(t, applyResp.ApplyID, impactResp.Items[0].ApplyID)
	require.Greater(t, impactResp.Items[0].SessionsAfter, 0)

	rollbackResp := postJSON[response.ApplyResultResp](t, echo, conf.App.APIToken, http.MethodPost, "/api/v1/applies/result", request.ApplyResultReq{
		ApplyID:     applyResp.ApplyID,
		Success:     true,
		Note:        "rolled back by route test",
		AppliedFile: "AGENTS.md",
		RolledBack:  true,
	})
	require.Equal(t, "rollback_confirmed", rollbackResp.Status)
	require.True(t, rollbackResp.RolledBack)

	applyHistoryAfterRollback := getJSON[response.ApplyHistoryResp](t, echo, conf.App.APIToken, "/api/v1/applies", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.NotEmpty(t, applyHistoryAfterRollback.Items)
	require.Equal(t, "rollback_confirmed", applyHistoryAfterRollback.Items[0].Status)
	require.True(t, applyHistoryAfterRollback.Items[0].RolledBack)

	overviewResp := getJSON[response.DashboardOverviewResp](t, echo, conf.App.APIToken, "/api/v1/dashboard/overview", url.Values{
		"org_id": []string{"org-route"},
	})
	require.Greater(t, overviewResp.AvgTokensPerQuery, 0.0)
	require.Greater(t, overviewResp.AvgInputTokensPerQuery, 0.0)
	require.Greater(t, overviewResp.AvgOutputTokensPerQuery, 0.0)
	require.Greater(t, overviewResp.TotalInputTokens, 0)
	require.Greater(t, overviewResp.TotalOutputTokens, 0)
	require.Greater(t, overviewResp.TotalTokens, 0)
	require.NotEmpty(t, overviewResp.ActionSummary)
	require.NotEmpty(t, overviewResp.OutcomeSummary)

	auditResp := getJSON[response.AuditListResp](t, echo, conf.App.APIToken, "/api/v1/audits", url.Values{
		"org_id": []string{"org-route"},
	})
	require.NotEmpty(t, auditResp.Items)
}

func TestAnalyticsRouteLoginAndCLITokenFlow(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := service.NewAnalyticsStore(conf)
	require.NoError(t, err)

	analyticsSvc := service.NewAnalyticsService(service.Options{
		Config:         conf,
		AnalyticsStore: store,
	})

	echo, err := routes.NewEcho(conf, nil, store)
	require.NoError(t, err)

	route := controller.NewAnalyticsRoute(controller.Options{
		AnalyticsService: analyticsSvc,
	})
	route.RegisterRoute(echo.Group(""))

	loginRec := postJSONRecorder(t, echo, "", http.MethodPost, "/api/v1/auth/login", request.LoginReq{
		Email:    "demo@example.com",
		Password: "demo1234",
	})
	loginResp := decodeOK[response.LoginResp](t, loginRec)
	require.Empty(t, loginResp.SessionToken)
	require.Equal(t, "demo@example.com", loginResp.User.Email)
	sessionCookie := requireCookie(t, loginRec, service.WebSessionCookieName)

	sessionResp := getJSON[response.AuthSessionResp](t, echo, "", "/api/v1/auth/me", nil, sessionCookie)
	require.Equal(t, "demo-org", sessionResp.Organization.ID)
	require.Equal(t, "demo-user", sessionResp.User.ID)

	cliTokenResp := postJSON[response.CLITokenIssueResp](t, echo, "", http.MethodPost, "/api/v1/auth/cli-tokens", request.IssueCLITokenReq{
		Label: "CI Mac",
	}, sessionCookie)
	require.NotEmpty(t, cliTokenResp.Token)
	require.Equal(t, "CI Mac", cliTokenResp.Label)

	tokenListResp := getJSON[response.CLITokenListResp](t, echo, "", "/api/v1/auth/cli-tokens", nil, sessionCookie)
	require.Len(t, tokenListResp.Items, 1)
	require.Equal(t, "active", tokenListResp.Items[0].Status)

	cliLoginResp := postJSON[response.CLILoginResp](t, echo, cliTokenResp.Token, http.MethodPost, "/api/v1/auth/cli/login", request.CLILoginReq{
		DeviceName: "macbook-pro",
		Hostname:   "ci-mac.local",
		Platform:   "darwin/arm64",
		CLIVersion: "0.1.0-dev",
		Tools:      []string{"codex"},
	})
	require.Equal(t, "registered", cliLoginResp.Status)
	require.Equal(t, "demo-org", cliLoginResp.OrgID)
	require.Equal(t, "demo-user", cliLoginResp.UserID)

	tokenListResp = getJSON[response.CLITokenListResp](t, echo, "", "/api/v1/auth/cli-tokens", nil, sessionCookie)
	require.Len(t, tokenListResp.Items, 1)
	require.NotNil(t, tokenListResp.Items[0].LastUsedAt)

	revokeResp := postJSON[response.CLITokenRevokeResp](t, echo, "", http.MethodPost, "/api/v1/auth/cli-tokens/revoke", request.RevokeCLITokenReq{
		TokenID: cliTokenResp.TokenID,
	}, sessionCookie)
	require.Equal(t, "revoked", revokeResp.Status)

	revokedTokenList := getJSON[response.CLITokenListResp](t, echo, "", "/api/v1/auth/cli-tokens", nil, sessionCookie)
	require.Len(t, revokedTokenList.Items, 1)
	require.Equal(t, "revoked", revokedTokenList.Items[0].Status)

	logoutRec := postJSONRecorder(t, echo, "", http.MethodPost, "/api/v1/auth/logout", map[string]any{}, sessionCookie)
	decodeOK[response.LogoutResp](t, logoutRec)
	logoutCookie := requireCookie(t, logoutRec, service.WebSessionCookieName)
	require.Empty(t, logoutCookie.Value)
	require.Equal(t, -1, logoutCookie.MaxAge)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req = req.WithContext(context.Background())
	req.AddCookie(sessionCookie)
	rec := httptest.NewRecorder()
	echo.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAnalyticsRouteDoesNotExposeLegacyAliasEndpoints(t *testing.T) {
	conf := &configs.Config{}
	conf.App.APIToken = "route-token"
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := service.NewAnalyticsStore(conf)
	require.NoError(t, err)

	analyticsSvc := service.NewAnalyticsService(service.Options{
		Config:         conf,
		AnalyticsStore: store,
	})

	echo, err := routes.NewEcho(conf, nil, store)
	require.NoError(t, err)

	route := controller.NewAnalyticsRoute(controller.Options{
		AnalyticsService: analyticsSvc,
	})
	route.RegisterRoute(echo.Group(""))

	for _, tc := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/api/v1/devices/register"},
		{method: http.MethodPost, path: "/api/v1/projects/connect"},
		{method: http.MethodGet, path: "/api/v1/execution-queue"},
		{method: http.MethodPost, path: "/api/v1/executions/result"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req = req.WithContext(context.Background())
		req.Header.Set("X-AgentOpt-Token", conf.App.APIToken)
		rec := httptest.NewRecorder()
		echo.ServeHTTP(rec, req)
		require.Equal(t, http.StatusNotFound, rec.Code, "%s %s should be removed", tc.method, tc.path)
	}
}

func postJSON[T any](t *testing.T, handler http.Handler, token, method, path string, payload any, cookies ...*http.Cookie) T {
	t.Helper()

	rec := postJSONRecorder(t, handler, token, method, path, payload, cookies...)
	return decodeOK[T](t, rec)
}

func postJSONRecorder(t *testing.T, handler http.Handler, token, method, path string, payload any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req = req.WithContext(context.Background())
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-AgentOpt-Token", token)
	}
	for _, cookie := range cookies {
		if cookie != nil {
			req.AddCookie(cookie)
		}
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	return rec
}

func getJSON[T any](t *testing.T, handler http.Handler, token, path string, query url.Values, cookies ...*http.Cookie) T {
	t.Helper()

	target := path
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req = req.WithContext(context.Background())
	if token != "" {
		req.Header.Set("X-AgentOpt-Token", token)
	}
	for _, cookie := range cookies {
		if cookie != nil {
			req.AddCookie(cookie)
		}
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	return decodeOK[T](t, rec)
}

func decodeOK[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()

	var env envelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	require.Equal(t, 0, env.Code, env.Message)

	var data T
	require.NoError(t, json.Unmarshal(env.Data, &data))
	return data
}

func requireCookie(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()

	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}

	require.FailNowf(t, "cookie missing", "expected cookie %q on response", name)
	return nil
}
