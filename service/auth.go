package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
	"time"
)

const (
	WebSessionCookieName = "crux_web_session"

	TokenKindCLI           = "cli"
	TokenKindCLIEnrollment = "cli_enrollment"
	TokenKindDeviceAccess  = "device_access"
	TokenKindDeviceRefresh = "device_refresh"
	TokenKindStatic        = "static"
	TokenKindWebSession    = "web_session"

	ErrCodeDeviceAccessTokenExpired  = 1022
	ErrCodeDeviceRefreshTokenExpired = 1023
	ErrCodeAgentBindingMismatch      = 1024

	defaultCLITokenTTL           = 10 * time.Minute
	defaultDeviceAccessTokenTTL  = 24 * time.Hour
	defaultDeviceRefreshTokenTTL = 30 * 24 * time.Hour
	defaultSessionTokenTTL       = 24 * time.Hour

	defaultDemoOrgID    = "demo-org"
	defaultDemoOrgName  = "Demo Org"
	defaultDemoUserID   = "demo-user"
	defaultDemoUserName = "Demo Operator"
	defaultDemoEmail    = "demo@example.com"

	userSourceDemo      = "demo"
	userSourceBootstrap = "bootstrap"
	userSourceGoogle    = "google"

	userRoleAdmin  = "admin"
	userRoleMember = "member"

	userStatusActive   = "active"
	userStatusDisabled = "disabled"
	userStatusDeleted  = "deleted"

	authProviderGoogle = "google"
)

type AccessToken struct {
	ID              string
	OrgID           string
	UserID          string
	AgentID         string
	Label           string
	Kind            string
	TokenPrefix     string
	TokenHash       string
	CreatedAt       time.Time
	ExpiresAt       *time.Time
	LastUsedAt      *time.Time
	LastSeenAt      *time.Time
	ConsumedAt      *time.Time
	RevokedAt       *time.Time
	ParentTokenID   string
	RotationCounter int
}

type AuthIdentity struct {
	TokenID   string
	TokenKind string
	OrgID     string
	UserID    string
	AgentID   string
	UserRole  string
}

type authContextKey struct{}

const accessTokenValidationFailed = 1001

func WithAuthIdentity(ctx context.Context, identity AuthIdentity) context.Context {
	return context.WithValue(ctx, authContextKey{}, identity)
}

func AuthIdentityFromContext(ctx context.Context) (AuthIdentity, bool) {
	identity, ok := ctx.Value(authContextKey{}).(AuthIdentity)
	return identity, ok
}

func (s *AnalyticsStore) ensureBootstrapData() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	modified := false
	if s.revokeLegacyWebSessionsLocked(now) {
		modified = true
	}
	if s.allowDemoUser {
		modified = s.ensureDemoUserLocked(now)
	} else if s.removeDemoUserLocked() {
		modified = true
	}
	if s.ensureConfiguredUsersLocked(now) {
		modified = true
	}
	if s.collapseProjectsLocked() {
		modified = true
	}

	if !modified {
		return nil
	}

	return s.persistLocked()
}

func (s *AnalyticsStore) ensureDemoUserLocked(now time.Time) bool {
	modified := false

	org := s.organizations[defaultDemoOrgID]
	if org == nil {
		s.organizations[defaultDemoOrgID] = &Organization{
			ID:   defaultDemoOrgID,
			Name: defaultDemoOrgName,
		}
		modified = true
	} else if strings.TrimSpace(org.Name) == "" {
		org.Name = defaultDemoOrgName
		modified = true
	}

	user := s.users[defaultDemoUserID]
	if user == nil {
		s.users[defaultDemoUserID] = &User{
			ID:        defaultDemoUserID,
			OrgID:     defaultDemoOrgID,
			Email:     defaultDemoEmail,
			Name:      defaultDemoUserName,
			Source:    userSourceDemo,
			Role:      userRoleAdmin,
			Status:    userStatusActive,
			CreatedAt: now,
		}
		return true
	}

	if strings.TrimSpace(user.OrgID) == "" {
		user.OrgID = defaultDemoOrgID
		modified = true
	}
	if strings.TrimSpace(user.Email) == "" {
		user.Email = defaultDemoEmail
		modified = true
	}
	if strings.TrimSpace(user.Name) == "" {
		user.Name = defaultDemoUserName
		modified = true
	}
	if strings.TrimSpace(user.Source) == "" {
		user.Source = userSourceDemo
		modified = true
	}
	if normalizeUserRole(user.Role) != userRoleAdmin {
		user.Role = userRoleAdmin
		modified = true
	}
	if normalizeUserStatus(user.Status) != userStatusActive {
		user.Status = userStatusActive
		user.DisabledAt = nil
		user.DeletedAt = nil
		modified = true
	}
	if user.CreatedAt.IsZero() {
		user.CreatedAt = now
		modified = true
	}

	return modified
}

