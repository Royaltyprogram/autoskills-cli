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
	researchAgent             *CloudResearchAgent
	evaluationAgent           *CloudEvaluationAgent
	recommendationMinSessions int
}

const defaultRecommendationMinSessions = 10

func NewAnalyticsService(opt Options) *AnalyticsService {
	recommendationMinSessions := opt.RecommendationMinSessions
	if recommendationMinSessions <= 0 {
		recommendationMinSessions = defaultRecommendationMinSessions
	}
	return &AnalyticsService{
		Options:                   opt,
		researchAgent:             NewCloudResearchAgent(opt.Config),
		evaluationAgent:           NewCloudEvaluationAgent(opt.Config),
		recommendationMinSessions: recommendationMinSessions,
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
		return ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown project"))
	}
	return s.authorizeOrg(ctx, project.OrgID)
}

func (s *AnalyticsService) projectsForOrgLocked(orgID string) []*Project {
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
	return projects
}

func (s *AnalyticsService) latestProjectForOrgLocked(orgID string) *Project {
	return latestProject(s.projectsForOrgLocked(orgID))
}

func (s *AnalyticsService) findProjectByRepoLocked(orgID, repoHash, repoPath string) *Project {
	orgID = strings.TrimSpace(orgID)
	repoHash = strings.TrimSpace(repoHash)
	repoPath = strings.TrimSpace(repoPath)
	if orgID == "" {
		return nil
	}
	cleanRepoPath := ""
	if repoPath != "" {
		cleanRepoPath = filepath.Clean(repoPath)
	}
	for _, project := range s.projectsForOrgLocked(orgID) {
		if project == nil {
			continue
		}
		if repoHash != "" && strings.TrimSpace(project.RepoHash) == repoHash {
			return project
		}
		if cleanRepoPath != "" && strings.TrimSpace(project.RepoPath) != "" && filepath.Clean(project.RepoPath) == cleanRepoPath {
			return project
		}
	}
	return nil
}

