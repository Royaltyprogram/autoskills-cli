package service

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Royaltyprogram/aiops/configs"
)

func TestBootstrapUserRemovalRevokesExistingTokens(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "crux-store.json")

	conf := &configs.Config{}
	conf.App.Mode = "prod"
	conf.App.StorePath = storePath
	conf.Auth.BootstrapUsers = []configs.BootstrapUser{{
		ID:       "beta-user-1",
		OrgID:    "beta-org",
		OrgName:  "Beta Org",
		Email:    "beta1@example.com",
		Name:     "Beta Operator",
		Role:     "member",
		Password: "initial-secret",
	}}

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	var tokenValue string
	store.mu.Lock()
	tokenValue, _, err = store.issueAccessTokenLocked(TokenKindCLI, "beta-org", "beta-user-1", "beta cli", defaultCLITokenTTL, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, store.persistLocked())
	store.mu.Unlock()

	_, ok := store.ValidateAccessToken(tokenValue)
	require.True(t, ok)

	confWithoutBootstrap := &configs.Config{}
	confWithoutBootstrap.App.Mode = "prod"
	confWithoutBootstrap.App.StorePath = storePath

	reloaded, err := NewAnalyticsStore(confWithoutBootstrap)
	require.NoError(t, err)

	_, ok = reloaded.ValidateAccessToken(tokenValue)
	require.False(t, ok)

	reloaded.mu.RLock()
	defer reloaded.mu.RUnlock()
	_, exists := reloaded.users["beta-user-1"]
	require.False(t, exists)

	var revokedFound bool
	for _, token := range reloaded.accessTokens {
		if token != nil && token.UserID == "beta-user-1" && token.RevokedAt != nil {
			revokedFound = true
			break
		}
	}
	require.True(t, revokedFound)
}

func TestBootstrapUserPasswordRotationRevokesExistingTokens(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "crux-store.json")

	conf := &configs.Config{}
	conf.App.Mode = "prod"
	conf.App.StorePath = storePath
	conf.Auth.BootstrapUsers = []configs.BootstrapUser{{
		ID:       "beta-user-1",
		OrgID:    "beta-org",
		OrgName:  "Beta Org",
		Email:    "beta1@example.com",
		Name:     "Beta Operator",
		Role:     "member",
		Password: "initial-secret",
	}}

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	var tokenValue string
	store.mu.Lock()
	tokenValue, _, err = store.issueAccessTokenLocked(TokenKindWebSession, "beta-org", "beta-user-1", "dashboard session", defaultSessionTokenTTL, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, store.persistLocked())
	store.mu.Unlock()

	confRotated := &configs.Config{}
	confRotated.App.Mode = "prod"
	confRotated.App.StorePath = storePath
	confRotated.Auth.BootstrapUsers = []configs.BootstrapUser{{
		ID:       "beta-user-1",
		OrgID:    "beta-org",
		OrgName:  "Beta Org Renamed",
		Email:    "beta1@example.com",
		Name:     "Renamed Operator",
		Role:     "admin",
		Password: "rotated-secret",
	}}

	reloaded, err := NewAnalyticsStore(confRotated)
	require.NoError(t, err)

	_, ok := reloaded.ValidateAccessToken(tokenValue)
	require.False(t, ok)

	reloaded.mu.RLock()
	defer reloaded.mu.RUnlock()
	user := reloaded.users["beta-user-1"]
	require.NotNil(t, user)
	require.Equal(t, userSourceBootstrap, user.Source)
	require.Equal(t, "Renamed Operator", user.Name)
	require.Equal(t, userRoleAdmin, normalizeUserRole(user.Role))
	require.True(t, verifyPassword(user, "rotated-secret"))
	require.False(t, verifyPassword(user, "initial-secret"))
	require.Equal(t, "Beta Org Renamed", reloaded.organizations["beta-org"].Name)
}
