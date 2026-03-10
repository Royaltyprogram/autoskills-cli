package service

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Royaltyprogram/aiops/dto/request"
	"github.com/Royaltyprogram/aiops/dto/response"
	"github.com/Royaltyprogram/aiops/pkg/ecode"
)

type AnalyticsService struct {
	Options
	researchAgent *CloudResearchAgent
}

const sharedWorkspaceName = "Shared workspace"

func NewAnalyticsService(opt Options) *AnalyticsService {
	return &AnalyticsService{
		Options:       opt,
		researchAgent: NewCloudResearchAgent(opt.Config),
	}
}

func (s *AnalyticsService) Login(ctx context.Context, req *request.LoginReq) (*response.LoginResp, error) {
	_ = ctx

	now := time.Now().UTC()

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	user := s.AnalyticsStore.findUserByEmailLocked(req.Email)
	if !verifyPassword(user, req.Password) {
		return nil, ecode.Unauthorized(1002, "invalid email or password")
	}

	org, ok := s.AnalyticsStore.organizations[user.OrgID]
	if !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown org_id"))
	}

	tokenValue, tokenRecord, err := s.AnalyticsStore.issueAccessTokenLocked(TokenKindWebSession, user.OrgID, user.ID, "dashboard session", defaultSessionTokenTTL, now)
	if err != nil {
		return nil, err
	}

	user.LastLoginAt = &now
	s.appendAuditLocked(user.OrgID, "", "auth.login", "dashboard session created")
	if err := s.AnalyticsStore.persistLocked(); err != nil {
		return nil, err
	}

	return &response.LoginResp{
		SessionToken:     tokenValue,
		SessionExpiresAt: tokenRecord.ExpiresAt,
		User:             toSessionUser(user),
		Organization:     toSessionOrganization(org),
	}, nil
}

func (s *AnalyticsService) CurrentSession(ctx context.Context) (*response.AuthSessionResp, error) {
	identity, err := s.requireUserIdentity(ctx, TokenKindWebSession, TokenKindCLI)
	if err != nil {
		return nil, err
	}

	s.AnalyticsStore.mu.RLock()
	defer s.AnalyticsStore.mu.RUnlock()

	user, ok := s.AnalyticsStore.users[identity.UserID]
	if !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown user_id"))
	}
	org, ok := s.AnalyticsStore.organizations[identity.OrgID]
	if !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown org_id"))
	}

	return &response.AuthSessionResp{
		User:         toSessionUser(user),
		Organization: toSessionOrganization(org),
	}, nil
}

func (s *AnalyticsService) Logout(ctx context.Context) (*response.LogoutResp, error) {
	identity, err := s.requireUserIdentity(ctx, TokenKindWebSession, TokenKindCLI)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	record, ok := s.AnalyticsStore.accessTokens[identity.TokenID]
	if !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown token_id"))
	}
	record.RevokedAt = &now
	s.appendAuditLocked(identity.OrgID, "", "auth.logout", "dashboard session revoked")
	if err := s.AnalyticsStore.persistLocked(); err != nil {
		return nil, err
	}

	return &response.LogoutResp{
		Status:    "signed_out",
		RevokedAt: now,
	}, nil
}

func (s *AnalyticsService) IssueCLIToken(ctx context.Context, req *request.IssueCLITokenReq) (*response.CLITokenIssueResp, error) {
	identity, err := s.requireUserIdentity(ctx, TokenKindWebSession)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	if _, ok := s.AnalyticsStore.users[identity.UserID]; !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown user_id"))
	}

	label := strings.TrimSpace(req.Label)
	if label == "" {
		label = "CLI login token"
	}

	tokenValue, tokenRecord, err := s.AnalyticsStore.issueAccessTokenLocked(TokenKindCLI, identity.OrgID, identity.UserID, label, defaultCLITokenTTL, now)
	if err != nil {
		return nil, err
	}

	s.appendAuditLocked(identity.OrgID, "", "auth.cli_token_issued", "cli token issued from dashboard")
	if err := s.AnalyticsStore.persistLocked(); err != nil {
		return nil, err
	}

	return &response.CLITokenIssueResp{
		TokenID:     tokenRecord.ID,
		Token:       tokenValue,
		TokenPrefix: tokenRecord.TokenPrefix,
		Label:       tokenRecord.Label,
		CreatedAt:   tokenRecord.CreatedAt,
		ExpiresAt:   tokenRecord.ExpiresAt,
	}, nil
}