func (s *AnalyticsStore) removeDemoUserLocked() bool {
	modified := false

	if _, ok := s.users[defaultDemoUserID]; ok {
		delete(s.users, defaultDemoUserID)
		modified = true
	}
	for id, user := range s.users {
		if user != nil && normalizeEmail(user.Email) == defaultDemoEmail {
			delete(s.users, id)
			modified = true
		}
	}
	for id, token := range s.accessTokens {
		if token == nil {
			continue
		}
		if token.UserID == defaultDemoUserID || token.OrgID == defaultDemoOrgID {
			delete(s.accessTokens, id)
			modified = true
		}
	}
	if org, ok := s.organizations[defaultDemoOrgID]; ok {
		deleteOrg := true
		for _, user := range s.users {
			if user != nil && user.OrgID == org.ID {
				deleteOrg = false
				break
			}
		}
		if deleteOrg {
			delete(s.organizations, defaultDemoOrgID)
			modified = true
		}
	}
	return modified
}

func (s *AnalyticsStore) ensureConfiguredUsersLocked(now time.Time) bool {
	modified := false
	desiredIDs := make(map[string]struct{}, len(s.bootstrapUsers))
	desiredEmails := make(map[string]struct{}, len(s.bootstrapUsers))

	for _, item := range s.bootstrapUsers {
		email := normalizeEmail(item.Email)
		role := normalizeUserRole(item.Role)
		if role == "" {
			role = userRoleMember
		}
		if email == "" {
			continue
		}

		orgID := strings.TrimSpace(item.OrgID)
		if orgID == "" {
			orgID = defaultDemoOrgID
		}
		orgName := strings.TrimSpace(item.OrgName)
		if orgName == "" {
			orgName = orgID
		}

		org := s.organizations[orgID]
		if org == nil {
			s.organizations[orgID] = &Organization{
				ID:   orgID,
				Name: orgName,
			}
			modified = true
		} else if strings.TrimSpace(org.Name) == "" || org.Name != orgName {
			org.Name = orgName
			modified = true
		}

		userID := strings.TrimSpace(item.ID)
		if userID == "" {
			userID = bootstrapUserID(email)
		}
		desiredIDs[userID] = struct{}{}
		desiredEmails[email] = struct{}{}

		user := s.users[userID]
		if user == nil {
			user = s.findUserByEmailLocked(email)
		}
		if user == nil {
			s.users[userID] = &User{
				ID:        userID,
				OrgID:     orgID,
				Email:     email,
				Name:      firstNonEmpty(strings.TrimSpace(item.Name), userID),
				Source:    userSourceBootstrap,
				Role:      role,
				Status:    userStatusActive,
				CreatedAt: now,
			}
			modified = true
			continue
		}

		accessChanged := false
		if strings.TrimSpace(user.OrgID) == "" || user.OrgID != orgID {
			if user.OrgID != "" && user.OrgID != orgID {
				accessChanged = true
			}
			user.OrgID = orgID
			modified = true
		}
		if normalizeEmail(user.Email) == "" || normalizeEmail(user.Email) != email {
			if normalizeEmail(user.Email) != "" && normalizeEmail(user.Email) != email {
				accessChanged = true
			}
			user.Email = email
			modified = true
		}
		desiredName := firstNonEmpty(strings.TrimSpace(item.Name), userID)
		if strings.TrimSpace(user.Name) == "" || user.Name != desiredName {
			user.Name = desiredName
			modified = true
		}
		if strings.TrimSpace(user.Source) == "" || user.Source != userSourceBootstrap {
			user.Source = userSourceBootstrap
			modified = true
		}
		if normalizeUserRole(user.Role) != role {
			user.Role = role
			accessChanged = true
			modified = true
		}
		if normalizeUserStatus(user.Status) != userStatusActive || user.DisabledAt != nil || user.DeletedAt != nil {
			user.Status = userStatusActive
			user.DisabledAt = nil
			user.DeletedAt = nil
			accessChanged = true
			modified = true
		}
		if user.CreatedAt.IsZero() {
			user.CreatedAt = now
			modified = true
		}
		if accessChanged && s.revokeUserTokensLocked(user.ID, now) {
			modified = true
		}
	}

	if s.reconcileBootstrapUsersLocked(desiredIDs, desiredEmails, now) {
		modified = true
	}

	return modified
}

