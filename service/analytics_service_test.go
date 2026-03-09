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

	planOld, err := svc.CreateApplyPlan(ctx, &request.ApplyRecommendationReq{
		RecommendationID: recommendations.Items[0].ID,
		RequestedBy:      "user-1",
		Scope:            "project",
	})
	require.NoError(t, err)

	time.Sleep(10 * time.Millisecond)

	planNew, err := svc.CreateApplyPlan(ctx, &request.ApplyRecommendationReq{
		RecommendationID: recommendations.Items[0].ID,
		RequestedBy:      "user-1",
		Scope:            "user",
	})
	require.NoError(t, err)

	pending, err := svc.PendingApplies(ctx, &request.PendingApplyReq{ProjectID: "project-z", RequestedBy: "user-1"})
	require.NoError(t, err)
	require.Len(t, pending.Items, 2)
	require.Equal(t, planNew.ApplyID, pending.Items[0].ApplyID)

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
	require.Len(t, history.Items, 2)
	require.Equal(t, planNew.ApplyID, history.Items[0].ApplyID)
	require.Equal(t, planOld.ApplyID, history.Items[1].ApplyID)

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