func (s *AnalyticsService) ListCLITokens(ctx context.Context) (*response.CLITokenListResp, error) {
	identity, err := s.requireUserIdentity(ctx, TokenKindWebSession)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()

	s.AnalyticsStore.mu.RLock()
	defer s.AnalyticsStore.mu.RUnlock()

	items := make([]response.CLITokenItemResp, 0)
	for _, record := range s.AnalyticsStore.accessTokens {
		if record.Kind != TokenKindCLI || record.OrgID != identity.OrgID || record.UserID != identity.UserID {
			continue
		}
		items = append(items, response.CLITokenItemResp{
			TokenID:     record.ID,
			TokenPrefix: record.TokenPrefix,
			Label:       record.Label,
			Status:      accessTokenStatus(record, now),
			CreatedAt:   record.CreatedAt,
			ExpiresAt:   record.ExpiresAt,
			LastUsedAt:  record.LastUsedAt,
			RevokedAt:   record.RevokedAt,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].TokenID > items[j].TokenID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})

	return &response.CLITokenListResp{Items: items}, nil
}

func (s *AnalyticsService) RevokeCLIToken(ctx context.Context, req *request.RevokeCLITokenReq) (*response.CLITokenRevokeResp, error) {
	identity, err := s.requireUserIdentity(ctx, TokenKindWebSession)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	record, ok := s.AnalyticsStore.accessTokens[req.TokenID]
	if !ok || record.Kind != TokenKindCLI {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown token_id"))
	}
	if record.OrgID != identity.OrgID || record.UserID != identity.UserID {
		return nil, ecode.Forbidden(1006, "token cannot be managed by this user")
	}
	record.RevokedAt = &now
	s.appendAuditLocked(identity.OrgID, "", "auth.cli_token_revoked", "cli token revoked from dashboard")
	if err := s.AnalyticsStore.persistLocked(); err != nil {
		return nil, err
	}

	return &response.CLITokenRevokeResp{
		TokenID:   record.ID,
		Status:    "revoked",
		RevokedAt: now,
	}, nil
}

func (s *AnalyticsService) AuthenticateCLI(ctx context.Context, req *request.CLILoginReq) (*response.CLILoginResp, error) {
	identity, err := s.requireUserIdentity(ctx, TokenKindCLI)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	user, ok := s.AnalyticsStore.users[identity.UserID]
	if !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown user_id"))
	}
	org, ok := s.AnalyticsStore.organizations[identity.OrgID]
	if !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown org_id"))
	}

	deviceID := firstNonEmpty(req.DeviceID, req.AgentID)
	if deviceID == "" {
		deviceID = s.AnalyticsStore.nextID("device")
	}

	consentScopes := append([]string(nil), req.ConsentScopes...)
	if len(consentScopes) == 0 {
		consentScopes = []string{"config_snapshot", "session_summary", "execution_result"}
	}
	if record, ok := s.AnalyticsStore.accessTokens[identity.TokenID]; ok {
		record.LastUsedAt = &now
	}

	s.AnalyticsStore.agents[deviceID] = &Agent{
		ID:            deviceID,
		OrgID:         identity.OrgID,
		UserID:        identity.UserID,
		DeviceName:    req.DeviceName,
		Hostname:      req.Hostname,
		Platform:      req.Platform,
		CLIVersion:    req.CLIVersion,
		Tools:         append([]string(nil), req.Tools...),
		ConsentScopes: consentScopes,
		RegisteredAt:  now,
	}

	s.appendAuditLocked(identity.OrgID, "", "device.registered", "local cli device registered")
	if err := s.AnalyticsStore.persistLocked(); err != nil {
		return nil, err
	}

	return &response.CLILoginResp{
		AgentID:       deviceID,
		DeviceID:      deviceID,
		OrgID:         org.ID,
		OrgName:       org.Name,
		UserID:        user.ID,
		UserName:      user.Name,
		UserEmail:     user.Email,
		Status:        "registered",
		ConsentScopes: consentScopes,
		RegisteredAt:  now,
	}, nil
}