func (s *AnalyticsStore) reconcileBootstrapUsersLocked(desiredIDs, desiredEmails map[string]struct{}, now time.Time) bool {
	modified := false
	for id, user := range s.users {
		if user == nil || user.Source != userSourceBootstrap {
			continue
		}
		if _, ok := desiredIDs[id]; ok {
			continue
		}
		if _, ok := desiredEmails[normalizeEmail(user.Email)]; ok {
			continue
		}
		if s.revokeUserTokensLocked(id, now) {
			modified = true
		}
		delete(s.users, id)
		modified = true
	}
	return modified
}

func (s *AnalyticsStore) revokeUserTokensLocked(userID string, now time.Time) bool {
	modified := false
	for _, token := range s.accessTokens {
		if token == nil || token.UserID != userID || token.RevokedAt != nil {
			continue
		}
		revokedAt := now
		token.RevokedAt = &revokedAt
		modified = true
	}
	return modified
}

func (s *AnalyticsStore) revokeLegacyWebSessionsLocked(now time.Time) bool {
	modified := false
	for _, token := range s.accessTokens {
		if token == nil || token.Kind != TokenKindWebSession || token.RevokedAt != nil {
			continue
		}
		user, ok := s.users[token.UserID]
		if !ok || user == nil {
			continue
		}
		if normalizeAuthProvider(user.AuthProvider) == authProviderGoogle {
			continue
		}
		revokedAt := now
		token.RevokedAt = &revokedAt
		modified = true
	}
	return modified
}

func (s *AnalyticsStore) findUserByEmailLocked(email string) *User {
	normalized := normalizeEmail(email)
	if normalized == "" {
		return nil
	}

	for _, user := range s.users {
		if normalizeEmail(user.Email) == normalized {
			return user
		}
	}

	return nil
}

func (s *AnalyticsStore) findUserByAuthSubjectLocked(provider, subject string) *User {
	normalizedProvider := normalizeAuthProvider(provider)
	normalizedSubject := strings.TrimSpace(subject)
	if normalizedProvider == "" || normalizedSubject == "" {
		return nil
	}

	for _, user := range s.users {
		if normalizeAuthProvider(user.AuthProvider) != normalizedProvider {
			continue
		}
		if strings.TrimSpace(user.AuthSubject) == normalizedSubject {
			return user
		}
	}

	return nil
}

type issueAccessTokenOptions struct {
	Kind            string
	OrgID           string
	UserID          string
	AgentID         string
	Label           string
	TTL             time.Duration
	ParentTokenID   string
	RotationCounter int
}

func (s *AnalyticsStore) issueAccessTokenLocked(kind, orgID, userID, label string, ttl time.Duration, now time.Time) (string, *AccessToken, error) {
	return s.issueAccessTokenWithOptionsLocked(issueAccessTokenOptions{
		Kind:   kind,
		OrgID:  orgID,
		UserID: userID,
		Label:  label,
		TTL:    ttl,
	}, now)
}

