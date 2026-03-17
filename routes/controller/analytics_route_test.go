package controller_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
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

type googleAuthTestUser struct {
	Code     string
	Subject  string
	Email    string
	Name     string
	Verified bool
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

func requireTokenByKind(t *testing.T, items []response.CLITokenItemResp, kind string) response.CLITokenItemResp {
	t.Helper()

	for _, item := range items {
		if item.Kind == kind {
			return item
		}
	}
	t.Fatalf("missing cli token kind %q", kind)
	return response.CLITokenItemResp{}
}

func loginCLICollectorForRouteTest(t *testing.T, echo *echo.Echo, googleCode, label string) response.CLILoginResp {
	t.Helper()

	sessionCookie := loginWithGoogleControllerTest(t, echo, googleCode)
	cliTokenResp := postJSON[response.CLITokenIssueResp](t, echo, "", http.MethodPost, "/api/v1/auth/cli-tokens", request.IssueCLITokenReq{
		Label: label,
	}, sessionCookie)

	return postJSON[response.CLILoginResp](t, echo, cliTokenResp.Token, http.MethodPost, "/api/v1/auth/cli/login", request.CLILoginReq{
		DeviceName: label,
		Hostname:   sanitizeForRouteTest(label) + ".local",
		Platform:   "darwin/arm64",
		CLIVersion: "0.1.0-dev",
		Tools:      []string{"codex"},
	})
}

func sanitizeForRouteTest(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "-")
	if value == "" {
		return "route-device"
	}
	return value
}

func ptrTime(value time.Time) *time.Time {
	cloned := value.UTC()
	return &cloned
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

func waitForSessionImportJobStatus(
	t *testing.T,
	echo *echo.Echo,
	token, jobID string,
	matcher func(*response.SessionImportJobResp) bool,
) *response.SessionImportJobResp {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		job := getJSON[response.SessionImportJobResp](t, echo, token, "/api/v1/session-import-jobs/"+jobID, nil)
		if matcher(&job) {
			return &job
		}
		time.Sleep(20 * time.Millisecond)
	}

	job := getJSON[response.SessionImportJobResp](t, echo, token, "/api/v1/session-import-jobs/"+jobID, nil)
	require.True(t, matcher(&job), "unexpected job status: %#v", job)
	return &job
}

