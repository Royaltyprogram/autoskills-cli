package service

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/liushuangls/go-server-template/dto/request"
	"github.com/liushuangls/go-server-template/dto/response"
	"github.com/liushuangls/go-server-template/pkg/ecode"
)

type AnalyticsService struct {
	Options
}

func NewAnalyticsService(opt Options) *AnalyticsService {
	return &AnalyticsService{Options: opt}
}

func (s *AnalyticsService) RegisterAgent(ctx context.Context, req *request.RegisterAgentReq) (*response.AgentRegistrationResp, error) {
	_ = ctx

	now := time.Now().UTC()

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	agentID := req.AgentID
	if agentID == "" {
		agentID = s.AnalyticsStore.nextID("agent")
	}

	if req.OrgName == "" {
		req.OrgName = req.OrgID
	}
	s.AnalyticsStore.organizations[req.OrgID] = &Organization{
		ID:   req.OrgID,
		Name: req.OrgName,
	}
	s.AnalyticsStore.users[req.UserID] = &User{
		ID:    req.UserID,
		OrgID: req.OrgID,
		Email: req.UserEmail,
	}
	s.AnalyticsStore.agents[agentID] = &Agent{
		ID:           agentID,
		OrgID:        req.OrgID,
		UserID:       req.UserID,
		DeviceName:   req.DeviceName,
		Hostname:     req.Hostname,
		CLIVersion:   req.CLIVersion,
		Tools:        append([]string(nil), req.Tools...),
		RegisteredAt: now,
	}

	s.appendAuditLocked(req.OrgID, "", "agent.registered", "cli agent registered")
	if err := s.AnalyticsStore.persistLocked(); err != nil {
		return nil, err
	}

	return &response.AgentRegistrationResp{
		AgentID:      agentID,
		OrgID:        req.OrgID,
		UserID:       req.UserID,
		Status:       "registered",
		RegisteredAt: now,
	}, nil
}

func (s *AnalyticsService) RegisterProject(ctx context.Context, req *request.RegisterProjectReq) (*response.ProjectRegistrationResp, error) {
	_ = ctx

	now := time.Now().UTC()

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	if _, ok := s.AnalyticsStore.agents[req.AgentID]; !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown agent_id"))
	}

	projectID := req.ProjectID
	if projectID == "" {
		projectID = s.AnalyticsStore.nextID("project")
	}

	project := &Project{
		ID:          projectID,
		OrgID:       req.OrgID,
		AgentID:     req.AgentID,
		Name:        req.Name,
		RepoHash:    req.RepoHash,
		RepoPath:    req.RepoPath,
		LanguageMix: cloneFloatMap(req.LanguageMix),
		DefaultTool: req.DefaultTool,
		ConnectedAt: now,
	}
	s.AnalyticsStore.projects[projectID] = project

	s.appendAuditLocked(req.OrgID, projectID, "project.connected", "project connected to analytics workspace")
	if err := s.AnalyticsStore.persistLocked(); err != nil {
		return nil, err
	}

	return &response.ProjectRegistrationResp{
		ProjectID:   projectID,
		Status:      "connected",
		ConnectedAt: now,
	}, nil
}

func (s *AnalyticsService) UploadConfigSnapshot(ctx context.Context, req *request.ConfigSnapshotReq) (*response.ConfigSnapshotResp, error) {
	_ = ctx

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	project, ok := s.AnalyticsStore.projects[req.ProjectID]
	if !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown project_id"))
	}

	capturedAt := req.CapturedAt.UTC()
	if capturedAt.IsZero() {
		capturedAt = time.Now().UTC()
	}
	profileID := req.ProfileID
	if profileID == "" {
		profileID = s.AnalyticsStore.nextID("profile")
	}

	snapshot := &ConfigSnapshot{
		ID:         s.AnalyticsStore.nextID("snapshot"),
		ProjectID:  req.ProjectID,
		Tool:       req.Tool,
		ProfileID:  profileID,
		Settings:   cloneAnyMap(req.Settings),
		CapturedAt: capturedAt,
	}

	s.AnalyticsStore.configSnapshots[req.ProjectID] = append(s.AnalyticsStore.configSnapshots[req.ProjectID], snapshot)
	project.LastProfileID = profileID
	project.LastIngestedAt = &capturedAt

	s.appendAuditLocked(project.OrgID, req.ProjectID, "config.snapshot", "config snapshot uploaded")
	if err := s.AnalyticsStore.persistLocked(); err != nil {
		return nil, err
	}

	return &response.ConfigSnapshotResp{
		SnapshotID: snapshot.ID,
		ProjectID:  req.ProjectID,
		ProfileID:  profileID,
		CapturedAt: capturedAt,
	}, nil
}

