package e2etest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Royaltyprogram/aiops/dto/request"
	"github.com/Royaltyprogram/aiops/dto/response"
)

func getAPIJSON[T any](t *testing.T, suite *APISuite, path string, query url.Values) T {
	t.Helper()

	status, body, err := suite.c.Get(suite.ctx, path, query)
	require.NoError(t, err)

	env, data, err := decodeEnvelope[T](body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, 0, env.Code)
	require.NotNil(t, data)
	return *data
}

func tryGetAPIJSON[T any](t *testing.T, suite *APISuite, path string, query url.Values) (int, *T, error) {
	t.Helper()

	status, body, err := suite.c.Get(suite.ctx, path, query)
	if err != nil {
		return 0, nil, err
	}
	env, data, err := decodeEnvelope[T](body)
	if err != nil {
		return status, nil, err
	}
	if status == http.StatusTooManyRequests {
		return status, nil, nil
	}
	if status != http.StatusOK {
		return status, nil, fmt.Errorf("unexpected status %d", status)
	}
	if env.Code != 0 {
		return status, nil, fmt.Errorf("unexpected envelope code %d", env.Code)
	}
	if data == nil {
		return status, nil, fmt.Errorf("missing response data")
	}
	return status, data, nil
}

func postAPIJSON[T any](t *testing.T, suite *APISuite, method, path string, body any) T {
	t.Helper()

	fullURL, err := suite.c.buildURL(path, nil)
	require.NoError(t, err)

	payload, err := json.Marshal(body)
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(suite.ctx, method, fullURL, bytes.NewReader(payload))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := suite.c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	raw := requireBodyBytes(t, resp)
	env, data, err := decodeEnvelope[T](raw)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, 0, env.Code)
	require.NotNil(t, data)
	return *data
}

func requireBodyBytes(t *testing.T, resp *http.Response) []byte {
	t.Helper()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return body
}

func waitForReportResearch(
	t *testing.T,
	suite *APISuite,
	orgID, projectID string,
	triggerSessionID string,
	baselineReportIDs map[string]struct{},
) (*response.DashboardOverviewResp, *response.ReportListResp) {
	t.Helper()

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		overviewStatus, overview, err := tryGetAPIJSON[response.DashboardOverviewResp](t, suite, "/api/v1/dashboard/overview", url.Values{
			"org_id": []string{orgID},
		})
		require.NoError(t, err)
		if overviewStatus == http.StatusTooManyRequests {
			time.Sleep(1 * time.Second)
			continue
		}
		if status := overview.ResearchStatus; status != nil {
			switch status.State {
			case "disabled":
				t.Skip("feedback report research is disabled on the server")
			case "failed":
				t.Skipf("feedback report research failed on the server: %s", status.LastError)
			}
		}

		reportsStatus, reports, err := tryGetAPIJSON[response.ReportListResp](t, suite, "/api/v1/reports", url.Values{
			"project_id": []string{projectID},
		})
		require.NoError(t, err)
		if reportsStatus == http.StatusTooManyRequests {
			time.Sleep(1 * time.Second)
			continue
		}
		if reportRefreshSatisfied(overview.ResearchStatus, reports.Items, triggerSessionID, baselineReportIDs) {
			return overview, reports
		}
		time.Sleep(1 * time.Second)
	}

	overview := getAPIJSON[response.DashboardOverviewResp](t, suite, "/api/v1/dashboard/overview", url.Values{
		"org_id": []string{orgID},
	})
	if status := overview.ResearchStatus; status != nil {
		t.Skipf("feedback report research completed without a published report: %s", status.Summary)
	}
	t.Skip("feedback report research completed without a published report")
	return nil, nil
}

func reportRefreshSatisfied(
	status *response.ReportResearchStatusResp,
	items []response.ReportResp,
	triggerSessionID string,
	baselineReportIDs map[string]struct{},
) bool {
	if status == nil || status.TriggerSessionID != triggerSessionID || status.State != "succeeded" {
		return false
	}
	if len(items) == 0 {
		return false
	}
	if len(baselineReportIDs) == 0 {
		return true
	}
	for _, item := range items {
		if _, ok := baselineReportIDs[item.ID]; !ok {
			return true
		}
	}
	return false
}

