package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/liushuangls/go-server-template/configs"
	"github.com/liushuangls/go-server-template/dto/request"
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
		ProjectID: "project-z",
		SessionID: "session-before",
		Tool:      "codex",
		TokenIn:   1000,
		TokenOut:  400,
		RawQueries: []string{
			"Inspect the route handler and summarize the current control flow.",
			"Find the smallest patch that fixes the failing analytics request path.",
			"List the exact tests to run after the patch.",
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
	require.Len(t, projects.Items, 2)
	require.Equal(t, "alpha", projects.Items[0].Name)
	require.Equal(t, "zeta", projects.Items[1].Name)

	recommendations, err := svc.ListRecommendations(ctx, &request.RecommendationListReq{ProjectID: "project-z"})
	require.NoError(t, err)
	require.Len(t, recommendations.Items, 1)
	require.Equal(t, "instruction-custom-rules", recommendations.Items[0].Kind)
	require.Len(t, recommendations.Items[0].ChangePlan, 1)
	require.Equal(t, "AGENTS.md", recommendations.Items[0].ChangePlan[0].TargetFile)
	require.Contains(t, recommendations.Items[0].ChangePlan[0].ContentPreview, "AgentOpt Personal Instruction Pack")

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
	require.Len(t, pending.Items, 2)
	require.Equal(t, planNew.ApplyID, pending.Items[0].ApplyID)

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
	require.Len(t, projectVisible.Items, 3)
	require.Equal(t, projectScopedPlan.ApplyID, projectVisible.Items[0].ApplyID)

	oldAppliedAt := now.Add(-30 * time.Minute)
	newAppliedAt := now.Add(-10 * time.Minute)

	store.mu.Lock()
	store.applyOperations[planOld.ApplyID].AppliedAt = &oldAppliedAt
	store.applyOperations[planOld.ApplyID].Status = "applied"
	store.applyOperations[planNew.ApplyID].AppliedAt = &newAppliedAt
	store.applyOperations[planNew.ApplyID].Status = "applied"
	store.mu.Unlock()

	_, err = svc.UploadSessionSummary(ctx, &request.SessionSummaryReq{
		ProjectID: "project-z",
		SessionID: "session-after",
		Tool:      "codex",
		TokenIn:   800,
		TokenOut:  300,
		RawQueries: []string{
			"Compare the analytics and health controllers before changing the shared response contract.",
			"Keep the patch minimal and list targeted verification steps.",
		},
		Timestamp: now.Add(-5 * time.Minute),
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

	impact, err := svc.ImpactSummary(ctx, &request.ImpactSummaryReq{ProjectID: "project-z"})
	require.NoError(t, err)
	require.Len(t, impact.Items, 2)
	require.Equal(t, planNew.ApplyID, impact.Items[0].ApplyID)
	require.Greater(t, impact.Items[0].SessionsAfter, 0)

	overview, err := svc.DashboardOverview(ctx, &request.DashboardOverviewReq{OrgID: "org-1"})
	require.NoError(t, err)
	require.Greater(t, overview.AvgTokensPerQuery, 0.0)
	require.Greater(t, overview.TotalTokens, 0)
	require.Equal(t, 2, overview.SuccessfulRolloutCount)
	require.NotEmpty(t, overview.ActionSummary)
	require.NotEmpty(t, overview.OutcomeSummary)

	audits, err := svc.AuditList(ctx, &request.AuditListReq{OrgID: "org-1", ProjectID: "project-z"})
	require.NoError(t, err)
	require.NotEmpty(t, audits.Items)
	require.Equal(t, "session.ingested", audits.Items[0].Type)
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

	_, err = svc.ReviewChangePlan(ctx, &request.ReviewChangePlanReq{
		ApplyID:    plan.ApplyID,
		Decision:   "approve",
		ReviewedBy: "user-exec",
	})
	require.NoError(t, err)

	applyResult, err := svc.ReportApplyResult(ctx, &request.ApplyResultReq{
		ApplyID:     plan.ApplyID,
		Success:     true,
		Note:        "applied by lifecycle test",
		AppliedFile: "AGENTS.md",
		AppliedText: "AgentOpt Personal Instruction Pack",
	})
	require.NoError(t, err)
	require.Equal(t, "applied", applyResult.Status)
	require.False(t, applyResult.RolledBack)

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
	require.Greater(t, impact.Items[0].SessionsAfter, 0)

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

	overview, err := svc.DashboardOverview(ctx, &request.DashboardOverviewReq{OrgID: "org-exec"})
	require.NoError(t, err)
	require.Greater(t, overview.AvgQueriesPerSession, 0.0)
	require.Greater(t, overview.TotalTokens, 0)
	require.Equal(t, 0, overview.SuccessfulRolloutCount)
	require.Equal(t, 0, overview.FailedExecutionCount)
	require.NotEmpty(t, overview.ActionSummary)
	require.NotEmpty(t, overview.OutcomeSummary)
}