func (s *AnalyticsService) UploadSessionSummary(ctx context.Context, req *request.SessionSummaryReq) (*response.SessionIngestResp, error) {
	_ = ctx

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	project, ok := s.AnalyticsStore.projects[req.ProjectID]
	if !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown project_id"))
	}

	recordedAt := req.Timestamp.UTC()
	if recordedAt.IsZero() {
		recordedAt = time.Now().UTC()
	}
	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = s.AnalyticsStore.nextID("session")
	}

	summary := &SessionSummary{
		ID:                    sessionID,
		ProjectID:             req.ProjectID,
		Tool:                  req.Tool,
		ProjectHash:           req.ProjectHash,
		LanguageMix:           cloneFloatMap(req.LanguageMix),
		TotalPromptsCount:     req.TotalPromptsCount,
		TotalToolCalls:        req.TotalToolCalls,
		BashCallsCount:        req.BashCallsCount,
		ReadOps:               req.ReadOps,
		EditOps:               req.EditOps,
		WriteOps:              req.WriteOps,
		MCPUsageCount:         req.MCPUsageCount,
		PermissionRejectCount: req.PermissionRejectCount,
		RetryCount:            req.RetryCount,
		TokenIn:               req.TokenIn,
		TokenOut:              req.TokenOut,
		EstimatedCost:         req.EstimatedCost,
		TaskType:              req.TaskType,
		RepoSizeBucket:        req.RepoSizeBucket,
		ConfigProfileID:       req.ConfigProfileID,
		Timestamp:             recordedAt,
	}

	s.AnalyticsStore.sessionSummaries[req.ProjectID] = append(s.AnalyticsStore.sessionSummaries[req.ProjectID], summary)
	project.LastIngestedAt = &recordedAt
	if summary.ConfigProfileID != "" {
		project.LastProfileID = summary.ConfigProfileID
	}

	recommendations := s.refreshRecommendationsLocked(project)
	ids := make([]string, 0, len(recommendations))
	for _, item := range recommendations {
		ids = append(ids, item.ID)
	}

	s.appendAuditLocked(project.OrgID, req.ProjectID, "session.ingested", "session summary uploaded")
	if err := s.AnalyticsStore.persistLocked(); err != nil {
		return nil, err
	}

	return &response.SessionIngestResp{
		SessionID:               sessionID,
		ProjectID:               req.ProjectID,
		RecommendationCount:     len(ids),
		LatestRecommendationIDs: ids,
		RecordedAt:              recordedAt,
	}, nil
}

func (s *AnalyticsService) ListRecommendations(ctx context.Context, req *request.RecommendationListReq) (*response.RecommendationListResp, error) {
	_ = ctx

	s.AnalyticsStore.mu.RLock()
	defer s.AnalyticsStore.mu.RUnlock()

	if _, ok := s.AnalyticsStore.projects[req.ProjectID]; !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown project_id"))
	}

	ids := s.AnalyticsStore.projectRecommendations[req.ProjectID]
	items := make([]response.RecommendationResp, 0, len(ids))
	for _, id := range ids {
		rec, ok := s.AnalyticsStore.recommendations[id]
		if !ok {
			continue
		}
		items = append(items, toRecommendationResp(rec))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Score == items[j].Score {
			return items[i].ID < items[j].ID
		}
		return items[i].Score > items[j].Score
	})

	return &response.RecommendationListResp{Items: items}, nil
}