func defaultProjectName(req *request.RegisterProjectReq) string {
	if req == nil {
		return "project"
	}
	if name := strings.TrimSpace(req.Name); name != "" {
		return name
	}
	if repoPath := strings.TrimSpace(req.RepoPath); repoPath != "" {
		base := filepath.Base(filepath.Clean(repoPath))
		if base != "" && base != "." && base != string(filepath.Separator) {
			return base
		}
	}
	if repoHash := strings.TrimSpace(req.RepoHash); repoHash != "" {
		return repoHash
	}
	return "project"
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
		if project := s.latestProjectForOrgLocked(identity.OrgID); project != nil {
			return project, nil
		}
	}

	if projectID != "" {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown project"))
	}
	return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("no connected project"))
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

	projectID := strings.TrimSpace(req.ProjectID)
	project := (*Project)(nil)
	if projectID != "" {
		if existing, ok := s.AnalyticsStore.projects[projectID]; ok {
			project = existing
		}
	}
	if project == nil {
		project = s.findProjectByRepoLocked(req.OrgID, req.RepoHash, req.RepoPath)
	}
	if project == nil {
		if projectID == "" {
			projectID = s.AnalyticsStore.nextID("project")
		}
		project = &Project{
			ID:    projectID,
			OrgID: req.OrgID,
			Name:  defaultProjectName(req),
		}
		s.AnalyticsStore.projects[projectID] = project
	}

	projectID = project.ID
	project.AgentID = req.AgentID
	project.Name = defaultProjectName(req)
	project.RepoHash = req.RepoHash
	project.RepoPath = req.RepoPath
	project.LanguageMix = cloneFloatMap(req.LanguageMix)
	project.DefaultTool = req.DefaultTool
	project.ConnectedAt = now

	s.appendAuditLocked(req.OrgID, projectID, "project.connected", "project connected to aiops")
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
		ID:                     sessionID,
		ProjectID:              req.ProjectID,
		Tool:                   req.Tool,
		TokenIn:                maxInt(req.TokenIn, 0),
		TokenOut:               maxInt(req.TokenOut, 0),
		CachedInputTokens:      maxInt(req.CachedInputTokens, 0),
		ReasoningOutputTokens:  maxInt(req.ReasoningOutputTokens, 0),
		FunctionCallCount:      maxInt(req.FunctionCallCount, 0),
		ToolErrorCount:         maxInt(req.ToolErrorCount, 0),
		SessionDurationMS:      maxInt(req.SessionDurationMS, 0),
		ToolWallTimeMS:         maxInt(req.ToolWallTimeMS, 0),
		ToolCalls:              cloneIntMap(req.ToolCalls),
		ToolErrors:             cloneIntMap(req.ToolErrors),
		ToolWallTimesMS:        cloneIntMap(req.ToolWallTimesMS),
		RawQueries:             cloneStringSlice(req.RawQueries),
		Models:                 cloneStringSlice(req.Models),
		ModelProvider:          strings.TrimSpace(req.ModelProvider),
		FirstResponseLatencyMS: maxInt(req.FirstResponseLatencyMS, 0),
		AssistantResponses:     cloneStringSlice(req.AssistantResponses),
		Timestamp:              recordedAt,
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

	var recommendations []*Recommendation
	if activeExperiment := s.findActiveExperimentLocked(project.ID); activeExperiment != nil {
		s.setRecommendationResearchStatusLocked(project.ID, RecommendationResearchStatus{
			ProjectID:           project.ID,
			State:               "deferred_active_experiment",
			Summary:             "Recommendation refresh is deferred while an active experiment is still being measured.",
			Provider:            s.researchAgent.Provider,
			Model:               s.researchAgent.Model,
			MinimumSessions:     s.recommendationMinSessions,
			SessionCount:        len(s.AnalyticsStore.sessionSummaries[project.ID]),
			RawQueryCount:       len(collectRawQueries(s.AnalyticsStore.sessionSummaries[project.ID])),
			RecommendationCount: len(s.currentRecommendationsLocked(project.ID)),
			TriggerSessionID:    sessionID,
		})
		s.evaluateExperimentsLocked(project, recordedAt)
		if s.findActiveExperimentLocked(project.ID) != nil {
			recommendations = s.currentRecommendationsLocked(project.ID)
		} else {
			recommendations = s.refreshRecommendationsLocked(project, sessionID)
		}
	} else {
		recommendations = s.refreshRecommendationsLocked(project, sessionID)
	}
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
		ResearchStatus:          cloneRecommendationResearchStatusResp(s.AnalyticsStore.recommendationResearch[project.ID]),
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
			ID:                     session.ID,
			ProjectID:              session.ProjectID,
			Tool:                   session.Tool,
			TokenIn:                session.TokenIn,
			TokenOut:               session.TokenOut,
			CachedInputTokens:      session.CachedInputTokens,
			ReasoningOutputTokens:  session.ReasoningOutputTokens,
			FunctionCallCount:      session.FunctionCallCount,
			ToolErrorCount:         session.ToolErrorCount,
			SessionDurationMS:      session.SessionDurationMS,
			ToolWallTimeMS:         session.ToolWallTimeMS,
			ToolCalls:              cloneIntMap(session.ToolCalls),
			ToolErrors:             cloneIntMap(session.ToolErrors),
			ToolWallTimesMS:        cloneIntMap(session.ToolWallTimesMS),
			RawQueries:             cloneStringSlice(session.RawQueries),
			Models:                 cloneStringSlice(session.Models),
			ModelProvider:          session.ModelProvider,
			FirstResponseLatencyMS: session.FirstResponseLatencyMS,
			AssistantResponses:     cloneStringSlice(session.AssistantResponses),
			Timestamp:              session.Timestamp,
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
		totalSessions          int
		totalInputTokens       int
		totalOutputTokens      int
		totalTokens            int
		totalQueries           int
		totalActiveRecs        int
		totalActiveExperiments int
		totalApplyOps          int
		totalSuccessfulApply   int
		totalFailedApply       int
		totalRollbacks         int
		totalPendingReview     int
		totalApprovedQueue     int
		totalHarnessRuns       int
		totalHarnessFailures   int
		lastIngestedAt         *time.Time
		latestHarnessAt        *time.Time
		latestFailingHarnessAt *time.Time
		latestHarnessStatus    string
		latestHarnessName      string
		lastFailingHarnessName string
		researchStatus         *RecommendationResearchStatus
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
		if candidate := s.AnalyticsStore.recommendationResearch[projectID]; isRecommendationResearchStatusNewer(candidate, researchStatus) {
			researchStatus = candidate
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
		if op.Status == "failed" || op.Status == "rollback_failed" {
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
	for _, experiment := range s.AnalyticsStore.experiments {
		if experiment == nil {
			continue
		}
		project := s.AnalyticsStore.projects[experiment.ProjectID]
		if project == nil || project.OrgID != req.OrgID {
			continue
		}
		switch experiment.Status {
		case "awaiting_review", "queued_for_apply", "measuring", "rollback_requested":
			totalActiveExperiments++
		}
	}
	for _, run := range s.AnalyticsStore.harnessRuns {
		if run == nil {
			continue
		}
		project := s.AnalyticsStore.projects[run.ProjectID]
		if project == nil || project.OrgID != req.OrgID {
			continue
		}
		totalHarnessRuns++
		if !run.Passed {
			totalHarnessFailures++
		}
		recordedAt := latestHarnessRecordedAt(run)
		if latestHarnessAt == nil || recordedAt.After(*latestHarnessAt) {
			ts := recordedAt
			latestHarnessAt = &ts
			latestHarnessStatus = run.Status
			latestHarnessName = run.Name
		}
		if !run.Passed && (latestFailingHarnessAt == nil || recordedAt.After(*latestFailingHarnessAt)) {
			ts := recordedAt
			latestFailingHarnessAt = &ts
			lastFailingHarnessName = run.Name
		}
	}

	rollbackRate := safeDiv(float64(totalRollbacks), float64(maxInt(totalApplyOps, 1)))

	return &response.DashboardOverviewResp{
		OrgID:                     req.OrgID,
		TotalDevices:              countDevicesByOrg(s.AnalyticsStore.agents, req.OrgID),
		TotalProjects:             len(projectIDs),
		TotalSessions:             totalSessions,
		ActiveRecommendations:     totalActiveRecs,
		ActiveExperimentCount:     totalActiveExperiments,
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
		HarnessRunCount:           totalHarnessRuns,
		HarnessFailureCount:       totalHarnessFailures,
		HarnessPassRate:           safeDiv(float64(maxInt(totalHarnessRuns-totalHarnessFailures, 0)), float64(maxInt(totalHarnessRuns, 1))),
		LatestHarnessStatus:       latestHarnessStatus,
		LatestHarnessName:         latestHarnessName,
		LastFailingHarnessName:    lastFailingHarnessName,
		LatestHarnessAt:           latestHarnessAt,
		ActionSummary:             buildDashboardActionSummary(totalPendingReview, totalApprovedQueue, totalActiveRecs),
		OutcomeSummary:            buildDashboardOutcomeSummary(totalSuccessfulApply, totalFailedApply, rollbackRate),
		ResearchProvider:          s.researchAgent.Provider,
		ResearchMode:              s.researchAgent.Mode,
		LastIngestedAt:            lastIngestedAt,
		ResearchStatus:            cloneRecommendationResearchStatusResp(researchStatus),
	}, nil
}

func (s *AnalyticsService) DashboardProjectInsights(ctx context.Context, req *request.DashboardProjectInsightsReq) (*response.DashboardProjectInsightsResp, error) {
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

	type projectInsightDayAccumulator struct {
		point           *response.DashboardProjectInsightDayResp
		latencyTotalMS  int
		durationTotalMS int
	}

	pointsByDay := make(map[string]*projectInsightDayAccumulator)
	modelCounts := make(map[string]int)
	providerCounts := make(map[string]int)
	toolCallCounts := make(map[string]int)
	toolErrorCounts := make(map[string]int)
	toolWallTimeMS := make(map[string]int)
	toolSessionCounts := make(map[string]int)
	knownModelSessions := 0
	unknownModelSessions := 0
	knownProviderSessions := 0
	unknownProviderSessions := 0
	knownLatencySessions := 0
	unknownLatencySessions := 0
	knownDurationSessions := 0
	unknownDurationSessions := 0
	totalLatencyMS := 0
	totalDurationMS := 0
	totalCachedInputTokens := 0
	totalReasoningOutputTokens := 0
	totalFunctionCalls := 0
	totalToolErrors := 0
	totalToolWallTimeMS := 0
	sessionsWithFunctionCalls := 0
	sessionsWithToolErrors := 0
	harnessRunCount := 0
	harnessPassCount := 0
	harnessFailCount := 0
	latestHarnessStatus := ""
	latestHarnessName := ""
	lastFailingHarnessName := ""
	var latestFailingHarnessAt *time.Time
	var latestHarnessAt *time.Time
	totalSessions := len(s.AnalyticsStore.sessionSummaries[req.ProjectID])

	ensureDay := func(day string) *projectInsightDayAccumulator {
		if point, ok := pointsByDay[day]; ok {
			return point
		}
		point := &projectInsightDayAccumulator{
			point: &response.DashboardProjectInsightDayResp{Day: day},
		}
		pointsByDay[day] = point
		return point
	}

	for _, session := range s.AnalyticsStore.sessionSummaries[req.ProjectID] {
		day := session.Timestamp.UTC().Format("2006-01-02")
		point := ensureDay(day)
		point.point.SessionCount++
		point.point.QueryCount += queryCountForSession(session)
		point.point.InputTokens += session.TokenIn
		point.point.OutputTokens += session.TokenOut
		point.point.TotalTokens += session.TokenIn + session.TokenOut
		point.point.CachedInputTokens += session.CachedInputTokens
		point.point.ReasoningOutputTokens += session.ReasoningOutputTokens
		point.point.FunctionCallCount += session.FunctionCallCount
		point.point.ToolErrorCount += session.ToolErrorCount
		point.point.ToolWallTimeMS += session.ToolWallTimeMS
		totalCachedInputTokens += session.CachedInputTokens
		totalReasoningOutputTokens += session.ReasoningOutputTokens
		totalFunctionCalls += session.FunctionCallCount
		totalToolErrors += session.ToolErrorCount
		totalToolWallTimeMS += session.ToolWallTimeMS
		if session.FunctionCallCount > 0 {
			sessionsWithFunctionCalls++
		}
		if session.ToolErrorCount > 0 {
			sessionsWithToolErrors++
		}
		seenToolsInSession := make(map[string]struct{})
		for toolName, count := range session.ToolCalls {
			toolName = strings.TrimSpace(toolName)
			if toolName == "" || count <= 0 {
				continue
			}
			toolCallCounts[toolName] += count
			if _, ok := seenToolsInSession[toolName]; ok {
				continue
			}
			seenToolsInSession[toolName] = struct{}{}
			toolSessionCounts[toolName]++
		}
		for toolName, count := range session.ToolErrors {
			toolName = strings.TrimSpace(toolName)
			if toolName == "" || count <= 0 {
				continue
			}
			toolErrorCounts[toolName] += count
		}
		for toolName, wallTimeMS := range session.ToolWallTimesMS {
			toolName = strings.TrimSpace(toolName)
			if toolName == "" || wallTimeMS <= 0 {
				continue
			}
			toolWallTimeMS[toolName] += wallTimeMS
		}

		if session.FirstResponseLatencyMS > 0 {
			knownLatencySessions++
			totalLatencyMS += session.FirstResponseLatencyMS
			point.point.LatencySessionCount++
			point.latencyTotalMS += session.FirstResponseLatencyMS
		} else {
			unknownLatencySessions++
		}
		if session.SessionDurationMS > 0 {
			knownDurationSessions++
			totalDurationMS += session.SessionDurationMS
			point.point.DurationSessionCount++
			point.durationTotalMS += session.SessionDurationMS
		} else {
			unknownDurationSessions++
		}

		if provider := strings.TrimSpace(session.ModelProvider); provider != "" {
			knownProviderSessions++
			providerCounts[provider]++
		} else {
			unknownProviderSessions++
		}

		sessionModels := make(map[string]struct{})
		for _, model := range session.Models {
			model = strings.TrimSpace(model)
			if model == "" {
				continue
			}
			sessionModels[model] = struct{}{}
		}
		if len(sessionModels) == 0 {
			unknownModelSessions++
			continue
		}
		knownModelSessions++
		for model := range sessionModels {
			modelCounts[model]++
		}
	}

	for _, snapshot := range s.AnalyticsStore.configSnapshots[req.ProjectID] {
		day := snapshot.CapturedAt.UTC().Format("2006-01-02")
		ensureDay(day).point.SnapshotCount++
	}
	for _, run := range s.AnalyticsStore.harnessRuns {
		if run == nil || run.ProjectID != req.ProjectID {
			continue
		}
		recordedAt := latestHarnessRecordedAt(run)
		day := recordedAt.UTC().Format("2006-01-02")
		point := ensureDay(day)
		harnessRunCount++
		if run.Passed {
			harnessPassCount++
			point.point.HarnessPassCount++
		} else {
			harnessFailCount++
			point.point.HarnessFailCount++
			if latestFailingHarnessAt == nil || recordedAt.After(*latestFailingHarnessAt) {
				ts := recordedAt
				latestFailingHarnessAt = &ts
				lastFailingHarnessName = run.Name
			}
		}
		if latestHarnessAt == nil || recordedAt.After(*latestHarnessAt) {
			ts := recordedAt
			latestHarnessAt = &ts
			latestHarnessStatus = run.Status
			latestHarnessName = run.Name
		}
	}

	for _, audit := range s.AnalyticsStore.audits {
		if audit == nil || audit.ProjectID != req.ProjectID {
			continue
		}
		day := audit.CreatedAt.UTC().Format("2006-01-02")
		point := ensureDay(day)
		switch audit.Type {
		case "change_plan.approved", "change_plan.auto_approved":
			point.point.ApprovalCount++
		case "execution.result":
			switch strings.TrimSpace(audit.Message) {
			case "rollback_confirmed":
				point.point.RollbackCount++
			case "applied":
				point.point.AppliedCount++
			}
		}
	}

	days := make([]response.DashboardProjectInsightDayResp, 0, len(pointsByDay))
	for _, point := range pointsByDay {
		if point.point.LatencySessionCount > 0 {
			point.point.AvgFirstResponseLatencyMS = int(math.Round(float64(point.latencyTotalMS) / float64(point.point.LatencySessionCount)))
		}
		if point.point.DurationSessionCount > 0 {
			point.point.AvgSessionDurationMS = int(math.Round(float64(point.durationTotalMS) / float64(point.point.DurationSessionCount)))
		}
		days = append(days, *point.point)
	}
	sort.Slice(days, func(i, j int) bool {
		return days[i].Day < days[j].Day
	})

	models := make([]response.DashboardProjectInsightModelResp, 0, len(modelCounts))
	for model, count := range modelCounts {
		models = append(models, response.DashboardProjectInsightModelResp{
			Model:        model,
			SessionCount: count,
			Share:        safeDiv(float64(count), float64(maxInt(totalSessions, 1))),
		})
	}
	sort.Slice(models, func(i, j int) bool {
		if models[i].SessionCount == models[j].SessionCount {
			return models[i].Model < models[j].Model
		}
		return models[i].SessionCount > models[j].SessionCount
	})

	providers := make([]response.DashboardProjectInsightProviderResp, 0, len(providerCounts))
	for provider, count := range providerCounts {
		providers = append(providers, response.DashboardProjectInsightProviderResp{
			Provider:     provider,
			SessionCount: count,
			Share:        safeDiv(float64(count), float64(maxInt(totalSessions, 1))),
		})
	}
	sort.Slice(providers, func(i, j int) bool {
		if providers[i].SessionCount == providers[j].SessionCount {
			return providers[i].Provider < providers[j].Provider
		}
		return providers[i].SessionCount > providers[j].SessionCount
	})

	toolKeys := make(map[string]struct{}, len(toolCallCounts)+len(toolErrorCounts))
	for toolName := range toolCallCounts {
		toolKeys[toolName] = struct{}{}
	}
	for toolName := range toolErrorCounts {
		toolKeys[toolName] = struct{}{}
	}
	tools := make([]response.DashboardProjectInsightToolResp, 0, len(toolKeys))
	for toolName := range toolKeys {
		count := toolCallCounts[toolName]
		tools = append(tools, response.DashboardProjectInsightToolResp{
			Tool:          toolName,
			CallCount:     count,
			ErrorCount:    toolErrorCounts[toolName],
			ErrorRate:     safeDiv(float64(toolErrorCounts[toolName]), float64(maxInt(count, 1))),
			WallTimeMS:    toolWallTimeMS[toolName],
			AvgWallTimeMS: int(math.Round(safeDiv(float64(toolWallTimeMS[toolName]), float64(maxInt(count, 1))))),
			SessionCount:  toolSessionCounts[toolName],
			Share:         safeDiv(float64(count), float64(maxInt(totalFunctionCalls, 1))),
		})
	}
	sort.Slice(tools, func(i, j int) bool {
		if tools[i].CallCount == tools[j].CallCount {
			return tools[i].Tool < tools[j].Tool
		}
		return tools[i].CallCount > tools[j].CallCount
	})

	return &response.DashboardProjectInsightsResp{
		ProjectID:                  req.ProjectID,
		Days:                       days,
		Models:                     models,
		Providers:                  providers,
		Tools:                      tools,
		KnownModelSessions:         knownModelSessions,
		UnknownModelSessions:       unknownModelSessions,
		KnownProviderSessions:      knownProviderSessions,
		UnknownProviderSessions:    unknownProviderSessions,
		KnownLatencySessions:       knownLatencySessions,
		UnknownLatencySessions:     unknownLatencySessions,
		KnownDurationSessions:      knownDurationSessions,
		UnknownDurationSessions:    unknownDurationSessions,
		AvgFirstResponseLatencyMS:  int(math.Round(safeDiv(float64(totalLatencyMS), float64(maxInt(knownLatencySessions, 1))))),
		AvgSessionDurationMS:       int(math.Round(safeDiv(float64(totalDurationMS), float64(maxInt(knownDurationSessions, 1))))),
		TotalCachedInputTokens:     totalCachedInputTokens,
		TotalReasoningOutputTokens: totalReasoningOutputTokens,
		TotalFunctionCalls:         totalFunctionCalls,
		TotalToolErrors:            totalToolErrors,
		TotalToolWallTimeMS:        totalToolWallTimeMS,
		AvgToolWallTimeMS:          int(math.Round(safeDiv(float64(totalToolWallTimeMS), float64(maxInt(totalFunctionCalls, 1))))),
		SessionsWithFunctionCalls:  sessionsWithFunctionCalls,
		SessionsWithToolErrors:     sessionsWithToolErrors,
		HarnessRunCount:            harnessRunCount,
		HarnessPassCount:           harnessPassCount,
		HarnessFailCount:           harnessFailCount,
		HarnessPassRate:            safeDiv(float64(harnessPassCount), float64(maxInt(harnessRunCount, 1))),
		LatestHarnessStatus:        latestHarnessStatus,
		LatestHarnessName:          latestHarnessName,
		LastFailingHarnessName:     lastFailingHarnessName,
		LatestHarnessAt:            latestHarnessAt,
		ResearchStatus:             cloneRecommendationResearchStatusResp(s.AnalyticsStore.recommendationResearch[req.ProjectID]),
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
	for _, project := range s.projectsForOrgLocked(req.OrgID) {
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
		left := s.AnalyticsStore.projects[items[i].ID]
		right := s.AnalyticsStore.projects[items[j].ID]
		if left != nil && right != nil && !left.ConnectedAt.Equal(right.ConnectedAt) {
			return left.ConnectedAt.After(right.ConnectedAt)
		}
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
	if activeExperiment := s.findActiveExperimentLocked(rec.ProjectID); activeExperiment != nil {
		return nil, ecode.InvalidParams.WithCause(ecode.NewInvalidParamsErr(
			fmt.Sprintf("active experiment %s must be resolved before creating another plan", activeExperiment.ID),
		))
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
	experiment := &Experiment{
		ID:               s.AnalyticsStore.nextID("exp"),
		ProjectID:        rec.ProjectID,
		RecommendationID: req.RecommendationID,
		ApplyID:          op.ID,
		RequestedBy:      req.RequestedBy,
		Scope:            scope,
		TargetMetric:     "tokens_per_query",
		Status:           "awaiting_review",
		Decision:         "pending",
		DecisionReason:   "waiting for approval",
		BaselineSessions: len(s.AnalyticsStore.sessionSummaries[rec.ProjectID]),
		BaselineQueries:  summarizeSessions(s.AnalyticsStore.sessionSummaries[rec.ProjectID]).QueryCount,
		CreatedAt:        now,
	}
	op.ExperimentID = experiment.ID
	if policyMode == "auto_approved" {
		op.Status = "approved_for_local_apply"
		op.ApprovalStatus = "approved"
		op.Decision = "auto_approved"
		op.ReviewedBy = "policy-engine"
		op.ReviewNote = policyReason
		op.ReviewedAt = &now
		experiment.Status = "queued_for_apply"
		experiment.Decision = "approved"
		experiment.DecisionReason = policyReason
		experiment.ApprovedAt = &now
	}
	s.AnalyticsStore.experiments[experiment.ID] = experiment
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
		ExperimentID:   experiment.ID,
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

func (s *AnalyticsService) ListExperiments(ctx context.Context, req *request.ExperimentListReq) (*response.ExperimentListResp, error) {
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

	items := make([]response.ExperimentSummaryResp, 0)
	for _, experiment := range s.AnalyticsStore.experiments {
		if experiment == nil || experiment.ProjectID != req.ProjectID {
			continue
		}
		items = append(items, s.toExperimentSummaryLocked(experiment))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ExperimentID > items[j].ExperimentID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})

	return &response.ExperimentListResp{Items: items}, nil
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
		s.markExperimentApprovedLocked(op.ExperimentID, now, "approved during review")
		s.appendAuditLocked(project.OrgID, op.ProjectID, "change_plan.approved", "change plan approved for local apply")
	default:
		op.Decision = "rejected"
		op.ApprovalStatus = "rejected"
		op.Status = "rejected"
		s.markExperimentResolvedLocked(op.ExperimentID, "rejected", "rejected", firstNonEmpty(req.ReviewNote, "rejected during review"), now)
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
		if op.ProjectID != req.ProjectID {
			continue
		}
		action := ""
		switch op.Status {
		case "approved_for_local_apply":
			action = "apply"
		case "rollback_requested":
			action = "rollback"
		default:
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
			ExperimentID:     op.ExperimentID,
			RecommendationID: op.RecommendationID,
			Action:           action,
			Status:           op.Status,
			PolicyMode:       op.PolicyMode,
			PolicyReason:     op.PolicyReason,
			ApprovalStatus:   op.ApprovalStatus,
			Scope:            op.Scope,
			RequestedBy:      op.RequestedBy,
			RequestedAt:      op.RequestedAt,
			Note:             op.Note,
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
			ExperimentID:                    op.ExperimentID,
			RecommendationID:                op.RecommendationID,
			Status:                          op.Status,
			EvaluationMode:                  experimentField(s.AnalyticsStore.experiments[op.ExperimentID], func(exp *Experiment) string { return exp.EvaluationMode }),
			EvaluationModel:                 experimentField(s.AnalyticsStore.experiments[op.ExperimentID], func(exp *Experiment) string { return exp.EvaluationModel }),
			EvaluationDecision:              experimentField(s.AnalyticsStore.experiments[op.ExperimentID], func(exp *Experiment) string { return exp.EvaluationDecision }),
			EvaluationConfidence:            experimentField(s.AnalyticsStore.experiments[op.ExperimentID], func(exp *Experiment) string { return exp.EvaluationConfidence }),
			EvaluationSummary:               experimentField(s.AnalyticsStore.experiments[op.ExperimentID], func(exp *Experiment) string { return exp.EvaluationSummary }),
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
	op.LastReportedAt = &now
	op.AppliedFile = req.AppliedFile
	op.AppliedSettings = cloneAnyMap(req.AppliedSettings)
	op.AppliedText = req.AppliedText
	op.Note = req.Note
	op.RolledBack = req.RolledBack
	switch {
	case req.RolledBack:
		if op.RolledBackAt == nil {
			op.RolledBackAt = &now
		}
		op.Status = "rollback_confirmed"
		s.markExperimentResolvedLocked(op.ExperimentID, "rolled_back", "rollback", firstNonEmpty(req.Note, "rollback confirmed"), now)
	case req.Success:
		if op.AppliedAt == nil {
			op.AppliedAt = &now
		}
		op.Status = "applied"
		s.markExperimentMeasuringLocked(op.ExperimentID, *op.AppliedAt, firstNonEmpty(req.Note, "waiting for post-apply sessions"))
	default:
		if op.Status == "rollback_requested" {
			op.Status = "rollback_failed"
			s.markExperimentResolvedLocked(op.ExperimentID, "rollback_failed", "rollback_failed", firstNonEmpty(req.Note, "local rollback failed"), now)
		} else {
			op.Status = "failed"
			s.markExperimentResolvedLocked(op.ExperimentID, "failed", "failed", firstNonEmpty(req.Note, "local apply failed"), now)
		}
	}
	if op.ApprovalStatus == "" {
		op.ApprovalStatus = "approved"
	}

	s.appendAuditLocked(project.OrgID, op.ProjectID, "execution.result", op.Status)
	if err := s.AnalyticsStore.persistLocked(); err != nil {
		return nil, err
	}

	return &response.ApplyResultResp{
		ApplyID:      op.ID,
		ExperimentID: op.ExperimentID,
		Status:       op.Status,
		AppliedAt:    derefTime(op.AppliedAt, now),
		RolledBack:   op.RolledBack,
	}, nil
}

func (s *AnalyticsService) ReportHarnessRun(ctx context.Context, req *request.HarnessRunReq) (*response.HarnessRunResp, error) {
	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	project, err := s.resolveProjectLocked(ctx, req.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeProject(ctx, project); err != nil {
		return nil, err
	}

	specFile := strings.TrimSpace(req.SpecFile)
	name := strings.TrimSpace(req.Name)
	if specFile == "" {
		return nil, ecode.InvalidParams.WithCause(ecode.NewInvalidParamsErr("spec_file is required"))
	}
	if name == "" {
		return nil, ecode.InvalidParams.WithCause(ecode.NewInvalidParamsErr("name is required"))
	}
	recommendationID := strings.TrimSpace(req.RecommendationID)
	if recommendationID != "" {
		rec, ok := s.AnalyticsStore.recommendations[recommendationID]
		if !ok || rec == nil || rec.ProjectID != project.ID {
			return nil, ecode.InvalidParams.WithCause(ecode.NewInvalidParamsErr("unknown recommendation_id"))
		}
	}
	applyID := strings.TrimSpace(req.ApplyID)
	if applyID != "" {
		op, ok := s.AnalyticsStore.applyOperations[applyID]
		if !ok || op == nil || op.ProjectID != project.ID {
			return nil, ecode.InvalidParams.WithCause(ecode.NewInvalidParamsErr("unknown apply_id"))
		}
		if recommendationID == "" {
			recommendationID = op.RecommendationID
		} else if strings.TrimSpace(op.RecommendationID) != recommendationID {
			return nil, ecode.InvalidParams.WithCause(ecode.NewInvalidParamsErr("apply_id does not match recommendation_id"))
		}
	}

	now := time.Now().UTC()
	startedAt := req.StartedAt.UTC()
	if startedAt.IsZero() {
		startedAt = now
	}
	completedAt := req.CompletedAt.UTC()
	if completedAt.IsZero() {
		if req.DurationMS > 0 {
			completedAt = startedAt.Add(time.Duration(req.DurationMS) * time.Millisecond)
		} else {
			completedAt = now
		}
	}
	if completedAt.Before(startedAt) {
		completedAt = startedAt
	}
	durationMS := req.DurationMS
	if durationMS < 0 {
		durationMS = 0
	}
	if durationMS == 0 {
		durationMS = completedAt.Sub(startedAt).Milliseconds()
	}

	commands := make([]HarnessCommandResult, 0, len(req.Commands))
	for _, item := range req.Commands {
		commands = append(commands, HarnessCommandResult{
			Phase:      strings.TrimSpace(item.Phase),
			Command:    strings.TrimSpace(item.Command),
			ExitCode:   item.ExitCode,
			DurationMS: item.DurationMS,
			Output:     strings.TrimSpace(item.Output),
			Passed:     item.Passed,
			Error:      strings.TrimSpace(item.Error),
		})
	}

	status := "passed"
	if !req.Passed {
		status = "failed"
	}
	run := &HarnessRun{
		ID:               s.AnalyticsStore.nextID("harness"),
		ProjectID:        project.ID,
		RecommendationID: recommendationID,
		ApplyID:          applyID,
		SpecFile:         filepath.Clean(specFile),
		Name:             name,
		Goal:             strings.TrimSpace(req.Goal),
		Status:           status,
		Passed:           req.Passed,
		Reason:           strings.TrimSpace(req.Reason),
		RootDir:          strings.TrimSpace(req.RootDir),
		DurationMS:       durationMS,
		Commands:         commands,
		TriggeredBy:      s.actorFromContext(ctx, req.TriggeredBy),
		StartedAt:        startedAt,
		CompletedAt:      completedAt,
		CreatedAt:        now,
	}
	s.AnalyticsStore.harnessRuns[run.ID] = run
	s.appendAuditLocked(project.OrgID, project.ID, "harness.run", run.Status)
	if err := s.AnalyticsStore.persistLocked(); err != nil {
		return nil, err
	}

	resp := toHarnessRunResp(run)
	return &resp, nil
}

func (s *AnalyticsService) currentRecommendationsLocked(projectID string) []*Recommendation {
	ids := s.AnalyticsStore.projectRecommendations[projectID]
	items := make([]*Recommendation, 0, len(ids))
	for _, id := range ids {
		rec, ok := s.AnalyticsStore.recommendations[id]
		if !ok || rec == nil {
			continue
		}
		items = append(items, rec)
	}
	return items
}

func (s *AnalyticsService) evaluateExperimentsLocked(project *Project, observedAt time.Time) {
	if project == nil {
		return
	}
	sessions := s.AnalyticsStore.sessionSummaries[project.ID]
	for _, experiment := range s.AnalyticsStore.experiments {
		if experiment == nil || experiment.ProjectID != project.ID || experiment.Status != "measuring" {
			continue
		}
		op := s.AnalyticsStore.applyOperations[experiment.ApplyID]
		if op == nil || op.AppliedAt == nil {
			continue
		}
		before, after := splitSessionsByApplyTime(sessions, *op.AppliedAt)
		beforeStats := summarizeSessions(before)
		afterStats := summarizeSessions(after)

		qualitative := s.maybeReviewExperimentLocked(project, experiment, before, after, beforeStats, afterStats, len(after), observedAt)
		nextStatus, decision, reason := qualitativeOutcome(qualitative)
		experiment.Decision = decision
		experiment.DecisionReason = reason

		switch nextStatus {
		case "measuring":
			continue
		case "completed":
			s.markExperimentResolvedLocked(experiment.ID, "completed", "keep", reason, observedAt)
			s.appendAuditLocked(project.OrgID, project.ID, "experiment.completed", reason)
		case "rollback_requested":
			s.markExperimentRollbackRequestedLocked(experiment.ID, reason)
			op.Status = "rollback_requested"
			op.Note = reason
			s.appendAuditLocked(project.OrgID, project.ID, "rollback.requested", reason)
		}
	}
}

func qualitativeOutcome(review experimentEvaluation) (string, string, string) {
	reason := strings.TrimSpace(review.Summary)
	if reason == "" {
		reason = "The qualitative review did not provide a decision yet."
	}
	switch review.Decision {
	case "keep":
		return "completed", "keep", reason
	case "rollback":
		return "rollback_requested", "rollback", reason
	default:
		return "measuring", "observe", reason
	}
}

func (s *AnalyticsService) refreshRecommendationsLocked(project *Project, triggerSessionID string) []*Recommendation {
	sessions := s.AnalyticsStore.sessionSummaries[project.ID]
	rawQueries := collectRawQueries(sessions)
	status := RecommendationResearchStatus{
		ProjectID:           project.ID,
		Provider:            s.researchAgent.Provider,
		Model:               s.researchAgent.Model,
		MinimumSessions:     s.recommendationMinSessions,
		SessionCount:        len(sessions),
		RawQueryCount:       len(rawQueries),
		TriggerSessionID:    triggerSessionID,
		RecommendationCount: len(s.currentRecommendationsLocked(project.ID)),
	}
	triggeredAt := time.Now().UTC()
	status.TriggeredAt = cloneTime(&triggeredAt)
	if len(sessions) < s.recommendationMinSessions {
		status.State = "waiting_for_min_sessions"
		status.Summary = fmt.Sprintf("Collected %d of %d sessions needed before fetching new recommendations.", len(sessions), s.recommendationMinSessions)
		s.setRecommendationResearchStatusLocked(project.ID, status)
		return s.currentRecommendationsLocked(project.ID)
	}
	if strings.TrimSpace(s.researchAgent.apiKey) == "" {
		status.State = "disabled"
		status.Summary = "OpenAI-backed recommendation research is disabled on this server, so no new suggestions will be fetched."
		s.setRecommendationResearchStatusLocked(project.ID, status)
		return s.currentRecommendationsLocked(project.ID)
	}
	if len(rawQueries) == 0 {
		status.State = "missing_raw_queries"
		status.Summary = "Uploaded sessions are missing raw query evidence, so the server cannot fetch targeted recommendations yet."
		s.setRecommendationResearchStatusLocked(project.ID, status)
		return s.currentRecommendationsLocked(project.ID)
	}
	status.State = "running"
	status.Summary = fmt.Sprintf("Fetching recommendation suggestions from %s across %d uploaded sessions.", s.researchAgent.Provider, len(sessions))
	status.StartedAt = cloneTime(&triggeredAt)
	s.setRecommendationResearchStatusLocked(project.ID, status)

	previousIDs := s.AnalyticsStore.projectRecommendations[project.ID]
	for _, id := range previousIDs {
		if rec, ok := s.AnalyticsStore.recommendations[id]; ok && rec.Status == "active" {
			rec.Status = "superseded"
		}
	}

	startedAt := time.Now()
	rawCandidates, err := s.researchAgent.AnalyzeProject(project, sessions, s.AnalyticsStore.configSnapshots[project.ID])
	completedAt := time.Now().UTC()
	status.CompletedAt = cloneTime(&completedAt)
	status.LastDurationMS = int(time.Since(startedAt) / time.Millisecond)
	if err != nil {
		status.State = "failed"
		status.Summary = fmt.Sprintf("Recommendation research failed after %s while waiting for %s.", humanizeDurationMS(status.LastDurationMS), s.researchAgent.Provider)
		status.LastError = err.Error()
		s.setRecommendationResearchStatusLocked(project.ID, status)
		return s.currentRecommendationsLocked(project.ID)
	}
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
	status.RecommendationCount = len(ids)
	status.LastError = ""
	if len(ids) == 0 {
		status.State = "no_recommendations"
		status.Summary = fmt.Sprintf("Recommendation research finished in %s but did not produce any actionable suggestions.", humanizeDurationMS(status.LastDurationMS))
	} else {
		status.State = "succeeded"
		status.Summary = fmt.Sprintf("Recommendation research finished in %s and produced %d suggestion(s).", humanizeDurationMS(status.LastDurationMS), len(ids))
		status.LastSuccessfulAt = cloneTime(&completedAt)
	}
	s.setRecommendationResearchStatusLocked(project.ID, status)

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
		HarnessSpec:      cloneHarnessSpec(tpl.HarnessSpec),
		SettingsUpdates:  cloneAnyMap(tpl.Settings),
		RawSuggestion:    tpl.RawSuggestion,
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
		HarnessSpec:      toHarnessSpecResp(rec.HarnessSpec),
		SettingsUpdates:  cloneAnyMap(rec.SettingsUpdates),
		RawSuggestion:    rec.RawSuggestion,
		CreatedAt:        rec.CreatedAt,
	}
}

func toHarnessSpecResp(spec *HarnessSpec) *response.HarnessSpecResp {
	if spec == nil {
		return nil
	}
	return &response.HarnessSpecResp{
		Version:       spec.Version,
		Name:          spec.Name,
		Goal:          spec.Goal,
		TargetPaths:   cloneStringSlice(spec.TargetPaths),
		SetupCommands: cloneStringSlice(spec.SetupCommands),
		TestCommands:  cloneStringSlice(spec.TestCommands),
		Assertions:    toHarnessAssertionResp(spec.Assertions),
		AntiGoals:     cloneStringSlice(spec.AntiGoals),
	}
}

func toHarnessAssertionResp(items []HarnessAssertion) []response.HarnessAssertionResp {
	out := make([]response.HarnessAssertionResp, 0, len(items))
	for _, item := range items {
		out = append(out, response.HarnessAssertionResp{
			Kind:        item.Kind,
			Equals:      item.Equals,
			Contains:    item.Contains,
			NotContains: item.NotContains,
		})
	}
	return out
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

func toHarnessCommandResultsResp(items []HarnessCommandResult) []response.HarnessCommandResultResp {
	out := make([]response.HarnessCommandResultResp, 0, len(items))
	for _, item := range items {
		out = append(out, response.HarnessCommandResultResp{
			Phase:      item.Phase,
			Command:    item.Command,
			ExitCode:   item.ExitCode,
			DurationMS: item.DurationMS,
			Output:     item.Output,
			Passed:     item.Passed,
			Error:      item.Error,
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

func toHarnessRunResp(run *HarnessRun) response.HarnessRunResp {
	return response.HarnessRunResp{
		ID:               run.ID,
		ProjectID:        run.ProjectID,
		RecommendationID: run.RecommendationID,
		ApplyID:          run.ApplyID,
		SpecFile:         run.SpecFile,
		Name:             run.Name,
		Goal:             run.Goal,
		Status:           run.Status,
		Passed:           run.Passed,
		Reason:           run.Reason,
		RootDir:          run.RootDir,
		DurationMS:       run.DurationMS,
		TriggeredBy:      run.TriggeredBy,
		Commands:         toHarnessCommandResultsResp(run.Commands),
		StartedAt:        run.StartedAt,
		CompletedAt:      run.CompletedAt,
		CreatedAt:        run.CreatedAt,
	}
}

func toApplyHistoryItem(op *ApplyOperation) response.ApplyHistoryItem {
	return response.ApplyHistoryItem{
		ApplyID:          op.ID,
		ExperimentID:     op.ExperimentID,
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
		LastReportedAt:   op.LastReportedAt,
		RolledBackAt:     op.RolledBackAt,
		AppliedFile:      op.AppliedFile,
		AppliedSettings:  cloneAnyMap(op.AppliedSettings),
		AppliedText:      op.AppliedText,
		PatchPreview:     toPatchPreviewResp(op.PatchPreview),
		RolledBack:       op.RolledBack,
	}
}

func (s *AnalyticsService) findActiveExperimentLocked(projectID string) *Experiment {
	var active *Experiment
	for _, experiment := range s.AnalyticsStore.experiments {
		if experiment == nil || experiment.ProjectID != projectID {
			continue
		}
		switch experiment.Status {
		case "awaiting_review", "queued_for_apply", "measuring", "rollback_requested":
		default:
			continue
		}
		if active == nil || experiment.CreatedAt.After(active.CreatedAt) {
			active = experiment
		}
	}
	return active
}

func (s *AnalyticsService) toExperimentSummaryLocked(experiment *Experiment) response.ExperimentSummaryResp {
	postApplySessions := 0
	postApplyQueries := 0
	var lastObservedAt *time.Time

	sessions := s.AnalyticsStore.sessionSummaries[experiment.ProjectID]
	for _, session := range sessions {
		if session == nil {
			continue
		}
		if lastObservedAt == nil || session.Timestamp.After(*lastObservedAt) {
			ts := session.Timestamp
			lastObservedAt = &ts
		}
	}
	if experiment.AppliedAt != nil {
		_, after := splitSessionsByApplyTime(sessions, *experiment.AppliedAt)
		postApplySessions = len(after)
		postApplyQueries = summarizeSessions(after).QueryCount
	}

	return response.ExperimentSummaryResp{
		ExperimentID:         experiment.ID,
		ProjectID:            experiment.ProjectID,
		RecommendationID:     experiment.RecommendationID,
		ApplyID:              experiment.ApplyID,
		Status:               experiment.Status,
		Decision:             experiment.Decision,
		DecisionReason:       experiment.DecisionReason,
		EvaluationMode:       experiment.EvaluationMode,
		EvaluationModel:      experiment.EvaluationModel,
		EvaluationDecision:   experiment.EvaluationDecision,
		EvaluationConfidence: experiment.EvaluationConfidence,
		EvaluationSummary:    experiment.EvaluationSummary,
		TargetMetric:         experiment.TargetMetric,
		RequestedBy:          experiment.RequestedBy,
		Scope:                experiment.Scope,
		BaselineSessions:     experiment.BaselineSessions,
		BaselineQueries:      experiment.BaselineQueries,
		PostApplySessions:    postApplySessions,
		PostApplyQueries:     postApplyQueries,
		CreatedAt:            experiment.CreatedAt,
		ApprovedAt:           experiment.ApprovedAt,
		AppliedAt:            experiment.AppliedAt,
		EvaluatedAt:          experiment.EvaluatedAt,
		LastObservedAt:       lastObservedAt,
		ResolvedAt:           experiment.ResolvedAt,
	}
}

func (s *AnalyticsService) markExperimentApprovedLocked(experimentID string, approvedAt time.Time, reason string) {
	experiment, ok := s.AnalyticsStore.experiments[experimentID]
	if !ok || experiment == nil {
		return
	}
	experiment.Status = "queued_for_apply"
	experiment.Decision = "approved"
	experiment.DecisionReason = firstNonEmpty(reason, "approved")
	experiment.ApprovedAt = &approvedAt
	experiment.ResolvedAt = nil
}

func (s *AnalyticsService) markExperimentMeasuringLocked(experimentID string, appliedAt time.Time, reason string) {
	experiment, ok := s.AnalyticsStore.experiments[experimentID]
	if !ok || experiment == nil {
		return
	}
	experiment.Status = "measuring"
	experiment.Decision = "observe"
	experiment.DecisionReason = firstNonEmpty(reason, "waiting for post-apply sessions")
	if experiment.AppliedAt == nil {
		experiment.AppliedAt = &appliedAt
	}
	experiment.EvaluationDecision = ""
	experiment.EvaluationConfidence = ""
	experiment.EvaluationSummary = ""
	experiment.EvaluatedAt = nil
	experiment.ResolvedAt = nil
}

func (s *AnalyticsService) markExperimentRollbackRequestedLocked(experimentID, reason string) {
	experiment, ok := s.AnalyticsStore.experiments[experimentID]
	if !ok || experiment == nil {
		return
	}
	experiment.Status = "rollback_requested"
	experiment.Decision = "rollback"
	experiment.DecisionReason = firstNonEmpty(reason, "rollback requested")
	experiment.ResolvedAt = nil
}

func (s *AnalyticsService) markExperimentResolvedLocked(experimentID, status, decision, reason string, resolvedAt time.Time) {
	experiment, ok := s.AnalyticsStore.experiments[experimentID]
	if !ok || experiment == nil {
		return
	}
	experiment.Status = status
	experiment.Decision = decision
	experiment.DecisionReason = firstNonEmpty(reason, experiment.DecisionReason)
	experiment.ResolvedAt = &resolvedAt
}

func (s *AnalyticsService) maybeReviewExperimentLocked(project *Project, experiment *Experiment, before, after []*SessionSummary, beforeStats, afterStats sessionSummaryStats, afterCount int, observedAt time.Time) experimentEvaluation {
	if experiment == nil || afterCount == 0 {
		return experimentEvaluation{}
	}

	review := s.evaluationAgent.ReviewExperiment(project, before, after, beforeStats, afterStats)
	experiment.EvaluationMode = review.Mode
	experiment.EvaluationModel = review.Model
	experiment.EvaluationDecision = review.Decision
	experiment.EvaluationConfidence = review.Confidence
	experiment.EvaluationSummary = review.Summary
	experiment.EvaluatedAt = &observedAt
	return review
}

func experimentField(experiment *Experiment, pick func(*Experiment) string) string {
	if experiment == nil || pick == nil {
		return ""
	}
	return pick(experiment)
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

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}

func cloneRecommendationResearchStatusResp(status *RecommendationResearchStatus) *response.RecommendationResearchStatusResp {
	if status == nil {
		return nil
	}
	return &response.RecommendationResearchStatusResp{
		State:               status.State,
		Summary:             status.Summary,
		Provider:            status.Provider,
		Model:               status.Model,
		MinimumSessions:     status.MinimumSessions,
		SessionCount:        status.SessionCount,
		RawQueryCount:       status.RawQueryCount,
		RecommendationCount: status.RecommendationCount,
		TriggerSessionID:    status.TriggerSessionID,
		LastError:           status.LastError,
		TriggeredAt:         cloneTime(status.TriggeredAt),
		StartedAt:           cloneTime(status.StartedAt),
		CompletedAt:         cloneTime(status.CompletedAt),
		LastSuccessfulAt:    cloneTime(status.LastSuccessfulAt),
		LastDurationMS:      status.LastDurationMS,
	}
}

func isRecommendationResearchStatusNewer(candidate, current *RecommendationResearchStatus) bool {
	if candidate == nil {
		return false
	}
	if current == nil {
		return true
	}
	candidateAt := derefTime(candidate.TriggeredAt, time.Time{})
	currentAt := derefTime(current.TriggeredAt, time.Time{})
	if candidateAt.Equal(currentAt) {
		return candidate.ProjectID > current.ProjectID
	}
	return candidateAt.After(currentAt)
}

func (s *AnalyticsService) setRecommendationResearchStatusLocked(projectID string, status RecommendationResearchStatus) {
	status.ProjectID = projectID
	s.AnalyticsStore.recommendationResearch[projectID] = &status
}

func humanizeDurationMS(value int) string {
	if value <= 0 {
		return "under 1s"
	}
	if value < 1000 {
		return fmt.Sprintf("%dms", value)
	}
	if value < 60000 {
		return fmt.Sprintf("%.1fs", float64(value)/1000)
	}
	minutes := value / 60000
	seconds := (value % 60000) / 1000
	if seconds == 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dm %ds", minutes, seconds)
}

func derefTime(value *time.Time, fallback time.Time) time.Time {
	if value == nil {
		return fallback
	}
	return *value
}

func latestHarnessRecordedAt(run *HarnessRun) time.Time {
	if run == nil {
		return time.Time{}
	}
	if !run.CompletedAt.IsZero() {
		return run.CompletedAt
	}
	if !run.StartedAt.IsZero() {
		return run.StartedAt
	}
	return run.CreatedAt
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
	AvgFirstResponseLatencyMS float64
	AvgSessionDurationMS      float64
	AvgToolErrorsPerSession   float64
}

func summarizeSessions(sessions []*SessionSummary) sessionSummaryStats {
	if len(sessions) == 0 {
		return sessionSummaryStats{}
	}

	totalInputTokens := 0
	totalOutputTokens := 0
	totalTokens := 0
	totalQueries := 0
	totalLatencyMS := 0
	latencyCount := 0
	totalDurationMS := 0
	durationCount := 0
	totalToolErrors := 0
	for _, session := range sessions {
		totalInputTokens += session.TokenIn
		totalOutputTokens += session.TokenOut
		totalTokens += session.TokenIn + session.TokenOut
		totalQueries += queryCountForSession(session)
		totalToolErrors += session.ToolErrorCount
		if session.FirstResponseLatencyMS > 0 {
			totalLatencyMS += session.FirstResponseLatencyMS
			latencyCount++
		}
		if session.SessionDurationMS > 0 {
			totalDurationMS += session.SessionDurationMS
			durationCount++
		}
	}

	return sessionSummaryStats{
		QueryCount:                totalQueries,
		AvgInputTokensPerQuery:    safeDiv(float64(totalInputTokens), float64(maxInt(totalQueries, 1))),
		AvgOutputTokensPerQuery:   safeDiv(float64(totalOutputTokens), float64(maxInt(totalQueries, 1))),
		AvgTokensPerQuery:         safeDiv(float64(totalTokens), float64(maxInt(totalQueries, 1))),
		AvgInputTokensPerSession:  safeDiv(float64(totalInputTokens), float64(len(sessions))),
		AvgOutputTokensPerSession: safeDiv(float64(totalOutputTokens), float64(len(sessions))),
		AvgTokensPerSession:       safeDiv(float64(totalTokens), float64(len(sessions))),
		AvgFirstResponseLatencyMS: safeDiv(float64(totalLatencyMS), float64(maxInt(latencyCount, 1))),
		AvgSessionDurationMS:      safeDiv(float64(totalDurationMS), float64(maxInt(durationCount, 1))),
		AvgToolErrorsPerSession:   safeDiv(float64(totalToolErrors), float64(len(sessions))),
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
		return fmt.Sprintf("%d recommendation(s) are ready to review.", activeRecommendations)
	default:
		return "No rollout action is waiting right now."
	}
}

func buildDashboardOutcomeSummary(successfulRollouts, failedExecutions int, rollbackRate float64) string {
	switch {
	case failedExecutions > 0:
		return fmt.Sprintf("%d rollout(s) need attention after failing local execution.", failedExecutions)
	case successfulRollouts == 0:
		return "No completed rollouts yet. Approve a change to start measuring workflow impact."
	case rollbackRate >= 0.25:
		return "Recent rollouts are being reversed too often. Narrow the next configuration change."
	default:
		return "Recent configuration changes are landing. Keep uploading sessions to measure workflow impact."
	}
}