func TestAnalyticsRouteLifecycle(t *testing.T) {
	conf := newRouteResearchConfig(t)
	conf.App.APIToken = "route-token"
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")
	closeGoogle := configureGoogleAuthControllerTest(t, conf, googleAuthTestUser{
		Code:     "route-login",
		Subject:  "google-route-subject",
		Email:    "demo@example.com",
		Name:     "Demo Operator",
		Verified: true,
	})
	defer closeGoogle()

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

	cliLoginResp := loginCLICollectorForRouteTest(t, echo, "route-login", "route-device")
	deviceToken := cliLoginResp.AccessToken

	projectResp := postJSON[response.ProjectRegistrationResp](t, echo, deviceToken, http.MethodPost, "/api/v1/projects/register", request.RegisterProjectReq{
		OrgID:       cliLoginResp.OrgID,
		AgentID:     cliLoginResp.AgentID,
		Name:        "route-project",
		RepoHash:    "route-project-hash",
		DefaultTool: "codex",
	})
	require.Equal(t, "connected", projectResp.Status)

	snapshotResp := postJSON[response.ConfigSnapshotResp](t, echo, deviceToken, http.MethodPost, "/api/v1/config-snapshots", request.ConfigSnapshotReq{
		ProjectID: projectResp.ProjectID,
		Tool:      "codex",
		ProfileID: "baseline",
		Settings:  map[string]any{"instructions_pack": "baseline"},
	})
	require.Equal(t, "baseline", snapshotResp.ProfileID)

	ingestResp := postJSON[response.SessionIngestResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-summaries", request.SessionSummaryReq{
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

	status := waitForDashboardResearchStatus(t, echo, deviceToken, cliLoginResp.OrgID, func(item *response.ReportResearchStatusResp) bool {
		return item != nil && item.State == "succeeded"
	})
	require.Equal(t, "report-api.v1", status.SchemaVersion)
	require.Equal(t, 1, status.ReportCount)

	snapshotList := getJSON[response.ConfigSnapshotListResp](t, echo, deviceToken, "/api/v1/config-snapshots", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.NotEmpty(t, snapshotList.Items)

	sessionList := getJSON[response.SessionSummaryListResp](t, echo, deviceToken, "/api/v1/session-summaries", url.Values{
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

	recResp := getJSON[response.ReportListResp](t, echo, deviceToken, "/api/v1/reports", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.Equal(t, "report-api.v1", recResp.SchemaVersion)
	require.NotEmpty(t, recResp.Items)
	require.Contains(t, recResp.Items[0].RawSuggestion, "\"kind\": \"llm-workflow-review\"")
	require.NotEmpty(t, recResp.Items[0].UserIntent)
	require.NotEmpty(t, recResp.Items[0].ModelInterpretation)

	syncedAt := time.Now().UTC()
	skillClientState := postJSON[response.SkillSetClientStateResp](t, echo, deviceToken, http.MethodPost, "/api/v1/skill-sets/client-state", request.SkillSetClientStateUpsertReq{
		ProjectID:      projectResp.ProjectID,
		BundleName:     "crux-personal-skillset",
		Mode:           "autopilot",
		SyncStatus:     "synced",
		AppliedVersion: "v-sync-1",
		AppliedHash:    "hash-sync-1",
		LastSyncedAt:   ptrTime(syncedAt),
	})
	require.Equal(t, projectResp.ProjectID, skillClientState.ProjectID)
	require.Equal(t, cliLoginResp.AgentID, skillClientState.AgentID)
	require.Equal(t, "synced", skillClientState.SyncStatus)

	postJSON[response.SkillSetClientStateResp](t, echo, deviceToken, http.MethodPost, "/api/v1/skill-sets/client-state", request.SkillSetClientStateUpsertReq{
		ProjectID:      projectResp.ProjectID,
		BundleName:     "crux-personal-skillset",
		Mode:           "autopilot",
		SyncStatus:     "synced",
		AppliedVersion: "v-sync-1",
		AppliedHash:    "hash-sync-1",
		LastSyncedAt:   ptrTime(syncedAt.Add(2 * time.Minute)),
	})

	skillSetResp := getJSON[response.SkillSetBundleResp](t, echo, deviceToken, "/api/v1/skill-sets/latest", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.Equal(t, "skill-set-bundle.v1", skillSetResp.SchemaVersion)
	require.Equal(t, "ready", skillSetResp.Status)
	require.Equal(t, "crux-personal-skillset", skillSetResp.BundleName)
	require.NotEmpty(t, skillSetResp.Version)
	require.NotEmpty(t, skillSetResp.CompiledHash)
	require.NotEmpty(t, skillSetResp.Files)
	require.True(t, slices.ContainsFunc(skillSetResp.Files, func(item response.SkillSetFileResp) bool {
		return strings.HasSuffix(item.Path, ".md")
	}))
	require.True(t, slices.ContainsFunc(skillSetResp.Files, func(item response.SkillSetFileResp) bool {
		return item.Path == "SKILL.md" &&
			strings.Contains(item.Content, "name: crux-personal-skillset") &&
			strings.Contains(item.Content, "Crux Personal Skill Set")
	}))
	require.True(t, slices.ContainsFunc(skillSetResp.Files, func(item response.SkillSetFileResp) bool {
		return item.Path == "agents/openai.yaml" &&
			strings.Contains(item.Content, "display_name: \"Crux Personal Skill Set\"")
	}))
	require.True(t, slices.ContainsFunc(skillSetResp.Files, func(item response.SkillSetFileResp) bool {
		return item.Path == "00-manifest.json" && strings.Contains(item.Content, skillSetResp.Version)
	}))
	require.NotNil(t, skillSetResp.ClientState)
	require.Equal(t, "v-sync-1", skillSetResp.ClientState.AppliedVersion)
	require.Equal(t, "synced", skillSetResp.ClientState.SyncStatus)
	require.Len(t, skillSetResp.DeploymentHistory, 1)
	require.Equal(t, "skillset_deployed", skillSetResp.DeploymentHistory[0].EventType)
	require.Equal(t, "v-sync-1", skillSetResp.DeploymentHistory[0].AppliedVersion)
	require.Contains(t, skillSetResp.DeploymentHistory[0].Summary, "Deployed")
	require.Len(t, skillSetResp.VersionHistory, 1)
	require.Equal(t, skillSetResp.Version, skillSetResp.VersionHistory[0].Version)
	require.Equal(t, skillSetResp.CompiledHash, skillSetResp.VersionHistory[0].CompiledHash)
	require.Equal(t, "shadow", skillSetResp.VersionHistory[0].DeploymentDecision)
	require.NotNil(t, skillSetResp.VersionHistory[0].ShadowEvaluation)
	require.Equal(t, "passed", skillSetResp.VersionHistory[0].ShadowEvaluation.Guardrail)
	require.Greater(t, skillSetResp.VersionHistory[0].ShadowEvaluation.Score, 0.0)
	require.GreaterOrEqual(t, skillSetResp.VersionHistory[0].ShadowEvaluation.AverageConfidence, 0.0)
	require.Greater(t, skillSetResp.VersionHistory[0].ShadowEvaluation.ChangedDocumentCount, 0)
	require.Nil(t, skillSetResp.LatestDiff)

	postJSON[response.SkillSetClientStateResp](t, echo, deviceToken, http.MethodPost, "/api/v1/skill-sets/client-state", request.SkillSetClientStateUpsertReq{
		ProjectID:      projectResp.ProjectID,
		BundleName:     "crux-personal-skillset",
		Mode:           "autopilot",
		SyncStatus:     "synced",
		AppliedVersion: skillSetResp.Version,
		AppliedHash:    skillSetResp.CompiledHash,
		LastSyncedAt:   ptrTime(syncedAt.Add(4 * time.Minute)),
	})

	skillSetResp = getJSON[response.SkillSetBundleResp](t, echo, deviceToken, "/api/v1/skill-sets/latest", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.Len(t, skillSetResp.VersionHistory, 1)
	require.Equal(t, "deployed", skillSetResp.VersionHistory[0].DeploymentDecision)
	require.Contains(t, skillSetResp.VersionHistory[0].DecisionReason, "connected CLI applied")
	require.NotNil(t, skillSetResp.VersionHistory[0].ShadowEvaluation)

	postApplySession := postJSON[response.SessionIngestResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-summaries", request.SessionSummaryReq{
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
	waitForDashboardResearchStatus(t, echo, deviceToken, cliLoginResp.OrgID, func(item *response.ReportResearchStatusResp) bool {
		return item != nil && item.State == "succeeded"
	})

	recRespAfterSecondSession := getJSON[response.ReportListResp](t, echo, deviceToken, "/api/v1/reports", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.NotEmpty(t, recRespAfterSecondSession.Items)
	require.NotEmpty(t, recRespAfterSecondSession.Items[0].Summary)

	overviewResp := getJSON[response.DashboardOverviewResp](t, echo, deviceToken, "/api/v1/dashboard/overview", url.Values{
		"org_id": []string{cliLoginResp.OrgID},
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

	insightsResp := getJSON[response.DashboardProjectInsightsResp](t, echo, deviceToken, "/api/v1/dashboard/project-insights", url.Values{
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

	auditResp := getJSON[response.AuditListResp](t, echo, deviceToken, "/api/v1/audits", url.Values{
		"org_id": []string{cliLoginResp.OrgID},
	})
	require.NotEmpty(t, auditResp.Items)
}

func TestAnalyticsRouteBatchSessionIngestSupportsPartialFailures(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")
	closeGoogle := configureGoogleAuthControllerTest(t, conf, googleAuthTestUser{
		Code:     "batch-partial-login",
		Subject:  "google-batch-partial-subject",
		Email:    "demo@example.com",
		Name:     "Demo Operator",
		Verified: true,
	})
	defer closeGoogle()

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

	cliLoginResp := loginCLICollectorForRouteTest(t, echo, "batch-partial-login", "batch-partial-device")
	deviceToken := cliLoginResp.AccessToken

	projectResp := postJSON[response.ProjectRegistrationResp](t, echo, deviceToken, http.MethodPost, "/api/v1/projects/register", request.RegisterProjectReq{
		OrgID:       cliLoginResp.OrgID,
		AgentID:     cliLoginResp.AgentID,
		Name:        "batch-partial-project",
		RepoHash:    "batch-partial-project-hash",
		DefaultTool: "codex",
	})

	batchResp := postJSON[response.SessionBatchIngestResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-summaries/batch", request.SessionSummaryBatchReq{
		ProjectID: projectResp.ProjectID,
		Sessions: []request.SessionSummaryReq{
			{
				Tool: "codex",
				RawQueries: []string{
					"Upload the valid batch session first.",
				},
				Timestamp: time.Now().UTC(),
			},
			{
				RawQueries: []string{
					"This item is missing the required tool field.",
				},
				Timestamp: time.Now().UTC().Add(time.Minute),
			},
		},
	})

	require.Equal(t, "report-api.v1", batchResp.SchemaVersion)
	require.Equal(t, projectResp.ProjectID, batchResp.ProjectID)
	require.Equal(t, 2, batchResp.Accepted)
	require.Equal(t, 1, batchResp.Uploaded)
	require.Equal(t, 0, batchResp.Updated)
	require.Equal(t, 1, batchResp.Failed)
	require.Len(t, batchResp.Items, 2)
	require.Equal(t, "uploaded", batchResp.Items[0].Status)
	require.NotNil(t, batchResp.Items[0].RecordedAt)
	require.Equal(t, "failed", batchResp.Items[1].Status)
	require.Equal(t, http.StatusBadRequest, batchResp.Items[1].HTTPStatus)
	require.Equal(t, 1000, batchResp.Items[1].APIErrorCode)
	require.Contains(t, batchResp.Items[1].Error, "tool is required")

	sessionList := getJSON[response.SessionSummaryListResp](t, echo, deviceToken, "/api/v1/session-summaries", url.Values{
		"project_id": []string{projectResp.ProjectID},
		"limit":      []string{"5"},
	})
	require.Len(t, sessionList.Items, 1)
}

func TestAnalyticsRouteRefreshesReportsEveryBatchOfSessions(t *testing.T) {
	conf := newRouteResearchConfig(t)
	conf.App.APIToken = "route-token"
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")
	closeGoogle := configureGoogleAuthControllerTest(t, conf, googleAuthTestUser{
		Code:     "batch-login",
		Subject:  "google-batch-subject",
		Email:    "demo@example.com",
		Name:     "Demo Operator",
		Verified: true,
	})
	defer closeGoogle()

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

	cliLoginResp := loginCLICollectorForRouteTest(t, echo, "batch-login", "batch-device")
	deviceToken := cliLoginResp.AccessToken

	projectResp := postJSON[response.ProjectRegistrationResp](t, echo, deviceToken, http.MethodPost, "/api/v1/projects/register", request.RegisterProjectReq{
		OrgID:       cliLoginResp.OrgID,
		AgentID:     cliLoginResp.AgentID,
		Name:        "batch-project",
		RepoHash:    "batch-project-hash",
		DefaultTool: "codex",
	})

	baseTime := time.Now().UTC().Round(time.Second)
	for i := 1; i <= 9; i++ {
		resp := postJSON[response.SessionIngestResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-summaries", request.SessionSummaryReq{
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

	tenthResp := postJSON[response.SessionIngestResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-summaries", request.SessionSummaryReq{
		ProjectID: projectResp.ProjectID,
		Tool:      "codex",
		RawQueries: []string{
			"Generate the first report now that ten sessions are available.",
		},
		Timestamp: baseTime.Add(10 * time.Minute),
	})
	require.NotNil(t, tenthResp.ResearchStatus)
	require.Equal(t, "running", tenthResp.ResearchStatus.State)

	statusAfterTen := waitForDashboardResearchStatus(t, echo, deviceToken, cliLoginResp.OrgID, func(item *response.ReportResearchStatusResp) bool {
		return item != nil && item.State == "succeeded" && item.SessionCount == 10
	})
	require.Equal(t, 1, statusAfterTen.ReportCount)

	reportsAfterTen := getJSON[response.ReportListResp](t, echo, deviceToken, "/api/v1/reports", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.Len(t, reportsAfterTen.Items, 1)
	firstReportID := reportsAfterTen.Items[0].ID

	eleventhResp := postJSON[response.SessionIngestResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-summaries", request.SessionSummaryReq{
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

	reportsAfterEleven := getJSON[response.ReportListResp](t, echo, deviceToken, "/api/v1/reports", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.Len(t, reportsAfterEleven.Items, 1)
	require.Equal(t, firstReportID, reportsAfterEleven.Items[0].ID)

	for i := 12; i <= 19; i++ {
		resp := postJSON[response.SessionIngestResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-summaries", request.SessionSummaryReq{
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

	twentiethResp := postJSON[response.SessionIngestResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-summaries", request.SessionSummaryReq{
		ProjectID: projectResp.ProjectID,
		Tool:      "codex",
		RawQueries: []string{
			"Refresh the report now that the second batch is complete.",
		},
		Timestamp: baseTime.Add(20 * time.Minute),
	})
	require.NotNil(t, twentiethResp.ResearchStatus)
	require.Equal(t, "running", twentiethResp.ResearchStatus.State)

	statusAfterTwenty := waitForDashboardResearchStatus(t, echo, deviceToken, cliLoginResp.OrgID, func(item *response.ReportResearchStatusResp) bool {
		return item != nil && item.State == "succeeded" && item.SessionCount == 20
	})
	require.Equal(t, 1, statusAfterTwenty.ReportCount)

	reportsAfterTwenty := getJSON[response.ReportListResp](t, echo, deviceToken, "/api/v1/reports", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.Len(t, reportsAfterTwenty.Items, 2)
	require.NotEqual(t, firstReportID, reportsAfterTwenty.Items[0].ID)
	require.Equal(t, "active", reportsAfterTwenty.Items[0].Status)
	require.Equal(t, firstReportID, reportsAfterTwenty.Items[1].ID)
	require.Equal(t, "superseded", reportsAfterTwenty.Items[1].Status)
}

func TestAnalyticsRouteBatchSessionIngestRefreshesWhenCrossingThreshold(t *testing.T) {
	conf := newRouteResearchConfig(t)
	conf.App.APIToken = "route-token"
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")
	closeGoogle := configureGoogleAuthControllerTest(t, conf, googleAuthTestUser{
		Code:     "batch-threshold-login",
		Subject:  "google-batch-threshold-subject",
		Email:    "demo@example.com",
		Name:     "Demo Operator",
		Verified: true,
	})
	defer closeGoogle()

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

	cliLoginResp := loginCLICollectorForRouteTest(t, echo, "batch-threshold-login", "batch-threshold-device")
	deviceToken := cliLoginResp.AccessToken

	projectResp := postJSON[response.ProjectRegistrationResp](t, echo, deviceToken, http.MethodPost, "/api/v1/projects/register", request.RegisterProjectReq{
		OrgID:       cliLoginResp.OrgID,
		AgentID:     cliLoginResp.AgentID,
		Name:        "batch-threshold-project",
		RepoHash:    "batch-threshold-project-hash",
		DefaultTool: "codex",
	})

	baseTime := time.Now().UTC().Round(time.Second)
	for i := 1; i <= 9; i++ {
		resp := postJSON[response.SessionIngestResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-summaries", request.SessionSummaryReq{
			ProjectID: projectResp.ProjectID,
			Tool:      "codex",
			RawQueries: []string{
				"Accumulate sessions until the batch crosses the refresh threshold.",
			},
			Timestamp: baseTime.Add(time.Duration(i) * time.Minute),
		})
		require.NotNil(t, resp.ResearchStatus)
		require.Equal(t, "waiting_for_min_sessions", resp.ResearchStatus.State)
	}

	batchResp := postJSON[response.SessionBatchIngestResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-summaries/batch", request.SessionSummaryBatchReq{
		ProjectID: projectResp.ProjectID,
		Sessions: []request.SessionSummaryReq{
			{
				Tool: "codex",
				RawQueries: []string{
					"Cross the first report threshold in a single batch upload.",
				},
				Timestamp: baseTime.Add(10 * time.Minute),
			},
			{
				Tool: "codex",
				RawQueries: []string{
					"Keep the refresh running even though the batch ends above the threshold.",
				},
				Timestamp: baseTime.Add(11 * time.Minute),
			},
		},
	})

	require.Equal(t, 2, batchResp.Uploaded)
	require.Zero(t, batchResp.Failed)
	require.Len(t, batchResp.Items, 2)
	require.NotNil(t, batchResp.ResearchStatus)
	require.Equal(t, "running", batchResp.ResearchStatus.State)
	require.Equal(t, 11, batchResp.ResearchStatus.SessionCount)

	status := waitForDashboardResearchStatus(t, echo, deviceToken, cliLoginResp.OrgID, func(item *response.ReportResearchStatusResp) bool {
		return item != nil && item.State == "succeeded" && item.SessionCount == 11
	})
	require.Equal(t, 1, status.ReportCount)
}

func TestAnalyticsRouteSessionImportJobRefreshesWhenCrossingThreshold(t *testing.T) {
	conf := newRouteResearchConfig(t)
	conf.App.APIToken = "route-token"
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")
	closeGoogle := configureGoogleAuthControllerTest(t, conf, googleAuthTestUser{
		Code:     "import-job-login",
		Subject:  "google-import-job-subject",
		Email:    "demo@example.com",
		Name:     "Demo Operator",
		Verified: true,
	})
	defer closeGoogle()

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

	cliLoginResp := loginCLICollectorForRouteTest(t, echo, "import-job-login", "import-job-device")
	deviceToken := cliLoginResp.AccessToken

	projectResp := postJSON[response.ProjectRegistrationResp](t, echo, deviceToken, http.MethodPost, "/api/v1/projects/register", request.RegisterProjectReq{
		OrgID:       cliLoginResp.OrgID,
		AgentID:     cliLoginResp.AgentID,
		Name:        "import-job-project",
		RepoHash:    "import-job-project-hash",
		DefaultTool: "codex",
	})

	baseTime := time.Now().UTC().Round(time.Second)
	for i := 1; i <= 9; i++ {
		resp := postJSON[response.SessionIngestResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-summaries", request.SessionSummaryReq{
			ProjectID: projectResp.ProjectID,
			Tool:      "codex",
			RawQueries: []string{
				"Collect enough sessions so the async import job crosses the first report threshold.",
			},
			Timestamp: baseTime.Add(time.Duration(i) * time.Minute),
		})
		require.Equal(t, "waiting_for_min_sessions", resp.ResearchStatus.State)
	}

	job := postJSON[response.SessionImportJobResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-import-jobs", request.SessionImportJobCreateReq{
		ProjectID: projectResp.ProjectID,
	})
	require.Equal(t, "receiving_chunks", job.Status)

	job = postJSON[response.SessionImportJobResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-import-jobs/"+job.JobID+"/chunks", request.SessionImportJobChunkReq{
		Sessions: []request.SessionSummaryReq{
			{
				Tool: "codex",
				RawQueries: []string{
					"Cross the refresh threshold through the async import job.",
				},
				Timestamp: baseTime.Add(10 * time.Minute),
			},
			{
				Tool: "codex",
				RawQueries: []string{
					"Keep processing inside the same async import job after the threshold.",
				},
				Timestamp: baseTime.Add(11 * time.Minute),
			},
		},
	})
	require.Equal(t, 2, job.ReceivedSessions)

	job = postJSON[response.SessionImportJobResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-import-jobs/"+job.JobID+"/complete", request.SessionImportJobCompleteReq{})
	require.Equal(t, "queued", job.Status)

	completed := waitForSessionImportJobStatus(t, echo, deviceToken, job.JobID, func(item *response.SessionImportJobResp) bool {
		return item != nil && item.Status == "succeeded" && item.ProcessedSessions == 2
	})
	require.Equal(t, 2, completed.UploadedSessions)
	require.Equal(t, 0, completed.FailedSessions)
	require.NotNil(t, completed.StartedAt)
	require.NotNil(t, completed.CompletedAt)

	status := waitForDashboardResearchStatus(t, echo, deviceToken, cliLoginResp.OrgID, func(item *response.ReportResearchStatusResp) bool {
		return item != nil && item.State == "succeeded" && item.SessionCount == 11
	})
	require.Equal(t, 1, status.ReportCount)
}

func TestAnalyticsRouteSessionImportJobTracksPartialFailures(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")
	closeGoogle := configureGoogleAuthControllerTest(t, conf, googleAuthTestUser{
		Code:     "import-job-partial-login",
		Subject:  "google-import-job-partial-subject",
		Email:    "demo@example.com",
		Name:     "Demo Operator",
		Verified: true,
	})
	defer closeGoogle()

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

	cliLoginResp := loginCLICollectorForRouteTest(t, echo, "import-job-partial-login", "import-job-partial-device")
	deviceToken := cliLoginResp.AccessToken

	projectResp := postJSON[response.ProjectRegistrationResp](t, echo, deviceToken, http.MethodPost, "/api/v1/projects/register", request.RegisterProjectReq{
		OrgID:       cliLoginResp.OrgID,
		AgentID:     cliLoginResp.AgentID,
		Name:        "import-job-partial-project",
		RepoHash:    "import-job-partial-project-hash",
		DefaultTool: "codex",
	})

	job := postJSON[response.SessionImportJobResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-import-jobs", request.SessionImportJobCreateReq{
		ProjectID: projectResp.ProjectID,
	})
	job = postJSON[response.SessionImportJobResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-import-jobs/"+job.JobID+"/chunks", request.SessionImportJobChunkReq{
		Sessions: []request.SessionSummaryReq{
			{
				Tool: "codex",
				RawQueries: []string{
					"Store the valid session through the async import job.",
				},
				Timestamp: time.Now().UTC(),
			},
			{
				RawQueries: []string{
					"This async import item is missing the required tool.",
				},
				Timestamp: time.Now().UTC().Add(time.Minute),
			},
		},
	})
	require.Equal(t, 2, job.ReceivedSessions)

	job = postJSON[response.SessionImportJobResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-import-jobs/"+job.JobID+"/complete", request.SessionImportJobCompleteReq{})
	require.Equal(t, "queued", job.Status)

	completed := waitForSessionImportJobStatus(t, echo, deviceToken, job.JobID, func(item *response.SessionImportJobResp) bool {
		return item != nil && item.Status == "partially_failed"
	})
	require.Equal(t, 1, completed.UploadedSessions)
	require.Equal(t, 1, completed.FailedSessions)
	require.Len(t, completed.Failures, 1)
	require.Equal(t, 1000, completed.Failures[0].APIErrorCode)
	require.Equal(t, http.StatusBadRequest, completed.Failures[0].HTTPStatus)
	require.Contains(t, completed.Failures[0].Error, "tool is required")

	sessionList := getJSON[response.SessionSummaryListResp](t, echo, deviceToken, "/api/v1/session-summaries", url.Values{
		"project_id": []string{projectResp.ProjectID},
		"limit":      []string{"5"},
	})
	require.Len(t, sessionList.Items, 1)
}

func TestAnalyticsRouteSessionImportJobCreateReusesActiveJob(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")
	closeGoogle := configureGoogleAuthControllerTest(t, conf, googleAuthTestUser{
		Code:     "import-job-reuse-login",
		Subject:  "google-import-job-reuse-subject",
		Email:    "demo@example.com",
		Name:     "Demo Operator",
		Verified: true,
	})
	defer closeGoogle()

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

	cliLoginResp := loginCLICollectorForRouteTest(t, echo, "import-job-reuse-login", "import-job-reuse-device")
	deviceToken := cliLoginResp.AccessToken

	projectResp := postJSON[response.ProjectRegistrationResp](t, echo, deviceToken, http.MethodPost, "/api/v1/projects/register", request.RegisterProjectReq{
		OrgID:       cliLoginResp.OrgID,
		AgentID:     cliLoginResp.AgentID,
		Name:        "import-job-reuse-project",
		RepoHash:    "import-job-reuse-project-hash",
		DefaultTool: "codex",
	})

	firstJob := postJSON[response.SessionImportJobResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-import-jobs", request.SessionImportJobCreateReq{
		ProjectID:     projectResp.ProjectID,
		TotalSessions: 2,
	})
	require.False(t, firstJob.Reused)
	require.Equal(t, 2, firstJob.TotalSessions)

	staged := postJSON[response.SessionImportJobResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-import-jobs/"+firstJob.JobID+"/chunks", request.SessionImportJobChunkReq{
		Sessions: []request.SessionSummaryReq{{
			Tool: "codex",
			RawQueries: []string{
				"Stage the first session so the reused create call can see existing progress.",
			},
			Timestamp: time.Now().UTC(),
		}},
	})
	require.Equal(t, 1, staged.ReceivedSessions)

	reusedJob := postJSON[response.SessionImportJobResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-import-jobs", request.SessionImportJobCreateReq{
		ProjectID:     projectResp.ProjectID,
		TotalSessions: 3,
	})
	require.True(t, reusedJob.Reused)
	require.Equal(t, firstJob.JobID, reusedJob.JobID)
	require.Equal(t, 1, reusedJob.ReceivedSessions)
	require.Equal(t, 3, reusedJob.TotalSessions)

	overview := getJSON[response.DashboardOverviewResp](t, echo, deviceToken, "/api/v1/dashboard/overview", url.Values{
		"org_id": []string{cliLoginResp.OrgID},
	})
	require.NotNil(t, overview.ActiveImportJob)
	require.Equal(t, firstJob.JobID, overview.ActiveImportJob.JobID)
	require.Equal(t, "receiving_chunks", overview.ActiveImportJob.Status)
	require.Equal(t, 1, overview.ActiveImportJob.ReceivedSessions)
}

func TestAnalyticsRouteSessionImportJobCancel(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")
	closeGoogle := configureGoogleAuthControllerTest(t, conf, googleAuthTestUser{
		Code:     "import-job-cancel-login",
		Subject:  "google-import-job-cancel-subject",
		Email:    "demo@example.com",
		Name:     "Demo Operator",
		Verified: true,
	})
	defer closeGoogle()

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

	cliLoginResp := loginCLICollectorForRouteTest(t, echo, "import-job-cancel-login", "import-job-cancel-device")
	deviceToken := cliLoginResp.AccessToken

	projectResp := postJSON[response.ProjectRegistrationResp](t, echo, deviceToken, http.MethodPost, "/api/v1/projects/register", request.RegisterProjectReq{
		OrgID:       cliLoginResp.OrgID,
		AgentID:     cliLoginResp.AgentID,
		Name:        "import-job-cancel-project",
		RepoHash:    "import-job-cancel-project-hash",
		DefaultTool: "codex",
	})

	job := postJSON[response.SessionImportJobResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-import-jobs", request.SessionImportJobCreateReq{
		ProjectID:     projectResp.ProjectID,
		TotalSessions: 2,
	})
	require.Equal(t, "receiving_chunks", job.Status)

	job = postJSON[response.SessionImportJobResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-import-jobs/"+job.JobID+"/chunks", request.SessionImportJobChunkReq{
		Sessions: []request.SessionSummaryReq{{
			Tool: "codex",
			RawQueries: []string{
				"Stage a session before canceling the import job.",
			},
			Timestamp: time.Now().UTC(),
		}},
	})
	require.Equal(t, 1, job.ReceivedSessions)

	canceled := postJSON[response.SessionImportJobResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-import-jobs/"+job.JobID+"/cancel", request.SessionImportJobCancelReq{})
	require.Equal(t, "canceled", canceled.Status)
	require.NotNil(t, canceled.CompletedAt)
	require.Contains(t, canceled.LastError, "canceled")

	fetched := getJSON[response.SessionImportJobResp](t, echo, deviceToken, "/api/v1/session-import-jobs/"+job.JobID, nil)
	require.Equal(t, "canceled", fetched.Status)
	require.Equal(t, 1, fetched.ReceivedSessions)

	replacement := postJSON[response.SessionImportJobResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-import-jobs", request.SessionImportJobCreateReq{
		ProjectID: projectResp.ProjectID,
	})
	require.NotEqual(t, job.JobID, replacement.JobID)
	require.False(t, replacement.Reused)
}

func TestAnalyticsRouteDashboardOverviewIncludesImportJobMetrics(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")
	closeGoogle := configureGoogleAuthControllerTest(t, conf, googleAuthTestUser{
		Code:     "import-job-metrics-login",
		Subject:  "google-import-job-metrics-subject",
		Email:    "demo@example.com",
		Name:     "Demo Operator",
		Verified: true,
	})
	defer closeGoogle()

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

	cliLoginResp := loginCLICollectorForRouteTest(t, echo, "import-job-metrics-login", "import-job-metrics-device")
	deviceToken := cliLoginResp.AccessToken

	projectResp := postJSON[response.ProjectRegistrationResp](t, echo, deviceToken, http.MethodPost, "/api/v1/projects/register", request.RegisterProjectReq{
		OrgID:       cliLoginResp.OrgID,
		AgentID:     cliLoginResp.AgentID,
		Name:        "import-job-metrics-project",
		RepoHash:    "import-job-metrics-project-hash",
		DefaultTool: "codex",
	})

	completedJob := postJSON[response.SessionImportJobResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-import-jobs", request.SessionImportJobCreateReq{
		ProjectID: projectResp.ProjectID,
	})
	completedJob = postJSON[response.SessionImportJobResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-import-jobs/"+completedJob.JobID+"/chunks", request.SessionImportJobChunkReq{
		Sessions: []request.SessionSummaryReq{{
			Tool: "codex",
			RawQueries: []string{
				"Populate dashboard import metrics with a completed import job.",
			},
			Timestamp: time.Now().UTC(),
		}},
	})
	require.Equal(t, 1, completedJob.ReceivedSessions)

	completedJob = postJSON[response.SessionImportJobResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-import-jobs/"+completedJob.JobID+"/complete", request.SessionImportJobCompleteReq{})
	require.Equal(t, "queued", completedJob.Status)

	waitForSessionImportJobStatus(t, echo, deviceToken, completedJob.JobID, func(item *response.SessionImportJobResp) bool {
		return item != nil && item.Status == "succeeded" && item.ProcessedSessions == 1
	})

	activeJob := postJSON[response.SessionImportJobResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-import-jobs", request.SessionImportJobCreateReq{
		ProjectID:     projectResp.ProjectID,
		TotalSessions: 2,
	})
	require.Equal(t, "receiving_chunks", activeJob.Status)

	overview := getJSON[response.DashboardOverviewResp](t, echo, deviceToken, "/api/v1/dashboard/overview", url.Values{
		"org_id": []string{cliLoginResp.OrgID},
	})
	require.NotNil(t, overview.ActiveImportJob)
	require.Equal(t, activeJob.JobID, overview.ActiveImportJob.JobID)
	require.Equal(t, "receiving_chunks", overview.ActiveImportJob.Status)
	require.NotNil(t, overview.ImportJobMetrics)
	require.Equal(t, 2, overview.ImportJobMetrics.CreatedJobs)
	require.Equal(t, 1, overview.ImportJobMetrics.ReceivingJobs)
	require.Equal(t, 1, overview.ImportJobMetrics.SucceededJobs)
	require.Equal(t, 1, overview.ImportJobMetrics.ProcessedSessions)
	require.Equal(t, 1, overview.ImportJobMetrics.UploadedSessions)
	require.Zero(t, overview.ImportJobMetrics.FailedSessions)
	require.NotNil(t, overview.ImportJobMetrics.LastCompletedAt)
}

func TestAnalyticsRouteAdminImportJobMetrics(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")
	closeGoogle := configureGoogleAuthControllerTest(t, conf, googleAuthTestUser{
		Code:     "admin-import-job-metrics-login",
		Subject:  "google-admin-import-job-metrics-subject",
		Email:    "demo@example.com",
		Name:     "Demo Operator",
		Verified: true,
	})
	defer closeGoogle()

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

	adminCookie := loginWithGoogleControllerTest(t, echo, "admin-import-job-metrics-login")
	cliLoginResp := loginCLICollectorForRouteTest(t, echo, "admin-import-job-metrics-login", "admin-import-job-metrics-device")
	deviceToken := cliLoginResp.AccessToken

	projectResp := postJSON[response.ProjectRegistrationResp](t, echo, deviceToken, http.MethodPost, "/api/v1/projects/register", request.RegisterProjectReq{
		OrgID:       cliLoginResp.OrgID,
		AgentID:     cliLoginResp.AgentID,
		Name:        "admin-import-job-metrics-project",
		RepoHash:    "admin-import-job-metrics-project-hash",
		DefaultTool: "codex",
	})

	failedJob := postJSON[response.SessionImportJobResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-import-jobs", request.SessionImportJobCreateReq{
		ProjectID: projectResp.ProjectID,
	})
	failedJob = postJSON[response.SessionImportJobResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-import-jobs/"+failedJob.JobID+"/chunks", request.SessionImportJobChunkReq{
		Sessions: []request.SessionSummaryReq{
			{
				Tool: "codex",
				RawQueries: []string{
					"Create one valid import session for admin metrics.",
				},
				Timestamp: time.Now().UTC(),
			},
			{
				Tool:      "",
				Timestamp: time.Now().UTC(),
			},
		},
	})
	require.Equal(t, 2, failedJob.ReceivedSessions)

	failedJob = postJSON[response.SessionImportJobResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-import-jobs/"+failedJob.JobID+"/complete", request.SessionImportJobCompleteReq{})
	require.Equal(t, "queued", failedJob.Status)

	waitForSessionImportJobStatus(t, echo, deviceToken, failedJob.JobID, func(item *response.SessionImportJobResp) bool {
		return item != nil && item.Status == "partially_failed" && item.FailedSessions == 1
	})

	activeJob := postJSON[response.SessionImportJobResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-import-jobs", request.SessionImportJobCreateReq{
		ProjectID:     projectResp.ProjectID,
		TotalSessions: 3,
	})
	require.Equal(t, "receiving_chunks", activeJob.Status)

	adminMetrics := getJSON[response.AdminImportJobMetricsResp](t, echo, "", "/api/v1/admin/import-job-metrics", url.Values{
		"project_id": []string{projectResp.ProjectID},
		"limit":      []string{"5"},
	}, adminCookie)
	require.Equal(t, cliLoginResp.OrgID, adminMetrics.OrgID)
	require.Equal(t, projectResp.ProjectID, adminMetrics.ProjectID)
	require.NotNil(t, adminMetrics.Metrics)
	require.Equal(t, 2, adminMetrics.Metrics.CreatedJobs)
	require.Equal(t, 1, adminMetrics.Metrics.ReceivingJobs)
	require.Equal(t, 1, adminMetrics.Metrics.PartiallyFailedJobs)
	require.Equal(t, 2, adminMetrics.Metrics.ProcessedSessions)
	require.Equal(t, 1, adminMetrics.Metrics.UploadedSessions)
	require.Equal(t, 1, adminMetrics.Metrics.FailedSessions)
	require.Len(t, adminMetrics.ActiveJobs, 1)
	require.Equal(t, activeJob.JobID, adminMetrics.ActiveJobs[0].JobID)
	require.Len(t, adminMetrics.RecentFailures, 1)
	require.Equal(t, failedJob.JobID, adminMetrics.RecentFailures[0].JobID)
	require.Equal(t, "partially_failed", adminMetrics.RecentFailures[0].Status)
}

func TestAnalyticsRouteListSessionImportJobsFiltersStatus(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")
	closeGoogle := configureGoogleAuthControllerTest(t, conf, googleAuthTestUser{
		Code:     "import-job-list-login",
		Subject:  "google-import-job-list-subject",
		Email:    "demo@example.com",
		Name:     "Demo Operator",
		Verified: true,
	})
	defer closeGoogle()

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

	cliLoginResp := loginCLICollectorForRouteTest(t, echo, "import-job-list-login", "import-job-list-device")
	deviceToken := cliLoginResp.AccessToken

	projectResp := postJSON[response.ProjectRegistrationResp](t, echo, deviceToken, http.MethodPost, "/api/v1/projects/register", request.RegisterProjectReq{
		OrgID:       cliLoginResp.OrgID,
		AgentID:     cliLoginResp.AgentID,
		Name:        "import-job-list-project",
		RepoHash:    "import-job-list-project-hash",
		DefaultTool: "codex",
	})

	job := postJSON[response.SessionImportJobResp](t, echo, deviceToken, http.MethodPost, "/api/v1/session-import-jobs", request.SessionImportJobCreateReq{
		ProjectID:     projectResp.ProjectID,
		TotalSessions: 4,
	})
	require.Equal(t, "receiving_chunks", job.Status)

	listResp := getJSON[response.SessionImportJobListResp](t, echo, deviceToken, "/api/v1/session-import-jobs", url.Values{
		"project_id": []string{projectResp.ProjectID},
		"status":     []string{"receiving_chunks"},
		"limit":      []string{"5"},
	})
	require.Len(t, listResp.Items, 1)
	require.Equal(t, job.JobID, listResp.Items[0].JobID)
	require.Equal(t, "receiving_chunks", listResp.Items[0].Status)
	require.Equal(t, 4, listResp.Items[0].TotalSessions)
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

func TestAnalyticsRouteGoogleLoginAndCLITokenFlow(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")
	closeGoogle := configureGoogleAuthControllerTest(t, conf, googleAuthTestUser{
		Code:     "demo-login",
		Subject:  "google-demo-subject",
		Email:    "demo@example.com",
		Name:     "Demo Operator",
		Verified: true,
	})
	defer closeGoogle()

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

	sessionCookie := loginWithGoogleControllerTest(t, echo, "demo-login")

	sessionResp := getJSON[response.AuthSessionResp](t, echo, "", "/api/v1/auth/me", nil, sessionCookie)
	require.Equal(t, "demo@example.com", sessionResp.User.Email)
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
	require.Equal(t, service.TokenKindCLIEnrollment, tokenListResp.Items[0].Kind)
	require.Equal(t, "active", tokenListResp.Items[0].Status)

	cliLoginResp := postJSON[response.CLILoginResp](t, echo, cliTokenResp.Token, http.MethodPost, "/api/v1/auth/cli/login", request.CLILoginReq{
		DeviceName: "macbook-pro",
		Hostname:   "ci-mac.local",
		Platform:   "darwin/arm64",
		CLIVersion: "0.1.0-dev",
		Tools:      []string{"codex"},
	})
	require.Equal(t, "registered", cliLoginResp.Status)
	require.Equal(t, "Bearer", cliLoginResp.TokenType)
	require.NotEmpty(t, cliLoginResp.AccessToken)
	require.NotEmpty(t, cliLoginResp.RefreshToken)
	require.Equal(t, "demo-org", cliLoginResp.OrgID)
	require.Equal(t, "demo-user", cliLoginResp.UserID)
	require.Equal(t, "admin", cliLoginResp.UserRole)
	require.Equal(t, "active", cliLoginResp.UserStatus)

	tokenListResp = getJSON[response.CLITokenListResp](t, echo, "", "/api/v1/auth/cli-tokens", nil, sessionCookie)
	require.Len(t, tokenListResp.Items, 3)
	enrollmentToken := requireTokenByKind(t, tokenListResp.Items, service.TokenKindCLIEnrollment)
	require.Equal(t, "consumed", enrollmentToken.Status)
	require.NotNil(t, enrollmentToken.LastUsedAt)
	require.NotNil(t, enrollmentToken.ConsumedAt)
	require.Equal(t, "active", requireTokenByKind(t, tokenListResp.Items, service.TokenKindDeviceAccess).Status)
	require.Equal(t, "active", requireTokenByKind(t, tokenListResp.Items, service.TokenKindDeviceRefresh).Status)

	revokeResp := postJSON[response.CLITokenRevokeResp](t, echo, "", http.MethodPost, "/api/v1/auth/cli-tokens/revoke", request.RevokeCLITokenReq{
		TokenID: cliTokenResp.TokenID,
	}, sessionCookie)
	require.Equal(t, "revoked", revokeResp.Status)

	revokedTokenList := getJSON[response.CLITokenListResp](t, echo, "", "/api/v1/auth/cli-tokens", nil, sessionCookie)
	require.Len(t, revokedTokenList.Items, 3)
	for _, item := range revokedTokenList.Items {
		require.Equal(t, "revoked", item.Status)
	}

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

func TestAnalyticsRouteCLITokenFlowWithStaticLocalToken(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")
	conf.App.APIToken = "route-token"
	conf.Auth.AllowDemoUser = true
	conf.Auth.StaticTokenEnabled = true

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

	cliTokenResp := postJSON[response.CLITokenIssueResp](t, echo, "route-token", http.MethodPost, "/api/v1/auth/cli-tokens", request.IssueCLITokenReq{
		Label: "CI Mac",
	})
	require.NotEmpty(t, cliTokenResp.Token)
	require.Equal(t, "CI Mac", cliTokenResp.Label)

	tokenListResp := getJSON[response.CLITokenListResp](t, echo, "route-token", "/api/v1/auth/cli-tokens", nil)
	require.Len(t, tokenListResp.Items, 1)
	require.Equal(t, service.TokenKindCLIEnrollment, tokenListResp.Items[0].Kind)
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
	require.Equal(t, "demo@example.com", cliLoginResp.UserEmail)

	revokeResp := postJSON[response.CLITokenRevokeResp](t, echo, "route-token", http.MethodPost, "/api/v1/auth/cli-tokens/revoke", request.RevokeCLITokenReq{
		TokenID: cliTokenResp.TokenID,
	})
	require.Equal(t, "revoked", revokeResp.Status)

	revokedTokenList := getJSON[response.CLITokenListResp](t, echo, "route-token", "/api/v1/auth/cli-tokens", nil)
	require.Len(t, revokedTokenList.Items, 3)
	for _, item := range revokedTokenList.Items {
		require.Equal(t, "revoked", item.Status)
	}
}

func TestAnalyticsRouteCLIRefreshRotatesDeviceTokens(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")
	closeGoogle := configureGoogleAuthControllerTest(t, conf, googleAuthTestUser{
		Code:     "demo-login",
		Subject:  "google-demo-subject",
		Email:    "demo@example.com",
		Name:     "Demo Operator",
		Verified: true,
	})
	defer closeGoogle()

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

	sessionCookie := loginWithGoogleControllerTest(t, echo, "demo-login")

	cliTokenResp := postJSON[response.CLITokenIssueResp](t, echo, "", http.MethodPost, "/api/v1/auth/cli-tokens", request.IssueCLITokenReq{
		Label: "Refreshable device",
	}, sessionCookie)

	cliLoginResp := postJSON[response.CLILoginResp](t, echo, cliTokenResp.Token, http.MethodPost, "/api/v1/auth/cli/login", request.CLILoginReq{
		DeviceName: "refresh-macbook",
		Hostname:   "refresh.local",
		Platform:   "darwin/arm64",
		CLIVersion: "0.1.0-dev",
		Tools:      []string{"codex"},
	})

	refreshResp := postJSON[response.CLIRefreshResp](t, echo, "", http.MethodPost, "/api/v1/auth/cli/refresh", request.CLIRefreshReq{
		RefreshToken: cliLoginResp.RefreshToken,
	})
	require.Equal(t, "Bearer", refreshResp.TokenType)
	require.NotEmpty(t, refreshResp.AccessToken)
	require.NotEmpty(t, refreshResp.RefreshToken)
	require.NotEqual(t, cliLoginResp.AccessToken, refreshResp.AccessToken)
	require.NotEqual(t, cliLoginResp.RefreshToken, refreshResp.RefreshToken)
	require.Equal(t, cliLoginResp.AgentID, refreshResp.AgentID)

	tokenListResp := getJSON[response.CLITokenListResp](t, echo, "", "/api/v1/auth/cli-tokens", nil, sessionCookie)
	require.Len(t, tokenListResp.Items, 5)

	activeAccess := 0
	activeRefresh := 0
	for _, item := range tokenListResp.Items {
		if item.Kind == service.TokenKindDeviceAccess && item.Status == "active" {
			activeAccess++
		}
		if item.Kind == service.TokenKindDeviceRefresh && item.Status == "active" {
			activeRefresh++
		}
	}
	require.Equal(t, 1, activeAccess)
	require.Equal(t, 1, activeRefresh)

	body, err := json.Marshal(request.CLIRefreshReq{
		RefreshToken: cliLoginResp.RefreshToken,
	})
	require.NoError(t, err)

	duplicateRefreshReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/cli/refresh", bytes.NewReader(body))
	duplicateRefreshReq = duplicateRefreshReq.WithContext(context.Background())
	duplicateRefreshReq.Header.Set("Content-Type", "application/json")
	duplicateRefreshReq.Header.Set("User-Agent", "route-test-client")
	duplicateRefreshRec := httptest.NewRecorder()
	echo.ServeHTTP(duplicateRefreshRec, duplicateRefreshReq)
	require.Equal(t, http.StatusUnauthorized, duplicateRefreshRec.Code)
}

func TestAnalyticsRouteRejectsAgentBindingMismatch(t *testing.T) {
	conf := newRouteResearchConfig(t)
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")
	closeGoogle := configureGoogleAuthControllerTest(t, conf, googleAuthTestUser{
		Code:     "binding-login",
		Subject:  "google-binding-subject",
		Email:    "demo@example.com",
		Name:     "Demo Operator",
		Verified: true,
	})
	defer closeGoogle()

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

	sessionCookie := loginWithGoogleControllerTest(t, echo, "binding-login")
	firstTokenResp := postJSON[response.CLITokenIssueResp](t, echo, "", http.MethodPost, "/api/v1/auth/cli-tokens", request.IssueCLITokenReq{
		Label: "binding-device-1",
	}, sessionCookie)
	firstLoginResp := postJSON[response.CLILoginResp](t, echo, firstTokenResp.Token, http.MethodPost, "/api/v1/auth/cli/login", request.CLILoginReq{
		DeviceName: "binding-device-1",
		Hostname:   "binding-1.local",
		Platform:   "darwin/arm64",
		CLIVersion: "0.1.0-dev",
		Tools:      []string{"codex"},
	})

	secondTokenResp := postJSON[response.CLITokenIssueResp](t, echo, "", http.MethodPost, "/api/v1/auth/cli-tokens", request.IssueCLITokenReq{
		Label: "binding-device-2",
	}, sessionCookie)
	secondLoginResp := postJSON[response.CLILoginResp](t, echo, secondTokenResp.Token, http.MethodPost, "/api/v1/auth/cli/login", request.CLILoginReq{
		DeviceName: "binding-device-2",
		Hostname:   "binding-2.local",
		Platform:   "darwin/arm64",
		CLIVersion: "0.1.0-dev",
		Tools:      []string{"codex"},
	})

	projectResp := postJSON[response.ProjectRegistrationResp](t, echo, firstLoginResp.AccessToken, http.MethodPost, "/api/v1/projects/register", request.RegisterProjectReq{
		OrgID:       firstLoginResp.OrgID,
		AgentID:     firstLoginResp.AgentID,
		Name:        "binding-project",
		RepoHash:    "binding-project-hash",
		DefaultTool: "codex",
	})

	mismatchedProjectRec := postJSONExpectCode(t, echo, firstLoginResp.AccessToken, http.MethodPost, "/api/v1/projects/register", request.RegisterProjectReq{
		OrgID:       firstLoginResp.OrgID,
		AgentID:     secondLoginResp.AgentID,
		Name:        "binding-project",
		RepoHash:    "binding-project-hash",
		DefaultTool: "codex",
	}, http.StatusForbidden)
	var projectEnv envelope
	require.NoError(t, json.Unmarshal(mismatchedProjectRec.Body.Bytes(), &projectEnv))
	require.Equal(t, service.ErrCodeAgentBindingMismatch, projectEnv.Code)

	mismatchedSnapshotRec := postJSONExpectCode(t, echo, secondLoginResp.AccessToken, http.MethodPost, "/api/v1/config-snapshots", request.ConfigSnapshotReq{
		ProjectID: projectResp.ProjectID,
		Tool:      "codex",
		ProfileID: "binding",
		Settings:  map[string]any{"instructions_pack": "binding"},
	}, http.StatusForbidden)
	var snapshotEnv envelope
	require.NoError(t, json.Unmarshal(mismatchedSnapshotRec.Body.Bytes(), &snapshotEnv))
	require.Equal(t, service.ErrCodeAgentBindingMismatch, snapshotEnv.Code)
}

func TestAnalyticsRouteGoogleSignupFlow(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")

	googleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			require.Equal(t, http.MethodPost, r.Method)
			require.NoError(t, r.ParseForm())
			require.Equal(t, "google-client-id", r.Form.Get("client_id"))
			require.Equal(t, "google-client-secret", r.Form.Get("client_secret"))
			require.Equal(t, "test-google-code", r.Form.Get("code"))
			require.Equal(t, "authorization_code", r.Form.Get("grant_type"))
			require.Equal(t, "http://example.com/api/v1/auth/google/callback", r.Form.Get("redirect_uri"))
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"access_token":"google-access-token","token_type":"Bearer"}`))
			if !isBenignRouteConnError(err) {
				require.NoError(t, err)
			}
		case "/userinfo":
			require.Equal(t, "Bearer google-access-token", r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"sub":"google-sub-1","email":"owner@example.com","email_verified":true,"name":"Owner Example"}`))
			if !isBenignRouteConnError(err) {
				require.NoError(t, err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(googleServer.Close)

	conf.Auth.Google.ClientID = "google-client-id"
	conf.Auth.Google.ClientSecret = "google-client-secret"
	conf.Auth.Google.AuthURL = googleServer.URL + "/auth"
	conf.Auth.Google.TokenURL = googleServer.URL + "/token"
	conf.Auth.Google.UserInfoURL = googleServer.URL + "/userinfo"

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

	startReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/google/start", nil)
	startReq = startReq.WithContext(context.Background())
	startReq.Header.Set("User-Agent", "route-test-client")
	startRec := httptest.NewRecorder()
	echo.ServeHTTP(startRec, startReq)
	require.Equal(t, http.StatusSeeOther, startRec.Code)

	startLocation := startRec.Header().Get("Location")
	require.NotEmpty(t, startLocation)
	startURL, err := url.Parse(startLocation)
	require.NoError(t, err)
	require.Equal(t, googleServer.URL+"/auth", startURL.Scheme+"://"+startURL.Host+startURL.Path)
	require.Equal(t, "google-client-id", startURL.Query().Get("client_id"))
	require.Equal(t, "http://example.com/api/v1/auth/google/callback", startURL.Query().Get("redirect_uri"))
	require.Equal(t, "code", startURL.Query().Get("response_type"))
	require.Contains(t, startURL.Query().Get("scope"), "openid")
	require.Contains(t, startURL.Query().Get("scope"), "email")
	require.Contains(t, startURL.Query().Get("scope"), "profile")

	stateCookie := requireCookie(t, startRec, service.GoogleOAuthStateCookieName)
	callbackURL := startURL.Query().Get("redirect_uri") + "?code=test-google-code&state=" + url.QueryEscape(startURL.Query().Get("state"))
	callbackReq := httptest.NewRequest(http.MethodGet, callbackURL, nil)
	callbackReq = callbackReq.WithContext(context.Background())
	callbackReq.Header.Set("User-Agent", "route-test-client")
	callbackReq.AddCookie(stateCookie)
	callbackRec := httptest.NewRecorder()
	echo.ServeHTTP(callbackRec, callbackReq)
	require.Equal(t, http.StatusSeeOther, callbackRec.Code)
	require.Equal(t, "/dashboard", callbackRec.Header().Get("Location"))

	sessionCookie := requireCookie(t, callbackRec, service.WebSessionCookieName)
	require.NotEmpty(t, sessionCookie.Value)
	expiredStateCookie := requireCookie(t, callbackRec, service.GoogleOAuthStateCookieName)
	require.Equal(t, -1, expiredStateCookie.MaxAge)

	sessionResp := getJSON[response.AuthSessionResp](t, echo, "", "/api/v1/auth/me", nil, sessionCookie)
	require.Equal(t, "owner@example.com", sessionResp.User.Email)
	require.Equal(t, "Owner Example", sessionResp.User.Name)
	require.Equal(t, "admin", sessionResp.User.Role)
	require.Equal(t, "active", sessionResp.User.Status)
	require.NotEmpty(t, sessionResp.User.ID)
	require.NotEqual(t, "demo-user", sessionResp.User.ID)
	require.NotEmpty(t, sessionResp.Organization.ID)
	require.NotEqual(t, "demo-org", sessionResp.Organization.ID)
	require.Contains(t, sessionResp.Organization.Name, "Workspace")
}

func TestAnalyticsRouteRegisterAgentRejectsSpoofedUserID(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")
	closeGoogle := configureGoogleAuthControllerTest(t, conf, googleAuthTestUser{
		Code:     "demo-login",
		Subject:  "google-demo-subject",
		Email:    "demo@example.com",
		Name:     "Demo Operator",
		Verified: true,
	})
	defer closeGoogle()

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

	sessionCookie := loginWithGoogleControllerTest(t, echo, "demo-login")

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
	conf.Auth.AllowDemoUser = true
	conf.Auth.BootstrapUsers = []configs.BootstrapUser{{
		ID:      "member-1",
		OrgID:   "demo-org",
		OrgName: "Demo Org",
		Email:   "member@example.com",
		Name:    "Member User",
		Role:    "member",
	}}
	closeGoogle := configureGoogleAuthControllerTest(
		t,
		conf,
		googleAuthTestUser{
			Code:     "demo-login",
			Subject:  "google-demo-subject",
			Email:    "demo@example.com",
			Name:     "Demo Operator",
			Verified: true,
		},
		googleAuthTestUser{
			Code:     "member-login",
			Subject:  "google-member-subject",
			Email:    "member@example.com",
			Name:     "Member User",
			Verified: true,
		},
	)
	defer closeGoogle()

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

	adminCookie := loginWithGoogleControllerTest(t, echo, "demo-login")

	adminList := getJSON[response.AdminUserListResp](t, echo, "", "/api/v1/admin/users", url.Values{
		"search": []string{"member@example.com"},
	}, adminCookie)
	require.Len(t, adminList.Items, 1)
	require.Equal(t, "member", adminList.Items[0].Role)
	require.Equal(t, "active", adminList.Items[0].Status)
	memberUserID := adminList.Items[0].ID

	memberCookie := loginWithGoogleControllerTest(t, echo, "member-login")

	memberAdminRec := getJSONExpectCode(t, echo, "", "/api/v1/admin/users", nil, http.StatusForbidden, memberCookie)
	var forbidden envelope
	require.NoError(t, json.Unmarshal(memberAdminRec.Body.Bytes(), &forbidden))
	require.NotEqual(t, 0, forbidden.Code)

	deactivateResp := postJSON[response.AdminUserDeactivateResp](t, echo, "", http.MethodPost, "/api/v1/admin/users/deactivate", request.AdminUserDeactivateReq{
		UserID: memberUserID,
	}, adminCookie)
	require.Equal(t, "deactivated", deactivateResp.Status)

	memberSessionAfterDeactivate := getJSONExpectCode(t, echo, "", "/api/v1/auth/me", nil, http.StatusUnauthorized, memberCookie)
	require.Equal(t, http.StatusUnauthorized, memberSessionAfterDeactivate.Code)

	deactivatedList := getJSON[response.AdminUserListResp](t, echo, "", "/api/v1/admin/users", url.Values{
		"status": []string{"disabled"},
	}, adminCookie)
	require.Len(t, deactivatedList.Items, 1)
	require.Equal(t, memberUserID, deactivatedList.Items[0].ID)

	deleteResp := postJSON[response.AdminUserDeleteResp](t, echo, "", http.MethodPost, "/api/v1/admin/users/delete", request.AdminUserDeleteReq{
		UserID: memberUserID,
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
	require.Equal(t, memberUserID, deletedList.Items[0].ID)

	deletedLoginRec := loginWithGoogleControllerTestExpectRedirect(t, echo, "member-login")
	require.Equal(t, http.StatusSeeOther, deletedLoginRec.Code)
	require.Contains(t, deletedLoginRec.Header().Get("Location"), "auth_error=user+account+cannot+sign+in")

	auditResp := getJSON[response.AuditListResp](t, echo, "", "/api/v1/audits", url.Values{
		"org_id":         []string{"demo-org"},
		"type":           []string{"admin.user_deleted"},
		"target_user_id": []string{memberUserID},
		"limit":          []string{"1"},
	}, adminCookie)
	require.Len(t, auditResp.Items, 1)
	require.Equal(t, "admin.user_deleted", auditResp.Items[0].Type)
	require.Equal(t, "demo-user", auditResp.Items[0].ActorUserID)
	require.Equal(t, "admin", auditResp.Items[0].ActorRole)
	require.Equal(t, memberUserID, auditResp.Items[0].TargetUserID)
	require.NotEmpty(t, auditResp.Items[0].SourceIP)
	require.Equal(t, "route-test-client", auditResp.Items[0].UserAgent)
	require.Equal(t, "success", auditResp.Items[0].Result)
	require.Equal(t, "organization user soft-deleted and sessions revoked", auditResp.Items[0].Reason)
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
		code   int
	}{
		{method: http.MethodPost, path: "/api/v1/auth/login", code: http.StatusNotFound},
		{method: http.MethodPost, path: "/api/v1/admin/users", code: http.StatusMethodNotAllowed},
		{method: http.MethodPost, path: "/api/v1/admin/users/reset-password", code: http.StatusNotFound},
		{method: http.MethodPost, path: "/api/v1/devices/register", code: http.StatusNotFound},
		{method: http.MethodPost, path: "/api/v1/projects/connect", code: http.StatusNotFound},
		{method: http.MethodGet, path: "/api/v1/execution-queue", code: http.StatusNotFound},
		{method: http.MethodPost, path: "/api/v1/executions/result", code: http.StatusNotFound},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req = req.WithContext(context.Background())
		req.Header.Set("X-Crux-Token", conf.App.APIToken)
		rec := httptest.NewRecorder()
		echo.ServeHTTP(rec, req)
		require.Equal(t, tc.code, rec.Code, "%s %s should be removed", tc.method, tc.path)
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

func configureGoogleAuthControllerTest(t *testing.T, conf *configs.Config, users ...googleAuthTestUser) func() {
	t.Helper()

	byCode := make(map[string]googleAuthTestUser, len(users))
	for _, user := range users {
		byCode[user.Code] = user
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			require.Equal(t, http.MethodPost, r.Method)
			require.NoError(t, r.ParseForm())
			code := r.Form.Get("code")
			user, ok := byCode[code]
			require.True(t, ok, "unexpected google auth code %q", code)
			require.Equal(t, "test-google-client-id", r.Form.Get("client_id"))
			require.Equal(t, "test-google-client-secret", r.Form.Get("client_secret"))
			require.Equal(t, "authorization_code", r.Form.Get("grant_type"))
			require.Equal(t, "http://example.com/api/v1/auth/google/callback", r.Form.Get("redirect_uri"))
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"access_token":"token-` + user.Code + `","token_type":"Bearer"}`))
			if !isBenignRouteConnError(err) {
				require.NoError(t, err)
			}
		case "/userinfo":
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			code := strings.TrimPrefix(token, "token-")
			user, ok := byCode[code]
			require.True(t, ok, "unexpected google bearer token %q", token)
			body, err := json.Marshal(map[string]any{
				"sub":            user.Subject,
				"email":          user.Email,
				"email_verified": user.Verified,
				"name":           user.Name,
			})
			require.NoError(t, err)
			w.Header().Set("Content-Type", "application/json")
			_, err = w.Write(body)
			if !isBenignRouteConnError(err) {
				require.NoError(t, err)
			}
		default:
			http.NotFound(w, r)
		}
	}))

	conf.Auth.Google.ClientID = "test-google-client-id"
	conf.Auth.Google.ClientSecret = "test-google-client-secret"
	conf.Auth.Google.AuthURL = server.URL + "/auth"
	conf.Auth.Google.TokenURL = server.URL + "/token"
	conf.Auth.Google.UserInfoURL = server.URL + "/userinfo"

	return server.Close
}

func loginWithGoogleControllerTest(t *testing.T, handler http.Handler, code string) *http.Cookie {
	t.Helper()

	rec := loginWithGoogleControllerTestExpectRedirect(t, handler, code)
	require.Equal(t, "/dashboard", rec.Header().Get("Location"))
	return requireCookie(t, rec, service.WebSessionCookieName)
}

func loginWithGoogleControllerTestExpectRedirect(t *testing.T, handler http.Handler, code string) *httptest.ResponseRecorder {
	t.Helper()

	startReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/google/start", nil)
	startReq = startReq.WithContext(context.Background())
	startReq.Header.Set("User-Agent", "route-test-client")
	startRec := httptest.NewRecorder()
	handler.ServeHTTP(startRec, startReq)
	require.Equal(t, http.StatusSeeOther, startRec.Code)

	startURL, err := url.Parse(startRec.Header().Get("Location"))
	require.NoError(t, err)
	stateCookie := requireCookie(t, startRec, service.GoogleOAuthStateCookieName)

	callbackReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/auth/google/callback?code="+url.QueryEscape(code)+"&state="+url.QueryEscape(startURL.Query().Get("state")),
		nil,
	)
	callbackReq = callbackReq.WithContext(context.Background())
	callbackReq.Header.Set("User-Agent", "route-test-client")
	callbackReq.AddCookie(stateCookie)
	callbackRec := httptest.NewRecorder()
	handler.ServeHTTP(callbackRec, callbackReq)
	require.Equal(t, http.StatusSeeOther, callbackRec.Code)
	return callbackRec
}