func (s *AnalyticsService) DashboardOverview(ctx context.Context, req *request.DashboardOverviewReq) (*response.DashboardOverviewResp, error) {
	_ = ctx

	s.AnalyticsStore.mu.RLock()
	defer s.AnalyticsStore.mu.RUnlock()

	if _, ok := s.AnalyticsStore.organizations[req.OrgID]; !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown org_id"))
	}

	projectIDs := make([]string, 0)
	for projectID, project := range s.AnalyticsStore.projects {
		if project.OrgID == req.OrgID {
			projectIDs = append(projectIDs, projectID)
		}
	}

	var (
		totalSessions        int
		totalCost            float64
		totalTokens          int
		totalQueries         int
		totalToolCalls       int
		totalRejects         int
		totalRetries         int
		totalActiveRecs      int
		totalApplyOps        int
		totalSuccessfulApply int
		totalRollbacks       int
		lastIngestedAt       *time.Time
		taskCounts           = map[string]int{}
	)

	for _, projectID := range projectIDs {
		for _, session := range s.AnalyticsStore.sessionSummaries[projectID] {
			totalSessions++
			totalCost += session.EstimatedCost
			totalTokens += session.TokenIn + session.TokenOut
			totalQueries += maxInt(session.TotalPromptsCount, 1)
			totalToolCalls += session.TotalToolCalls
			totalRejects += session.PermissionRejectCount
			totalRetries += session.RetryCount
			taskCounts[session.TaskType]++
			if lastIngestedAt == nil || session.Timestamp.After(*lastIngestedAt) {
				ts := session.Timestamp
				lastIngestedAt = &ts
			}
		}

		for _, recommendationID := range s.AnalyticsStore.projectRecommendations[projectID] {
			rec := s.AnalyticsStore.recommendations[recommendationID]
			if rec != nil && rec.Status == "active" {
				totalActiveRecs++
			}
		}
	}

	for _, op := range s.AnalyticsStore.applyOperations {
		project := s.AnalyticsStore.projects[op.ProjectID]
		if project == nil || project.OrgID != req.OrgID {
			continue
		}
		totalApplyOps++
		if op.Status == "applied" {
			totalSuccessfulApply++
		}
		if op.RolledBack {
			totalRollbacks++
		}
	}

	return &response.DashboardOverviewResp{
		OrgID:                   req.OrgID,
		TotalProjects:           len(projectIDs),
		TotalSessions:           totalSessions,
		ActiveRecommendations:   totalActiveRecs,
		TotalEstimatedCost:      round(totalCost),
		AvgTokensPerQuery:       safeDiv(float64(totalTokens), float64(totalQueries)),
		AvgToolCallsPerQuery:    safeDiv(float64(totalToolCalls), float64(totalQueries)),
		PermissionRejectRate:    safeDiv(float64(totalRejects), float64(maxInt(totalToolCalls, 1))),
		RetryRate:               safeDiv(float64(totalRetries), float64(maxInt(totalQueries, 1))),
		RecommendationApplyRate: safeDiv(float64(totalSuccessfulApply), float64(maxInt(totalActiveRecs+totalSuccessfulApply, 1))),
		RollbackRate:            safeDiv(float64(totalRollbacks), float64(maxInt(totalApplyOps, 1))),
		TopTaskTypes:            sortedTaskBreakdown(taskCounts),
		LastIngestedAt:          lastIngestedAt,
	}, nil
}

func (s *AnalyticsService) ListProjects(ctx context.Context, req *request.ProjectListReq) (*response.ProjectListResp, error) {
	_ = ctx

	s.AnalyticsStore.mu.RLock()
	defer s.AnalyticsStore.mu.RUnlock()

	if _, ok := s.AnalyticsStore.organizations[req.OrgID]; !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown org_id"))
	}

	items := make([]response.ProjectResp, 0)
	for _, project := range s.AnalyticsStore.projects {
		if project.OrgID != req.OrgID {
			continue
		}
		items = append(items, response.ProjectResp{
			ID:             project.ID,
			Name:           project.Name,
			RepoHash:       project.RepoHash,
			RepoPath:       project.RepoPath,
			DefaultTool:    project.DefaultTool,
			LastProfileID:  project.LastProfileID,
			LastIngestedAt: project.LastIngestedAt,
			LanguageMix:    cloneFloatMap(project.LanguageMix),
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Name == items[j].Name {
			return items[i].ID < items[j].ID
		}
		return items[i].Name < items[j].Name
	})

	return &response.ProjectListResp{Items: items}, nil
}

