package controller_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/require"

	"github.com/Royaltyprogram/aiops/configs"
	"github.com/Royaltyprogram/aiops/dto/request"
	"github.com/Royaltyprogram/aiops/dto/response"
	"github.com/Royaltyprogram/aiops/routes"
	"github.com/Royaltyprogram/aiops/routes/controller"
	"github.com/Royaltyprogram/aiops/service"
)

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"msg"`
	Data    json.RawMessage `json:"data"`
}

func isBenignRouteConnError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset by peer")
}

func waitForDashboardResearchStatus(
	t *testing.T,
	echo *echo.Echo,
	token, orgID string,
	matcher func(*response.ReportResearchStatusResp) bool,
) *response.ReportResearchStatusResp {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		overview := getJSON[response.DashboardOverviewResp](t, echo, token, "/api/v1/dashboard/overview", url.Values{
			"org_id": []string{orgID},
		})
		if matcher(overview.ResearchStatus) {
			return overview.ResearchStatus
		}
		time.Sleep(20 * time.Millisecond)
	}

	overview := getJSON[response.DashboardOverviewResp](t, echo, token, "/api/v1/dashboard/overview", url.Values{
		"org_id": []string{orgID},
	})
	require.True(t, matcher(overview.ResearchStatus), "unexpected research status: %#v", overview.ResearchStatus)
	return overview.ResearchStatus
}

func TestAnalyticsRouteLifecycle(t *testing.T) {
	conf := newRouteResearchConfig(t)
	conf.App.APIToken = "route-token"
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")

	store, err := service.NewAnalyticsStore(conf)
	require.NoError(t, err)

	analyticsSvc := service.NewAnalyticsService(service.Options{
		Config:            conf,
		AnalyticsStore:    store,
		ReportMinSessions: 1,
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
		ProjectID:             projectResp.ProjectID,
		Tool:                  "codex",
		TokenIn:               1000,
		TokenOut:              200,
		CachedInputTokens:     260,
		ReasoningOutputTokens: 40,
		FunctionCallCount:     2,
		ToolErrorCount:        1,
		SessionDurationMS:     91000,
		ToolWallTimeMS:        900,
		ToolCalls:             map[string]int{"shell": 1, "read_file": 1},
		ToolErrors:            map[string]int{"shell": 1},
		ToolWallTimesMS:       map[string]int{"shell": 700, "read_file": 200},
		RawQueries: []string{
			"Inspect the route handler and summarize the current control flow.",
			"Find the smallest patch that fixes the analytics route regression.",
			"List the exact tests to run after the patch.",
		},
		Models:                 []string{"gpt-5.4"},
		ModelProvider:          "openai",
		FirstResponseLatencyMS: 2300,
		AssistantResponses: []string{
			"The analytics route registers auth, ingestion, and dashboard handlers in one place.",
		},
		ReasoningSummaries: []string{
			"Checking route flow and test expectations before proposing the patch.",
		},
	})
	require.NotNil(t, ingestResp.ResearchStatus)
	require.Equal(t, "report-api.v1", ingestResp.SchemaVersion)
	require.Equal(t, "running", ingestResp.ResearchStatus.State)

	status := waitForDashboardResearchStatus(t, echo, conf.App.APIToken, "org-route", func(item *response.ReportResearchStatusResp) bool {
		return item != nil && item.State == "succeeded"
	})
	require.Equal(t, "report-api.v1", status.SchemaVersion)
	require.Equal(t, 1, status.ReportCount)

	snapshotList := getJSON[response.ConfigSnapshotListResp](t, echo, conf.App.APIToken, "/api/v1/config-snapshots", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.NotEmpty(t, snapshotList.Items)

	sessionList := getJSON[response.SessionSummaryListResp](t, echo, conf.App.APIToken, "/api/v1/session-summaries", url.Values{
		"project_id": []string{projectResp.ProjectID},
		"limit":      []string{"5"},
	})
	require.NotEmpty(t, sessionList.Items)
	require.Equal(t, []string{"gpt-5.4"}, sessionList.Items[0].Models)
	require.Equal(t, "openai", sessionList.Items[0].ModelProvider)
	require.Equal(t, 2300, sessionList.Items[0].FirstResponseLatencyMS)
	require.Equal(t, 260, sessionList.Items[0].CachedInputTokens)
	require.Equal(t, 40, sessionList.Items[0].ReasoningOutputTokens)
	require.Equal(t, 2, sessionList.Items[0].FunctionCallCount)
	require.Equal(t, 1, sessionList.Items[0].ToolErrorCount)
	require.Equal(t, 91000, sessionList.Items[0].SessionDurationMS)
	require.Equal(t, 900, sessionList.Items[0].ToolWallTimeMS)
	require.Equal(t, map[string]int{"shell": 1, "read_file": 1}, sessionList.Items[0].ToolCalls)
	require.Equal(t, map[string]int{"shell": 1}, sessionList.Items[0].ToolErrors)
	require.Equal(t, map[string]int{"shell": 700, "read_file": 200}, sessionList.Items[0].ToolWallTimesMS)
	require.Equal(t, []string{"The analytics route registers auth, ingestion, and dashboard handlers in one place."}, sessionList.Items[0].AssistantResponses)
	require.Equal(t, []string{"Checking route flow and test expectations before proposing the patch."}, sessionList.Items[0].ReasoningSummaries)

	recResp := getJSON[response.ReportListResp](t, echo, conf.App.APIToken, "/api/v1/reports", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.Equal(t, "report-api.v1", recResp.SchemaVersion)
	require.NotEmpty(t, recResp.Items)
	require.Contains(t, recResp.Items[0].RawSuggestion, "\"kind\": \"llm-workflow-review\"")
	require.NotEmpty(t, recResp.Items[0].UserIntent)
	require.NotEmpty(t, recResp.Items[0].ModelInterpretation)

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
	waitForDashboardResearchStatus(t, echo, conf.App.APIToken, "org-route", func(item *response.ReportResearchStatusResp) bool {
		return item != nil && item.State == "succeeded"
	})

	recRespAfterSecondSession := getJSON[response.ReportListResp](t, echo, conf.App.APIToken, "/api/v1/reports", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.NotEmpty(t, recRespAfterSecondSession.Items)
	require.NotEmpty(t, recRespAfterSecondSession.Items[0].Summary)

	overviewResp := getJSON[response.DashboardOverviewResp](t, echo, conf.App.APIToken, "/api/v1/dashboard/overview", url.Values{
		"org_id": []string{"org-route"},
	})
	require.Equal(t, "report-api.v1", overviewResp.SchemaVersion)
	require.Greater(t, overviewResp.AvgTokensPerQuery, 0.0)
	require.Greater(t, overviewResp.AvgInputTokensPerQuery, 0.0)
	require.Greater(t, overviewResp.AvgOutputTokensPerQuery, 0.0)
	require.Greater(t, overviewResp.TotalInputTokens, 0)
	require.Greater(t, overviewResp.TotalOutputTokens, 0)
	require.Greater(t, overviewResp.TotalTokens, 0)
	require.NotEmpty(t, overviewResp.ActionSummary)
	require.NotEmpty(t, overviewResp.OutcomeSummary)

	insightsResp := getJSON[response.DashboardProjectInsightsResp](t, echo, conf.App.APIToken, "/api/v1/dashboard/project-insights", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.Equal(t, "report-api.v1", insightsResp.SchemaVersion)
	require.NotEmpty(t, insightsResp.Days)
	require.Equal(t, projectResp.ProjectID, insightsResp.ProjectID)
	require.Equal(t, 1, insightsResp.KnownModelSessions)
	require.Equal(t, 1, insightsResp.KnownProviderSessions)
	require.Equal(t, 1, insightsResp.KnownLatencySessions)
	require.Equal(t, 1, insightsResp.KnownDurationSessions)
	require.Equal(t, 2300, insightsResp.AvgFirstResponseLatencyMS)
	require.Equal(t, 91000, insightsResp.AvgSessionDurationMS)
	require.Equal(t, 260, insightsResp.TotalCachedInputTokens)
	require.Equal(t, 40, insightsResp.TotalReasoningOutputTokens)
	require.Equal(t, 2, insightsResp.TotalFunctionCalls)
	require.Equal(t, 1, insightsResp.TotalToolErrors)
	require.Equal(t, 900, insightsResp.TotalToolWallTimeMS)
	require.Equal(t, 450, insightsResp.AvgToolWallTimeMS)
	require.Equal(t, 1, insightsResp.SessionsWithFunctionCalls)
	require.Equal(t, 1, insightsResp.SessionsWithToolErrors)
	require.NotEmpty(t, insightsResp.Tools)
	require.Equal(t, "read_file", insightsResp.Tools[0].Tool)
	require.Equal(t, 1, insightsResp.Tools[0].CallCount)
	require.Zero(t, insightsResp.Tools[0].ErrorCount)
	require.Equal(t, 200, insightsResp.Tools[0].WallTimeMS)
	require.Equal(t, 200, insightsResp.Tools[0].AvgWallTimeMS)
	sumDayCalls := 0
	sumDayErrors := 0
	sumDayToolWallTime := 0
	sumDayDurations := 0
	for _, day := range insightsResp.Days {
		sumDayCalls += day.FunctionCallCount
		sumDayErrors += day.ToolErrorCount
		sumDayToolWallTime += day.ToolWallTimeMS
		sumDayDurations += day.DurationSessionCount
	}
	require.Equal(t, insightsResp.TotalFunctionCalls, sumDayCalls)
	require.Equal(t, insightsResp.TotalToolErrors, sumDayErrors)
	require.Equal(t, insightsResp.TotalToolWallTimeMS, sumDayToolWallTime)
	require.Equal(t, insightsResp.KnownDurationSessions, sumDayDurations)
	require.NotEmpty(t, insightsResp.Models)
	require.Equal(t, "gpt-5.4", insightsResp.Models[0].Model)
	require.NotEmpty(t, insightsResp.Providers)
	require.Equal(t, "openai", insightsResp.Providers[0].Provider)

	auditResp := getJSON[response.AuditListResp](t, echo, conf.App.APIToken, "/api/v1/audits", url.Values{
		"org_id": []string{"org-route"},
	})
	require.NotEmpty(t, auditResp.Items)
}

func TestAnalyticsRouteRefreshesReportsEveryBatchOfSessions(t *testing.T) {
	conf := newRouteResearchConfig(t)
	conf.App.APIToken = "route-token"
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")

	store, err := service.NewAnalyticsStore(conf)
	require.NoError(t, err)

	analyticsSvc := service.NewAnalyticsService(service.Options{
		Config:            conf,
		AnalyticsStore:    store,
		ReportMinSessions: 10,
	})

	echo, err := routes.NewEcho(conf, nil, store)
	require.NoError(t, err)

	route := controller.NewAnalyticsRoute(controller.Options{
		AnalyticsService: analyticsSvc,
	})
	route.RegisterRoute(echo.Group(""))

	agentResp := postJSON[response.AgentRegistrationResp](t, echo, conf.App.APIToken, http.MethodPost, "/api/v1/agents/register", request.RegisterAgentReq{
		OrgID:      "org-batch",
		UserID:     "user-batch",
		DeviceName: "batch-mbp",
	})
	projectResp := postJSON[response.ProjectRegistrationResp](t, echo, conf.App.APIToken, http.MethodPost, "/api/v1/projects/register", request.RegisterProjectReq{
		OrgID:       "org-batch",
		AgentID:     agentResp.AgentID,
		Name:        "batch-project",
		RepoHash:    "batch-project-hash",
		DefaultTool: "codex",
	})

	baseTime := time.Now().UTC().Round(time.Second)
	for i := 1; i <= 9; i++ {
		resp := postJSON[response.SessionIngestResp](t, echo, conf.App.APIToken, http.MethodPost, "/api/v1/session-summaries", request.SessionSummaryReq{
			ProjectID: projectResp.ProjectID,
			Tool:      "codex",
			RawQueries: []string{
				"Inspect the batch refresh policy before editing it.",
				"Confirm when the next report refresh should run.",
			},
			Timestamp: baseTime.Add(time.Duration(i) * time.Minute),
		})
		require.NotNil(t, resp.ResearchStatus)
		require.Equal(t, "waiting_for_min_sessions", resp.ResearchStatus.State)
		require.Equal(t, i, resp.ResearchStatus.SessionCount)
	}

	tenthResp := postJSON[response.SessionIngestResp](t, echo, conf.App.APIToken, http.MethodPost, "/api/v1/session-summaries", request.SessionSummaryReq{
		ProjectID: projectResp.ProjectID,
		Tool:      "codex",
		RawQueries: []string{
			"Generate the first report now that ten sessions are available.",
		},
		Timestamp: baseTime.Add(10 * time.Minute),
	})
	require.NotNil(t, tenthResp.ResearchStatus)
	require.Equal(t, "running", tenthResp.ResearchStatus.State)

	statusAfterTen := waitForDashboardResearchStatus(t, echo, conf.App.APIToken, "org-batch", func(item *response.ReportResearchStatusResp) bool {
		return item != nil && item.State == "succeeded" && item.SessionCount == 10
	})
	require.Equal(t, 1, statusAfterTen.ReportCount)

	reportsAfterTen := getJSON[response.ReportListResp](t, echo, conf.App.APIToken, "/api/v1/reports", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.Len(t, reportsAfterTen.Items, 1)
	firstReportID := reportsAfterTen.Items[0].ID

	eleventhResp := postJSON[response.SessionIngestResp](t, echo, conf.App.APIToken, http.MethodPost, "/api/v1/session-summaries", request.SessionSummaryReq{
		ProjectID: projectResp.ProjectID,
		Tool:      "codex",
		RawQueries: []string{
			"Keep the current report active until the next batch boundary.",
		},
		Timestamp: baseTime.Add(11 * time.Minute),
	})
	require.NotNil(t, eleventhResp.ResearchStatus)
	require.Equal(t, "waiting_for_next_batch", eleventhResp.ResearchStatus.State)
	require.Equal(t, 11, eleventhResp.ResearchStatus.SessionCount)
	require.Contains(t, eleventhResp.ResearchStatus.Summary, "20 sessions")

	reportsAfterEleven := getJSON[response.ReportListResp](t, echo, conf.App.APIToken, "/api/v1/reports", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.Len(t, reportsAfterEleven.Items, 1)
	require.Equal(t, firstReportID, reportsAfterEleven.Items[0].ID)

	for i := 12; i <= 19; i++ {
		resp := postJSON[response.SessionIngestResp](t, echo, conf.App.APIToken, http.MethodPost, "/api/v1/session-summaries", request.SessionSummaryReq{
			ProjectID: projectResp.ProjectID,
			Tool:      "codex",
			RawQueries: []string{
				"Accumulate more sessions before the next report refresh.",
			},
			Timestamp: baseTime.Add(time.Duration(i) * time.Minute),
		})
		require.NotNil(t, resp.ResearchStatus)
		require.Equal(t, "waiting_for_next_batch", resp.ResearchStatus.State)
	}

	twentiethResp := postJSON[response.SessionIngestResp](t, echo, conf.App.APIToken, http.MethodPost, "/api/v1/session-summaries", request.SessionSummaryReq{
		ProjectID: projectResp.ProjectID,
		Tool:      "codex",
		RawQueries: []string{
			"Refresh the report now that the second batch is complete.",
		},
		Timestamp: baseTime.Add(20 * time.Minute),
	})
	require.NotNil(t, twentiethResp.ResearchStatus)
	require.Equal(t, "running", twentiethResp.ResearchStatus.State)

	statusAfterTwenty := waitForDashboardResearchStatus(t, echo, conf.App.APIToken, "org-batch", func(item *response.ReportResearchStatusResp) bool {
		return item != nil && item.State == "succeeded" && item.SessionCount == 20
	})
	require.Equal(t, 1, statusAfterTwenty.ReportCount)

	reportsAfterTwenty := getJSON[response.ReportListResp](t, echo, conf.App.APIToken, "/api/v1/reports", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.Len(t, reportsAfterTwenty.Items, 1)
	require.NotEqual(t, firstReportID, reportsAfterTwenty.Items[0].ID)
}

func newRouteResearchConfig(t *testing.T) *configs.Config {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/responses", r.URL.Path)
		require.Equal(t, "Bearer test-openai-key", r.Header.Get("Authorization"))

		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			if isBenignRouteConnError(err) {
				return
			}
			require.NoError(t, err)
		}
		body := `{
  "output": [
    {
      "type": "message",
      "content": [
        {
          "type": "output_text",
          "text": "{\"schema_version\":\"report-feedback.v1\",\"reports\":[{\"kind\":\"llm-workflow-review\",\"title\":\"Reduce repeated workflow recap before implementation\",\"summary\":\"The uploaded raw queries show the user spending too many turns on control-flow recap and verification setup before the actual patch work starts.\",\"user_intent\":\"The user wants a small, validated fix and keeps narrowing scope before implementation.\",\"model_interpretation\":\"The model appears to understand the request as repo-orientation and verification planning first, then patching.\",\"reason\":\"Recent raw queries repeatedly ask for current behavior summaries, exact checks, and narrow patch scope before implementation begins.\",\"explanation\":\"The report should call out that the user is compensating for missing default repo discovery and verification structure.\",\"expected_benefit\":\"Less repeated steering and faster transition from orientation to implementation.\",\"risk\":\"Low. Observational feedback only.\",\"expected_impact\":\"Fewer exploratory turns and clearer first useful responses.\",\"confidence\":\"high\",\"strengths\":[\"The user consistently asks for narrow patch scope.\",\"Verification intent is explicit before risky edits.\"],\"frictions\":[\"Repo discovery is repeated across sessions.\",\"Verification setup often arrives only after extra recap turns.\"],\"next_steps\":[\"Start with concrete file discovery before summarizing control flow.\",\"List targeted verification immediately after locating the fix.\"],\"score\":0.82,\"evidence\":[\"repeated control-flow recap\",\"repeated verification prompts\"]}]}"
        }
      ]
    }
  ]
}`

		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(body))
		if !isBenignRouteConnError(err) {
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

func TestAnalyticsRouteLoginAndCLITokenFlow(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")

	store, err := service.NewAnalyticsStore(conf)
	require.NoError(t, err)

	analyticsSvc := service.NewAnalyticsService(service.Options{
		Config:            conf,
		AnalyticsStore:    store,
		ReportMinSessions: 1,
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
	require.Equal(t, "admin", loginResp.User.Role)
	require.Equal(t, "active", loginResp.User.Status)
	sessionCookie := requireCookie(t, loginRec, service.WebSessionCookieName)

	sessionResp := getJSON[response.AuthSessionResp](t, echo, "", "/api/v1/auth/me", nil, sessionCookie)
	require.Equal(t, "demo-org", sessionResp.Organization.ID)
	require.Equal(t, "demo-user", sessionResp.User.ID)
	require.Equal(t, "admin", sessionResp.User.Role)
	require.Equal(t, "active", sessionResp.User.Status)

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
	require.Equal(t, "admin", cliLoginResp.UserRole)
	require.Equal(t, "active", cliLoginResp.UserStatus)

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

func TestAnalyticsRouteRegisterAgentRejectsSpoofedUserID(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")

	store, err := service.NewAnalyticsStore(conf)
	require.NoError(t, err)

	analyticsSvc := service.NewAnalyticsService(service.Options{
		Config:            conf,
		AnalyticsStore:    store,
		ReportMinSessions: 1,
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
	sessionCookie := requireCookie(t, loginRec, service.WebSessionCookieName)

	rec := postJSONExpectCode(t, echo, "", http.MethodPost, "/api/v1/agents/register", request.RegisterAgentReq{
		OrgID:      "demo-org",
		UserID:     "spoofed-user",
		DeviceName: "spoof-attempt",
	}, http.StatusForbidden, sessionCookie)
	var env envelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	require.NotEqual(t, 0, env.Code)
}

func TestAnalyticsRouteAdminUserLifecycle(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")

	store, err := service.NewAnalyticsStore(conf)
	require.NoError(t, err)

	analyticsSvc := service.NewAnalyticsService(service.Options{
		Config:            conf,
		AnalyticsStore:    store,
		ReportMinSessions: 1,
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
	adminCookie := requireCookie(t, loginRec, service.WebSessionCookieName)

	createResp := postJSON[response.AdminUserCreateResp](t, echo, "", http.MethodPost, "/api/v1/admin/users", request.AdminUserCreateReq{
		Email:    "member@example.com",
		Name:     "Member User",
		Role:     "member",
		Password: "member-pass-1",
	}, adminCookie)
	require.Equal(t, "created", createResp.Status)
	require.Equal(t, "member", createResp.User.Role)
	require.Equal(t, "active", createResp.User.Status)

	adminList := getJSON[response.AdminUserListResp](t, echo, "", "/api/v1/admin/users", url.Values{
		"search": []string{"member@example.com"},
	}, adminCookie)
	require.Len(t, adminList.Items, 1)
	require.Equal(t, createResp.User.ID, adminList.Items[0].ID)

	memberLoginRec := postJSONRecorder(t, echo, "", http.MethodPost, "/api/v1/auth/login", request.LoginReq{
		Email:    "member@example.com",
		Password: "member-pass-1",
	})
	memberCookie := requireCookie(t, memberLoginRec, service.WebSessionCookieName)

	memberAdminRec := getJSONExpectCode(t, echo, "", "/api/v1/admin/users", nil, http.StatusForbidden, memberCookie)
	var forbidden envelope
	require.NoError(t, json.Unmarshal(memberAdminRec.Body.Bytes(), &forbidden))
	require.NotEqual(t, 0, forbidden.Code)

	resetResp := postJSON[response.AdminUserPasswordResetResp](t, echo, "", http.MethodPost, "/api/v1/admin/users/reset-password", request.AdminUserPasswordResetReq{
		UserID:   createResp.User.ID,
		Password: "member-pass-2",
	}, adminCookie)
	require.Equal(t, "password_reset", resetResp.Status)

	memberSessionAfterReset := getJSONExpectCode(t, echo, "", "/api/v1/auth/me", nil, http.StatusUnauthorized, memberCookie)
	require.Equal(t, http.StatusUnauthorized, memberSessionAfterReset.Code)

	oldPasswordRec := postJSONExpectCode(t, echo, "", http.MethodPost, "/api/v1/auth/login", request.LoginReq{
		Email:    "member@example.com",
		Password: "member-pass-1",
	}, http.StatusUnauthorized)
	require.Equal(t, http.StatusUnauthorized, oldPasswordRec.Code)

	memberLoginRec = postJSONRecorder(t, echo, "", http.MethodPost, "/api/v1/auth/login", request.LoginReq{
		Email:    "member@example.com",
		Password: "member-pass-2",
	})
	memberCookie = requireCookie(t, memberLoginRec, service.WebSessionCookieName)

	deactivateResp := postJSON[response.AdminUserDeactivateResp](t, echo, "", http.MethodPost, "/api/v1/admin/users/deactivate", request.AdminUserDeactivateReq{
		UserID: createResp.User.ID,
	}, adminCookie)
	require.Equal(t, "deactivated", deactivateResp.Status)

	memberSessionAfterDeactivate := getJSONExpectCode(t, echo, "", "/api/v1/auth/me", nil, http.StatusUnauthorized, memberCookie)
	require.Equal(t, http.StatusUnauthorized, memberSessionAfterDeactivate.Code)

	deactivatedList := getJSON[response.AdminUserListResp](t, echo, "", "/api/v1/admin/users", url.Values{
		"status": []string{"disabled"},
	}, adminCookie)
	require.Len(t, deactivatedList.Items, 1)
	require.Equal(t, createResp.User.ID, deactivatedList.Items[0].ID)

	deleteResp := postJSON[response.AdminUserDeleteResp](t, echo, "", http.MethodPost, "/api/v1/admin/users/delete", request.AdminUserDeleteReq{
		UserID: createResp.User.ID,
	}, adminCookie)
	require.Equal(t, "deleted", deleteResp.Status)

	defaultList := getJSON[response.AdminUserListResp](t, echo, "", "/api/v1/admin/users", url.Values{
		"search": []string{"member@example.com"},
	}, adminCookie)
	require.Empty(t, defaultList.Items)

	deletedList := getJSON[response.AdminUserListResp](t, echo, "", "/api/v1/admin/users", url.Values{
		"search":          []string{"member@example.com"},
		"include_deleted": []string{"true"},
		"status":          []string{"deleted"},
	}, adminCookie)
	require.Len(t, deletedList.Items, 1)
	require.Equal(t, "deleted", deletedList.Items[0].Status)

	deletedLoginRec := postJSONExpectCode(t, echo, "", http.MethodPost, "/api/v1/auth/login", request.LoginReq{
		Email:    "member@example.com",
		Password: "member-pass-2",
	}, http.StatusUnauthorized)
	require.Equal(t, http.StatusUnauthorized, deletedLoginRec.Code)

	auditResp := getJSON[response.AuditListResp](t, echo, "", "/api/v1/audits", url.Values{
		"org_id":         []string{"demo-org"},
		"type":           []string{"admin.user_deleted"},
		"target_user_id": []string{createResp.User.ID},
		"limit":          []string{"1"},
	}, adminCookie)
	require.Len(t, auditResp.Items, 1)
	require.Equal(t, "admin.user_deleted", auditResp.Items[0].Type)
	require.Equal(t, "demo-user", auditResp.Items[0].ActorUserID)
	require.Equal(t, "admin", auditResp.Items[0].ActorRole)
	require.Equal(t, createResp.User.ID, auditResp.Items[0].TargetUserID)
	require.NotEmpty(t, auditResp.Items[0].SourceIP)
	require.Equal(t, "route-test-client", auditResp.Items[0].UserAgent)
	require.Equal(t, "success", auditResp.Items[0].Result)
	require.Equal(t, "managed user soft-deleted and sessions revoked", auditResp.Items[0].Reason)
}

func TestAnalyticsRouteDoesNotExposeLegacyAliasEndpoints(t *testing.T) {
	conf := &configs.Config{}
	conf.App.APIToken = "route-token"
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")

	store, err := service.NewAnalyticsStore(conf)
	require.NoError(t, err)

	analyticsSvc := service.NewAnalyticsService(service.Options{
		Config:            conf,
		AnalyticsStore:    store,
		ReportMinSessions: 1,
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
		req.Header.Set("X-Crux-Token", conf.App.APIToken)
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
	req.Header.Set("User-Agent", "route-test-client")
	if token != "" {
		req.Header.Set("X-Crux-Token", token)
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
	req.Header.Set("User-Agent", "route-test-client")
	if token != "" {
		req.Header.Set("X-Crux-Token", token)
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

func getJSONExpectCode(t *testing.T, handler http.Handler, token, path string, query url.Values, expectedCode int, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()

	target := path
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req = req.WithContext(context.Background())
	req.Header.Set("User-Agent", "route-test-client")
	if token != "" {
		req.Header.Set("X-Crux-Token", token)
	}
	for _, cookie := range cookies {
		if cookie != nil {
			req.AddCookie(cookie)
		}
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, expectedCode, rec.Code)
	return rec
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

func postJSONExpectCode(t *testing.T, handler http.Handler, token, method, path string, payload any, expectedCode int, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req = req.WithContext(context.Background())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "route-test-client")
	if token != "" {
		req.Header.Set("X-Crux-Token", token)
	}
	for _, cookie := range cookies {
		if cookie != nil {
			req.AddCookie(cookie)
		}
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, expectedCode, rec.Code)
	return rec
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
