package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Royaltyprogram/aiops/dto/request"
	"github.com/Royaltyprogram/aiops/dto/response"
	"github.com/Royaltyprogram/aiops/pkg/ecode"
)

type AnalyticsService struct {
	Options
	researchAgent     *CloudResearchAgent
	reportMinSessions int
	reportRefreshMu   sync.Mutex
	reportRefreshLive map[string]bool
	reportRefreshNext map[string]*reportRefreshJob
}

const sharedWorkspaceName = "Shared workspace"
const defaultReportMinSessions = 10

func NewAnalyticsService(opt Options) *AnalyticsService {
	reportMinSessions := opt.ReportMinSessions
	if reportMinSessions <= 0 {
		reportMinSessions = defaultReportMinSessions
	}
	return &AnalyticsService{
		Options:           opt,
		researchAgent:     NewCloudResearchAgent(opt.Config),
		reportMinSessions: reportMinSessions,
		reportRefreshLive: make(map[string]bool),
		reportRefreshNext: make(map[string]*reportRefreshJob),
	}
}

type reportRefreshJob struct {
	project          *Project
	sessions         []*SessionSummary
	snapshots        []*ConfigSnapshot
	triggerSessionID string
	triggeredAt      time.Time
}

type auditEventInput struct {
	ProjectID    string
	Type         string
	Message      string
	ActorUserID  string
	ActorRole    string
	TargetUserID string
	Result       string
	Reason       string
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
	s.appendAuditLocked(ctx, user.OrgID, auditEventInput{
		Type:        "auth.login",
		Message:     "dashboard session created",
		ActorUserID: user.ID,
		ActorRole:   normalizeUserRole(user.Role),
		Result:      "success",
		Reason:      "user authenticated with email and password",
	})
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
	s.appendAuditLocked(ctx, identity.OrgID, auditEventInput{
		Type:    "auth.logout",
		Message: "dashboard session revoked",
		Result:  "success",
		Reason:  "user signed out from the dashboard",
	})
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

	s.appendAuditLocked(ctx, identity.OrgID, auditEventInput{
		Type:    "auth.cli_token_issued",
		Message: "cli token issued from dashboard",
		Result:  "success",
		Reason:  "user issued a new CLI token from the dashboard",
	})
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
	s.appendAuditLocked(ctx, identity.OrgID, auditEventInput{
		Type:    "auth.cli_token_revoked",
		Message: "cli token revoked from dashboard",
		Result:  "success",
		Reason:  "user revoked a CLI token from the dashboard",
	})
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

	s.appendAuditLocked(ctx, identity.OrgID, auditEventInput{
		Type:         "device.registered",
		Message:      "local cli device registered",
		TargetUserID: user.ID,
		Result:       "success",
		Reason:       "cli device authenticated and registered",
	})
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
		UserRole:      normalizeUserRole(user.Role),
		UserStatus:    normalizeUserStatus(user.Status),
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

func (s *AnalyticsService) requireAdminUserLocked(ctx context.Context) (AuthIdentity, *User, error) {
	identity, err := s.requireUserIdentity(ctx, TokenKindWebSession)
	if err != nil {
		return AuthIdentity{}, nil, err
	}
	user, ok := s.AnalyticsStore.users[identity.UserID]
	if !ok {
		return AuthIdentity{}, nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown user_id"))
	}
	if !userCanAuthenticate(user) {
		return AuthIdentity{}, nil, ecode.Forbidden(1008, "user account is not active")
	}
	if normalizeUserRole(user.Role) != userRoleAdmin {
		return AuthIdentity{}, nil, ecode.Forbidden(1009, "admin access is required")
	}
	return identity, user, nil
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

	identity, hasIdentity := AuthIdentityFromContext(ctx)
	if err := s.authorizeOrg(ctx, req.OrgID); err != nil {
		return nil, err
	}
	if hasIdentity && identity.TokenKind != TokenKindStatic {
		if req.UserID != "" && req.UserID != identity.UserID {
			return nil, ecode.Forbidden(1007, "token cannot register a different user")
		}
		req.UserID = identity.UserID
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
	if hasIdentity && identity.TokenKind != TokenKindStatic {
		if user == nil {
			return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown user_id"))
		}
		if !userCanAuthenticate(user) {
			return nil, ecode.Forbidden(1008, "user account is not active")
		}
	} else if user == nil {
		user = &User{
			ID:        req.UserID,
			OrgID:     req.OrgID,
			Email:     req.UserEmail,
			Role:      userRoleMember,
			Status:    userStatusActive,
			CreatedAt: now,
		}
		s.AnalyticsStore.users[req.UserID] = user
	} else {
		user.OrgID = req.OrgID
		if !hasIdentity || identity.TokenKind == TokenKindStatic {
			if strings.TrimSpace(req.UserEmail) != "" {
				user.Email = req.UserEmail
			}
		}
	}
	if user.Role == "" {
		user.Role = userRoleMember
	}
	if user.Status == "" {
		user.Status = userStatusActive
	}
	if user.CreatedAt.IsZero() {
		user.CreatedAt = now
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

	s.appendAuditLocked(ctx, req.OrgID, auditEventInput{
		Type:         "device.registered",
		Message:      "local cli device registered",
		TargetUserID: req.UserID,
		Result:       "success",
		Reason:       "collector registered a local cli device",
	})
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

	s.appendAuditLocked(ctx, req.OrgID, auditEventInput{
		ProjectID: projectID,
		Type:      "workspace.connected",
		Message:   "shared workspace connected to aiops",
		Result:    "success",
		Reason:    "collector connected the shared workspace",
	})
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

	s.appendAuditLocked(ctx, project.OrgID, auditEventInput{
		ProjectID: req.ProjectID,
		Type:      "config.snapshot",
		Message:   "config snapshot uploaded from local collector",
		Result:    "success",
		Reason:    "collector uploaded a config snapshot",
	})
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

	project, err := s.resolveProjectLocked(ctx, req.ProjectID)
	if err != nil {
		s.AnalyticsStore.mu.Unlock()
		return nil, err
	}
	if err := s.authorizeProject(ctx, project); err != nil {
		s.AnalyticsStore.mu.Unlock()
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
		ReasoningSummaries:     cloneStringSlice(req.ReasoningSummaries),
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

	reports, refreshJob := s.prepareReportRefreshLocked(project, sessionID)
	ids := make([]string, 0, len(reports))
	for _, item := range reports {
		ids = append(ids, item.ID)
	}

	s.appendAuditLocked(ctx, project.OrgID, auditEventInput{
		ProjectID: req.ProjectID,
		Type:      "session.ingested",
		Message:   "session summary uploaded from local collector",
		Result:    "success",
		Reason:    "collector uploaded a session summary",
	})
	if err := s.AnalyticsStore.persistLocked(); err != nil {
		s.AnalyticsStore.mu.Unlock()
		return nil, err
	}

	resp := &response.SessionIngestResp{
		SchemaVersion:   reportAPISchemaVersion,
		SessionID:       sessionID,
		ProjectID:       req.ProjectID,
		ReportCount:     len(ids),
		LatestReportIDs: cloneStringSlice(ids),
		RecordedAt:      recordedAt,
		ResearchStatus:  cloneReportResearchStatusResp(s.AnalyticsStore.reportResearch[project.ID]),
	}
	s.AnalyticsStore.mu.Unlock()

	s.enqueueReportRefresh(refreshJob)
	return resp, nil
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
			ReasoningSummaries:     cloneStringSlice(session.ReasoningSummaries),
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

func (s *AnalyticsService) ListReports(ctx context.Context, req *request.ReportListReq) (*response.ReportListResp, error) {
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

	ids := s.AnalyticsStore.projectReports[req.ProjectID]
	items := make([]response.ReportResp, 0, len(ids))
	for _, id := range ids {
		rec, ok := s.AnalyticsStore.reports[id]
		if !ok {
			continue
		}
		items = append(items, toReportResp(rec))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Score == items[j].Score {
			return items[i].ID < items[j].ID
		}
		return items[i].Score > items[j].Score
	})

	return &response.ReportListResp{
		SchemaVersion: reportAPISchemaVersion,
		Items:         items,
	}, nil
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
		totalSessions      int
		totalInputTokens   int
		totalOutputTokens  int
		totalTokens        int
		totalQueries       int
		totalActiveReports int
		lastIngestedAt     *time.Time
		researchStatus     *ReportResearchStatus
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

		for _, reportID := range s.AnalyticsStore.projectReports[projectID] {
			rec := s.AnalyticsStore.reports[reportID]
			if rec != nil && rec.Status == "active" {
				totalActiveReports++
			}
		}
		if candidate := s.AnalyticsStore.reportResearch[projectID]; isReportResearchStatusNewer(candidate, researchStatus) {
			researchStatus = candidate
		}
	}

	return &response.DashboardOverviewResp{
		SchemaVersion:             reportAPISchemaVersion,
		OrgID:                     req.OrgID,
		TotalDevices:              countDevicesByOrg(s.AnalyticsStore.agents, req.OrgID),
		TotalProjects:             len(projectIDs),
		TotalSessions:             totalSessions,
		ActiveReports:             totalActiveReports,
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
		ActionSummary:             buildDashboardActionSummary(totalActiveReports, researchStatus),
		OutcomeSummary:            buildDashboardOutcomeSummary(totalSessions, totalActiveReports, researchStatus),
		ResearchProvider:          s.researchAgent.Provider,
		ResearchMode:              s.researchAgent.Mode,
		LastIngestedAt:            lastIngestedAt,
		ResearchStatus:            cloneReportResearchStatusResp(researchStatus),
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
	for _, reportID := range s.AnalyticsStore.projectReports[req.ProjectID] {
		report := s.AnalyticsStore.reports[reportID]
		if report == nil {
			continue
		}
		day := report.CreatedAt.UTC().Format("2006-01-02")
		ensureDay(day).point.ReportCount++
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
		SchemaVersion:              reportAPISchemaVersion,
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
		ResearchStatus:             cloneReportResearchStatusResp(s.AnalyticsStore.reportResearch[req.ProjectID]),
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

func (s *AnalyticsService) AuditList(ctx context.Context, req *request.AuditListReq) (*response.AuditListResp, error) {
	s.AnalyticsStore.mu.RLock()
	defer s.AnalyticsStore.mu.RUnlock()

	if _, ok := s.AnalyticsStore.organizations[req.OrgID]; !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown org_id"))
	}
	if err := s.authorizeOrg(ctx, req.OrgID); err != nil {
		return nil, err
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	typeFilter := strings.TrimSpace(req.Type)
	actorFilter := strings.TrimSpace(req.ActorUserID)
	targetFilter := strings.TrimSpace(req.TargetUserID)

	items := make([]response.AuditEventResp, 0)
	for i := len(s.AnalyticsStore.audits) - 1; i >= 0; i-- {
		audit := s.AnalyticsStore.audits[i]
		if audit.OrgID != req.OrgID {
			continue
		}
		if req.ProjectID != "" && audit.ProjectID != req.ProjectID {
			continue
		}
		if typeFilter != "" && audit.Type != typeFilter {
			continue
		}
		if actorFilter != "" && audit.ActorUserID != actorFilter {
			continue
		}
		if targetFilter != "" && audit.TargetUserID != targetFilter {
			continue
		}
		items = append(items, response.AuditEventResp{
			ID:           audit.ID,
			OrgID:        audit.OrgID,
			ProjectID:    audit.ProjectID,
			Type:         audit.Type,
			Message:      audit.Message,
			ActorUserID:  audit.ActorUserID,
			ActorRole:    audit.ActorRole,
			TargetUserID: audit.TargetUserID,
			SourceIP:     audit.SourceIP,
			UserAgent:    audit.UserAgent,
			Result:       firstNonEmpty(strings.TrimSpace(audit.Result), "success"),
			Reason:       audit.Reason,
			CreatedAt:    audit.CreatedAt,
		})
		if len(items) >= limit {
			break
		}
	}

	return &response.AuditListResp{Items: items}, nil
}

func (s *AnalyticsService) ListAdminUsers(ctx context.Context, req *request.AdminUserListReq) (*response.AdminUserListResp, error) {
	s.AnalyticsStore.mu.RLock()
	defer s.AnalyticsStore.mu.RUnlock()

	identity, _, err := s.requireAdminUserLocked(ctx)
	if err != nil {
		return nil, err
	}

	search := strings.ToLower(strings.TrimSpace(req.Search))
	roleFilter := strings.TrimSpace(req.Role)
	if roleFilter != "" {
		role, ok := parseUserRole(roleFilter)
		if !ok {
			return nil, ecode.InvalidParams.WithCause(ecode.NewInvalidParamsErr("invalid role"))
		}
		roleFilter = role
	}
	statusFilter := strings.ToLower(strings.TrimSpace(req.Status))
	if statusFilter != "" {
		switch statusFilter {
		case userStatusActive, userStatusDisabled, userStatusDeleted:
		default:
			return nil, ecode.InvalidParams.WithCause(ecode.NewInvalidParamsErr("invalid status"))
		}
	}

	items := make([]response.AdminUserResp, 0)
	for _, user := range s.AnalyticsStore.users {
		if user == nil || user.OrgID != identity.OrgID {
			continue
		}
		status := normalizeUserStatus(user.Status)
		if status == userStatusDeleted && !req.IncludeDeleted {
			continue
		}
		if roleFilter != "" && normalizeUserRole(user.Role) != roleFilter {
			continue
		}
		if statusFilter != "" && status != statusFilter {
			continue
		}
		if search != "" {
			haystack := strings.ToLower(strings.Join([]string{user.ID, user.Email, user.Name}, "\n"))
			if !strings.Contains(haystack, search) {
				continue
			}
		}
		items = append(items, toAdminUserResp(user))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Email == items[j].Email {
			return items[i].ID < items[j].ID
		}
		return items[i].Email < items[j].Email
	})

	return &response.AdminUserListResp{Items: items}, nil
}

func (s *AnalyticsService) CreateAdminUser(ctx context.Context, req *request.AdminUserCreateReq) (*response.AdminUserCreateResp, error) {
	now := time.Now().UTC()

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	identity, _, err := s.requireAdminUserLocked(ctx)
	if err != nil {
		return nil, err
	}

	email := normalizeEmail(req.Email)
	name := strings.TrimSpace(req.Name)
	if email == "" {
		return nil, ecode.InvalidParams.WithCause(ecode.NewInvalidParamsErr("email is required"))
	}
	if name == "" {
		return nil, ecode.InvalidParams.WithCause(ecode.NewInvalidParamsErr("name is required"))
	}
	if existing := s.AnalyticsStore.findUserByEmailLocked(email); existing != nil {
		return nil, ecode.InvalidParams.WithCause(ecode.NewInvalidParamsErr("email already exists"))
	}
	role, ok := parseUserRole(req.Role)
	if !ok {
		return nil, ecode.InvalidParams.WithCause(ecode.NewInvalidParamsErr("invalid role"))
	}
	salt, hash := hashPassword(req.Password, "")
	user := &User{
		ID:                s.AnalyticsStore.nextID("user"),
		OrgID:             identity.OrgID,
		Email:             email,
		Name:              name,
		Source:            userSourceManaged,
		Role:              role,
		Status:            userStatusActive,
		PasswordSalt:      salt,
		PasswordHash:      hash,
		CreatedAt:         now,
		PasswordChangedAt: cloneTime(&now),
	}
	s.AnalyticsStore.users[user.ID] = user
	s.appendAuditLocked(ctx, identity.OrgID, auditEventInput{
		Type:         "admin.user_created",
		Message:      "admin created a managed user",
		TargetUserID: user.ID,
		Result:       "success",
		Reason:       "managed user created from the admin console",
	})
	if err := s.AnalyticsStore.persistLocked(); err != nil {
		return nil, err
	}

	return &response.AdminUserCreateResp{
		Status: "created",
		User:   toAdminUserResp(user),
	}, nil
}

func (s *AnalyticsService) ResetAdminUserPassword(ctx context.Context, req *request.AdminUserPasswordResetReq) (*response.AdminUserPasswordResetResp, error) {
	now := time.Now().UTC()

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	identity, _, err := s.requireAdminUserLocked(ctx)
	if err != nil {
		return nil, err
	}
	user, err := s.findAdminManagedUserLocked(identity.OrgID, req.UserID)
	if err != nil {
		return nil, err
	}
	salt, hash := hashPassword(req.Password, "")
	user.PasswordSalt = salt
	user.PasswordHash = hash
	user.PasswordChangedAt = cloneTime(&now)
	s.AnalyticsStore.revokeUserTokensLocked(user.ID, now)
	s.appendAuditLocked(ctx, identity.OrgID, auditEventInput{
		Type:         "admin.user_password_reset",
		Message:      "admin reset a user password",
		TargetUserID: user.ID,
		Result:       "success",
		Reason:       "managed user password reset and sessions revoked",
	})
	if err := s.AnalyticsStore.persistLocked(); err != nil {
		return nil, err
	}

	return &response.AdminUserPasswordResetResp{
		Status:            "password_reset",
		UserID:            user.ID,
		PasswordChangedAt: now,
	}, nil
}

func (s *AnalyticsService) DeactivateAdminUser(ctx context.Context, req *request.AdminUserDeactivateReq) (*response.AdminUserDeactivateResp, error) {
	now := time.Now().UTC()

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	identity, _, err := s.requireAdminUserLocked(ctx)
	if err != nil {
		return nil, err
	}
	user, err := s.findAdminManagedUserLocked(identity.OrgID, req.UserID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureUserCanLoseAccessLocked(user); err != nil {
		return nil, err
	}
	user.Status = userStatusDisabled
	user.DisabledAt = cloneTime(&now)
	s.AnalyticsStore.revokeUserTokensLocked(user.ID, now)
	s.appendAuditLocked(ctx, identity.OrgID, auditEventInput{
		Type:         "admin.user_deactivated",
		Message:      "admin deactivated a user",
		TargetUserID: user.ID,
		Result:       "success",
		Reason:       "managed user disabled and sessions revoked",
	})
	if err := s.AnalyticsStore.persistLocked(); err != nil {
		return nil, err
	}

	return &response.AdminUserDeactivateResp{
		Status:     "deactivated",
		UserID:     user.ID,
		DisabledAt: now,
	}, nil
}

func (s *AnalyticsService) DeleteAdminUser(ctx context.Context, req *request.AdminUserDeleteReq) (*response.AdminUserDeleteResp, error) {
	now := time.Now().UTC()

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	identity, _, err := s.requireAdminUserLocked(ctx)
	if err != nil {
		return nil, err
	}
	user, err := s.findAdminManagedUserLocked(identity.OrgID, req.UserID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureUserCanLoseAccessLocked(user); err != nil {
		return nil, err
	}
	user.Status = userStatusDeleted
	user.DeletedAt = cloneTime(&now)
	if user.DisabledAt == nil {
		user.DisabledAt = cloneTime(&now)
	}
	s.AnalyticsStore.revokeUserTokensLocked(user.ID, now)
	s.appendAuditLocked(ctx, identity.OrgID, auditEventInput{
		Type:         "admin.user_deleted",
		Message:      "admin deleted a user",
		TargetUserID: user.ID,
		Result:       "success",
		Reason:       "managed user soft-deleted and sessions revoked",
	})
	if err := s.AnalyticsStore.persistLocked(); err != nil {
		return nil, err
	}

	return &response.AdminUserDeleteResp{
		Status:    "deleted",
		UserID:    user.ID,
		DeletedAt: now,
	}, nil
}

func (s *AnalyticsService) currentReportsLocked(projectID string) []*Report {
	ids := s.AnalyticsStore.projectReports[projectID]
	items := make([]*Report, 0, len(ids))
	for _, id := range ids {
		rec, ok := s.AnalyticsStore.reports[id]
		if !ok || rec == nil {
			continue
		}
		items = append(items, rec)
	}
	return items
}

func (s *AnalyticsService) prepareReportRefreshLocked(project *Project, triggerSessionID string) ([]*Report, *reportRefreshJob) {
	sessions := s.AnalyticsStore.sessionSummaries[project.ID]
	rawQueries := collectRawQueries(sessions)
	currentReports := s.currentReportsLocked(project.ID)
	currentStatus := s.AnalyticsStore.reportResearch[project.ID]
	sessionCount := len(sessions)
	status := ReportResearchStatus{
		ProjectID:        project.ID,
		Provider:         s.researchAgent.Provider,
		Model:            s.researchAgent.Model,
		MinimumSessions:  s.reportMinSessions,
		SessionCount:     sessionCount,
		RawQueryCount:    len(rawQueries),
		TriggerSessionID: triggerSessionID,
		ReportCount:      len(currentReports),
	}
	triggeredAt := time.Now().UTC()
	status.TriggeredAt = cloneTime(&triggeredAt)
	if currentStatus != nil && currentStatus.SessionCount == sessionCount {
		return currentReports, nil
	}
	if sessionCount < s.reportMinSessions {
		status.State = "waiting_for_min_sessions"
		status.Summary = fmt.Sprintf("Collected %d of %d sessions needed before generating the first feedback report.", sessionCount, s.reportMinSessions)
		s.setReportResearchStatusLocked(project.ID, status)
		return currentReports, nil
	}
	if !shouldRefreshReportsForSessionCount(sessionCount, s.reportMinSessions) {
		if currentStatus != nil && strings.EqualFold(strings.TrimSpace(currentStatus.State), "running") {
			return currentReports, nil
		}
		preserveReportResearchHistory(&status, currentStatus)
		status.State = "waiting_for_next_batch"
		nextRefreshAt := nextReportRefreshSessionCount(sessionCount, s.reportMinSessions)
		if len(currentReports) > 0 {
			status.Summary = fmt.Sprintf("Collected %d sessions. The current feedback report stays active until %d sessions are collected for the next refresh.", sessionCount, nextRefreshAt)
		} else {
			status.Summary = fmt.Sprintf("Collected %d sessions. The next feedback analysis starts at %d sessions.", sessionCount, nextRefreshAt)
		}
		s.setReportResearchStatusLocked(project.ID, status)
		return currentReports, nil
	}
	if strings.TrimSpace(s.researchAgent.apiKey) == "" {
		status.State = "disabled"
		status.Summary = "OpenAI-backed report research is disabled on this server, so no new feedback reports will be generated."
		s.setReportResearchStatusLocked(project.ID, status)
		return currentReports, nil
	}
	if len(rawQueries) == 0 {
		status.State = "missing_raw_queries"
		status.Summary = "Uploaded sessions are missing raw query evidence, so the server cannot generate a targeted feedback report yet."
		s.setReportResearchStatusLocked(project.ID, status)
		return currentReports, nil
	}
	status.State = "running"
	status.Summary = fmt.Sprintf("Preparing the next feedback report with %s while uploaded sessions are analyzed in the background.", s.researchAgent.Provider)
	status.StartedAt = cloneTime(&triggeredAt)
	s.setReportResearchStatusLocked(project.ID, status)
	return currentReports, &reportRefreshJob{
		project:          cloneProject(project),
		sessions:         cloneSessionSummaries(sessions),
		snapshots:        cloneConfigSnapshots(s.AnalyticsStore.configSnapshots[project.ID]),
		triggerSessionID: triggerSessionID,
		triggeredAt:      triggeredAt,
	}
}

func shouldRefreshReportsForSessionCount(sessionCount, batchSize int) bool {
	if sessionCount <= 0 || batchSize <= 0 {
		return false
	}
	return sessionCount >= batchSize && sessionCount%batchSize == 0
}

func nextReportRefreshSessionCount(sessionCount, batchSize int) int {
	if batchSize <= 0 {
		return 0
	}
	if sessionCount < batchSize {
		return batchSize
	}
	return ((sessionCount / batchSize) + 1) * batchSize
}

func preserveReportResearchHistory(dst, src *ReportResearchStatus) {
	if dst == nil || src == nil {
		return
	}
	dst.LastError = src.LastError
	dst.StartedAt = cloneTime(src.StartedAt)
	dst.CompletedAt = cloneTime(src.CompletedAt)
	dst.LastSuccessfulAt = cloneTime(src.LastSuccessfulAt)
	dst.LastDurationMS = src.LastDurationMS
}

func (s *AnalyticsService) enqueueReportRefresh(job *reportRefreshJob) {
	if job == nil || job.project == nil {
		return
	}

	s.reportRefreshMu.Lock()
	projectID := job.project.ID
	if s.reportRefreshLive[projectID] {
		s.reportRefreshNext[projectID] = job
		s.reportRefreshMu.Unlock()
		return
	}
	s.reportRefreshLive[projectID] = true
	s.reportRefreshMu.Unlock()

	go func(current *reportRefreshJob) {
		for current != nil {
			s.runReportRefresh(current)

			s.reportRefreshMu.Lock()
			next := s.reportRefreshNext[projectID]
			if next != nil {
				delete(s.reportRefreshNext, projectID)
				s.reportRefreshMu.Unlock()
				current = next
				continue
			}
			delete(s.reportRefreshLive, projectID)
			s.reportRefreshMu.Unlock()
			current = nil
		}
	}(job)
}

func (s *AnalyticsService) runReportRefresh(job *reportRefreshJob) {
	if job == nil || job.project == nil {
		return
	}

	startedAt := time.Now()
	rawCandidates, err := s.researchAgent.AnalyzeProject(job.project, job.sessions, job.snapshots)
	completedAt := time.Now().UTC()
	durationMS := int(time.Since(startedAt) / time.Millisecond)

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	current := s.AnalyticsStore.reportResearch[job.project.ID]
	if !reportRefreshMatchesRun(current, job.triggeredAt) {
		return
	}

	status := *current
	status.CompletedAt = cloneTime(&completedAt)
	status.LastDurationMS = durationMS
	status.TriggerSessionID = job.triggerSessionID
	if err != nil {
		status.State = "failed"
		status.Summary = fmt.Sprintf("Feedback analysis failed after %s while waiting for %s.", humanizeDurationMS(status.LastDurationMS), s.researchAgent.Provider)
		status.LastError = err.Error()
		s.setReportResearchStatusLocked(job.project.ID, status)
		_ = s.AnalyticsStore.persistLocked()
		return
	}

	previousIDs := s.AnalyticsStore.projectReports[job.project.ID]
	for _, id := range previousIDs {
		if rec, ok := s.AnalyticsStore.reports[id]; ok && rec.Status == "active" {
			rec.Status = "superseded"
		}
	}

	candidates := make([]*Report, 0, len(rawCandidates))
	for _, candidate := range rawCandidates {
		candidates = append(candidates, s.newReportRecordLocked(job.project, candidate))
	}

	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.ID)
		s.AnalyticsStore.reports[candidate.ID] = candidate
	}
	s.AnalyticsStore.projectReports[job.project.ID] = ids
	status.ReportCount = len(ids)
	status.LastError = ""
	if len(ids) == 0 {
		status.State = "no_reports"
		status.Summary = fmt.Sprintf("Feedback analysis finished in %s but did not produce a publishable report.", humanizeDurationMS(status.LastDurationMS))
	} else {
		status.State = "succeeded"
		status.Summary = fmt.Sprintf("Feedback analysis finished in %s and produced %d report(s).", humanizeDurationMS(status.LastDurationMS), len(ids))
		status.LastSuccessfulAt = cloneTime(&completedAt)
	}
	s.setReportResearchStatusLocked(job.project.ID, status)
	_ = s.AnalyticsStore.persistLocked()
}

func reportRefreshMatchesRun(current *ReportResearchStatus, triggeredAt time.Time) bool {
	if current == nil || current.TriggeredAt == nil {
		return false
	}
	return current.TriggeredAt.Equal(triggeredAt)
}

func cloneProject(project *Project) *Project {
	if project == nil {
		return nil
	}
	cloned := *project
	cloned.LanguageMix = cloneFloatMap(project.LanguageMix)
	cloned.LastIngestedAt = cloneTime(project.LastIngestedAt)
	return &cloned
}

func cloneSessionSummaries(items []*SessionSummary) []*SessionSummary {
	if len(items) == 0 {
		return nil
	}
	out := make([]*SessionSummary, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		cloned := *item
		cloned.ToolCalls = cloneIntMap(item.ToolCalls)
		cloned.ToolErrors = cloneIntMap(item.ToolErrors)
		cloned.ToolWallTimesMS = cloneIntMap(item.ToolWallTimesMS)
		cloned.RawQueries = cloneStringSlice(item.RawQueries)
		cloned.Models = cloneStringSlice(item.Models)
		cloned.AssistantResponses = cloneStringSlice(item.AssistantResponses)
		cloned.ReasoningSummaries = cloneStringSlice(item.ReasoningSummaries)
		out = append(out, &cloned)
	}
	return out
}

func cloneConfigSnapshots(items []*ConfigSnapshot) []*ConfigSnapshot {
	if len(items) == 0 {
		return nil
	}
	out := make([]*ConfigSnapshot, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		cloned := *item
		cloned.Settings = cloneAnyMap(item.Settings)
		cloned.InstructionFiles = cloneStringSlice(item.InstructionFiles)
		cloned.RecentConfigChanges = cloneStringSlice(item.RecentConfigChanges)
		out = append(out, &cloned)
	}
	return out
}

func (s *AnalyticsService) newReportRecordLocked(project *Project, tpl researchReport) *Report {
	tool := project.DefaultTool
	if strings.TrimSpace(tool) == "" {
		tool = "codex"
	}
	return &Report{
		ID:                  s.AnalyticsStore.nextID("rec"),
		ProjectID:           project.ID,
		Kind:                tpl.Kind,
		Title:               tpl.Title,
		Summary:             tpl.Summary,
		UserIntent:          tpl.UserIntent,
		ModelInterpretation: tpl.ModelInterpretation,
		Reason:              tpl.Reason,
		Explanation:         tpl.Explanation,
		ExpectedBenefit:     tpl.ExpectedBenefit,
		Risk:                tpl.Risk,
		ExpectedImpact:      tpl.ExpectedImpact,
		Confidence:          tpl.Confidence,
		Strengths:           cloneStringSlice(tpl.Strengths),
		Frictions:           cloneStringSlice(tpl.Frictions),
		NextSteps:           cloneStringSlice(tpl.NextSteps),
		Score:               tpl.Score,
		Status:              "active",
		TargetTool:          tool,
		ResearchProvider:    s.researchAgent.Provider,
		ResearchModel:       s.researchAgent.Model,
		Evidence:            cloneStringSlice(tpl.Evidence),
		RawSuggestion:       tpl.RawSuggestion,
		CreatedAt:           time.Now().UTC(),
	}
}

func toReportResp(rec *Report) response.ReportResp {
	return response.ReportResp{
		ID:                  rec.ID,
		ProjectID:           rec.ProjectID,
		Kind:                rec.Kind,
		Title:               rec.Title,
		Summary:             rec.Summary,
		UserIntent:          rec.UserIntent,
		ModelInterpretation: rec.ModelInterpretation,
		Reason:              rec.Reason,
		Explanation:         rec.Explanation,
		ExpectedBenefit:     rec.ExpectedBenefit,
		Risk:                rec.Risk,
		ExpectedImpact:      rec.ExpectedImpact,
		Confidence:          rec.Confidence,
		Strengths:           cloneStringSlice(rec.Strengths),
		Frictions:           cloneStringSlice(rec.Frictions),
		NextSteps:           cloneStringSlice(rec.NextSteps),
		Status:              rec.Status,
		Score:               rec.Score,
		TargetTool:          rec.TargetTool,
		ResearchProvider:    rec.ResearchProvider,
		ResearchModel:       rec.ResearchModel,
		Evidence:            cloneStringSlice(rec.Evidence),
		RawSuggestion:       rec.RawSuggestion,
		CreatedAt:           rec.CreatedAt,
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

func (s *AnalyticsService) appendAuditLocked(ctx context.Context, orgID string, input auditEventInput) {
	metadata, _ := RequestMetadataFromContext(ctx)
	identity, hasIdentity := AuthIdentityFromContext(ctx)

	actorUserID := strings.TrimSpace(input.ActorUserID)
	actorRole := normalizeUserRole(input.ActorRole)
	if actorRole == "" {
		actorRole = userRoleMember
	}
	if actorUserID == "" && hasIdentity && identity.TokenKind != TokenKindStatic {
		actorUserID = identity.UserID
	}
	if input.ActorRole == "" && hasIdentity && identity.TokenKind != TokenKindStatic {
		actorRole = normalizeUserRole(identity.UserRole)
	}
	if actorUserID == "" {
		actorRole = ""
	}

	result := strings.TrimSpace(input.Result)
	if result == "" {
		result = "success"
	}

	s.AnalyticsStore.audits = append(s.AnalyticsStore.audits, &AuditEvent{
		ID:           s.AnalyticsStore.nextID("audit"),
		OrgID:        orgID,
		ProjectID:    strings.TrimSpace(input.ProjectID),
		Type:         strings.TrimSpace(input.Type),
		Message:      strings.TrimSpace(input.Message),
		ActorUserID:  actorUserID,
		ActorRole:    actorRole,
		TargetUserID: strings.TrimSpace(input.TargetUserID),
		SourceIP:     metadata.SourceIP,
		UserAgent:    metadata.UserAgent,
		Result:       result,
		Reason:       strings.TrimSpace(input.Reason),
		CreatedAt:    time.Now().UTC(),
	})
}

func (s *AnalyticsService) findAdminManagedUserLocked(orgID, userID string) (*User, error) {
	userID = strings.TrimSpace(userID)
	user, ok := s.AnalyticsStore.users[userID]
	if !ok || user == nil || user.OrgID != orgID {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown user_id"))
	}
	if normalizeUserStatus(user.Status) == userStatusDeleted {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown user_id"))
	}
	return user, nil
}

func (s *AnalyticsService) ensureUserCanLoseAccessLocked(target *User) error {
	if target == nil {
		return ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown user_id"))
	}
	if normalizeUserRole(target.Role) != userRoleAdmin || !userCanAuthenticate(target) {
		return nil
	}
	if s.countActiveAdminsByOrgLocked(target.OrgID) <= 1 {
		return ecode.Forbidden(1010, "cannot remove the last active admin")
	}
	return nil
}

func (s *AnalyticsService) countActiveAdminsByOrgLocked(orgID string) int {
	total := 0
	for _, user := range s.AnalyticsStore.users {
		if user == nil || user.OrgID != orgID {
			continue
		}
		if normalizeUserRole(user.Role) != userRoleAdmin {
			continue
		}
		if !userCanAuthenticate(user) {
			continue
		}
		total++
	}
	return total
}

func toSessionUser(user *User) response.AuthUserResp {
	if user == nil {
		return response.AuthUserResp{}
	}
	return response.AuthUserResp{
		ID:     user.ID,
		Name:   user.Name,
		Email:  user.Email,
		Role:   normalizeUserRole(user.Role),
		Status: normalizeUserStatus(user.Status),
	}
}

func toAdminUserResp(user *User) response.AdminUserResp {
	if user == nil {
		return response.AdminUserResp{}
	}
	return response.AdminUserResp{
		ID:                user.ID,
		OrgID:             user.OrgID,
		Email:             user.Email,
		Name:              user.Name,
		Source:            user.Source,
		Role:              normalizeUserRole(user.Role),
		Status:            normalizeUserStatus(user.Status),
		CreatedAt:         user.CreatedAt,
		PasswordChangedAt: cloneTime(user.PasswordChangedAt),
		LastLoginAt:       cloneTime(user.LastLoginAt),
		DisabledAt:        cloneTime(user.DisabledAt),
		DeletedAt:         cloneTime(user.DeletedAt),
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

func cloneReportResearchStatusResp(status *ReportResearchStatus) *response.ReportResearchStatusResp {
	if status == nil {
		return nil
	}
	reportCount := status.ReportCount
	return &response.ReportResearchStatusResp{
		SchemaVersion:    reportAPISchemaVersion,
		State:            normalizeResearchStatusState(status.State),
		Summary:          status.Summary,
		Provider:         status.Provider,
		Model:            status.Model,
		MinimumSessions:  status.MinimumSessions,
		SessionCount:     status.SessionCount,
		RawQueryCount:    status.RawQueryCount,
		ReportCount:      reportCount,
		TriggerSessionID: status.TriggerSessionID,
		LastError:        status.LastError,
		TriggeredAt:      cloneTime(status.TriggeredAt),
		StartedAt:        cloneTime(status.StartedAt),
		CompletedAt:      cloneTime(status.CompletedAt),
		LastSuccessfulAt: cloneTime(status.LastSuccessfulAt),
		LastDurationMS:   status.LastDurationMS,
	}
}

func normalizeResearchStatusState(state string) string {
	return strings.TrimSpace(state)
}

func isReportResearchStatusNewer(candidate, current *ReportResearchStatus) bool {
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

func (s *AnalyticsService) setReportResearchStatusLocked(projectID string, status ReportResearchStatus) {
	status.ProjectID = projectID
	s.AnalyticsStore.reportResearch[projectID] = &status
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

func buildDashboardActionSummary(activeReports int, researchStatus *ReportResearchStatus) string {
	if researchStatus != nil && strings.EqualFold(strings.TrimSpace(researchStatus.State), "running") {
		return "A fresh feedback report is currently being prepared."
	}
	switch {
	case activeReports > 0:
		return fmt.Sprintf("%d feedback report(s) are ready to review.", activeReports)
	default:
		return "No feedback report is waiting right now."
	}
}

func buildDashboardOutcomeSummary(totalSessions, activeReports int, researchStatus *ReportResearchStatus) string {
	state := ""
	if researchStatus != nil {
		state = strings.ToLower(strings.TrimSpace(researchStatus.State))
	}
	switch {
	case state == "failed":
		return "The latest analysis pass needs attention before the next report can be published."
	case activeReports > 0:
		return "Recent sessions are giving the research engine enough signal to compare usage patterns over time."
	case totalSessions == 0:
		return "Upload sessions so the system can start building workflow feedback."
	case state == "running":
		return "The latest uploaded sessions are being analyzed for the next workflow report."
	default:
		return "Keep uploading sessions so the system can build sharper usage feedback."
	}
}
