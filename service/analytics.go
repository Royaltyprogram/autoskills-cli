package service

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/liushuangls/go-server-template/dto/request"
	"github.com/liushuangls/go-server-template/dto/response"
	"github.com/liushuangls/go-server-template/pkg/ecode"
)

type AnalyticsService struct {
	Options
	researchAgent *CloudResearchAgent
}

func NewAnalyticsService(opt Options) *AnalyticsService {
	return &AnalyticsService{
		Options:       opt,
		researchAgent: NewCloudResearchAgentPlaceholder(opt.Config),
	}
}

func (s *AnalyticsService) RegisterAgent(ctx context.Context, req *request.RegisterAgentReq) (*response.AgentRegistrationResp, error) {
	_ = ctx

	now := time.Now().UTC()

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	deviceID := req.DeviceID
	if deviceID == "" {
		deviceID = req.AgentID
	}
	if deviceID == "" {
		deviceID = s.AnalyticsStore.nextID("device")
	}
	if len(req.ConsentScopes) == 0 {
		req.ConsentScopes = []string{"config_snapshot", "session_summary", "execution_result"}
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
	s.AnalyticsStore.agents[deviceID] = &Agent{
		ID:            deviceID,
		OrgID:         req.OrgID,
		UserID:        req.UserID,
		DeviceName:    req.DeviceName,
		Hostname:      req.Hostname,
		Platform:      req.Platform,
		CLIVersion:    req.CLIVersion,
		Tools:         append([]string(nil), req.Tools...),
		ConsentScopes: append([]string(nil), req.ConsentScopes...),
		RegisteredAt:  now,
	}

	s.appendAuditLocked(req.OrgID, "", "device.registered", "local cli device registered")
	if err := s.AnalyticsStore.persistLocked(); err != nil {
		return nil, err
	}

	return &response.AgentRegistrationResp{
		AgentID:       deviceID,
		DeviceID:      deviceID,
		OrgID:         req.OrgID,
		UserID:        req.UserID,
		Status:        "registered",
		ConsentScopes: append([]string(nil), req.ConsentScopes...),
		RegisteredAt:  now,
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

	s.appendAuditLocked(req.OrgID, projectID, "project.connected", "project connected to aiops workspace")
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
		ID:                  s.AnalyticsStore.nextID("snapshot"),
		ProjectID:           req.ProjectID,
		Tool:                req.Tool,
		ProfileID:           profileID,
		Settings:            cloneAnyMap(req.Settings),
		EnabledMCPCount:     req.EnabledMCPCount,
		HooksEnabled:        req.HooksEnabled,
		InstructionFiles:    cloneStringSlice(req.InstructionFiles),
		ConfigFingerprint:   req.ConfigFingerprint,
		RecentConfigChanges: cloneStringSlice(req.RecentConfigChanges),
		CapturedAt:          capturedAt,
	}

	s.AnalyticsStore.configSnapshots[req.ProjectID] = append(s.AnalyticsStore.configSnapshots[req.ProjectID], snapshot)
	project.LastProfileID = profileID
	project.LastIngestedAt = &capturedAt

	s.appendAuditLocked(project.OrgID, req.ProjectID, "config.snapshot", "config snapshot uploaded from local collector")
	if err := s.AnalyticsStore.persistLocked(); err != nil {
		return nil, err
	}

	return &response.ConfigSnapshotResp{
		SnapshotID:        snapshot.ID,
		ProjectID:         req.ProjectID,
		ProfileID:         profileID,
		ConfigFingerprint: snapshot.ConfigFingerprint,
		CapturedAt:        capturedAt,
	}, nil
}

func (s *AnalyticsService) ListConfigSnapshots(ctx context.Context, req *request.ConfigSnapshotListReq) (*response.ConfigSnapshotListResp, error) {
	_ = ctx

	s.AnalyticsStore.mu.RLock()
	defer s.AnalyticsStore.mu.RUnlock()

	if _, ok := s.AnalyticsStore.projects[req.ProjectID]; !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown project_id"))
	}

	items := make([]response.ConfigSnapshotItem, 0, len(s.AnalyticsStore.configSnapshots[req.ProjectID]))
	for _, snapshot := range s.AnalyticsStore.configSnapshots[req.ProjectID] {
		items = append(items, response.ConfigSnapshotItem{
			ID:                  snapshot.ID,
			ProjectID:           snapshot.ProjectID,
			Tool:                snapshot.Tool,
			ProfileID:           snapshot.ProfileID,
			Settings:            cloneAnyMap(snapshot.Settings),
			EnabledMCPCount:     snapshot.EnabledMCPCount,
			HooksEnabled:        snapshot.HooksEnabled,
			InstructionFiles:    cloneStringSlice(snapshot.InstructionFiles),
			ConfigFingerprint:   snapshot.ConfigFingerprint,
			RecentConfigChanges: cloneStringSlice(snapshot.RecentConfigChanges),
			CapturedAt:          snapshot.CapturedAt,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CapturedAt.Equal(items[j].CapturedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].CapturedAt.After(items[j].CapturedAt)
	})

	return &response.ConfigSnapshotListResp{Items: items}, nil
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
		ID:                       sessionID,
		ProjectID:                req.ProjectID,
		Tool:                     req.Tool,
		ProjectHash:              req.ProjectHash,
		LanguageMix:              cloneFloatMap(req.LanguageMix),
		TotalPromptsCount:        req.TotalPromptsCount,
		TotalToolCalls:           req.TotalToolCalls,
		BashCallsCount:           req.BashCallsCount,
		ReadOps:                  req.ReadOps,
		EditOps:                  req.EditOps,
		WriteOps:                 req.WriteOps,
		MCPUsageCount:            req.MCPUsageCount,
		PermissionRejectCount:    req.PermissionRejectCount,
		RetryCount:               req.RetryCount,
		TokenIn:                  req.TokenIn,
		TokenOut:                 req.TokenOut,
		RawQueries:               cloneStringSlice(req.RawQueries),
		EstimatedCost:            req.EstimatedCost,
		TaskType:                 req.TaskType,
		RepoSizeBucket:           req.RepoSizeBucket,
		ConfigProfileID:          req.ConfigProfileID,
		TaskTypeDistribution:     cloneFloatMap(req.TaskTypeDistribution),
		RepoExplorationIntensity: req.RepoExplorationIntensity,
		ShellHeavy:               req.ShellHeavy,
		WorkloadTags:             cloneStringSlice(req.WorkloadTags),
		AcceptanceProxy:          req.AcceptanceProxy,
		EventSummaries:           cloneStringSlice(req.EventSummaries),
		Timestamp:                recordedAt,
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

	s.appendAuditLocked(project.OrgID, req.ProjectID, "session.ingested", "session summary uploaded from local collector")
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

func (s *AnalyticsService) ListSessionSummaries(ctx context.Context, req *request.SessionSummaryListReq) (*response.SessionSummaryListResp, error) {
	_ = ctx

	s.AnalyticsStore.mu.RLock()
	defer s.AnalyticsStore.mu.RUnlock()

	if _, ok := s.AnalyticsStore.projects[req.ProjectID]; !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown project_id"))
	}

	limit := req.Limit
	if limit <= 0 || limit > 20 {
		limit = 5
	}

	items := make([]response.SessionSummaryItem, 0, len(s.AnalyticsStore.sessionSummaries[req.ProjectID]))
	for _, session := range s.AnalyticsStore.sessionSummaries[req.ProjectID] {
		items = append(items, response.SessionSummaryItem{
			ID:                       session.ID,
			ProjectID:                session.ProjectID,
			Tool:                     session.Tool,
			ProjectHash:              session.ProjectHash,
			LanguageMix:              cloneFloatMap(session.LanguageMix),
			TotalPromptsCount:        session.TotalPromptsCount,
			TotalToolCalls:           session.TotalToolCalls,
			BashCallsCount:           session.BashCallsCount,
			ReadOps:                  session.ReadOps,
			EditOps:                  session.EditOps,
			WriteOps:                 session.WriteOps,
			MCPUsageCount:            session.MCPUsageCount,
			PermissionRejectCount:    session.PermissionRejectCount,
			RetryCount:               session.RetryCount,
			TokenIn:                  session.TokenIn,
			TokenOut:                 session.TokenOut,
			RawQueries:               cloneStringSlice(session.RawQueries),
			EstimatedCost:            session.EstimatedCost,
			TaskType:                 session.TaskType,
			RepoSizeBucket:           session.RepoSizeBucket,
			ConfigProfileID:          session.ConfigProfileID,
			TaskTypeDistribution:     cloneFloatMap(session.TaskTypeDistribution),
			RepoExplorationIntensity: session.RepoExplorationIntensity,
			ShellHeavy:               session.ShellHeavy,
			WorkloadTags:             cloneStringSlice(session.WorkloadTags),
			AcceptanceProxy:          session.AcceptanceProxy,
			EventSummaries:           cloneStringSlice(session.EventSummaries),
			Timestamp:                session.Timestamp,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Timestamp.Equal(items[j].Timestamp) {
			return items[i].ID > items[j].ID
		}
		return items[i].Timestamp.After(items[j].Timestamp)
	})
	if len(items) > limit {
		items = items[:limit]
	}

	return &response.SessionSummaryListResp{Items: items}, nil
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
		totalFailedApply     int
		totalRollbacks       int
		totalPendingReview   int
		totalApprovedQueue   int
		totalAcceptProxy     float64
		lastIngestedAt       *time.Time
		taskCounts           = map[string]int{}
	)

	for _, projectID := range projectIDs {
		for _, session := range s.AnalyticsStore.sessionSummaries[projectID] {
			totalSessions++
			totalCost += session.EstimatedCost
			totalTokens += session.TokenIn + session.TokenOut
			totalQueries += queryCountForSession(session)
			totalToolCalls += session.TotalToolCalls
			totalRejects += session.PermissionRejectCount
			totalRetries += session.RetryCount
			totalAcceptProxy += session.AcceptanceProxy
			if taskType := strings.TrimSpace(session.TaskType); taskType != "" {
				taskCounts[taskType]++
			}
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
		if op.Status == "failed" {
			totalFailedApply++
		}
		if op.Status == "awaiting_review" {
			totalPendingReview++
		}
		if op.Status == "approved_for_local_apply" {
			totalApprovedQueue++
		}
		if op.RolledBack {
			totalRollbacks++
		}
	}

	topTaskTypes := sortedTaskBreakdown(taskCounts)
	primaryTaskType := ""
	if len(topTaskTypes) > 0 {
		primaryTaskType = topTaskTypes[0].TaskType
	}
	inferredAcceptRate := safeDiv(totalAcceptProxy, float64(maxInt(totalSessions, 1)))
	rollbackRate := safeDiv(float64(totalRollbacks), float64(maxInt(totalApplyOps, 1)))

	return &response.DashboardOverviewResp{
		OrgID:                   req.OrgID,
		TotalDevices:            countDevicesByOrg(s.AnalyticsStore.agents, req.OrgID),
		TotalProjects:           len(projectIDs),
		TotalSessions:           totalSessions,
		ActiveRecommendations:   totalActiveRecs,
		PendingReviewCount:      totalPendingReview,
		ApprovedQueueCount:      totalApprovedQueue,
		SuccessfulRolloutCount:  totalSuccessfulApply,
		FailedExecutionCount:    totalFailedApply,
		TotalEstimatedCost:      round(totalCost),
		AvgTokensPerQuery:       safeDiv(float64(totalTokens), float64(totalQueries)),
		AvgToolCallsPerQuery:    safeDiv(float64(totalToolCalls), float64(totalQueries)),
		PermissionRejectRate:    safeDiv(float64(totalRejects), float64(maxInt(totalToolCalls, 1))),
		RetryRate:               safeDiv(float64(totalRetries), float64(maxInt(totalQueries, 1))),
		RecommendationApplyRate: safeDiv(float64(totalSuccessfulApply), float64(maxInt(totalActiveRecs+totalSuccessfulApply, 1))),
		InferredAcceptRate:      inferredAcceptRate,
		RollbackRate:            rollbackRate,
		PrimaryTaskType:         primaryTaskType,
		ActionSummary:           buildDashboardActionSummary(primaryTaskType, totalPendingReview, totalApprovedQueue, totalActiveRecs),
		OutcomeSummary:          buildDashboardOutcomeSummary(totalSuccessfulApply, totalFailedApply, inferredAcceptRate, rollbackRate),
		ResearchProvider:        s.researchAgent.Provider,
		ResearchMode:            s.researchAgent.Mode,
		TopTaskTypes:            topTaskTypes,
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
	policyMode, policyReason := evaluateChangePlanPolicy(rec, scope, patchPreview)
	op := &ApplyOperation{
		ID:               s.AnalyticsStore.nextID("apply"),
		RecommendationID: req.RecommendationID,
		ProjectID:        rec.ProjectID,
		RequestedBy:      req.RequestedBy,
		Scope:            scope,
		Status:           "awaiting_review",
		PolicyMode:       policyMode,
		PolicyReason:     policyReason,
		ApprovalStatus:   "awaiting_review",
		Decision:         "pending",
		PatchPreview:     patchPreview,
		RequestedAt:      now,
	}
	if policyMode == "auto_approved" {
		op.Status = "approved_for_local_apply"
		op.ApprovalStatus = "approved"
		op.Decision = "auto_approved"
		op.ReviewedBy = "policy-engine"
		op.ReviewNote = policyReason
		op.ReviewedAt = &now
	}
	s.AnalyticsStore.applyOperations[op.ID] = op
	auditType := "change_plan.requested"
	auditMessage := "change plan created and waiting for review"
	if policyMode == "auto_approved" {
		auditType = "change_plan.auto_approved"
		auditMessage = "change plan auto-approved by policy engine"
	}
	s.appendAuditLocked(s.AnalyticsStore.projects[rec.ProjectID].OrgID, rec.ProjectID, auditType, auditMessage)
	if err := s.AnalyticsStore.persistLocked(); err != nil {
		return nil, err
	}

	return &response.ApplyPlanResp{
		ApplyID:        op.ID,
		Recommendation: toRecommendationResp(rec),
		Status:         op.Status,
		PolicyMode:     op.PolicyMode,
		PolicyReason:   op.PolicyReason,
		ApprovalStatus: op.ApprovalStatus,
		Decision:       op.Decision,
		ReviewedBy:     op.ReviewedBy,
		ReviewNote:     op.ReviewNote,
		ReviewedAt:     op.ReviewedAt,
		PatchPreview:   toPatchPreviewResp(patchPreview),
		RequestedAt:    now,
	}, nil
}

func (s *AnalyticsService) ListChangePlans(ctx context.Context, req *request.ChangePlanListReq) (*response.ApplyHistoryResp, error) {
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
		if req.Status != "" && op.Status != req.Status && op.ApprovalStatus != req.Status {
			continue
		}
		if req.UserID != "" && op.RequestedBy != req.UserID && op.ReviewedBy != req.UserID {
			continue
		}
		items = append(items, toApplyHistoryItem(op))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].RequestedAt.Equal(items[j].RequestedAt) {
			return items[i].ApplyID > items[j].ApplyID
		}
		return items[i].RequestedAt.After(items[j].RequestedAt)
	})

	return &response.ApplyHistoryResp{Items: items}, nil
}