func (s *AnalyticsService) requireUserIdentity(ctx context.Context, allowedKinds ...string) (AuthIdentity, error) {
	identity, ok := AuthIdentityFromContext(ctx)
	if !ok || identity.UserID == "" || identity.OrgID == "" {
		return AuthIdentity{}, ecode.Unauthorized(1003, "login is required")
	}
	if len(allowedKinds) > 0 && !stringInSlice(identity.TokenKind, allowedKinds) {
		return AuthIdentity{}, ecode.Forbidden(1004, "token type cannot access this route")
	}
	return identity, nil
}

func (s *AnalyticsService) authorizeOrg(ctx context.Context, orgID string) error {
	identity, ok := AuthIdentityFromContext(ctx)
	if !ok || identity.TokenKind == TokenKindStatic || identity.OrgID == "" {
		return nil
	}
	if identity.OrgID != orgID {
		return ecode.Forbidden(1005, "token cannot access this organization")
	}
	return nil
}

func (s *AnalyticsService) authorizeProject(ctx context.Context, project *Project) error {
	if project == nil {
		return ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown workspace"))
	}
	return s.authorizeOrg(ctx, project.OrgID)
}

func (s *AnalyticsService) findWorkspaceForOrgLocked(orgID string) *Project {
	orgID = strings.TrimSpace(orgID)
	if orgID == "" {
		return nil
	}

	var projects []*Project
	for _, project := range s.AnalyticsStore.projects {
		if project != nil && project.OrgID == orgID {
			projects = append(projects, project)
		}
	}
	workspace := latestProject(projects)
	if workspace != nil {
		workspace.Name = sharedWorkspaceName
	}
	return workspace
}

func (s *AnalyticsService) resolveProjectLocked(ctx context.Context, projectID string) (*Project, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID != "" {
		if project, ok := s.AnalyticsStore.projects[projectID]; ok {
			return project, nil
		}
	}

	identity, ok := AuthIdentityFromContext(ctx)
	if ok && identity.OrgID != "" {
		if workspace := s.findWorkspaceForOrgLocked(identity.OrgID); workspace != nil {
			return workspace, nil
		}
	}

	if projectID != "" {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown workspace"))
	}
	return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("no connected workspace"))
}

func (s *AnalyticsService) actorFromContext(ctx context.Context, fallback string) string {
	identity, ok := AuthIdentityFromContext(ctx)
	if !ok || identity.UserID == "" || identity.TokenKind == TokenKindStatic {
		return strings.TrimSpace(fallback)
	}
	return identity.UserID
}

