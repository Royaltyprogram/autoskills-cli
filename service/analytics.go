package service

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"net/http"
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
	refineAgent       *SkillRefineAgent
	reportMinSessions int
	reportRefreshMu   sync.Mutex
	reportRefreshLive map[string]bool
	reportRefreshNext map[string][]*reportRefreshJob
	sessionImportMu   sync.Mutex
	sessionImportLive map[string]bool
}

const sharedWorkspaceName = "Shared workspace"
const defaultReportMinSessions = 10
const defaultReportSessionAnalysisCap = 100

const (
	sessionImportJobStatusReceiving = "receiving_chunks"
	sessionImportJobStatusQueued    = "queued"
	sessionImportJobStatusRunning   = "running"
	sessionImportJobStatusSucceeded = "succeeded"
	sessionImportJobStatusPartial   = "partially_failed"
	sessionImportJobStatusFailed    = "failed"
	sessionImportJobStatusCanceled  = "canceled"
)

const (
	sessionImportJobActiveTTL    = 24 * time.Hour
	sessionImportJobRetentionTTL = 7 * 24 * time.Hour
)

func NewAnalyticsService(opt Options) *AnalyticsService {
	reportMinSessions := opt.ReportMinSessions
	if reportMinSessions <= 0 {
		reportMinSessions = defaultReportMinSessions
	}
	return &AnalyticsService{
		Options:           opt,
		researchAgent:     NewCloudResearchAgent(opt.Config),
		refineAgent:       NewSkillRefineAgent(opt.Config),
		reportMinSessions: reportMinSessions,
		reportRefreshLive: make(map[string]bool),
		reportRefreshNext: make(map[string][]*reportRefreshJob),
		sessionImportLive: make(map[string]bool),
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

func (s *AnalyticsService) CurrentSession(ctx context.Context) (*response.AuthSessionResp, error) {
	identity, err := s.requireUserIdentity(ctx, TokenKindWebSession)
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
	identity, err := s.requireUserIdentity(ctx, TokenKindWebSession)
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
	audit := s.appendAuditLocked(ctx, identity.OrgID, auditEventInput{
		Type:    "auth.logout",
		Message: "dashboard session revoked",
		Result:  "success",
		Reason:  "user signed out from the dashboard",
	})
	if err := s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
		if err := s.AnalyticsStore.persistAccessTokenLocked(tx, record.ID); err != nil {
			return err
		}
		return s.AnalyticsStore.persistAuditLocked(tx, audit.ID)
	}); err != nil {
		return nil, err
	}

	return &response.LogoutResp{
		Status:    "signed_out",
		RevokedAt: now,
	}, nil
}

func (s *AnalyticsService) IssueCLIToken(ctx context.Context, req *request.IssueCLITokenReq) (*response.CLITokenIssueResp, error) {
	identity, err := s.requireCLITokenManagerIdentity(ctx)
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
		label = "CLI enrollment token"
	}

	tokenValue, tokenRecord, err := s.AnalyticsStore.issueAccessTokenLocked(TokenKindCLIEnrollment, identity.OrgID, identity.UserID, label, defaultCLITokenTTL, now)
	if err != nil {
		return nil, err
	}

	audit := s.appendAuditLocked(ctx, identity.OrgID, auditEventInput{
		Type:    "auth.cli_token_issued",
		Message: "cli token issued from dashboard",
		Result:  "success",
		Reason:  "user issued a new CLI token from the dashboard",
	})
	if err := s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
		if err := s.AnalyticsStore.persistAccessTokenLocked(tx, tokenRecord.ID); err != nil {
			return err
		}
		return s.AnalyticsStore.persistAuditLocked(tx, audit.ID)
	}); err != nil {
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
	identity, err := s.requireCLITokenManagerIdentity(ctx)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()

	s.AnalyticsStore.mu.RLock()
	defer s.AnalyticsStore.mu.RUnlock()

	items := make([]response.CLITokenItemResp, 0)
	for _, record := range s.AnalyticsStore.accessTokens {
		if !isCLITokenKind(record.Kind) || record.OrgID != identity.OrgID || record.UserID != identity.UserID {
			continue
		}
		items = append(items, response.CLITokenItemResp{
			TokenID:     record.ID,
			Kind:        record.Kind,
			TokenPrefix: record.TokenPrefix,
			Label:       record.Label,
			Status:      accessTokenStatus(record, now),
			CreatedAt:   record.CreatedAt,
			ExpiresAt:   record.ExpiresAt,
			LastUsedAt:  record.LastUsedAt,
			LastSeenAt:  record.LastSeenAt,
			ConsumedAt:  record.ConsumedAt,
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
	identity, err := s.requireCLITokenManagerIdentity(ctx)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	record, ok := s.AnalyticsStore.accessTokens[req.TokenID]
	if !ok || !isCLITokenKind(record.Kind) {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown token_id"))
	}
	if record.OrgID != identity.OrgID || record.UserID != identity.UserID {
		return nil, ecode.Forbidden(1006, "token cannot be managed by this user")
	}

	rootTokenID := s.AnalyticsStore.accessTokenChainRootLocked(record.ID)
	if rootTokenID == "" {
		rootTokenID = record.ID
	}
	if !s.AnalyticsStore.revokeAccessTokenChainLocked(rootTokenID, now) && record.RevokedAt == nil {
		record.RevokedAt = cloneTime(&now)
	}
	tokenIDs := s.AnalyticsStore.accessTokenChainIDsLocked(rootTokenID)
	audit := s.appendAuditLocked(ctx, identity.OrgID, auditEventInput{
		Type:    "auth.cli_token_revoked",
		Message: "cli token revoked from dashboard",
		Result:  "success",
		Reason:  "user revoked a CLI token from the dashboard",
	})
	if err := s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
		if err := s.AnalyticsStore.persistAccessTokensLocked(tx, tokenIDs...); err != nil {
			return err
		}
		return s.AnalyticsStore.persistAuditLocked(tx, audit.ID)
	}); err != nil {
		return nil, err
	}

	return &response.CLITokenRevokeResp{
		TokenID:   record.ID,
		Status:    "revoked",
		RevokedAt: now,
	}, nil
}

func (s *AnalyticsService) AuthenticateCLI(ctx context.Context, req *request.CLILoginReq) (*response.CLILoginResp, error) {
	identity, err := s.requireUserIdentity(ctx, TokenKindCLIEnrollment, TokenKindCLI)
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

	tokenRecord, ok := s.AnalyticsStore.accessTokens[identity.TokenID]
	if !ok {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown token_id"))
	}
	tokenRecord.LastUsedAt = cloneTime(&now)
	if tokenRecord.Kind == TokenKindCLIEnrollment {
		tokenRecord.ConsumedAt = cloneTime(&now)
	}

	agent := &Agent{
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
	s.AnalyticsStore.agents[deviceID] = agent
	s.AnalyticsStore.revokeAgentTokensLocked(deviceID, now)

	accessToken, accessRecord, refreshToken, refreshRecord, err := s.issueDeviceTokenPairLocked(identity, agent, tokenRecord.ID, 0, now)
	if err != nil {
		return nil, err
	}

	agentTokenIDs := s.AnalyticsStore.accessTokenIDsForAgentLocked(deviceID)
	audit := s.appendAuditLocked(ctx, identity.OrgID, auditEventInput{
		Type:         "device.registered",
		Message:      "local cli device registered",
		TargetUserID: user.ID,
		Result:       "success",
		Reason:       "cli device authenticated and registered",
	})
	if err := s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
		if err := s.AnalyticsStore.persistAgentLocked(tx, agent.ID); err != nil {
			return err
		}
		if err := s.AnalyticsStore.persistAccessTokensLocked(tx, append(agentTokenIDs, tokenRecord.ID)...); err != nil {
			return err
		}
		return s.AnalyticsStore.persistAuditLocked(tx, audit.ID)
	}); err != nil {
		return nil, err
	}

	return &response.CLILoginResp{
		AccessToken:      accessToken,
		AccessExpiresAt:  accessRecord.ExpiresAt,
		RefreshToken:     refreshToken,
		RefreshExpiresAt: refreshRecord.ExpiresAt,
		TokenType:        "Bearer",
		AgentID:          deviceID,
		DeviceID:         deviceID,
		OrgID:            org.ID,
		OrgName:          org.Name,
		UserID:           user.ID,
		UserName:         user.Name,
		UserEmail:        user.Email,
		UserRole:         normalizeUserRole(user.Role),
		UserStatus:       normalizeUserStatus(user.Status),
		Status:           "registered",
		ConsentScopes:    consentScopes,
		RegisteredAt:     now,
	}, nil
}

