package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Royaltyprogram/aiops/configs"
	"github.com/Royaltyprogram/aiops/dto/request"
)

func TestAnalyticsServiceLifecycleAndOrdering(t *testing.T) {
	ctx := context.Background()
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	svc := NewAnalyticsService(Options{
		Config:         conf,
		AnalyticsStore: store,
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

	recommendations, err := svc.ListRecommendations(ctx, &request.RecommendationListReq{ProjectID: "project-z"})
	require.NoError(t, err)
	require.Len(t, recommendations.Items, 1)
	require.Equal(t, "instruction-custom-rules", recommendations.Items[0].Kind)
	require.Len(t, recommendations.Items[0].ChangePlan, 1)
	require.Equal(t, defaultCodexInstructionTarget, recommendations.Items[0].ChangePlan[0].TargetFile)
	require.Contains(t, recommendations.Items[0].ChangePlan[0].ContentPreview, "AgentOpt Research Findings")

	planOld, err := svc.CreateApplyPlan(ctx, &request.ApplyRecommendationReq{
		RecommendationID: recommendations.Items[0].ID,
		RequestedBy:      "user-1",
		Scope:            "project",
	})
	require.NoError(t, err)
	require.Len(t, planOld.PatchPreview, len(recommendations.Items[0].ChangePlan))
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
		RecommendationID: recommendations.Items[0].ID,
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
		RecommendationID: recommendations.Items[0].ID,
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

func TestRegisterProjectReusesExistingProjectAndPreservesSignals(t *testing.T) {
	ctx := context.Background()
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	svc := NewAnalyticsService(Options{
		Config:         conf,
		AnalyticsStore: store,
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
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	svc := NewAnalyticsService(Options{
		Config:         conf,
		AnalyticsStore: store,
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

	recommendations, err := svc.ListRecommendations(cliCtx, &request.RecommendationListReq{})
	require.NoError(t, err)
	require.NotEmpty(t, recommendations.Items)

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
		Config:         conf,
		AnalyticsStore: store,
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
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	svc := NewAnalyticsService(Options{
		Config:         conf,
		AnalyticsStore: store,
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

	recommendations, err := svc.ListRecommendations(ctx, &request.RecommendationListReq{ProjectID: "project-auto"})
	require.NoError(t, err)
	require.Len(t, recommendations.Items, 1)
	require.Equal(t, "instruction-custom-rules", recommendations.Items[0].Kind)

	plan, err := svc.CreateApplyPlan(ctx, &request.ApplyRecommendationReq{
		RecommendationID: recommendations.Items[0].ID,
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

func TestCreateApplyPlanBlocksWhenActiveExperimentExists(t *testing.T) {
	ctx := context.Background()
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	svc := NewAnalyticsService(Options{
		Config:         conf,
		AnalyticsStore: store,
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

	recommendations, err := svc.ListRecommendations(ctx, &request.RecommendationListReq{ProjectID: "project-active"})
	require.NoError(t, err)
	require.Len(t, recommendations.Items, 1)

	firstPlan, err := svc.CreateApplyPlan(ctx, &request.ApplyRecommendationReq{
		RecommendationID: recommendations.Items[0].ID,
		RequestedBy:      "user-active",
		Scope:            "project",
	})
	require.NoError(t, err)
	require.NotEmpty(t, firstPlan.ExperimentID)

	_, err = svc.CreateApplyPlan(ctx, &request.ApplyRecommendationReq{
		RecommendationID: recommendations.Items[0].ID,
		RequestedBy:      "user-active",
		Scope:            "project",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "active experiment")
}

func TestUploadSessionSummaryRequestsRollbackAfterSevereRegression(t *testing.T) {
	ctx := context.Background()
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	svc := NewAnalyticsService(Options{
		Config:         conf,
		AnalyticsStore: store,
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

	recommendations, err := svc.ListRecommendations(ctx, &request.RecommendationListReq{ProjectID: "project-rollback"})
	require.NoError(t, err)
	require.Len(t, recommendations.Items, 1)

	plan, err := svc.CreateApplyPlan(ctx, &request.ApplyRecommendationReq{
		RecommendationID: recommendations.Items[0].ID,
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
	require.Contains(t, experiments.Items[0].DecisionReason, "tokens per query regressed")

	pending, err := svc.PendingApplies(ctx, &request.PendingApplyReq{ProjectID: "project-rollback", UserID: "user-rollback"})
	require.NoError(t, err)
	require.Len(t, pending.Items, 1)
	require.Equal(t, "rollback", pending.Items[0].Action)
	require.Equal(t, "rollback_requested", pending.Items[0].Status)
	require.Equal(t, plan.ExperimentID, pending.Items[0].ExperimentID)
	require.Contains(t, pending.Items[0].Note, "tokens per query regressed")

	overview, err := svc.DashboardOverview(ctx, &request.DashboardOverviewReq{OrgID: "org-rollback"})
	require.NoError(t, err)
	require.Equal(t, 1, overview.ActiveExperimentCount)
}

func TestUploadSessionSummaryReplacesExistingSessionByID(t *testing.T) {
	ctx := context.Background()
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	svc := NewAnalyticsService(Options{
		Config:         conf,
		AnalyticsStore: store,
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
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	svc := NewAnalyticsService(Options{
		Config:         conf,
		AnalyticsStore: store,
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

	recommendations, err := svc.ListRecommendations(ctx, &request.RecommendationListReq{ProjectID: "project-exec"})
	require.NoError(t, err)
	require.NotEmpty(t, recommendations.Items)

	plan, err := svc.CreateApplyPlan(ctx, &request.ApplyRecommendationReq{
		RecommendationID: recommendations.Items[0].ID,
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