func (s *AnalyticsService) ReviewChangePlan(ctx context.Context, req *request.ReviewChangePlanReq) (*response.ChangePlanReviewResp, error) {
	_ = ctx

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	op, ok := s.AnalyticsStore.applyOperations[req.ApplyID]
	if !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown apply_id"))
	}

	decision := strings.ToLower(strings.TrimSpace(req.Decision))
	if decision != "approve" && decision != "approved" && decision != "reject" && decision != "rejected" {
		return nil, ecode.InvalidParams.WithCause(ecode.NewInvalidParamsErr("decision must be approve or reject"))
	}

	now := time.Now().UTC()
	op.ReviewedBy = req.ReviewedBy
	op.ReviewNote = req.ReviewNote
	op.ReviewedAt = &now

	switch decision {
	case "approve", "approved":
		op.Decision = "approved"
		op.ApprovalStatus = "approved"
		op.Status = "approved_for_local_apply"
		s.appendAuditLocked(s.AnalyticsStore.projects[op.ProjectID].OrgID, op.ProjectID, "change_plan.approved", "change plan approved for local apply")
	default:
		op.Decision = "rejected"
		op.ApprovalStatus = "rejected"
		op.Status = "rejected"
		s.appendAuditLocked(s.AnalyticsStore.projects[op.ProjectID].OrgID, op.ProjectID, "change_plan.rejected", "change plan rejected during review")
	}

	if err := s.AnalyticsStore.persistLocked(); err != nil {
		return nil, err
	}

	return &response.ChangePlanReviewResp{
		ApplyID:        op.ID,
		Status:         op.Status,
		PolicyMode:     op.PolicyMode,
		PolicyReason:   op.PolicyReason,
		ApprovalStatus: op.ApprovalStatus,
		Decision:       op.Decision,
		ReviewedBy:     op.ReviewedBy,
		ReviewNote:     op.ReviewNote,
		ReviewedAt:     op.ReviewedAt,
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
		items = append(items, toApplyHistoryItem(op))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].RequestedAt.Equal(items[j].RequestedAt) {
			return items[i].ApplyID > items[j].ApplyID
		}
		return items[i].RequestedAt.After(items[j].RequestedAt)
	})

	return &response.ApplyHistoryResp{Items: items}, nil
}