func (s *AnalyticsService) RefreshCLI(ctx context.Context, req *request.CLIRefreshReq) (*response.CLIRefreshResp, error) {
	now := time.Now().UTC()
	secretHash := hashSecret(req.RefreshToken)
	if secretHash == "" {
		return nil, ecode.Unauthorized(1001, "invalid refresh token")
	}

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	record := s.AnalyticsStore.findAccessTokenBySecretHashLocked(secretHash)
	if record == nil || record.Kind != TokenKindDeviceRefresh {
		return nil, ecode.Unauthorized(1001, "invalid refresh token")
	}
	if record.ExpiresAt != nil && now.After(record.ExpiresAt.UTC()) {
		return nil, ecode.Unauthorized(ErrCodeDeviceRefreshTokenExpired, "device refresh token expired")
	}
	if record.RevokedAt != nil {
		return nil, ecode.Unauthorized(1001, "invalid refresh token")
	}
	if record.ConsumedAt != nil {
		if rootTokenID := s.AnalyticsStore.accessTokenChainRootLocked(record.ID); rootTokenID != "" {
			s.AnalyticsStore.revokeAccessTokenChainLocked(rootTokenID, now)
			tokenIDs := s.AnalyticsStore.accessTokenChainIDsLocked(rootTokenID)
			_ = s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
				return s.AnalyticsStore.persistAccessTokensLocked(tx, tokenIDs...)
			})
		}
		return nil, ecode.Unauthorized(1001, "refresh token has already been used")
	}

	user, ok := s.AnalyticsStore.users[record.UserID]
	if !ok || !userCanAuthenticate(user) {
		return nil, ecode.Unauthorized(1001, "invalid refresh token")
	}

	agent := s.AnalyticsStore.agents[record.AgentID]
	if agent == nil || agent.OrgID != record.OrgID || agent.UserID != record.UserID {
		return nil, ecode.Unauthorized(1001, "invalid refresh token")
	}

	record.LastUsedAt = cloneTime(&now)
	record.ConsumedAt = cloneTime(&now)
	s.AnalyticsStore.revokeAccessTokenChainLocked(record.ID, now)

	identity := AuthIdentity{
		TokenID:   record.ID,
		TokenKind: record.Kind,
		OrgID:     record.OrgID,
		UserID:    record.UserID,
		AgentID:   record.AgentID,
		UserRole:  normalizeUserRole(user.Role),
	}

	accessToken, accessRecord, refreshToken, refreshRecord, err := s.issueDeviceTokenPairLocked(identity, agent, record.ID, record.RotationCounter+1, now)
	if err != nil {
		return nil, err
	}
	tokenIDs := append(s.AnalyticsStore.accessTokenChainIDsLocked(record.ID), accessRecord.ID, refreshRecord.ID)
	if err := s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
		return s.AnalyticsStore.persistAccessTokensLocked(tx, tokenIDs...)
	}); err != nil {
		return nil, err
	}

	return &response.CLIRefreshResp{
		AccessToken:      accessToken,
		AccessExpiresAt:  accessRecord.ExpiresAt,
		RefreshToken:     refreshToken,
		RefreshExpiresAt: refreshRecord.ExpiresAt,
		TokenType:        "Bearer",
		AgentID:          agent.ID,
	}, nil
}

func (s *AnalyticsService) issueDeviceTokenPairLocked(identity AuthIdentity, agent *Agent, parentTokenID string, rotationCounter int, now time.Time) (string, *AccessToken, string, *AccessToken, error) {
	if agent == nil {
		return "", nil, "", nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown agent_id"))
	}

	deviceLabel := firstNonEmpty(strings.TrimSpace(agent.DeviceName), agent.ID)
	refreshToken, refreshRecord, err := s.AnalyticsStore.issueAccessTokenWithOptionsLocked(issueAccessTokenOptions{
		Kind:            TokenKindDeviceRefresh,
		OrgID:           identity.OrgID,
		UserID:          identity.UserID,
		AgentID:         agent.ID,
		Label:           "Device refresh for " + deviceLabel,
		TTL:             defaultDeviceRefreshTokenTTL,
		ParentTokenID:   parentTokenID,
		RotationCounter: rotationCounter,
	}, now)
	if err != nil {
		return "", nil, "", nil, err
	}

	accessToken, accessRecord, err := s.AnalyticsStore.issueAccessTokenWithOptionsLocked(issueAccessTokenOptions{
		Kind:            TokenKindDeviceAccess,
		OrgID:           identity.OrgID,
		UserID:          identity.UserID,
		AgentID:         agent.ID,
		Label:           "Device access for " + deviceLabel,
		TTL:             defaultDeviceAccessTokenTTL,
		ParentTokenID:   refreshRecord.ID,
		RotationCounter: rotationCounter,
	}, now)
	if err != nil {
		return "", nil, "", nil, err
	}

	return accessToken, accessRecord, refreshToken, refreshRecord, nil
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

func (s *AnalyticsService) requireCLITokenManagerIdentity(ctx context.Context) (AuthIdentity, error) {
	identity, ok := AuthIdentityFromContext(ctx)
	if ok && identity.TokenKind == TokenKindStatic && s.Config != nil && s.Config.AllowsDemoUser() && s.Config.AllowsStaticToken() {
		return AuthIdentity{
			TokenKind: TokenKindStatic,
			OrgID:     defaultDemoOrgID,
			UserID:    defaultDemoUserID,
			UserRole:  userRoleAdmin,
		}, nil
	}
	return s.requireUserIdentity(ctx, TokenKindWebSession)
}

func (s *AnalyticsService) requireDeviceAccessIdentity(ctx context.Context) (AuthIdentity, error) {
	return s.requireUserIdentity(ctx, TokenKindDeviceAccess)
}

func (s *AnalyticsService) ensureAgentBinding(identity AuthIdentity, agentID string) error {
	agentID = strings.TrimSpace(agentID)
	if strings.TrimSpace(identity.AgentID) == "" || agentID == "" || identity.AgentID != agentID {
		return ecode.Forbidden(ErrCodeAgentBindingMismatch, "device token cannot access a different agent")
	}
	return nil
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

func (s *AnalyticsService) authorizeProjectAgentBinding(ctx context.Context, project *Project) error {
	if err := s.authorizeProject(ctx, project); err != nil {
		return err
	}
	identity, ok := AuthIdentityFromContext(ctx)
	if !ok || identity.TokenKind != TokenKindDeviceAccess {
		return nil
	}
	return s.ensureAgentBinding(identity, project.AgentID)
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

	audit := s.appendAuditLocked(ctx, req.OrgID, auditEventInput{
		Type:         "device.registered",
		Message:      "local cli device registered",
		TargetUserID: req.UserID,
		Result:       "success",
		Reason:       "collector registered a local cli device",
	})
	if err := s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
		if err := s.AnalyticsStore.persistOrganizationLocked(tx, req.OrgID); err != nil {
			return err
		}
		if err := s.AnalyticsStore.persistUserLocked(tx, req.UserID); err != nil {
			return err
		}
		if err := s.AnalyticsStore.persistAgentLocked(tx, deviceID); err != nil {
			return err
		}
		return s.AnalyticsStore.persistAuditLocked(tx, audit.ID)
	}); err != nil {
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
	identity, err := s.requireDeviceAccessIdentity(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.ensureAgentBinding(identity, req.AgentID); err != nil {
		return nil, err
	}

	now := time.Now().UTC()

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	req.AgentID = identity.AgentID

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
	if agent.UserID != identity.UserID {
		return nil, ecode.Forbidden(ErrCodeAgentBindingMismatch, "device token cannot access a different agent")
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

	audit := s.appendAuditLocked(ctx, req.OrgID, auditEventInput{
		ProjectID: projectID,
		Type:      "workspace.connected",
		Message:   "shared workspace connected to aiops",
		Result:    "success",
		Reason:    "collector connected the shared workspace",
	})
	if err := s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
		if err := s.AnalyticsStore.persistProjectLocked(tx, projectID); err != nil {
			return err
		}
		return s.AnalyticsStore.persistAuditLocked(tx, audit.ID)
	}); err != nil {
		return nil, err
	}

	return &response.ProjectRegistrationResp{
		ProjectID:   projectID,
		Status:      "connected",
		ConnectedAt: now,
	}, nil
}

func (s *AnalyticsService) UploadConfigSnapshot(ctx context.Context, req *request.ConfigSnapshotReq) (*response.ConfigSnapshotResp, error) {
	if _, err := s.requireDeviceAccessIdentity(ctx); err != nil {
		return nil, err
	}

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	project, err := s.resolveProjectLocked(ctx, req.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeProjectAgentBinding(ctx, project); err != nil {
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

	audit := s.appendAuditLocked(ctx, project.OrgID, auditEventInput{
		ProjectID: req.ProjectID,
		Type:      "config.snapshot",
		Message:   "config snapshot uploaded from local collector",
		Result:    "success",
		Reason:    "collector uploaded a config snapshot",
	})
	if err := s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
		if err := s.AnalyticsStore.persistConfigSnapshotLocked(tx, req.ProjectID, snapshot.ID); err != nil {
			return err
		}
		if err := s.AnalyticsStore.persistProjectLocked(tx, project.ID); err != nil {
			return err
		}
		return s.AnalyticsStore.persistAuditLocked(tx, audit.ID)
	}); err != nil {
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
	if _, err := s.requireDeviceAccessIdentity(ctx); err != nil {
		return nil, err
	}

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	project, err := s.resolveProjectLocked(ctx, req.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeProjectAgentBinding(ctx, project); err != nil {
		return nil, err
	}
	req.ProjectID = project.ID
	previousSessionCount := len(s.AnalyticsStore.sessionSummaries[project.ID])

	resp, _, err := s.ingestSessionSummaryLocked(project, req)
	if err != nil {
		return nil, err
	}

	reports, refreshJobs := s.prepareReportRefreshLocked(project, resp.SessionID, previousSessionCount)
	resp.ReportCount = len(reports)
	resp.LatestReportIDs = reportIDs(reports)
	resp.ResearchStatus = cloneReportResearchStatusResp(s.AnalyticsStore.reportResearch[project.ID])

	audit := s.appendAuditLocked(ctx, project.OrgID, auditEventInput{
		ProjectID: req.ProjectID,
		Type:      "session.ingested",
		Message:   "session summary uploaded from local collector",
		Result:    "success",
		Reason:    "collector uploaded a session summary",
	})
	if err := s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
		if err := s.AnalyticsStore.persistSessionSummaryLocked(tx, project.ID, resp.SessionID); err != nil {
			return err
		}
		if err := s.AnalyticsStore.persistProjectLocked(tx, project.ID); err != nil {
			return err
		}
		if err := s.AnalyticsStore.persistReportResearchLocked(tx, project.ID); err != nil {
			return err
		}
		return s.AnalyticsStore.persistAuditLocked(tx, audit.ID)
	}); err != nil {
		return nil, err
	}

	s.enqueueReportRefresh(refreshJobs)
	return resp, nil
}

func (s *AnalyticsService) UploadSessionSummaries(ctx context.Context, req *request.SessionSummaryBatchReq) (*response.SessionBatchIngestResp, error) {
	if _, err := s.requireDeviceAccessIdentity(ctx); err != nil {
		return nil, err
	}

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	project, err := s.resolveProjectLocked(ctx, req.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeProjectAgentBinding(ctx, project); err != nil {
		return nil, err
	}
	req.ProjectID = project.ID

	resp := &response.SessionBatchIngestResp{
		SchemaVersion: reportAPISchemaVersion,
		ProjectID:     project.ID,
		Accepted:      len(req.Sessions),
		Items:         make([]response.SessionBatchIngestItemResp, 0, len(req.Sessions)),
	}

	previousSessionCount := len(s.AnalyticsStore.sessionSummaries[project.ID])
	lastSuccessfulSessionID := ""
	successfulSessionIDs := make([]string, 0, len(req.Sessions))
	successCount := 0
	for _, itemReq := range req.Sessions {
		itemReq.ProjectID = project.ID
		itemResp, updated, err := s.ingestSessionSummaryLocked(project, &itemReq)
		if err != nil {
			if ecode.HttpCode(err) >= http.StatusInternalServerError {
				return nil, err
			}
			resp.Failed++
			resp.Items = append(resp.Items, newSessionBatchFailureItem(itemReq.SessionID, err))
			continue
		}

		lastSuccessfulSessionID = itemResp.SessionID
		successfulSessionIDs = append(successfulSessionIDs, itemResp.SessionID)
		successCount++
		if updated {
			resp.Updated++
		} else {
			resp.Uploaded++
		}

		recordedAt := itemResp.RecordedAt
		status := "uploaded"
		if updated {
			status = "updated"
		}
		resp.Items = append(resp.Items, response.SessionBatchIngestItemResp{
			SessionID:  itemResp.SessionID,
			ProjectID:  itemResp.ProjectID,
			Status:     status,
			RecordedAt: &recordedAt,
		})
	}

	reports := s.currentReportsLocked(project.ID)
	var refreshJobs []*reportRefreshJob
	if successCount > 0 {
		reports, refreshJobs = s.prepareReportRefreshLocked(project, lastSuccessfulSessionID, previousSessionCount)
		audit := s.appendAuditLocked(ctx, project.OrgID, auditEventInput{
			ProjectID: req.ProjectID,
			Type:      "session.batch_ingested",
			Message:   fmt.Sprintf("%d session summaries uploaded from local collector", successCount),
			Result:    "success",
			Reason:    "collector uploaded session summaries in batch",
		})
		if err := s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
			if err := s.AnalyticsStore.persistSessionSummariesForProjectLocked(tx, project.ID, successfulSessionIDs); err != nil {
				return err
			}
			if err := s.AnalyticsStore.persistProjectLocked(tx, project.ID); err != nil {
				return err
			}
			if err := s.AnalyticsStore.persistReportResearchLocked(tx, project.ID); err != nil {
				return err
			}
			return s.AnalyticsStore.persistAuditLocked(tx, audit.ID)
		}); err != nil {
			return nil, err
		}
	}

	resp.ReportCount = len(reports)
	resp.LatestReportIDs = reportIDs(reports)
	resp.ResearchStatus = cloneReportResearchStatusResp(s.AnalyticsStore.reportResearch[project.ID])

	s.enqueueReportRefresh(refreshJobs)
	return resp, nil
}