func (s *AnalyticsService) RegisterAgent(ctx context.Context, req *request.RegisterAgentReq) (*response.AgentRegistrationResp, error) {
	now := time.Now().UTC()

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	if err := s.authorizeOrg(ctx, req.OrgID); err != nil {
		return nil, err
	}

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
	org := s.AnalyticsStore.organizations[req.OrgID]
	if org == nil {
		org = &Organization{ID: req.OrgID, Name: req.OrgName}
		s.AnalyticsStore.organizations[req.OrgID] = org
	} else if strings.TrimSpace(req.OrgName) != "" {
		org.Name = req.OrgName
	}

	user := s.AnalyticsStore.users[req.UserID]
	if user == nil {
		user = &User{
			ID:        req.UserID,
			OrgID:     req.OrgID,
			Email:     req.UserEmail,
			CreatedAt: now,
		}
		s.AnalyticsStore.users[req.UserID] = user
	} else {
		user.OrgID = req.OrgID
		if strings.TrimSpace(req.UserEmail) != "" {
			user.Email = req.UserEmail
		}
		if user.CreatedAt.IsZero() {
			user.CreatedAt = now
		}
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
	now := time.Now().UTC()

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	agent, ok := s.AnalyticsStore.agents[req.AgentID]
	if !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown agent_id"))
	}
	if err := s.authorizeOrg(ctx, req.OrgID); err != nil {
		return nil, err
	}
	if agent.OrgID != req.OrgID {
		return nil, ecode.InvalidParams.WithCause(ecode.NewInvalidParamsErr("agent_id does not belong to org_id"))
	}

	project := s.findWorkspaceForOrgLocked(req.OrgID)
	if project == nil {
		projectID := strings.TrimSpace(req.ProjectID)
		if projectID == "" {
			projectID = s.AnalyticsStore.nextID("project")
		}
		project = &Project{
			ID:    projectID,
			OrgID: req.OrgID,
			Name:  sharedWorkspaceName,
		}
		s.AnalyticsStore.projects[projectID] = project
	}

	projectID := project.ID
	project.AgentID = req.AgentID
	project.Name = sharedWorkspaceName
	project.RepoHash = req.RepoHash
	project.RepoPath = req.RepoPath
	project.LanguageMix = cloneFloatMap(req.LanguageMix)
	project.DefaultTool = req.DefaultTool
	project.ConnectedAt = now

	s.appendAuditLocked(req.OrgID, projectID, "workspace.connected", "shared workspace connected to aiops")
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
	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	project, err := s.resolveProjectLocked(ctx, req.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeProject(ctx, project); err != nil {
		return nil, err
	}
	req.ProjectID = project.ID

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
	s.AnalyticsStore.mu.RLock()
	defer s.AnalyticsStore.mu.RUnlock()

	project, err := s.resolveProjectLocked(ctx, req.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeProject(ctx, project); err != nil {
		return nil, err
	}
	req.ProjectID = project.ID

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
	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	project, err := s.resolveProjectLocked(ctx, req.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeProject(ctx, project); err != nil {
		return nil, err
	}
	req.ProjectID = project.ID

	recordedAt := req.Timestamp.UTC()
	if recordedAt.IsZero() {
		recordedAt = time.Now().UTC()
	}
	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = s.AnalyticsStore.nextID("session")
	}

	summary := &SessionSummary{
		ID:         sessionID,
		ProjectID:  req.ProjectID,
		Tool:       req.Tool,
		TokenIn:    req.TokenIn,
		TokenOut:   req.TokenOut,
		RawQueries: cloneStringSlice(req.RawQueries),
		Timestamp:  recordedAt,
	}

	existingIndex := -1
	for i, item := range s.AnalyticsStore.sessionSummaries[req.ProjectID] {
		if item.ID == sessionID {
			existingIndex = i
			break
		}
	}
	if existingIndex >= 0 {
		s.AnalyticsStore.sessionSummaries[req.ProjectID][existingIndex] = summary
	} else {
		s.AnalyticsStore.sessionSummaries[req.ProjectID] = append(s.AnalyticsStore.sessionSummaries[req.ProjectID], summary)
	}
	if project.LastIngestedAt == nil || recordedAt.After(*project.LastIngestedAt) {
		project.LastIngestedAt = &recordedAt
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
	s.AnalyticsStore.mu.RLock()
	defer s.AnalyticsStore.mu.RUnlock()

	project, err := s.resolveProjectLocked(ctx, req.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeProject(ctx, project); err != nil {
		return nil, err
	}
	req.ProjectID = project.ID

	limit := req.Limit
	if limit <= 0 || limit > 20 {
		limit = 5
	}

	items := make([]response.SessionSummaryItem, 0, len(s.AnalyticsStore.sessionSummaries[req.ProjectID]))
	for _, session := range s.AnalyticsStore.sessionSummaries[req.ProjectID] {
		items = append(items, response.SessionSummaryItem{
			ID:         session.ID,
			ProjectID:  session.ProjectID,
			Tool:       session.Tool,
			TokenIn:    session.TokenIn,
			TokenOut:   session.TokenOut,
			RawQueries: cloneStringSlice(session.RawQueries),
			Timestamp:  session.Timestamp,
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
	s.AnalyticsStore.mu.RLock()
	defer s.AnalyticsStore.mu.RUnlock()

	project, err := s.resolveProjectLocked(ctx, req.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeProject(ctx, project); err != nil {
		return nil, err
	}
	req.ProjectID = project.ID

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
	s.AnalyticsStore.mu.RLock()
	defer s.AnalyticsStore.mu.RUnlock()

	if _, ok := s.AnalyticsStore.organizations[req.OrgID]; !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown org_id"))
	}
	if err := s.authorizeOrg(ctx, req.OrgID); err != nil {
		return nil, err
	}

	projectIDs := make([]string, 0)
	for projectID, project := range s.AnalyticsStore.projects {
		if project.OrgID == req.OrgID {
			projectIDs = append(projectIDs, projectID)
		}
	}

	var (
		totalSessions        int
		totalInputTokens     int
		totalOutputTokens    int
		totalTokens          int
		totalQueries         int
		totalActiveRecs      int
		totalApplyOps        int
		totalSuccessfulApply int
		totalFailedApply     int
		totalRollbacks       int
		totalPendingReview   int
		totalApprovedQueue   int
		lastIngestedAt       *time.Time
	)

	for _, projectID := range projectIDs {
		for _, session := range s.AnalyticsStore.sessionSummaries[projectID] {
			totalSessions++
			totalInputTokens += session.TokenIn
			totalOutputTokens += session.TokenOut
			totalTokens += session.TokenIn + session.TokenOut
			totalQueries += queryCountForSession(session)
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

	rollbackRate := safeDiv(float64(totalRollbacks), float64(maxInt(totalApplyOps, 1)))

	return &response.DashboardOverviewResp{
		OrgID:                     req.OrgID,
		TotalDevices:              countDevicesByOrg(s.AnalyticsStore.agents, req.OrgID),
		TotalProjects:             len(projectIDs),
		TotalSessions:             totalSessions,
		ActiveRecommendations:     totalActiveRecs,
		PendingReviewCount:        totalPendingReview,
		ApprovedQueueCount:        totalApprovedQueue,
		SuccessfulRolloutCount:    totalSuccessfulApply,
		FailedExecutionCount:      totalFailedApply,
		TotalInputTokens:          totalInputTokens,
		TotalOutputTokens:         totalOutputTokens,
		TotalTokens:               totalTokens,
		AvgInputTokensPerQuery:    safeDiv(float64(totalInputTokens), float64(totalQueries)),
		AvgOutputTokensPerQuery:   safeDiv(float64(totalOutputTokens), float64(totalQueries)),
		AvgTokensPerQuery:         safeDiv(float64(totalTokens), float64(totalQueries)),
		AvgInputTokensPerSession:  safeDiv(float64(totalInputTokens), float64(maxInt(totalSessions, 1))),
		AvgOutputTokensPerSession: safeDiv(float64(totalOutputTokens), float64(maxInt(totalSessions, 1))),
		AvgTokensPerSession:       safeDiv(float64(totalTokens), float64(maxInt(totalSessions, 1))),
		AvgQueriesPerSession:      safeDiv(float64(totalQueries), float64(maxInt(totalSessions, 1))),
		RecommendationApplyRate:   safeDiv(float64(totalSuccessfulApply), float64(maxInt(totalActiveRecs+totalSuccessfulApply, 1))),
		RollbackRate:              rollbackRate,
		ActionSummary:             buildDashboardActionSummary(totalPendingReview, totalApprovedQueue, totalActiveRecs),
		OutcomeSummary:            buildDashboardOutcomeSummary(totalSuccessfulApply, totalFailedApply, rollbackRate),
		ResearchProvider:          s.researchAgent.Provider,
		ResearchMode:              s.researchAgent.Mode,
		LastIngestedAt:            lastIngestedAt,
	}, nil
}

func (s *AnalyticsService) ListProjects(ctx context.Context, req *request.ProjectListReq) (*response.ProjectListResp, error) {
	s.AnalyticsStore.mu.RLock()
	defer s.AnalyticsStore.mu.RUnlock()

	if _, ok := s.AnalyticsStore.organizations[req.OrgID]; !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown org_id"))
	}
	if err := s.authorizeOrg(ctx, req.OrgID); err != nil {
		return nil, err
	}

	items := make([]response.ProjectResp, 0)
	if workspace := s.findWorkspaceForOrgLocked(req.OrgID); workspace != nil {
		items = append(items, response.ProjectResp{
			ID:             workspace.ID,
			Name:           workspace.Name,
			RepoHash:       workspace.RepoHash,
			RepoPath:       workspace.RepoPath,
			DefaultTool:    workspace.DefaultTool,
			LastProfileID:  workspace.LastProfileID,
			LastIngestedAt: workspace.LastIngestedAt,
			LanguageMix:    cloneFloatMap(workspace.LanguageMix),
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
	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	rec, ok := s.AnalyticsStore.recommendations[req.RecommendationID]
	if !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown recommendation_id"))
	}
	project, err := s.resolveProjectLocked(ctx, rec.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeProject(ctx, project); err != nil {
		return nil, err
	}

	req.RequestedBy = s.actorFromContext(ctx, req.RequestedBy)
	if req.RequestedBy == "" {
		return nil, ecode.InvalidParams.WithCause(ecode.NewInvalidParamsErr("requested_by is required"))
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
	s.appendAuditLocked(project.OrgID, rec.ProjectID, auditType, auditMessage)
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
	s.AnalyticsStore.mu.RLock()
	defer s.AnalyticsStore.mu.RUnlock()

	project, err := s.resolveProjectLocked(ctx, req.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeProject(ctx, project); err != nil {
		return nil, err
	}
	req.ProjectID = project.ID
	req.UserID = s.actorFromContext(ctx, req.UserID)

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
	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	op, ok := s.AnalyticsStore.applyOperations[req.ApplyID]
	if !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown apply_id"))
	}
	project, err := s.resolveProjectLocked(ctx, op.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeProject(ctx, project); err != nil {
		return nil, err
	}

	decision := strings.ToLower(strings.TrimSpace(req.Decision))
	if decision != "approve" && decision != "approved" && decision != "reject" && decision != "rejected" {
		return nil, ecode.InvalidParams.WithCause(ecode.NewInvalidParamsErr("decision must be approve or reject"))
	}

	now := time.Now().UTC()
	req.ReviewedBy = s.actorFromContext(ctx, req.ReviewedBy)
	if req.ReviewedBy == "" {
		return nil, ecode.InvalidParams.WithCause(ecode.NewInvalidParamsErr("reviewed_by is required"))
	}
	op.ReviewedBy = req.ReviewedBy
	op.ReviewNote = req.ReviewNote
	op.ReviewedAt = &now

	switch decision {
	case "approve", "approved":
		op.Decision = "approved"
		op.ApprovalStatus = "approved"
		op.Status = "approved_for_local_apply"
		s.appendAuditLocked(project.OrgID, op.ProjectID, "change_plan.approved", "change plan approved for local apply")
	default:
		op.Decision = "rejected"
		op.ApprovalStatus = "rejected"
		op.Status = "rejected"
		s.appendAuditLocked(project.OrgID, op.ProjectID, "change_plan.rejected", "change plan rejected during review")
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
	s.AnalyticsStore.mu.RLock()
	defer s.AnalyticsStore.mu.RUnlock()

	project, err := s.resolveProjectLocked(ctx, req.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeProject(ctx, project); err != nil {
		return nil, err
	}
	req.ProjectID = project.ID

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
	s.AnalyticsStore.mu.RLock()
	defer s.AnalyticsStore.mu.RUnlock()

	project, err := s.resolveProjectLocked(ctx, req.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeProject(ctx, project); err != nil {
		return nil, err
	}
	req.ProjectID = project.ID
	req.UserID = s.actorFromContext(ctx, req.UserID)

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
	s.AnalyticsStore.mu.RLock()
	defer s.AnalyticsStore.mu.RUnlock()

	project, err := s.resolveProjectLocked(ctx, req.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeProject(ctx, project); err != nil {
		return nil, err
	}
	req.ProjectID = project.ID

	items := make([]response.ImpactSummaryItem, 0)
	sessions := s.AnalyticsStore.sessionSummaries[req.ProjectID]
	for _, op := range s.AnalyticsStore.applyOperations {
		if op.ProjectID != req.ProjectID || op.AppliedAt == nil {
			continue
		}

		before, after := splitSessionsByApplyTime(sessions, *op.AppliedAt)
		beforeStats := summarizeSessions(before)
		afterStats := summarizeSessions(after)

		items = append(items, response.ImpactSummaryItem{
			ApplyID:                         op.ID,
			RecommendationID:                op.RecommendationID,
			Status:                          op.Status,
			AppliedAt:                       op.AppliedAt,
			SessionsBefore:                  len(before),
			SessionsAfter:                   len(after),
			QueriesBefore:                   beforeStats.QueryCount,
			QueriesAfter:                    afterStats.QueryCount,
			AvgInputTokensPerQueryBefore:    beforeStats.AvgInputTokensPerQuery,
			AvgInputTokensPerQueryAfter:     afterStats.AvgInputTokensPerQuery,
			AvgOutputTokensPerQueryBefore:   beforeStats.AvgOutputTokensPerQuery,
			AvgOutputTokensPerQueryAfter:    afterStats.AvgOutputTokensPerQuery,
			AvgTokensPerQueryBefore:         beforeStats.AvgTokensPerQuery,
			AvgTokensPerQueryAfter:          afterStats.AvgTokensPerQuery,
			AvgInputTokensPerSessionBefore:  beforeStats.AvgInputTokensPerSession,
			AvgInputTokensPerSessionAfter:   afterStats.AvgInputTokensPerSession,
			AvgOutputTokensPerSessionBefore: beforeStats.AvgOutputTokensPerSession,
			AvgOutputTokensPerSessionAfter:  afterStats.AvgOutputTokensPerSession,
			AvgTokensPerSessionBefore:       beforeStats.AvgTokensPerSession,
			AvgTokensPerSessionAfter:        afterStats.AvgTokensPerSession,
			InputTokensPerQueryDelta:        round(afterStats.AvgInputTokensPerQuery - beforeStats.AvgInputTokensPerQuery),
			OutputTokensPerQueryDelta:       round(afterStats.AvgOutputTokensPerQuery - beforeStats.AvgOutputTokensPerQuery),
			TokensPerQueryDelta:             round(afterStats.AvgTokensPerQuery - beforeStats.AvgTokensPerQuery),
			InputTokensPerSessionDelta:      round(afterStats.AvgInputTokensPerSession - beforeStats.AvgInputTokensPerSession),
			OutputTokensPerSessionDelta:     round(afterStats.AvgOutputTokensPerSession - beforeStats.AvgOutputTokensPerSession),
			TokensPerSessionDelta:           round(afterStats.AvgTokensPerSession - beforeStats.AvgTokensPerSession),
			Interpretation:                  interpretImpact(beforeStats, afterStats, len(after)),
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
	s.AnalyticsStore.mu.RLock()
	defer s.AnalyticsStore.mu.RUnlock()

	if _, ok := s.AnalyticsStore.organizations[req.OrgID]; !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown org_id"))
	}
	if err := s.authorizeOrg(ctx, req.OrgID); err != nil {
		return nil, err
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
	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	op, ok := s.AnalyticsStore.applyOperations[req.ApplyID]
	if !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown apply_id"))
	}
	project, err := s.resolveProjectLocked(ctx, op.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeProject(ctx, project); err != nil {
		return nil, err
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

func toSessionUser(user *User) response.AuthUserResp {
	if user == nil {
		return response.AuthUserResp{}
	}
	return response.AuthUserResp{
		ID:    user.ID,
		Name:  user.Name,
		Email: user.Email,
	}
}

func toSessionOrganization(org *Organization) response.AuthOrganizationResp {
	if org == nil {
		return response.AuthOrganizationResp{}
	}
	return response.AuthOrganizationResp{
		ID:   org.ID,
		Name: org.Name,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func stringInSlice(target string, values []string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
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
	return 1
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

type sessionSummaryStats struct {
	QueryCount                int
	AvgInputTokensPerQuery    float64
	AvgOutputTokensPerQuery   float64
	AvgTokensPerQuery         float64
	AvgInputTokensPerSession  float64
	AvgOutputTokensPerSession float64
	AvgTokensPerSession       float64
}

func summarizeSessions(sessions []*SessionSummary) sessionSummaryStats {
	if len(sessions) == 0 {
		return sessionSummaryStats{}
	}

	totalInputTokens := 0
	totalOutputTokens := 0
	totalTokens := 0
	totalQueries := 0
	for _, session := range sessions {
		totalInputTokens += session.TokenIn
		totalOutputTokens += session.TokenOut
		totalTokens += session.TokenIn + session.TokenOut
		totalQueries += queryCountForSession(session)
	}

	return sessionSummaryStats{
		QueryCount:                totalQueries,
		AvgInputTokensPerQuery:    safeDiv(float64(totalInputTokens), float64(maxInt(totalQueries, 1))),
		AvgOutputTokensPerQuery:   safeDiv(float64(totalOutputTokens), float64(maxInt(totalQueries, 1))),
		AvgTokensPerQuery:         safeDiv(float64(totalTokens), float64(maxInt(totalQueries, 1))),
		AvgInputTokensPerSession:  safeDiv(float64(totalInputTokens), float64(len(sessions))),
		AvgOutputTokensPerSession: safeDiv(float64(totalOutputTokens), float64(len(sessions))),
		AvgTokensPerSession:       safeDiv(float64(totalTokens), float64(len(sessions))),
	}
}

func interpretImpact(before, after sessionSummaryStats, afterCount int) string {
	if afterCount == 0 {
		return "Waiting for post-apply sessions."
	}
	if before.QueryCount == 0 {
		return "Collect a few more baseline sessions to compare token usage before and after the rollout."
	}

	switch {
	case after.AvgInputTokensPerQuery < before.AvgInputTokensPerQuery:
		return "Prompt-side token usage per query is trending down after the rollout."
	case after.AvgInputTokensPerQuery > before.AvgInputTokensPerQuery:
		return "Prompt-side token usage per query is trending up after the rollout."
	case after.AvgOutputTokensPerQuery < before.AvgOutputTokensPerQuery:
		return "Response-side token usage per query is trending down after the rollout."
	case after.AvgOutputTokensPerQuery > before.AvgOutputTokensPerQuery:
		return "Response-side token usage per query is trending up after the rollout."
	case after.AvgTokensPerQuery < before.AvgTokensPerQuery:
		return "Follow-up sessions are reaching answers with fewer tokens per query."
	case after.AvgTokensPerQuery > before.AvgTokensPerQuery:
		return "Follow-up sessions are spending more tokens per query so far."
	case after.AvgTokensPerSession < before.AvgTokensPerSession:
		return "Per-session token usage is trending down after the rollout."
	case after.AvgTokensPerSession > before.AvgTokensPerSession:
		return "Per-session token usage is trending up after the rollout."
	default:
		return "Token usage looks flat so far; collect more sessions for a clearer comparison."
	}
}

func buildDashboardActionSummary(pendingReview, approvedQueue, activeRecommendations int) string {
	switch {
	case pendingReview > 0 && approvedQueue > 0:
		return fmt.Sprintf("%d plan(s) need approval and %d more are ready for the next local sync.", pendingReview, approvedQueue)
	case pendingReview > 0:
		return fmt.Sprintf("%d plan(s) are waiting for approval.", pendingReview)
	case approvedQueue > 0:
		return fmt.Sprintf("%d approved plan(s) are ready for the next local sync.", approvedQueue)
	case activeRecommendations > 0:
		return fmt.Sprintf("%d instruction recommendation(s) are ready to review.", activeRecommendations)
	default:
		return "No rollout action is waiting right now."
	}
}

func buildDashboardOutcomeSummary(successfulRollouts, failedExecutions int, rollbackRate float64) string {
	switch {
	case failedExecutions > 0:
		return fmt.Sprintf("%d rollout(s) need attention after failing local execution.", failedExecutions)
	case successfulRollouts == 0:
		return "No completed rollouts yet. Approve a change to start measuring token trends."
	case rollbackRate >= 0.25:
		return "Recent rollouts are being reversed too often. Narrow the next instruction change."
	default:
		return "Recent instruction changes are landing. Keep uploading sessions to measure token impact."
	}
}
