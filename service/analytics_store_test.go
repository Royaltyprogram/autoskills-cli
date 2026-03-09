package service

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/liushuangls/go-server-template/configs"
)

func TestAnalyticsStorePersistenceRoundTrip(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	now := time.Now().UTC().Round(time.Second)

	store.mu.Lock()
	store.seq = 12
	store.organizations["demo-org"] = &Organization{ID: "demo-org", Name: "Demo Org"}
	store.projects["project_1"] = &Project{
		ID:             "project_1",
		OrgID:          "demo-org",
		Name:           "demo",
		DefaultTool:    "codex",
		LastIngestedAt: &now,
	}
	store.sessionSummaries["project_1"] = []*SessionSummary{
		{
			ID:        "session_1",
			ProjectID: "project_1",
			Tool:      "codex",
			TokenIn:   480,
			TokenOut:  120,
			RawQueries: []string{
				"Inspect the controller before editing it.",
			},
			Timestamp: now,
		},
	}
	require.NoError(t, store.persistLocked())
	store.mu.Unlock()

	loaded, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	loaded.mu.RLock()
	defer loaded.mu.RUnlock()

	require.Equal(t, uint64(12), loaded.seq)
	require.Contains(t, loaded.organizations, "demo-org")
	require.Contains(t, loaded.projects, "project_1")
	require.Len(t, loaded.sessionSummaries["project_1"], 1)
	require.Equal(t, "session_1", loaded.sessionSummaries["project_1"][0].ID)
	require.NotNil(t, loaded.projects["project_1"].LastIngestedAt)
}

func TestAnalyticsStoreCollapsesProjectsIntoSharedWorkspaceOnLoad(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	older := time.Now().UTC().Add(-time.Hour).Round(time.Second)
	newer := time.Now().UTC().Round(time.Second)

	store.mu.Lock()
	store.organizations["org-1"] = &Organization{ID: "org-1", Name: "Org 1"}
	store.projects["project_1"] = &Project{
		ID:          "project_1",
		OrgID:       "org-1",
		Name:        "repo-a",
		ConnectedAt: older,
	}
	store.projects["project_2"] = &Project{
		ID:          "project_2",
		OrgID:       "org-1",
		Name:        "repo-b",
		ConnectedAt: newer,
	}
	store.sessionSummaries["project_1"] = []*SessionSummary{{
		ID:        "session_old",
		ProjectID: "project_1",
		Tool:      "codex",
		Timestamp: older,
	}}
	store.sessionSummaries["project_2"] = []*SessionSummary{{
		ID:        "session_new",
		ProjectID: "project_2",
		Tool:      "codex",
		Timestamp: newer,
	}}
	require.NoError(t, store.persistLocked())
	store.mu.Unlock()

	loaded, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	loaded.mu.RLock()
	defer loaded.mu.RUnlock()

	require.Len(t, loaded.projects, 1)
	workspace := loaded.projects["project_2"]
	require.NotNil(t, workspace)
	require.Equal(t, "Shared workspace", workspace.Name)
	require.Len(t, loaded.sessionSummaries["project_2"], 2)
	require.Empty(t, loaded.sessionSummaries["project_1"])
}