func (s *AnalyticsService) CreateSessionImportJob(ctx context.Context, req *request.SessionImportJobCreateReq) (*response.SessionImportJobResp, error) {
	identity, err := s.requireDeviceAccessIdentity(ctx)
	if err != nil {
		return nil, err
	}

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()
	if err := s.cleanupExpiredSessionImportJobsLocked(time.Now().UTC()); err != nil {
		return nil, err
	}

	project, err := s.resolveProjectLocked(ctx, req.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeProjectAgentBinding(ctx, project); err != nil {
		return nil, err
	}
	totalSessions := maxInt(req.TotalSessions, 0)
	if existing := s.findActiveSessionImportJobLocked(project.ID, identity.AgentID); existing != nil {
		s.touchSessionImportJob(existing, time.Now().UTC())
		if totalSessions > existing.TotalSessions {
			existing.TotalSessions = totalSessions
		}
		if err := s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
			return s.AnalyticsStore.persistSessionImportJobLocked(tx, existing.ID)
		}); err != nil {
			return nil, err
		}
		logSessionImportJobEvent(slog.LevelInfo, "reused", existing)
		return s.toSessionImportJobRespLocked(existing, true), nil
	}

	now := time.Now().UTC()
	job := &SessionImportJob{
		ID:            s.AnalyticsStore.nextID("import"),
		ProjectID:     project.ID,
		OrgID:         project.OrgID,
		AgentID:       identity.AgentID,
		Status:        sessionImportJobStatusReceiving,
		TotalSessions: totalSessions,
		CreatedAt:     now,
		UpdatedAt:     now,
		Failures:      make([]SessionImportJobFailure, 0),
	}
	s.AnalyticsStore.sessionImportJobs[job.ID] = job
	audit := s.appendAuditLocked(ctx, project.OrgID, auditEventInput{
		ProjectID: project.ID,
		Type:      "session_import_job.created",
		Message:   "session import job created by local collector",
		Result:    "success",
		Reason:    "collector started an async session import job",
	})
	if err := s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
		if err := s.AnalyticsStore.persistSessionImportJobLocked(tx, job.ID); err != nil {
			return err
		}
		return s.AnalyticsStore.persistAuditLocked(tx, audit.ID)
	}); err != nil {
		return nil, err
	}
	logSessionImportJobEvent(slog.LevelInfo, "created", job)
	return s.toSessionImportJobRespLocked(job, false), nil
}

func (s *AnalyticsService) AppendSessionImportJobChunk(ctx context.Context, jobID string, req *request.SessionImportJobChunkReq) (*response.SessionImportJobResp, error) {
	if _, err := s.requireDeviceAccessIdentity(ctx); err != nil {
		return nil, err
	}

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()
	if err := s.cleanupExpiredSessionImportJobsLocked(time.Now().UTC()); err != nil {
		return nil, err
	}

	job, project, err := s.resolveSessionImportJobLocked(ctx, jobID, true)
	if err != nil {
		return nil, err
	}
	if job.Status != sessionImportJobStatusReceiving {
		return nil, ecode.Forbidden(1012, "session import job is not accepting chunks")
	}

	for _, item := range req.Sessions {
		prepared := s.sessionImportJobSessionFromRequest(item)
		s.upsertSessionImportJobSession(job, prepared)
	}
	s.touchSessionImportJob(job, time.Now().UTC())
	if err := s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
		return s.AnalyticsStore.persistSessionImportJobLocked(tx, job.ID)
	}); err != nil {
		return nil, err
	}
	logSessionImportJobEvent(slog.LevelInfo, "chunk_appended", job, "chunk_sessions", len(req.Sessions))
	_ = project
	return s.toSessionImportJobRespLocked(job, false), nil
}

func (s *AnalyticsService) CompleteSessionImportJob(ctx context.Context, jobID string, _ *request.SessionImportJobCompleteReq) (*response.SessionImportJobResp, error) {
	if _, err := s.requireDeviceAccessIdentity(ctx); err != nil {
		return nil, err
	}

	s.AnalyticsStore.mu.Lock()
	if err := s.cleanupExpiredSessionImportJobsLocked(time.Now().UTC()); err != nil {
		s.AnalyticsStore.mu.Unlock()
		return nil, err
	}
	job, project, err := s.resolveSessionImportJobLocked(ctx, jobID, true)
	if err != nil {
		s.AnalyticsStore.mu.Unlock()
		return nil, err
	}
	if job.Status != sessionImportJobStatusReceiving {
		resp := s.toSessionImportJobRespLocked(job, false)
		s.AnalyticsStore.mu.Unlock()
		return resp, nil
	}
	if job.ReceivedSessions == 0 {
		s.AnalyticsStore.mu.Unlock()
		return nil, ecode.NewInvalidParamsErr("session import job has no uploaded sessions")
	}

	job.Status = sessionImportJobStatusQueued
	s.touchSessionImportJob(job, time.Now().UTC())
	if err := s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
		return s.AnalyticsStore.persistSessionImportJobLocked(tx, job.ID)
	}); err != nil {
		s.AnalyticsStore.mu.Unlock()
		return nil, err
	}
	resp := s.toSessionImportJobRespLocked(job, false)
	logSessionImportJobEvent(slog.LevelInfo, "queued", job)
	s.AnalyticsStore.mu.Unlock()

	s.enqueueSessionImportJob(job.ID)
	s.appendSessionImportAuditAsync(project.OrgID, project.ID, "session_import_job.queued", "session import job queued", "collector finished uploading async import chunks")
	return resp, nil
}