func (s *AnalyticsService) CreateApplyPlan(ctx context.Context, req *request.ApplyRecommendationReq) (*response.ApplyPlanResp, error) {
	_ = ctx

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	rec, ok := s.AnalyticsStore.recommendations[req.RecommendationID]
	if !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown recommendation_id"))
	}

	scope := req.Scope
	if scope == "" {
		scope = "user"
	}

	now := time.Now().UTC()
	patchPreview := buildPatchPreview(rec)
	op := &ApplyOperation{
		ID:               s.AnalyticsStore.nextID("apply"),
		RecommendationID: req.RecommendationID,
		ProjectID:        rec.ProjectID,
		RequestedBy:      req.RequestedBy,
		Scope:            scope,
		Status:           "pending_local_apply",
		PatchPreview:     patchPreview,
		RequestedAt:      now,
	}
	s.AnalyticsStore.applyOperations[op.ID] = op
	s.appendAuditLocked(s.AnalyticsStore.projects[rec.ProjectID].OrgID, rec.ProjectID, "recommendation.apply_requested", "apply plan created")
	if err := s.AnalyticsStore.persistLocked(); err != nil {
		return nil, err
	}

	return &response.ApplyPlanResp{
		ApplyID:        op.ID,
		Recommendation: toRecommendationResp(rec),
		Status:         op.Status,
		PatchPreview:   toPatchPreviewResp(patchPreview),
		RequestedAt:    now,
	}, nil
}

func (s *AnalyticsService) ApplyHistory(ctx context.Context, req *request.ApplyHistoryReq) (*response.ApplyHistoryResp, error) {
	_ = ctx

	s.AnalyticsStore.mu.RLock()
	defer s.AnalyticsStore.mu.RUnlock()

	if _, ok := s.AnalyticsStore.projects[req.ProjectID]; !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown project_id"))
	}

	items := make([]response.ApplyHistoryItem, 0)
	for _, op := range s.AnalyticsStore.applyOperations {
		if op.ProjectID != req.ProjectID {
			continue
		}
		items = append(items, response.ApplyHistoryItem{
			ApplyID:          op.ID,
			RecommendationID: op.RecommendationID,
			Status:           op.Status,
			Scope:            op.Scope,
			RequestedBy:      op.RequestedBy,
			RequestedAt:      op.RequestedAt,
			AppliedAt:        op.AppliedAt,
			AppliedFile:      op.AppliedFile,
			AppliedSettings:  cloneAnyMap(op.AppliedSettings),
			PatchPreview:     toPatchPreviewResp(op.PatchPreview),
			RolledBack:       op.RolledBack,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].RequestedAt.Equal(items[j].RequestedAt) {
			return items[i].ApplyID > items[j].ApplyID
		}
		return items[i].RequestedAt.After(items[j].RequestedAt)
	})

	return &response.ApplyHistoryResp{Items: items}, nil
}

func (s *AnalyticsService) ImpactSummary(ctx context.Context, req *request.ImpactSummaryReq) (*response.ImpactSummaryResp, error) {
	_ = ctx

	s.AnalyticsStore.mu.RLock()
	defer s.AnalyticsStore.mu.RUnlock()

	if _, ok := s.AnalyticsStore.projects[req.ProjectID]; !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown project_id"))
	}

	items := make([]response.ImpactSummaryItem, 0)
	sessions := s.AnalyticsStore.sessionSummaries[req.ProjectID]
	for _, op := range s.AnalyticsStore.applyOperations {
		if op.ProjectID != req.ProjectID || op.AppliedAt == nil {
			continue
		}

		before, after := splitSessionsByApplyTime(sessions, *op.AppliedAt)
		beforeCost, beforeRetry, beforeReject := summarizeSessions(before)
		afterCost, afterRetry, afterReject := summarizeSessions(after)

		items = append(items, response.ImpactSummaryItem{
			ApplyID:             op.ID,
			RecommendationID:    op.RecommendationID,
			Status:              op.Status,
			AppliedAt:           op.AppliedAt,
			SessionsBefore:      len(before),
			SessionsAfter:       len(after),
			AvgCostBefore:       beforeCost,
			AvgCostAfter:        afterCost,
			AvgRetryRateBefore:  beforeRetry,
			AvgRetryRateAfter:   afterRetry,
			AvgRejectRateBefore: beforeReject,
			AvgRejectRateAfter:  afterReject,
			CostDelta:           round(afterCost - beforeCost),
			RetryDelta:          round(afterRetry - beforeRetry),
			RejectDelta:         round(afterReject - beforeReject),
			Interpretation:      interpretImpact(beforeCost, afterCost, beforeRetry, afterRetry, beforeReject, afterReject, len(after)),
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].AppliedAt == nil {
			return false
		}
		if items[j].AppliedAt == nil {
			return true
		}
		if items[i].AppliedAt.Equal(*items[j].AppliedAt) {
			return items[i].ApplyID > items[j].ApplyID
		}
		return items[i].AppliedAt.After(*items[j].AppliedAt)
	})

	return &response.ImpactSummaryResp{Items: items}, nil
}