func (s *AnalyticsService) PendingApplies(ctx context.Context, req *request.PendingApplyReq) (*response.PendingApplyResp, error) {
	_ = ctx

	s.AnalyticsStore.mu.RLock()
	defer s.AnalyticsStore.mu.RUnlock()

	if _, ok := s.AnalyticsStore.projects[req.ProjectID]; !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown project_id"))
	}

	items := make([]response.PendingApplyItem, 0)
	for _, op := range s.AnalyticsStore.applyOperations {
		if op.ProjectID != req.ProjectID || op.Status != "approved_for_local_apply" {
			continue
		}
		switch op.Scope {
		case "project", "team", "org":
		case "user", "":
			if req.UserID == "" || op.RequestedBy != req.UserID {
				continue
			}
		default:
			if req.UserID == "" || op.RequestedBy != req.UserID {
				continue
			}
		}
		items = append(items, response.PendingApplyItem{
			ApplyID:          op.ID,
			RecommendationID: op.RecommendationID,
			Status:           op.Status,
			PolicyMode:       op.PolicyMode,
			PolicyReason:     op.PolicyReason,
			ApprovalStatus:   op.ApprovalStatus,
			Scope:            op.Scope,
			RequestedBy:      op.RequestedBy,
			RequestedAt:      op.RequestedAt,
			PatchPreview:     toPatchPreviewResp(op.PatchPreview),
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].RequestedAt.Equal(items[j].RequestedAt) {
			return items[i].ApplyID > items[j].ApplyID
		}
		return items[i].RequestedAt.After(items[j].RequestedAt)
	})

	return &response.PendingApplyResp{Items: items}, nil
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
	op.AppliedText = req.AppliedText
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
	if op.ApprovalStatus == "" {
		op.ApprovalStatus = "approved"
	}

	project := s.AnalyticsStore.projects[op.ProjectID]
	s.appendAuditLocked(project.OrgID, op.ProjectID, "execution.result", op.Status)
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
	rawCandidates := s.researchAgent.AnalyzeProject(project, sessions, s.AnalyticsStore.configSnapshots[project.ID])
	candidates := make([]*Recommendation, 0, len(rawCandidates))
	for _, candidate := range rawCandidates {
		candidates = append(candidates, s.newRecommendationLocked(project, candidate))
	}

	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.ID)
		s.AnalyticsStore.recommendations[candidate.ID] = candidate
	}
	s.AnalyticsStore.projectRecommendations[project.ID] = ids

	return candidates
}