func (s *AnalyticsService) CancelSessionImportJob(ctx context.Context, jobID string, _ *request.SessionImportJobCancelReq) (*response.SessionImportJobResp, error) {
	if _, err := s.requireDeviceAccessIdentity(ctx); err != nil {
		return nil, err
	}

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()
	if err := s.cleanupExpiredSessionImportJobsLocked(time.Now().UTC()); err != nil {
		return nil, err
	}

	job, project, err := s.resolveSessionImportJobLocked(ctx, jobID, true)
	if err != nil {
		return nil, err
	}
	if !isActiveSessionImportJobStatus(job.Status) {
		return s.toSessionImportJobRespLocked(job, false), nil
	}

	now := time.Now().UTC()
	job.Status = sessionImportJobStatusCanceled
	job.LastError = "session import job canceled"
	job.CompletedAt = cloneTime(&now)
	job.Sessions = nil
	s.touchSessionImportJob(job, now)
	audit := s.appendAuditLocked(ctx, project.OrgID, auditEventInput{
		ProjectID: project.ID,
		Type:      "session_import_job.canceled",
		Message:   "session import job canceled",
		Result:    "success",
		Reason:    "collector canceled the async session import job",
	})
	if err := s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
		if err := s.AnalyticsStore.persistSessionImportJobLocked(tx, job.ID); err != nil {
			return err
		}
		return s.AnalyticsStore.persistAuditLocked(tx, audit.ID)
	}); err != nil {
		return nil, err
	}
	logSessionImportJobEvent(slog.LevelWarn, "canceled", job, "cancel_source", "collector")
	return s.toSessionImportJobRespLocked(job, false), nil
}

func (s *AnalyticsService) GetSessionImportJob(ctx context.Context, jobID string) (*response.SessionImportJobResp, error) {
	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()
	if err := s.cleanupExpiredSessionImportJobsLocked(time.Now().UTC()); err != nil {
		return nil, err
	}

	jobID = strings.TrimSpace(jobID)
	job, ok := s.AnalyticsStore.sessionImportJobs[jobID]
	if !ok || job == nil {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown session_import_job"))
	}
	project, ok := s.AnalyticsStore.projects[job.ProjectID]
	if !ok || project == nil {
		return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown workspace"))
	}
	if err := s.authorizeProject(ctx, project); err != nil {
		return nil, err
	}
	return s.toSessionImportJobRespLocked(job, false), nil
}

func (s *AnalyticsService) ListSessionImportJobs(ctx context.Context, req *request.SessionImportJobListReq) (*response.SessionImportJobListResp, error) {
	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()
	if err := s.cleanupExpiredSessionImportJobsLocked(time.Now().UTC()); err != nil {
		return nil, err
	}

	project, err := s.resolveProjectLocked(ctx, req.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeProject(ctx, project); err != nil {
		return nil, err
	}
	req.ProjectID = project.ID

	statusFilter := strings.TrimSpace(req.Status)
	agentFilter := strings.TrimSpace(req.AgentID)
	cursor := strings.TrimSpace(req.Cursor)
	limit := req.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	items := make([]*SessionImportJob, 0)
	for _, job := range s.AnalyticsStore.sessionImportJobs {
		if job == nil || strings.TrimSpace(job.ProjectID) != project.ID {
			continue
		}
		if statusFilter != "" && strings.TrimSpace(job.Status) != statusFilter {
			continue
		}
		if agentFilter != "" && strings.TrimSpace(job.AgentID) != agentFilter {
			continue
		}
		if req.FailedOnly && !isFailedSessionImportJob(job) {
			continue
		}
		items = append(items, job)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	if cursor != "" {
		cursorIdx := -1
		for idx, item := range items {
			if strings.TrimSpace(item.ID) == cursor {
				cursorIdx = idx
				break
			}
		}
		if cursorIdx < 0 {
			return nil, ecode.NewInvalidParamsErr("unknown session_import_job cursor")
		}
		items = items[cursorIdx+1:]
	}

	nextCursor := ""
	if len(items) > limit {
		nextCursor = strings.TrimSpace(items[limit-1].ID)
		items = items[:limit]
	}

	respItems := make([]response.SessionImportJobResp, 0, len(items))
	for _, item := range items {
		if converted := s.toSessionImportJobRespLocked(item, false); converted != nil {
			respItems = append(respItems, *converted)
		}
	}
	return &response.SessionImportJobListResp{
		Items:      respItems,
		NextCursor: nextCursor,
	}, nil
}

func (s *AnalyticsService) findActiveSessionImportJobLocked(projectID, agentID string) *SessionImportJob {
	projectID = strings.TrimSpace(projectID)
	agentID = strings.TrimSpace(agentID)
	for _, job := range s.AnalyticsStore.sessionImportJobs {
		if job == nil {
			continue
		}
		if strings.TrimSpace(job.ProjectID) != projectID {
			continue
		}
		if agentID != "" && strings.TrimSpace(job.AgentID) != agentID {
			continue
		}
		switch strings.TrimSpace(job.Status) {
		case sessionImportJobStatusReceiving, sessionImportJobStatusQueued, sessionImportJobStatusRunning:
			return job
		}
	}
	return nil
}

func (s *AnalyticsService) findNewestActiveSessionImportJobForProjectLocked(projectID string) *SessionImportJob {
	projectID = strings.TrimSpace(projectID)
	var latest *SessionImportJob
	for _, job := range s.AnalyticsStore.sessionImportJobs {
		if job == nil || strings.TrimSpace(job.ProjectID) != projectID {
			continue
		}
		if !isActiveSessionImportJobStatus(job.Status) {
			continue
		}
		if isSessionImportJobNewer(job, latest) {
			latest = job
		}
	}
	return latest
}

func isActiveSessionImportJobStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case sessionImportJobStatusReceiving, sessionImportJobStatusQueued, sessionImportJobStatusRunning:
		return true
	default:
		return false
	}
}

func isSessionImportJobNewer(candidate, current *SessionImportJob) bool {
	if candidate == nil {
		return false
	}
	if current == nil {
		return true
	}
	if candidate.CreatedAt.Equal(current.CreatedAt) {
		return candidate.ID > current.ID
	}
	return candidate.CreatedAt.After(current.CreatedAt)
}

func isFailedSessionImportJob(job *SessionImportJob) bool {
	if job == nil {
		return false
	}
	switch strings.TrimSpace(job.Status) {
	case sessionImportJobStatusFailed, sessionImportJobStatusPartial:
		return true
	default:
		return false
	}
}

func (s *AnalyticsService) upsertSessionImportJobSession(job *SessionImportJob, item SessionImportJobSession) {
	if job == nil {
		return
	}
	sessionID := strings.TrimSpace(item.SessionID)
	if sessionID != "" {
		for idx := range job.Sessions {
			if strings.TrimSpace(job.Sessions[idx].SessionID) == sessionID {
				job.Sessions[idx] = item
				job.ReceivedSessions = len(job.Sessions)
				s.touchSessionImportJob(job, time.Now().UTC())
				return
			}
		}
	}
	job.Sessions = append(job.Sessions, item)
	job.ReceivedSessions = len(job.Sessions)
	s.touchSessionImportJob(job, time.Now().UTC())
}

func (s *AnalyticsService) resolveSessionImportJobLocked(ctx context.Context, jobID string, requireAgentBinding bool) (*SessionImportJob, *Project, error) {
	jobID = strings.TrimSpace(jobID)
	job, ok := s.AnalyticsStore.sessionImportJobs[jobID]
	if !ok || job == nil {
		return nil, nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown session_import_job"))
	}
	project, ok := s.AnalyticsStore.projects[job.ProjectID]
	if !ok || project == nil {
		return nil, nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown workspace"))
	}
	if requireAgentBinding {
		if err := s.authorizeProjectAgentBinding(ctx, project); err != nil {
			return nil, nil, err
		}
	} else if err := s.authorizeProject(ctx, project); err != nil {
		return nil, nil, err
	}
	return job, project, nil
}