func (s *AnalyticsStore) issueAccessTokenWithOptionsLocked(opts issueAccessTokenOptions, now time.Time) (string, *AccessToken, error) {
	token, err := newAccessTokenValue(opts.Kind)
	if err != nil {
		return "", nil, err
	}

	record := &AccessToken{
		ID:              s.nextID("token"),
		OrgID:           opts.OrgID,
		UserID:          opts.UserID,
		AgentID:         strings.TrimSpace(opts.AgentID),
		Label:           strings.TrimSpace(opts.Label),
		Kind:            opts.Kind,
		TokenPrefix:     tokenPrefix(token),
		TokenHash:       hashSecret(token),
		CreatedAt:       now,
		ParentTokenID:   strings.TrimSpace(opts.ParentTokenID),
		RotationCounter: opts.RotationCounter,
	}
	if opts.TTL > 0 {
		expiresAt := now.Add(opts.TTL)
		record.ExpiresAt = &expiresAt
	}

	s.accessTokens[record.ID] = record
	return token, record, nil
}

func (s *AnalyticsStore) ValidateAccessToken(token string) (*AuthIdentity, bool) {
	identity, code := s.ValidateAccessTokenWithCode(token)
	return identity, code == 0 && identity != nil
}

func (s *AnalyticsStore) ValidateAccessTokenWithCode(token string) (*AuthIdentity, int) {
	secretHash := hashSecret(strings.TrimSpace(token))
	if secretHash == "" {
		return nil, accessTokenValidationFailed
	}

	now := time.Now().UTC()

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, record := range s.accessTokens {
		if record == nil {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(record.TokenHash), []byte(secretHash)) != 1 {
			continue
		}
		if record.RevokedAt != nil || record.ConsumedAt != nil {
			return nil, accessTokenValidationFailed
		}
		if record.ExpiresAt != nil && now.After(record.ExpiresAt.UTC()) {
			switch record.Kind {
			case TokenKindDeviceAccess:
				return nil, ErrCodeDeviceAccessTokenExpired
			case TokenKindDeviceRefresh:
				return nil, ErrCodeDeviceRefreshTokenExpired
			default:
				return nil, accessTokenValidationFailed
			}
		}
		user, ok := s.users[record.UserID]
		if !ok || !userCanAuthenticate(user) {
			return nil, accessTokenValidationFailed
		}
		if requiresAgentBinding(record.Kind) {
			agent, ok := s.agents[record.AgentID]
			if !ok || agent == nil {
				return nil, accessTokenValidationFailed
			}
			if agent.OrgID != record.OrgID || agent.UserID != record.UserID {
				return nil, accessTokenValidationFailed
			}
		}

		return &AuthIdentity{
			TokenID:   record.ID,
			TokenKind: record.Kind,
			OrgID:     record.OrgID,
			UserID:    record.UserID,
			AgentID:   record.AgentID,
			UserRole:  normalizeUserRole(user.Role),
		}, 0
	}

	return nil, accessTokenValidationFailed
}

func (s *AnalyticsStore) revokeAccessTokenChainLocked(rootTokenID string, now time.Time) bool {
	if strings.TrimSpace(rootTokenID) == "" {
		return false
	}

	modified := false
	queue := []string{rootTokenID}
	seen := map[string]struct{}{}

	for len(queue) > 0 {
		tokenID := queue[0]
		queue = queue[1:]
		if _, ok := seen[tokenID]; ok {
			continue
		}
		seen[tokenID] = struct{}{}

		record := s.accessTokens[tokenID]
		if record != nil && record.RevokedAt == nil {
			revokedAt := now
			record.RevokedAt = &revokedAt
			modified = true
		}

		for _, candidate := range s.accessTokens {
			if candidate == nil || strings.TrimSpace(candidate.ParentTokenID) != tokenID {
				continue
			}
			queue = append(queue, candidate.ID)
		}
	}

	return modified
}

