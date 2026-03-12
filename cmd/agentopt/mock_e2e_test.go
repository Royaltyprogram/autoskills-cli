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
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Royaltyprogram/aiops/configs"
	"github.com/Royaltyprogram/aiops/dto/request"
	"github.com/Royaltyprogram/aiops/dto/response"
	"github.com/Royaltyprogram/aiops/routes"
	"github.com/Royaltyprogram/aiops/routes/controller"
	"github.com/Royaltyprogram/aiops/service"
)

func waitForDashboardRecommendations(t *testing.T, client *http.Client, baseURL, projectID string) response.RecommendationListResp {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		recommendations := dashboardGetJSON[response.RecommendationListResp](t, client, baseURL, "/api/v1/recommendations", url.Values{
			"project_id": []string{projectID},
		})
		if len(recommendations.Items) > 0 {
			return recommendations
		}
		time.Sleep(20 * time.Millisecond)
	}

	recommendations := dashboardGetJSON[response.RecommendationListResp](t, client, baseURL, "/api/v1/recommendations", url.Values{
		"project_id": []string{projectID},
	})
	require.NotEmpty(t, recommendations.Items)
	return recommendations
}

func TestMockDashboardApprovalTriggersLocalSyncAndRollback(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
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
	agentsPath := filepath.Join(home, ".codex", "AGENTS.md")
	originalAgents := "# Global Codex instructions\n"
	require.NoError(t, os.MkdirAll(workspace, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(agentsPath), 0o755))
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
	require.NotEmpty(t, st.WorkspaceID)
	require.Equal(t, "demo-user", st.UserID)
	workspaceID := st.workspaceID()

	recommendations := waitForDashboardRecommendations(t, dashboardClient, serverURL, workspaceID)
	require.NotEmpty(t, recommendations.Items)
	require.Equal(t, "~/.codex/AGENTS.md", recommendations.Items[0].ChangePlan[0].TargetFile)

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
		"project_id": []string{workspaceID},
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
	require.Contains(t, string(agentsAfterSync), "## AgentOpt Research Findings")

	historyAfterSync := dashboardGetJSON[response.ApplyHistoryResp](t, dashboardClient, serverURL, "/api/v1/applies", url.Values{
		"project_id": []string{workspaceID},
	})
	require.NotEmpty(t, historyAfterSync.Items)
	require.Equal(t, "applied", historyAfterSync.Items[0].Status)
	require.Equal(t, canonicalTestPath(t, agentsPath), canonicalTestPath(t, historyAfterSync.Items[0].AppliedFile))

	pendingAfterSync := dashboardGetJSON[response.PendingApplyResp](t, dashboardClient, serverURL, "/api/v1/applies/pending", url.Values{
		"project_id": []string{workspaceID},
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
		"project_id": []string{workspaceID},
	})
	require.NotEmpty(t, historyAfterRollback.Items)
	require.Equal(t, "rollback_confirmed", historyAfterRollback.Items[0].Status)
	require.True(t, historyAfterRollback.Items[0].RolledBack)
}

func startMockAgentoptServer(t *testing.T) string {
	t.Helper()

	conf := newMockResearchConfig(t)
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := service.NewAnalyticsStore(conf)
	require.NoError(t, err)

	analyticsSvc := service.NewAnalyticsService(service.Options{
		Config:                    conf,
		AnalyticsStore:            store,
		RecommendationMinSessions: 1,
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

func newMockResearchConfig(t *testing.T) *configs.Config {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/responses", r.URL.Path)
		require.Equal(t, "Bearer test-openai-key", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{
  "output": [
    {
      "type": "message",
      "content": [
        {
          "type": "output_text",
          "text": "{\"recommendations\":[{\"kind\":\"llm-workflow-review\",\"title\":\"Reduce repeated workflow recap before implementation\",\"summary\":\"The uploaded raw queries show too much control-flow recap before the actual patch work starts.\",\"reason\":\"Recent raw queries repeatedly ask for current behavior summaries and exact verification planning before implementation begins.\",\"explanation\":\"A small instruction update can push the coding agent to orient on the relevant files and define verification earlier.\",\"expected_benefit\":\"Less repeated steering and faster implementation starts.\",\"risk\":\"Low. Reviewable instruction update only.\",\"expected_impact\":\"Fewer exploratory turns and clearer first useful responses.\",\"score\":0.82,\"evidence\":[\"repeated control-flow recap\",\"repeated verification prompts\"],\"change_plan\":[{\"type\":\"text_append\",\"action\":\"append_block\",\"target_file\":\"~/.codex/AGENTS.md\",\"summary\":\"Add a reusable workflow-defaults block for Codex.\",\"content_preview\":\"## AgentOpt Research Findings\\n- Locate the concrete files first.\\n- Summarize only the relevant control flow.\\n- List targeted verification before proposing the patch.\\n\"}]}]}"
        }
      ]
    }
  ]
}`))
		require.NoError(t, err)
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
