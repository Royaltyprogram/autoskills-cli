package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/liushuangls/go-server-template/configs"
	"github.com/liushuangls/go-server-template/dto/request"
	"github.com/liushuangls/go-server-template/dto/response"
	"github.com/liushuangls/go-server-template/routes"
	"github.com/liushuangls/go-server-template/routes/controller"
	"github.com/liushuangls/go-server-template/service"
)

func TestMockDashboardApprovalTriggersLocalSyncAndRollback(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)
	useStubCodexRunner(t, "apply")

	serverURL := startMockAgentoptServer(t)
	dashboardClient := newDashboardClient(t)

	loginResp := dashboardPostJSON[response.LoginResp](t, dashboardClient, serverURL, "/api/v1/auth/login", request.LoginReq{
		Email:    "demo@example.com",
		Password: "demo1234",
	})
	require.Equal(t, "demo@example.com", loginResp.User.Email)

	cliTokenResp := dashboardPostJSON[response.CLITokenIssueResp](t, dashboardClient, serverURL, "/api/v1/auth/cli-tokens", request.IssueCLITokenReq{
		Label: "Mock E2E CLI",
	})
	require.NotEmpty(t, cliTokenResp.Token)

	workspace := filepath.Join(root, "workspace")
	agentsPath := filepath.Join(workspace, "AGENTS.md")
	originalAgents := "# Mock workspace\n"
	require.NoError(t, os.MkdirAll(workspace, 0o755))
	require.NoError(t, os.WriteFile(agentsPath, []byte(originalAgents), 0o644))

	captureStdout(t, func() {
		require.NoError(t, run([]string{
			"login",
			"--server", serverURL,
			"--token", cliTokenResp.Token,
			"--device", "mock-e2e-agent",
			"--hostname", "mock-e2e.local",
			"--platform", "darwin/arm64",
			"--tools", "codex",
		}))
	})
	captureStdout(t, func() {
		require.NoError(t, run([]string{
			"connect",
			"--project", "mock-e2e",
			"--repo-path", workspace,
		}))
	})
	captureStdout(t, func() {
		require.NoError(t, run([]string{"snapshot"}))
	})

	sessionFile := filepath.Join(root, "session.json")
	require.NoError(t, os.WriteFile(sessionFile, []byte(`{
  "tool": "codex",
  "token_in": 1000,
  "token_out": 200,
  "raw_queries": [
    "Inspect the route handler and summarize the current control flow.",
    "Find the smallest patch that fixes the analytics route regression.",
    "List the exact tests to run after the patch."
  ]
}`), 0o644))
	captureStdout(t, func() {
		require.NoError(t, run([]string{"session", "--file", sessionFile}))
	})

	st, err := loadState()
	require.NoError(t, err)
	require.NotEmpty(t, st.ProjectID)
	require.Equal(t, "demo-user", st.UserID)

	recommendations := dashboardGetJSON[response.RecommendationListResp](t, dashboardClient, serverURL, "/api/v1/recommendations", url.Values{
		"project_id": []string{st.ProjectID},
	})
	require.NotEmpty(t, recommendations.Items)
	require.Equal(t, "AGENTS.md", recommendations.Items[0].ChangePlan[0].TargetFile)

	applyResp := dashboardPostJSON[response.ApplyPlanResp](t, dashboardClient, serverURL, "/api/v1/recommendations/apply", request.ApplyRecommendationReq{
		RecommendationID: recommendations.Items[0].ID,
		RequestedBy:      st.UserID,
	})
	require.Equal(t, "awaiting_review", applyResp.Status)
	require.Len(t, applyResp.PatchPreview, 1)

	reviewResp := dashboardPostJSON[response.ChangePlanReviewResp](t, dashboardClient, serverURL, "/api/v1/change-plans/review", request.ReviewChangePlanReq{
		ApplyID:    applyResp.ApplyID,
		Decision:   "approve",
		ReviewedBy: st.UserID,
		ReviewNote: "mock approve click",
	})
	require.Equal(t, "approved_for_local_apply", reviewResp.Status)

	pendingBeforeSync := dashboardGetJSON[response.PendingApplyResp](t, dashboardClient, serverURL, "/api/v1/applies/pending", url.Values{
		"project_id": []string{st.ProjectID},
		"user_id":    []string{st.UserID},
	})
	require.Len(t, pendingBeforeSync.Items, 1)
	require.Equal(t, applyResp.ApplyID, pendingBeforeSync.Items[0].ApplyID)

	originalWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workspace))
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})

	syncOutput := captureStdout(t, func() {
		require.NoError(t, run([]string{"sync"}))
	})

	var syncPayload struct {
		FailedCount int `json:"failed_count"`
		Results     []struct {
			ApplyID string `json:"apply_id"`
			Status  string `json:"status"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(syncOutput), &syncPayload))
	require.Zero(t, syncPayload.FailedCount)
	require.Len(t, syncPayload.Results, 1)
	require.Equal(t, applyResp.ApplyID, syncPayload.Results[0].ApplyID)
	require.Equal(t, "applied", syncPayload.Results[0].Status)

	agentsAfterSync, err := os.ReadFile(agentsPath)
	require.NoError(t, err)
	require.Contains(t, string(agentsAfterSync), "## AgentOpt Personal Instruction Pack")

	historyAfterSync := dashboardGetJSON[response.ApplyHistoryResp](t, dashboardClient, serverURL, "/api/v1/applies", url.Values{
		"project_id": []string{st.ProjectID},
	})
	require.NotEmpty(t, historyAfterSync.Items)
	require.Equal(t, "applied", historyAfterSync.Items[0].Status)
	require.Equal(t, canonicalTestPath(t, agentsPath), canonicalTestPath(t, historyAfterSync.Items[0].AppliedFile))

	pendingAfterSync := dashboardGetJSON[response.PendingApplyResp](t, dashboardClient, serverURL, "/api/v1/applies/pending", url.Values{
		"project_id": []string{st.ProjectID},
		"user_id":    []string{st.UserID},
	})
	require.Empty(t, pendingAfterSync.Items)

	rollbackOutput := captureStdout(t, func() {
		require.NoError(t, run([]string{"rollback", "--apply-id", applyResp.ApplyID}))
	})
	var rollbackResp response.ApplyResultResp
	require.NoError(t, json.Unmarshal([]byte(rollbackOutput), &rollbackResp))
	require.Equal(t, "rollback_confirmed", rollbackResp.Status)
	require.True(t, rollbackResp.RolledBack)

	agentsAfterRollback, err := os.ReadFile(agentsPath)
	require.NoError(t, err)
	require.Equal(t, originalAgents, string(agentsAfterRollback))

	historyAfterRollback := dashboardGetJSON[response.ApplyHistoryResp](t, dashboardClient, serverURL, "/api/v1/applies", url.Values{
		"project_id": []string{st.ProjectID},
	})
	require.NotEmpty(t, historyAfterRollback.Items)
	require.Equal(t, "rollback_confirmed", historyAfterRollback.Items[0].Status)
	require.True(t, historyAfterRollback.Items[0].RolledBack)
}

func startMockAgentoptServer(t *testing.T) string {
	t.Helper()

	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := service.NewAnalyticsStore(conf)
	require.NoError(t, err)

	analyticsSvc := service.NewAnalyticsService(service.Options{
		Config:         conf,
		AnalyticsStore: store,
	})
	healthSvc := service.NewHealthService(service.Options{
		Config:         conf,
		AnalyticsStore: store,
	})

	echo, err := routes.NewEcho(conf, nil, store)
	require.NoError(t, err)

	controller.NewHealthRoute(controller.Options{
		HealthService: healthSvc,
	}).RegisterRoute(echo.Group(""))
	controller.NewAnalyticsRoute(controller.Options{
		AnalyticsService: analyticsSvc,
	}).RegisterRoute(echo.Group(""))

	server := httptest.NewServer(echo)
	t.Cleanup(server.Close)
	return server.URL
}

func newDashboardClient(t *testing.T) *http.Client {
	t.Helper()

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)

	return &http.Client{
		Jar: jar,
	}
}

func dashboardPostJSON[T any](t *testing.T, client *http.Client, baseURL, path string, payload any) T {
	t.Helper()

	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, baseURL+path, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var env envelope
	require.NoError(t, json.Unmarshal(rawBody, &env))
	require.Equal(t, 0, env.Code, env.Message)

	var data T
	require.NoError(t, json.Unmarshal(env.Data, &data))
	return data
}

func dashboardGetJSON[T any](t *testing.T, client *http.Client, baseURL, path string, query url.Values) T {
	t.Helper()

	reqURL := baseURL + path
	if encoded := query.Encode(); encoded != "" {
		reqURL += "?" + encoded
	}

	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var env envelope
	require.NoError(t, json.Unmarshal(rawBody, &env))
	require.Equal(t, 0, env.Code, env.Message)

	var data T
	require.NoError(t, json.Unmarshal(env.Data, &data))
	return data
}

func canonicalTestPath(t *testing.T, path string) string {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(path)
}