func (s *AnalyticsStore) findAccessTokenBySecretHashLocked(secretHash string) *AccessToken {
	secretHash = strings.TrimSpace(secretHash)
	if secretHash == "" {
		return nil
	}
	for _, record := range s.accessTokens {
		if record == nil {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(record.TokenHash), []byte(secretHash)) == 1 {
			return record
		}
	}
	return nil
}

func (s *AnalyticsStore) accessTokenChainRootLocked(tokenID string) string {
	tokenID = strings.TrimSpace(tokenID)
	if tokenID == "" {
		return ""
	}

	current := tokenID
	seen := map[string]struct{}{}
	for current != "" {
		if _, ok := seen[current]; ok {
			break
		}
		seen[current] = struct{}{}

		record := s.accessTokens[current]
		if record == nil || strings.TrimSpace(record.ParentTokenID) == "" {
			return current
		}
		current = strings.TrimSpace(record.ParentTokenID)
	}

	return tokenID
}

func (s *AnalyticsStore) revokeAgentTokensLocked(agentID string, now time.Time) bool {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return false
	}

	modified := false
	for _, token := range s.accessTokens {
		if token == nil || token.AgentID != agentID || token.RevokedAt != nil {
			continue
		}
		if token.Kind != TokenKindDeviceAccess && token.Kind != TokenKindDeviceRefresh {
			continue
		}
		revokedAt := now
		token.RevokedAt = &revokedAt
		modified = true
	}
	return modified
}

func accessTokenStatus(record *AccessToken, now time.Time) string {
	if record == nil {
		return "unknown"
	}
	if record.RevokedAt != nil {
		return "revoked"
	}
	if record.ConsumedAt != nil {
		return "consumed"
	}
	if record.ExpiresAt != nil && now.After(record.ExpiresAt.UTC()) {
		return "expired"
	}
	return "active"
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func normalizeAuthProvider(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}

func userCanAuthenticate(user *User) bool {
	if user == nil {
		return false
	}
	return normalizeUserStatus(user.Status) == userStatusActive
}

func normalizeUserRole(value string) string {
	if role, ok := parseUserRole(value); ok {
		return role
	}
	return userRoleMember
}

func normalizeUserStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case userStatusDisabled:
		return userStatusDisabled
	case userStatusDeleted:
		return userStatusDeleted
	case "", userStatusActive:
		return userStatusActive
	default:
		return userStatusActive
	}
}

func parseUserRole(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", userRoleMember:
		return userRoleMember, true
	case userRoleAdmin:
		return userRoleAdmin, true
	default:
		return "", false
	}
}

func bootstrapUserID(email string) string {
	email = normalizeEmail(email)
	email = strings.ReplaceAll(email, "@", "-at-")
	email = strings.ReplaceAll(email, ".", "-")
	return "user-" + email
}

func newAccessTokenValue(kind string) (string, error) {
	prefix := "agt_web"
	switch kind {
	case TokenKindCLI:
		prefix = "agt_cli"
	case TokenKindCLIEnrollment:
		prefix = "agt_enr"
	case TokenKindDeviceAccess:
		prefix = "agt_dva"
	case TokenKindDeviceRefresh:
		prefix = "agt_dvr"
	}

	return prefix + "_" + randomHex(24), nil
}

func requiresAgentBinding(kind string) bool {
	return kind == TokenKindDeviceAccess || kind == TokenKindDeviceRefresh
}

func isCLITokenKind(kind string) bool {
	switch kind {
	case TokenKindCLI, TokenKindCLIEnrollment, TokenKindDeviceAccess, TokenKindDeviceRefresh:
		return true
	default:
		return false
	}
}

func tokenPrefix(token string) string {
	token = strings.TrimSpace(token)
	if len(token) <= 14 {
		return token
	}
	return token[:14]
}

func hashSecret(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func randomHex(size int) string {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		sum := sha256.Sum256([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
		return hex.EncodeToString(sum[:])
	}
	return hex.EncodeToString(buf)
}