func (s *AnalyticsService) touchSessionImportJob(job *SessionImportJob, now time.Time) {
	if job == nil {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	job.UpdatedAt = now
}

func logSessionImportJobEvent(level slog.Level, event string, job *SessionImportJob, attrs ...any) {
	if job == nil {
		return
	}
	args := []any{
		"event", strings.TrimSpace(event),
		"job_id", strings.TrimSpace(job.ID),
		"org_id", strings.TrimSpace(job.OrgID),
		"project_id", strings.TrimSpace(job.ProjectID),
		"agent_id", strings.TrimSpace(job.AgentID),
		"status", strings.TrimSpace(job.Status),
		"total_sessions", job.TotalSessions,
		"received_sessions", job.ReceivedSessions,
		"processed_sessions", job.ProcessedSessions,
		"uploaded_sessions", job.UploadedSessions,
		"updated_sessions", job.UpdatedSessions,
		"failed_sessions", job.FailedSessions,
		"failure_count", len(job.Failures),
	}
	if sessionID := strings.TrimSpace(job.LastSessionID); sessionID != "" {
		args = append(args, "last_session_id", sessionID)
	}
	if lastError := strings.TrimSpace(job.LastError); lastError != "" {
		args = append(args, "last_error", lastError)
	}
	args = append(args, attrs...)
	slog.Log(context.Background(), level, "session import job", args...)
}

func (s *AnalyticsService) cleanupExpiredSessionImportJobsLocked(now time.Time) error {
	upsertIDs := make([]string, 0)
	deleteIDs := make([]string, 0)
	for id, job := range s.AnalyticsStore.sessionImportJobs {
		if job == nil {
			delete(s.AnalyticsStore.sessionImportJobs, id)
			deleteIDs = append(deleteIDs, id)
			continue
		}
		lastActivity := job.UpdatedAt
		if lastActivity.IsZero() {
			lastActivity = job.CreatedAt
		}
		if isActiveSessionImportJobStatus(job.Status) && now.Sub(lastActivity) >= sessionImportJobActiveTTL {
			job.Status = sessionImportJobStatusCanceled
			job.LastError = "session import job expired before completion"
			job.CompletedAt = cloneTime(&now)
			job.Sessions = nil
			s.touchSessionImportJob(job, now)
			logSessionImportJobEvent(slog.LevelWarn, "expired", job, "ttl_hours", int(sessionImportJobActiveTTL/time.Hour))
			upsertIDs = append(upsertIDs, id)
			continue
		}
		if job.CompletedAt != nil && now.Sub(job.CompletedAt.UTC()) >= sessionImportJobRetentionTTL {
			logSessionImportJobEvent(slog.LevelInfo, "deleted", job, "retention_ttl_hours", int(sessionImportJobRetentionTTL/time.Hour))
			delete(s.AnalyticsStore.sessionImportJobs, id)
			deleteIDs = append(deleteIDs, id)
		}
	}
	if len(upsertIDs) == 0 && len(deleteIDs) == 0 {
		return nil
	}
	return s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
		for _, id := range upsertIDs {
			if err := s.AnalyticsStore.persistSessionImportJobLocked(tx, id); err != nil {
				return err
			}
		}
		for _, id := range deleteIDs {
			if err := s.AnalyticsStore.deleteSessionImportJobLocked(tx, id); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *AnalyticsService) buildSessionImportJobMetricsLocked(orgID string) *response.SessionImportJobMetricsResp {
	return s.buildSessionImportJobMetricsFilteredLocked(orgID, "")
}

func (s *AnalyticsService) buildSessionImportJobMetricsFilteredLocked(orgID, projectID string) *response.SessionImportJobMetricsResp {
	orgID = strings.TrimSpace(orgID)
	if orgID == "" {
		return nil
	}
	projectID = strings.TrimSpace(projectID)

	metrics := &response.SessionImportJobMetricsResp{}
	var (
		durationSamples           int
		durationTotalMS           int
		throughputDurationMinutes float64
		throughputProcessed       int
		lastCompletedAt           *time.Time
	)
	for _, job := range s.AnalyticsStore.sessionImportJobs {
		if !sessionImportJobMatchesScope(job, orgID, projectID) {
			continue
		}
		metrics.CreatedJobs++
		metrics.ProcessedSessions += job.ProcessedSessions
		metrics.UploadedSessions += job.UploadedSessions + job.UpdatedSessions
		metrics.FailedSessions += job.FailedSessions

		switch strings.TrimSpace(job.Status) {
		case sessionImportJobStatusReceiving:
			metrics.ReceivingJobs++
		case sessionImportJobStatusQueued:
			metrics.QueuedJobs++
		case sessionImportJobStatusRunning:
			metrics.RunningJobs++
		case sessionImportJobStatusSucceeded:
			metrics.SucceededJobs++
		case sessionImportJobStatusPartial:
			metrics.PartiallyFailedJobs++
		case sessionImportJobStatusFailed:
			metrics.FailedJobs++
		case sessionImportJobStatusCanceled:
			metrics.CanceledJobs++
		}

		if job.StartedAt != nil && job.CompletedAt != nil && !job.CompletedAt.Before(*job.StartedAt) {
			duration := job.CompletedAt.Sub(job.StartedAt.UTC())
			durationMS := int(duration / time.Millisecond)
			if durationMS > 0 {
				durationSamples++
				durationTotalMS += durationMS
				throughputDurationMinutes += duration.Minutes()
				throughputProcessed += job.ProcessedSessions
			}
		}
		if job.CompletedAt != nil && (lastCompletedAt == nil || job.CompletedAt.After(*lastCompletedAt)) {
			completedAt := job.CompletedAt.UTC()
			lastCompletedAt = &completedAt
		}
	}
	if metrics.CreatedJobs == 0 {
		return nil
	}
	if metrics.ProcessedSessions > 0 {
		metrics.FailureRate = safeDiv(float64(metrics.FailedSessions), float64(metrics.ProcessedSessions))
	}
	if durationSamples > 0 {
		metrics.AvgDurationMS = durationTotalMS / durationSamples
		metrics.ThroughputPerMinute = safeDiv(float64(throughputProcessed), throughputDurationMinutes)
	}
	metrics.LastCompletedAt = lastCompletedAt
	return metrics
}

func sessionImportJobMatchesScope(job *SessionImportJob, orgID, projectID string) bool {
	if job == nil || strings.TrimSpace(job.OrgID) != strings.TrimSpace(orgID) {
		return false
	}
	if projectID != "" && strings.TrimSpace(job.ProjectID) != strings.TrimSpace(projectID) {
		return false
	}
	return true
}

func sessionImportJobActivityAt(job *SessionImportJob) time.Time {
	if job == nil {
		return time.Time{}
	}
	if job.CompletedAt != nil && !job.CompletedAt.IsZero() {
		return job.CompletedAt.UTC()
	}
	if !job.UpdatedAt.IsZero() {
		return job.UpdatedAt.UTC()
	}
	if job.StartedAt != nil && !job.StartedAt.IsZero() {
		return job.StartedAt.UTC()
	}
	return job.CreatedAt.UTC()
}

func (s *AnalyticsService) listAdminSessionImportJobsLocked(orgID, projectID string, limit int, include func(*SessionImportJob) bool) []response.SessionImportJobResp {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	items := make([]*SessionImportJob, 0)
	for _, job := range s.AnalyticsStore.sessionImportJobs {
		if !sessionImportJobMatchesScope(job, orgID, projectID) {
			continue
		}
		if include != nil && !include(job) {
			continue
		}
		items = append(items, job)
	}
	sort.Slice(items, func(i, j int) bool {
		left := sessionImportJobActivityAt(items[i])
		right := sessionImportJobActivityAt(items[j])
		if left.Equal(right) {
			return strings.TrimSpace(items[i].ID) > strings.TrimSpace(items[j].ID)
		}
		return left.After(right)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	resp := make([]response.SessionImportJobResp, 0, len(items))
	for _, item := range items {
		if converted := s.toSessionImportJobRespLocked(item, false); converted != nil {
			resp = append(resp, *converted)
		}
	}
	return resp
}

func (s *AnalyticsService) enqueueSessionImportJob(jobID string) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return
	}

	s.sessionImportMu.Lock()
	if s.sessionImportLive[jobID] {
		s.sessionImportMu.Unlock()
		return
	}
	s.sessionImportLive[jobID] = true
	s.sessionImportMu.Unlock()

	go func(id string) {
		defer func() {
			s.sessionImportMu.Lock()
			delete(s.sessionImportLive, id)
			s.sessionImportMu.Unlock()
		}()
		s.runSessionImportJob(id)
	}(jobID)
}

func (s *AnalyticsService) runSessionImportJob(jobID string) {
	s.AnalyticsStore.mu.Lock()
	job, ok := s.AnalyticsStore.sessionImportJobs[jobID]
	if !ok || job == nil {
		s.AnalyticsStore.mu.Unlock()
		return
	}
	if job.Status != sessionImportJobStatusQueued && job.Status != sessionImportJobStatusRunning {
		s.AnalyticsStore.mu.Unlock()
		return
	}
	project, ok := s.AnalyticsStore.projects[job.ProjectID]
	if !ok || project == nil {
		job.Status = sessionImportJobStatusFailed
		job.LastError = "workspace no longer exists"
		completedAt := time.Now().UTC()
		job.CompletedAt = cloneTime(&completedAt)
		s.touchSessionImportJob(job, completedAt)
		_ = s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
			return s.AnalyticsStore.persistSessionImportJobLocked(tx, job.ID)
		})
		s.AnalyticsStore.mu.Unlock()
		return
	}

	startedAt := time.Now().UTC()
	job.Status = sessionImportJobStatusRunning
	job.StartedAt = cloneTime(&startedAt)
	s.touchSessionImportJob(job, startedAt)
	sessions := append([]SessionImportJobSession(nil), job.Sessions...)
	projectID := project.ID
	previousSessionCount := len(s.AnalyticsStore.sessionSummaries[project.ID])
	if err := s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
		return s.AnalyticsStore.persistSessionImportJobLocked(tx, job.ID)
	}); err != nil {
		s.AnalyticsStore.mu.Unlock()
		return
	}
	logSessionImportJobEvent(slog.LevelInfo, "started", job)
	s.AnalyticsStore.mu.Unlock()

	lastSuccessfulSessionID := ""
	successCount := 0
	for _, item := range sessions {
		s.AnalyticsStore.mu.Lock()
		job, ok := s.AnalyticsStore.sessionImportJobs[jobID]
		if !ok || job == nil {
			s.AnalyticsStore.mu.Unlock()
			return
		}
		if strings.TrimSpace(job.Status) == sessionImportJobStatusCanceled {
			job.Sessions = nil
			s.touchSessionImportJob(job, time.Now().UTC())
			_ = s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
				return s.AnalyticsStore.persistSessionImportJobLocked(tx, job.ID)
			})
			s.AnalyticsStore.mu.Unlock()
			return
		}
		project, ok := s.AnalyticsStore.projects[projectID]
		if !ok || project == nil {
			job.Status = sessionImportJobStatusFailed
			job.LastError = "workspace no longer exists"
			completedAt := time.Now().UTC()
			job.CompletedAt = cloneTime(&completedAt)
			s.touchSessionImportJob(job, completedAt)
			logSessionImportJobEvent(slog.LevelError, "failed", job, "reason", "workspace_missing")
			_ = s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
				return s.AnalyticsStore.persistSessionImportJobLocked(tx, job.ID)
			})
			s.AnalyticsStore.mu.Unlock()
			return
		}
		req := s.sessionImportJobSessionToRequest(project.ID, item)
		resp, updated, err := s.ingestSessionSummaryLocked(project, &req)
		if err != nil {
			if ecode.HttpCode(err) >= http.StatusInternalServerError {
				job.Status = sessionImportJobStatusFailed
				job.LastError = ecode.FromError(err).Message
				completedAt := time.Now().UTC()
				job.CompletedAt = cloneTime(&completedAt)
				s.touchSessionImportJob(job, completedAt)
				logSessionImportJobEvent(slog.LevelError, "failed", job, "reason", "internal_ingest_error", "http_status", ecode.HttpCode(err), "api_error_code", ecode.Code(err))
				_ = s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
					return s.AnalyticsStore.persistSessionImportJobLocked(tx, job.ID)
				})
				s.AnalyticsStore.mu.Unlock()
				return
			}
			job.FailedSessions++
			job.ProcessedSessions++
			job.Failures = append(job.Failures, SessionImportJobFailure{
				SessionID:    strings.TrimSpace(req.SessionID),
				Error:        ecode.FromError(err).Message,
				HTTPStatus:   ecode.HttpCode(err),
				APIErrorCode: ecode.Code(err),
			})
			s.touchSessionImportJob(job, time.Now().UTC())
			logSessionImportJobEvent(slog.LevelWarn, "session_failed", job, "session_id", strings.TrimSpace(req.SessionID), "http_status", ecode.HttpCode(err), "api_error_code", ecode.Code(err))
			if err := s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
				return s.AnalyticsStore.persistSessionImportJobLocked(tx, job.ID)
			}); err != nil {
				s.AnalyticsStore.mu.Unlock()
				return
			}
			s.AnalyticsStore.mu.Unlock()
			continue
		}

		lastSuccessfulSessionID = resp.SessionID
		successCount++
		job.ProcessedSessions++
		job.LastSessionID = resp.SessionID
		if updated {
			job.UpdatedSessions++
		} else {
			job.UploadedSessions++
		}
		s.touchSessionImportJob(job, time.Now().UTC())
		if err := s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
			if err := s.AnalyticsStore.persistSessionSummaryLocked(tx, project.ID, resp.SessionID); err != nil {
				return err
			}
			if err := s.AnalyticsStore.persistProjectLocked(tx, project.ID); err != nil {
				return err
			}
			return s.AnalyticsStore.persistSessionImportJobLocked(tx, job.ID)
		}); err != nil {
			s.AnalyticsStore.mu.Unlock()
			return
		}
		s.AnalyticsStore.mu.Unlock()
	}

	s.AnalyticsStore.mu.Lock()
	job, ok = s.AnalyticsStore.sessionImportJobs[jobID]
	if !ok || job == nil {
		s.AnalyticsStore.mu.Unlock()
		return
	}
	if strings.TrimSpace(job.Status) == sessionImportJobStatusCanceled {
		job.Sessions = nil
		s.touchSessionImportJob(job, time.Now().UTC())
		logSessionImportJobEvent(slog.LevelWarn, "canceled", job, "cancel_source", "runtime")
		_ = s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
			return s.AnalyticsStore.persistSessionImportJobLocked(tx, job.ID)
		})
		s.AnalyticsStore.mu.Unlock()
		return
	}
	project, ok = s.AnalyticsStore.projects[projectID]
	if !ok || project == nil {
		job.Status = sessionImportJobStatusFailed
		job.LastError = "workspace no longer exists"
		completedAt := time.Now().UTC()
		job.CompletedAt = cloneTime(&completedAt)
		s.touchSessionImportJob(job, completedAt)
		logSessionImportJobEvent(slog.LevelError, "failed", job, "reason", "workspace_missing")
		_ = s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
			return s.AnalyticsStore.persistSessionImportJobLocked(tx, job.ID)
		})
		s.AnalyticsStore.mu.Unlock()
		return
	}

	reports := s.currentReportsLocked(project.ID)
	var refreshJobs []*reportRefreshJob
	var audit *AuditEvent
	if successCount > 0 {
		reports, refreshJobs = s.prepareReportRefreshLocked(project, lastSuccessfulSessionID, previousSessionCount)
		audit = s.appendAuditLocked(context.Background(), project.OrgID, auditEventInput{
			ProjectID: project.ID,
			Type:      "session_import_job.completed",
			Message:   fmt.Sprintf("session import job processed %d session summaries", successCount),
			Result:    "success",
			Reason:    "collector uploaded session summaries through async import job",
		})
	}

	completedAt := time.Now().UTC()
	job.CompletedAt = cloneTime(&completedAt)
	job.LastError = ""
	if job.FailedSessions > 0 {
		job.Status = sessionImportJobStatusPartial
	} else {
		job.Status = sessionImportJobStatusSucceeded
	}
	job.Sessions = nil
	s.touchSessionImportJob(job, completedAt)
	if job.Status == sessionImportJobStatusPartial {
		logSessionImportJobEvent(slog.LevelWarn, "completed", job, "result", "partially_failed")
	} else {
		logSessionImportJobEvent(slog.LevelInfo, "completed", job, "result", "succeeded")
	}
	if err := s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
		if err := s.AnalyticsStore.persistSessionImportJobLocked(tx, job.ID); err != nil {
			return err
		}
		if successCount > 0 {
			if err := s.AnalyticsStore.persistReportResearchLocked(tx, project.ID); err != nil {
				return err
			}
		}
		if audit != nil {
			if err := s.AnalyticsStore.persistAuditLocked(tx, audit.ID); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		s.AnalyticsStore.mu.Unlock()
		return
	}
	s.AnalyticsStore.mu.Unlock()

	_ = reports
	s.enqueueReportRefresh(refreshJobs)
}