func (s *AnalyticsService) AuditList(ctx context.Context, req *request.AuditListReq) (*response.AuditListResp, error) {
	_ = ctx

	s.AnalyticsStore.mu.RLock()
	defer s.AnalyticsStore.mu.RUnlock()

	if _, ok := s.AnalyticsStore.organizations[req.OrgID]; !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown org_id"))
	}

	items := make([]response.AuditEventResp, 0)
	for i := len(s.AnalyticsStore.audits) - 1; i >= 0; i-- {
		audit := s.AnalyticsStore.audits[i]
		if audit.OrgID != req.OrgID {
			continue
		}
		if req.ProjectID != "" && audit.ProjectID != req.ProjectID {
			continue
		}
		items = append(items, response.AuditEventResp{
			ID:        audit.ID,
			OrgID:     audit.OrgID,
			ProjectID: audit.ProjectID,
			Type:      audit.Type,
			Message:   audit.Message,
			CreatedAt: audit.CreatedAt,
		})
		if len(items) >= 20 {
			break
		}
	}

	return &response.AuditListResp{Items: items}, nil
}

func (s *AnalyticsService) ReportApplyResult(ctx context.Context, req *request.ApplyResultReq) (*response.ApplyResultResp, error) {
	_ = ctx

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	op, ok := s.AnalyticsStore.applyOperations[req.ApplyID]
	if !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown apply_id"))
	}

	now := time.Now().UTC()
	op.AppliedAt = &now
	op.AppliedFile = req.AppliedFile
	op.AppliedSettings = cloneAnyMap(req.AppliedSettings)
	op.Note = req.Note
	op.RolledBack = req.RolledBack
	switch {
	case req.RolledBack:
		op.Status = "rollback_confirmed"
	case req.Success:
		op.Status = "applied"
	default:
		op.Status = "failed"
	}

	project := s.AnalyticsStore.projects[op.ProjectID]
	s.appendAuditLocked(project.OrgID, op.ProjectID, "recommendation.apply_result", op.Status)
	if err := s.AnalyticsStore.persistLocked(); err != nil {
		return nil, err
	}

	return &response.ApplyResultResp{
		ApplyID:    op.ID,
		Status:     op.Status,
		AppliedAt:  now,
		RolledBack: op.RolledBack,
	}, nil
}

