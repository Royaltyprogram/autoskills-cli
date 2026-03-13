package service

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Royaltyprogram/aiops/configs"
)

func TestAnalyticsStoreExportImportRoundTrip(t *testing.T) {
	sourceConf := &configs.Config{}
	sourceConf.App.Mode = "prod"
	sourceConf.App.StorePath = filepath.Join(t.TempDir(), "source-store.json")
	sourceConf.Auth.BootstrapUsers = []configs.BootstrapUser{{
		ID:       "beta-user-1",
		OrgID:    "beta-org",
		OrgName:  "Beta Org",
		Email:    "beta1@example.com",
		Name:     "Beta Operator",
		Role:     "member",
		Password: "beta-secret",
	}}

	sourceStore, err := NewAnalyticsStore(sourceConf)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, sourceStore.Close())
	}()

	now := time.Now().UTC().Round(time.Second)

	var tokenValue string
	sourceStore.mu.Lock()
	sourceStore.projects["project_1"] = &Project{
		ID:             "project_1",
		OrgID:          "beta-org",
		AgentID:        "agent_1",
		Name:           "Shared workspace",
		DefaultTool:    "codex",
		LastIngestedAt: &now,
		ConnectedAt:    now,
	}
	sourceStore.sessionSummaries["project_1"] = []*SessionSummary{{
		ID:        "session_1",
		ProjectID: "project_1",
		Tool:      "codex",
		TokenIn:   120,
		TokenOut:  30,
		RawQueries: []string{
			"Summarize the current runtime store before editing it.",
		},
		Timestamp: now,
	}}
	tokenValue, _, err = sourceStore.issueAccessTokenLocked(TokenKindCLI, "beta-org", "beta-user-1", "beta cli", defaultCLITokenTTL, now)
	require.NoError(t, err)
	require.NoError(t, sourceStore.persistLocked())
	sourceStore.mu.Unlock()

	backup, err := sourceStore.ExportStateJSON()
	require.NoError(t, err)
	require.Contains(t, string(backup), "\"project_1\"")
	require.Contains(t, string(backup), "\"beta-user-1\"")

	targetConf := &configs.Config{}
	targetConf.App.Mode = "prod"
	targetConf.App.StorePath = filepath.Join(t.TempDir(), "target-store.json")
	targetConf.Auth.BootstrapUsers = append([]configs.BootstrapUser(nil), sourceConf.Auth.BootstrapUsers...)

	targetStore, err := NewAnalyticsStore(targetConf)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, targetStore.Close())
	}()

	require.NoError(t, targetStore.ImportStateJSON(backup))

	identity, ok := targetStore.ValidateAccessToken(tokenValue)
	require.True(t, ok)
	require.Equal(t, "beta-user-1", identity.UserID)

	targetStore.mu.RLock()
	defer targetStore.mu.RUnlock()
	require.Contains(t, targetStore.projects, "project_1")
	require.Len(t, targetStore.sessionSummaries["project_1"], 1)
	require.Equal(t, "session_1", targetStore.sessionSummaries["project_1"][0].ID)
	require.Equal(t, userSourceBootstrap, targetStore.users["beta-user-1"].Source)
}