func (s *AnalyticsService) appendSessionImportAuditAsync(orgID, projectID, eventType, message, reason string) {
	orgID = strings.TrimSpace(orgID)
	projectID = strings.TrimSpace(projectID)
	if orgID == "" {
		return
	}
	go func() {
		s.AnalyticsStore.mu.Lock()
		defer s.AnalyticsStore.mu.Unlock()
		audit := s.appendAuditLocked(context.Background(), orgID, auditEventInput{
			ProjectID: projectID,
			Type:      eventType,
			Message:   message,
			Result:    "success",
			Reason:    reason,
		})
		_ = s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
			return s.AnalyticsStore.persistAuditLocked(tx, audit.ID)
		})
	}()
}

func (s *AnalyticsService) sessionImportJobSessionFromRequest(req request.SessionSummaryReq) SessionImportJobSession {
	return SessionImportJobSession{
		SessionID:              strings.TrimSpace(req.SessionID),
		Tool:                   strings.TrimSpace(req.Tool),
		TokenIn:                req.TokenIn,
		TokenOut:               req.TokenOut,
		CachedInputTokens:      req.CachedInputTokens,
		ReasoningOutputTokens:  req.ReasoningOutputTokens,
		FunctionCallCount:      req.FunctionCallCount,
		ToolErrorCount:         req.ToolErrorCount,
		SessionDurationMS:      req.SessionDurationMS,
		ToolWallTimeMS:         req.ToolWallTimeMS,
		ToolCalls:              cloneIntMap(req.ToolCalls),
		ToolErrors:             cloneIntMap(req.ToolErrors),
		ToolWallTimesMS:        cloneIntMap(req.ToolWallTimesMS),
		RawQueries:             cloneStringSlice(req.RawQueries),
		Models:                 cloneStringSlice(req.Models),
		ModelProvider:          strings.TrimSpace(req.ModelProvider),
		FirstResponseLatencyMS: req.FirstResponseLatencyMS,
		AssistantResponses:     cloneStringSlice(req.AssistantResponses),
		ReasoningSummaries:     cloneStringSlice(req.ReasoningSummaries),
		Timestamp:              req.Timestamp,
	}
}