func (s *AnalyticsService) refreshRecommendationsLocked(project *Project) []*Recommendation {
	previousIDs := s.AnalyticsStore.projectRecommendations[project.ID]
	for _, id := range previousIDs {
		if rec, ok := s.AnalyticsStore.recommendations[id]; ok && rec.Status == "active" {
			rec.Status = "superseded"
		}
	}

	sessions := s.AnalyticsStore.sessionSummaries[project.ID]
	if len(sessions) == 0 {
		s.AnalyticsStore.projectRecommendations[project.ID] = nil
		return nil
	}

	latestTool := project.DefaultTool
	if latestTool == "" {
		latestTool = sessions[len(sessions)-1].Tool
	}

	var (
		prompts      int
		toolCalls    int
		bashCalls    int
		readOps      int
		editOps      int
		retries      int
		rejects      int
		largeRepoHit int
		taskCounts   = map[string]int{}
	)

	for _, session := range sessions {
		prompts += maxInt(session.TotalPromptsCount, 1)
		toolCalls += session.TotalToolCalls
		bashCalls += session.BashCallsCount
		readOps += session.ReadOps
		editOps += session.EditOps
		retries += session.RetryCount
		rejects += session.PermissionRejectCount
		taskCounts[strings.ToLower(session.TaskType)]++
		if session.RepoSizeBucket == "large" || session.RepoSizeBucket == "xlarge" {
			largeRepoHit++
		}
	}

	dominantTask := dominantTask(taskCounts)
	readShare := safeDiv(float64(readOps), float64(maxInt(readOps+editOps, 1)))
	bashShare := safeDiv(float64(bashCalls), float64(maxInt(toolCalls, 1)))
	rejectRate := safeDiv(float64(rejects), float64(maxInt(toolCalls, 1)))
	retryRate := safeDiv(float64(retries), float64(maxInt(prompts, 1)))

	candidates := make([]*Recommendation, 0, 4)
	if largeRepoHit > 0 || dominantTask == "repo-qna" || readShare > 0.58 {
		candidates = append(candidates, s.newRecommendationLocked(project.ID, latestTool, recommendationTemplate{
			Kind:           "repo-qna-pack",
			Title:          "Adopt repo exploration instruction pack",
			Summary:        "Large-repo exploration is frequent, so a retrieval-oriented pack should reduce search churn.",
			Reason:         "The project shows heavy read-oriented sessions or large-repo behavior.",
			ExpectedImpact: "Lower tokens per query and faster repo Q&A turn-around.",
			Score:          0.86,
			Settings: map[string]any{
				"instructions_pack":   "repo-qna",
				"retrieval_mode":      "hierarchical",
				"context_window_hint": "large-repo",
			},
		}))
	}
	if bashShare > 0.2 || rejectRate > 0.05 {
		candidates = append(candidates, s.newRecommendationLocked(project.ID, latestTool, recommendationTemplate{
			Kind:           "shell-safe-profile",
			Title:          "Enable shell-safe execution profile",
			Summary:        "Bash activity and permission denials suggest the current shell policy is too noisy.",
			Reason:         "Frequent shell usage or rejected commands usually benefit from tighter guardrails and scoped approvals.",
			ExpectedImpact: "Lower permission rejection rate and fewer retries around shell automation.",
			Score:          0.82,
			Settings: map[string]any{
				"shell_profile":   "safe",
				"bash_guardrails": "strict",
				"approval_scope":  "scoped",
			},
		}))
	}
	if dominantTask == "bugfix" || dominantTask == "test" || retryRate > 0.12 {
		candidates = append(candidates, s.newRecommendationLocked(project.ID, latestTool, recommendationTemplate{
			Kind:           "post-edit-test-hook",
			Title:          "Add post-edit test hook",
			Summary:        "Bugfix and retry-heavy sessions benefit from immediate validation after code edits.",
			Reason:         "The session mix points to verification-heavy workflows.",
			ExpectedImpact: "Higher inferred accept rate and fewer repeated fix attempts.",
			Score:          0.79,
			Settings: map[string]any{
				"post_edit_hook":   "go test ./...",
				"hook_timeout_sec": 120,
			},
		}))
	}
	if len(candidates) == 0 {
		candidates = append(candidates, s.newRecommendationLocked(project.ID, latestTool, recommendationTemplate{
			Kind:           "measurement-baseline",
			Title:          "Keep observing with baseline profile",
			Summary:        "The current signal is still thin, so keep measurement active before pushing a stronger recommendation.",
			Reason:         "Not enough differentiated behavior yet.",
			ExpectedImpact: "Improved confidence for the next recommendation cycle.",
			Score:          0.55,
			Settings: map[string]any{
				"metrics_sampling":    "session-summary-only",
				"recommendation_mode": "observe",
			},
		}))
	}

	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.ID)
		s.AnalyticsStore.recommendations[candidate.ID] = candidate
	}
	s.AnalyticsStore.projectRecommendations[project.ID] = ids

	return candidates
}

type recommendationTemplate struct {
	Kind           string
	Title          string
	Summary        string
	Reason         string
	ExpectedImpact string
	Score          float64
	Settings       map[string]any
}

func (s *AnalyticsService) newRecommendationLocked(projectID, tool string, tpl recommendationTemplate) *Recommendation {
	return &Recommendation{
		ID:              s.AnalyticsStore.nextID("rec"),
		ProjectID:       projectID,
		Kind:            tpl.Kind,
		Title:           tpl.Title,
		Summary:         tpl.Summary,
		Reason:          tpl.Reason,
		ExpectedImpact:  tpl.ExpectedImpact,
		Score:           tpl.Score,
		Status:          "active",
		TargetTool:      tool,
		TargetFileHint:  targetFileHint(tool),
		SettingsUpdates: cloneAnyMap(tpl.Settings),
		CreatedAt:       time.Now().UTC(),
	}
}