func (s *AnalyticsService) newRecommendationLocked(project *Project, tpl researchRecommendation) *Recommendation {
	tool := project.DefaultTool
	if strings.TrimSpace(tool) == "" {
		tool = "codex"
	}
	targetFile := targetFileHint(tool)
	if len(tpl.Steps) > 0 && tpl.Steps[0].TargetFile != "" {
		targetFile = tpl.Steps[0].TargetFile
	}
	return &Recommendation{
		ID:               s.AnalyticsStore.nextID("rec"),
		ProjectID:        project.ID,
		Kind:             tpl.Kind,
		Title:            tpl.Title,
		Summary:          tpl.Summary,
		Reason:           tpl.Reason,
		Explanation:      tpl.Explanation,
		ExpectedBenefit:  tpl.ExpectedBenefit,
		Risk:             tpl.Risk,
		ExpectedImpact:   tpl.ExpectedImpact,
		Score:            tpl.Score,
		Status:           "active",
		TargetTool:       tool,
		TargetFileHint:   targetFile,
		ResearchProvider: s.researchAgent.Provider,
		ResearchModel:    s.researchAgent.Model,
		Evidence:         cloneStringSlice(tpl.Evidence),
		ChangePlan:       cloneChangePlanSteps(tpl.Steps),
		SettingsUpdates:  cloneAnyMap(tpl.Settings),
		CreatedAt:        time.Now().UTC(),
	}
}