func (s *AnalyticsService) sessionImportJobSessionToRequest(projectID string, item SessionImportJobSession) request.SessionSummaryReq {
	return request.SessionSummaryReq{
		ProjectID:              projectID,
		SessionID:              strings.TrimSpace(item.SessionID),
		Tool:                   strings.TrimSpace(item.Tool),
		TokenIn:                item.TokenIn,
		TokenOut:               item.TokenOut,
		CachedInputTokens:      item.CachedInputTokens,
		ReasoningOutputTokens:  item.ReasoningOutputTokens,
		FunctionCallCount:      item.FunctionCallCount,
		ToolErrorCount:         item.ToolErrorCount,
		SessionDurationMS:      item.SessionDurationMS,
		ToolWallTimeMS:         item.ToolWallTimeMS,
		ToolCalls:              cloneIntMap(item.ToolCalls),
		ToolErrors:             cloneIntMap(item.ToolErrors),
		ToolWallTimesMS:        cloneIntMap(item.ToolWallTimesMS),
		RawQueries:             cloneStringSlice(item.RawQueries),
		Models:                 cloneStringSlice(item.Models),
		ModelProvider:          strings.TrimSpace(item.ModelProvider),
		FirstResponseLatencyMS: item.FirstResponseLatencyMS,
		AssistantResponses:     cloneStringSlice(item.AssistantResponses),
		ReasoningSummaries:     cloneStringSlice(item.ReasoningSummaries),
		Timestamp:              item.Timestamp,
	}
}

func (s *AnalyticsService) toSessionImportJobRespLocked(job *SessionImportJob, reused bool) *response.SessionImportJobResp {
	if job == nil {
		return nil
	}
	failures := make([]response.SessionImportJobFailureResp, 0, len(job.Failures))
	for _, item := range job.Failures {
		failures = append(failures, response.SessionImportJobFailureResp{
			SessionID:    strings.TrimSpace(item.SessionID),
			Error:        strings.TrimSpace(item.Error),
			HTTPStatus:   item.HTTPStatus,
			APIErrorCode: item.APIErrorCode,
		})
	}
	return &response.SessionImportJobResp{
		SchemaVersion:     reportAPISchemaVersion,
		JobID:             job.ID,
		ProjectID:         job.ProjectID,
		Status:            strings.TrimSpace(job.Status),
		Reused:            reused,
		TotalSessions:     job.TotalSessions,
		ReceivedSessions:  job.ReceivedSessions,
		ProcessedSessions: job.ProcessedSessions,
		UploadedSessions:  job.UploadedSessions,
		UpdatedSessions:   job.UpdatedSessions,
		FailedSessions:    job.FailedSessions,
		CreatedAt:         job.CreatedAt,
		StartedAt:         cloneTime(job.StartedAt),
		CompletedAt:       cloneTime(job.CompletedAt),
		LastError:         strings.TrimSpace(job.LastError),
		Failures:          failures,
		ResearchStatus:    cloneReportResearchStatusResp(s.AnalyticsStore.reportResearch[job.ProjectID]),
	}
}

func (s *AnalyticsService) ingestSessionSummaryLocked(project *Project, req *request.SessionSummaryReq) (*response.SessionIngestResp, bool, error) {
	if project == nil {
		return nil, false, ecode.InvalidParams.WithCause(ecode.NewInvalidParamsErr("unknown project"))
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		return nil, false, ecode.NewInvalidParamsErr("tool is required")
	}

	recordedAt := req.Timestamp.UTC()
	if recordedAt.IsZero() {
		recordedAt = time.Now().UTC()
	}
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		sessionID = s.AnalyticsStore.nextID("session")
	}

	req.ProjectID = project.ID
	req.SessionID = sessionID
	req.Tool = tool

	summary := &SessionSummary{
		ID:                     sessionID,
		ProjectID:              project.ID,
		Tool:                   tool,
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
	for i, item := range s.AnalyticsStore.sessionSummaries[project.ID] {
		if item.ID == sessionID {
			existingIndex = i
			break
		}
	}
	updated := existingIndex >= 0
	if updated {
		s.AnalyticsStore.sessionSummaries[project.ID][existingIndex] = summary
	} else {
		s.AnalyticsStore.sessionSummaries[project.ID] = append(s.AnalyticsStore.sessionSummaries[project.ID], summary)
	}
	if project.LastIngestedAt == nil || recordedAt.After(*project.LastIngestedAt) {
		project.LastIngestedAt = &recordedAt
	}

	return &response.SessionIngestResp{
		SchemaVersion: reportAPISchemaVersion,
		SessionID:     sessionID,
		ProjectID:     project.ID,
		RecordedAt:    recordedAt,
	}, updated, nil
}

func newSessionBatchFailureItem(sessionID string, err error) response.SessionBatchIngestItemResp {
	item := response.SessionBatchIngestItemResp{
		SessionID: strings.TrimSpace(sessionID),
		Status:    "failed",
	}
	if err == nil {
		return item
	}

	ec := ecode.FromError(err)
	item.Error = ec.Message
	item.HTTPStatus = ec.HttpCode
	item.APIErrorCode = ec.Code
	return item
}

func reportIDs(reports []*Report) []string {
	ids := make([]string, 0, len(reports))
	for _, item := range reports {
		if item == nil {
			continue
		}
		ids = append(ids, item.ID)
	}
	return ids
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

	return &response.ReportListResp{
		SchemaVersion: reportAPISchemaVersion,
		Items:         items,
	}, nil
}

