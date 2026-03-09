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
		ProjectID:             "project-z",
		SessionID:             "session-before",
		Tool:                  "codex",
		ProjectHash:           "zeta-hash",
		LanguageMix:           map[string]float64{"go": 1},
		TotalPromptsCount:     10,
		TotalToolCalls:        20,
		BashCallsCount:        5,
		ReadOps:               15,
		EditOps:               5,
		WriteOps:              2,
		MCPUsageCount:         1,
		PermissionRejectCount: 4,
		RetryCount:            3,
		TokenIn:               1000,
		TokenOut:              400,
		EstimatedCost:         1.2,
		TaskType:              "bugfix",
		RepoSizeBucket:        "large",
		ConfigProfileID:       "baseline",
		Timestamp:             now.Add(-2 * time.Hour),
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
	require.GreaterOrEqual(t, len(recommendations.Items), 1)
	for i := 1; i < len(recommendations.Items); i++ {
		require.GreaterOrEqual(t, recommendations.Items[i-1].Score, recommendations.Items[i].Score)
	}
	require.GreaterOrEqual(t, len(recommendations.Items[0].ChangePlan), 2)
	require.Equal(t, "AGENTS.md", recommendations.Items[0].ChangePlan[0].TargetFile)
	require.Equal(t, ".codex/config.json", recommendations.Items[0].ChangePlan[1].TargetFile)

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
		ProjectID:             "project-z",
		SessionID:             "session-after",
		Tool:                  "codex",
		ProjectHash:           "zeta-hash",
		LanguageMix:           map[string]float64{"go": 1},
		TotalPromptsCount:     12,
		TotalToolCalls:        18,
		BashCallsCount:        4,
		ReadOps:               12,
		EditOps:               8,
		WriteOps:              3,
		MCPUsageCount:         1,
		PermissionRejectCount: 1,
		RetryCount:            1,
		TokenIn:               800,
		TokenOut:              300,
		EstimatedCost:         0.7,
		TaskType:              "bugfix",
		RepoSizeBucket:        "large",
		ConfigProfileID:       "repo-qna",
		Timestamp:             now.Add(-5 * time.Minute),
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

	impact, err := svc.ImpactSummary(ctx, &request.ImpactSummaryReq{ProjectID: "project-z"})
	require.NoError(t, err)
	require.Len(t, impact.Items, 2)
	require.Equal(t, planNew.ApplyID, impact.Items[0].ApplyID)
	require.Greater(t, impact.Items[0].SessionsAfter, 0)

	audits, err := svc.AuditList(ctx, &request.AuditListReq{OrgID: "org-1", ProjectID: "project-z"})
	require.NoError(t, err)
	require.NotEmpty(t, audits.Items)
	require.Equal(t, "session.ingested", audits.Items[0].Type)
}

func TestCreateApplyPlanAutoApprovesLowRiskConfigMerge(t *testing.T) {
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
		ProjectID:                "project-auto",
		SessionID:                "session-auto",
		Tool:                     "codex",
		ProjectHash:              "auto-hash",
		LanguageMix:              map[string]float64{"go": 1},
		TotalPromptsCount:        8,
		TotalToolCalls:           10,
		BashCallsCount:           3,
		ReadOps:                  2,
		EditOps:                  5,
		WriteOps:                 1,
		MCPUsageCount:            0,
		PermissionRejectCount:    1,
		RetryCount:               0,
		TokenIn:                  300,
		TokenOut:                 120,
		EstimatedCost:            0.2,
		TaskType:                 "docs",
		RepoSizeBucket:           "small",
		ConfigProfileID:          "baseline",
		RepoExplorationIntensity: 0.1,
		ShellHeavy:               true,
		AcceptanceProxy:          0.9,
		Timestamp:                time.Now().UTC(),
	})
	require.NoError(t, err)

	recommendations, err := svc.ListRecommendations(ctx, &request.RecommendationListReq{ProjectID: "project-auto"})
	require.NoError(t, err)
	require.NotEmpty(t, recommendations.Items)

	var autoRecID string
	for _, rec := range recommendations.Items {
		if rec.Kind == "safe-executor-profile" {
			autoRecID = rec.ID
			break
		}
	}
	require.NotEmpty(t, autoRecID)

	plan, err := svc.CreateApplyPlan(ctx, &request.ApplyRecommendationReq{
		RecommendationID: autoRecID,
		RequestedBy:      "user-auto",
		Scope:            "user",
	})
	require.NoError(t, err)
	require.Equal(t, "auto_approved", plan.PolicyMode)
	require.Equal(t, "approved_for_local_apply", plan.Status)
	require.Equal(t, "approved", plan.ApprovalStatus)
	require.Equal(t, "auto_approved", plan.Decision)
	require.Equal(t, "policy-engine", plan.ReviewedBy)
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
		ProjectID:                "project-exec",
		SessionID:                "session-before-exec",
		Tool:                     "codex",
		ProjectHash:              "exec-hash",
		LanguageMix:              map[string]float64{"go": 1},
		TotalPromptsCount:        12,
		TotalToolCalls:           24,
		BashCallsCount:           6,
		ReadOps:                  12,
		EditOps:                  4,
		WriteOps:                 2,
		MCPUsageCount:            1,
		PermissionRejectCount:    2,
		RetryCount:               1,
		TokenIn:                  800,
		TokenOut:                 220,
		EstimatedCost:            0.5,
		TaskType:                 "bugfix",
		RepoSizeBucket:           "large",
		ConfigProfileID:          "baseline",
		RepoExplorationIntensity: 0.8,
		AcceptanceProxy:          0.45,
		Timestamp:                now.Add(-2 * time.Hour),
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
		ApplyID:         plan.ApplyID,
		Success:         true,
		Note:            "applied by lifecycle test",
		AppliedFile:     "AGENTS.md, .codex/config.json",
		AppliedSettings: map[string]any{"instructions_pack": "repo-research"},
		AppliedText:     "AgentOpt Research Pack",
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
	require.Equal(t, "AGENTS.md, .codex/config.json", history.Items[0].AppliedFile)

	_, err = svc.UploadSessionSummary(ctx, &request.SessionSummaryReq{
		ProjectID:                "project-exec",
		SessionID:                "session-after-exec",
		Tool:                     "codex",
		ProjectHash:              "exec-hash",
		LanguageMix:              map[string]float64{"go": 1},
		TotalPromptsCount:        10,
		TotalToolCalls:           18,
		BashCallsCount:           4,
		ReadOps:                  9,
		EditOps:                  6,
		WriteOps:                 3,
		MCPUsageCount:            1,
		PermissionRejectCount:    1,
		RetryCount:               0,
		TokenIn:                  600,
		TokenOut:                 180,
		EstimatedCost:            0.3,
		TaskType:                 "bugfix",
		RepoSizeBucket:           "large",
		ConfigProfileID:          "repo-research",
		RepoExplorationIntensity: 0.5,
		AcceptanceProxy:          0.9,
		Timestamp:                now.Add(2 * time.Hour),
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
		AppliedFile: "AGENTS.md, .codex/config.json",
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
}