func buildPatchPreview(rec *Recommendation) []PatchPreview {
	return []PatchPreview{
		{
			FilePath:        rec.TargetFileHint,
			Summary:         rec.Title,
			SettingsUpdates: cloneAnyMap(rec.SettingsUpdates),
		},
	}
}

func dominantTask(counts map[string]int) string {
	bestTask := "observe"
	bestCount := -1
	for task, count := range counts {
		if count > bestCount {
			bestTask = task
			bestCount = count
		}
	}
	return bestTask
}

func targetFileHint(tool string) string {
	switch strings.ToLower(tool) {
	case "claude", "claude-code":
		return ".claude/settings.local.json"
	default:
		return ".codex/config.json"
	}
}

func toRecommendationResp(rec *Recommendation) response.RecommendationResp {
	return response.RecommendationResp{
		ID:              rec.ID,
		ProjectID:       rec.ProjectID,
		Kind:            rec.Kind,
		Title:           rec.Title,
		Summary:         rec.Summary,
		Reason:          rec.Reason,
		ExpectedImpact:  rec.ExpectedImpact,
		Status:          rec.Status,
		Score:           rec.Score,
		TargetTool:      rec.TargetTool,
		TargetFileHint:  rec.TargetFileHint,
		SettingsUpdates: cloneAnyMap(rec.SettingsUpdates),
		CreatedAt:       rec.CreatedAt,
	}
}

func toPatchPreviewResp(items []PatchPreview) []response.PatchPreviewItem {
	out := make([]response.PatchPreviewItem, 0, len(items))
	for _, item := range items {
		out = append(out, response.PatchPreviewItem{
			FilePath:        item.FilePath,
			Summary:         item.Summary,
			SettingsUpdates: cloneAnyMap(item.SettingsUpdates),
		})
	}
	return out
}

func (s *AnalyticsService) appendAuditLocked(orgID, projectID, eventType, message string) {
	s.AnalyticsStore.audits = append(s.AnalyticsStore.audits, &AuditEvent{
		ID:        s.AnalyticsStore.nextID("audit"),
		OrgID:     orgID,
		ProjectID: projectID,
		Type:      eventType,
		Message:   message,
		CreatedAt: time.Now().UTC(),
	})
}

func round(in float64) float64 {
	return math.Round(in*100) / 100
}

func safeDiv(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return round(a / b)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func splitSessionsByApplyTime(sessions []*SessionSummary, appliedAt time.Time) ([]*SessionSummary, []*SessionSummary) {
	before := make([]*SessionSummary, 0)
	after := make([]*SessionSummary, 0)
	for _, session := range sessions {
		if session.Timestamp.Before(appliedAt) {
			before = append(before, session)
			continue
		}
		after = append(after, session)
	}
	return before, after
}

func summarizeSessions(sessions []*SessionSummary) (float64, float64, float64) {
	if len(sessions) == 0 {
		return 0, 0, 0
	}

	var (
		totalCost   float64
		totalRetry  float64
		totalReject float64
	)
	for _, session := range sessions {
		totalCost += session.EstimatedCost
		totalRetry += safeDiv(float64(session.RetryCount), float64(maxInt(session.TotalPromptsCount, 1)))
		totalReject += safeDiv(float64(session.PermissionRejectCount), float64(maxInt(session.TotalToolCalls, 1)))
	}

	count := float64(len(sessions))
	return round(totalCost / count), round(totalRetry / count), round(totalReject / count)
}

func interpretImpact(beforeCost, afterCost, beforeRetry, afterRetry, beforeReject, afterReject float64, afterCount int) string {
	if afterCount == 0 {
		return "Waiting for post-apply sessions."
	}

	improvements := 0
	if afterCost < beforeCost || beforeCost == 0 {
		improvements++
	}
	if afterRetry < beforeRetry || beforeRetry == 0 {
		improvements++
	}
	if afterReject < beforeReject || beforeReject == 0 {
		improvements++
	}

	switch {
	case improvements >= 3:
		return "Positive early signal across cost, retry, and rejection metrics."
	case improvements == 2:
		return "Mixed but promising signal after rollout."
	case improvements == 1:
		return "Weak signal so far; collect more sessions."
	default:
		return "No improvement detected yet."
	}
}