func (s *AnalyticsService) DashboardOverview(ctx context.Context, req *request.DashboardOverviewReq) (*response.DashboardOverviewResp, error) {
	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()
	if err := s.cleanupExpiredSessionImportJobsLocked(time.Now().UTC()); err != nil {
		return nil, err
	}

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
		activeImportJob    *SessionImportJob
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
		if candidate := s.findNewestActiveSessionImportJobForProjectLocked(projectID); isSessionImportJobNewer(candidate, activeImportJob) {
			activeImportJob = candidate
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
		ActiveImportJob:           s.toSessionImportJobRespLocked(activeImportJob, false),
		ImportJobMetrics:          s.buildSessionImportJobMetricsLocked(req.OrgID),
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

func (s *AnalyticsService) AdminImportJobMetrics(ctx context.Context, req *request.AdminImportJobMetricsReq) (*response.AdminImportJobMetricsResp, error) {
	now := time.Now().UTC()

	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()
	if err := s.cleanupExpiredSessionImportJobsLocked(now); err != nil {
		return nil, err
	}

	identity, _, err := s.requireAdminUserLocked(ctx)
	if err != nil {
		return nil, err
	}

	projectID := strings.TrimSpace(req.ProjectID)
	if projectID != "" {
		project, ok := s.AnalyticsStore.projects[projectID]
		if !ok || project == nil || strings.TrimSpace(project.OrgID) != identity.OrgID {
			return nil, ecode.NotFound.WithCause(ecode.NewInvalidParamsErr("unknown workspace"))
		}
	}

	limit := req.Limit
	if limit <= 0 || limit > 50 {
		limit = 10
	}

	return &response.AdminImportJobMetricsResp{
		OrgID:       identity.OrgID,
		ProjectID:   projectID,
		GeneratedAt: now,
		Metrics:     s.buildSessionImportJobMetricsFilteredLocked(identity.OrgID, projectID),
		ActiveJobs: s.listAdminSessionImportJobsLocked(identity.OrgID, projectID, limit, func(job *SessionImportJob) bool {
			return isActiveSessionImportJobStatus(strings.TrimSpace(job.Status))
		}),
		RecentFailures: s.listAdminSessionImportJobsLocked(identity.OrgID, projectID, limit, func(job *SessionImportJob) bool {
			status := strings.TrimSpace(job.Status)
			return status == sessionImportJobStatusFailed || status == sessionImportJobStatusPartial
		}),
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
	user, err := s.findAdminUserLocked(identity.OrgID, req.UserID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureUserCanLoseAccessLocked(user); err != nil {
		return nil, err
	}
	user.Status = userStatusDisabled
	user.DisabledAt = cloneTime(&now)
	s.AnalyticsStore.revokeUserTokensLocked(user.ID, now)
	tokenIDs := s.AnalyticsStore.accessTokenIDsForUserLocked(user.ID)
	audit := s.appendAuditLocked(ctx, identity.OrgID, auditEventInput{
		Type:         "admin.user_deactivated",
		Message:      "admin deactivated a user",
		TargetUserID: user.ID,
		Result:       "success",
		Reason:       "organization user disabled and sessions revoked",
	})
	if err := s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
		if err := s.AnalyticsStore.persistUserLocked(tx, user.ID); err != nil {
			return err
		}
		if err := s.AnalyticsStore.persistAccessTokensLocked(tx, tokenIDs...); err != nil {
			return err
		}
		return s.AnalyticsStore.persistAuditLocked(tx, audit.ID)
	}); err != nil {
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
	user, err := s.findAdminUserLocked(identity.OrgID, req.UserID)
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
	tokenIDs := s.AnalyticsStore.accessTokenIDsForUserLocked(user.ID)
	audit := s.appendAuditLocked(ctx, identity.OrgID, auditEventInput{
		Type:         "admin.user_deleted",
		Message:      "admin deleted a user",
		TargetUserID: user.ID,
		Result:       "success",
		Reason:       "organization user soft-deleted and sessions revoked",
	})
	if err := s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
		if err := s.AnalyticsStore.persistUserLocked(tx, user.ID); err != nil {
			return err
		}
		if err := s.AnalyticsStore.persistAccessTokensLocked(tx, tokenIDs...); err != nil {
			return err
		}
		return s.AnalyticsStore.persistAuditLocked(tx, audit.ID)
	}); err != nil {
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
		if rec.Status != "active" {
			continue
		}
		items = append(items, rec)
	}
	return items
}

func (s *AnalyticsService) prepareReportRefreshLocked(project *Project, triggerSessionID string, previousSessionCount int) ([]*Report, []*reportRefreshJob) {
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
	checkpoints := reportRefreshCheckpointCounts(previousSessionCount, sessionCount, s.reportMinSessions, defaultReportSessionAnalysisCap)
	if len(checkpoints) == 0 {
		if currentStatus != nil && strings.EqualFold(strings.TrimSpace(currentStatus.State), "running") {
			return currentReports, nil
		}
		preserveReportResearchHistory(&status, currentStatus)
		if sessionCount >= defaultReportSessionAnalysisCap {
			status.State = "capped_history_window"
			status.Summary = fmt.Sprintf("Collected %d sessions. Cold-start feedback analysis is capped at the first %d sessions to control cost.", sessionCount, defaultReportSessionAnalysisCap)
		} else {
			status.State = "waiting_for_next_batch"
			nextRefreshAt := nextReportRefreshSessionCount(sessionCount, s.reportMinSessions)
			if len(currentReports) > 0 {
				status.Summary = fmt.Sprintf("Collected %d sessions. The current feedback report stays active until %d sessions are collected for the next refresh.", sessionCount, nextRefreshAt)
			} else {
				status.Summary = fmt.Sprintf("Collected %d sessions. The next feedback analysis starts at %d sessions.", sessionCount, nextRefreshAt)
			}
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
	if len(checkpoints) > 1 {
		status.Summary = fmt.Sprintf("Preparing %d backfill feedback passes with %s while the first %d uploaded sessions are analyzed in %d-session steps.", len(checkpoints), s.researchAgent.Provider, checkpoints[len(checkpoints)-1], s.reportMinSessions)
	} else {
		status.Summary = fmt.Sprintf("Preparing the next feedback report with %s while uploaded sessions are analyzed in the background.", s.researchAgent.Provider)
	}
	status.StartedAt = cloneTime(&triggeredAt)
	s.setReportResearchStatusLocked(project.ID, status)
	orderedSessions := reportRefreshSessionsInChronologicalOrder(sessions)
	snapshots := cloneConfigSnapshots(s.AnalyticsStore.configSnapshots[project.ID])
	jobs := make([]*reportRefreshJob, 0, len(checkpoints))
	for _, checkpoint := range checkpoints {
		jobs = append(jobs, &reportRefreshJob{
			project:          cloneProject(project),
			sessions:         cloneSessionSummaries(orderedSessions[:checkpoint]),
			snapshots:        cloneConfigSnapshots(snapshots),
			triggerSessionID: triggerSessionID,
			triggeredAt:      triggeredAt,
		})
	}
	return currentReports, jobs
}

func shouldRefreshReportsForSessionCount(sessionCount, batchSize int) bool {
	if sessionCount <= 0 || batchSize <= 0 {
		return false
	}
	return sessionCount >= batchSize && sessionCount%batchSize == 0
}

func shouldRefreshReportsForSessionRange(previousSessionCount, sessionCount, batchSize int) bool {
	if sessionCount <= 0 || batchSize <= 0 || sessionCount <= previousSessionCount {
		return false
	}
	return nextReportRefreshSessionCount(previousSessionCount, batchSize) <= sessionCount
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

func (s *AnalyticsService) enqueueReportRefresh(jobs []*reportRefreshJob) {
	if len(jobs) == 0 || jobs[0] == nil || jobs[0].project == nil {
		return
	}

	s.reportRefreshMu.Lock()
	projectID := jobs[0].project.ID
	if s.reportRefreshLive[projectID] {
		s.reportRefreshNext[projectID] = append(s.reportRefreshNext[projectID], jobs...)
		s.reportRefreshMu.Unlock()
		return
	}
	s.reportRefreshLive[projectID] = true
	s.reportRefreshMu.Unlock()

	go func(queue []*reportRefreshJob) {
		pending := append([]*reportRefreshJob(nil), queue...)
		for len(pending) > 0 {
			current := pending[0]
			pending = pending[1:]
			if current != nil {
				s.runReportRefresh(current)
			}

			s.reportRefreshMu.Lock()
			if len(pending) == 0 && len(s.reportRefreshNext[projectID]) > 0 {
				pending = append(pending, s.reportRefreshNext[projectID]...)
				delete(s.reportRefreshNext, projectID)
			}
			if len(pending) == 0 {
				delete(s.reportRefreshLive, projectID)
				s.reportRefreshMu.Unlock()
				return
			}
			s.reportRefreshMu.Unlock()
		}
	}(jobs)
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
		_ = s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
			return s.AnalyticsStore.persistReportResearchLocked(tx, job.project.ID)
		})
		return
	}

	previousIDs := append([]string(nil), s.AnalyticsStore.projectReports[job.project.ID]...)
	previousSkillSetVersionIDs := skillSetVersionIDs(s.AnalyticsStore.skillSetVersions[job.project.ID])
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
	s.AnalyticsStore.projectReports[job.project.ID] = append(ids, previousIDs...)
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
	prevVersion := latestSkillSetVersion(s.AnalyticsStore.skillSetVersions[job.project.ID])
	bundle, bundleErr := buildLatestSkillSetBundle(job.project.ID, s.activeSkillSetReportsLocked(job.project.ID), skillSetBuildOptions{
		RefineAgent:     s.refineAgent,
		PreviousVersion: prevVersion,
	})
	if bundleErr == nil {
		s.ensureLatestSkillSetVersionLocked(job.project, bundle, s.activeSkillSetReportsLocked(job.project.ID))
	}
	s.setReportResearchStatusLocked(job.project.ID, status)
	_ = s.AnalyticsStore.withTxLocked(func(tx *sql.Tx) error {
		if err := s.AnalyticsStore.persistReportsLocked(tx, append(ids, previousIDs...)...); err != nil {
			return err
		}
		if err := s.AnalyticsStore.persistProjectReportsLocked(tx, job.project.ID); err != nil {
			return err
		}
		if err := s.AnalyticsStore.persistReportResearchLocked(tx, job.project.ID); err != nil {
			return err
		}
		return s.syncSkillSetVersionHistoryLocked(tx, job.project.ID, previousSkillSetVersionIDs)
	})
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

func reportRefreshCheckpointCounts(previousSessionCount, sessionCount, batchSize, cap int) []int {
	if sessionCount <= 0 || batchSize <= 0 || sessionCount <= previousSessionCount || cap <= 0 {
		return nil
	}
	upperBound := minInt(sessionCount, cap)
	start := nextReportRefreshSessionCount(previousSessionCount, batchSize)
	if start <= 0 || start > upperBound {
		return nil
	}
	checkpoints := make([]int, 0, ((upperBound-start)/batchSize)+1)
	for checkpoint := start; checkpoint <= upperBound; checkpoint += batchSize {
		checkpoints = append(checkpoints, checkpoint)
	}
	return checkpoints
}

func reportRefreshSessionsInChronologicalOrder(sessions []*SessionSummary) []*SessionSummary {
	ordered := cloneSessionSummaries(sessions)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Timestamp.Equal(ordered[j].Timestamp) {
			return strings.TrimSpace(ordered[i].ID) < strings.TrimSpace(ordered[j].ID)
		}
		return ordered[i].Timestamp.Before(ordered[j].Timestamp)
	})
	return ordered
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

func (s *AnalyticsService) appendAuditLocked(ctx context.Context, orgID string, input auditEventInput) *AuditEvent {
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

	event := &AuditEvent{
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
	}
	s.AnalyticsStore.audits = append(s.AnalyticsStore.audits, event)
	return event
}

func (s *AnalyticsService) findAdminUserLocked(orgID, userID string) (*User, error) {
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
		ID:          user.ID,
		OrgID:       user.OrgID,
		Email:       user.Email,
		Name:        user.Name,
		Source:      user.Source,
		Role:        normalizeUserRole(user.Role),
		Status:      normalizeUserStatus(user.Status),
		CreatedAt:   user.CreatedAt,
		LastLoginAt: cloneTime(user.LastLoginAt),
		DisabledAt:  cloneTime(user.DisabledAt),
		DeletedAt:   cloneTime(user.DeletedAt),
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