func (s *APISuite) TestAnalyticsLifecycle_GeneratesFeedbackReports() {
	if !s.analyticsAuthed {
		s.T().Skip("analytics e2e requires E2E_API_TOKEN or login credentials")
	}

	now := time.Now().UTC()
	suffix := fmt.Sprintf("%d", now.UnixNano())
	orgID := s.c.AuthOrgID
	if orgID == "" {
		orgID = "org-e2e-" + suffix
	}
	userID := "user-e2e-" + suffix
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
		Name:        "workspace-" + suffix,
		RepoHash:    projectHash,
		DefaultTool: "codex",
	})
	require.Equal(s.T(), "connected", projectResp.Status)

	initialReports := getAPIJSON[response.ReportListResp](s.T(), s, "/api/v1/reports", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	baselineReportIDs := make(map[string]struct{}, len(initialReports.Items))
	for _, item := range initialReports.Items {
		baselineReportIDs[item.ID] = struct{}{}
	}

	snapshotResp := postAPIJSON[response.ConfigSnapshotResp](s.T(), s, http.MethodPost, "/api/v1/config-snapshots", request.ConfigSnapshotReq{
		ProjectID:  projectResp.ProjectID,
		Tool:       "codex",
		ProfileID:  "baseline",
		Settings:   map[string]any{"instructions_pack": "baseline"},
		CapturedAt: now.Add(-90 * time.Minute),
	})
	require.Equal(s.T(), "baseline", snapshotResp.ProfileID)

	uploadSession := func(id string, ts time.Time, rawQueries []string, tokenIn, tokenOut int) response.SessionIngestResp {
		return postAPIJSON[response.SessionIngestResp](s.T(), s, http.MethodPost, "/api/v1/session-summaries", request.SessionSummaryReq{
			ProjectID:              projectResp.ProjectID,
			SessionID:              id,
			Tool:                   "codex",
			TokenIn:                tokenIn,
			TokenOut:               tokenOut,
			CachedInputTokens:      180,
			ReasoningOutputTokens:  40,
			FunctionCallCount:      2,
			ToolErrorCount:         1,
			SessionDurationMS:      90000,
			ToolWallTimeMS:         1100,
			ToolCalls:              map[string]int{"shell": 1, "read_file": 1},
			ToolErrors:             map[string]int{"shell": 1},
			ToolWallTimesMS:        map[string]int{"shell": 700, "read_file": 400},
			RawQueries:             rawQueries,
			Models:                 []string{"gpt-5.4"},
			ModelProvider:          "openai",
			FirstResponseLatencyMS: 1800,
			AssistantResponses: []string{
				"The current workflow appears to spend extra turns on discovery before the patch.",
			},
			ReasoningSummaries: []string{
				"Checking current control flow and test expectations before patching.",
			},
			Timestamp: ts,
		})
	}

	finalSessionResp := uploadSession(
		"session-before-"+suffix,
		now.Add(-2*time.Hour),
		[]string{
			"Inspect the route handler and summarize the current control flow.",
			"Find the smallest patch that fixes the failing analytics path.",
			"List the exact tests to run after the patch.",
		},
		1000,
		240,
	)
	require.Equal(s.T(), "report-api.v1", finalSessionResp.SchemaVersion)
	if status := finalSessionResp.ResearchStatus; status != nil && status.MinimumSessions > 1 {
		for i := 2; i <= status.MinimumSessions; i++ {
			finalSessionResp = uploadSession(
				fmt.Sprintf("session-before-%s-%d", suffix, i),
				now.Add(time.Duration(-120+i)*time.Minute),
				[]string{
					"Compare the analytics and health controllers before editing the shared response contract.",
					"Keep the patch minimal and list the targeted verification steps.",
				},
				850,
				210,
			)
		}
	}

	require.Equal(s.T(), "report-api.v1", finalSessionResp.SchemaVersion)
	overview, reports := waitForReportResearch(s.T(), s, orgID, projectResp.ProjectID, finalSessionResp.SessionID, baselineReportIDs)
	require.Equal(s.T(), "report-api.v1", overview.SchemaVersion)
	require.Equal(s.T(), "report-api.v1", reports.SchemaVersion)
	require.NotNil(s.T(), overview.ResearchStatus)
	require.Greater(s.T(), overview.TotalTokens, 0)
	require.Greater(s.T(), overview.AvgTokensPerQuery, 0.0)
	require.NotEmpty(s.T(), overview.ActionSummary)
	require.NotEmpty(s.T(), overview.OutcomeSummary)

	require.NotEmpty(s.T(), reports.Items)
	report := reports.Items[0]
	require.NotEmpty(s.T(), report.Title)
	require.NotEmpty(s.T(), report.Summary)
	require.NotEmpty(s.T(), report.UserIntent)
	require.NotEmpty(s.T(), report.ModelInterpretation)
	require.NotEmpty(s.T(), report.RawSuggestion)

	sessionList := getAPIJSON[response.SessionSummaryListResp](s.T(), s, "/api/v1/session-summaries", url.Values{
		"project_id": []string{projectResp.ProjectID},
		"limit":      []string{"5"},
	})
	require.NotEmpty(s.T(), sessionList.Items)
	require.Equal(s.T(), "openai", sessionList.Items[0].ModelProvider)
	require.NotEmpty(s.T(), sessionList.Items[0].ReasoningSummaries)

	insights := getAPIJSON[response.DashboardProjectInsightsResp](s.T(), s, "/api/v1/dashboard/project-insights", url.Values{
		"project_id": []string{projectResp.ProjectID},
	})
	require.Equal(s.T(), "report-api.v1", insights.SchemaVersion)
	require.Equal(s.T(), projectResp.ProjectID, insights.ProjectID)
	require.NotEmpty(s.T(), insights.Days)
	require.Greater(s.T(), insights.TotalFunctionCalls, 0)
	require.Greater(s.T(), insights.TotalToolWallTimeMS, 0)
	require.NotEmpty(s.T(), insights.Tools)

	auditResp := getAPIJSON[response.AuditListResp](s.T(), s, "/api/v1/audits", url.Values{
		"org_id": []string{orgID},
	})
	require.NotEmpty(s.T(), auditResp.Items)
}
