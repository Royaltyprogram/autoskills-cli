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

	TokenKindCLI        = "cli"
	TokenKindStatic     = "static"
	TokenKindWebSession = "web_session"

	defaultCLITokenTTL     = 30 * 24 * time.Hour
	defaultSessionTokenTTL = 24 * time.Hour

	defaultDemoOrgID    = "demo-org"
	defaultDemoOrgName  = "Demo Org"
	defaultDemoUserID   = "demo-user"
	defaultDemoUserName = "Demo Operator"
	defaultDemoEmail    = "demo@example.com"
	defaultDemoPassword = "demo1234"

	userSourceDemo      = "demo"
	userSourceBootstrap = "bootstrap"
)

type AccessToken struct {
	ID          string
	OrgID       string
	UserID      string
	Label       string
	Kind        string
	TokenPrefix string
	TokenHash   string
	CreatedAt   time.Time
	ExpiresAt   *time.Time
	LastUsedAt  *time.Time
	RevokedAt   *time.Time
}

type AuthIdentity struct {
	TokenID   string
	TokenKind string
	OrgID     string
	UserID    string
}

type authContextKey struct{}

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
		salt, hash := hashPassword(defaultDemoPassword, "")
		s.users[defaultDemoUserID] = &User{
			ID:           defaultDemoUserID,
			OrgID:        defaultDemoOrgID,
			Email:        defaultDemoEmail,
			Name:         defaultDemoUserName,
			Source:       userSourceDemo,
			PasswordSalt: salt,
			PasswordHash: hash,
			CreatedAt:    now,
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
	if user.CreatedAt.IsZero() {
		user.CreatedAt = now
		modified = true
	}
	if user.PasswordSalt == "" || user.PasswordHash == "" {
		salt, hash := hashPassword(defaultDemoPassword, "")
		user.PasswordSalt = salt
		user.PasswordHash = hash
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
		password := strings.TrimSpace(item.Password)
		if email == "" || password == "" {
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
			salt, hash := hashPassword(password, "")
			s.users[userID] = &User{
				ID:           userID,
				OrgID:        orgID,
				Email:        email,
				Name:         firstNonEmpty(strings.TrimSpace(item.Name), userID),
				Source:       userSourceBootstrap,
				PasswordSalt: salt,
				PasswordHash: hash,
				CreatedAt:    now,
			}
			modified = true
			continue
		}

		credentialsChanged := false
		if strings.TrimSpace(user.OrgID) == "" || user.OrgID != orgID {
			if user.OrgID != "" && user.OrgID != orgID {
				credentialsChanged = true
			}
			user.OrgID = orgID
			modified = true
		}
		if normalizeEmail(user.Email) == "" || normalizeEmail(user.Email) != email {
			if normalizeEmail(user.Email) != "" && normalizeEmail(user.Email) != email {
				credentialsChanged = true
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
		if user.CreatedAt.IsZero() {
			user.CreatedAt = now
			modified = true
		}
		salt, hash := hashPassword(password, user.PasswordSalt)
		if user.PasswordSalt != salt || user.PasswordHash != hash {
			user.PasswordSalt = salt
			user.PasswordHash = hash
			credentialsChanged = true
			modified = true
		}
		if credentialsChanged && s.revokeUserTokensLocked(user.ID, now) {
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

func (s *AnalyticsStore) issueAccessTokenLocked(kind, orgID, userID, label string, ttl time.Duration, now time.Time) (string, *AccessToken, error) {
	token, err := newAccessTokenValue(kind)
	if err != nil {
		return "", nil, err
	}

	record := &AccessToken{
		ID:          s.nextID("token"),
		OrgID:       orgID,
		UserID:      userID,
		Label:       strings.TrimSpace(label),
		Kind:        kind,
		TokenPrefix: tokenPrefix(token),
		TokenHash:   hashSecret(token),
		CreatedAt:   now,
	}
	if ttl > 0 {
		expiresAt := now.Add(ttl)
		record.ExpiresAt = &expiresAt
	}

	s.accessTokens[record.ID] = record
	return token, record, nil
}

func (s *AnalyticsStore) ValidateAccessToken(token string) (*AuthIdentity, bool) {
	secretHash := hashSecret(strings.TrimSpace(token))
	if secretHash == "" {
		return nil, false
	}

	now := time.Now().UTC()

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, record := range s.accessTokens {
		if subtle.ConstantTimeCompare([]byte(record.TokenHash), []byte(secretHash)) != 1 {
			continue
		}
		if record.RevokedAt != nil {
			return nil, false
		}
		if record.ExpiresAt != nil && now.After(record.ExpiresAt.UTC()) {
			return nil, false
		}

		return &AuthIdentity{
			TokenID:   record.ID,
			TokenKind: record.Kind,
			OrgID:     record.OrgID,
			UserID:    record.UserID,
		}, true
	}

	return nil, false
}

func accessTokenStatus(record *AccessToken, now time.Time) string {
	if record == nil {
		return "unknown"
	}
	if record.RevokedAt != nil {
		return "revoked"
	}
	if record.ExpiresAt != nil && now.After(record.ExpiresAt.UTC()) {
		return "expired"
	}
	return "active"
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func hashPassword(password, salt string) (string, string) {
	if salt == "" {
		salt = randomHex(16)
	}
	return salt, hashSecret(salt + "\n" + password)
}

func verifyPassword(user *User, password string) bool {
	if user == nil || user.PasswordSalt == "" || user.PasswordHash == "" {
		return false
	}

	_, hash := hashPassword(password, user.PasswordSalt)
	return subtle.ConstantTimeCompare([]byte(hash), []byte(user.PasswordHash)) == 1
}

func bootstrapUserID(email string) string {
	email = normalizeEmail(email)
	email = strings.ReplaceAll(email, "@", "-at-")
	email = strings.ReplaceAll(email, ".", "-")
	return "user-" + email
}

func newAccessTokenValue(kind string) (string, error) {
	prefix := "agt_web"
	if kind == TokenKindCLI {
		prefix = "agt_cli"
	}

	return prefix + "_" + randomHex(24), nil
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