func buildPatchPreview(rec *Recommendation) []PatchPreview {
	if len(rec.ChangePlan) == 0 {
		return []PatchPreview{{
			FilePath:        rec.TargetFileHint,
			Operation:       "merge_patch",
			Summary:         rec.Title,
			SettingsUpdates: cloneAnyMap(rec.SettingsUpdates),
		}}
	}
	out := make([]PatchPreview, 0, len(rec.ChangePlan))
	for _, step := range rec.ChangePlan {
		out = append(out, PatchPreview{
			FilePath:        step.TargetFile,
			Operation:       step.Action,
			Summary:         step.Summary,
			SettingsUpdates: cloneAnyMap(step.SettingsUpdates),
			ContentPreview:  step.ContentPreview,
		})
	}
	return out
}

func evaluateChangePlanPolicy(rec *Recommendation, scope string, preview []PatchPreview) (string, string) {
	if strings.ToLower(scope) != "user" {
		return "requires_review", "non-user scope rollout requires explicit approval"
	}
	if !isLowRiskRecommendation(rec.Risk) {
		return "requires_review", "recommendation risk is not low"
	}
	if len(preview) != 1 {
		return "requires_review", "multi-step plans require human review"
	}
	step := preview[0]
	if step.Operation != "merge_patch" {
		return "requires_review", "non-merge patch operations require human review"
	}
	if !isPolicyAutoApproveTarget(step.FilePath) {
		return "requires_review", "target file is outside the auto-approve policy scope"
	}
	return "auto_approved", "low-risk single-file config merge qualifies for automatic approval"
}

func isLowRiskRecommendation(risk string) bool {
	risk = strings.ToLower(strings.TrimSpace(risk))
	return strings.HasPrefix(risk, "low") && !strings.Contains(risk, "medium")
}

func isPolicyAutoApproveTarget(target string) bool {
	switch filepath.Clean(target) {
	case filepath.Clean(".codex/config.json"), filepath.Clean(".claude/settings.local.json"):
		return true
	default:
		return false
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
		ID:               rec.ID,
		ProjectID:        rec.ProjectID,
		Kind:             rec.Kind,
		Title:            rec.Title,
		Summary:          rec.Summary,
		Reason:           rec.Reason,
		Explanation:      rec.Explanation,
		ExpectedBenefit:  rec.ExpectedBenefit,
		Risk:             rec.Risk,
		ExpectedImpact:   rec.ExpectedImpact,
		Status:           rec.Status,
		Score:            rec.Score,
		TargetTool:       rec.TargetTool,
		TargetFileHint:   rec.TargetFileHint,
		ResearchProvider: rec.ResearchProvider,
		ResearchModel:    rec.ResearchModel,
		Evidence:         cloneStringSlice(rec.Evidence),
		ChangePlan:       toChangePlanResp(rec.ChangePlan),
		SettingsUpdates:  cloneAnyMap(rec.SettingsUpdates),
		CreatedAt:        rec.CreatedAt,
	}
}

func toPatchPreviewResp(items []PatchPreview) []response.PatchPreviewItem {
	out := make([]response.PatchPreviewItem, 0, len(items))
	for _, item := range items {
		out = append(out, response.PatchPreviewItem{
			FilePath:        item.FilePath,
			Operation:       item.Operation,
			Summary:         item.Summary,
			SettingsUpdates: cloneAnyMap(item.SettingsUpdates),
			ContentPreview:  item.ContentPreview,
		})
	}
	return out
}

func toChangePlanResp(items []ChangePlanStep) []response.ChangePlanStepResp {
	out := make([]response.ChangePlanStepResp, 0, len(items))
	for _, item := range items {
		out = append(out, response.ChangePlanStepResp{
			Type:            item.Type,
			Action:          item.Action,
			TargetFile:      item.TargetFile,
			Summary:         item.Summary,
			SettingsUpdates: cloneAnyMap(item.SettingsUpdates),
			ContentPreview:  item.ContentPreview,
		})
	}
	return out
}

func toApplyHistoryItem(op *ApplyOperation) response.ApplyHistoryItem {
	return response.ApplyHistoryItem{
		ApplyID:          op.ID,
		RecommendationID: op.RecommendationID,
		Status:           op.Status,
		PolicyMode:       op.PolicyMode,
		PolicyReason:     op.PolicyReason,
		ApprovalStatus:   op.ApprovalStatus,
		Decision:         op.Decision,
		Scope:            op.Scope,
		RequestedBy:      op.RequestedBy,
		ReviewedBy:       op.ReviewedBy,
		ReviewNote:       op.ReviewNote,
		RequestedAt:      op.RequestedAt,
		ReviewedAt:       op.ReviewedAt,
		AppliedAt:        op.AppliedAt,
		AppliedFile:      op.AppliedFile,
		AppliedSettings:  cloneAnyMap(op.AppliedSettings),
		AppliedText:      op.AppliedText,
		PatchPreview:     toPatchPreviewResp(op.PatchPreview),
		RolledBack:       op.RolledBack,
	}
}

func countDevicesByOrg(devices map[string]*Agent, orgID string) int {
	total := 0
	for _, device := range devices {
		if device.OrgID == orgID {
			total++
		}
	}
	return total
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

func queryCountForSession(session *SessionSummary) int {
	if session == nil {
		return 1
	}
	if len(session.RawQueries) > 0 {
		return len(session.RawQueries)
	}
	return maxInt(session.TotalPromptsCount, 1)
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
		totalRetry += safeDiv(float64(session.RetryCount), float64(queryCountForSession(session)))
		totalReject += safeDiv(float64(session.PermissionRejectCount), float64(maxInt(session.TotalToolCalls, 1)))
	}

	count := float64(len(sessions))
	return round(totalCost / count), round(totalRetry / count), round(totalReject / count)
}

func interpretImpact(beforeCost, afterCost, beforeRetry, afterRetry, beforeReject, afterReject float64, afterCount int) string {
	if afterCount == 0 {
		return "Waiting for post-apply sessions."
	}
	if beforeCost == 0 && afterCost == 0 && beforeRetry == 0 && afterRetry == 0 && beforeReject == 0 && afterReject == 0 {
		return "The MVP is collecting follow-up usage, but impact deltas still need richer operational metrics."
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

func buildDashboardActionSummary(primaryTaskType string, pendingReview, approvedQueue, activeRecommendations int) string {
	taskHint := ""
	if primaryTaskType != "" {
		taskHint = " for " + strings.ReplaceAll(primaryTaskType, "-", " ") + " work"
	}
	switch {
	case pendingReview > 0 && approvedQueue > 0:
		return fmt.Sprintf("%d plan(s) need approval and %d more are ready for the next local sync.", pendingReview, approvedQueue)
	case pendingReview > 0:
		return fmt.Sprintf("%d plan(s) are waiting for approval.", pendingReview)
	case approvedQueue > 0:
		return fmt.Sprintf("%d approved plan(s) are ready for the next local sync.", approvedQueue)
	case activeRecommendations > 0:
		return fmt.Sprintf("%d recommendation(s) are ready to review%s.", activeRecommendations, taskHint)
	default:
		return "No rollout action is waiting right now."
	}
}

func buildDashboardOutcomeSummary(successfulRollouts, failedExecutions int, inferredAcceptRate, rollbackRate float64) string {
	switch {
	case failedExecutions > 0:
		return fmt.Sprintf("%d rollout(s) need attention after failing local execution.", failedExecutions)
	case successfulRollouts == 0:
		return "No completed rollouts yet. Approve a change to start measuring outcomes."
	case rollbackRate >= 0.25:
		return "Recent rollouts are being reversed too often. Narrow the next change scope."
	case inferredAcceptRate >= 0.75:
		return "Recent changes are largely sticking after rollout."
	case inferredAcceptRate >= 0.5:
		return "Recent changes look promising, but more usage is needed to confirm the effect."
	default:
		return "Recent changes are not sticking consistently yet."
	}
}
